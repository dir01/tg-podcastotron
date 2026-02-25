package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "tg-podcastotron"

// Metrics holds all metric instruments for the application.
type Metrics struct {
	// Episode metrics
	EpisodeCreated         metric.Int64Counter
	EpisodeProcessingTime  metric.Float64Histogram
	EpisodeDeleted         metric.Int64Counter
	EpisodePublished       metric.Int64Counter

	// Job metrics
	JobEnqueued        metric.Int64Counter
	JobProcessed       metric.Int64Counter
	JobErrors          metric.Int64Counter
	JobProcessingTime  metric.Float64Histogram

	// Feed metrics
	FeedRegenerated    metric.Int64Counter
}

// NewMetrics creates and registers all metric instruments.
func NewMetrics() (*Metrics, error) {
	meter := otel.Meter(meterName)

	episodeCreated, err := meter.Int64Counter(
		"episode.created",
		metric.WithDescription("Number of episodes created"),
		metric.WithUnit("{episode}"),
	)
	if err != nil {
		return nil, err
	}

	episodeProcessingTime, err := meter.Float64Histogram(
		"episode.processing_time",
		metric.WithDescription("Time taken to process an episode"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	episodeDeleted, err := meter.Int64Counter(
		"episode.deleted",
		metric.WithDescription("Number of episodes deleted"),
		metric.WithUnit("{episode}"),
	)
	if err != nil {
		return nil, err
	}

	episodePublished, err := meter.Int64Counter(
		"episode.published",
		metric.WithDescription("Number of episodes published"),
		metric.WithUnit("{episode}"),
	)
	if err != nil {
		return nil, err
	}

	jobEnqueued, err := meter.Int64Counter(
		"job.enqueued",
		metric.WithDescription("Number of jobs enqueued"),
		metric.WithUnit("{job}"),
	)
	if err != nil {
		return nil, err
	}

	jobProcessed, err := meter.Int64Counter(
		"job.processed",
		metric.WithDescription("Number of jobs successfully processed"),
		metric.WithUnit("{job}"),
	)
	if err != nil {
		return nil, err
	}

	jobErrors, err := meter.Int64Counter(
		"job.errors",
		metric.WithDescription("Number of job processing errors"),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, err
	}

	jobProcessingTime, err := meter.Float64Histogram(
		"job.processing_time",
		metric.WithDescription("Time taken to process a job"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	feedRegenerated, err := meter.Int64Counter(
		"feed.regenerated",
		metric.WithDescription("Number of feeds regenerated"),
		metric.WithUnit("{feed}"),
	)
	if err != nil {
		return nil, err
	}

	return &Metrics{
		EpisodeCreated:        episodeCreated,
		EpisodeProcessingTime: episodeProcessingTime,
		EpisodeDeleted:        episodeDeleted,
		EpisodePublished:      episodePublished,
		JobEnqueued:           jobEnqueued,
		JobProcessed:          jobProcessed,
		JobErrors:             jobErrors,
		JobProcessingTime:     jobProcessingTime,
		FeedRegenerated:       feedRegenerated,
	}, nil
}

// Helper methods for recording metrics

// RecordEpisodeCreated records an episode creation event.
func (m *Metrics) RecordEpisodeCreated(ctx context.Context, userID string) {
	m.EpisodeCreated.Add(ctx, 1, metric.WithAttributes(AttrUserID(userID)))
}

// RecordEpisodeProcessingTime records the time taken to process an episode.
func (m *Metrics) RecordEpisodeProcessingTime(ctx context.Context, duration time.Duration, userID string) {
	m.EpisodeProcessingTime.Record(ctx, duration.Seconds(),
		metric.WithAttributes(AttrUserID(userID)))
}

// RecordEpisodeDeleted records an episode deletion event.
func (m *Metrics) RecordEpisodeDeleted(ctx context.Context, userID string, count int) {
	m.EpisodeDeleted.Add(ctx, int64(count), metric.WithAttributes(AttrUserID(userID)))
}

// RecordEpisodePublished records an episode publish event.
func (m *Metrics) RecordEpisodePublished(ctx context.Context, userID string, count int) {
	m.EpisodePublished.Add(ctx, int64(count), metric.WithAttributes(AttrUserID(userID)))
}

// RecordJobEnqueued records a job enqueue event.
func (m *Metrics) RecordJobEnqueued(ctx context.Context, jobType string) {
	m.JobEnqueued.Add(ctx, 1, metric.WithAttributes(AttrJobType(jobType)))
}

// RecordJobProcessed records a successful job processing event.
func (m *Metrics) RecordJobProcessed(ctx context.Context, jobType string) {
	m.JobProcessed.Add(ctx, 1, metric.WithAttributes(AttrJobType(jobType)))
}

// RecordJobError records a job processing error.
func (m *Metrics) RecordJobError(ctx context.Context, jobType string, err error) {
	attrs := []attribute.KeyValue{AttrJobType(jobType)}
	if err != nil {
		attrs = append(attrs, attribute.String("error.type", err.Error()))
	}
	m.JobErrors.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// RecordJobProcessingTime records the time taken to process a job.
func (m *Metrics) RecordJobProcessingTime(ctx context.Context, jobType string, duration time.Duration) {
	m.JobProcessingTime.Record(ctx, duration.Seconds(),
		metric.WithAttributes(AttrJobType(jobType)))
}

// RecordFeedRegenerated records a feed regeneration event.
func (m *Metrics) RecordFeedRegenerated(ctx context.Context, feedID string) {
	m.FeedRegenerated.Add(ctx, 1, metric.WithAttributes(AttrFeedID(feedID)))
}
