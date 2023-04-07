package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"tg-podcastotron/mediary"
	jobsqueue "tg-podcastotron/service/jobs_queue"
)

//go:generate moq -out servicemocks/s3.go -pkg servicemocks -rm . S3Store:MockS3Store
type S3Store interface {
	PreSignedURL(key string) (string, error)
	Put(ctx context.Context, key string, dataReader io.ReadSeeker, opts ...func(*PutOptions)) error
	Delete(ctx context.Context, key string) error
}

type Service struct {
	logger       *zap.Logger
	s3Store      S3Store
	mediaSvc     mediary.Service
	repository   *Repository
	jobsQueue    *jobsqueue.RJQ
	obfuscateIDs func(string) string

	episodeStatusChangesChan chan []EpisodeStatusChange
	defaultFeedTitle         string
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
	StorageKey      string
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
	ErrFeedNotFound            = fmt.Errorf("feed not found")
	ErrEpisodeNotFound         = fmt.Errorf("episode not found")
	ErrNotImplemented          = fmt.Errorf("not implemented")
	ErrCannotDeleteDefaultFeed = fmt.Errorf("cannot delete default feed")
)

const maxPollEpisodesRequeueCount = 100

func New(
	mediaSvc mediary.Service,
	repository *Repository,
	s3Store S3Store,
	jobsQueue *jobsqueue.RJQ,
	defaultFeedTitle string,
	obfuscateIDs func(string) string,
	logger *zap.Logger,
) *Service {
	if defaultFeedTitle == "" {
		defaultFeedTitle = "Podcastotron"
	}
	return &Service{
		logger:                   logger,
		s3Store:                  s3Store,
		mediaSvc:                 mediaSvc,
		repository:               repository,
		jobsQueue:                jobsQueue,
		episodeStatusChangesChan: make(chan []EpisodeStatusChange, 1),
		obfuscateIDs:             obfuscateIDs,
		defaultFeedTitle:         defaultFeedTitle,
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
	svc.jobsQueue.Run() // MUST be called after all subscriptions
	return svc.episodeStatusChangesChan
}

func (svc *Service) FetchMetadata(ctx context.Context, mediaURL string) (*Metadata, error) {
	return retry(ctx, func() (*Metadata, error) {
		return svc.mediaSvc.FetchMetadataLongPolling(ctx, mediaURL)
	}, metadataDelays...)
}

func (svc *Service) CreateEpisodesAsync(ctx context.Context, url string, variantsPerEpisode [][]string, userID string) error {
	zapFields := []zap.Field{
		zap.String("url", url),
		zap.Any("variants_per_episode", variantsPerEpisode),
		zap.String("user_id", userID),
	}

	svc.logger.Info("queueing episodes creation", zapFields...)

	if err := svc.jobsQueue.Publish(ctx, createEpisodes, &CreateEpisodesQueuePayload{
		URL:                url,
		VariantsPerEpisode: variantsPerEpisode,
		UserID:             userID,
	}); err != nil {
		return zaperr.Wrap(err, "failed to enqueue episodes creation", zapFields...)
	}

	return nil
}

func (svc *Service) CreateEpisode(ctx context.Context, mediaURL string, variants []string, userID string) (*Episode, error) {
	filename := uuid.New().String() + ".mp3" // TODO: implement more elaborate filename generation
	episodeKey := path.Join(svc.getUserKeyPrefix(userID), "episodes", filename)

	zapFields := []zap.Field{
		zap.String("media_url", mediaURL),
		zap.Strings("variants", variants),
		zap.String("filename", filename),
		zap.String("user_id", userID),
		zap.String("episode_key", episodeKey),
	}

	presignURL, err := svc.s3Store.PreSignedURL(episodeKey)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to get presigned url", zapFields...)
	}

	mediaryID, err := svc.mediaSvc.CreateUploadJob(ctx, &mediary.CreateUploadJobParams{
		URL:  mediaURL,
		Type: mediary.JobTypeConcatenate,
		Params: mediary.ConcatenateJobParams{
			Variants:  variants,
			UploadURL: presignURL,
		},
	})
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to create mediary job", zapFields...)
	}

	episodeTitle := titleFromFilepaths(variants)
	if episodeTitle == "" {
		episodeTitle = titleFromSourceURL(mediaURL)
	} else {
		episodeTitle = fmt.Sprintf("%s - %s", episodeTitle, titleFromSourceURL(mediaURL))
	}

	ep := &Episode{
		Title:           episodeTitle,
		UserID:          userID,
		SourceURL:       mediaURL,
		SourceFilepaths: variants,
		StorageKey:      episodeKey,
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
		return nil, zaperr.Wrap(err, "failed to list user episodes", zap.String("user_id", userID))
	}
}

