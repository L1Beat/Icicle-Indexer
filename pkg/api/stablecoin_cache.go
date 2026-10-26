package api

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// stablecoinCache holds the stablecoin list per chain and serves it
// stale-while-revalidate: a fresh copy is returned as is, a stale copy is
// returned immediately while one background load replaces it, and concurrent
// cold requests share a single load. Errors and empty results are not cached,
// so unknown chain IDs never grow the map.
type stablecoinCache struct {
	ttl         time.Duration
	loadTimeout time.Duration

	mu      sync.Mutex
	entries map[uint32]*stablecoinEntry
}

type stablecoinEntry struct {
	data       []Stablecoin
	fetched    time.Time
	refreshing bool

	// loading is non-nil while a cold load runs, and closed when it ends.
	loading chan struct{}
	err     error
}

type stablecoinLoader func(ctx context.Context, chainID uint32) ([]Stablecoin, error)

func newStablecoinCache(ttl time.Duration) *stablecoinCache {
	return &stablecoinCache{
		ttl:         ttl,
		loadTimeout: 60 * time.Second,
		entries:     map[uint32]*stablecoinEntry{},
	}
}

func (c *stablecoinCache) get(ctx context.Context, chainID uint32, load stablecoinLoader) ([]Stablecoin, error) {
	c.mu.Lock()
	e := c.entries[chainID]
	if e != nil && e.data != nil {
		data := e.data
		if time.Since(e.fetched) >= c.ttl && !e.refreshing {
			e.refreshing = true
			go c.refresh(chainID, load)
		}
		c.mu.Unlock()
		return data, nil
	}
	if e == nil {
		e = &stablecoinEntry{}
		c.entries[chainID] = e
	}
	if e.loading == nil {
		e.loading = make(chan struct{})
		go c.coldLoad(chainID, e, load)
	}
	wait := e.loading
	c.mu.Unlock()

	// The load runs detached from this request, so a client that gives up
	// does not cancel it for the others waiting on it.
	select {
	case <-wait:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if e.err != nil {
		return nil, e.err
	}
	if e.data == nil {
		return []Stablecoin{}, nil
	}
	return e.data, nil
}

func (c *stablecoinCache) coldLoad(chainID uint32, e *stablecoinEntry, load stablecoinLoader) {
	ctx, cancel := context.WithTimeout(context.Background(), c.loadTimeout)
	defer cancel()
	data, err := load(ctx, chainID)

	c.mu.Lock()
	defer c.mu.Unlock()
	e.err = err
	if err == nil && len(data) > 0 {
		e.data = data
		e.fetched = time.Now()
	} else if c.entries[chainID] == e {
		delete(c.entries, chainID)
	}
	close(e.loading)
	e.loading = nil
}

func (c *stablecoinCache) refresh(chainID uint32, load stablecoinLoader) {
	ctx, cancel := context.WithTimeout(context.Background(), c.loadTimeout)
	defer cancel()
	data, err := load(ctx, chainID)

	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[chainID]
	if e == nil {
		return
	}
	e.refreshing = false
	if err != nil {
		// Keep serving the stale copy; the next request past the TTL retries.
		slog.Warn("Stablecoin list refresh failed", "chain_id", chainID, "error", err)
		return
	}
	if len(data) > 0 {
		e.data = data
		e.fetched = time.Now()
	}
}
