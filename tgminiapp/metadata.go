package tgminiapp

import (
	"context"
	"sync"
	"time"

	"tg-podcastotron/service"
)

const (
	// metadataFetchTimeout is longer than all retries of service.FetchMetadata.
	metadataFetchTimeout = 15 * time.Minute
	metadataTTL          = time.Hour
	// metadataErrorTTL is short, so that a reload of the page tries again.
	metadataErrorTTL = 30 * time.Second
)

// metadataCache fetches the metadata of links in the background. Mediary can need
// minutes to get the file list of a torrent, so the page polls the cache.
type metadataCache struct {
	fetch func(ctx context.Context, url string) (*service.Metadata, error)
	now   func() time.Time

	mu      sync.Mutex
	entries map[string]*metadataEntry
}

type metadataEntry struct {
	done      bool
	metadata  *service.Metadata
	err       error
	expiresAt time.Time // zero while the fetch runs
}

func newMetadataCache(fetch func(ctx context.Context, url string) (*service.Metadata, error)) *metadataCache {
	return &metadataCache{fetch: fetch, now: time.Now, entries: map[string]*metadataEntry{}}
}

// start starts to get the metadata of url, if it is not in the cache and its fetch
// does not run. ctx gives the trace of the fetch.
func (c *metadataCache) start(ctx context.Context, url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.startLocked(ctx, url)
}

// get returns the metadata of url if the fetch is done. Otherwise it starts the
// fetch if it does not run, and returns done=false.
func (c *metadataCache) get(ctx context.Context, url string) (md *service.Metadata, done bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.startLocked(ctx, url)
	return e.metadata, e.done, e.err
}

func (c *metadataCache) startLocked(ctx context.Context, url string) *metadataEntry {
	now := c.now()
	for k, e := range c.entries {
		if e.done && now.After(e.expiresAt) {
			delete(c.entries, k)
		}
	}

	if e, ok := c.entries[url]; ok {
		return e
	}

	e := &metadataEntry{}
	c.entries[url] = e
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metadataFetchTimeout)
	go func() {
		defer cancel()
		md, err := c.fetch(ctx, url)

		c.mu.Lock()
		defer c.mu.Unlock()
		e.done, e.metadata, e.err = true, md, err
		if err != nil {
			e.expiresAt = c.now().Add(metadataErrorTTL)
		} else {
			e.expiresAt = c.now().Add(metadataTTL)
		}
	}()
	return e
}
