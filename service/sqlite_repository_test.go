package service

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/rubenv/sql-migrate"

	_ "github.com/mattn/go-sqlite3"
)

// TestSqliteRepository__NextIDs tests NextEpisodeID and NextFeedID
func TestSqliteRepository__UserLocalIDs(t *testing.T) {
	repo := getRepo(t)

	// region first episode id is 1
	id, err := repo.NextEpisodeID(context.TODO(), "user1")
	if err != nil {
		t.Fatal(err)
	}
	if id != "1" {
		t.Errorf("expected new episode id to be 1, got %s", id)
	}
	// endregion

	// region first feed id is 1
	id, err = repo.NextFeedID(context.TODO(), "user1")
	if err != nil {
		t.Fatal(err)
	}
	if id != "1" {
		t.Errorf("expected new feed id to be 1, got %s", id)
	}
	// endregion

	// region second NextEpisodeID is 2
	id, err = repo.NextEpisodeID(context.TODO(), "user1")
	if err != nil {
		t.Fatal(err)
	}
	if id != "2" {
		t.Errorf("expected second episode id to be 2, got %s", id)
	}
	// endregion

	// region second NextFeedID is 2
	id, err = repo.NextFeedID(context.TODO(), "user1")
	if err != nil {
		t.Fatal(err)
	}
	if id != "2" {
		t.Errorf("expected second feed id to be 2, got %s", id)
	}
	// endregion
}

