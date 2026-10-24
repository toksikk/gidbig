// Package tv serves the German TV program from the free-epg.de XMLTV feed
// through /tv and the LLM tool tv_program. The feed is cached on disk and
// served from a compact in-memory index.
package tv

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	// Embed the zone database so Europe/Berlin resolves in minimal images.
	_ "time/tzdata"

	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/tool"
)

const (
	// staleCacheAge forces a download at start when the cache is older.
	staleCacheAge = 24 * time.Hour
	// maxIndexAge rebuilds the index from cache even without a feed change,
	// so the kept time window slides forward.
	maxIndexAge = 24 * time.Hour
	// msgNotReady answers queries before the first index exists.
	msgNotReady = "TV-Programm wird noch geladen, bitte gleich nochmal versuchen."
)

// berlin is the display time zone.
var berlin = loadBerlin()

func loadBerlin() *time.Location {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return time.FixedZone("CET", 3600)
	}
	return loc
}

// Module implements bot.Module for the TV program.
type Module struct {
	enabled  bool
	channels []string
	interval time.Duration
	fetcher  *fetcher
	tools    *tool.Registry
	now      func() time.Time

	idx atomic.Pointer[index]

	lastFetch   atomic.Int64 // Unix seconds of the last fetch attempt
	lastStatus  atomic.Value // string: "200", "304" or "error"
	fetchErrors atomic.Int64
}

// New returns a Module; it stays inert unless tv.enabled is set.
func New() *Module { return &Module{now: time.Now} }

func (m *Module) Name() string { return "tv" }

// Init registers /tv and tv_program when tv.enabled is true. A disabled module
// registers nothing and runs no background task.
func (m *Module) Init(d bot.Deps) error {
	if d.Config == nil || !d.Config.TV.Enabled {
		slog.Info("tv: disabled")
		return nil
	}
	m.enabled = true
	m.channels = defaultChannels
	if len(d.Config.TV.Channels) > 0 {
		m.channels = d.Config.TV.Channels
	}
	m.interval = d.Config.TVRefreshInterval()
	if m.fetcher == nil {
		m.fetcher = newFetcher(d.Config.TVCacheDir())
	}
	m.tools = d.Tools
	m.registerTools()
	d.Router.AddCommand(command(), m.onInteraction)
	slog.Info("tv: initialized", "channels", len(m.channels), "refresh", m.interval)
	return nil
}

// Background returns the feed refresh task when enabled.
func (m *Module) Background() []bot.BackgroundTask {
	if !m.enabled {
		return nil
	}
	return []bot.BackgroundTask{{Name: "tv-refresh", Run: m.run}}
}

func (m *Module) Shutdown() error { return nil }

// index returns the current index or nil before the first build.
func (m *Module) index() *index { return m.idx.Load() }

// run loads the cache, fetches when it is missing or stale, then refreshes on
// every tick until ctx is cancelled.
func (m *Module) run(ctx context.Context) {
	loaded := m.loadCache()
	if age, ok := m.fetcher.cacheAge(); !loaded || !ok || age >= staleCacheAge {
		m.refresh(ctx)
	}
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refresh(ctx)
		}
	}
}

// refresh performs one conditional fetch and rebuilds the index when the feed
// changed or the index is older than maxIndexAge. Failures keep the old index.
func (m *Module) refresh(ctx context.Context) {
	m.lastFetch.Store(m.now().Unix())
	status, changed, err := m.fetcher.fetch(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		m.fetchErrors.Add(1)
		m.lastStatus.Store("error")
		slog.Warn("tv: feed refresh failed, keeping previous data", "error", err)
		// A replaced cache (changed) is still reparsed below.
		if !changed && m.index() != nil {
			return
		}
	} else {
		m.lastStatus.Store(status)
		slog.Info("tv: feed refreshed", "status", status, "changed", changed)
	}
	if ix := m.index(); !changed && err == nil && ix != nil && m.now().Sub(ix.builtAt) < maxIndexAge {
		return
	}
	m.loadCache()
}

// loadCache parses the cache file into a new index and reports success.
func (m *Module) loadCache() bool {
	rc, err := m.fetcher.openCache()
	if err != nil {
		slog.Info("tv: no usable cache", "error", err)
		return false
	}
	defer func() { _ = rc.Close() }()
	start := time.Now()
	ix, err := parse(rc, m.channels, m.now())
	if err != nil {
		slog.Warn("tv: cache parse failed, keeping previous data", "error", err)
		return false
	}
	m.idx.Store(ix)
	slog.Info("tv: index built", "channels", len(ix.channels), "programmes", ix.programmeCount(), "took", time.Since(start).Round(time.Millisecond))
	return true
}

// Stats reports the index size and fetch state for /status.
func (m *Module) Stats(_ context.Context) (bot.ModuleStats, error) {
	if !m.enabled {
		return bot.ModuleStats{Summary: []bot.Stat{{Name: "status", Value: "disabled"}}}, nil
	}
	channels, programmes, built := "0", "0", "never"
	if ix := m.index(); ix != nil {
		channels = strconv.Itoa(len(ix.channels))
		programmes = strconv.Itoa(ix.programmeCount())
		built = ix.builtAt.In(berlin).Format("2006-01-02 15:04")
	}
	lastFetch, lastStatus := "never", "none"
	if ts := m.lastFetch.Load(); ts != 0 {
		lastFetch = time.Unix(ts, 0).In(berlin).Format("2006-01-02 15:04")
	}
	if s, ok := m.lastStatus.Load().(string); ok {
		lastStatus = s
	}
	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "channels", Value: channels},
			{Name: "programmes", Value: programmes},
		},
		Detail: []bot.Stat{
			{Name: "index built", Value: built},
			{Name: "last fetch", Value: lastFetch},
			{Name: "last fetch status", Value: lastStatus},
			{Name: "fetch errors", Value: fmt.Sprint(m.fetchErrors.Load())},
		},
	}, nil
}
