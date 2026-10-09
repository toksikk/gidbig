package gidbig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

func sampleSnapshot() statusSnapshot {
	return statusSnapshot{
		Version:    "v1.2.3",
		GoVersion:  "go1.26.0",
		Discordgo:  "0.29.0",
		Platform:   "linux/amd64",
		Started:    time.Date(2026, 10, 6, 5, 47, 0, 0, time.UTC),
		Uptime:     3*24*time.Hour + 4*time.Hour + 12*time.Minute,
		Guilds:     2,
		Goroutines: 38,
		VoiceConns: 1,
		Latency:    41 * time.Millisecond,
		HeapAlloc:  24 << 20,
		NumGC:      1204,
		DBFiles:    []dbFileStatus{{Path: "data/gidbig.db", Size: 18 << 20}},
		Modules: []moduleStatus{
			{Name: "coffee", Stats: bot.ModuleStats{
				Summary: []bot.Stat{{Name: "cups", Value: "642"}},
				Detail:  []bot.Stat{{Name: "refills", Value: "88"}},
			}},
			{Name: "gippity", Err: errors.New("timed out after 500ms")},
		},
		GuildList: []guildStatus{{Name: "Big Guild", Members: 1500, Known: 2, Voice: true}, {Name: "Small", Members: 3, Known: 1}},
		Users: []userStatus{
			{Name: "alice", Guilds: []string{"Big Guild", "Small"}},
			{Name: "bob", Guilds: []string{"Big Guild"}},
		},
	}
}

func TestRenderStatusTextSummaryLayout(t *testing.T) {
	out := renderStatusText(sampleSnapshot(), statusViewSummary, discordMessageLimit)

	for _, want := range []string{
		"gidbig v1.2.3 · go1.26.0 linux/amd64",
		"up 3d 4h 12m · since 2026-10-06 05:47 UTC",
		"── Runtime",
		"guilds",
		"41 ms",
		"gidbig.db",
		"18 MiB",
		"coffee      642 cups",
		"gippity     n/a",
		"! gippity: timed out after 500ms",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"refills", "Big Guild", "alice", "discordgo", "TotalAlloc"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("summary should not contain %q:\n%s", unwanted, out)
		}
	}
}

func TestRenderStatusTextDetailedAddsDetailAndGuilds(t *testing.T) {
	out := renderStatusText(sampleSnapshot(), statusViewDetailed, discordMessageLimit)
	for _, want := range []string{"88 refills", "── Guilds (2)", "Big Guild", "1,500 members · 2 known · voice", "── Known users (2)", "alice", "Big Guild, Small", "discordgo", "sys mem"} {
		if !strings.Contains(out, want) {
			t.Errorf("detailed missing %q:\n%s", want, out)
		}
	}
}

func TestRenderStatusTextFitsLimit(t *testing.T) {
	snap := sampleSnapshot()
	for i := 0; i < 200; i++ {
		snap.GuildList = append(snap.GuildList, guildStatus{Name: fmt.Sprintf("Guild %03d", i), Members: i})
	}
	limit := discordMessageLimit - 6
	out := renderStatusText(snap, statusViewDetailed, limit)
	if n := len([]rune(out)); n > limit {
		t.Fatalf("output %d runes exceeds %d", n, limit)
	}
	if !strings.Contains(out, "more") {
		t.Errorf("expected omission marker:\n%s", out)
	}
	if !strings.Contains(out, "── Runtime") || !strings.Contains(out, "coffee") {
		t.Errorf("trimming dropped non-guild sections:\n%s", out)
	}

	data := statusTextResponseData(snap, statusViewDetailed)
	if n := len([]rune(data.Content)); n > discordMessageLimit {
		t.Fatalf("response %d runes exceeds Discord limit", n)
	}
	if data.Flags != discordgo.MessageFlagsEphemeral {
		t.Error("text response must be ephemeral")
	}
}

