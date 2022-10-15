package auth

import (
	"context"
)

func New(adminUsername string) *Service {
	return &Service{
		adminUsername: adminUsername,
	}
}

type Service struct {
	adminUsername string
}

func (authService *Service) IsAuthenticated(ctx context.Context, username string) (bool, error) {
	return authService.IsAdmin(ctx, username)
}

func (authService *Service) IsAdmin(ctx context.Context, username string) (bool, error) {
	return username == authService.adminUsername, nil
}
