package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
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
	URL(key string) (url string, err error)
}

type Repository interface {
	NextFeedID(ctx context.Context, userID string) (feedID string, err error)
	SaveFeed(ctx context.Context, feed *Feed) (*Feed, error)
	GetFeed(ctx context.Context, userID, feedID string) (*Feed, error)
	ListUserFeeds(ctx context.Context, userID string) ([]*Feed, error)
	GetFeedsMap(ctx context.Context, userID string, feedIDs []string) (map[string]*Feed, error)
	DeleteFeed(ctx context.Context, userID string, feedIDs string) error

	NextEpisodeID(ctx context.Context, userID string) (epID string, err error)
	SaveEpisode(ctx context.Context, episode *Episode) (*Episode, error)
	ListUserEpisodes(ctx context.Context, userID string) ([]*Episode, error)
	ListFeedEpisodes(ctx context.Context, userID, feedID string) ([]*Episode, error)
	GetEpisodesMap(ctx context.Context, userID string, episodeIDs []string) (map[string]*Episode, error)
	DeleteEpisodes(ctx context.Context, userID string, episodeIDs []string) error

	BulkInsertPublications(ctx context.Context, publications []*Publication) error
	ListPublicationsByEpisodeIDs(ctx context.Context, userID string, episodeIDs []string) ([]*Publication, error)
	DeletePublications(ctx context.Context, userID string, publicationIDs []string) error

	Transaction(ctx context.Context, fn func(ctx context.Context) error) error
}

type Service struct {
	logger       *zap.Logger
	s3Store      S3Store
	mediaSvc     mediary.Service
	repository   Repository
	jobsQueue    *jobsqueue.RJQ
	obfuscateIDs func(string) string

	episodeStatusChangesChan chan []EpisodeStatusChange
	defaultFeedTitle         string
}

type Metadata = mediary.Metadata

