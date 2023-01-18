package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hori-ryota/zaperr"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	"undercast-bot/mediary"
	jobsqueue "undercast-bot/service/jobs_queue"
)

//go:generate moq -out servicemocks/s3.go -pkg servicemocks -rm . S3Store:MockS3Store
type S3Store interface {
	PreSignedURL(key string) (string, error)
	Put(ctx context.Context, key string, dataReader io.Reader, opts ...func(*PutOptions)) error
}

type Service struct {
	logger         *zap.Logger
	s3Store        S3Store
	mediaSvc       mediary.Service
	repository     *Repository
	jobsQueue      *jobsqueue.RedisJobQueue
	userPathSecret string

	episodeStatusChangesChan chan []EpisodeStatusChange
}

type Metadata = mediary.Metadata

type Episode struct {
	ID              string
	Title           string
	PubDate         time.Time
	UserID          string
	SourceURL       string
	SourceFilepaths []string
	MediaryID       string
	URL             string
	Status          EpisodeStatus
	Duration        time.Duration
	FileLenBytes    int64
	Format          string
	FeedIDs         []string
}

type EpisodeStatus string

const (
	EpisodeStatusUndefined   EpisodeStatus = "undefined"
	EpisodeStatusCreated     EpisodeStatus = "created"
	EpisodeStatusPending     EpisodeStatus = "pending"
	EpisodeStatusDownloading EpisodeStatus = "downloading"
	EpisodeStatusProcessing  EpisodeStatus = "processing"
	EpisodeStatusUploading   EpisodeStatus = "uploading"
	EpisodeStatusComplete    EpisodeStatus = "complete"
)

type Feed struct {
	ID         string
	UserID     string
	Title      string
	URL        string
	EpisodeIDs []string
}

var (
	metadataDelays = []time.Duration{
		1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second,
		40 * time.Second, 60 * time.Second, 120 * time.Second, 240 * time.Second,
	}
	ErrFeedNotFound    = fmt.Errorf("feed not found")
	ErrEpisodeNotFound = fmt.Errorf("episode not found")
)

func New(mediaSvc mediary.Service, repository *Repository, s3Store S3Store, jobsQueue *jobsqueue.RedisJobQueue, userPathSecret string, logger *zap.Logger) *Service {
	return &Service{
		logger:                   logger,
		s3Store:                  s3Store,
		mediaSvc:                 mediaSvc,
		repository:               repository,
		jobsQueue:                jobsQueue,
		episodeStatusChangesChan: make(chan []EpisodeStatusChange, 1),
		userPathSecret:           userPathSecret,
	}
}

type EpisodeStatusChange struct {
	Episode   *Episode
	OldStatus EpisodeStatus
	NewStatus EpisodeStatus
}

func (svc *Service) Start(ctx context.Context) chan []EpisodeStatusChange {
	svc.jobsQueue.Subscribe(ctx, createEpisodes, func(payload []byte) error {
		return svc.onCreateEpisodesQueueEvent(ctx, payload)
	})
	svc.jobsQueue.Subscribe(ctx, pollEpisodesStatus, func(payload []byte) error {
		return svc.onPollEpisodesQueueEvent(ctx, payload)
	})
	svc.jobsQueue.Subscribe(ctx, regenerateFeed, func(payload []byte) error {
		return svc.onRegenerateFeedQueueEvent(ctx, payload)
	})
	return svc.episodeStatusChangesChan
}

func (svc *Service) FetchMetadata(ctx context.Context, mediaURL string) (*Metadata, error) {
	return retry(ctx, func() (*Metadata, error) {
		return svc.mediaSvc.FetchMetadataLongPolling(ctx, mediaURL)
	}, metadataDelays...)
}

func (svc *Service) CreateEpisodesAsync(ctx context.Context, url string, filepaths [][]string, userID string) error {
	zapFields := []zap.Field{
		zap.String("url", url),
		zap.Any("filepaths", filepaths),
		zap.String("userID", userID),
	}

	svc.logger.Info("queueing episodes creation", zapFields...)

	if err := svc.jobsQueue.Publish(ctx, createEpisodes, &CreateEpisodesQueuePayload{
		URL:    url,
		Paths:  filepaths,
		UserID: userID,
	}); err != nil {
		return zaperr.Wrap(err, "failed to enqueue episodes creation", zapFields...)
	}

	return nil
}

