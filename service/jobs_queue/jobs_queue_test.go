package jobsqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

func TestCappedExponentialBackoff(t *testing.T) {
	cases := map[uint16]time.Duration{
		0:  1 * time.Second,
		1:  2 * time.Second,
		3:  8 * time.Second,
		9:  5 * time.Minute, // capped
		50: 5 * time.Minute, // capped, no overflow
	}
	for retry, want := range cases {
		if got := cappedExponentialBackoff(retry); got != want {
			t.Errorf("cappedExponentialBackoff(%d) = %v, want %v", retry, got, want)
		}
	}
}

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

func TestPollDelay(t *testing.T) {
	cases := map[uint16]struct{ low, high time.Duration }{
		0:              {2 * time.Second, 2400 * time.Millisecond},
		1:              {2200 * time.Millisecond, 2640 * time.Millisecond},
		10:             {5187 * time.Millisecond, 6225 * time.Millisecond}, // 2s × 1.1^10 ≈ 5.19s
		29:             {30 * time.Second, 36 * time.Second},               // 2s × 1.1^29 > 30s
		math.MaxUint16: {30 * time.Second, 36 * time.Second},
	}
	for emptyPolls, want := range cases {
		for range 100 {
			got := pollDelay(emptyPolls)
			if got < want.low || got >= want.high {
				t.Fatalf("pollDelay(%d) = %v, want in [%v, %v)", emptyPolls, got, want.low, want.high)
			}
		}
	}
}
