package auth

import (
	"context"

	"github.com/hori-ryota/zaperr"
	"go.uber.org/zap"
)

type User struct {
	ID string
}

type Repository interface {
	AddUser(ctx context.Context, user *User) error
	GetUser(ctx context.Context, userID string) (*User, error)
}

func New(adminUsername string, repository Repository, logger *zap.Logger) *Service {
	return &Service{
		adminUsername: adminUsername,
		repository:    repository,
		logger:        logger,
	}
}

type Service struct {
	adminUsername string
	repository    Repository
	logger        *zap.Logger
}

func (auth *Service) AddUser(ctx context.Context, userID string) error {
	user := &User{ID: userID}
	if err := auth.repository.AddUser(ctx, user); err != nil {
		return zaperr.Wrap(err, "failed to add user")
	}
	return nil
}

func (auth *Service) IsAuthenticated(ctx context.Context, userID string, username string) (bool, error) {
	if isAdmin, err := auth.IsAdmin(ctx, username); err != nil {
		return false, zaperr.Wrap(err, "error while checking if user is admin")
	} else if isAdmin {
		return true, nil
	}

	if user, err := auth.repository.GetUser(ctx, userID); err != nil {
		return false, zaperr.Wrap(err, "failed to get user")
	} else {
		return user != nil, nil
	}
}

func (auth *Service) IsAdmin(_ context.Context, username string) (bool, error) {
	return username == auth.adminUsername, nil
}