func (svc *Service) CreateEpisode(ctx context.Context, mediaURL string, filepaths []string, userID string) (*Episode, error) {
	filename := uuid.New().String() + ".mp3" // TODO: implement more elaborate filename generation

	zapFields := []zap.Field{
		zap.String("mediaURL", mediaURL),
		zap.Strings("filepaths", filepaths),
		zap.String("filename", filename),
		zap.String("userID", userID),
	}

	episodePath := path.Join(svc.getUserKeyPrefix(userID), "episodes", filename)
	presignURL, err := svc.s3Store.PreSignedURL(episodePath)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to get presigned url", zapFields...)
	}

	mediaryID, err := svc.mediaSvc.CreateUploadJob(ctx, &mediary.CreateUploadJobParams{
		URL:  mediaURL,
		Type: mediary.JobTypeConcatenate,
		Params: mediary.ConcatenateJobParams{
			Filepaths: filepaths,
			UploadURL: presignURL,
		},
	})
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to create mediary job", zapFields...)
	}

	episodeTitle := titleFromFilepaths(filepaths)
	if episodeTitle == "" {
		episodeTitle = titleFromSourceURL(mediaURL)
	} else {
		episodeTitle = fmt.Sprintf("%s - %s", episodeTitle, titleFromSourceURL(mediaURL))
	}

	ep := &Episode{
		Title:           episodeTitle,
		UserID:          userID,
		SourceURL:       mediaURL,
		SourceFilepaths: filepaths,
		URL:             stripQuery(presignURL),
		MediaryID:       mediaryID,
		PubDate:         time.Now(),
		Duration:        0,     // should be populated later when job is complete
		FileLenBytes:    0,     // should be populated later when job is complete
		Format:          "mp3", // FIXME: hardcoded
	}

	ep, err = svc.repository.SaveEpisode(ctx, ep)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to save episode", zapFields...)
	}

	return ep, nil
}

func (svc *Service) IsValidURL(ctx context.Context, mediaURL string) (bool, error) {
	if isValid, err := svc.mediaSvc.IsValidURL(ctx, mediaURL); err == nil {
		return isValid, err
	} else {
		return false, zaperr.Wrap(err, "failed to check if url is valid", zap.String("url", mediaURL))
	}
}

func (svc *Service) ListEpisodes(ctx context.Context, userID string) ([]*Episode, error) {
	if episodes, err := svc.repository.ListUserEpisodes(ctx, userID); err == nil {
		return episodes, nil
	} else {
		return nil, zaperr.Wrap(err, "failed to list user episodes", zap.String("userID", userID))
	}
}

func (svc *Service) GetEpisodesMap(ctx context.Context, ids []string, userID string) (map[string]*Episode, error) {
	if episodes, err := svc.repository.GetEpisodesMap(ctx, ids, userID); err == nil {
		return episodes, nil
	} else {
		return nil, zaperr.Wrap(ErrEpisodeNotFound, "failed to get episodes map", zap.Strings("ids", ids), zap.Error(err))
	}
}

func (svc *Service) ListFeeds(ctx context.Context, userID string) ([]*Feed, error) {
	zapFields := []zap.Field{
		zap.String("username", userID),
	}

	feeds, err := svc.repository.ListUserFeeds(ctx, userID)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to list user feeds", zapFields...)
	}

	for _, f := range feeds {
		if f.ID == strconv.Itoa(defaultFeedID) {
			return feeds, nil // if default feed is present, we're all set
		}
	}

	// create default feed if it doesn't exist
	defaultFeed, err := svc.DefaultFeed(ctx, userID)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to get default feed", zapFields...)
	}

	feeds = append([]*Feed{defaultFeed}, feeds...)
	return feeds, nil
}

