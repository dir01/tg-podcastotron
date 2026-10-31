package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/multierr"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	"tg-podcastotron/mediary"
	jobsqueue "tg-podcastotron/service/jobs_queue"
	"tg-podcastotron/telemetry"
)

//go:generate go tool moq -out servicemocks/s3.go -pkg servicemocks -rm . S3Store:MockS3Store
type S3Store interface {
	PreSignedURL(key string) (string, error)
	Put(ctx context.Context, key string, dataReader io.ReadSeeker, opts ...func(*PutOptions)) error
	Delete(ctx context.Context, key string) error
	URL(key string) (url string, err error)
	List(ctx context.Context, prefix string) ([]S3Object, error)
	DeleteMany(ctx context.Context, keys []string) error
}

type S3Object struct {
	Key          string
	Size         int64
	LastModified time.Time
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
	SetEpisodeMediaryID(ctx context.Context, userID, episodeID, mediaryID, title, placeholderTitle string, updatedAt time.Time) (updated bool, err error)
	ListUserEpisodes(ctx context.Context, userID string) ([]*Episode, error)
	ListFeedEpisodes(ctx context.Context, userID, feedID string) ([]*Episode, error)
	GetEpisodesMap(ctx context.Context, userID string, episodeIDs []string) (map[string]*Episode, error)
	DeleteEpisodes(ctx context.Context, userID string, episodeIDs []string) error
	ListExpiredEpisodes(ctx context.Context, maxAge time.Duration) ([]*Episode, error)
	GetEpisodeByCreationKey(ctx context.Context, userID, creationKey string) (*Episode, error)
	ListEpisodesByStorageKeys(ctx context.Context, storageKeys []string) ([]*Episode, error)
	ListAllEpisodes(ctx context.Context) ([]*Episode, error)

	BulkInsertPublications(ctx context.Context, publications []*Publication) error
	ListPublicationsByEpisodeIDs(ctx context.Context, userID string, episodeIDs []string) ([]*Publication, error)
	DeletePublications(ctx context.Context, userID string, publicationIDs []string) error

	Transaction(ctx context.Context, fn func(ctx context.Context) error) error
}

type Service struct {
	logger       *slog.Logger
	s3Store      S3Store
	mediaSvc     mediary.Service
	repository   Repository
	jobsQueue    *jobsqueue.SQLJobsQueue
	obfuscateIDs func(string) string
	metrics      *telemetry.Metrics

	episodeStatusChangesChan chan []EpisodeStatusChange
	defaultFeedTitle         string
}

type Metadata = mediary.Metadata

type Episode struct {
	ID              string
	UserID          string
	Title           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
	// CreationKey identifies the unit of work (create request + variant index) that
	// created this episode, so that retrying that work finds this row instead of
	// creating another one. Empty for episodes created before it was introduced.
	CreationKey string
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
	EpisodeStatusFailed      EpisodeStatus = "failed"
)

const DefaultFeedID = "1"

type Feed struct {
	ID          string
	UserID      string
	Title       string
	URL         string
	EpisodeIDs  []string
	IsPermanent bool // whether episodes in this feed should be kept regardless or cleaned up after some time
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

	errEpisodeUploadNotRecorded = fmt.Errorf("episode was deleted or its upload was already started")
)

const maxPollEpisodesRequeueCount = 100

// maxStartUploadRetries bounds how often starting one episode's mediary job is
// retried. With the queue's backoff capped at 5 minutes this is about 3 days,
// enough to ride out a long mediary outage. After that the job is dead-lettered
// and the episode is marked failed.
const maxStartUploadRetries = 864

// maxPollingDuration bounds how long we keep polling a single episode's job
// before giving up. Past this we cancel the mediary job and mark the episode
// failed, so a stuck or never-completing job cannot be polled forever.
const maxPollingDuration = 24 * time.Hour

func New(
	mediaSvc mediary.Service,
	repository Repository,
	s3Store S3Store,
	jobsQueue *jobsqueue.SQLJobsQueue,
	defaultFeedTitle string,
	obfuscateIDs func(string) string,
	logger *slog.Logger,
	metrics *telemetry.Metrics,
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
		metrics:                  metrics,
	}
}

type EpisodeStatusChange struct {
	Episode   *Episode
	OldStatus EpisodeStatus
	NewStatus EpisodeStatus
}

