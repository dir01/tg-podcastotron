package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/go-redis/redis"
)

func NewRepository(redisClient *redis.Client, keyPrefix string) *Repository {
	return &Repository{redisClient: redisClient, keyPrefix: keyPrefix}
}

type Repository struct {
	redisClient *redis.Client
	keyPrefix   string
}

const defaultFeedID = 1

func (repo *Repository) SaveEpisode(ctx context.Context, episode *Episode) (*Episode, error) {
	redisClient := repo.redisClient.WithContext(ctx)

	if episode.ID == "" {
		episodeID, err := repo.nextEpisodeID(ctx, episode.UserID)
		if err != nil {
			return nil, fmt.Errorf("failed to generate episode id: %w", err)
		}
		episode.ID = strconv.FormatInt(episodeID, 10)
	}

	episodeKey := repo.episodeKey(episode.ID, episode.UserID)
	episodeBytes, err := repo.episodeToJSON(episode)
	if err != nil {
		return nil, fmt.Errorf("failed to save episode: %w", err)
	}
	if err := redisClient.Set(episodeKey, episodeBytes, 0).Err(); err != nil {
		return nil, fmt.Errorf("failed to save episode: %w", err)
	}

	userEpisodesKey := repo.userEpisodesKey(episode.UserID)
	if err := redisClient.SAdd(userEpisodesKey, episode.ID).Err(); err != nil {
		return nil, fmt.Errorf("failed add episode to user episodes: %w", err)
	}

	return episode, nil
}

func (repo *Repository) SaveFeed(ctx context.Context, feed *Feed) (*Feed, error) {
	redisClient := repo.redisClient.WithContext(ctx)

	if feed.ID == "" {
		feedID, err := repo.nextFeedID(ctx, feed.UserID)
		if err != nil {
			return nil, fmt.Errorf("failed to generate feed id: %w", err)
		}
		feed.ID = strconv.FormatInt(feedID, 10)
	}

	feedKey := repo.feedKey(feed.ID, feed.UserID)
	feedJSON, err := repo.feedToJSON(feed)
	if err != nil {
		return nil, fmt.Errorf("failed to save feed: %w", err)
	}
	if err := redisClient.Set(feedKey, feedJSON, 0).Err(); err != nil {
		return nil, fmt.Errorf("failed to save feed: %w", err)
	}

	userFeedsKey := repo.userFeedsKey(feed.UserID)
	if err := redisClient.SAdd(userFeedsKey, feed.ID).Err(); err != nil {
		return nil, fmt.Errorf("failed add feed to user feeds: %w", err)
	}

	return feed, nil
}

func (repo *Repository) GetFeed(ctx context.Context, feedID string, userID string) (*Feed, error) {
	redisClient := repo.redisClient.WithContext(ctx)

	feedKey := repo.feedKey(feedID, userID)

	rawFeed, err := redisClient.Get(feedKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get feed: %w", err)
	}

	return repo.feedFromJSON([]byte(rawFeed))
}

func (repo *Repository) GetEpisode(ctx context.Context, episodeID string, userID string) (*Episode, error) {
	redisClient := repo.redisClient.WithContext(ctx)

	episodeKey := repo.episodeKey(episodeID, userID)

	rawEpisode, err := redisClient.Get(episodeKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get episode: %w", err)
	}

	return repo.episodeFromJSON([]byte(rawEpisode))
}

func (repo *Repository) ListUserEpisodes(ctx context.Context, userID string) ([]*Episode, error) {
	redisClient := repo.redisClient.WithContext(ctx)

	userEpisodesKey := repo.userEpisodesKey(userID)

	episodeIDs, err := redisClient.SMembers(userEpisodesKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list user episode ids: %w", err)
	}

	if len(episodeIDs) == 0 {
		return []*Episode{}, nil
	}

	rawEpisodes, err := redisClient.MGet(repo.episodeKeySlice(episodeIDs, userID)...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list user episodes: %w", err)
	}
	episodes := make([]*Episode, len(rawEpisodes))
	for i, rawEpisode := range rawEpisodes {
		episodes[i], err = repo.episodeFromJSON([]byte(rawEpisode.(string)))
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal episode: %w", err)
		}
	}

	return episodes, nil
}

