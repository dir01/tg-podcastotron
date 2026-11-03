package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRegisterEpisodeGauges(t *testing.T) {
	repo := getRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	for i, ep := range []struct {
		status EpisodeStatus
		age    time.Duration
	}{
		{EpisodeStatusDownloading, 10 * time.Minute},
		{EpisodeStatusDownloading, 3 * time.Minute},
		{EpisodeStatusFailed, time.Hour},
		{EpisodeStatusComplete, 2 * time.Hour},
	} {
		if _, err := repo.SaveEpisode(ctx, &Episode{
			ID:        string(rune('a' + i)),
			UserID:    "some-user-id",
			CreatedAt: now.Add(-ep.age),
			UpdatedAt: now.Add(-ep.age),
			Status:    ep.status,
		}); err != nil {
			t.Fatal(err)
		}
	}

	counts, ages := collectEpisodeGauges(t, repo, now)

	wantCounts := map[string]int64{
		"undefined": 0, "created": 0, "pending": 0, "downloading": 2,
		"processing": 0, "uploading": 0, "retrying": 0, "failed": 1,
	}
	if len(counts) != len(wantCounts) {
		t.Fatalf("expected counts %v, got %v", wantCounts, counts)
	}
	for status, want := range wantCounts {
		if counts[status] != want {
			t.Errorf("expected count %d for %s, got %d", want, status, counts[status])
		}
	}

	wantAges := map[string]float64{"downloading": 600}
	if len(ages) != len(wantAges) || ages["downloading"] != 600 {
		t.Fatalf("expected ages %v, got %v", wantAges, ages)
	}
}

func TestRegisterEpisodeGauges__QueryFails(t *testing.T) {
	counts, ages := collectEpisodeGauges(t, failingStatsRepo{}, time.Now())
	if len(counts) != 0 || len(ages) != 0 {
		t.Fatalf("expected no values, got counts %v and ages %v", counts, ages)
	}
}

type failingStatsRepo struct{ Repository }

func (failingStatsRepo) CountIncompleteEpisodes(context.Context) ([]EpisodeStatusStats, error) {
	return nil, errors.New("some error")
}

func collectEpisodeGauges(t *testing.T, repo Repository, now time.Time) (map[string]int64, map[string]float64) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := RegisterEpisodeGauges(provider.Meter("test"), repo, logger, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	counts := map[string]int64{}
	ages := map[string]float64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Gauge[int64]:
				if m.Name != "podcastotron.episodes" || m.Unit != "{episode}" {
					t.Fatalf("unexpected gauge %s (%s)", m.Name, m.Unit)
				}
				for _, dp := range data.DataPoints {
					status, _ := dp.Attributes.Value("episode.status")
					counts[status.AsString()] = dp.Value
				}
			case metricdata.Gauge[float64]:
				if m.Name != "podcastotron.episodes.oldest_age" || m.Unit != "s" {
					t.Fatalf("unexpected gauge %s (%s)", m.Name, m.Unit)
				}
				for _, dp := range data.DataPoints {
					status, _ := dp.Attributes.Value("episode.status")
					ages[status.AsString()] = dp.Value
				}
			}
		}
	}
	return counts, ages
}