func (svc *Service) Start(ctx context.Context) chan []EpisodeStatusChange {
	svc.jobsQueue.Subscribe(ctx, queueEventCreateEpisodes, func(ctx context.Context, payload []byte) error {
		return svc.onCreateEpisodesQueueEvent(ctx, payload)
	})
	svc.jobsQueue.Subscribe(ctx, queueEventStartEpisodeUpload, svc.onStartEpisodeUploadQueueEvent,
		jobsqueue.WithMaxRetries(maxStartUploadRetries),
		jobsqueue.WithOnDeadLetter(svc.onStartEpisodeUploadDeadLetter),
	)
	svc.jobsQueue.Subscribe(ctx, queueEventPollEpisodesStatus, func(ctx context.Context, payload []byte) error {
		return svc.onPollEpisodesQueueEvent(ctx, payload)
	})
	svc.jobsQueue.Subscribe(ctx, queueEventRegenerateFeed, func(ctx context.Context, payload []byte) error {
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
	fields := []any{
		slog.String("url", url),
		slog.Any("variants_per_episode", variantsPerEpisode),
		slog.String("processing_type", string(processingType)),
		slog.String("user_id", userID),
	}

	svc.logger.InfoContext(ctx, "queueing episodes creation", fields...)

	if err := svc.jobsQueue.Publish(ctx, queueEventCreateEpisodes, &CreateEpisodesQueuePayload{
		RequestID:          uuid.NewString(),
		URL:                url,
		VariantsPerEpisode: variantsPerEpisode,
		ProcessingType:     processingType,
		UserID:             userID,
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to enqueue episodes creation", fields...)
	}

	return nil
}

// episodeFilename derives an episode's S3 filename from its creation key. It is
// stable across retries of the same unit of work, so a retry reuses the object (and,
// since the key is part of the presigned upload URL, the mediary job) instead of
// adding a new one. Separate requests for the same content get distinct files, so
// deleting one episode never removes another episode's audio.
func episodeFilename(userID, creationKey string) string {
	sum := sha256.Sum256([]byte(userID + "\x00" + creationKey))
	return hex.EncodeToString(sum[:]) + ".mp3"
}

// episodeCreationKey identifies the episode made from the variantIdx-th variant of a create request.
func episodeCreationKey(requestID string, variantIdx int) string {
	return fmt.Sprintf("%s/%d", requestID, variantIdx)
}

// torrentEpisodeTitle needs no metadata, so it is also an episode's placeholder
// title until its upload is started and metadata is known.
func torrentEpisodeTitle(mediaURL string, variants []string) string {
	title := titleFromFilepaths(variants)
	if title == "" {
		return titleFromSourceURL(mediaURL)
	}
	return fmt.Sprintf("%s - %s", title, titleFromSourceURL(mediaURL))
}

// CreateEpisode persists a single episode and starts its upload synchronously.
func (svc *Service) CreateEpisode(ctx context.Context, userID string, mediaURL string, variants []string, processingType ProcessingType) (*Episode, error) {
	ep, err := svc.ensureEpisode(ctx, userID, mediaURL, variants, uuid.NewString())
	if err != nil {
		return nil, err
	}
	return svc.startEpisodeUpload(ctx, ep, mediaURL, variants, processingType)
}

// ensureEpisode returns the episode created under creationKey, persisting it first if
// it does not exist yet. The row, including its storage key, exists before any upload
// to that key is started, so every object in S3 has an owning episode.
func (svc *Service) ensureEpisode(ctx context.Context, userID, mediaURL string, variants []string, creationKey string) (*Episode, error) {
	fields := []any{
		slog.String("user_id", userID),
		slog.String("media_url", mediaURL),
		slog.String("creation_key", creationKey),
	}

	existing, err := svc.repository.GetEpisodeByCreationKey(ctx, userID, creationKey)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to look up episode by creation key", fields...)
	}
	if existing != nil {
		return existing, nil
	}

	storageKey := svc.constructS3EpisodeKey(userID, episodeFilename(userID, creationKey))
	url, err := svc.s3Store.URL(storageKey)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to get episode url", fields...)
	}

	epID, err := svc.repository.NextEpisodeID(ctx, userID)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to get next episode id", fields...)
	}

	now := time.Now().UTC()
	ep, err := svc.repository.SaveEpisode(ctx, &Episode{
		ID:              epID,
		UserID:          userID,
		Title:           torrentEpisodeTitle(mediaURL, variants),
		CreatedAt:       now,
		UpdatedAt:       now,
		SourceURL:       mediaURL,
		SourceFilepaths: variants,
		URL:             url,
		Status:          EpisodeStatusCreated,
		Format:          "mp3", // FIXME: hardcoded
		StorageKey:      storageKey,
		CreationKey:     creationKey,
	})
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to save episode", fields...)
	}
	return ep, nil
}

