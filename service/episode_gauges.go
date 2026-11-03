package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/metric"

	"tg-podcastotron/telemetry"
)

// EpisodeStatusStats is the number of episodes in one status and the oldest updated_at of these episodes.
type EpisodeStatusStats struct {
	Status          EpisodeStatus
	Count           int64
	OldestUpdatedAt time.Time
}

// gaugeCountStatuses are the statuses that podcastotron.episodes reports. A status with no episodes reports 0.
var gaugeCountStatuses = []EpisodeStatus{
	EpisodeStatusUndefined,
	EpisodeStatusCreated,
	EpisodeStatusPending,
	EpisodeStatusDownloading,
	EpisodeStatusProcessing,
	EpisodeStatusUploading,
	EpisodeStatusRetrying,
	EpisodeStatusFailed,
}

// RegisterEpisodeGauges registers the gauges of the episodes that are not complete.
// The callback runs one query on each collection. If the query fails, the callback
// logs the error and reports no values.
func RegisterEpisodeGauges(meter metric.Meter, repo Repository, logger *slog.Logger, now func() time.Time) (metric.Registration, error) {
	episodes, err := meter.Int64ObservableGauge(
		"podcastotron.episodes",
		metric.WithDescription("Number of episodes that are not complete, by status"),
		metric.WithUnit("{episode}"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create episodes gauge: %w", err)
	}

	oldestAge, err := meter.Float64ObservableGauge(
		"podcastotron.episodes.oldest_age",
		metric.WithDescription("Time since the last update of the oldest episode, by status that is not complete or failed"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create oldest age gauge: %w", err)
	}

	return meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		stats, err := repo.CountIncompleteEpisodes(ctx)
		if err != nil {
			logger.ErrorContext(ctx, "failed to collect episode gauges", slog.Any("error", err))
			return nil
		}

		counts := make(map[EpisodeStatus]int64, len(gaugeCountStatuses))
		for _, status := range gaugeCountStatuses {
			counts[status] = 0
		}
		t := now()
		for _, s := range stats {
			counts[s.Status] = s.Count
			if s.Status != EpisodeStatusFailed {
				o.ObserveFloat64(oldestAge, t.Sub(s.OldestUpdatedAt).Seconds(),
					metric.WithAttributes(telemetry.AttrEpisodeStatus(string(s.Status))))
			}
		}
		for status, count := range counts {
			o.ObserveInt64(episodes, count, metric.WithAttributes(telemetry.AttrEpisodeStatus(string(status))))
		}
		return nil
	}, episodes, oldestAge)
}