func (svc *Service) DefaultFeed(ctx context.Context, userID string) (*Feed, error) {
	defaultFeedID := "1"

	existing, err := svc.repository.GetFeed(ctx, defaultFeedID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get default feed: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	created, err := svc.createFeed(ctx, userID, "Default Undercast Feed", defaultFeedID)
	if err != nil {
		return nil, fmt.Errorf("failed to create default feed: %w", err)
	}

	return created, nil
}

func (svc *Service) CreateFeed(ctx context.Context, userID string, title string) (*Feed, error) {
	return svc.createFeed(ctx, userID, title, "")
}

func (svc *Service) PublishEpisodes(ctx context.Context, episodeIDs []string, feedID, userID string) error {
	zapFields := []zap.Field{
		zap.Strings("episodeIDs", episodeIDs),
		zap.String("feedID", feedID),
		zap.String("userID", userID),
	}
	feed, err := svc.repository.GetFeed(ctx, feedID, userID)
	if err != nil {
		return zaperr.Wrap(err, "failed to publish episode", zapFields...)
	}
	if feed == nil {
		return zaperr.Wrap(ErrFeedNotFound, "feed not found", zapFields...)
	}

	// FIXME: non-transactional persistence
	for _, epID := range episodeIDs {
		if slices.Contains(feed.EpisodeIDs, epID) {
			continue
		}
		episode, err := svc.repository.GetEpisode(ctx, epID, userID)
		if err != nil {
			return zaperr.Wrap(err, "failed to publish episode", zapFields...)
		}

		episode.FeedIDs = append(episode.FeedIDs, feedID)
		if _, err = svc.repository.SaveEpisode(ctx, episode); err != nil {
			return zaperr.Wrap(err, "failed to update episode feed ids", zapFields...)
		}

		feed.EpisodeIDs = append(feed.EpisodeIDs, epID)
	}

	if _, err = svc.repository.SaveFeed(ctx, feed); err != nil {
		return zaperr.Wrap(err, "failed to update feed episode ids", zapFields...)
	}

	if err = svc.jobsQueue.Publish(ctx, regenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: []string{feedID},
	}); err != nil {
		return zaperr.Wrap(err, "failed to publish regenerate feed job", zapFields...)
	}

	return nil
}

func (svc *Service) UnpublishEpisodes(ctx context.Context, episodeIDs []string, feedID, userID string) error {
	zapFields := []zap.Field{
		zap.Strings("episodeIDs", episodeIDs),
		zap.String("feedID", feedID),
		zap.String("userID", userID),
	}
	feed, err := svc.repository.GetFeed(ctx, feedID, userID)
	if err != nil {
		return zaperr.Wrap(ErrFeedNotFound, "unpublish failed to find feed", zapFields...)
	}

	// FIXME: non-transactional persistence
	for _, id := range episodeIDs {
		if !slices.Contains(feed.EpisodeIDs, id) {
			continue
		}
		episode, err := svc.repository.GetEpisode(ctx, id, userID)
		if err != nil {
			return fmt.Errorf("failed to publish episode: %w", err)
		}

		episode.FeedIDs = remove(episode.FeedIDs, feedID)
		if _, err = svc.repository.SaveEpisode(ctx, episode); err != nil {
			return fmt.Errorf("failed to update episode feed ids: %w", err)
		}

		feed.EpisodeIDs = remove(feed.EpisodeIDs, id)
	}

	if _, err = svc.repository.SaveFeed(ctx, feed); err != nil {
		return fmt.Errorf("failed to update feed episode ids: %w", err)
	}

	if err = svc.jobsQueue.Publish(ctx, regenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: []string{feedID},
	}); err != nil {
		return zaperr.Wrap(err, "failed to publish regenerate feed job", zapFields...)
	}

	return nil
}

func (svc *Service) RenameEpisodes(ctx context.Context, epIDs []string, newTitlePattern string, userID string) error {
	zapFields := []zap.Field{
		zap.Strings("episodeIDs", epIDs),
		zap.String("newTitlePattern", newTitlePattern),
		zap.String("userID", userID),
	}

	episodesMap, err := svc.repository.GetEpisodesMap(ctx, epIDs, userID)
	if err != nil {
		return zaperr.Wrap(err, "failed to get episodes", zapFields...)
	}

	feedsToUpdate := map[string]bool{}
	for _, ep := range episodesMap {
		newTitle := getUpdatedEpisodeTitle(ep.Title, newTitlePattern)
		if newTitle != ep.Title {
			ep.Title = newTitle
			if _, err := svc.repository.SaveEpisode(ctx, ep); err != nil { // TODO: batch save
				return zaperr.Wrap(err, "failed to save episode", zapFields...)
			}
			for _, feedID := range ep.FeedIDs {
				feedsToUpdate[feedID] = true
			}
		}
	}

	if len(feedsToUpdate) > 0 {
		if err = svc.jobsQueue.Publish(ctx, regenerateFeed, RegenerateFeedQueuePayload{
			UserID:  userID,
			FeedIDs: maps.Keys(feedsToUpdate),
		}); err != nil {
			return zaperr.Wrap(err, "failed to publish regenerate feed job", zapFields...)
		}
	}

	return nil
}