// startEpisodeUpload starts the mediary job that produces ep's file, unless one was
// already started for it: an episode never gets a second upload.
func (svc *Service) startEpisodeUpload(ctx context.Context, ep *Episode, mediaURL string, variants []string, processingType ProcessingType) (*Episode, error) {
	if ep.MediaryID != "" {
		return ep, nil
	}

	fields := []any{
		slog.String("media_url", mediaURL),
		slog.Any("variants", variants),
		slog.String("processing_type", string(processingType)),
		slog.String("user_id", ep.UserID),
		slog.String("episode_id", ep.ID),
		slog.String("episode_key", ep.StorageKey),
	}

	presignURL, err := svc.s3Store.PreSignedURL(ep.StorageKey)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to get presigned url", fields...)
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
		return nil, telemetry.LogError(svc.logger, ctx, ErrNotImplemented, "unsupported processing type", fields...)
	}

	metadata, err := svc.FetchMetadata(ctx, mediaURL)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to fetch metadata", fields...)
	}

	var episodeTitle string
	switch metadata.DownloaderName {
	case "torrent":
		episodeTitle = torrentEpisodeTitle(mediaURL, variants)
	case "ytdl":
		episodeTitle = metadata.Name
	default:
		return nil, telemetry.LogError(svc.logger, ctx, ErrNotImplemented, "unsupported downloader while generating episode title", fields...)
	}

	mediaryID, err := svc.mediaSvc.CreateUploadJob(ctx, mediaryParams)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to create mediary job", fields...)
	}

	// Do not use SaveEpisode here: it would create the episode again if the user
	// deleted it while metadata was fetched or the job was created.
	placeholderTitle := torrentEpisodeTitle(mediaURL, variants) // keep a title the user set in the meantime
	now := time.Now().UTC()
	updated, err := svc.repository.SetEpisodeMediaryID(ctx, ep.UserID, ep.ID, mediaryID, episodeTitle, placeholderTitle, now)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to save episode", fields...)
	}
	if !updated {
		svc.logger.InfoContext(ctx, "episode was deleted or its upload was started by another worker, cancelling mediary job",
			append(fields, slog.String("mediary_id", mediaryID))...)
		if err := svc.mediaSvc.CancelJob(ctx, mediaryID); err != nil {
			// gc removes the file the job uploads.
			svc.logger.ErrorContext(ctx, "failed to cancel mediary job", append(fields, slog.String("mediary_id", mediaryID), slog.Any("error", err))...)
		}
		return nil, errEpisodeUploadNotRecorded
	}

	if ep.Title == placeholderTitle {
		ep.Title = episodeTitle
	}
	ep.MediaryID = mediaryID
	ep.UpdatedAt = now

	return ep, nil
}

func (svc *Service) IsValidURL(ctx context.Context, mediaURL string) (bool, error) {
	if isValid, err := svc.mediaSvc.IsValidURL(ctx, mediaURL); err == nil {
		return isValid, err
	} else {
		return false, telemetry.LogError(svc.logger, ctx, err, "failed to check if url is valid", slog.String("url", mediaURL))
	}
}

func (svc *Service) ListUserEpisodes(ctx context.Context, userID string) ([]*Episode, error) {
	if episodes, err := svc.repository.ListUserEpisodes(ctx, userID); err == nil {
		return episodes, nil
	} else {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to list user episodes", slog.String("user_id", userID))
	}
}

func (svc *Service) GetEpisodesMap(ctx context.Context, userID string, ids []string) (map[string]*Episode, error) {
	if episodes, err := svc.repository.GetEpisodesMap(ctx, userID, ids); err == nil {
		return episodes, nil
	} else {
		return nil, telemetry.LogError(svc.logger, ctx, ErrEpisodeNotFound, "failed to get episodes map", slog.Any("ids", ids), slog.Any("error", err))
	}
}

func (svc *Service) ListFeeds(ctx context.Context, userID string) ([]*Feed, error) {
	fields := []any{
		slog.String("username", userID),
	}

	feeds, err := svc.repository.ListUserFeeds(ctx, userID)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to list user feeds", fields...)
	}

	for _, f := range feeds {
		if f.ID == DefaultFeedID {
			return feeds, nil // if default feed is present, we're all set
		}
	}

	// create default feed if it doesn't exist
	defaultFeed, err := svc.DefaultFeed(ctx, userID)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to get default feed", fields...)
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
	fields := []any{
		slog.Any("episode_ids", episodeIDs),
		slog.Any("feed_ids", feedIDs),
		slog.String("user_id", userID),
	}

	changedFeedsMap := make(map[string]struct{}, len(feedIDs))

	if err := svc.repository.Transaction(ctx, func(ctx context.Context) error {
		existing, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, episodeIDs)
		if err != nil {
			return telemetry.LogError(svc.logger, ctx, err, "failed to list publicationsToCreate by episode ids")
		}

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
			return telemetry.LogError(svc.logger, ctx, err, "failed to delete existing publications")
		}

		if err := svc.repository.BulkInsertPublications(ctx, publicationsToCreate); err != nil {
			return telemetry.LogError(svc.logger, ctx, err, "failed to bulk insert publicationsToCreate")
		}
		return nil
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to publish episodes", fields...)
	}

	if err := svc.jobsQueue.Publish(ctx, queueEventRegenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: maps.Keys(changedFeedsMap),
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to publish regenerate feed job", fields...)
	}

	return nil
}

