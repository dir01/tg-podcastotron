package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redis"
	"go.uber.org/zap"
	"undercast-bot/mediary"
	"undercast-bot/mediary/mediarymocks"
	"undercast-bot/service"
	jobsqueue "undercast-bot/service/jobs_queue"
	"undercast-bot/service/servicemocks"
	tests "undercast-bot/testutils"
)

func TestService(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	redisURL, teardown, err := tests.GetFakeRedisURL(ctx)
	defer teardown()
	if err != nil {
		t.Fatalf("error getting redis url: %v", err)
	}

	opt, _ := redis.ParseURL(redisURL)
	redisClient := redis.NewClient(opt)
	defer func() { _ = redisClient.Close() }()

	logger, err := zap.NewDevelopment()

	repo := service.NewRepository(redisClient, "some-prefix")
	jobsQueue, err := jobsqueue.NewRedisJobsQueue(redisClient, 1, "", logger)
	mockedMediary := &mediarymocks.ServiceMock{
		CreateUploadJobFunc: func(ctx context.Context, params *mediary.CreateUploadJobParams) (string, error) {
			return "some-job-id", nil
		},
	}
	mockedS3Store := &servicemocks.MockS3Store{
		PreSignedURLFunc: func(key string) (string, error) {
			return "some-presigned-url", nil
		},
	}

	svc := service.New(mockedMediary, repo, mockedS3Store, jobsQueue, logger)

	t.Run("Two users create and get feeds", func(t *testing.T) {
		feed1, err := svc.CreateFeed(ctx, "user-1", "feed-of-user-1")
		if err != nil {
			t.Fatalf("error creating feed: %v", err)
		}
		if feed1.ID != "2" {
			t.Fatalf("expected feed-of-user-1 to have id 2, got %s", feed1.ID)
		}

		feed2, err := svc.CreateFeed(ctx, "user-2", "feed-of-user-2")
		if err != nil {
			t.Fatalf("error creating feed: %v", err)
		}
		if feed2.ID != "2" {
			t.Fatalf("expected feed-of-user-2 to have id 2, got %s", feed2.ID)
		}

		feedSlice1, err := svc.ListFeeds(ctx, "user-1")
		if err != nil {
			t.Fatalf("error listing feeds of user-1: %v", err)
		}
		if len(feedSlice1) != 2 {
			t.Fatalf("expected user-1 to have 2 feed, got %d", len(feedSlice1))
		}
		if feedSlice1[0].ID != "1" {
			t.Fatalf("expected feeds of user-1 to start with default feed (id 1), got %s", feedSlice1[0].ID)
		}
		if feedSlice1[1].ID != "2" {
			t.Fatalf("expected feeds of contain feed-of-user-1 (id 2), got %s", feedSlice1[1].ID)
		}

	})

	t.Run("Create, publish and unpublish episode", func(t *testing.T) {
		// region Create and publish
		ep, err := svc.CreateEpisode(ctx, "some-media-url", []string{}, "user-1")
		if err != nil {
			t.Fatalf("error creating episode: %v", err)
		}

		defaultFeed, err := svc.DefaultFeed(ctx, "user-1")
		if err != nil {
			t.Fatalf("error getting default feed of user-1: %v", err)
		}

		err = svc.PublishEpisodes(ctx, []string{ep.ID}, defaultFeed.ID, "user-1")
		if err != nil {
			t.Fatalf("error publishing episode: %v", err)
		}
		// endregion

		// region Validate than feed contains episode
		defaultFeed, err = svc.DefaultFeed(ctx, "user-1")
		if err != nil {
			t.Fatalf("error getting default feed of user-1: %v", err)
		}
		if len(defaultFeed.EpisodeIDs) != 1 {
			t.Fatalf("expected default feed of user-1 to have 1 episode, got %d", len(defaultFeed.EpisodeIDs))
		}
		if defaultFeed.EpisodeIDs[0] != ep.ID {
			t.Fatalf("expected default feed of user-1 to have episode %s, got %s", ep.ID, defaultFeed.EpisodeIDs[0])
		}
		// endregion

		// region Validate that episode contains feed
		eps, err := svc.ListEpisodes(ctx, "user-1")
		if err != nil {
			t.Fatalf("error getting episodes: %v", err)
		}
		if len(eps) != 1 {
			t.Fatalf("expected 1 episode, got %d", len(eps))
		}
		if len(eps[0].FeedIDs) != 1 {
			t.Fatalf("expected episode to be published in 1 feed, got %d", len(eps[0].FeedIDs))
		}
		if eps[0].FeedIDs[0] != defaultFeed.ID {
			t.Fatalf("expected episode to be published in default feed of user-1, got %s", eps[0].FeedIDs[0])
		}
		// endregion

		// region Unpublish episode
		err = svc.UnpublishEpisodes(ctx, []string{ep.ID}, defaultFeed.ID, "user-1")
		if err != nil {
			t.Fatalf("error unpublishing episode: %v", err)
		}
		// endregion

		// region Validate that episode is not in feed
		defaultFeed, err = svc.DefaultFeed(ctx, "user-1")
		if err != nil {
			t.Fatalf("error getting default feed of user-1: %v", err)
		}
		if len(defaultFeed.EpisodeIDs) != 0 {
			t.Fatalf("expected default feed of user-1 to have 0 episode, got %d", len(defaultFeed.EpisodeIDs))
		}
		// endregion

		// region Validate that feed is not in episode
		eps, err = svc.ListEpisodes(ctx, ep.ID)
		if err != nil {
			t.Fatalf("error getting episodes: %v", err)
		}
		if len(eps) != 0 {
			t.Fatalf("expected 0 episode, got %d", len(eps))
		}
		// endregion
	})

	t.Run("Double publish is not allowed", func(t *testing.T) {
		// region Create and publish
		ep, err := svc.CreateEpisode(ctx, "some-media-url", []string{}, "user-id")
		if err != nil {
			t.Fatalf("error creating episode: %v", err)
		}

		defaultFeed, err := svc.DefaultFeed(ctx, "user-id")
		if err != nil {
			t.Fatalf("error getting default feed of user-id: %v", err)
		}

		err = svc.PublishEpisodes(ctx, []string{ep.ID}, defaultFeed.ID, "user-id")
		if err != nil {
			t.Fatalf("error publishing episode: %v", err)
		}
		// endregion

		// region Second publish
		err = svc.PublishEpisodes(ctx, []string{ep.ID}, defaultFeed.ID, "user-id")
		if err != nil {
			t.Fatalf("error publishing episode second time: %v", err)
		}
		// endregion

		// region Validate that episode is only in feed once
		defaultFeed, err = svc.DefaultFeed(ctx, "user-id")
		if err != nil {
			t.Fatalf("error getting default feed of user-id second time: %v", err)
		}

		if len(defaultFeed.EpisodeIDs) != 1 {
			t.Fatalf("expected default feed of user-id to have 1 episode, got %d", len(defaultFeed.EpisodeIDs))
		}
		// endregion
	})

	t.Run("Unpublishing an episode that is not published does nothing", func(t *testing.T) {
		defaultFeed, err := svc.DefaultFeed(ctx, "user-id")
		if err != nil {
			t.Fatalf("error getting default feed of user-id: %v", err)
		}

		err = svc.UnpublishEpisodes(ctx, []string{"some-episode-id"}, defaultFeed.ID, "user-id")
		if err != nil {
			t.Fatalf("error unpublishing episode: %v", err)
		}
	})
}
