package tv

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/tool"
)

func enabledConfig() *cfg.Config {
	c := &cfg.Config{}
	c.TV.Enabled = true
	c.TV.Channels = fixtureChannels
	return c
}

// readyModule returns an enabled module serving the fixture index.
func readyModule(t *testing.T) *Module {
	t.Helper()
	m := New()
	m.now = func() time.Time { return fixtureNow }
	m.enabled = true
	m.loc = berlin
	m.idx.Store(fixtureIndex(t))
	return m
}

func TestInit_Disabled(t *testing.T) {
	tools := tool.NewRegistry()
	b := bot.New(bot.Deps{Config: &cfg.Config{}, Tools: tools})
	m := New()
	if err := b.RegisterModule(m); err != nil {
		t.Fatalf("register: %v", err)
	}
	for _, c := range b.Router.Commands() {
		if c.Name == commandName {
			t.Error("/tv registered while disabled")
		}
	}
	if _, ok := tools.Get("tv_program"); ok {
		t.Error("tv_program registered while disabled")
	}
	if m.Background() != nil {
		t.Error("background task while disabled")
	}
	st, _ := m.Stats(context.Background())
	if len(st.Summary) != 1 || st.Summary[0].Value != "disabled" {
		t.Errorf("stats = %+v", st)
	}
}

func TestInit_Enabled(t *testing.T) {
	tools := tool.NewRegistry()
	b := bot.New(bot.Deps{Config: enabledConfig(), Tools: tools})
	m := New()
	m.fetchers = []*fetcher{newFetcher(t.TempDir(), "de")}
	if err := b.RegisterModule(m); err != nil {
		t.Fatalf("register: %v", err)
	}
	found := false
	for _, c := range b.Router.Commands() {
		found = found || c.Name == commandName
	}
	if !found {
		t.Error("/tv not registered")
	}
	if _, ok := tools.Get("tv_program"); !ok {
		t.Error("tv_program not registered")
	}
	if bg := m.Background(); len(bg) != 1 || bg[0].Name != "tv-refresh" {
		t.Errorf("background = %+v", bg)
	}
	if m.interval != cfg.DefaultTVRefreshInterval {
		t.Errorf("interval = %s", m.interval)
	}
}