func (svc *Service) RenameEpisodes(ctx context.Context, userID string, epIDs []string, newTitlePattern string) error {
	fields := []any{
		slog.Any("episode_ids", epIDs),
		slog.String("new_title_pattern", newTitlePattern),
		slog.String("user_id", userID),
	}

	episodesMap, err := svc.repository.GetEpisodesMap(ctx, userID, epIDs)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to get episodes", fields...)
	}

	publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to list publications", fields...)
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
				return telemetry.LogError(svc.logger, ctx, err, "failed to save episode", fields...)
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
			return telemetry.LogError(svc.logger, ctx, err, "failed to publish regenerate feed job", fields...)
		}
	}

	return nil
}

func (svc *Service) DeleteEpisodes(ctx context.Context, userID string, epIDs []string) error {
	fields := []any{
		slog.Any("episode_ids", epIDs),
		slog.String("user_id", userID),
	}

	episodesMap, err := svc.GetEpisodesMap(ctx, userID, epIDs)
	if err != nil {
		return err
	}

	publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to list publications", fields...)
	}

	publicationIDs := make([]string, 0, len(publications))
	affectedFeedsMap := make(map[string]struct{})
	for _, p := range publications {
		publicationIDs = append(publicationIDs, p.ID)
		affectedFeedsMap[p.FeedID] = struct{}{}
	}

	if err := svc.repository.DeletePublications(ctx, userID, publicationIDs); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to delete publications", fields...)
	}

	sharedKeys, err := svc.storageKeysUsedElsewhere(ctx, userID, epIDs, episodesMap)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to check whether episode files are shared", fields...)
	}

	for _, ep := range episodesMap {
		key := svc.extractEpisodeS3Key(ep)
		if key == "" {
			continue
		}
		if sharedKeys[key] {
			svc.logger.InfoContext(ctx, "keeping episode file still used by another episode",
				slog.String("episode_id", ep.ID), slog.String("storage_key", key))
			continue
		}
		if err := svc.s3Store.Delete(ctx, key); err != nil {
			svc.logger.ErrorContext(ctx, "failed to delete episode file", slog.Any("error", err))
		}
	}

	if err := svc.repository.DeleteEpisodes(ctx, userID, epIDs); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to delete episodes", fields...)
	}

	if len(affectedFeedsMap) > 0 {
		if err := svc.jobsQueue.Publish(ctx, queueEventRegenerateFeed, RegenerateFeedQueuePayload{
			UserID:  userID,
			FeedIDs: maps.Keys(affectedFeedsMap),
		}); err != nil {
			return telemetry.LogError(svc.logger, ctx, err, "failed to publish regenerate feed job", fields...)
		}
	}

	return nil
}

// storageKeysUsedElsewhere returns the storage keys of episodesMap that are also
// referenced by episodes outside of the (userID, epIDs) set being deleted. Episodes
// created between 2026-07-11 and the introduction of creation keys derived their
// file from content, so two such episodes may share one file.
func (svc *Service) storageKeysUsedElsewhere(ctx context.Context, userID string, epIDs []string, episodesMap map[string]*Episode) (map[string]bool, error) {
	keys := make([]string, 0, len(episodesMap))
	for _, ep := range episodesMap {
		if ep.StorageKey != "" {
			keys = append(keys, ep.StorageKey)
		}
	}

	referencing, err := svc.repository.ListEpisodesByStorageKeys(ctx, keys)
	if err != nil {
		return nil, err
	}

	shared := make(map[string]bool)
	for _, ep := range referencing {
		if ep.UserID == userID && slices.Contains(epIDs, ep.ID) {
			continue
		}
		shared[ep.StorageKey] = true
	}
	return shared, nil
}

func (svc *Service) GetFeed(ctx context.Context, userID string, feedID string) (*Feed, error) {
	return svc.repository.GetFeed(ctx, userID, feedID)
}

