// Package tv serves the German TV program from the free-epg.de XMLTV feed
// through /tv and the LLM tool tv_program. The feed is cached on disk and
// served from a compact in-memory index.
package tv

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	// Embed the zone database so tv.timezone resolves in minimal images.
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

// Module implements bot.Module for the TV program.
type Module struct {
	enabled  bool
	channels []string
	interval time.Duration
	loc      *time.Location
	fetchers []*fetcher // one per region, in config order
	tools    *tool.Registry
	now      func() time.Time

	idx atomic.Pointer[index]

	lastFetch   atomic.Int64 // Unix seconds of the last fetch attempt
	lastStatus  atomic.Value // string: "<region>:<200|304|error>" per region
	fetchErrors atomic.Int64
}

// New returns a Module; it stays inert unless tv.enabled is set.
func New() *Module { return &Module{now: time.Now, loc: time.UTC} }

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
	loc, err := time.LoadLocation(d.Config.TVTimezone())
	if err != nil {
		return fmt.Errorf("tv: load timezone: %w", err)
	}
	m.loc = loc
	if m.fetchers == nil {
		for _, region := range d.Config.TVRegions() {
			m.fetchers = append(m.fetchers, newFetcher(d.Config.TVCacheDir(), region))
		}
	}
	m.tools = d.Tools
	m.registerTools()
	d.Router.AddCommand(command(), m.onInteraction)
	slog.Info("tv: initialized", "regions", d.Config.TVRegions(), "channels", len(m.channels), "timezone", loc.String(), "refresh", m.interval)
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

// run loads the caches, fetches the regions whose cache is missing or stale
// (all of them when no cache could be loaded), then refreshes every region on
// each tick until ctx is cancelled.
func (m *Module) run(ctx context.Context) {
	due := m.fetchers
	if m.loadCache() {
		due = nil
		for _, f := range m.fetchers {
			if age, ok := f.cacheAge(); !ok || age >= staleCacheAge {
				due = append(due, f)
			}
		}
	}
	if len(due) > 0 {
		m.refresh(ctx, due)
	}
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refresh(ctx, m.fetchers)
		}
	}
}

// refresh performs one conditional fetch per given region and rebuilds the
// index when any cache changed, when no index exists yet, or when the index is
// older than maxIndexAge. A failed region keeps its previous cache.
func (m *Module) refresh(ctx context.Context, fs []*fetcher) {
	m.lastFetch.Store(m.now().Unix())
	changed, failed := false, false
	statuses := make([]string, 0, len(fs))
	for _, f := range fs {
		status, ch, err := f.fetch(ctx)
		if ctx.Err() != nil {
			return
		}
		// A replaced cache counts as changed even if writing its meta failed.
		changed = changed || ch
		if err != nil {
			failed = true
			m.fetchErrors.Add(1)
			status = "error"
			slog.Warn("tv: feed refresh failed, keeping previous data", "region", f.region, "error", err)
		} else {
			slog.Info("tv: feed refreshed", "region", f.region, "status", status, "changed", ch)
		}
		statuses = append(statuses, f.region+":"+status)
	}
	m.lastStatus.Store(strings.Join(statuses, " "))
	ix := m.index()
	if !changed && ix != nil && (failed || m.now().Sub(ix.builtAt) < maxIndexAge) {
		return
	}
	m.loadCache()
}

// loadCache parses every region's cache into a new index and reports success.
// A region without a cache is skipped; a parse error keeps the old index.
func (m *Module) loadCache() bool {
	start := time.Now()
	b := newBuilder(m.channels)
	loaded := 0
	for _, f := range m.fetchers {
		rc, err := f.openCache()
		if err != nil {
			slog.Info("tv: no usable cache", "region", f.region, "error", err)
			continue
		}
		err = b.add(rc)
		_ = rc.Close()
		if err != nil {
			slog.Warn("tv: cache parse failed, keeping previous data", "region", f.region, "error", err)
			return false
		}
		loaded++
	}
	if loaded == 0 {
		return false
	}
	ix := b.build(m.now(), m.loc)
	m.idx.Store(ix)
	slog.Info("tv: index built", "regions", loaded, "channels", len(ix.channels), "programmes", ix.programmeCount(), "took", time.Since(start).Round(time.Millisecond))
	return true
}

// regions lists the configured feed regions.
func (m *Module) regions() string {
	rs := make([]string, len(m.fetchers))
	for i, f := range m.fetchers {
		rs[i] = f.region
	}
	return strings.Join(rs, ",")
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
		built = ix.builtAt.In(m.loc).Format("2006-01-02 15:04")
	}
	lastFetch, lastStatus := "never", "none"
	if ts := m.lastFetch.Load(); ts != 0 {
		lastFetch = time.Unix(ts, 0).In(m.loc).Format("2006-01-02 15:04")
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
			{Name: "regions", Value: m.regions()},
			{Name: "timezone", Value: m.loc.String()},
			{Name: "index built", Value: built},
			{Name: "last fetch", Value: lastFetch},
			{Name: "last fetch status", Value: lastStatus},
			{Name: "fetch errors", Value: fmt.Sprint(m.fetchErrors.Load())},
		},
	}, nil
}