func (repo *Repository) ListUserFeeds(ctx context.Context, userID string) ([]*Feed, error) {
	redisClient := repo.redisClient.WithContext(ctx)

	userFeedsKey := repo.userFeedsKey(userID)

	feedIDs, err := redisClient.SMembers(userFeedsKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list user feed ids: %w", err)
	}

	if len(feedIDs) == 0 {
		return []*Feed{}, nil
	}

	rawFeeds, err := redisClient.MGet(repo.feedKeySlice(feedIDs, userID)...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list user feeds: %w", err)
	}
	feeds := make([]*Feed, len(rawFeeds))
	for i, rawFeed := range rawFeeds {
		feeds[i], err = repo.feedFromJSON([]byte(rawFeed.(string)))
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal feed: %w", err)
		}
	}

	return feeds, nil
}

func (repo *Repository) ListFeedEpisodes(ctx context.Context, feedID string) ([]*Episode, error) {
	panic("implement me")
}

func (repo *Repository) ListEpisodeFeeds(ctx context.Context, episodeID string) ([]*Feed, error) {
	panic("implement me")
}

func (repo *Repository) UnPublishEpisode(ctx context.Context, episodeID string, feedID string) error {
	panic("implement me")
}

// region ids
func (repo *Repository) nextEpisodeID(ctx context.Context, userID string) (int64, error) {
	redisClient := repo.redisClient.WithContext(ctx)

	userEpisodeIDsKey := repo.userEpisodeIDsKey(userID)

	id, err := redisClient.Incr(userEpisodeIDsKey).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get next episode id: %w", err)
	}

	return id, nil
}

func (repo *Repository) nextFeedID(ctx context.Context, userID string) (int64, error) {
	redisClient := repo.redisClient.WithContext(ctx)

	userFeedIDsKey := repo.userFeedIDsKey(userID)

	var id int64 = -1
	var err error
	for id <= defaultFeedID {
		id, err = redisClient.Incr(userFeedIDsKey).Result()
		if err != nil {
			return 0, fmt.Errorf("failed to get next episode id: %w", err)
		}
	}

	return id, nil
}

// endregion

// region key helpers
func (repo *Repository) userEpisodesKey(userID string) string {
	return repo.keyPrefix + ":user:episodes:" + userID
}

func (repo *Repository) episodeKey(episodeID string, userID string) string {
	return repo.keyPrefix + ":episode:" + episodeID
}

func (repo *Repository) userEpisodeIDsKey(userID string) string {
	return repo.keyPrefix + ":user-episode-ids:" + userID
}

func (repo *Repository) userFeedIDsKey(userID string) string {
	return repo.keyPrefix + ":user-feed-ids:" + userID
}

func (repo *Repository) episodeKeySlice(ids []string, userID string) []string {
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = repo.episodeKey(id, userID)
	}
	return keys
}

func (repo *Repository) userFeedsKey(userID string) string {
	return repo.keyPrefix + ":user:feeds:" + userID
}

func (repo *Repository) feedKey(feedID string, userID string) string {
	return fmt.Sprintf("%s:feed:%s:%s", repo.keyPrefix, userID, feedID)
}

func (repo *Repository) feedKeySlice(feedIDs []string, userID string) []string {
	keys := make([]string, len(feedIDs))
	for i, id := range feedIDs {
		keys[i] = repo.feedKey(id, userID)
	}
	return keys
}

// endregion

// region json helpers
func (repo *Repository) episodeToJSON(episode *Episode) ([]byte, error) {
	episodeBytes, err := json.Marshal(episode)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal episode: %w", err)
	}
	return episodeBytes, nil
}

func (repo *Repository) episodeFromJSON(bytes []byte) (*Episode, error) {
	var episode Episode
	if err := json.Unmarshal(bytes, &episode); err != nil {
		return nil, fmt.Errorf("failed to unmarshal episode: %w", err)
	}
	return &episode, nil
}

func (repo *Repository) feedToJSON(feed *Feed) ([]byte, error) {
	feedBytes, err := json.Marshal(feed)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal feed: %w", err)
	}
	return feedBytes, nil
}

func (repo *Repository) feedFromJSON(bytes []byte) (*Feed, error) {
	var feed Feed
	if err := json.Unmarshal(bytes, &feed); err != nil {
		return nil, fmt.Errorf("failed to unmarshal feed: %w", err)
	}
	return &feed, nil
}

// endregion