func TestSqliteRepository__Feeds(t *testing.T) {
	repo := getRepo(t)

	feed1 := &Feed{
		ID:     "feed1-id",
		UserID: "some-user-id",
		Title:  "some-feed1-title",
		URL:    "some-feed1-url",
	}

	// region save feed1
	feed1, err := repo.SaveFeed(context.TODO(), feed1)
	if err != nil {
		t.Fatal(err)
	}
	// endregion

	// region get feed1
	f, err := repo.GetFeed(context.TODO(), "some-user-id", "feed1-id")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(feed1, f) {
		t.Errorf("original feed1 is \n%+v\nloaded feed1 is \n%+v", feed1, f)
	}
	// endregion

	// region update feed1
	feed1.Title = "some-updated-title"
	feed1.URL = "some-updated-url"
	_, err = repo.SaveFeed(context.TODO(), feed1)
	if err != nil {
		t.Fatal(err)
	}
	// endregion

	// region get updated feed1
	f, err = repo.GetFeed(context.TODO(), "some-user-id", "feed1-id")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(feed1, f) {
		t.Errorf("original updated feed1 is\n%v\nloaded updated feed1 is\n%v", feed1, f)
	}
	// endregion

	// region save feed2
	var feed2 *Feed
	{
		temp := *feed1
		temp.ID = "feed2-id"
		feed2 = &temp
	}
	if _, err := repo.SaveFeed(context.TODO(), feed2); err != nil {
		t.Fatal(err)
	}
	// endregion

	// region get feeds map
	feedMap, err := repo.GetFeedsMap(context.TODO(), "some-user-id", []string{"feed1-id", "feed2-id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(feedMap) != 2 {
		t.Fatalf("expected 2 feeds in map, got %d", len(feedMap))
	}
	expectedFeedMap := map[string]*Feed{
		"feed1-id": feed1,
		"feed2-id": feed2,
	}
	if !reflect.DeepEqual(expectedFeedMap, feedMap) {
		t.Errorf("expected feedMap to be\n%v\n, got\n%v", expectedFeedMap, feedMap)
	}
	// endregion

	// region list user feeds
	feeds, err := repo.ListUserFeeds(context.TODO(), "some-user-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 2 {
		t.Fatalf("expected 1 feed1 in user feeds list, got %d", len(feeds))
	}
	expectedFeeds := []*Feed{feed1, feed2}
	if !reflect.DeepEqual(expectedFeeds, feeds) {
		t.Errorf("expected feeds to be\n%v\n, got\n%v", expectedFeeds, feeds)
	}
	// endregion

	// region delete feed1
	err = repo.DeleteFeed(context.TODO(), "some-user-id", "feed1-id")
	if err != nil {
		t.Fatal(err)
	}
	// endregion

	// region get deleted feed1
	f, err = repo.GetFeed(context.TODO(), "some-user-id", "feed1-id")
	if err != nil {
		t.Fatal(err)
	}
	if f != nil {
		t.Errorf("expected deleted feed1 to be nil, got %v", f)
	}
	// endregion
}

func TestSqliteRepository__Feeds__Empty(t *testing.T) {
	repo := getRepo(t)

	// region get feed
	f, err := repo.GetFeed(context.TODO(), "some-user-id", "some-feed-id")
	if err != nil {
		t.Fatal(err)
	}
	if f != nil {
		t.Errorf("expected feed to be nil, got %v", f)
	}
	// endregion

	// region get feed map
	feedMap, err := repo.GetFeedsMap(context.TODO(), "some-user-id", []string{"some-feed-id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(feedMap) != 0 {
		t.Errorf("expected feed map to be empty, got %v", feedMap)
	}
	// endregion

	// region list user feeds
	feeds, err := repo.ListUserFeeds(context.TODO(), "some-user-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 0 {
		t.Errorf("expected user feeds list to be empty, got %v", feeds)
	}
	// endregion
}

func TestSqliteRepository__Episodes(t *testing.T) {
	repo := getRepo(t)

	episode1 := &Episode{
		ID:              "episode1-id",
		UserID:          "some-user-id",
		Title:           "some-title",
		PubDate:         time.Now().UTC(),
		SourceURL:       "some-source-url",
		SourceFilepaths: []string{"some-source-filepath", "some-other-source-filepath"},
		MediaryID:       "some-mediary-id",
		URL:             "some-url",
		Status:          "some-status",
		Duration:        111,
		FileLenBytes:    222,
		Format:          "some-format",
		StorageKey:      "some-storage-key",
	}
	var episode2 *Episode
	{
		temp := *episode1
		temp.ID = "episode2-id"
		episode2 = &temp
	}

	// region save 2 episodes and publish them to feed
	for _, ep := range []*Episode{episode1, episode2} {
		sep, err := repo.SaveEpisode(context.TODO(), ep)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ep, sep) {
			t.Errorf("original episode is %v, saved episode is %v", ep, sep)
		}

		if err := repo.BulkInsertPublications(context.TODO(), []*Publication{
			{UserID: "some-user-id", FeedID: "some-feed-id", EpisodeID: ep.ID},
			{UserID: "some-user-id", FeedID: "some-other-feed-id", EpisodeID: ep.ID},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// endregion

	// region get episodes map - only those 2 should be returned
	epMap, err := repo.GetEpisodesMap(context.TODO(), "some-user-id", []string{"episode1-id", "episode2-id"})
	if err != nil {
		t.Fatal(err)
	}

	if len(epMap) != 2 {
		t.Fatalf("expected 2 episodes in map, got %d", len(epMap))
	}

	if !reflect.DeepEqual(episode1, epMap["episode1-id"]) {
		t.Errorf("\noriginal episode1:\n%v\nloaded episode1:\n%v\n", episode1, epMap["episode1-id"])
	}
	if !reflect.DeepEqual(episode2, epMap["episode2-id"]) {
		t.Errorf("original episode2:\n%v\nloaded episode2:\n%v\n", episode2, epMap["episode2-id"])
	}
	// endregion

	// region get user episodes - only those 2 should be present, from older to newer
	episodes, err := repo.ListUserEpisodes(context.TODO(), "some-user-id")
	if err != nil {
		t.Fatal(err)
	}

	if len(episodes) != 2 {
		t.Fatalf("expected 1 episode1 in user episodes list, got %d", len(episodes))
	}

	if !reflect.DeepEqual(episode1, episodes[0]) {
		t.Errorf("original episode1 is\n%v\n, loaded episode1 is\n%v\n", episode1, episodes[0])
	}
	if !reflect.DeepEqual(episode2, episodes[1]) {
		t.Errorf("original episode2 is\n%v\n, loaded episode2 is\n%v\n", episode2, episodes[1])
	}
	// endregion

	// region feed episodes - only those 2 should be present, from older to newer
	episodes, err = repo.ListFeedEpisodes(context.TODO(), "some-user-id", "some-feed-id")
	if err != nil {
		t.Fatal(err)
	}

	if len(episodes) != 2 {
		t.Fatalf("expected 2 episode1 in feed episodes list, got %d", len(episodes))
	}

	if !reflect.DeepEqual(episodes, []*Episode{episode1, episode2}) {
		t.Errorf("original episodes are\n%v\n, loaded episodes are\n%v\n", []*Episode{episode1, episode2}, episodes)
	}
	// endregion

	// region delete episodes
	err = repo.DeleteEpisodes(context.TODO(), "some-user-id", []string{"episode1-id", "episode2-id"})
	if err != nil {
		t.Fatal(err)
	}
	// endregion

	// region get episodes map - should be empty
	epMap, err = repo.GetEpisodesMap(context.TODO(), "some-user-id", []string{"episode1-id", "episode2-id"})
	if err != nil {
		t.Fatal(err)
	}

	if len(epMap) != 0 {
		t.Fatalf("expected episodes map to have 0 episodes, got %d", len(epMap))
	}
	// endregion
}

func getRepo(t *testing.T) Repository {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	repo := NewSqliteRepository(db)

	migrations := &migrate.FileMigrationSource{
		Dir: "../db/migrations",
	}
	_, err = migrate.Exec(db, "sqlite3", migrations, migrate.Up)
	if err != nil {
		t.Fatalf("failed to apply migrations: %v", err)
	}

	return repo
}
