package jobsqueue

import (
	"context"
	"database/sql"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"github.com/dir01/sqlq"
)

// PublishOption is an alias for sqlq.PublishOption.
type PublishOption = sqlq.PublishOption

// ConsumerOption is an alias for sqlq.ConsumerOption.
type ConsumerOption = sqlq.ConsumerOption

// JobInfo is an alias for sqlq.JobInfo.
type JobInfo = sqlq.JobInfo

// JobInfoFromContext returns information about the job being handled.
func JobInfoFromContext(ctx context.Context) (JobInfo, bool) {
	return sqlq.JobInfoFromContext(ctx)
}

// WithMaxRetries overrides the queue-wide unlimited retries for one job type.
// Once exhausted, the job is moved to the dead letter queue.
func WithMaxRetries(maxRetries int32) ConsumerOption {
	return sqlq.WithConsumerMaxRetries(maxRetries)
}

// WithOnDeadLetter registers a hook that runs in the transaction that dead-letters a job of this type.
// The hook must write only through tx (SQLite's write lock is held) and must not commit or roll it back.
// Returning an error rolls the move back and reschedules the job like a failed attempt.
func WithOnDeadLetter(hook func(ctx context.Context, tx *sql.Tx, info JobInfo, payload []byte, handlerErr error) error) ConsumerOption {
	return sqlq.WithConsumerOnDeadLetter(hook)
}

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
		sqlq.WithLogger(logger),
		sqlq.WithDefaultConcurrency(uint16(concurrency)), //nolint:gosec
		sqlq.WithDefaultPollDelayFunc(pollDelay),
		sqlq.WithDefaultJobTimeout(2*time.Hour),
		// Retry indefinitely: jobs that depend on mediary (create episodes,
		// regenerate feed) must survive a multi-day mediary outage and resume
		// once it recovers, rather than being dead-lettered and silently lost.
		// This matches the behavior of the previous Redis-backed queue.
		sqlq.WithDefaultMaxRetries(-1),
		sqlq.WithDefaultBackoffFunc(cappedExponentialBackoff),
	)
	if err != nil {
		return nil, err
	}
	// Run() creates or migrates the schema. Call it immediately so the tables
	// exist before any Publish or Subscribe calls.
	ctx := context.Background()
	if err := q.Run(ctx); err != nil {
		return nil, err
	}
	return &SQLJobsQueue{q: q, logger: logger}, nil
}

// pollDelay is 2s after a poll that found jobs. Each poll that finds no jobs makes
// the delay 10% longer, to a maximum of 30s. Thus an idle consumer sends few queries.
// The minimum is larger than the sqlq default (100ms), so that a busy queue does not
// add many write transactions to SQLite. A random jitter of up to 20% is added,
// so that idle consumers do not poll at the same time.
func pollDelay(emptyPolls uint16) time.Duration {
	const (
		minDelay = 2 * time.Second
		maxDelay = 30 * time.Second
	)
	delay := time.Duration(min(float64(minDelay)*math.Pow(1.1, float64(emptyPolls)), float64(maxDelay)))
	return delay + rand.N(delay/5)
}

// cappedExponentialBackoff grows exponentially (1s, 2s, 4s, ...) but is capped so
// that with infinite retries a long dependency outage keeps being retried on a
// sane cadence instead of backing off to hours/days.
func cappedExponentialBackoff(retryNum uint16) time.Duration {
	const maxDelay = 5 * time.Minute
	if retryNum >= 9 { // 2^9s = ~8.5m already exceeds the cap
		return maxDelay
	}
	d := time.Duration(1<<retryNum) * time.Second
	if d > maxDelay {
		return maxDelay
	}
	return d
}

// Run is a no-op: schema is initialized in New(), and worker goroutines start in Subscribe().
// It exists for API compatibility with callers that expect to call Run() after Subscribe().
func (j *SQLJobsQueue) Run() {}

// Shutdown stops the consumers and waits up to 30 seconds for running handlers.
func (j *SQLJobsQueue) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := j.q.Shutdown(ctx); err != nil {
		j.logger.ErrorContext(ctx, "failed to shut down jobs queue", slog.Any("error", err))
	}
}

func (j *SQLJobsQueue) Publish(ctx context.Context, jobType string, payload any, opts ...PublishOption) error {
	return j.q.Publish(ctx, jobType, payload, opts...)
}

// Subscribe registers a handler for the given job type.
// The handler receives the per-job context (with OTel trace propagated from publisher)
// and the raw JSON payload bytes.
func (j *SQLJobsQueue) Subscribe(ctx context.Context, jobType string, f func(ctx context.Context, payload []byte) error, opts ...ConsumerOption) {
	err := j.q.Consume(ctx, jobType, func(ctx context.Context, _ *sql.Tx, payloadBytes []byte) error {
		return f(ctx, payloadBytes)
	}, opts...)
	if err != nil {
		j.logger.ErrorContext(ctx, "failed to register consumer", slog.String("job_type", jobType), slog.Any("error", err))
	}
}