// startRun runs the refresh task until the test ends and waits for an index.
func startRun(t *testing.T, m *Module) (cancel context.CancelFunc, done chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() {
		defer close(done)
		m.run(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for m.index() == nil {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("index not built")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cancel, done
}

func moduleWithFetcher(f *fetcher) *Module {
	m := New()
	m.enabled = true
	m.now = func() time.Time { return fixtureNow }
	m.channels = fixtureChannels
	m.interval = time.Hour
	m.loc = berlin
	m.fetchers = []*fetcher{f}
	return m
}

func TestRun_FreshCacheSkipsDownload(t *testing.T) {
	feed := newFakeFeed(t, fixtureGzip(t))
	f := newTestFetcher(t, feed.srv.URL)
	if _, _, err := f.fetch(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := moduleWithFetcher(f)
	cancel, done := startRun(t, m)
	cancel()
	<-done
	if got := feed.hits.Load(); got != 1 {
		t.Errorf("HTTP hits = %d, want 1 (seed only)", got)
	}
	if m.index().programmeCount() == 0 {
		t.Error("empty index")
	}
}

func TestRun_StaleCacheFetches(t *testing.T) {
	feed := newFakeFeed(t, fixtureGzip(t))
	f := newTestFetcher(t, feed.srv.URL)
	if _, _, err := f.fetch(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.now = func() time.Time { return fixtureNow.Add(25 * time.Hour) }
	m := moduleWithFetcher(f)
	cancel, done := startRun(t, m)
	cancel()
	<-done
	if got := feed.hits.Load(); got != 2 {
		t.Errorf("HTTP hits = %d, want 2", got)
	}
	st, _ := m.Stats(context.Background())
	if v := statValue(st, "last fetch status"); v != "de:304" {
		t.Errorf("last fetch status = %q", v)
	}
}

func TestRun_NoCacheDownloadsAndCancels(t *testing.T) {
	feed := newFakeFeed(t, fixtureGzip(t))
	m := moduleWithFetcher(newTestFetcher(t, feed.srv.URL))
	cancel, done := startRun(t, m)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop after cancel")
	}
	if feed.hits.Load() != 1 {
		t.Errorf("hits = %d", feed.hits.Load())
	}
}

func TestRefresh_ErrorKeepsIndex(t *testing.T) {
	feed := newFakeFeed(t, nil)
	feed.status = 500
	m := moduleWithFetcher(newTestFetcher(t, feed.srv.URL))
	ix := fixtureIndex(t)
	m.idx.Store(ix)
	m.refresh(context.Background(), m.fetchers)
	if m.index() != ix {
		t.Error("index replaced after failed fetch")
	}
	st, _ := m.Stats(context.Background())
	if statValue(st, "fetch errors") != "1" || statValue(st, "last fetch status") != "de:error" {
		t.Errorf("stats = %+v", st)
	}
	if statValue(st, "programmes") == "0" {
		t.Errorf("programmes = 0")
	}
}

func statValue(st bot.ModuleStats, name string) string {
	for _, s := range append(st.Summary, st.Detail...) {
		if s.Name == name {
			return s.Value
		}
	}
	return ""
}

func cmdData(sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) discordgo.ApplicationCommandInteractionData {
	return discordgo.ApplicationCommandInteractionData{
		Name: commandName,
		Options: []*discordgo.ApplicationCommandInteractionDataOption{{
			Name: sub, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts,
		}},
	}
}

func strOpt(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionString, Value: value}
}

func TestAnswer_NotReady(t *testing.T) {
	m := New()
	if got, eph := m.answer(cmdData("now")); got != msgNotReady || !eph {
		t.Errorf("answer = %q %v", got, eph)
	}
	out, err := m.toolHandler(context.Background(), tool.Env{}, json.RawMessage(`{"mode":"now"}`))
	if err != nil || out != msgNotReady {
		t.Errorf("tool = %q %v", out, err)
	}
}

func TestAnswer(t *testing.T) {
	m := readyModule(t)
	cases := []struct {
		data      discordgo.ApplicationCommandInteractionData
		want      []string
		ephemeral bool
	}{
		{cmdData("now"), []string{"**ZDF** 20:15 Der Alte · danach 21:45 heute journal", "**Das Erste** keine Daten · danach 22:15 Tatort"}, false},
		{cmdData("now", strOpt("channel", "zdf")), []string{"**ZDF** 20:15–21:45 · Der Alte: Tod in München (Krimi) · danach 21:45 heute journal"}, false},
		{cmdData("now", strOpt("channel", "ard")), []string{"**Das Erste**"}, false},
		{cmdData("now", strOpt("channel", "Erst")), []string{"Sender unbekannt", "Meintest du: Das Erste?"}, true},
		{cmdData("channel", strOpt("channel", "ZDF")), []string{"**ZDF** – Sa 24.10.2026", "19:00–19:15 · heute (Nachrichten)"}, false},
		{cmdData("channel", strOpt("channel", "ZDF"), strOpt("day", "morgen")), []string{"So 25.10.2026", "01:00–02:00 · Nachtfilm"}, false},
		{cmdData("channel", strOpt("channel", "ZDF"), strOpt("day", "2026-12-01")), []string{"Tag unbekannt"}, true},
		{cmdData("search", strOpt("title", "tatort")), []string{"**Das Erste** Sa 24.10.2026 22:15–23:45 · Tatort: Der letzte Schrei", "Mo 26.10.2026"}, false},
		{cmdData("search", strOpt("title", "xyz")), []string{"Keine Sendung gefunden"}, false},
	}
	for _, c := range cases {
		got, eph := m.answer(c.data)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("answer(%s) missing %q in:\n%s", c.data.Options[0].Name, w, got)
			}
		}
		if eph != c.ephemeral {
			t.Errorf("answer(%s) ephemeral = %v", c.data.Options[0].Name, eph)
		}
		if strings.Contains(got, "ää") {
			t.Errorf("command reply contains description:\n%s", got)
		}
	}
}

func TestChannelChoices(t *testing.T) {
	m := readyModule(t)
	data := cmdData("channel", &discordgo.ApplicationCommandInteractionDataOption{Name: "channel", Type: discordgo.ApplicationCommandOptionString, Value: "erste", Focused: true})
	got := m.channelChoices(data)
	if len(got) != 1 || got[0].Name != "Das Erste" || got[0].Value != "DasErste.de" {
		t.Errorf("choices = %+v", got)
	}
	if got := New().channelChoices(data); got == nil || len(got) != 0 {
		t.Errorf("not ready choices = %+v", got)
	}
}

func TestCapLines(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = strings.Repeat("x", 50)
	}
	out := capLines(lines, discordLimit)
	if n := len([]rune(out)); n > discordLimit || !strings.HasSuffix(out, "\n…") {
		t.Errorf("len = %d, tail = %q", n, out[len(out)-5:])
	}
	if out := capLines([]string{"a", "b"}, discordLimit); out != "a\nb" {
		t.Errorf("short = %q", out)
	}
}