func (svc *Service) RenameFeed(ctx context.Context, userID string, feedID string, newTitle string) error {
	fields := []any{
		slog.String("feed_id", feedID),
		slog.String("user_id", userID),
		slog.String("new_title", newTitle),
	}

	feed, err := svc.repository.GetFeed(ctx, userID, feedID)
	if err != nil {
		fields := append(fields, slog.Any("error", err))
		return telemetry.LogError(svc.logger, ctx, ErrFeedNotFound, "", fields...)
	}

	feed.Title = newTitle
	if _, err := svc.repository.SaveFeed(ctx, feed); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to save feed", fields...)
	}

	if err = svc.jobsQueue.Publish(ctx, queueEventRegenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: []string{feedID},
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to publish regenerate feed job", fields...)
	}

	return nil
}

func (svc *Service) DeleteFeed(ctx context.Context, userID string, feedID string, deleteEpisodes bool) error {
	fields := []any{
		slog.String("feed_id", feedID),
		slog.String("user_id", userID),
		slog.Bool("delete_episodes", deleteEpisodes),
	}

	feed, err := svc.repository.GetFeed(ctx, userID, feedID)
	if err != nil || feed == nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to find feed", fields...)
	}

	episodes, err := svc.repository.ListFeedEpisodes(ctx, feed.UserID, feed.ID)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to list feed episodes", fields...)
	}

	var epIDs []string
	for _, ep := range episodes {
		epIDs = append(epIDs, ep.ID)
	}

	publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to list publications", fields...)
	}
	var publicationToDeleteIDs []string
	for _, p := range publications {
		if p.FeedID == feedID {
			publicationToDeleteIDs = append(publicationToDeleteIDs, p.ID)
		}
	}
	if err := svc.repository.DeletePublications(ctx, userID, publicationToDeleteIDs); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to delete publications", fields...)
	}

	if deleteEpisodes {
		if err := svc.DeleteEpisodes(ctx, userID, epIDs); err != nil {
			return telemetry.LogError(svc.logger, ctx, err, "failed to delete episodes", fields...)
		}
	}

	if err := svc.s3Store.Delete(ctx, svc.constructS3FeedKey(userID, feedID)); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to delete feed from s3", fields...)
	}

	if err := svc.repository.DeleteFeed(ctx, userID, feedID); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to delete feed", fields...)
	}

	return nil
}

func (svc *Service) MarkFeedAsPermanent(ctx context.Context, userID string, feedID string) error {
	feed, err := svc.repository.GetFeed(ctx, userID, feedID)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to get feed")
	}

	feed.IsPermanent = true

	if _, err := svc.repository.SaveFeed(ctx, feed); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to save feed")
	}

	return nil
}

func (svc *Service) MarkFeedAsEphemeral(ctx context.Context, userID string, feedID string) error {
	feed, err := svc.repository.GetFeed(ctx, userID, feedID)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to get feed")
	}

	feed.IsPermanent = false

	if _, err := svc.repository.SaveFeed(ctx, feed); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to save feed")
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
		return nil, telemetry.LogError(svc.logger, ctx, err, "failed to list episode feeds")
	}

	feeds := make([]*Feed, 0, len(feedsMap))
	for _, fid := range feedIDs {
		feeds = append(feeds, feedsMap[fid])
	}

	return feeds, nil
}

