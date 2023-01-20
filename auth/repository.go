package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"
)

func NewRepository(redisClient *redis.Client, namespace string) Repository {
	return &repository{
		redisClient: redisClient,
		namespace:   namespace,
	}
}

type repository struct {
	redisClient *redis.Client
	namespace   string
}

func (r *repository) AddUser(ctx context.Context, user *User) error {
	userBytes, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("failed to marshal user: %w", err)
	}

	if err = r.redisClient.Set(ctx, r.getUserKey(user.ID), userBytes, 0).Err(); err != nil {
		return fmt.Errorf("failed to set user bytes to redis: %w", err)
	}

	return nil
}

func (r *repository) GetUser(ctx context.Context, userID string) (*User, error) {
	key := r.getUserKey(userID)

	userBytes, err := r.redisClient.Get(ctx, key).Bytes()
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
	prefix := strings.Trim(r.namespace, ":")
	return fmt.Sprintf("%s:user:%s", prefix, userID)
}
