package jobsqueue

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/dir01/sqlq"
)

// PublishOption is an alias for sqlq.PublishOption.
type PublishOption = sqlq.PublishOption

// WithDelay schedules a job to run after the given duration.
func WithDelay(d time.Duration) PublishOption {
	return sqlq.WithDelay(d)
}

// SQLJobsQueue wraps sqlq with a simpler Subscribe-style API.
type SQLJobsQueue struct {
	q      sqlq.JobsQueue
	logger *slog.Logger
}

func New(db *sql.DB, concurrency int, logger *slog.Logger) (*SQLJobsQueue, error) {
	q, err := sqlq.New(db, sqlq.DBTypeSQLite,
		sqlq.WithDefaultConcurrency(uint16(concurrency)), //nolint:gosec
		sqlq.WithDefaultPollInterval(2*time.Second),
		sqlq.WithDefaultJobTimeout(2*time.Hour),
		sqlq.WithDefaultMaxRetries(3),
	)
	if err != nil {
		return nil, err
	}
	// Run() initializes the schema (CREATE TABLE IF NOT EXISTS). Call it immediately
	// so the tables exist before any Publish or Subscribe calls.
	q.Run()
	return &SQLJobsQueue{q: q, logger: logger}, nil
}

// Run is a no-op: schema is initialized in New(), and worker goroutines start in Subscribe().
// It exists for API compatibility with callers that expect to call Run() after Subscribe().
func (j *SQLJobsQueue) Run() {}

func (j *SQLJobsQueue) Shutdown() { j.q.Shutdown() }

func (j *SQLJobsQueue) Publish(ctx context.Context, jobType string, payload any, opts ...PublishOption) error {
	return j.q.Publish(ctx, jobType, payload, opts...)
}

// Subscribe registers a handler for the given job type.
// The handler receives the per-job context (with OTel trace propagated from publisher)
// and the raw JSON payload bytes.
func (j *SQLJobsQueue) Subscribe(ctx context.Context, jobType string, f func(ctx context.Context, payload []byte) error) {
	err := j.q.Consume(ctx, jobType, func(ctx context.Context, _ *sql.Tx, payloadBytes []byte) error {
		return f(ctx, payloadBytes)
	})
	if err != nil {
		j.logger.ErrorContext(ctx, "failed to register consumer", slog.String("job_type", jobType), slog.Any("error", err))
	}
}
