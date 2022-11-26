package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"undercast-bot/mediary"
	jobsqueue "undercast-bot/service/jobs_queue"
)

//go:generate moq -out servicemocks/s3.go -pkg servicemocks -rm . S3Store:MockS3Store
type S3Store interface {
	PreSignedURL(key string) (string, error)
}

type Service struct {
	logger     *zap.Logger
	s3Store    S3Store
	mediaSvc   mediary.Service
	repository *Repository
	jobsQueue  *jobsqueue.RedisJobQueue

	episodesChan chan []*Episode
}

type Metadata = mediary.Metadata

type Episode struct {
	ID              string
	Title           string
	UserID          string
	SourceURL       string
	SourceFilepaths []string
	MediaryID       string
	URL             string
	FeedIDs         []string
}

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
)

func New(mediaSvc mediary.Service, repository *Repository, s3Store S3Store, jobsQueue *jobsqueue.RedisJobQueue, logger *zap.Logger) *Service {
	return &Service{
		logger:       logger,
		s3Store:      s3Store,
		mediaSvc:     mediaSvc,
		repository:   repository,
		jobsQueue:    jobsQueue,
		episodesChan: make(chan []*Episode, 100),
	}
}

func (svc *Service) Start(ctx context.Context) chan []*Episode {
	svc.jobsQueue.Subscribe(ctx, createEpisodes, func(payload []byte) error {
		return svc.onQueueEpisodeCreated(ctx, payload)
	})
	return svc.episodesChan
}

func (svc *Service) FetchMetadata(ctx context.Context, mediaURL string) (*Metadata, error) {
	return retry(ctx, func() (*Metadata, error) {
		return svc.mediaSvc.FetchMetadataLongPolling(ctx, mediaURL)
	}, metadataDelays...)
}

func (svc *Service) CreateEpisodesAsync(ctx context.Context, url string, filepaths [][]string, userID string, chatID int) error {
	svc.logger.Info(
		"queueing episodes creation",
		zap.String("url", url),
		zap.Any("filepaths", filepaths),
		zap.Int("chatID", chatID),
		zap.String("userID", userID),
	)
	if err := svc.jobsQueue.Publish(ctx, createEpisodes, &EnqueueEpisodesPayload{
		URL:    url,
		Paths:  filepaths,
		ChatID: chatID,
		UserID: userID,
	}); err != nil {
		return fmt.Errorf("failed to enqueue episodes creation: %w", err)
	}
	return nil
}

func (svc *Service) onQueueEpisodeCreated(ctx context.Context, payloadBytes []byte) error {
	var payload EnqueueEpisodesPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return err
	}

	svc.logger.Info(
		"creating single file episode",
		zap.String("url", payload.URL),
		zap.Any("filepaths", payload.Paths),
		zap.Int("chatID", payload.ChatID),
	)

	var createdEpisodes []*Episode
	for _, fps := range payload.Paths {
		episode, err := svc.CreateEpisode(ctx, payload.URL, fps, payload.UserID)
		if err != nil {
			svc.logger.Error("failed to create single file episode",
				zap.String("url", payload.URL),
				zap.Any("filepaths", payload.Paths),
				zap.Int("chatID", payload.ChatID),
				zap.Error(err),
			)
			return err
		}
		createdEpisodes = append(createdEpisodes, episode)
	}

	svc.episodesChan <- createdEpisodes

	return nil
}

func (svc *Service) CreateEpisode(ctx context.Context, mediaURL string, filepaths []string, userID string) (*Episode, error) {
	filename := uuid.New().String() + ".mp3" // TODO: implement more elaborate filename generation

	presignURL, err := svc.s3Store.PreSignedURL(fmt.Sprintf("%s/%s", userID, filename))
	if err != nil {
		return nil, fmt.Errorf("CreateEpisode failed to get presigned url: %w", err)
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
		return nil, fmt.Errorf("failed to create mediary upload job: %w", err)
	}

	ep := &Episode{
		UserID:          userID,
		SourceURL:       mediaURL,
		SourceFilepaths: filepaths,
		URL:             stripQuery(presignURL),
		MediaryID:       mediaryID,
	}

	ep, err = svc.repository.SaveEpisode(ctx, ep)
	if err != nil {
		return nil, fmt.Errorf("failed to save episode: %w", err)
	}

	return ep, nil
}

func (svc *Service) IsValidURL(ctx context.Context, mediaURL string) (bool, error) {
	return svc.mediaSvc.IsValidURL(ctx, mediaURL)
}

func (svc *Service) ListEpisodes(ctx context.Context, username string) ([]*Episode, error) {
	return svc.repository.ListUserEpisodes(ctx, username)
}

func (svc *Service) ListFeeds(ctx context.Context, username string) ([]*Feed, error) {
	feeds, err := svc.repository.ListUserFeeds(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("failed to list user feeds: %w", err)
	}

	for _, f := range feeds {
		if f.ID == strconv.Itoa(defaultFeedID) {
			return feeds, nil // if default feed is present, we're all set
		}
	}

	// create default feed if it doesn't exist
	defaultFeed, err := svc.DefaultFeed(ctx, username)
	if err != nil {
		return nil, err
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

func (svc *Service) createFeed(ctx context.Context, userID string, title string, feedID string) (*Feed, error) {
	presignURL, err := svc.s3Store.PreSignedURL(fmt.Sprintf("feeds/%s/%s", userID, feedID))
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

func (svc *Service) PublishEpisode(ctx context.Context, episodeID, feedID, userID string) error {
	episode, err := svc.repository.GetEpisode(ctx, episodeID, userID)
	if err != nil {
		return fmt.Errorf("failed to publish episode: %w", err)
	}

	feed, err := svc.repository.GetFeed(ctx, feedID, userID)
	if err != nil {
		return fmt.Errorf("failed to publish episode: %w", err)
	}

	episode.FeedIDs = append(episode.FeedIDs, feedID)
	episode, err = svc.repository.SaveEpisode(ctx, episode)
	if err != nil {
		return fmt.Errorf("failed to update episode feed ids: %w", err)
	}

	feed.EpisodeIDs = append(feed.EpisodeIDs, episodeID)
	if feed, err = svc.repository.SaveFeed(ctx, feed); err != nil {
		return fmt.Errorf("failed to update feed episode ids: %w", err)
	}

	return nil
}

func (svc *Service) UnpublishEpisode(ctx context.Context, episodeID, feedID, userID string) error {
	episode, err := svc.repository.GetEpisode(ctx, episodeID, userID)
	if err != nil {
		return fmt.Errorf("failed to publish episode: %w", err)
	}

	feed, err := svc.repository.GetFeed(ctx, feedID, userID)
	if err != nil {
		return fmt.Errorf("failed to publish episode: %w", err)
	}

	episode.FeedIDs = remove(episode.FeedIDs, feedID)
	episode, err = svc.repository.SaveEpisode(ctx, episode)
	if err != nil {
		return fmt.Errorf("failed to update episode feed ids: %w", err)
	}

	feed.EpisodeIDs = remove(feed.EpisodeIDs, episodeID)
	if feed, err = svc.repository.SaveFeed(ctx, feed); err != nil {
		return fmt.Errorf("failed to update feed episode ids: %w", err)
	}

	return nil
}

func remove(s []string, r string) []string {
	var i int
	for i = range s {
		if s[i] == r {
			break
		}
	}
	return append(s[:i], s[i+1:]...)
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
