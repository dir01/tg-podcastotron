package service

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	tests "undercast-bot/testutils"
)

func TestRepository(t *testing.T) {
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

	repo := NewRepository(redisClient, "some-prefix")
	var _id int
	nextID := func() string {
		_id++
		return fmt.Sprintf("fake-id-%d", _id)
	}

	t.Run("SaveEpisode + ListUserEpisodes", func(t *testing.T) {
		episodeID := nextID()
		userID := nextID()

		episode := &Episode{
			ID:     episodeID,
			UserID: userID,
		}

		_, err := repo.SaveEpisode(ctx, episode)
		if err != nil {
			t.Fatalf("error saving episode: %v", err)
		}

		ep, err := repo.ListUserEpisodes(ctx, userID)
		if err != nil {
			t.Fatalf("error listing user episodes: %v", err)
		}
		if len(ep) != 1 {
			t.Fatalf("expected 1 episode, got %d", len(ep))
		}
		if !reflect.DeepEqual(ep[0], episode) {
			t.Fatalf("expected episode to be %+v, got %+v", episode, ep[0])
		}
	})

	t.Run("SaveFeed + ListUserFeeds", func(t *testing.T) {
		feedID := nextID()
		userID := nextID()

		f := &Feed{
			ID:     feedID,
			UserID: userID,
		}

		_, err := repo.SaveFeed(ctx, f)
		if err != nil {
			t.Fatalf("error saving feed: %v", err)
		}

		feeds, err := repo.ListUserFeeds(ctx, userID)
		if err != nil {
			t.Fatalf("error listing user feeds: %v", err)
		}
		if len(feeds) != 1 {
			t.Fatalf("expected 1 feed, got %d", len(feeds))
		}
		if !reflect.DeepEqual(feeds[0], f) {
			t.Fatalf("expected feed to be %+v, got %+v", f, feeds[0])
		}
	})
}
