package openmeteo

import (
	"context"
	"sync"
	"time"
)

// flightCache is a bounded TTL cache with single-flight deduplication. The
// mutex is never held while fetch runs.
type flightCache[V any] struct {
	mu       sync.Mutex
	base     context.Context
	ttl      time.Duration
	max      int
	now      func() time.Time
	entries  map[string]cacheEntry[V]
	inflight map[string]*call[V]
	lookups  int64
	hits     int64
	errors   int64
}

type cacheEntry[V any] struct {
	value     V
	expiresAt time.Time
}

type call[V any] struct {
	done  chan struct{}
	value V
	err   error
}

func newFlightCache[V any](base context.Context, ttl time.Duration, maxEntries int, now func() time.Time) *flightCache[V] {
	return &flightCache[V]{
		base:     base,
		ttl:      ttl,
		max:      maxEntries,
		now:      now,
		entries:  make(map[string]cacheEntry[V]),
		inflight: make(map[string]*call[V]),
	}
}

// get returns the cached value for key or runs fetch once for all concurrent callers.
func (c *flightCache[V]) get(ctx context.Context, key string, fetch func(context.Context) (V, error)) (V, error) {
	c.mu.Lock()
	c.lookups++
	if e, ok := c.entries[key]; ok {
		if c.now().Before(e.expiresAt) {
			c.hits++
			c.mu.Unlock()
			return e.value, nil
		}
		delete(c.entries, key)
	}
	if cl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-cl.done:
			return cl.value, cl.err
		case <-ctx.Done():
			var zero V
			return zero, ctx.Err()
		}
	}
	cl := &call[V]{done: make(chan struct{})}
	c.inflight[key] = cl
	c.mu.Unlock()

	// The shared fetch runs under the cache's base context, not the first
	// caller's, so one caller's cancellation does not fail the waiters. The base
	// context is cancelled on module shutdown; the HTTP timeout bounds the rest.
	cl.value, cl.err = fetch(c.base)

	c.mu.Lock()
	if cl.err == nil {
		c.storeLocked(key, cl.value)
	} else {
		c.errors++
	}
	delete(c.inflight, key)
	close(cl.done)
	c.mu.Unlock()
	return cl.value, cl.err
}

// storeLocked caches value. When full it sweeps expired entries and skips the
// insert if still full. mu must be held.
func (c *flightCache[V]) storeLocked(key string, value V) {
	now := c.now()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.max {
		for k, e := range c.entries {
			if !now.Before(e.expiresAt) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.max {
			return
		}
	}
	c.entries[key] = cacheEntry[V]{value: value, expiresAt: now.Add(c.ttl)}
}

type cacheStats struct {
	lookups, hits, errors int64
	size, inflight        int
}

func (c *flightCache[V]) stats() cacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cacheStats{lookups: c.lookups, hits: c.hits, errors: c.errors, size: len(c.entries), inflight: len(c.inflight)}
}
