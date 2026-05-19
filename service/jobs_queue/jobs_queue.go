package jobsqueue

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	work2 "github.com/taylorchu/work"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"tg-podcastotron/telemetry"
)

type jobEnvelope struct {
	TraceContext map[string]string `json:"tc,omitempty"`
	Payload      json.RawMessage   `json:"p"`
}

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
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return telemetry.LogError(r.logger, ctx, err, "failed to marshal payload")
	}

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	envelope := jobEnvelope{
		TraceContext: carrier,
		Payload:      payloadBytes,
	}

	job := work2.NewJob()
	if err := job.MarshalJSONPayload(envelope); err != nil {
		return telemetry.LogError(r.logger, ctx, err, "failed to marshal envelope")
	}

	if err := r.work2Queue.Enqueue(job, &work2.EnqueueOptions{Namespace: r.namespace, QueueID: jobType}); err != nil {
		return telemetry.LogError(r.logger, ctx, err, "failed to enqueue job")
	}

	return nil
}

func (r *RJQ) Subscribe(ctx context.Context, jobType string, f func(ctx context.Context, payloadBytes []byte) error) {
	err := r.work2Worker.Register(jobType, func(job *work2.Job, opt *work2.DequeueOptions) error {
		var envelope jobEnvelope
		if err := json.Unmarshal(job.Payload, &envelope); err != nil || len(envelope.Payload) == 0 {
			// fall back to raw payload for jobs enqueued before this change
			envelope = jobEnvelope{Payload: job.Payload}
		}

		jobCtx := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(envelope.TraceContext))
		jobCtx, span := telemetry.StartSpan(jobCtx, "job:"+jobType)
		defer span.End()

		if err := f(jobCtx, envelope.Payload); err != nil {
			r.logger.ErrorContext(jobCtx, "failed to handle job", slog.Any("error", err))
			telemetry.RecordError(span, err)
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