func (svc *Service) DeleteEpisodes(ctx context.Context, epIDs []string, userID string) error {
	zapFields := []zap.Field{
		zap.Strings("episodeIDs", epIDs),
		zap.String("userID", userID),
	}

	episodesMap, err := svc.GetEpisodesMap(ctx, epIDs, userID)
	if err != nil {
		return err
	}

	feedIDsMap := make(map[string]bool, len(episodesMap)*2)
	for _, ep := range episodesMap {
		for _, feedID := range ep.FeedIDs {
			feedIDsMap[feedID] = true
		}
	}
	feedIDs := maps.Keys(feedIDsMap)

	zapFields = append(zapFields, zap.Strings("feedIDs", feedIDs))

	feedsMap, err := svc.repository.GetFeedsMap(ctx, feedIDs, userID)
	if err != nil {
		return zaperr.Wrap(err, "failed to get feeds map", zapFields...)
	}

	for _, f := range feedsMap {
		f.EpisodeIDs = remove(f.EpisodeIDs, epIDs...)
		if _, err := svc.repository.SaveFeed(ctx, f); err != nil { // TODO: batch save
			return zaperr.Wrap(err, "failed to save feed", zapFields...)
		}
	}

	if err := svc.repository.DeleteEpisodes(ctx, epIDs, userID); err != nil {
		return zaperr.Wrap(err, "failed to delete episodes", zapFields...)
	}

	// TODO: delete episodes from s3

	return nil
}

func (svc *Service) createFeed(ctx context.Context, userID string, title string, feedID string) (*Feed, error) {
	feedPath := path.Join(svc.getUserKeyPrefix(userID), "feeds", feedID)
	presignURL, err := svc.s3Store.PreSignedURL(feedPath)
	if err != nil {
		return nil, fmt.Errorf("CreateFeed failed to get presigned url: %w", err)
	}

	feed := &Feed{
		ID:     feedID, // feedID can be empty, in which case it will be generated by the repository
		Title:  title,
		UserID: userID,
		URL:    stripQuery(presignURL),
	}
	if feed, err = svc.repository.SaveFeed(ctx, feed); err != nil {
		return nil, fmt.Errorf("failed to save default feed: %w", err)
	}
	return feed, nil
}

func (svc *Service) onCreateEpisodesQueueEvent(ctx context.Context, payloadBytes []byte) error {
	var payload CreateEpisodesQueuePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return zaperr.Wrap(err, "failed to unmarshal payload", zap.String("payload", string(payloadBytes)))
	}

	zapFields := []zap.Field{
		zap.String("url", payload.URL),
		zap.Any("filepaths", payload.Paths),
	}

	svc.logger.Info("creating queued episodes", zapFields...)

	var createdEpisodes []*Episode
	for _, ePaths := range payload.Paths {
		episode, err := svc.CreateEpisode(ctx, payload.URL, ePaths, payload.UserID)
		if err != nil {
			return zaperr.Wrap(err, "failed to create single file episode", zapFields...)
		}
		createdEpisodes = append(createdEpisodes, episode)
	}

	episodeIDs := make([]string, len(createdEpisodes))
	for i, e := range createdEpisodes {
		episodeIDs[i] = e.ID
	}

	if err := svc.jobsQueue.Publish(ctx, pollEpisodesStatus, &PollEpisodesStatusQueuePayload{
		EpisodeIDs: episodeIDs,
		UserID:     payload.UserID,
	}); err != nil {
		svc.logger.Error("failed to enqueue episode status polling", append([]zap.Field{zap.Strings("episodeIDs", episodeIDs)}, zapFields...)...)
	}

	episodesStatusChanges := make([]EpisodeStatusChange, len(createdEpisodes))
	for i, e := range createdEpisodes {
		episodesStatusChanges[i] = EpisodeStatusChange{
			Episode:   e,
			OldStatus: EpisodeStatusUndefined,
			NewStatus: EpisodeStatusCreated,
		}
	}
	svc.episodeStatusChangesChan <- episodesStatusChanges

	return nil
}