func (svc *Service) GetPublishedFeedsMap(ctx context.Context, userID string, epIDs []string) (map[string][]string, error) {
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

func (svc *Service) ListExpiredEpisodes(ctx context.Context, maxAge time.Duration) ([]*Episode, error) {
	return svc.repository.ListExpiredEpisodes(ctx, maxAge)
}

func (svc *Service) RegenerateFeed(ctx context.Context, userID string, feedID string) error {
	if err := svc.jobsQueue.Publish(ctx, queueEventRegenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: []string{feedID},
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to publish regenerate feed job", slog.String("feed_id", feedID), slog.String("user_id", userID))
	}

	return nil
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
		return telemetry.LogError(svc.logger, ctx, err, "failed to unmarshal payload", slog.String("payload", string(payloadBytes)))
	}

	fields := []any{
		slog.String("url", payload.URL),
		slog.Any("variants_per_episode", payload.VariantsPerEpisode),
		slog.String("processing_type", string(payload.ProcessingType)),
	}

	svc.logger.InfoContext(ctx, "creating queued episodes", fields...)

	requestID := payload.RequestID
	if requestID == "" { // enqueued before RequestID existed; the payload is identical across retries
		sum := sha256.Sum256(payloadBytes)
		requestID = hex.EncodeToString(sum[:])
	}

	// Persist all of the request's episodes before starting any upload. A retry finds
	// them by creation key instead of creating more, and each episode's upload is a
	// separate job, so one failing variant neither blocks nor re-runs the others.
	var episodes []*Episode
	if err := svc.repository.Transaction(ctx, func(ctx context.Context) error {
		episodes = episodes[:0]
		for i, variants := range payload.VariantsPerEpisode {
			ep, err := svc.ensureEpisode(ctx, payload.UserID, payload.URL, variants, episodeCreationKey(requestID, i))
			if err != nil {
				return err
			}
			episodes = append(episodes, ep)
		}
		return nil
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to persist queued episodes", fields...)
	}

	for i, ep := range episodes {
		if err := svc.jobsQueue.Publish(ctx, queueEventStartEpisodeUpload, &StartEpisodeUploadQueuePayload{
			UserID:         payload.UserID,
			EpisodeID:      ep.ID,
			URL:            payload.URL,
			Variants:       payload.VariantsPerEpisode[i],
			ProcessingType: payload.ProcessingType,
		}); err != nil {
			fields := append(fields, slog.String("episode_id", ep.ID))
			return telemetry.LogError(svc.logger, ctx, err, "failed to enqueue episode upload", fields...)
		}
	}

	episodesStatusChanges := make([]EpisodeStatusChange, len(episodes))
	for i, e := range episodes {
		episodesStatusChanges[i] = EpisodeStatusChange{
			Episode:   e,
			OldStatus: EpisodeStatusUndefined,
			NewStatus: EpisodeStatusCreated,
		}
	}
	svc.episodeStatusChangesChan <- episodesStatusChanges

	return nil
}

func (svc *Service) onStartEpisodeUploadQueueEvent(ctx context.Context, payloadBytes []byte) error {
	var payload StartEpisodeUploadQueuePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to unmarshal payload", slog.String("payload", string(payloadBytes)))
	}

	fields := []any{
		slog.String("user_id", payload.UserID),
		slog.String("episode_id", payload.EpisodeID),
	}
	if info, ok := jobsqueue.JobInfoFromContext(ctx); ok {
		fields = append(fields, slog.Int("retry_count", int(info.RetryCount)))
	}

	episodesMap, err := svc.repository.GetEpisodesMap(ctx, payload.UserID, []string{payload.EpisodeID})
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to get episode", fields...)
	}
	ep, ok := episodesMap[payload.EpisodeID]
	if !ok {
		svc.logger.InfoContext(ctx, "episode no longer exists, not starting its upload", fields...)
		return nil
	}
	if ep.Status == EpisodeStatusFailed {
		return nil
	}

	if _, err := svc.startEpisodeUpload(ctx, ep, payload.URL, payload.Variants, payload.ProcessingType); errors.Is(err, errEpisodeUploadNotRecorded) {
		return nil // deleted, or polled by the worker that started its upload
	} else if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to start episode upload", fields...)
	}

	// Also reached when an earlier attempt started the upload but failed to enqueue
	// polling; a rare duplicate poller is preferable to an episode never polled.
	if err := svc.jobsQueue.Publish(ctx, queueEventPollEpisodesStatus, &PollEpisodesStatusQueuePayload{
		EpisodeIDs: []string{ep.ID},
		UserID:     payload.UserID,
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to enqueue episode status polling", fields...)
	}

	return nil
}

// onStartEpisodeUploadDeadLetter runs once an episode's upload could not be started
// within maxStartUploadRetries: the episode is marked failed so the user is told.
// It runs in the queue's dead-letter transaction, so the job only leaves the queue
// once the episode is marked failed.
func (svc *Service) onStartEpisodeUploadDeadLetter(ctx context.Context, tx *sql.Tx, info jobsqueue.JobInfo, payloadBytes []byte, handlerErr error) error {
	var payload StartEpisodeUploadQueuePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		// Retrying can't fix the payload: let the job be dead-lettered.
		svc.logger.ErrorContext(ctx, "failed to unmarshal dead-lettered payload", slog.String("payload", string(payloadBytes)), slog.Any("error", err))
		return nil
	}

	fields := []any{
		slog.String("user_id", payload.UserID),
		slog.String("episode_id", payload.EpisodeID),
		slog.Int64("job_id", info.ID),
		slog.Int("retry_count", int(info.RetryCount)),
		slog.Any("error", handlerErr),
	}
	svc.logger.ErrorContext(ctx, "gave up starting episode upload", fields...)

	txCtx := contextWithTx(ctx, tx)
	episodesMap, err := svc.repository.GetEpisodesMap(txCtx, payload.UserID, []string{payload.EpisodeID})
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to get dead-lettered episode", fields...)
	}
	ep, ok := episodesMap[payload.EpisodeID]
	if !ok || ep.MediaryID != "" || ep.Status == EpisodeStatusFailed {
		return nil // deleted, or started after all and owned by polling now
	}

	oldStatus := ep.Status
	ep.Status = EpisodeStatusFailed
	if _, err := svc.repository.SaveEpisode(txCtx, ep); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to save failed episode", fields...)
	}

	// Don't block on the bot while holding the queue's write lock. If the transaction
	// is then rolled back, the hook runs again and the user may be told twice.
	changes := []EpisodeStatusChange{{Episode: ep, OldStatus: oldStatus, NewStatus: EpisodeStatusFailed}}
	go func() { svc.episodeStatusChangesChan <- changes }()

	return nil
}