func (svc *Service) GetEpisodesMap(ctx context.Context, ids []string, userID string) (map[string]*Episode, error) {
	if episodes, err := svc.repository.GetEpisodesMap(ctx, ids, userID); err == nil {
		return episodes, nil
	} else {
		return nil, zaperr.Wrap(ErrEpisodeNotFound, "failed to get episodes map", zap.Strings("ids", ids), zaperr.ToField(err))
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
		if f.ID == strconv.Itoa(DefaultFeedID) {
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

func (svc *Service) GetFeedsMap(ctx context.Context, feedIDs []string, userID string) (map[string]*Feed, error) {
	feeds, err := svc.repository.GetFeedsMap(ctx, feedIDs, userID)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to get feeds map", zap.Strings("feed_ids", feedIDs))
	}
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

	created, err := svc.createFeed(ctx, userID, svc.defaultFeedTitle, defaultFeedID)
	if err != nil {
		return nil, fmt.Errorf("failed to create default feed: %w", err)
	}

	return created, nil
}

func (svc *Service) CreateFeed(ctx context.Context, userID string, title string) (*Feed, error) {
	return svc.createFeed(ctx, userID, title, "")
}

func (svc *Service) PublishEpisodes(ctx context.Context, episodeIDs []string, feedIDs []string, userID string) error {
	zapFields := []zap.Field{
		zap.Strings("episode_ids", episodeIDs),
		zap.Strings("feed_ids", feedIDs),
		zap.String("user_id", userID),
	}

	episodes := make([]*Episode, 0, len(episodeIDs))
	episodesMap, err := svc.repository.GetEpisodesMap(ctx, episodeIDs, userID)
	if err != nil {
		return zaperr.Wrap(err, "failed to set episodes feeds", zapFields...)
	}
	for _, epID := range episodeIDs {
		episodes = append(episodes, episodesMap[epID])
	}

	desiredFeeds := make([]*Feed, 0, len(feedIDs))
	desiredFeedsMap, err := svc.repository.GetFeedsMap(ctx, feedIDs, userID)
	if err != nil {
		return zaperr.Wrap(err, "failed to set episodes feeds", zapFields...)
	}
	for _, feedID := range feedIDs {
		desiredFeeds = append(desiredFeeds, desiredFeedsMap[feedID])
	}

	existingFeedIDs := make(map[string]struct{}, len(episodes)*2)
	for _, ep := range episodes {
		for _, feedID := range ep.FeedIDs {
			existingFeedIDs[feedID] = struct{}{}
		}
	}
	existingFeedsMap, err := svc.repository.GetFeedsMap(ctx, maps.Keys(existingFeedIDs), userID)

	changedEpisodes := make(map[*Episode]struct{}, len(episodes))
	changedFeeds := make(map[*Feed]struct{}, len(desiredFeeds))

	for _, ep := range episodes {
		// region unpublish
		for _, existingFeedID := range ep.FeedIDs {
			if _, ok := desiredFeedsMap[existingFeedID]; !ok {
				// remove episode from feed
				existingFeed := existingFeedsMap[existingFeedID]
				existingFeed.EpisodeIDs = remove(existingFeed.EpisodeIDs, ep.ID)
				changedFeeds[existingFeed] = struct{}{}
				// remove feed from episode
				ep.FeedIDs = remove(ep.FeedIDs, existingFeedID)
				changedEpisodes[ep] = struct{}{}
			}
		}
		// endregion

		for _, desiredFeed := range desiredFeeds {
			if slices.Contains(ep.FeedIDs, desiredFeed.ID) {
				continue
			}
			// we know this feed will change,
			// but we'll do it later to preserve order
			changedFeeds[desiredFeed] = struct{}{}
			// add feed to episode
			ep.FeedIDs = append(ep.FeedIDs, desiredFeed.ID)
			changedEpisodes[ep] = struct{}{}
		}
	}

	// region publish
	for _, f := range desiredFeeds {
		for _, ep := range episodes {
			if !slices.Contains(f.EpisodeIDs, ep.ID) {
				f.EpisodeIDs = append(f.EpisodeIDs, ep.ID)
			}
		}
		changedFeeds[f] = struct{}{}
	}
	// endregion

	for ep, _ := range changedEpisodes {
		if _, err = svc.repository.SaveEpisode(ctx, ep); err != nil {
			return zaperr.Wrap(err, "failed to update episode feed ids", zapFields...)
		}
	}

	for feed, _ := range changedFeeds {
		if _, err = svc.repository.SaveFeed(ctx, feed); err != nil {
			return zaperr.Wrap(err, "failed to update feed episode ids", zapFields...)
		}
	}

	changedFeedIDs := make([]string, 0, len(changedFeeds))
	for feed, _ := range changedFeeds {
		changedFeedIDs = append(changedFeedIDs, feed.ID)
	}

	if err = svc.jobsQueue.Publish(ctx, regenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: changedFeedIDs,
	}); err != nil {
		return zaperr.Wrap(err, "failed to publish regenerate feed job", zapFields...)
	}

	return nil
}

func (svc *Service) RenameEpisodes(ctx context.Context, epIDs []string, newTitlePattern string, userID string) error {
	zapFields := []zap.Field{
		zap.Strings("episode_ids", epIDs),
		zap.String("new_title_pattern", newTitlePattern),
		zap.String("user_id", userID),
	}

	episodesMap, err := svc.repository.GetEpisodesMap(ctx, epIDs, userID)
	if err != nil {
		return zaperr.Wrap(err, "failed to get episodes", zapFields...)
	}

	feedsToUpdate := map[string]bool{}
	newTitleMap := getUpdatedEpisodeTitle(maps.Values(episodesMap), newTitlePattern)
	for _, ep := range episodesMap {
		newTitle := newTitleMap[ep.ID]
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
		zap.Strings("episode_ids", epIDs),
		zap.String("user_id", userID),
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

	zapFields = append(zapFields, zap.Strings("feed_ids", feedIDs))

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

	for _, ep := range episodesMap {
		svc.s3Store.Delete(ctx, svc.extractEpisodeS3Key(ep))
	}

	if err := svc.repository.DeleteEpisodes(ctx, epIDs, userID); err != nil {
		return zaperr.Wrap(err, "failed to delete episodes", zapFields...)
	}

	return nil
}

func (svc *Service) RenameFeed(ctx context.Context, feedID string, userID string, newTitle string) error {
	zapFields := []zap.Field{
		zap.String("feed_id", feedID),
		zap.String("user_id", userID),
		zap.String("new_title", newTitle),
	}

	feed, err := svc.repository.GetFeed(ctx, feedID, userID)
	if err != nil {
		zapFields := append(zapFields, zaperr.ToField(err))
		return zaperr.Wrap(ErrFeedNotFound, "", zapFields...)
	}

	feed.Title = newTitle
	if _, err := svc.repository.SaveFeed(ctx, feed); err != nil {
		return zaperr.Wrap(err, "failed to save feed", zapFields...)
	}

	if err = svc.jobsQueue.Publish(ctx, regenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: []string{feedID},
	}); err != nil {
		return zaperr.Wrap(err, "failed to publish regenerate feed job", zapFields...)
	}

	return nil
}

func (svc *Service) DeleteFeed(ctx context.Context, feedID string, userID string, deleteEpisodes bool) error {
	zapFields := []zap.Field{
		zap.String("feed_id", feedID),
		zap.String("user_id", userID),
		zap.Bool("delete_episodes", deleteEpisodes),
	}

	if feedID == strconv.Itoa(DefaultFeedID) {
		return zaperr.Wrap(ErrCannotDeleteDefaultFeed, "", zapFields...)
	}

	feed, err := svc.repository.GetFeed(ctx, feedID, userID)
	if err != nil || feed == nil {
		return zaperr.Wrap(err, "failed to find feed", zapFields...)
	}

	if deleteEpisodes {
		if err := svc.DeleteEpisodes(ctx, feed.EpisodeIDs, userID); err != nil {
			return zaperr.Wrap(err, "failed to delete episodes", zapFields...)
		}
	} else if eps, err := svc.repository.ListFeedEpisodes(ctx, feed); err != nil {
		return zaperr.Wrap(err, "failed to list feed episodes", zapFields...)
	} else {
		for _, ep := range eps {
			ep.FeedIDs = remove(ep.FeedIDs, feedID)
			if _, err := svc.repository.SaveEpisode(ctx, ep); err != nil {
				return zaperr.Wrap(err, "failed to save episode", zapFields...)
			}
		}
	}

	if err := svc.s3Store.Delete(ctx, svc.constructS3FeedPath(userID, feedID)); err != nil {
		return zaperr.Wrap(err, "failed to delete feed from s3", zapFields...)
	}

	if err := svc.repository.DeleteFeed(ctx, feedID, userID); err != nil {
		return zaperr.Wrap(err, "failed to delete feed", zapFields...)
	}

	return nil
}

func (svc *Service) createFeed(ctx context.Context, userID string, title string, feedID string) (*Feed, error) {
	if feedID == "" {
		nextID, err := svc.repository.nextFeedID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to get next feed id: %w", err)
		}
		feedID = strconv.FormatInt(nextID, 10)
	}
	feedPath := svc.constructS3FeedPath(userID, feedID)
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
		zap.Any("variants_per_episode", payload.VariantsPerEpisode),
	}

	svc.logger.Info("creating queued episodes", zapFields...)

	var createdEpisodes []*Episode
	for _, variants := range payload.VariantsPerEpisode {
		episode, err := svc.CreateEpisode(ctx, payload.URL, variants, payload.UserID)
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
		zapFields := append(zapFields, zap.Strings("episode_ids", episodeIDs), zaperr.ToField(err))
		svc.logger.Error("failed to enqueue episode status polling", zapFields...)
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
		zap.Strings("episode_ids", payload.EpisodeIDs),
		zap.String("user_id", payload.UserID),
		zap.Timep("poll_after", payload.PollAfter),
	}

	if payload.PollAfter != nil {
		sleepDuration := time.Until(*payload.PollAfter)
		if sleepDuration > 0 {
			zapFields := append(zapFields, zap.Duration("sleep_duration", sleepDuration))
			svc.logger.Debug("sleeping before polling episodes", zapFields...)
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
		zapFields := append(zapFields, zap.Strings("mediary_ids", mediaryIDs))
		return zaperr.Wrap(err, "failed to fetch job status", zapFields...)
	}

	episodesStateChanges := make([]EpisodeStatusChange, 0, len(episodesMap))
	episodesToSave := make([]*Episode, 0, len(episodesMap))
	episodeIDsToRequeue := make([]string, 0, len(episodesMap))
	for _, ep := range episodesMap {
		zapFields := append(zapFields, zap.String("episode_id", ep.ID), zap.String("mediary_id", ep.MediaryID))
		jstat, exists := jobStatusMap[ep.MediaryID]
		if !exists {
			if payload.RequeueCount < maxPollEpisodesRequeueCount {
				svc.logger.Warn("mediary job status not found", zapFields...)
				episodeIDsToRequeue = append(episodeIDsToRequeue, ep.ID)
			} else {
				svc.logger.Warn("mediary job status not found, max requeue count reached", zapFields...)
			}
			continue
		}

		newStatus, err := jobStatusToEpisodeStatus(jstat.Status)
		if err != nil {
			zapFields := append(zapFields, zap.String("job_status", string(jstat.Status)))
			return zaperr.Wrap(err, "failed to convert job status to episode status", zapFields...)
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
		zapFields := append(zapFields, zap.String("episode_id", e.ID))
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
			zapFields := append(zapFields, zap.Strings("feed_ids", feedIDs))
			return zaperr.Wrap(err, "failed to enqueue feed regeneration", zapFields...)
		}
	}

	if len(episodesStateChanges) > 0 {
		svc.episodeStatusChangesChan <- episodesStateChanges
	}

	if len(episodeIDsToRequeue) > 0 {
		newPayload := &PollEpisodesStatusQueuePayload{
			EpisodeIDs:       episodeIDsToRequeue,
			UserID:           payload.UserID,
			PollingStartedAt: payload.PollingStartedAt,
			Delay:            payload.Delay,
			PollAfter:        payload.PollAfter,
			RequeueCount:     payload.RequeueCount + 1,
		}

		now := time.Now()
		if newPayload.PollingStartedAt == nil {
			newPayload.PollingStartedAt = &now
		}
		if newPayload.Delay != nil {
			newDelay := time.Duration(float64(*newPayload.Delay) * 1.1)
			if newDelay > 60*time.Minute {
				newDelay = 60 * time.Minute
			}
			newPayload.Delay = &newDelay
		} else {
			newDelay := 10 * time.Second
			newPayload.Delay = &newDelay
		}
		pollAfter := now.Add(*newPayload.Delay)
		newPayload.PollAfter = &pollAfter

		if err := svc.jobsQueue.Publish(ctx, pollEpisodesStatus, newPayload); err != nil {
			zapFields := append(zapFields, zap.Strings("episode_ids", episodeIDsToRequeue))
			return zaperr.Wrap(err, "failed to enqueue episode status polling", zapFields...)
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
		zap.Strings("feed_ids", payload.FeedIDs),
	}

	svc.logger.Info("regenerating feeds", zapFields...)

	feedsMap, err := svc.repository.GetFeedsMap(ctx, payload.FeedIDs, payload.UserID)
	if err != nil {
		return zaperr.Wrap(err, "failed to get feeds", zapFields...)
	}

	for _, f := range feedsMap {
		if err := svc.regenerateFeedFile(ctx, f); err != nil {
			zapFields := append(zapFields, zap.String("feed_id", f.ID))
			return zaperr.Wrap(err, "failed to regenerate feed", zapFields...)
		}
	}

	return nil
}

func (svc *Service) regenerateFeedFile(ctx context.Context, feed *Feed) error {
	zapFields := []zap.Field{
		zap.String("feed_id", feed.ID),
		zap.String("user_id", feed.UserID),
	}

	episodes, err := svc.repository.ListFeedEpisodes(ctx, feed)
	if err != nil {
		return zaperr.Wrap(err, "failed to list feed episodes", zapFields...)
	}

	var episodesMap = make(map[string]*Episode)
	for _, ep := range episodes {
		episodesMap[ep.ID] = ep
	}

	objectKey := svc.constructS3FeedPath(feed.UserID, feed.ID)
	feedReader, err := generateFeed(feed, episodesMap)
	if err != nil {
		return zaperr.Wrap(err, "failed to generate feed", zapFields...)
	}

	if err := svc.s3Store.Put(ctx, objectKey, feedReader, WithContentType("text/xml; charset=utf-8")); err != nil {
		return zaperr.Wrap(err, "failed to upload feed", zapFields...)
	}

	return nil
}

func (svc *Service) constructS3FeedPath(userID string, feedID string) string {
	return path.Join(svc.getUserKeyPrefix(userID), "feeds", feedID)
}

func (svc *Service) getUserKeyPrefix(userID string) string {
	return svc.obfuscateIDs(userID)
}

func (svc *Service) extractEpisodeS3Key(ep *Episode) string {
	if ep.StorageKey != "" {
		return ep.StorageKey
	}
	// ahd this is a fallback for old episodes
	// that were created before we started saving storage key
	// TODO: remove this fallback after some time
	userPrefix := svc.getUserKeyPrefix(ep.UserID)
	return ep.URL[strings.Index(ep.URL, userPrefix):]
}

func jobStatusToEpisodeStatus(status mediary.JobStatusName) (EpisodeStatus, error) {
	switch status {
	case mediary.JobStatusAccepted, mediary.JobStatusCreated:
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
