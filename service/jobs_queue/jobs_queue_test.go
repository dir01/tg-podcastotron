package jobsqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

func TestSQLJobsQueue(t *testing.T) {
	t.Run("job is persisted", func(t *testing.T) {
		// Publish before subscribe — job should still be delivered.
		queue := newTestQueue(t, 10)
		defer queue.Shutdown()

		ctx := context.Background()
		if err := queue.Publish(ctx, "some-job-type", map[string]string{"foo": "bar"}); err != nil {
			t.Fatalf("error publishing job: %v", err)
		}

		var mu sync.Mutex
		callCount := 0
		queue.Subscribe(ctx, "some-job-type", func(ctx context.Context, payloadBytes []byte) error {
			var result map[string]string
			if err := json.Unmarshal(payloadBytes, &result); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			callCount++
			return nil
		})
		queue.Run()

		if !eventually(20*time.Second, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return callCount == 1
		}) {
			t.Error("job was never delivered to subscriber")
		}
	})

	t.Run("job is retried on failure", func(t *testing.T) {
		queue := newTestQueue(t, 10)
		defer queue.Shutdown()

		ctx := context.Background()
		if err := queue.Publish(ctx, "some-job-type", map[string]string{"foo": "bar"}); err != nil {
			t.Fatalf("error publishing job: %v", err)
		}

		var mu sync.Mutex
		callCount := 0
		queue.Subscribe(ctx, "some-job-type", func(ctx context.Context, payloadBytes []byte) error {
			mu.Lock()
			defer mu.Unlock()
			callCount++
			if callCount < 2 {
				return fmt.Errorf("transient error")
			}
			return nil
		})
		queue.Run()

		if !eventually(60*time.Second, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return callCount >= 2
		}) {
			t.Error("job was never retried")
		}
	})

	t.Run("delayed job", func(t *testing.T) {
		queue := newTestQueue(t, 10)
		defer queue.Shutdown()

		ctx := context.Background()
		delay := 300 * time.Millisecond
		publishedAt := time.Now()
		if err := queue.Publish(ctx, "delayed-job", "payload", WithDelay(delay)); err != nil {
			t.Fatalf("error publishing delayed job: %v", err)
		}

		var mu sync.Mutex
		var deliveredAt time.Time
		queue.Subscribe(ctx, "delayed-job", func(ctx context.Context, _ []byte) error {
			mu.Lock()
			defer mu.Unlock()
			deliveredAt = time.Now()
			return nil
		})
		queue.Run()

		if !eventually(10*time.Second, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return !deliveredAt.IsZero()
		}) {
			t.Fatal("delayed job was never delivered")
		}

		elapsed := deliveredAt.Sub(publishedAt)
		if elapsed < delay {
			t.Errorf("job was delivered too early: elapsed=%v, expected>=%v", elapsed, delay)
		}
	})
}

func newTestQueue(t *testing.T, concurrency int) *SQLJobsQueue {
	t.Helper()
	// Use a file-based SQLite with WAL mode so multiple goroutines can access it concurrently.
	dbPath := "file:" + t.TempDir() + "/queue.db?_journal_mode=WAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("error opening sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	q, err := New(db, concurrency, logger)
	if err != nil {
		t.Fatalf("error creating jobs queue: %v", err)
	}
	return q
}

func eventually(timeout time.Duration, f func() bool) bool {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for time.Now().Before(deadline) {
		<-tick.C
		if f() {
			return true
		}
	}
	return false
}
