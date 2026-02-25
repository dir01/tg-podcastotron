package jobsqueue

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	work2 "github.com/taylorchu/work"
	"tg-podcastotron/telemetry"
)

type RJQ struct {
	work2Queue  work2.RedisQueue
	work2Worker *work2.Worker
	namespace   string
	concurrency int
	logger      *slog.Logger
}

func NewRedisJobsQueue(redisClient *redis.Client, concurrency int, namespace string, logger *slog.Logger) (*RJQ, error) {
	jobsQueue := &RJQ{
		work2Queue: work2.NewRedisQueue(redisClient),
		work2Worker: work2.NewWorker(&work2.WorkerOptions{
			Namespace: namespace,
			Queue:     work2.NewRedisQueue(redisClient),
			ErrorFunc: func(err error) {
				logger.ErrorContext(context.Background(), "failed to handle job", slog.Any("error", err))
			},
		}),
		namespace:   namespace,
		concurrency: concurrency,
		logger:      logger,
	}
	return jobsQueue, nil
}

func (r *RJQ) Run() {
	r.work2Worker.Start()
}

func (r *RJQ) Shutdown() {
	r.work2Worker.Stop()
}

func (r *RJQ) Publish(ctx context.Context, jobType string, payload any) error {
	job := work2.NewJob()
	if err := job.MarshalJSONPayload(payload); err != nil {
		return telemetry.LogError(r.logger, ctx, err, "failed to marshal payload")
	}

	if err := r.work2Queue.Enqueue(job, &work2.EnqueueOptions{Namespace: r.namespace, QueueID: jobType}); err != nil {
		return telemetry.LogError(r.logger, ctx, err, "failed to enqueue job")
	}

	return nil
}

func (r *RJQ) Subscribe(ctx context.Context, jobType string, f func(payloadBytes []byte) error) {
	err := r.work2Worker.Register(jobType, func(job *work2.Job, opt *work2.DequeueOptions) error {
		if err := f(job.Payload); err != nil {
			r.logger.ErrorContext(ctx, "failed to handle job", slog.Any("error", err))
			return err
		}
		return nil
	}, &work2.JobOptions{
		MaxExecutionTime: 2 * time.Hour,
		IdleWait:         2 * time.Second,
		NumGoroutines:    int64(r.concurrency),
	})
	if err != nil {
		r.logger.ErrorContext(ctx, "failed to register job", slog.Any("error", err))
	}
}
