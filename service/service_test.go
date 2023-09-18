package service_test

import (
	"context"
	"database/sql"
	migrate "github.com/rubenv/sql-migrate"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"go.uber.org/zap"
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

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	repo := service.NewSqliteRepository(db)
	migrations := &migrate.FileMigrationSource{
		Dir: "../db/migrations",
	}
	_, err = migrate.Exec(db, "sqlite3", migrations, migrate.Up)
	if err != nil {
		t.Fatalf("failed to apply migrations: %v", err)
	}

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

		if feed.URL != "https://exapmple.com/feeds/"+userID+"/1" {
			t.Fatalf("expected default feed to have url https://exapmple.com/feeds/"+userID+"/1, got %s", feed.URL)
		}
	})

	t.Run("Create feed", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.CreateFeed(ctx, userID, "some-feed"))(t)
		if feed.ID != "2" {
			t.Fatalf("expected feed to have id 2, got %s", feed.ID)
		}

		if feed.URL != "https://exapmple.com/feeds/"+userID+"/2" {
			t.Fatalf("expected feed to have url https://exapmple.com/feeds/"+userID+"/2, got %s", feed.URL)
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
		ep := must(svc.CreateEpisode(ctx, userID, "some-media-url", []string{}, "concatenate"))(t)

		defaultFeed := must(svc.DefaultFeed(ctx, userID))(t)

		if err = svc.PublishEpisodes(ctx, userID, []string{ep.ID}, []string{defaultFeed.ID}); err != nil {
			t.Fatalf("error publishing episode: %v", err)
		}
		// endregion

		// region Validate that feed contains episode
		defaultFeed = must(svc.DefaultFeed(ctx, userID))(t)
		episodes := must(svc.ListFeedEpisodes(ctx, userID, defaultFeed.ID))(t)
		if len(episodes) != 1 {
			t.Fatalf("expected default feed of user-1 to have 1 episode, got %d", len(episodes))
		}
		if episodes[0].ID != ep.ID {
			t.Fatalf("expected default feed of user-1 to have episode %s, got %s", ep.ID, episodes[0].ID)
		}
		// endregion
	})

	t.Run("Double publish is not allowed", func(t *testing.T) {
		userID := mkUserID()

		// region Create and publish twice
		ep := must(svc.CreateEpisode(ctx, userID, "some-media-url", []string{}, "concatenate"))(t)

		defaultFeed := must(svc.DefaultFeed(ctx, userID))(t)

		for i := 0; i < 2; i++ {
			if err = svc.PublishEpisodes(ctx, userID, []string{ep.ID}, []string{defaultFeed.ID}); err != nil {
				t.Fatalf("error publishing episode: %v", err)
			}
		}
		// endregion

		// region Validate that episode is only in feed once
		defaultFeed = must(svc.DefaultFeed(ctx, userID))(t)
		episodes := must(svc.ListFeedEpisodes(ctx, userID, defaultFeed.ID))(t)
		if len(episodes) != 1 {
			t.Fatalf("expected default feed of user-1 to have 1 episode, got %d", len(episodes))
		}
		// endregion
	})

	t.Run("Publish episodes to several feeds", func(t *testing.T) {
		userID := mkUserID()

		feed1 := must(svc.CreateFeed(ctx, userID, "first feed of user-1"))(t)
		feed2 := must(svc.CreateFeed(ctx, userID, "second feed of user-1"))(t)

		// region Create and publish 10 episodes feed1 and feed2
		episodeIDs := make([]string, 10)
		for i := 0; i < 10; i++ {
			ep := must(svc.CreateEpisode(ctx, userID, "some-media-url", []string{}, "concatenate"))(t)

			var f *service.Feed
			if i%2 == 0 {
				f = feed1
			} else {
				f = feed2
			}

			if err = svc.PublishEpisodes(ctx, userID, []string{ep.ID}, []string{f.ID}); err != nil {
				t.Fatalf("error publishing episode: %v", err)
			}

			episodeIDs[i] = ep.ID
		}
		// endregion

		// region Prepare feed3 with one existing episode
		feed3 := must(svc.CreateFeed(ctx, userID, "third feed of user-1"))(t)
		feed3ep := must(svc.CreateEpisode(ctx, userID, "some-media-url", []string{}, "concatenate"))(t)
		if err = svc.PublishEpisodes(ctx, userID, []string{feed3ep.ID}, []string{feed3.ID}); err != nil {
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
		if err = svc.PublishEpisodes(ctx, userID, episodeIDs, []string{feed2.ID, feed3.ID}); err != nil {
			t.Fatalf("error setting episodes feeds: %v", err)
		}
		// endregion

		// region Validate that feed1 has no episodes
		feed1Episodes := must(svc.ListFeedEpisodes(ctx, userID, feed1.ID))(t)
		if len(feed1Episodes) != 0 {
			t.Fatalf("expected feed1 to have 0 episodes, got %d", len(feed1Episodes))
		}
		// endregion

		// region Validate that feed2 has 10 episodes
		feed2Episodes := must(svc.ListFeedEpisodes(ctx, userID, feed2.ID))(t)
		if len(feed2Episodes) != 10 {
			t.Fatalf("expected feed2 to have 10 episodes, got %d", len(feed2Episodes))
		}
		// endregion

		// region Validate that feed3 has 11 episodes
		feed3Episodes := must(svc.ListFeedEpisodes(ctx, userID, feed3.ID))(t)
		if len(feed3Episodes) != 11 {
			t.Fatalf("expected feed3 to have 11 episodes, got %d", len(feed3Episodes))
		}
		// endregion

		//region Validate that episodes order did not change
		expectedIDs := append([]string{feed3ep.ID}, episodeIDs...)

		var realIDs []string
		for _, ep := range feed3Episodes {
			realIDs = append(realIDs, ep.ID)
		}

		if !reflect.DeepEqual(expectedIDs, realIDs) {
			t.Fatalf("expected feed3 episode IDs to be %v, got %v", expectedIDs, realIDs)
		}
		//endregion
	})

	t.Run("Rename default feed", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.DefaultFeed(ctx, userID))(t)

		if err = svc.RenameFeed(ctx, userID, feed.ID, "new title"); err != nil {
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

		if err = svc.DeleteFeed(ctx, userID, feed.ID, false); err != nil {
			t.Fatalf("error deleting feed: %v", err)
		}

		feeds := must(svc.ListFeeds(ctx, userID))(t)
		if len(feeds) != 1 { // default feed is still there
			t.Fatalf("expected 1 feed, got %d", len(feeds))
		}

		feedWasDeleted := false
		for _, call := range mockedS3Store.DeleteCalls() {
			if call.Key == "feeds/"+userID+"/2" {
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
		ep1 := must(svc.CreateEpisode(ctx, userID, "some-media-url", []string{}, "concatenate"))(t)
		if err = svc.PublishEpisodes(ctx, userID, []string{ep1.ID}, []string{feed.ID}); err != nil {
			t.Fatalf("error publishing episode1: %v", err)
		}

		if err = svc.DeleteFeed(ctx, userID, feed.ID, false); err != nil {
			t.Fatalf("error deleting feed: %v", err)
		}

		episodes := must(svc.ListUserEpisodes(ctx, userID))(t)
		if len(episodes) != 1 {
			t.Fatalf("expected 1 episode, got %d", len(episodes))
		}
	})

	t.Run("Delete feed with episodes", func(t *testing.T) {
		userID := mkUserID()

		feed := must(svc.CreateFeed(ctx, userID, "feed to be deleted"))(t)

		ep1 := must(svc.CreateEpisode(ctx, userID, "some-media-url", []string{}, "concatenate"))(t)
		if err = svc.PublishEpisodes(ctx, userID, []string{ep1.ID}, []string{feed.ID}); err != nil {
			t.Fatalf("error publishing episode1: %v", err)
		}

		ep2 := must(svc.CreateEpisode(ctx, userID, "some-media-url", []string{}, "concatenate"))(t)
		if err = svc.PublishEpisodes(ctx, userID, []string{ep2.ID}, []string{feed.ID}); err != nil {
			t.Fatalf("error publishing episode2: %v", err)
		}

		if err = svc.DeleteFeed(ctx, userID, feed.ID, true); err != nil {
			t.Fatalf("error deleting feed with episodes: %v", err)
		}

		feeds := must(svc.ListFeeds(ctx, userID))(t)
		if len(feeds) != 1 { // default feed is still there
			t.Fatalf("expected 1 feed, got %d", len(feeds))
		}

		episodes := must(svc.ListUserEpisodes(ctx, userID))(t)
		if len(episodes) != 0 {
			t.Fatalf("expected 0 episodes, got %d", len(episodes))
		}

		feedWasDeleted := false
		ep1WasDeleted := false
		ep2WasDeleted := false
		for _, call := range mockedS3Store.DeleteCalls() {
			switch {
			case call.Key == "feeds/"+userID+"/2":
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

		if err = svc.DeleteFeed(ctx, userID, feed.ID, false); err != nil {
			t.Fatalf("expected no error deleting default feed, got %v", err)
		}
	})

	t.Run("Delete episodes with missing IDs is allowed", func(t *testing.T) {
		userID := mkUserID()

		ep := must(svc.CreateEpisode(ctx, userID, "some-media-url", []string{}, "concatenate"))(t)

		epMap := must(svc.GetEpisodesMap(ctx, userID, []string{ep.ID}))(t)
		if len(epMap) != 1 || epMap[ep.ID] == nil {
			t.Fatalf("expected episode to be found, but it wasn't")
		}

		if err = svc.DeleteEpisodes(ctx, userID, []string{"missing-id", ep.ID}); err != nil {
			t.Fatalf("expected no error deleting episodes, got %v", err)
		}

		epMap = must(svc.GetEpisodesMap(ctx, userID, []string{ep.ID}))(t)
		if len(epMap) != 0 {
			t.Fatalf("expected episode to be deleted, but it wasn't")
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
