package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"golang.org/x/exp/maps"
	"tg-podcastotron/telemetry"
)

// creationFeedIDs returns the feeds that get new episodes: the requested feeds of
// the user that still exist, or the default feed if there are none.
func (svc *Service) creationFeedIDs(ctx context.Context, userID string, requested []string) ([]string, error) {
	if len(requested) > 0 {
		feeds, err := svc.repository.GetFeedsMap(ctx, userID, requested)
		if err != nil {
			return nil, fmt.Errorf("failed to get requested feeds: %w", err)
		}
		var ids []string
		for _, id := range requested {
			if _, ok := feeds[id]; ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return ids, nil
		}
	}
	feed, err := svc.DefaultFeed(ctx, userID)
	if err != nil {
		return nil, err
	}
	return []string{feed.ID}, nil
}

// addPublications adds each episode to each feed, if it is not there yet.
// It returns the IDs of the feeds that changed.
func (svc *Service) addPublications(ctx context.Context, userID string, epIDs, feedIDs []string) ([]string, error) {
	existing, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list publications: %w", err)
	}
	type key struct{ episodeID, feedID string }
	exists := make(map[key]bool, len(existing))
	for _, p := range existing {
		exists[key{p.EpisodeID, p.FeedID}] = true
	}

	changed := make(map[string]bool)
	var toCreate []*Publication
	now := time.Now()
	for _, epID := range epIDs {
		for _, feedID := range feedIDs {
			if exists[key{epID, feedID}] {
				continue
			}
			toCreate = append(toCreate, &Publication{UserID: userID, FeedID: feedID, EpisodeID: epID, CreatedAt: now})
			changed[feedID] = true
		}
	}
	if err := svc.repository.BulkInsertPublications(ctx, toCreate); err != nil {
		return nil, fmt.Errorf("failed to insert publications: %w", err)
	}
	return maps.Keys(changed), nil
}

// AddEpisodesToFeeds adds the episodes to the feeds. The episodes stay in their other feeds.
func (svc *Service) AddEpisodesToFeeds(ctx context.Context, userID string, epIDs, feedIDs []string) error {
	fields := []any{slog.String("user_id", userID), slog.Any("episode_ids", epIDs), slog.Any("feed_ids", feedIDs)}

	var changed []string
	if err := svc.repository.Transaction(ctx, func(ctx context.Context) error {
		if err := svc.checkOwnership(ctx, userID, epIDs, feedIDs); err != nil {
			return err
		}
		var err error
		changed, err = svc.addPublications(ctx, userID, epIDs, feedIDs)
		return err
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to add episodes to feeds", fields...)
	}
	return svc.enqueueFeedsRegeneration(ctx, userID, changed)
}

// RemoveEpisodesFromFeeds removes the episodes from the feeds. The episodes stay in their other feeds.
func (svc *Service) RemoveEpisodesFromFeeds(ctx context.Context, userID string, epIDs, feedIDs []string) error {
	fields := []any{slog.String("user_id", userID), slog.Any("episode_ids", epIDs), slog.Any("feed_ids", feedIDs)}

	changed := make(map[string]bool)
	if err := svc.repository.Transaction(ctx, func(ctx context.Context) error {
		existing, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
		if err != nil {
			return fmt.Errorf("failed to list publications: %w", err)
		}
		var toDelete []string
		for _, p := range existing {
			if slices.Contains(feedIDs, p.FeedID) {
				toDelete = append(toDelete, p.ID)
				changed[p.FeedID] = true
			}
		}
		return svc.repository.DeletePublications(ctx, userID, toDelete)
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to remove episodes from feeds", fields...)
	}
	return svc.enqueueFeedsRegeneration(ctx, userID, maps.Keys(changed))
}

// SetFeedPermanent sets if the feed keeps its episodes after EpisodeMaxAge.
func (svc *Service) SetFeedPermanent(ctx context.Context, userID, feedID string, permanent bool) error {
	if permanent {
		return svc.MarkFeedAsPermanent(ctx, userID, feedID)
	}
	return svc.MarkFeedAsEphemeral(ctx, userID, feedID)
}

// checkOwnership returns an error if the user does not have all of the episodes and feeds.
func (svc *Service) checkOwnership(ctx context.Context, userID string, epIDs, feedIDs []string) error {
	episodes, err := svc.repository.GetEpisodesMap(ctx, userID, epIDs)
	if err != nil {
		return fmt.Errorf("failed to get episodes: %w", err)
	}
	for _, id := range epIDs {
		if _, ok := episodes[id]; !ok {
			return fmt.Errorf("%w: %s", ErrEpisodeNotFound, id)
		}
	}
	feeds, err := svc.repository.GetFeedsMap(ctx, userID, feedIDs)
	if err != nil {
		return fmt.Errorf("failed to get feeds: %w", err)
	}
	for _, id := range feedIDs {
		if _, ok := feeds[id]; !ok {
			return fmt.Errorf("%w: %s", ErrFeedNotFound, id)
		}
	}
	return nil
}

func (svc *Service) enqueueFeedsRegeneration(ctx context.Context, userID string, feedIDs []string) error {
	if len(feedIDs) == 0 {
		return nil
	}
	if err := svc.jobsQueue.Publish(ctx, queueEventRegenerateFeed, RegenerateFeedQueuePayload{
		UserID:  userID,
		FeedIDs: feedIDs,
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to publish regenerate feed job",
			slog.String("user_id", userID), slog.Any("feed_ids", feedIDs))
	}
	return nil
}