func (svc *Service) onPollEpisodesQueueEvent(ctx context.Context, payloadBytes []byte) error {
	var payload PollEpisodesStatusQueuePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to unmarshal payload", slog.String("payload", string(payloadBytes)))
	}

	fields := []any{
		slog.Any("episode_ids", payload.EpisodeIDs),
		slog.String("user_id", payload.UserID),
	}

	svc.logger.InfoContext(ctx, "polling episode status", fields...)

	episodesMap, err := svc.repository.GetEpisodesMap(ctx, payload.UserID, payload.EpisodeIDs)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to get episodes", fields...)
	}

	mediaryIDs := make([]string, 0, len(episodesMap))
	for _, e := range episodesMap {
		if e.Status == EpisodeStatusComplete || e.Status == EpisodeStatusFailed {
			continue
		}
		if e.MediaryID == "" {
			continue
		}
		mediaryIDs = append(mediaryIDs, e.MediaryID)
	}

	jobStatusMap, err := svc.mediaSvc.FetchJobStatusMap(ctx, mediaryIDs)
	if err != nil {
		fields := append(fields, slog.Any("mediary_ids", mediaryIDs))
		return telemetry.LogError(svc.logger, ctx, err, "failed to fetch job status", fields...)
	}

	episodesStateChanges := make([]EpisodeStatusChange, 0, len(episodesMap))
	episodesToSave := make([]*Episode, 0, len(episodesMap))
	episodeIDsToRequeue := make([]string, 0, len(episodesMap))
	for _, ep := range episodesMap {
		fields := append(fields, slog.String("episode_id", ep.ID), slog.String("mediary_id", ep.MediaryID))
		jstat, exists := jobStatusMap[ep.MediaryID]
		if !exists {
			if payload.RequeueCount < maxPollEpisodesRequeueCount {
				svc.logger.WarnContext(ctx, "mediary job status not found", fields...)
				episodeIDsToRequeue = append(episodeIDsToRequeue, ep.ID)
			} else {
				svc.logger.WarnContext(ctx, "mediary job status not found, max requeue count reached", fields...)
			}
			continue
		}

		newStatus, err := jobStatusToEpisodeStatus(jstat.Status)
		if err != nil {
			fields := append(fields, slog.String("job_status", string(jstat.Status)))
			return telemetry.LogError(svc.logger, ctx, err, "failed to convert job status to episode status", fields...)
		}

		if newStatus != EpisodeStatusComplete && newStatus != EpisodeStatusFailed {
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
		return telemetry.LogError(svc.logger, ctx, err, "failed to get publications", fields...)
	}
	epFeedsMap := make(map[string][]string)
	for _, p := range publications {
		epFeedsMap[p.EpisodeID] = append(epFeedsMap[p.EpisodeID], p.FeedID)
	}

	var episodesSaveError error
	feedsToPublish := make(map[string]bool)
	for _, e := range episodesToSave {
		fields := append(fields, slog.String("episode_id", e.ID))
		if _, err := svc.repository.SaveEpisode(ctx, e); err == nil {
			if _, exists := epFeedsMap[e.ID]; exists {
				for _, f := range epFeedsMap[e.ID] {
					feedsToPublish[f] = true
				}
			}
		} else {
			episodesSaveError = multierr.Append(episodesSaveError, telemetry.LogError(svc.logger, ctx, err, "failed to save episode", fields...))
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
			fields := append(fields, slog.Any("feed_ids", feedIDs))
			return telemetry.LogError(svc.logger, ctx, err, "failed to enqueue feed regeneration", fields...)
		}
	}

	if len(episodesStateChanges) > 0 {
		svc.episodeStatusChangesChan <- episodesStateChanges
	}

	if len(episodeIDsToRequeue) > 0 {
		// Give up once we've been polling a job for too long: cancel it in mediary
		// and mark the episode failed, rather than polling forever.
		if payload.PollingStartedAt != nil && time.Since(*payload.PollingStartedAt) > maxPollingDuration {
			svc.failEpisodes(ctx, episodeIDsToRequeue, episodesMap, "episodes marked failed after polling timeout")
			return nil
		}

		now := time.Now()
		newPayload := &PollEpisodesStatusQueuePayload{
			EpisodeIDs:   episodeIDsToRequeue,
			UserID:       payload.UserID,
			RequeueCount: payload.RequeueCount + 1,
		}

		if payload.PollingStartedAt == nil {
			newPayload.PollingStartedAt = &now
		} else {
			newPayload.PollingStartedAt = payload.PollingStartedAt
		}

		delay := 10 * time.Second
		if payload.Delay != nil {
			delay = time.Duration(float64(*payload.Delay) * 1.1)
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
		newPayload.Delay = &delay

		if err := svc.jobsQueue.Publish(ctx, queueEventPollEpisodesStatus, newPayload, jobsqueue.WithDelay(delay)); err != nil {
			fields := append(fields, slog.Any("episode_ids", episodeIDsToRequeue))
			return telemetry.LogError(svc.logger, ctx, err, "failed to enqueue episode status polling", fields...)
		}
	}

	return nil
}

// failEpisodes is invoked when episodes cannot reach a terminal state: their
// upload could not be started, or they were polled past maxPollingDuration. It
// cancels the mediary job (best effort) and marks each episode failed, emitting
// status changes so the user is notified and polling stops for these episodes.
func (svc *Service) failEpisodes(ctx context.Context, episodeIDs []string, episodesMap map[string]*Episode, reason string) {
	changes := make([]EpisodeStatusChange, 0, len(episodeIDs))
	for _, epID := range episodeIDs {
		ep, ok := episodesMap[epID]
		if !ok {
			continue
		}
		fields := []any{slog.String("episode_id", ep.ID), slog.String("mediary_id", ep.MediaryID)}

		if ep.MediaryID != "" {
			if err := svc.mediaSvc.CancelJob(ctx, ep.MediaryID); err != nil {
				svc.logger.WarnContext(ctx, "failed to cancel mediary job for failed episode", append(fields, slog.Any("error", err))...)
			}
		}

		oldStatus := ep.Status
		ep.Status = EpisodeStatusFailed
		if _, err := svc.repository.SaveEpisode(ctx, ep); err != nil {
			svc.logger.ErrorContext(ctx, "failed to save failed episode", append(fields, slog.Any("error", err))...)
			continue
		}
		changes = append(changes, EpisodeStatusChange{Episode: ep, OldStatus: oldStatus, NewStatus: EpisodeStatusFailed})
	}

	if len(changes) > 0 {
		svc.logger.WarnContext(ctx, reason, slog.Any("episode_ids", episodeIDs))
		svc.episodeStatusChangesChan <- changes
	}
}

func (svc *Service) onRegenerateFeedQueueEvent(ctx context.Context, payloadBytes []byte) error {
	var payload RegenerateFeedQueuePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to unmarshal payload", slog.String("payload", string(payloadBytes)))
	}

	fields := []any{
		slog.Any("feed_ids", payload.FeedIDs),
	}

	if len(payload.FeedIDs) == 0 {
		svc.logger.DebugContext(ctx, "no feeds to regenerate", fields...)
		return nil
	}

	svc.logger.InfoContext(ctx, "regenerating feeds", fields...)

	feedsMap, err := svc.repository.GetFeedsMap(ctx, payload.UserID, payload.FeedIDs)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to get feeds map to regenerate feed queue", fields...)
	}

	for _, f := range feedsMap {
		if err := svc.regenerateFeedFile(ctx, f); err != nil {
			fields := append(fields, slog.String("feed_id", f.ID))
			return telemetry.LogError(svc.logger, ctx, err, "failed to regenerate feed", fields...)
		}
	}

	return nil
}

func (svc *Service) regenerateFeedFile(ctx context.Context, feed *Feed) error {
	fields := []any{
		slog.String("feed_id", feed.ID),
		slog.String("user_id", feed.UserID),
	}

	episodes, err := svc.repository.ListFeedEpisodes(ctx, feed.UserID, feed.ID)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to list feed episodes", fields...)
	}

	objectKey := svc.constructS3FeedKey(feed.UserID, feed.ID)
	feedReader, err := generateFeed(feed, episodes)
	if err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to generate feed", fields...)
	}

	if err := svc.s3Store.Put(ctx, objectKey, feedReader, WithContentType("text/xml; charset=utf-8")); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to upload feed", fields...)
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
	if idx := strings.Index(ep.URL, path.Join("episodes", userPrefix)+"/"); idx != -1 {
		return ep.URL[idx:]
	}
	if idx := strings.Index(ep.URL, userPrefix); idx != -1 {
		return ep.URL[idx:]
	}
	return ""
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
	case mediary.JobStatusCancelled:
		return EpisodeStatusFailed, nil
	}
	return "", fmt.Errorf("unknown job status: %s", string(status))
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
