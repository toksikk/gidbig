package wttrin

import (
	"context"
	"strconv"

	"github.com/toksikk/gidbig/internal/bot"
)

// Stats reports in-memory weather lookup counters for /status.
func (m *Module) Stats(_ context.Context) (bot.ModuleStats, error) {
	m.cacheMu.Lock()
	lookups, hits, errs := m.lookups, m.cacheHits, m.fetchErrors
	cached, inflight := len(m.cache), len(m.inflight)
	m.cacheMu.Unlock()

	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "lookups", Value: strconv.FormatInt(lookups, 10)},
			{Name: "cache", Value: strconv.Itoa(cached)},
		},
		Detail: []bot.Stat{
			{Name: "cache hits", Value: strconv.FormatInt(hits, 10)},
			{Name: "fetch errors", Value: strconv.FormatInt(errs, 10)},
			{Name: "in flight", Value: strconv.Itoa(inflight)},
		},
	}, nil
}
