package bot

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v8"
)

type RedisStore struct {
	redisClient *redis.Client
	namespace   string
}

func NewRedisStore(redisClient *redis.Client, namespace string) *RedisStore {
	return &RedisStore{
		redisClient: redisClient,
		namespace:   namespace,
	}
}

func (rs *RedisStore) SetChatID(ctx context.Context, userID string, chatID int64) error {
	return rs.redisClient.HSet(ctx, rs.chatIDsKey(), userID, chatID).Err()
}

func (rs *RedisStore) GetChatID(ctx context.Context, userID string) (int64, error) {
	chatID, err := rs.redisClient.HGet(ctx, rs.chatIDsKey(), userID).Int64()
	if err != nil {
		return -1, err
	}
	return chatID, nil
}

func (rs *RedisStore) chatIDsKey() string {
	return fmt.Sprintf("%s:chat_ids", rs.namespace)
}