func TestParseDay(t *testing.T) {
	for in, want := range map[string]int{"": 24, "heute": 24, "Morgen": 25, "übermorgen": 26, "uebermorgen": 26, "gestern": 23, "2026-10-31": 31} {
		got, ok := parseDay(in, fixtureNow, berlin)
		if !ok || got.Day() != want {
			t.Errorf("parseDay(%q) = %v %v, want day %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"2026-10-22", "2026-11-01", "bald"} {
		if _, ok := parseDay(in, fixtureNow, berlin); ok {
			t.Errorf("parseDay(%q) accepted", in)
		}
	}
}

func TestToolHandler(t *testing.T) {
	m := readyModule(t)
	cases := map[string]string{
		`{"mode":"now","channel":"ZDF"}`:                   "Ein Fall & mehr.",
		`{"mode":"channel","channel":"ard","day":"heute"}`: "22:15–23:45 · Tatort",
		`{"mode":"search","query":"Tatort"}`:               strings.Repeat("ä", 10),
		`{"mode":"channel"}`:                               "needs a channel",
		`{"mode":"search"}`:                                "needs a query",
		`{"mode":"later"}`:                                 "mode must be",
		`{"mode":"now","channel":"nope"}`:                  "Sender unbekannt",
	}
	for args, want := range cases {
		out, err := m.toolHandler(context.Background(), tool.Env{}, json.RawMessage(args))
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("tool(%s) = %q %v, want %q", args, out, err, want)
		}
	}
	if _, err := m.toolHandler(context.Background(), tool.Env{}, json.RawMessage(`{`)); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestRefresh_ChangedWithMetaErrorReparses(t *testing.T) {
	feed := newFakeFeed(t, fixtureGzip(t))
	f := newTestFetcher(t, feed.srv.URL)
	if err := os.MkdirAll(f.metaPath(), 0o755); err != nil {
		t.Fatalf("block meta path: %v", err)
	}
	m := moduleWithFetcher(f)
	old := newIndex(nil, nil, fixtureNow, berlin)
	m.idx.Store(old)
	m.refresh(context.Background(), m.fetchers)
	if m.index() == old {
		t.Error("index not rebuilt after the cache was replaced")
	}
}

var _ bot.StatsProvider = (*Module)(nil)

func TestRegisterModule_RegistersStats(t *testing.T) {
	stats := bot.NewStatsRegistry()
	b := bot.New(bot.Deps{Config: &cfg.Config{}, Tools: tool.NewRegistry(), Stats: stats})
	if err := b.RegisterModule(New()); err != nil {
		t.Fatalf("register: %v", err)
	}
	found := false
	for _, p := range stats.All() {
		found = found || p.Name() == "tv"
	}
	if !found {
		t.Error("tv not in stats registry")
	}
}

const atFeed = `<?xml version="1.0" encoding="UTF-8"?>
<tv>
  <channel id="ORF1.at"><display-name>AT - ORF 1</display-name></channel>
  <programme start="20261024201500 +0200" stop="20261024220000 +0200" channel="ORF1.at">
    <title>Tatort</title>
    <sub-title>Wien</sub-title>
  </programme>
</tv>`

func TestRun_MultipleRegionsMergeAndFetchOnlyStale(t *testing.T) {
	dir := t.TempDir()
	de := newFakeFeed(t, fixtureGzip(t))
	at := newFakeFeed(t, []byte(atFeed))
	fde := newFetcher(dir, "de")
	fde.url, fde.now = de.srv.URL, func() time.Time { return fixtureNow }
	fat := newFetcher(dir, "at")
	fat.url, fat.now = at.srv.URL, func() time.Time { return fixtureNow }
	// Only de has a fresh cache; at must be downloaded on start.
	if _, _, err := fde.fetch(context.Background()); err != nil {
		t.Fatalf("seed de: %v", err)
	}
	m := moduleWithFetcher(fde)
	m.fetchers = append(m.fetchers, fat)
	m.channels = []string{"DasErste.de", "ORF1.at"}
	cancel, done := startRun(t, m)
	deadline := time.Now().Add(5 * time.Second)
	for at.hits.Load() == 0 || len(m.index().search("wien", fixtureNow, 5)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("at region not loaded")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if de.hits.Load() != 1 {
		t.Errorf("de hits = %d, want 1 (seed only)", de.hits.Load())
	}
	ix := m.index()
	if ch, ok := ix.resolveChannel("ORF 1"); !ok || ix.channels[ch].Name != "ORF 1" {
		t.Errorf("ORF 1 not resolved: %d %v", ch, ok)
	}
	if _, ok := ix.resolveChannel("ORF1"); !ok {
		t.Error("region suffix not trimmed for alias")
	}
	if hits := ix.search("tatort", fixtureNow, 5); len(hits) != 3 {
		t.Errorf("merged search hits = %d, want 3", len(hits))
	}
	st, _ := m.Stats(context.Background())
	if statValue(st, "regions") != "de,at" || statValue(st, "last fetch status") != "at:200" {
		t.Errorf("stats = %+v", st)
	}
	if _, err := os.Stat(fat.cachePath()); err != nil || !strings.HasSuffix(fat.cachePath(), "epg_at.xml.gz") {
		t.Errorf("at cache %q: %v", fat.cachePath(), err)
	}
}

func TestInit_RegionsAndTimezone(t *testing.T) {
	c := enabledConfig()
	c.TV.Regions = []string{"de", "at"}
	c.TV.Timezone = "Europe/Vienna"
	c.TV.CacheDir = t.TempDir()
	m := New()
	if err := bot.New(bot.Deps{Config: c, Tools: tool.NewRegistry()}).RegisterModule(m); err != nil {
		t.Fatalf("register: %v", err)
	}
	if m.regions() != "de,at" || m.loc.String() != "Europe/Vienna" {
		t.Errorf("regions = %q, loc = %s", m.regions(), m.loc)
	}
	if m.fetchers[1].url != "https://www.free-epg.de/api/epg/at.xml.gz" {
		t.Errorf("at url = %q", m.fetchers[1].url)
	}
}

func TestRender_UsesIndexTimezone(t *testing.T) {
	ix, err := parse(strings.NewReader(fixtureXML(t)), fixtureChannels, fixtureNow, time.UTC)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ch, _ := ix.resolveChannel("ZDF")
	if out := renderNow(ix, []int{ch}, fixtureNow, false); !strings.Contains(out, "18:15–19:45 · Der Alte") {
		t.Errorf("UTC render:\n%s", out)
	}
}
