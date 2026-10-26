package openholidays

import (
	"context"
	"strconv"

	"github.com/toksikk/gidbig/internal/bot"
)

// Stats reports in-memory lookup counters for /status.
func (m *Module) Stats(_ context.Context) (bot.ModuleStats, error) {
	h := m.holidays.stats()
	c := m.countries.stats()
	sd := m.subdivisions.stats()
	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "holiday lookups", Value: strconv.FormatInt(h.lookups, 10)},
			{Name: "cache", Value: strconv.Itoa(h.size)},
		},
		Detail: []bot.Stat{
			{Name: "cache hits", Value: strconv.FormatInt(h.hits, 10)},
			{Name: "cache misses", Value: strconv.FormatInt(h.lookups-h.hits, 10)},
			{Name: "metadata lookups", Value: strconv.FormatInt(c.lookups+sd.lookups, 10)},
			{Name: "api errors", Value: strconv.FormatInt(h.errors+c.errors+sd.errors, 10)},
			{Name: "in flight", Value: strconv.Itoa(h.inflight + c.inflight + sd.inflight)},
		},
	}, nil
}
