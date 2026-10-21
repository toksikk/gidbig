package openmeteo

import (
	"context"
	"strconv"

	"github.com/toksikk/gidbig/internal/bot"
)

// Stats reports in-memory lookup counters for /status.
func (m *Module) Stats(_ context.Context) (bot.ModuleStats, error) {
	fc := m.forecasts.stats()
	geo := m.geocodes.stats()
	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "forecast lookups", Value: strconv.FormatInt(fc.lookups, 10)},
			{Name: "cache", Value: strconv.Itoa(fc.size)},
		},
		Detail: []bot.Stat{
			{Name: "cache hits", Value: strconv.FormatInt(fc.hits, 10)},
			{Name: "geocode lookups", Value: strconv.FormatInt(geo.lookups, 10)},
			{Name: "geocode hits", Value: strconv.FormatInt(geo.hits, 10)},
			{Name: "fetch errors", Value: strconv.FormatInt(fc.errors+geo.errors, 10)},
			{Name: "in flight", Value: strconv.Itoa(fc.inflight + geo.inflight)},
		},
	}, nil
}