type Episode struct {
	ID              string
	UserID          string
	Title           string
	PubDate         time.Time
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

const DefaultFeedID = "1"

type Feed struct {
	ID         string
	UserID     string
	Title      string
	URL        string
	EpisodeIDs []string
}

type Publication struct {
	ID        string
	UserID    string
	FeedID    string
	EpisodeID string
	CreatedAt time.Time
}

var (
	metadataDelays = []time.Duration{
		1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second,
		40 * time.Second, 60 * time.Second, 120 * time.Second, 240 * time.Second,
	}
	ErrFeedNotFound    = fmt.Errorf("feed not found")
	ErrEpisodeNotFound = fmt.Errorf("episode not found")
	ErrNotImplemented  = fmt.Errorf("not implemented")
)

const maxPollEpisodesRequeueCount = 100

func New(
	mediaSvc mediary.Service,
	repository Repository,
	s3Store S3Store,
	jobsQueue *jobsqueue.RJQ,
	defaultFeedTitle string,
	obfuscateIDs func(string) string,
	logger *zap.Logger,
) *Service {
	if defaultFeedTitle == "" {
		defaultFeedTitle = "Podcast-O-Tron"
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
	svc.jobsQueue.Subscribe(ctx, queueEventCreateEpisodes, func(payload []byte) error {
		return svc.onCreateEpisodesQueueEvent(ctx, payload)
	})
	svc.jobsQueue.Subscribe(ctx, queueEventPollEpisodesStatus, func(payload []byte) error {
		return svc.onPollEpisodesQueueEvent(ctx, payload)
	})
	svc.jobsQueue.Subscribe(ctx, queueEventRegenerateFeed, func(payload []byte) error {
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

func (svc *Service) CreateEpisodesAsync(
	ctx context.Context,
	userID string,
	url string,
	variantsPerEpisode [][]string,
	processingType ProcessingType,
) error {
	zapFields := []zap.Field{
		zap.String("url", url),
		zap.Any("variants_per_episode", variantsPerEpisode),
		zap.String("processing_type", string(processingType)),
		zap.String("user_id", userID),
	}

	svc.logger.Info("queueing episodes creation", zapFields...)

	if err := svc.jobsQueue.Publish(ctx, queueEventCreateEpisodes, &CreateEpisodesQueuePayload{
		URL:                url,
		VariantsPerEpisode: variantsPerEpisode,
		ProcessingType:     processingType,
		UserID:             userID,
	}); err != nil {
		return zaperr.Wrap(err, "failed to enqueue episodes creation", zapFields...)
	}

	return nil
}

func (svc *Service) CreateEpisode(ctx context.Context, userID string, mediaURL string, variants []string, processingType ProcessingType) (*Episode, error) {
	filename := uuid.New().String() + ".mp3" // TODO: implement more elaborate filename generation
	episodeKey := svc.constructS3EpisodeKey(userID, filename)

	zapFields := []zap.Field{
		zap.String("media_url", mediaURL),
		zap.Strings("variants", variants),
		zap.String("processing_type", string(processingType)),
		zap.String("filename", filename),
		zap.String("user_id", userID),
		zap.String("episode_key", episodeKey),
	}

	presignURL, err := svc.s3Store.PreSignedURL(episodeKey)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to get presigned url", zapFields...)
	}

	var mediaryParams *mediary.CreateUploadJobParams
	switch processingType {
	case ProcessingTypeConcatenate:
		mediaryParams = &mediary.CreateUploadJobParams{
			URL:  mediaURL,
			Type: mediary.JobTypeConcatenate,
			Params: mediary.ConcatenateJobParams{
				Variants:  variants,
				UploadURL: presignURL,
			},
		}
	case ProcessingTypeUploadOriginal:
		mediaryParams = &mediary.CreateUploadJobParams{
			URL:  mediaURL,
			Type: mediary.JobTypeUploadOriginal,
			Params: mediary.UploadOriginalJobParams{
				Variant:   variants[0],
				UploadURL: presignURL,
			},
		}
	default:
		return nil, zaperr.Wrap(ErrNotImplemented, "unsupported processing type", zapFields...)
	}

	metadata, err := svc.FetchMetadata(ctx, mediaURL)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to fetch metadata", zapFields...)
	}

	mediaryID, err := svc.mediaSvc.CreateUploadJob(ctx, mediaryParams)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to create mediary job", zapFields...)
	}

	var episodeTitle string
	switch metadata.DownloaderName {
	case "torrent":
		episodeTitle = titleFromFilepaths(variants)
		if episodeTitle == "" {
			episodeTitle = titleFromSourceURL(mediaURL)
		} else {
			episodeTitle = fmt.Sprintf("%s - %s", episodeTitle, titleFromSourceURL(mediaURL))
		}
	case "ytdl":
		episodeTitle = metadata.Name
	default:
		return nil, zaperr.Wrap(ErrNotImplemented, "unsupported downloader while generating episode title", zapFields...)
	}

	epID, err := svc.repository.NextEpisodeID(ctx, userID)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to get next episode id", zapFields...)
	}

	ep := &Episode{
		ID:              epID,
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

func (svc *Service) ListUserEpisodes(ctx context.Context, userID string) ([]*Episode, error) {
	if episodes, err := svc.repository.ListUserEpisodes(ctx, userID); err == nil {
		return episodes, nil
	} else {
		return nil, zaperr.Wrap(err, "failed to list user episodes", zap.String("user_id", userID))
	}
}

func (svc *Service) GetEpisodesMap(ctx context.Context, userID string, ids []string) (map[string]*Episode, error) {
	if episodes, err := svc.repository.GetEpisodesMap(ctx, userID, ids); err == nil {
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
		if f.ID == DefaultFeedID {
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

	existing, err := svc.repository.GetFeed(ctx, userID, DefaultFeedID)
	if err != nil {
		return nil, fmt.Errorf("failed to get default feed: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	created, err := svc.createFeed(ctx, userID, svc.defaultFeedTitle, DefaultFeedID)
	if err != nil {
		return nil, fmt.Errorf("failed to create default feed: %w", err)
	}

	return created, nil
}

func (svc *Service) CreateFeed(ctx context.Context, userID string, title string) (*Feed, error) {
	return svc.createFeed(ctx, userID, title, "")
}

func (svc *Service) PublishEpisodes(ctx context.Context, userID string, episodeIDs []string, feedIDs []string) error {
	zapFields := []zap.Field{
		zap.Strings("episode_ids", episodeIDs),
		zap.Strings("feed_ids", feedIDs),
		zap.String("user_id", userID),
	}

	changedFeedIDs := make([]string, 0, 10)

	if err := svc.repository.Transaction(ctx, func(ctx context.Context) error {
		existing, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, episodeIDs)
		if err != nil {
			return zaperr.Wrap(err, "failed to list publicationsToCreate by episode ids")
		}

		changedFeedsMap := make(map[string]struct{}, len(feedIDs))

		publicationsToDelete := make([]string, 0, len(existing))

		type key struct {
			episodeID string
			feedID    string
		}
		existingPublicationsMap := make(map[key]struct{}, len(existing))

		for _, p := range existing {
			existingPublicationsMap[key{episodeID: p.EpisodeID, feedID: p.FeedID}] = struct{}{}
			if !slices.Contains(feedIDs, p.FeedID) {
				publicationsToDelete = append(publicationsToDelete, p.ID)
				changedFeedsMap[p.FeedID] = struct{}{}
			}
		}

		publicationsToCreate := make([]*Publication, 0, len(episodeIDs)*len(feedIDs))
		for _, epID := range episodeIDs {
			for _, feedID := range feedIDs {
				if _, ok := existingPublicationsMap[key{episodeID: epID, feedID: feedID}]; ok {
					continue
				}
				publicationsToCreate = append(publicationsToCreate, &Publication{
					UserID:    userID,
					FeedID:    feedID,
					EpisodeID: epID,
					CreatedAt: time.Now(),
				})
				changedFeedsMap[feedID] = struct{}{}
			}
		}

		if err := svc.repository.DeletePublications(ctx, userID, publicationsToDelete); err != nil {
			return zaperr.Wrap(err, "failed to delete existing publications")
		}

		if err := svc.repository.BulkInsertPublications(ctx, publicationsToCreate); err != nil {
			return zaperr.Wrap(err, "failed to bulk insert publicationsToCreate")
		}
		return nil
	}); err != nil {
		return zaperr.Wrap(err, "failed to publish episodes", zapFields...)
	}

	if err := svc.jobsQueue.Publish(ctx, queueEventRegenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: changedFeedIDs,
	}); err != nil {
		return zaperr.Wrap(err, "failed to publish regenerate feed job", zapFields...)
	}

	return nil
}

func (svc *Service) RenameEpisodes(ctx context.Context, userID string, epIDs []string, newTitlePattern string) error {
	zapFields := []zap.Field{
		zap.Strings("episode_ids", epIDs),
		zap.String("new_title_pattern", newTitlePattern),
		zap.String("user_id", userID),
	}

	episodesMap, err := svc.repository.GetEpisodesMap(ctx, userID, epIDs)
	if err != nil {
		return zaperr.Wrap(err, "failed to get episodes", zapFields...)
	}

	publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
	if err != nil {
		return zaperr.Wrap(err, "failed to list publications", zapFields...)
	}
	epToFeedMap := make(map[string][]string, len(publications))
	for _, p := range publications {
		epToFeedMap[p.EpisodeID] = append(epToFeedMap[p.EpisodeID], p.FeedID)
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
			if feedIDs, ok := epToFeedMap[ep.ID]; ok {
				for _, feedID := range feedIDs {
					feedsToUpdate[feedID] = true
				}
			}
		}
	}

	if len(feedsToUpdate) > 0 {
		if err = svc.jobsQueue.Publish(ctx, queueEventRegenerateFeed, RegenerateFeedQueuePayload{
			UserID:  userID,
			FeedIDs: maps.Keys(feedsToUpdate),
		}); err != nil {
			return zaperr.Wrap(err, "failed to publish regenerate feed job", zapFields...)
		}
	}

	return nil
}

func (svc *Service) DeleteEpisodes(ctx context.Context, userID string, epIDs []string) error {
	zapFields := []zap.Field{
		zap.Strings("episode_ids", epIDs),
		zap.String("user_id", userID),
	}

	episodesMap, err := svc.GetEpisodesMap(ctx, userID, epIDs)
	if err != nil {
		return err
	}

	publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
	if err != nil {
		return zaperr.Wrap(err, "failed to list publications", zapFields...)
	}

	publicationIDs := make([]string, 0, len(publications))
	for _, p := range publications {
		publicationIDs = append(publicationIDs, p.ID)
	}

	if err := svc.repository.DeletePublications(ctx, userID, publicationIDs); err != nil {
		return zaperr.Wrap(err, "failed to delete publications", zapFields...)
	}

	for _, ep := range episodesMap {
		if err := svc.s3Store.Delete(ctx, svc.extractEpisodeS3Key(ep)); err != nil {
			svc.logger.Error("failed to delete episode file", zaperr.ToField(err))
		}
	}

	if err := svc.repository.DeleteEpisodes(ctx, userID, epIDs); err != nil {
		return zaperr.Wrap(err, "failed to delete episodes", zapFields...)
	}

	return nil
}

func (svc *Service) RenameFeed(ctx context.Context, userID string, feedID string, newTitle string) error {
	zapFields := []zap.Field{
		zap.String("feed_id", feedID),
		zap.String("user_id", userID),
		zap.String("new_title", newTitle),
	}

	feed, err := svc.repository.GetFeed(ctx, userID, feedID)
	if err != nil {
		zapFields := append(zapFields, zaperr.ToField(err))
		return zaperr.Wrap(ErrFeedNotFound, "", zapFields...)
	}

	feed.Title = newTitle
	if _, err := svc.repository.SaveFeed(ctx, feed); err != nil {
		return zaperr.Wrap(err, "failed to save feed", zapFields...)
	}

	if err = svc.jobsQueue.Publish(ctx, queueEventRegenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: []string{feedID},
	}); err != nil {
		return zaperr.Wrap(err, "failed to publish regenerate feed job", zapFields...)
	}

	return nil
}

func (svc *Service) DeleteFeed(ctx context.Context, userID string, feedID string, deleteEpisodes bool) error {
	zapFields := []zap.Field{
		zap.String("feed_id", feedID),
		zap.String("user_id", userID),
		zap.Bool("delete_episodes", deleteEpisodes),
	}

	feed, err := svc.repository.GetFeed(ctx, userID, feedID)
	if err != nil || feed == nil {
		return zaperr.Wrap(err, "failed to find feed", zapFields...)
	}

	episodes, err := svc.repository.ListFeedEpisodes(ctx, feed.UserID, feed.ID)
	if err != nil {
		return zaperr.Wrap(err, "failed to list feed episodes", zapFields...)
	}

	var epIDs []string
	for _, ep := range episodes {
		epIDs = append(epIDs, ep.ID)
	}

	publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
	if err != nil {
		return zaperr.Wrap(err, "failed to list publications", zapFields...)
	}
	var publicationToDeleteIDs []string
	for _, p := range publications {
		if p.FeedID == feedID {
			publicationToDeleteIDs = append(publicationToDeleteIDs, p.ID)
		}
	}
	if err := svc.repository.DeletePublications(ctx, userID, publicationToDeleteIDs); err != nil {
		return zaperr.Wrap(err, "failed to delete publications", zapFields...)
	}

	if deleteEpisodes {
		if err := svc.DeleteEpisodes(ctx, userID, epIDs); err != nil {
			return zaperr.Wrap(err, "failed to delete episodes", zapFields...)
		}
	}

	if err := svc.s3Store.Delete(ctx, svc.constructS3FeedKey(userID, feedID)); err != nil {
		return zaperr.Wrap(err, "failed to delete feed from s3", zapFields...)
	}

	if err := svc.repository.DeleteFeed(ctx, userID, feedID); err != nil {
		return zaperr.Wrap(err, "failed to delete feed", zapFields...)
	}

	return nil
}

func (svc *Service) ListFeedEpisodes(ctx context.Context, userID string, feedID string) ([]*Episode, error) {
	return svc.repository.ListFeedEpisodes(ctx, userID, feedID)
}

func (svc *Service) ListEpisodeFeeds(ctx context.Context, userID string, epID string) ([]*Feed, error) {
	publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, []string{epID})
	if err != nil {
		return nil, err
	}

	feedIDs := make([]string, 0, len(publications))
	for _, p := range publications {
		if p.EpisodeID == epID {
			feedIDs = append(feedIDs, p.FeedID)
		}
	}

	feedsMap, err := svc.repository.GetFeedsMap(ctx, userID, feedIDs)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to list episode feeds")
	}

	feeds := make([]*Feed, 0, len(feedsMap))
	for _, fid := range feedIDs {
		feeds = append(feeds, feedsMap[fid])
	}

	return feeds, nil
}

func (svc *Service) GetPublishedFeedsMap(ctx context.Context, epIDs []string, userID string) (map[string][]string, error) {
	publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
	if err != nil {
		return nil, err
	}

	epToFeedMap := make(map[string][]string, len(publications))
	for _, p := range publications {
		epToFeedMap[p.EpisodeID] = append(epToFeedMap[p.EpisodeID], p.FeedID)
	}

	return epToFeedMap, nil
}

func (svc *Service) createFeed(ctx context.Context, userID string, title string, feedID string) (*Feed, error) {
	var err error
	if feedID == "" {
		for feedID == "" || feedID == DefaultFeedID {
			feedID, err = svc.repository.NextFeedID(ctx, userID)
			if err != nil {
				return nil, fmt.Errorf("failed to get next feed id: %w", err)
			}
		}
	}

	feedKey := svc.constructS3FeedKey(userID, feedID)

	url, err := svc.s3Store.URL(feedKey)
	if err != nil {
		return nil, fmt.Errorf("CreateFeed failed to get s3 url: %w", err)
	}

	feed := &Feed{
		ID:     feedID, // feedIDs can be empty, in which case it will be generated by the repository
		Title:  title,
		UserID: userID,
		URL:    url,
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
		zap.String("processing_type", string(payload.ProcessingType)),
	}

	svc.logger.Info("creating queued episodes", zapFields...)

	var createdEpisodes []*Episode
	for _, variants := range payload.VariantsPerEpisode {
		episode, err := svc.CreateEpisode(ctx, payload.UserID, payload.URL, variants, payload.ProcessingType)
		if err != nil {
			return zaperr.Wrap(err, "failed to create single file episode", zapFields...)
		}
		createdEpisodes = append(createdEpisodes, episode)
	}

	episodeIDs := make([]string, len(createdEpisodes))
	for i, e := range createdEpisodes {
		episodeIDs[i] = e.ID
	}

	if err := svc.jobsQueue.Publish(ctx, queueEventPollEpisodesStatus, &PollEpisodesStatusQueuePayload{
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

	episodesMap, err := svc.repository.GetEpisodesMap(ctx, payload.UserID, payload.EpisodeIDs)
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

	publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, payload.UserID, payload.EpisodeIDs)
	if err != nil {
		return zaperr.Wrap(err, "failed to get publications", zapFields...)
	}
	epFeedsMap := make(map[string][]string)
	for _, p := range publications {
		epFeedsMap[p.EpisodeID] = append(epFeedsMap[p.EpisodeID], p.FeedID)
	}

	var episodesSaveError error
	feedsToPublish := make(map[string]bool)
	for _, e := range episodesToSave {
		zapFields := append(zapFields, zap.String("episode_id", e.ID))
		if _, err := svc.repository.SaveEpisode(ctx, e); err == nil {
			if _, exists := epFeedsMap[e.ID]; exists {
				for _, f := range epFeedsMap[e.ID] {
					feedsToPublish[f] = true
				}
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
		if err := svc.jobsQueue.Publish(ctx, queueEventRegenerateFeed, &RegenerateFeedQueuePayload{
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

		if err := svc.jobsQueue.Publish(ctx, queueEventPollEpisodesStatus, newPayload); err != nil {
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

	if len(payload.FeedIDs) == 0 {
		svc.logger.Debug("no feeds to regenerate", zapFields...)
		return nil
	}

	svc.logger.Info("regenerating feeds", zapFields...)

	feedsMap, err := svc.repository.GetFeedsMap(ctx, payload.UserID, payload.FeedIDs)
	if err != nil {
		return zaperr.Wrap(err, "failed to get feeds map to regenerate feed queue", zapFields...)
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

	episodes, err := svc.repository.ListFeedEpisodes(ctx, feed.UserID, feed.ID)
	if err != nil {
		return zaperr.Wrap(err, "failed to list feed episodes", zapFields...)
	}

	objectKey := svc.constructS3FeedKey(feed.UserID, feed.ID)
	feedReader, err := generateFeed(feed, episodes)
	if err != nil {
		return zaperr.Wrap(err, "failed to generate feed", zapFields...)
	}

	if err := svc.s3Store.Put(ctx, objectKey, feedReader, WithContentType("text/xml; charset=utf-8")); err != nil {
		return zaperr.Wrap(err, "failed to upload feed", zapFields...)
	}

	return nil
}

func (svc *Service) constructS3FeedKey(userID string, feedID string) string {
	// we want `feeds` to go first to make it easier to assign prefix-based policies
	return path.Join("feeds", svc.getUserKeyPrefix(userID), feedID)
}

func (svc *Service) constructS3EpisodeKey(userID string, filename string) string {
	// we want `episodes` to go first to make it easier to assign prefix-based policies
	return path.Join("episodes", svc.getUserKeyPrefix(userID), filename)
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
