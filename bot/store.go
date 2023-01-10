package bot

import (
	"context"
	"fmt"

	"github.com/go-redis/redis"
)

type RedisStore struct {
	redisClient *redis.Client
	keyPrefix   string
}

func NewRedisStore(redisClient *redis.Client, keyPrefix string) *RedisStore {
	return &RedisStore{
		redisClient: redisClient,
		keyPrefix:   keyPrefix,
	}
}

func (rs *RedisStore) SetChatID(ctx context.Context, userID string, chatID int64) error {
	redisClient := rs.redisClient.WithContext(ctx)
	return redisClient.HSet(rs.chatIDsKey(), userID, chatID).Err()
}

func (rs *RedisStore) GetChatID(ctx context.Context, userID string) (int64, error) {
	redisClient := rs.redisClient.WithContext(ctx)
	chatID, err := redisClient.HGet(rs.chatIDsKey(), userID).Int64()
	if err != nil {
		return -1, err
	}
	return chatID, nil
}

func (rs *RedisStore) chatIDsKey() string {
	return fmt.Sprintf("%s:chat_ids", rs.keyPrefix)
}
