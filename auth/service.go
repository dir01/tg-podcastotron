package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type User struct {
	ID string
}

// Invite is a one-time link that gives access to a new user.
type Invite struct {
	Token     string
	CreatedBy string
	CreatedAt time.Time
}

// InviteMaxAge is how long an invite link works.
const InviteMaxAge = 7 * 24 * time.Hour

// ErrInviteNotValid means that the invite does not exist, was used, or expired.
var ErrInviteNotValid = errors.New("invite is not valid")

type Repository interface {
	AddUser(ctx context.Context, user *User) error
	GetUser(ctx context.Context, userID string) (*User, error)
	InsertInvite(ctx context.Context, invite *Invite) error
	// UseInvite marks the invite as used by userID. It returns false if the invite
	// does not exist, was used, or was created before notBefore.
	UseInvite(ctx context.Context, token, userID string, usedAt, notBefore time.Time) (bool, error)
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
	return username != "" && username == auth.adminUsername, nil
}

// CreateInvite makes a new invite link token.
func (auth *Service) CreateInvite(ctx context.Context, createdBy string) (*Invite, error) {
	var b [15]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("failed to make invite token: %w", err)
	}
	invite := &Invite{
		// Telegram allows only A-Z, a-z, 0-9, _ and - in a /start parameter.
		Token:     strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])),
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}
	if err := auth.repository.InsertInvite(ctx, invite); err != nil {
		return nil, fmt.Errorf("failed to save invite: %w", err)
	}
	return invite, nil
}

// AcceptInvite gives access to userID. It returns ErrInviteNotValid if the invite
// does not exist, was used, or expired.
func (auth *Service) AcceptInvite(ctx context.Context, token, userID string) error {
	now := time.Now().UTC()
	ok, err := auth.repository.UseInvite(ctx, token, userID, now, now.Add(-InviteMaxAge))
	if err != nil {
		return fmt.Errorf("failed to use invite: %w", err)
	}
	if !ok {
		return ErrInviteNotValid
	}
	return auth.AddUser(ctx, userID)
}