func TestStatusEmbedResponse(t *testing.T) {
	data := statusEmbedResponseData(sampleSnapshot(), statusViewSummary)
	if data.Flags != discordgo.MessageFlagsEphemeral {
		t.Error("embed response must be ephemeral")
	}
	if len(data.Embeds) != 1 {
		t.Fatalf("embeds = %d, want 1", len(data.Embeds))
	}
	e := data.Embeds[0]
	if e.Color != statusEmbedColorWarn {
		t.Errorf("color = %x, want warning color", e.Color)
	}
	names := map[string]string{}
	total := len(e.Title) + len(e.Description) + len(e.Footer.Text)
	for _, f := range e.Fields {
		names[f.Name] = f.Value
		if len(f.Value) > embedFieldValueLimit {
			t.Errorf("field %q value too long: %d", f.Name, len(f.Value))
		}
		total += len(f.Name) + len(f.Value)
	}
	if total > 6000 {
		t.Errorf("embed total %d exceeds 6000", total)
	}
	for _, want := range []string{"Runtime", "Database", "coffee", "gippity", "Warnings"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing field %q", want)
		}
	}
	if strings.Contains(names["coffee"], "refills") {
		t.Error("summary embed should not include detail stats")
	}

	detailed := statusEmbedResponseData(sampleSnapshot(), statusViewDetailed).Embeds[0]
	found := false
	for _, f := range detailed.Fields {
		if f.Name == "coffee" && strings.Contains(f.Value, "refills") {
			found = true
		}
		if strings.HasPrefix(f.Name, "Guilds") && !strings.Contains(f.Value, "Big Guild") {
			t.Errorf("guild field missing guild: %q", f.Value)
		}
	}
	if !found {
		t.Error("detailed embed should include detail stats")
	}
}

func TestStatusEmbedGuildFieldTruncated(t *testing.T) {
	snap := sampleSnapshot()
	for i := 0; i < 300; i++ {
		snap.GuildList = append(snap.GuildList, guildStatus{Name: fmt.Sprintf("Guild %03d", i)})
	}
	e := statusEmbedResponseData(snap, statusViewDetailed).Embeds[0]
	for _, f := range e.Fields {
		if len(f.Value) > embedFieldValueLimit {
			t.Fatalf("field %q value %d exceeds limit", f.Name, len(f.Value))
		}
	}
}

func TestStatusOptions(t *testing.T) {
	opt := func(name, val string) *discordgo.ApplicationCommandInteractionDataOption {
		return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionString, Value: val}
	}
	if v, f := statusOptions(nil); v != statusViewSummary || f != statusFormatText {
		t.Errorf("defaults = %q,%q", v, f)
	}
	v, f := statusOptions([]*discordgo.ApplicationCommandInteractionDataOption{
		opt(statusOptionView, statusViewDetailed), opt(statusOptionFormat, statusFormatEmbed),
	})
	if v != statusViewDetailed || f != statusFormatEmbed {
		t.Errorf("got %q,%q", v, f)
	}
	if v, f := statusOptions([]*discordgo.ApplicationCommandInteractionDataOption{opt(statusOptionView, "bogus")}); v != statusViewSummary || f != statusFormatText {
		t.Errorf("unknown values should fall back to defaults, got %q,%q", v, f)
	}
}

func TestStatusCommandDefinition(t *testing.T) {
	cmd := statusCommand()
	if cmd.Name != "status" || len(cmd.Options) != 2 {
		t.Fatalf("unexpected command: %+v", cmd)
	}
	for _, o := range cmd.Options {
		if o.Required {
			t.Errorf("option %q must be optional", o.Name)
		}
	}
}

type blockingProvider struct{}

func (blockingProvider) Name() string { return "slow" }
func (blockingProvider) Stats(ctx context.Context) (bot.ModuleStats, error) {
	<-ctx.Done()
	return bot.ModuleStats{}, ctx.Err()
}

type ignoringProvider struct{ release chan struct{} }

func (ignoringProvider) Name() string { return "stuck" }
func (p ignoringProvider) Stats(context.Context) (bot.ModuleStats, error) {
	<-p.release
	return bot.ModuleStats{}, nil
}

type panicProvider struct{}

func (panicProvider) Name() string                                   { return "boom" }
func (panicProvider) Stats(context.Context) (bot.ModuleStats, error) { panic("kaboom") }

