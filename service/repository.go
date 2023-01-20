package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/go-redis/redis/v8"
)

func NewRepository(redisClient *redis.Client, namespace string) *Repository {
	return &Repository{redisClient: redisClient, namespace: namespace}
}

type Repository struct {
	redisClient *redis.Client
	namespace   string
}

const defaultFeedID = 1

func (repo *Repository) SaveEpisode(ctx context.Context, episode *Episode) (*Episode, error) {
	if episode.ID == "" {
		episodeID, err := repo.nextEpisodeID(ctx, episode.UserID)
		if err != nil {
			return nil, fmt.Errorf("failed to generate episode id: %w", err)
		}
		episode.ID = strconv.FormatInt(episodeID, 10)
	}

	episodeBytes, err := repo.episodeToJSON(episode)
	if err != nil {
		return nil, fmt.Errorf("failed to save episode: %w", err)
	}
	if err := repo.redisClient.HSet(ctx, repo.episodeMapKey(), repo.episodeFieldKey(episode.UserID, episode.ID), episodeBytes).Err(); err != nil {
		return nil, fmt.Errorf("failed to save episode: %w", err)
	}

	userEpisodesSetKey := repo.userEpisodesSetKey(episode.UserID)
	if err := repo.redisClient.SAdd(ctx, userEpisodesSetKey, episode.ID).Err(); err != nil {
		return nil, fmt.Errorf("failed add episode to user episodes: %w", err)
	}

	return episode, nil
}

func (repo *Repository) SaveFeed(ctx context.Context, feed *Feed) (*Feed, error) {

	if feed.ID == "" {
		feedID, err := repo.nextFeedID(ctx, feed.UserID)
		if err != nil {
			return nil, fmt.Errorf("failed to generate feed id: %w", err)
		}
		feed.ID = strconv.FormatInt(feedID, 10)
	}

	feedJSON, err := repo.feedToJSON(feed)
	if err != nil {
		return nil, fmt.Errorf("failed to save feed: %w", err)
	}
	if err := repo.redisClient.HSet(ctx, repo.feedsMapKey(), repo.feedFieldKey(feed.UserID, feed.ID), feedJSON).Err(); err != nil {
		return nil, fmt.Errorf("failed to save feed: %w", err)
	}

	userFeedsKey := repo.userFeedsKey(feed.UserID)
	if err := repo.redisClient.SAdd(ctx, userFeedsKey, feed.ID).Err(); err != nil {
		return nil, fmt.Errorf("failed add feed to user feeds: %w", err)
	}

	return feed, nil
}

func (repo *Repository) GetFeed(ctx context.Context, feedID string, userID string) (*Feed, error) {
	rawFeed, err := repo.redisClient.HGet(ctx, repo.feedsMapKey(), repo.feedFieldKey(userID, feedID)).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get feed: %w", err)
	}

	return repo.feedFromJSON([]byte(rawFeed))
}

func (repo *Repository) GetEpisode(ctx context.Context, episodeID string, userID string) (*Episode, error) {
	rawEpisode, err := repo.redisClient.HGet(ctx, repo.episodeMapKey(), repo.episodeFieldKey(userID, episodeID)).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get episode: %w", err)
	}

	return repo.episodeFromJSON([]byte(rawEpisode))
}

func (repo *Repository) ListUserEpisodes(ctx context.Context, userID string) ([]*Episode, error) {
	userEpisodesKey := repo.userEpisodesSetKey(userID)

	episodeIDs, err := repo.redisClient.SMembers(ctx, userEpisodesKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list user episode ids: %w", err)
	}

	if len(episodeIDs) == 0 {
		return []*Episode{}, nil
	}

	rawEpisodes, err := repo.redisClient.HMGet(ctx, repo.episodeMapKey(), repo.episodeKeySlice(userID, episodeIDs)...).Result()
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

	userFeedsKey := repo.userFeedsKey(userID)

	feedIDs, err := repo.redisClient.SMembers(ctx, userFeedsKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list user feed ids: %w", err)
	}

	if len(feedIDs) == 0 {
		return []*Feed{}, nil
	}

	rawFeeds, err := repo.redisClient.HMGet(ctx, repo.feedsMapKey(), repo.feedFieldKeysSlice(userID, feedIDs)...).Result()
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

func (repo *Repository) ListFeedEpisodes(ctx context.Context, feed *Feed) ([]*Episode, error) {
	episodesMap, err := repo.GetEpisodesMap(ctx, feed.EpisodeIDs, feed.UserID)
	if err != nil {
		return nil, err
	}

	sortedEpisodes := make([]*Episode, len(feed.EpisodeIDs))
	for i, episodeID := range feed.EpisodeIDs {
		sortedEpisodes[i] = episodesMap[episodeID]
	}

	return sortedEpisodes, nil
}

func (repo *Repository) GetEpisodesMap(ctx context.Context, episodeIDs []string, userID string) (map[string]*Episode, error) {
	rawEpisodes, err := repo.redisClient.HMGet(ctx, repo.episodeMapKey(), repo.episodeKeySlice(userID, episodeIDs)...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list feed episodes: %w", err)
	}

	episodes := make([]*Episode, len(rawEpisodes))
	for i, rawEpisode := range rawEpisodes {
		if rawEpisode == nil {
			return nil, fmt.Errorf("failed to get episode: %w", err)
		}
		episodes[i], err = repo.episodeFromJSON([]byte(rawEpisode.(string)))
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal episode: %w", err)
		}
	}

	episodesMap := make(map[string]*Episode, len(episodes))
	for _, episode := range episodes {
		episodesMap[episode.ID] = episode
	}
	return episodesMap, nil
}

