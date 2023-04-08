package service_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
	"tg-podcastotron/mediary"
	"tg-podcastotron/mediary/mediarymocks"
	"tg-podcastotron/service"
	jobsqueue "tg-podcastotron/service/jobs_queue"
	"tg-podcastotron/service/servicemocks"
	tests "tg-podcastotron/testutils"
)

func TestService(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	redisURL, teardown, err := tests.GetFakeRedisURL(ctx)
	defer teardown()
	if err != nil {
		t.Fatalf("error getting redis url: %v", err)
	}

	opt := must(redis.ParseURL(redisURL))(t)
	redisClient := redis.NewClient(opt)
	defer func() { _ = redisClient.Close() }()

	logger := must(zap.NewDevelopment())(t)

	repo := service.NewRepository(redisClient, "some-namespace")
	jobsQueue := must(
		jobsqueue.NewRedisJobsQueue(redisClient, 1, "some-jobs-namespace", logger),
	)(t)
	mockedMediary := &mediarymocks.ServiceMock{
		CreateUploadJobFunc: func(ctx context.Context, params *mediary.CreateUploadJobParams) (string, error) {
			return "some-job-id", nil
		},
		FetchMetadataLongPollingFunc: func(ctx context.Context, mediaURL string) (*mediary.Metadata, error) {
			return &mediary.Metadata{
				URL:            mediaURL,
				DownloaderName: "torrent",
			}, nil
		},
	}
	mockedS3Store := &servicemocks.MockS3Store{
		PreSignedURLFunc: func(key string) (string, error) {
			return "https://exapmple.com/" + key, nil
		},
		DeleteFunc: func(ctx context.Context, key string) error {
			return nil
		},
	}

	obfuscateIDs := func(s string) string {
		return s
	}
	svc := service.New(mockedMediary, repo, mockedS3Store, jobsQueue, "default-feed-title", obfuscateIDs, logger)

	mkUserID := func() string {
		return uuid.Must(uuid.NewRandom()).String()
	}

	t.Run("Default feed", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.DefaultFeed(ctx, userID))(t)
		if feed.ID != "1" {
			t.Fatalf("expected default feed to have id 1, got %s", feed.ID)
		}

		if feed.URL != "https://exapmple.com/"+userID+"/feeds/1" {
			t.Fatalf("expected default feed to have url https://exapmple.com/"+userID+"/feeds/1, got %s", feed.URL)
		}
	})

	t.Run("Create feed", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.CreateFeed(ctx, userID, "some-feed"))(t)
		if feed.ID != "2" {
			t.Fatalf("expected feed to have id 2, got %s", feed.ID)
		}

		if feed.URL != "https://exapmple.com/"+userID+"/feeds/2" {
			t.Fatalf("expected feed to have url https://exapmple.com/"+userID+"/feeds/2, got %s", feed.URL)
		}
	})

	t.Run("Two users create and get feeds", func(t *testing.T) {
		userID := mkUserID()

		feed1 := must(svc.CreateFeed(ctx, userID, "feed-of-user-1"))(t)
		if feed1.ID != "2" {
			t.Fatalf("expected feed-of-user-1 to have id 2, got %s", feed1.ID)
		}

		feed2 := must(svc.CreateFeed(ctx, "user-2", "feed-of-user-2"))(t)
		if feed2.ID != "2" {
			t.Fatalf("expected feed-of-user-2 to have id 2, got %s", feed2.ID)
		}

		feedSlice1 := must(svc.ListFeeds(ctx, userID))(t)
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

	t.Run("Create and publish episode", func(t *testing.T) {
		userID := mkUserID()

		// region Create and publish
		ep := must(svc.CreateEpisode(ctx, "some-media-url", []string{}, "concatenate", userID))(t)

		defaultFeed := must(svc.DefaultFeed(ctx, userID))(t)

		if err = svc.PublishEpisodes(ctx, []string{ep.ID}, []string{defaultFeed.ID}, userID); err != nil {
			t.Fatalf("error publishing episode: %v", err)
		}
		// endregion

		// region Validate than feed contains episode
		defaultFeed = must(svc.DefaultFeed(ctx, userID))(t)
		if len(defaultFeed.EpisodeIDs) != 1 {
			t.Fatalf("expected default feed of user-1 to have 1 episode, got %d", len(defaultFeed.EpisodeIDs))
		}
		if defaultFeed.EpisodeIDs[0] != ep.ID {
			t.Fatalf("expected default feed of user-1 to have episode %s, got %s", ep.ID, defaultFeed.EpisodeIDs[0])
		}
		// endregion

		// region Validate that episode contains feed
		eps := must(svc.ListEpisodes(ctx, userID))(t)
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
	})

	t.Run("Double publish is not allowed", func(t *testing.T) {
		userID := mkUserID()

		// region Create and publish twice
		ep := must(svc.CreateEpisode(ctx, "some-media-url", []string{}, "concatenate", userID))(t)

		defaultFeed := must(svc.DefaultFeed(ctx, userID))(t)

		for i := 0; i < 2; i++ {
			if err = svc.PublishEpisodes(ctx, []string{ep.ID}, []string{defaultFeed.ID}, userID); err != nil {
				t.Fatalf("error publishing episode: %v", err)
			}
		}
		// endregion

		// region Validate that episode is only in feed once
		defaultFeed = must(svc.DefaultFeed(ctx, userID))(t)
		if len(defaultFeed.EpisodeIDs) != 1 {
			t.Fatalf("expected default feed of user-id to have 1 episode, got %d", len(defaultFeed.EpisodeIDs))
		}
		// endregion
	})

	t.Run("Publish episodes to several feeds", func(t *testing.T) {
		userID := mkUserID()

		feed1 := must(svc.CreateFeed(ctx, userID, "first feed of user-1"))(t)
		feed2 := must(svc.CreateFeed(ctx, userID, "second feed of user-1"))(t)

		// region Create and publish 10 episodes to feed1 and others to feed2
		episodeIDs := make([]string, 10)
		for i := 0; i < 10; i++ {
			ep := must(svc.CreateEpisode(ctx, "some-media-url", []string{}, "concatenate", userID))(t)

			var f *service.Feed
			if i%2 == 0 {
				f = feed1
			} else {
				f = feed2
			}

			if err = svc.PublishEpisodes(ctx, []string{ep.ID}, []string{f.ID}, userID); err != nil {
				t.Fatalf("error publishing episode: %v", err)
			}

			episodeIDs[i] = ep.ID
		}
		// endregion

		// region Prepare feed3 with one existing episode
		feed3 := must(svc.CreateFeed(ctx, userID, "third feed of user-1"))(t)
		feed3ep := must(svc.CreateEpisode(ctx, "some-media-url", []string{}, "concatenate", userID))(t)
		if err = svc.PublishEpisodes(ctx, []string{feed3ep.ID}, []string{feed3.ID}, userID); err != nil {
			t.Fatalf("error publishing episode: %v", err)
		}
		// refetch feed3 so that service will deal with refreshed information
		allFeeds := must(svc.ListFeeds(ctx, userID))(t)
		for _, f := range allFeeds {
			if f.ID == feed3.ID {
				feed3 = f
			}
		}
		// endregion

		// region Set first 10 episodes to feed2 and feed3
		if err = svc.PublishEpisodes(ctx, episodeIDs, []string{feed2.ID, feed3.ID}, userID); err != nil {
			t.Fatalf("error setting episodes feeds: %v", err)
		}
		// endregion

		feedsMap := must(svc.GetFeedsMap(ctx, []string{feed1.ID, feed2.ID, feed3.ID}, userID))(t)
		// region Validate that feed1 has no episodes
		feed1 = feedsMap[feed1.ID]
		if len(feed1.EpisodeIDs) != 0 {
			t.Fatalf("expected feed1 to have 0 episodes, got %d", len(feed1.EpisodeIDs))
		}
		// endregion

		// region Validate that feed2 has 10 episodes
		feed2 = feedsMap[feed2.ID]
		if len(feed2.EpisodeIDs) != 10 {
			t.Fatalf("expected feed2 to have 10 episodes, got %d", len(feed2.EpisodeIDs))
		}
		// endregion

		// region Validate that feed3 has 11 episodes
		feed3 = feedsMap[feed3.ID]
		if len(feed3.EpisodeIDs) != 11 {
			t.Fatalf("expected feed3 to have 11 episodes, got %d", len(feed3.EpisodeIDs))
		}
		// endregion

		// region Validate that 10 episodes are in feed2 and feed3
		eps := must(svc.GetEpisodesMap(ctx, episodeIDs, userID))(t)
		for _, ep := range eps {
			if len(ep.FeedIDs) != 2 {
				t.Fatalf("expected episode to be published in 2 feeds, got %d", len(ep.FeedIDs))
			}
			if !slices.Equal(ep.FeedIDs, []string{feed2.ID, feed3.ID}) {
				t.Fatalf("expected episode to be published in feed2 and feed3, got %v", ep.FeedIDs)
			}
		}
		// endregion

		//region Validate that episodes order did not change
		expected := append([]string{feed3ep.ID}, episodeIDs...)
		if !reflect.DeepEqual(expected, feed3.EpisodeIDs) {
			t.Fatalf("expected feed3 episode IDs to be %v, got %v", expected, feed3.EpisodeIDs)
		}
		//endregion
	})

	t.Run("Rename default feed", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.DefaultFeed(ctx, userID))(t)

		if err = svc.RenameFeed(ctx, feed.ID, userID, "new title"); err != nil {
			t.Fatalf("error renaming feed: %v", err)
		}

		feed = must(svc.DefaultFeed(ctx, userID))(t)
		if feed.Title != "new title" {
			t.Fatalf("expected feed title to be 'new title', got %s", feed.Title)
		}
	})

	t.Run("Delete feed", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.CreateFeed(ctx, userID, "feed to be deleted"))(t)

		if err = svc.DeleteFeed(ctx, feed.ID, userID, false); err != nil {
			t.Fatalf("error deleting feed: %v", err)
		}

		feeds := must(svc.ListFeeds(ctx, userID))(t)
		if len(feeds) != 1 { // default feed is still there
			t.Fatalf("expected 1 feed, got %d", len(feeds))
		}

		feedWasDeleted := false
		for _, call := range mockedS3Store.DeleteCalls() {
			if call.Key == userID+"/feeds/2" {
				feedWasDeleted = true
			}
		}
		if !feedWasDeleted {
			t.Fatalf("expected feed to be deleted from s3 store, but it wasn't")
		}
	})

	t.Run("Delete feed removes feed id from episodes", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.CreateFeed(ctx, userID, "feed to be deleted"))(t)
		ep1 := must(svc.CreateEpisode(ctx, "some-media-url", []string{}, "concatenate", userID))(t)
		if err = svc.PublishEpisodes(ctx, []string{ep1.ID}, []string{feed.ID}, userID); err != nil {
			t.Fatalf("error publishing episode1: %v", err)
		}

		if err = svc.DeleteFeed(ctx, feed.ID, userID, false); err != nil {
			t.Fatalf("error deleting feed: %v", err)
		}

		episodes := must(svc.GetEpisodesMap(ctx, []string{ep1.ID}, userID))(t)
		if len(episodes[ep1.ID].FeedIDs) != 0 {
			t.Fatalf("expected episode to be removed from feed, but it wasn't")
		}
	})

	t.Run("Delete feed with episodes", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.CreateFeed(ctx, userID, "feed to be deleted"))(t)
		ep1 := must(svc.CreateEpisode(ctx, "some-media-url", []string{}, "concatenate", userID))(t)
		if err = svc.PublishEpisodes(ctx, []string{ep1.ID}, []string{feed.ID}, userID); err != nil {
			t.Fatalf("error publishing episode1: %v", err)
		}
		ep2 := must(svc.CreateEpisode(ctx, "some-media-url", []string{}, "concatenate", userID))(t)
		if err = svc.PublishEpisodes(ctx, []string{ep2.ID}, []string{feed.ID}, userID); err != nil {
			t.Fatalf("error publishing episode2: %v", err)
		}

		if err = svc.DeleteFeed(ctx, feed.ID, userID, true); err != nil {
			t.Fatalf("error deleting feed with episodes: %v", err)
		}

		feeds := must(svc.ListFeeds(ctx, userID))(t)
		if len(feeds) != 1 { // default feed is still there
			t.Fatalf("expected 1 feed, got %d", len(feeds))
		}

		episodes := must(svc.ListEpisodes(ctx, userID))(t)
		if len(episodes) != 0 {
			t.Fatalf("expected 0 episodes, got %d", len(episodes))
		}

		feedWasDeleted := false
		ep1WasDeleted := false
		ep2WasDeleted := false
		for _, call := range mockedS3Store.DeleteCalls() {
			switch {
			case call.Key == userID+"/feeds/2":
				feedWasDeleted = true
			case strings.Contains(ep1.URL, call.Key):
				ep1WasDeleted = true
			case strings.Contains(ep2.URL, call.Key):
				ep2WasDeleted = true
			default:
			}
		}
		if !feedWasDeleted {
			t.Fatalf("expected feed to be deleted from s3 store, but it wasn't")
		}
		if !ep1WasDeleted {
			t.Fatalf("expected episode1 to be deleted from s3 store, but it wasn't")
		}
		if !ep2WasDeleted {
			t.Fatalf("expected episode2 to be deleted from s3 store, but it wasn't")
		}
	})

	t.Run("Default feed can not be deleted", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.DefaultFeed(ctx, userID))(t)

		if err = svc.DeleteFeed(ctx, feed.ID, userID, false); err == nil {
			t.Fatalf("expected error deleting default feed, got nil")
		}
	})
}

func must[R any](result R, err error) func(t *testing.T) R {
	return func(t *testing.T) R {
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		return result
	}
}