func (svc *Service) onPollEpisodesQueueEvent(ctx context.Context, payloadBytes []byte) error {
	var payload PollEpisodesStatusQueuePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return zaperr.Wrap(err, "failed to unmarshal payload", zap.String("payload", string(payloadBytes)))
	}

	zapFields := []zap.Field{
		zap.Strings("episodeIDs", payload.EpisodeIDs),
		zap.String("userID", payload.UserID),
		zap.Any("pollAfter", payload.PollAfter),
	}

	if payload.PollAfter != nil {
		sleepDuration := time.Until(*payload.PollAfter)
		if sleepDuration > 0 {
			svc.logger.Debug("sleeping before polling episodes", append([]zap.Field{
				zap.Duration("sleepDuration", sleepDuration),
			}, zapFields...)...)
			select {
			case <-time.After(sleepDuration):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	svc.logger.Info("polling episode status", zapFields...)

	episodesMap, err := svc.repository.GetEpisodesMap(ctx, payload.EpisodeIDs, payload.UserID)
	if err != nil {
		return zaperr.Wrap(err, "failed to get episodes", zapFields...)
	}

	mediaryIDs := make([]string, 0, len(episodesMap))
	for _, e := range episodesMap {
		if e.Status == EpisodeStatusComplete {
			continue
		}
		if e.MediaryID == "" {
			continue
		}
		mediaryIDs = append(mediaryIDs, e.MediaryID)
	}

	jobStatusMap, err := svc.mediaSvc.FetchJobStatusMap(ctx, mediaryIDs)
	if err != nil {
		return zaperr.Wrap(err, "failed to fetch job status", append([]zap.Field{zap.Strings("mediaryIDs", mediaryIDs)}, zapFields...)...)
	}

	episodesStateChanges := make([]EpisodeStatusChange, 0, len(episodesMap))
	episodesToSave := make([]*Episode, 0, len(episodesMap))
	episodeIDsToRequeue := make([]string, 0, len(episodesMap))
	for _, ep := range episodesMap {
		zapFields := append([]zap.Field{zap.String("episodeID", ep.ID), zap.String("mediaryID", ep.MediaryID)}, zapFields...)
		jstat, exists := jobStatusMap[ep.MediaryID]
		if !exists {
			svc.logger.Warn("mediary job status not found", zapFields...)
			episodeIDsToRequeue = append(episodeIDsToRequeue, ep.ID)
			continue
		}

		newStatus, err := jobStatusToEpisodeStatus(jstat.Status)
		if err != nil {
			return zaperr.Wrap(err, "failed to convert job status to episode status",
				append([]zap.Field{zap.String("jobStatus", string(jstat.Status))}, zapFields...)...,
			)
		}

		if newStatus != EpisodeStatusComplete {
			episodeIDsToRequeue = append(episodeIDsToRequeue, ep.ID)
		}

		if newStatus == ep.Status {
			continue
		}

		episodesStateChanges = append(episodesStateChanges, EpisodeStatusChange{
			Episode:   ep,
			OldStatus: ep.Status,
			NewStatus: newStatus,
		})

		ep.Status = newStatus
		switch newStatus {
		case EpisodeStatusUploading, EpisodeStatusComplete:
			ep.FileLenBytes = jstat.ResultFileBytes
			ep.Duration = jstat.ResultMediaDuration
		}
		episodesToSave = append(episodesToSave, ep)
	}

	var episodesSaveError error
	feedsToPublish := make(map[string]bool)
	for _, e := range episodesToSave {
		zapFields := append([]zap.Field{zap.String("episodeID", e.ID)}, zapFields...)
		if _, err := svc.repository.SaveEpisode(ctx, e); err == nil {
			for _, f := range e.FeedIDs {
				feedsToPublish[f] = true
			}
		} else {
			episodesSaveError = multierr.Append(episodesSaveError, zaperr.Wrap(err, "failed to save episode", zapFields...))
		}
	}

	feedIDs := make([]string, 0, len(feedsToPublish))
	for f := range feedsToPublish {
		feedIDs = append(feedIDs, f)
	}
	if len(feedIDs) > 0 {
		if err := svc.jobsQueue.Publish(ctx, regenerateFeed, &RegenerateFeedQueuePayload{
			FeedIDs: feedIDs,
			UserID:  payload.UserID,
		}); err != nil {
			// TODO: failure here will leave data in inconsistent state: episodes will be saved but feeds will not be regenerated
			return zaperr.Wrap(err, "failed to enqueue feed regeneration", append([]zap.Field{zap.Strings("feedIDs", feedIDs)}, zapFields...)...)
		}
	}

	if len(episodesStateChanges) > 0 {
		svc.episodeStatusChangesChan <- episodesStateChanges
	}

	if len(episodeIDsToRequeue) > 0 {
		pollAfter := time.Now().Add(10 * time.Second)
		if err := svc.jobsQueue.Publish(ctx, pollEpisodesStatus, &PollEpisodesStatusQueuePayload{
			EpisodeIDs: episodeIDsToRequeue,
			UserID:     payload.UserID,
			PollAfter:  &pollAfter,
		}); err != nil {
			return zaperr.Wrap(err, "failed to enqueue episode status polling", append([]zap.Field{zap.Strings("episodeIDs", episodeIDsToRequeue)}, zapFields...)...)
		}
	}

	return nil
}

func (svc *Service) onRegenerateFeedQueueEvent(ctx context.Context, payloadBytes []byte) error {
	var payload RegenerateFeedQueuePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return zaperr.Wrap(err, "failed to unmarshal payload", zap.String("payload", string(payloadBytes)))
	}

	zapFields := []zap.Field{
		zap.Strings("feedIDs", payload.FeedIDs),
	}

	svc.logger.Info("regenerating feeds", zapFields...)

	feedsMap, err := svc.repository.GetFeedsMap(ctx, payload.FeedIDs, payload.UserID)
	if err != nil {
		return zaperr.Wrap(err, "failed to get feeds", zapFields...)
	}

	for _, f := range feedsMap {
		if err := svc.regenerateFeedFile(ctx, f); err != nil {
			return zaperr.Wrap(err, "failed to regenerate feed", append([]zap.Field{zap.String("feedID", f.ID)}, zapFields...)...)
		}
	}

	return nil
}

func (svc *Service) regenerateFeedFile(ctx context.Context, feed *Feed) error {
	zapFields := []zap.Field{
		zap.String("feedID", feed.ID),
		zap.String("userID", feed.UserID),
	}

	episodes, err := svc.repository.ListFeedEpisodes(ctx, feed)
	if err != nil {
		return zaperr.Wrap(err, "failed to list feed episodes", zapFields...)
	}

	var episodesMap = make(map[string]*Episode)
	for _, ep := range episodes {
		episodesMap[ep.ID] = ep
	}

	feedReader, err := generateFeed(feed, episodesMap)
	if err != nil {
		return zaperr.Wrap(err, "failed to generate feed", zapFields...)
	}

	parsed, err := url.Parse(feed.URL)
	if err != nil {
		return zaperr.Wrap(err, "failed to parse feed url", zapFields...)
	}
	objectKey := strings.TrimPrefix(parsed.Path, "/")

	if err := svc.s3Store.Put(ctx, objectKey, feedReader, WithContentType("text/xml; charset=utf-8")); err != nil {
		zapFields = append(zapFields, zap.Error(err))
		return zaperr.Wrap(err, "failed to upload feed", zapFields...)
	}

	return nil
}

func (svc *Service) getUserKeyPrefix(id string) string {
	hash := sha256.Sum256([]byte(svc.userPathSecret + id))
	return hex.EncodeToString(hash[:])
}

func jobStatusToEpisodeStatus(status mediary.JobStatusName) (EpisodeStatus, error) {
	switch status {
	case mediary.JobStatusAccepted:
		return EpisodeStatusPending, nil
	case mediary.JobStatusDownloading:
		return EpisodeStatusDownloading, nil
	case mediary.JobStatusProcessing:
		return EpisodeStatusProcessing, nil
	case mediary.JobStatusUploading:
		return EpisodeStatusUploading, nil
	case mediary.JobStatusComplete:
		return EpisodeStatusComplete, nil
	}
	return "", zaperr.New("unknown job status", zap.String("status", string(status)))
}

func remove(s []string, removed ...string) []string {
	var result []string
	for _, v := range s {
		if !slices.Contains(removed, v) {
			result = append(result, v)
		}
	}
	return result
}

func stripQuery(url string) string {
	if i := strings.Index(url, "?"); i != -1 {
		return url[:i]
	}
	return url
}

func retry[T any](ctx context.Context, fn func() (*T, error), durations ...time.Duration) (*T, error) {
	var lastErr error
	for _, dur := range durations {
		if t, err := fn(); err == nil {
			return t, nil
		} else {
			lastErr = err
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(dur):
				continue
			}
		}
	}
	return nil, lastErr
}
