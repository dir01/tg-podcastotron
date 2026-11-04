package tgminiapp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"tg-podcastotron/service"
)

func TestMetadataCache(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c := newMetadataCache(func(ctx context.Context, url string) (*service.Metadata, error) {
		calls.Add(1)
		<-release
		if url == "bad" {
			return nil, errors.New("boom")
		}
		return &service.Metadata{Name: url}, nil
	})
	ctx := context.Background()

	c.start(ctx, "a")
	if _, done, _ := c.get(ctx, "a"); done {
		t.Fatal("get is done before the fetch")
	}
	close(release)

	waitFor(t, func() bool { _, done, _ := c.get(ctx, "a"); return done })
	md, _, err := c.get(ctx, "a")
	if err != nil || md.Name != "a" {
		t.Fatalf("get = %v, %v", md, err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("fetch calls = %d, want 1", n)
	}

	c.start(ctx, "bad")
	waitFor(t, func() bool { _, done, _ := c.get(ctx, "bad"); return done })
	if _, _, err := c.get(ctx, "bad"); err == nil {
		t.Fatal("no error for bad url")
	}

	// After the error TTL, the next get fetches again.
	c.now = func() time.Time { return time.Now().Add(metadataErrorTTL + time.Second) }
	if _, done, _ := c.get(ctx, "bad"); done {
		t.Fatal("expired error is still in the cache")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