func TestCollectModuleStatsProviderFailures(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	ok := bot.StatsFunc{ModuleName: "ok", Fn: func(context.Context) (bot.ModuleStats, error) {
		return bot.ModuleStats{Summary: []bot.Stat{{Name: "n", Value: "1"}}}, nil
	}}
	failing := bot.StatsFunc{ModuleName: "bad", Fn: func(context.Context) (bot.ModuleStats, error) {
		return bot.ModuleStats{}, errors.New("db locked")
	}}

	start := time.Now()
	got := collectModuleStats(context.Background(), []bot.StatsProvider{ok, failing, blockingProvider{}, ignoringProvider{release}, panicProvider{}}, 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("collection took %s; timeout not enforced", elapsed)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Err != nil || got[0].Stats.Summary[0].Value != "1" {
		t.Errorf("ok provider: %+v", got[0])
	}
	if got[1].Err == nil || !strings.Contains(got[1].Err.Error(), "db locked") {
		t.Errorf("failing provider: %+v", got[1])
	}
	for _, i := range []int{2, 3} {
		if got[i].Err == nil || !strings.Contains(got[i].Err.Error(), "timed out") {
			t.Errorf("%s provider: %+v", got[i].Name, got[i])
		}
	}
	if got[4].Err == nil || !strings.Contains(got[4].Err.Error(), "panic") {
		t.Errorf("panic provider: %+v", got[4])
	}
}

func TestCollectDBFilesDedupesAndReportsMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "gidbig.db")
	if err := os.WriteFile(p, make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p+"-wal", make([]byte, 20), 0o600); err != nil {
		t.Fatal(err)
	}
	got := collectDBFiles([]string{p, p, filepath.Join(dir, "missing.db"), ""})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Err != nil || got[0].Size != 120 {
		t.Errorf("db = %+v, want size 120", got[0])
	}
	if got[1].Err == nil {
		t.Error("missing db should report error")
	}
}

func TestSoundboardStatsProvider(t *testing.T) {
	orig := COLLECTIONS
	t.Cleanup(func() { COLLECTIONS = orig })
	COLLECTIONS = []*soundCollection{{Prefix: "a", Sounds: []*soundClip{{Name: "x"}, {Name: "y"}}}}

	st, err := soundboardStatsProvider().Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if joinStats(st.Summary) != "2 sounds · 1 collections · 0 queued" {
		t.Errorf("summary = %q", joinStats(st.Summary))
	}
}

func TestFormatUptime(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second:                              "1m",
		5 * time.Minute:                               "5m",
		2*time.Hour + 3*time.Minute:                   "2h 3m",
		50*time.Hour + 1*time.Minute + 20*time.Second: "2d 2h 1m",
	}
	for d, want := range cases {
		if got := formatUptime(d); got != want {
			t.Errorf("formatUptime(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestCollectStatusKnownUsers(t *testing.T) {
	s := &discordgo.Session{State: discordgo.NewState(), VoiceConnections: map[string]*discordgo.VoiceConnection{}}
	alice := &discordgo.User{ID: "1", Username: "alice", GlobalName: "Alice"}
	bob := &discordgo.User{ID: "2", Username: "bob"}
	botUser := &discordgo.User{ID: "3", Username: "gidbig", Bot: true}
	guilds := []*discordgo.Guild{
		{ID: "g1", Name: "Zeta", MemberCount: 10, Members: []*discordgo.Member{{User: alice}, {User: bob}, {User: botUser}}},
		{ID: "g2", Name: "Alpha", MemberCount: 5, Members: []*discordgo.Member{{User: alice}}},
	}
	for _, g := range guilds {
		if err := s.State.GuildAdd(g); err != nil {
			t.Fatal(err)
		}
	}

	snap := collectStatus(context.Background(), s, nil, nil)
	if len(snap.Users) != 2 {
		t.Fatalf("users = %+v, want 2 (bots skipped)", snap.Users)
	}
	if snap.Users[0].Name != "Alice (alice)" || strings.Join(snap.Users[0].Guilds, ",") != "Alpha,Zeta" {
		t.Errorf("first user = %+v, want Alice in Alpha,Zeta", snap.Users[0])
	}
	if snap.Users[1].Name != "bob" || len(snap.Users[1].Guilds) != 1 {
		t.Errorf("second user = %+v", snap.Users[1])
	}
	known := map[string]int{}
	for _, g := range snap.GuildList {
		known[g.Name] = g.Known
	}
	if known["Zeta"] != 2 || known["Alpha"] != 1 {
		t.Errorf("known counts = %v", known)
	}
}

func TestRenderStatusTextTrimsUsersBeforeGuilds(t *testing.T) {
	snap := sampleSnapshot()
	for i := 0; i < 500; i++ {
		snap.Users = append(snap.Users, userStatus{Name: fmt.Sprintf("user%03d", i), Guilds: []string{"Big Guild"}})
	}
	limit := discordMessageLimit - 6
	out := renderStatusText(snap, statusViewDetailed, limit)
	if n := len([]rune(out)); n > limit {
		t.Fatalf("output %d runes exceeds %d", n, limit)
	}
	for _, want := range []string{"Small", "! gippity", "── Known users (502)", "more"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
