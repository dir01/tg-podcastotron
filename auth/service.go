package auth

import (
	"context"
	"fmt"
	"log/slog"
)

type User struct {
	ID string
}

type Repository interface {
	AddUser(ctx context.Context, user *User) error
	GetUser(ctx context.Context, userID string) (*User, error)
}

func New(adminUsername string, repository Repository, logger *slog.Logger) *Service {
	return &Service{
		adminUsername: adminUsername,
		repository:    repository,
		logger:        logger,
	}
}

type Service struct {
	adminUsername string
	repository    Repository
	logger        *slog.Logger
}

func (auth *Service) AddUser(ctx context.Context, userID string) error {
	user := &User{ID: userID}
	if err := auth.repository.AddUser(ctx, user); err != nil {
		return fmt.Errorf("failed to add user: %w", err)
	}
	return nil
}

func (auth *Service) IsAuthenticated(ctx context.Context, userID string, username string) (bool, error) {
	if isAdmin, err := auth.IsAdmin(ctx, username); err != nil {
		return false, fmt.Errorf("error while checking if user is admin: %w", err)
	} else if isAdmin {
		return true, nil
	}

	if user, err := auth.repository.GetUser(ctx, userID); err != nil {
		return false, fmt.Errorf("failed to get user: %w", err)
	} else {
		return user != nil, nil
	}
}

func (auth *Service) IsAdmin(_ context.Context, username string) (bool, error) {
	return username == auth.adminUsername, nil
}
