package service

import (
	"context"
	"fmt"
	"time"
)

// EpisodeEvent is one entry in the status history of an episode.
type EpisodeEvent struct {
	UserID      string
	EpisodeID   string
	At          time.Time
	Status      EpisodeStatus
	Attempt     int // 0 is unknown
	MaxAttempts int // 0 is unknown
	// Detail is the reason of a failed attempt.
	Detail string
}

// recordStatusChanges adds the changes to the status history of the episodes.
func (svc *Service) recordStatusChanges(ctx context.Context, changes []EpisodeStatusChange) error {
	now := time.Now().UTC()
	events := make([]*EpisodeEvent, len(changes))
	for i, c := range changes {
		events[i] = &EpisodeEvent{
			UserID:      c.Episode.UserID,
			EpisodeID:   c.Episode.ID,
			At:          now,
			Status:      c.NewStatus,
			Attempt:     c.Attempt,
			MaxAttempts: c.MaxAttempts,
			Detail:      c.Error,
		}
	}
	if err := svc.repository.InsertEpisodeEvents(ctx, events); err != nil {
		return fmt.Errorf("failed to record status changes: %w", err)
	}
	return nil
}

// ListEpisodeEvents returns the status history of the episode, oldest first.
func (svc *Service) ListEpisodeEvents(ctx context.Context, userID, episodeID string) ([]*EpisodeEvent, error) {
	return svc.repository.ListEpisodeEvents(ctx, userID, episodeID)
}
