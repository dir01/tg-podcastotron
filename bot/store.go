package bot

import (
	"context"
	"strconv"

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

func (rs *RedisStore) SetChatID(ctx context.Context, userID string, chatID int) error {
	redisClient := rs.redisClient.WithContext(ctx)
	return redisClient.Set(rs.chatIDKey(userID), chatID, 0).Err()
}

func (rs *RedisStore) GetChatID(ctx context.Context, userID string) (int, error) {
	redisClient := rs.redisClient.WithContext(ctx)
	chatIDStr, err := redisClient.Get(rs.chatIDKey(userID)).Result()
	if err != nil {
		return -1, err
	}
	return strconv.Atoi(chatIDStr)
}

func (rs *RedisStore) chatIDKey(userID string) string {
	return userID
}