func (repo *Repository) GetFeedsMap(ctx context.Context, feedIDs []string, userID string) (map[string]*Feed, error) {
	feedKeys := make([]string, len(feedIDs))
	for i, fID := range feedIDs {
		feedKeys[i] = repo.feedFieldKey(userID, fID)
	}

	rawFeeds, err := repo.redisClient.HMGet(ctx, repo.feedsMapKey(), feedKeys...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list feed episodes: %w", err)
	}

	feeds := make([]*Feed, len(rawFeeds))
	for i, rawFeed := range rawFeeds {
		feeds[i], err = repo.feedFromJSON([]byte(rawFeed.(string)))
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal feed: %w", err)
		}
	}

	feedsMap := make(map[string]*Feed, len(feeds))
	for _, feed := range feeds {
		feedsMap[feed.ID] = feed
	}

	return feedsMap, nil
}

func (repo *Repository) DeleteEpisodes(ctx context.Context, episodeIDs []string, userID string) error {

	episodeKeys := make([]string, len(episodeIDs))
	for i, eID := range episodeIDs {
		episodeKeys[i] = repo.episodeFieldKey(userID, eID)
	}

	_, err := repo.redisClient.HDel(ctx, repo.episodeMapKey(), episodeKeys...).Result()
	if err != nil {
		return fmt.Errorf("failed to delete episodes: %w", err)
	}

	setEpisodeIDs := make([]interface{}, len(episodeIDs))
	for _, eID := range episodeIDs {
		setEpisodeIDs = append(setEpisodeIDs, eID)
	}

	if err = repo.redisClient.SRem(ctx, repo.userEpisodesSetKey(userID), setEpisodeIDs...).Err(); err != nil {
		return fmt.Errorf("failed to delete user episodes: %w", err)
	}

	return nil
}

// region ids
func (repo *Repository) nextEpisodeID(ctx context.Context, userID string) (int64, error) {
	id, err := repo.redisClient.HIncrBy(ctx, repo.userEpisodeIDsCounterKey(), userID, 1).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get next episode id: %w", err)
	}

	return id, nil
}

func (repo *Repository) nextFeedID(ctx context.Context, userID string) (int64, error) {

	var id int64 = -1
	var err error
	for id <= defaultFeedID {
		id, err = repo.redisClient.HIncrBy(ctx, repo.userFeedIDsCounterKey(), userID, 1).Result()
		if err != nil {
			return 0, fmt.Errorf("failed to get next episode id: %w", err)
		}
	}

	return id, nil
}

// endregion

// region key helpers
func (repo *Repository) userEpisodesSetKey(userID string) string {
	return repo.namespace + ":user:episodes:" + userID
}

func (repo *Repository) episodeMapKey() string {
	return repo.namespace + ":episode"
}

func (repo *Repository) episodeFieldKey(userID, episodeID string) string {
	return fmt.Sprintf("%s:%s", userID, episodeID)
}

func (repo *Repository) userEpisodeIDsCounterKey() string {
	return repo.namespace + ":user-episode-ids"
}

func (repo *Repository) userFeedIDsCounterKey() string {
	return repo.namespace + ":user-feed-ids"
}

func (repo *Repository) episodeKeySlice(userID string, ids []string) []string {
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = repo.episodeFieldKey(userID, id)
	}
	return keys
}

func (repo *Repository) userFeedsKey(userID string) string {
	return repo.namespace + ":user:feeds:" + userID
}

func (repo *Repository) feedsMapKey() string {
	return fmt.Sprintf("%s:feeds", repo.namespace)
}

func (repo *Repository) feedFieldKey(userID, feedID string) string {
	return fmt.Sprintf("%s:%s", userID, feedID)
}

func (repo *Repository) feedFieldKeysSlice(userID string, feedIDs []string) []string {
	keys := make([]string, len(feedIDs))
	for i, fID := range feedIDs {
		keys[i] = repo.feedFieldKey(userID, fID)
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
