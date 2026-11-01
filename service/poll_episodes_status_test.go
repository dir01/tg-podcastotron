package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	migrate "github.com/rubenv/sql-migrate"

	"tg-podcastotron/mediary"
	"tg-podcastotron/mediary/mediarymocks"
	jobsqueue "tg-podcastotron/service/jobs_queue"
)

// The poll must tell the user about each failed mediary attempt and its reason one time,
// also when the retry delay of mediary is shorter than the poll delay, and the poll
// does not see the "retrying" status.
func TestPollEpisodesStatus_ReportsMediaryRetries(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	dsn := "file:" + filepath.Join(t.TempDir(), "test.db") + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../db/migrations"}, migrate.Up); err != nil {
		t.Fatalf("failed to apply migrations: %v", err)
	}
	queue, err := jobsqueue.New(db, 1, logger) // no consumers: the test runs each poll itself
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(queue.Shutdown)

	var jobStatus mediary.JobStatus
	mediaryMock := &mediarymocks.ServiceMock{
		FetchJobStatusMapFunc: func(ctx context.Context, jobIDs []string) (map[string]*mediary.JobStatus, error) {
			st := jobStatus
			return map[string]*mediary.JobStatus{"job-1": &st}, nil
		},
	}
	repo := NewSqliteRepository(db, db)
	svc := New(mediaryMock, repo, nil, queue, "", func(s string) string { return s }, logger, nil)

	now := time.Now()
	if _, err := repo.SaveEpisode(ctx, &Episode{
		ID: "1", UserID: "u1", Title: "Book", MediaryID: "job-1", Status: EpisodeStatusPending,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	payload := &PollEpisodesStatusQueuePayload{EpisodeIDs: []string{"1"}, UserID: "u1"}

	// poll sets the mediary status, runs one poll, and returns the status change (nil if none).
	// The next poll uses the payload that this poll put in the queue.
	poll := func(st mediary.JobStatus) *EpisodeStatusChange {
		t.Helper()
		st.Id = "job-1"
		jobStatus = st
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.onPollEpisodesQueueEvent(ctx, payloadBytes); err != nil {
			t.Fatal(err)
		}

		var change *EpisodeStatusChange
		select {
		case batch := <-svc.episodeStatusChangesChan:
			if len(batch.Changes) != 1 {
				t.Fatalf("expected 1 change, got %+v", batch.Changes)
			}
			change = &batch.Changes[0]
		default:
		}

		if st.Status == mediary.JobStatusFailed {
			return change
		}
		var next []byte
		if err := db.QueryRowContext(ctx,
			`SELECT payload FROM sqlq_jobs WHERE job_type = ? ORDER BY id DESC LIMIT 1`,
			queueEventPollEpisodesStatus,
		).Scan(&next); err != nil {
			t.Fatalf("failed to get the requeued poll: %v", err)
		}
		payload = &PollEpisodesStatusQueuePayload{}
		if err := json.Unmarshal(next, payload); err != nil {
			t.Fatal(err)
		}
		return change
	}

	expect := func(got *EpisodeStatusChange, status EpisodeStatus, attempt int, errText string) {
		t.Helper()
		if got == nil {
			t.Fatalf("expected a change to %s, got no change", status)
		}
		if got.NewStatus != status || got.Attempt != attempt || got.MaxAttempts != 6 || got.Error != errText {
			t.Fatalf("expected status=%s attempt=%d/6 error=%q, got status=%s attempt=%d/%d error=%q",
				status, attempt, errText, got.NewStatus, got.Attempt, got.MaxAttempts, got.Error)
		}
	}

	expect(poll(mediary.JobStatus{Status: mediary.JobStatusDownloading, Attempt: 1, MaxAttempts: 6}),
		EpisodeStatusDownloading, 1, "")

	if got := poll(mediary.JobStatus{Status: mediary.JobStatusDownloading, Attempt: 1, MaxAttempts: 6}); got != nil {
		t.Fatalf("expected no change for the same status and attempt, got %+v", got)
	}

	// The poll did not see "retrying". The new attempt shows the error of the failed attempt.
	expect(poll(mediary.JobStatus{Status: mediary.JobStatusDownloading, Attempt: 2, MaxAttempts: 6, Error: "stalled"}),
		EpisodeStatusDownloading, 2, "stalled")

	expect(poll(mediary.JobStatus{Status: mediary.JobStatusRetrying, Attempt: 2, MaxAttempts: 6, Error: "stalled again"}),
		EpisodeStatusRetrying, 2, "stalled again")

	// The poll saw "retrying" with this error. Do not show it again.
	expect(poll(mediary.JobStatus{Status: mediary.JobStatusDownloading, Attempt: 3, MaxAttempts: 6, Error: "stalled again"}),
		EpisodeStatusDownloading, 3, "")

	expect(poll(mediary.JobStatus{Status: mediary.JobStatusRetrying, Attempt: 3, MaxAttempts: 6, Error: "no peers"}),
		EpisodeStatusRetrying, 3, "no peers")

	// The poll saw "retrying" for attempt 3, but it missed attempt 4. The error is from
	// attempt 4, and the user did not see it.
	expect(poll(mediary.JobStatus{Status: mediary.JobStatusDownloading, Attempt: 5, MaxAttempts: 6, Error: "disk full"}),
		EpisodeStatusDownloading, 5, "disk full")

	expect(poll(mediary.JobStatus{Status: mediary.JobStatusFailed, Attempt: 6, MaxAttempts: 6, Error: "timed out"}),
		EpisodeStatusFailed, 6, "timed out")

	eps, err := repo.GetEpisodesMap(ctx, "u1", []string{"1"})
	if err != nil {
		t.Fatal(err)
	}
	if eps["1"].Status != EpisodeStatusFailed {
		t.Fatalf("expected the episode to be failed, got %s", eps["1"].Status)
	}
}
