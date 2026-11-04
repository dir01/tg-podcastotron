package service

import (
	"context"
	"log/slog"
	"time"
)

// EpisodeMaxAge is the time after the last change of an episode when the episode
// is deleted, unless it is in a permanent feed.
const EpisodeMaxAge = 30 * 24 * time.Hour

// ExpiresAt returns the time when the cleanup deletes the episode. It returns false
// if the episode is in a permanent feed and stays. feeds must have the feeds of the episode.
func ExpiresAt(ep *Episode, feeds map[string]*Feed) (time.Time, bool) {
	for _, id := range ep.FeedIDs {
		if f, ok := feeds[id]; ok && f.IsPermanent {
			return time.Time{}, false
		}
	}
	return ep.UpdatedAt.Add(EpisodeMaxAge), true
}

// RunExpiredEpisodesCleanup deletes expired episodes at each tick until ctx is done.
func (svc *Service) RunExpiredEpisodesCleanup(ctx context.Context, ticks <-chan time.Time) {
	svc.logger.InfoContext(ctx, "starting expired episodes cleanup")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			svc.deleteExpiredEpisodes(ctx)
		}
	}
}

func (svc *Service) deleteExpiredEpisodes(ctx context.Context) {
	expired, err := svc.ListExpiredEpisodes(ctx, EpisodeMaxAge)
	if err != nil {
		svc.logger.ErrorContext(ctx, "failed to list expired episodes", slog.Any("error", err))
		return
	}
	for _, ep := range expired {
		if err := svc.DeleteEpisodes(ctx, ep.UserID, []string{ep.ID}); err != nil {
			svc.logger.ErrorContext(ctx, "failed to delete expired episode", slog.String("id", ep.ID), slog.Any("error", err))
			continue
		}
		svc.logger.InfoContext(ctx, "deleted expired episode",
			slog.String("id", ep.ID), slog.String("title", ep.Title), slog.String("url", ep.URL))
	}
}
