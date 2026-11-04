package service

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// SourceLink is a link that a user sent to the bot or pasted in the Mini App.
// The Mini App opens it by its short ID: a magnet link can be too long for a button URL.
type SourceLink struct {
	ID        string
	UserID    string
	URL       string
	CreatedAt time.Time
}

// sourceLinkMaxAge is how long the bot button of a link works.
const sourceLinkMaxAge = 30 * 24 * time.Hour

func (svc *Service) SaveSourceLink(ctx context.Context, userID, url string) (*SourceLink, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, fmt.Errorf("empty url")
	}
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("failed to make link id: %w", err)
	}
	link := &SourceLink{
		ID:        strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])),
		UserID:    userID,
		URL:       url,
		CreatedAt: time.Now().UTC(),
	}
	if err := svc.repository.DeleteSourceLinksBefore(ctx, link.CreatedAt.Add(-sourceLinkMaxAge)); err != nil {
		svc.logger.WarnContext(ctx, "failed to delete old source links", slog.Any("error", err))
	}
	if err := svc.repository.InsertSourceLink(ctx, link); err != nil {
		return nil, err
	}
	return link, nil
}

// GetSourceLink returns the link, or nil if the user has no link with this ID.
func (svc *Service) GetSourceLink(ctx context.Context, userID, id string) (*SourceLink, error) {
	return svc.repository.GetSourceLink(ctx, userID, id)
}
