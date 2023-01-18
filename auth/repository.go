package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-redis/redis"
)

func NewRepository(redisClient *redis.Client, keyPrefix string) Repository {
	return &repository{
		redisClient: redisClient,
		keyPrefix:   keyPrefix,
	}
}

type repository struct {
	redisClient *redis.Client
	keyPrefix   string
}

func (r *repository) AddUser(ctx context.Context, user *User) error {
	userBytes, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("failed to marshal user: %w", err)
	}

	redisClient := r.redisClient.WithContext(ctx)
	if err = redisClient.Set(r.getUserKey(user.ID), userBytes, 0).Err(); err != nil {
		return fmt.Errorf("failed to set user bytes to redis: %w", err)
	}

	return nil
}

func (r *repository) GetUser(ctx context.Context, userID string) (*User, error) {
	redisClient := r.redisClient.WithContext(ctx)

	key := r.getUserKey(userID)

	userBytes, err := redisClient.Get(key).Bytes()
	if err != nil {
		return nil, fmt.Errorf("failed to get user bytes from redis: %w", err)
	}

	user := &User{}
	if err = json.Unmarshal(userBytes, user); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user: %w", err)
	}

	return user, nil
}

func (r *repository) getUserKey(userID string) string {
	prefix := strings.Trim(r.keyPrefix, ":")
	return fmt.Sprintf("%s:user:%s", prefix, userID)
}
