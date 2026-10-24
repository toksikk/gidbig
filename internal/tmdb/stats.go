package tmdb

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"

	"github.com/toksikk/gidbig/internal/bot"
)

// counters are in-memory runtime counters since boot, reported in /status.
type counters struct {
	searches    atomic.Int64
	picks       atomic.Int64
	toolCalls   atomic.Int64
	cacheHits   atomic.Int64
	errors      atomic.Int64
	rateLimited atomic.Int64
}

// recordError counts a failed TMDB call; 429s are also counted separately.
func (c *counters) recordError(err error) {
	if err == nil {
		return
	}
	c.errors.Add(1)
	if errors.Is(err, ErrRateLimited) {
		c.rateLimited.Add(1)
	}
}

// Stats reports tmdb counters for /status.
func (m *Module) Stats(_ context.Context) (bot.ModuleStats, error) {
	f := func(v *atomic.Int64) string { return strconv.FormatInt(v.Load(), 10) }
	m.mu.Lock()
	cached := len(m.cache)
	m.mu.Unlock()
	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "searches", Value: f(&m.stats.searches)},
			{Name: "picks", Value: f(&m.stats.picks)},
			{Name: "tool calls", Value: f(&m.stats.toolCalls)},
		},
		Detail: []bot.Stat{
			{Name: "cache hits", Value: f(&m.stats.cacheHits)},
			{Name: "cached titles", Value: strconv.Itoa(cached)},
			{Name: "errors", Value: f(&m.stats.errors)},
			{Name: "rate limited", Value: f(&m.stats.rateLimited)},
		},
	}, nil
}
