package jobsqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	tests "undercast-bot/testutils"
)

var logger, _ = zap.NewDevelopment()

func TestRedisJobsQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	redisURL, teardown, err := tests.GetFakeRedisURL(ctx)
	defer teardown()
	if err != nil {
		t.Fatalf("error getting redis url: %v", err)
	}

	opt, _ := redis.ParseURL(redisURL)
	redisClient := redis.NewClient(opt)
	defer func() { _ = redisClient.Close() }()

	t.Run("job is persisted", func(t *testing.T) {
		// First publish, then subscribe. Job should arrive
		queue, err := NewRedisJobsQueue(redisClient, 10, randomPrefix(), logger)
		if err != nil {
			t.Errorf("error creating redis job queue: %v", err)
		}
		defer queue.Shutdown()

		err = queue.Publish(ctx, "some-job-type", map[string]string{"foo": "bar"})
		if err != nil {
			t.Errorf("error publishing job: %v", err)
		}

		var callCountMutex sync.RWMutex
		callCountMutex.Lock()
		callCount := 0
		callCountMutex.Unlock()
		queue.Subscribe(ctx, "some-job-type", func(payloadBytes []byte) error {
			var result map[string]string
			err := json.Unmarshal(payloadBytes, &result)
			if err != nil {
				return err
			}
			callCountMutex.Lock()
			defer callCountMutex.Unlock()
			callCount++
			return nil
		})
		queue.Run()

		if eventually(20*time.Second, func() bool {
			callCountMutex.RLock()
			defer callCountMutex.RUnlock()
			return callCount == 1
		}) != true {
			t.Errorf("job was never delivered to subscriber")
		}
	})

	t.Run("job is retried", func(t *testing.T) {
		// If job is failed once, it will be retried shortly after
		queue, err := NewRedisJobsQueue(redisClient, 10, randomPrefix(), logger)
		if err != nil {
			t.Errorf("error creating redis job queue: %v", err)
		}
		defer queue.Shutdown()

		err = queue.Publish(ctx, "some-job-type", map[string]string{"foo": "bar"})
		if err != nil {
			t.Errorf("error publishing job: %v", err)
		}

		var callCountMutex sync.RWMutex
		callCountMutex.Lock()
		callCount := 0
		callCountMutex.Unlock()
		queue.Subscribe(ctx, "some-job-type", func(payloadBytes []byte) error {
			callCountMutex.Lock()
			defer callCountMutex.Unlock()
			callCount++
			if callCount < 2 {
				return fmt.Errorf("some error")
			}
			return nil
		})

		queue.Run()

		if eventually(60*time.Second, func() bool {
			callCountMutex.RLock()
			defer callCountMutex.RUnlock()
			return callCount == 2
		}) != true {
			t.Errorf("job was never retried")
		}
	})
}

func eventually(timeout time.Duration, f func() bool) bool {
	timeoutChan := time.After(timeout)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-timeoutChan:
			return false
		case <-tick.C:
			if f() {
				return true
			}
		}
	}
}

func randomPrefix() (str string) {
	b := make([]byte, 24)
	rand.Read(b)
	return fmt.Sprintf("mediary:%x", b)

}
