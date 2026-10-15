package leetoclock

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/util"
)

func registerRecordTools(t *testing.T) *Module {
	t.Helper()
	m, _ := newTestModule(t)
	m.tools = tool.NewRegistry()
	m.registerTools()
	return m
}

func TestRegisterToolsRegistersRecords(t *testing.T) {
	m := registerRecordTools(t)

	if _, ok := m.tools.Get("leetoclock_records"); !ok {
		t.Fatal("leetoclock_records was not registered")
	}
}

func TestRecordsHandlerTop(t *testing.T) {
	m := registerRecordTools(t)
	addRecord(t, m, "guild-1", "chan-1", "alice", "m1", time.Now(), 137)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"top"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "Leet o'Clock top") {
		t.Errorf("top output = %q", out)
	}
}

func TestRecordsHandlerPlayerDefaultsToCaller(t *testing.T) {
	m := registerRecordTools(t)
	addRecord(t, m, "guild-1", "chan-1", "alice", "m1", time.Now(), 137)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1", UserID: "alice"}, json.RawMessage(`{"subcommand":"player"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "<@alice>") {
		t.Errorf("player output = %q", out)
	}
}

func TestRecordsHandlerGlobalPlayerOnlyForCaller(t *testing.T) {
	m := registerRecordTools(t)
	addRecord(t, m, "guild-1", "chan-1", "111111111111111111", "m1", time.Now(), 137)
	addRecord(t, m, "guild-1", "chan-1", "222222222222222222", "m2", time.Now(), 200)
	def, _ := m.tools.Get("leetoclock_records")
	env := tool.Env{GuildID: "guild-1", UserID: "111111111111111111"}

	for _, tc := range []struct {
		args, want string
	}{
		{`{"subcommand":"player","user":"222222222222222222","scope":"global"}`, "global scope is only available for your own records"},
		{`{"subcommand":"player","scope":"global"}`, "<@111111111111111111>"},
		{`{"subcommand":"player","user":"111111111111111111","scope":"global"}`, "<@111111111111111111>"},
		{`{"subcommand":"player","user":"222222222222222222","scope":"server"}`, "<@222222222222222222>"},
	} {
		out, err := def.Handler(context.Background(), env, json.RawMessage(tc.args))
		if err != nil {
			t.Fatalf("handler %s: %v", tc.args, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: output = %q, want %q", tc.args, out, tc.want)
		}
	}
}

func TestRecordsHandlerRejectsUnknownSubcommand(t *testing.T) {
	m := registerRecordTools(t)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"bogus"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "top") {
		t.Errorf("unknown subcommand output = %q", out)
	}
}

func TestRecordsHandlerRequiresGuild(t *testing.T) {
	m := registerRecordTools(t)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"subcommand":"top"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "no guild available") {
		t.Errorf("output = %q", out)
	}
}

func TestRecordsHandlerDate(t *testing.T) {
	m := registerRecordTools(t)
	m.now = func() time.Time { return time.Date(2026, time.September, 24, 13, 0, 0, 0, time.Local) }
	addRecord(t, m, "guild-1", "chan-1", "alice", "m-day", time.Date(2026, time.July, 8, 13, 37, 0, 0, time.Local), 137)
	addRecord(t, m, "guild-1", "chan-1", "alice", "m-other", time.Date(2026, time.July, 9, 13, 37, 0, 0, time.Local), 200)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"top","date":"2026-07-08"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "137 ms") || !strings.Contains(out, "2026-07-08") {
		t.Errorf("date output = %q", out)
	}
	if strings.Contains(out, "200 ms") {
		t.Errorf("date output leaked another day: %q", out)
	}

	bad, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"top","date":"08.07.2026"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(bad, "invalid date") {
		t.Errorf("invalid date output = %q", bad)
	}
}

func TestRecordsHandlerDateRange(t *testing.T) {
	m := registerRecordTools(t)
	m.now = func() time.Time { return time.Date(2026, time.September, 24, 13, 0, 0, 0, time.Local) }
	player := "111111111111111111"
	addRecord(t, m, "guild-1", "c1", player, "m-jul-08", time.Date(2026, time.July, 8, 13, 37, 0, 0, time.Local), 100)
	addRecord(t, m, "guild-1", "c1", player, "m-jul-10", time.Date(2026, time.July, 10, 13, 37, 0, 0, time.Local), 137)
	addRecord(t, m, "guild-1", "c1", player, "m-jul-12", time.Date(2026, time.July, 12, 13, 37, 0, 0, time.Local), 200)

	def, _ := m.tools.Get("leetoclock_records")
	call := func(args string) string {
		t.Helper()
		out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(args))
		if err != nil {
			t.Fatalf("handler(%s): %v", args, err)
		}
		return out
	}

	// A closed range includes both ends and nothing outside it.
	out := call(`{"subcommand":"player","user":"` + player + `","from":"2026-07-08","to":"2026-07-10"}`)
	if !strings.Contains(out, "Valid attempts: 2") || !strings.Contains(out, "100 ms") || !strings.Contains(out, "137 ms") || strings.Contains(out, "200 ms") {
		t.Errorf("closed range output = %q", out)
	}
	if !strings.Contains(out, "2026-07-08 – 2026-07-10") {
		t.Errorf("closed range label = %q", out)
	}

	// An open start reaches back to the earliest stored game.
	out = call(`{"subcommand":"player","user":"` + player + `","to":"2026-07-08"}`)
	if !strings.Contains(out, "Valid attempts: 1") || !strings.Contains(out, "100 ms") || !strings.Contains(out, "until 2026-07-08") {
		t.Errorf("open start output = %q", out)
	}

	// An open end reaches forward to now.
	out = call(`{"subcommand":"player","user":"` + player + `","from":"2026-07-11"}`)
	if !strings.Contains(out, "Valid attempts: 1") || !strings.Contains(out, "200 ms") || !strings.Contains(out, "from 2026-07-11") {
		t.Errorf("open end output = %q", out)
	}

	// A range overrides period, and the top board only keeps each player's best.
	out = call(`{"subcommand":"top","from":"2026-07-08","to":"2026-07-10"}`)
	if !strings.Contains(out, "100 ms") || strings.Contains(out, "137 ms") || strings.Contains(out, "200 ms") {
		t.Errorf("top range output = %q", out)
	}

	if got := call(`{"subcommand":"top","date":"2026-07-08","from":"2026-07-01"}`); !strings.Contains(got, "not both") {
		t.Errorf("date+range output = %q", got)
	}
	if got := call(`{"subcommand":"top","from":"2026-07-10","to":"2026-07-08"}`); !strings.Contains(got, "after") {
		t.Errorf("reversed range output = %q", got)
	}
	if got := call(`{"subcommand":"top","from":"07/08/2026"}`); !strings.Contains(got, "invalid from") {
		t.Errorf("invalid from output = %q", got)
	}
	if got := call(`{"subcommand":"top","to":"07/08/2026"}`); !strings.Contains(got, "invalid to") {
		t.Errorf("invalid to output = %q", got)
	}
}

func TestRecordsHandlerPeriodPresets(t *testing.T) {
	m := registerRecordTools(t)
	m.now = func() time.Time { return time.Date(2026, time.September, 24, 13, 0, 0, 0, time.Local) }
	player := "111111111111111111"
	addRecord(t, m, "guild-1", "c1", player, "today", time.Date(2026, time.September, 24, 8, 0, 0, 0, time.Local), 1)
	addRecord(t, m, "guild-1", "c1", player, "yesterday", time.Date(2026, time.September, 23, 8, 0, 0, 0, time.Local), 2)
	addRecord(t, m, "guild-1", "c1", player, "last-month", time.Date(2026, time.August, 24, 8, 0, 0, 0, time.Local), 3)

	def, _ := m.tools.Get("leetoclock_records")
	for _, tc := range []struct {
		period string
		want   int
	}{
		{"today", 1},
		{"yesterday", 1},
		{"week", 2},
		{"year", 3},
		{"all", 3},
	} {
		out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"player","user":"`+player+`","period":"`+tc.period+`"}`))
		if err != nil {
			t.Fatalf("period %s: %v", tc.period, err)
		}
		if !strings.Contains(out, fmt.Sprintf("Valid attempts: %d", tc.want)) {
			t.Errorf("period %s output = %q, want %d attempts", tc.period, out, tc.want)
		}
	}
}

func TestRecordsHandlerDaily(t *testing.T) {
	m := registerRecordTools(t)
	m.now = func() time.Time { return time.Date(2026, time.September, 24, 13, 0, 0, 0, time.Local) }
	addRecord(t, m, "guild-1", "c1", "alice", "d1-a", time.Date(2026, time.September, 10, 13, 37, 0, 0, time.Local), 60)
	addRecord(t, m, "guild-1", "c1", "bob", "d1-b", time.Date(2026, time.September, 10, 13, 37, 1, 0, time.Local), 200)
	addRecord(t, m, "guild-1", "c1", "bob", "d2", time.Date(2026, time.September, 11, 13, 37, 0, 0, time.Local), 90)

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"daily","period":"month"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "daily winners") || !strings.Contains(out, "60 ms") || !strings.Contains(out, "90 ms") || strings.Contains(out, "200 ms") {
		t.Errorf("daily output = %q", out)
	}
}

func TestRecordsToolSchemaHasDaily(t *testing.T) {
	var schema struct {
		Properties struct {
			Subcommand struct {
				Enum []string `json:"enum"`
			} `json:"subcommand"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(leetoclockRecordsParameters, &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, want := range []string{"top", "daily", "player"} {
		found := false
		for _, value := range schema.Properties.Subcommand.Enum {
			if value == want {
				found = true
			}
		}
		if !found {
			t.Errorf("subcommand enum %v lacks %q", schema.Properties.Subcommand.Enum, want)
		}
	}
}

func TestRegisterToolsNilRegistryIsSafe(t *testing.T) {
	m := New()
	m.registerTools()
}

func TestRecordsHandlerResolvesPlayerName(t *testing.T) {
	m := registerRecordTools(t)
	addRecord(t, m, "guild-1", "chan-1", "42", "m1", time.Now(), 137)
	m.resolveMember = func(_ *discordgo.Session, guildID, query string) (util.MemberMatch, bool, string) {
		if guildID != "guild-1" || query != "s1kk" {
			return util.MemberMatch{}, false, "unexpected lookup"
		}
		return util.MemberMatch{ID: "42", Username: "sikk"}, true, ""
	}

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"player","user":"s1kk"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "<@42>") {
		t.Errorf("resolved player output = %q, want mention of 42", out)
	}
}

func TestRecordsHandlerAmbiguousPlayerName(t *testing.T) {
	m := registerRecordTools(t)
	m.resolveMember = func(_ *discordgo.Session, _, _ string) (util.MemberMatch, bool, string) {
		return util.MemberMatch{}, false, `"sikk" matches several members; ask the user which one`
	}

	def, _ := m.tools.Get("leetoclock_records")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"player","user":"sikk"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "several members") {
		t.Errorf("ambiguous player output = %q", out)
	}
}

func TestRecordsHandlerNumericUserSkipsResolution(t *testing.T) {
	m := registerRecordTools(t)
	m.resolveMember = func(_ *discordgo.Session, _, _ string) (util.MemberMatch, bool, string) {
		t.Fatal("resolver called for a numeric user ID")
		return util.MemberMatch{}, false, ""
	}

	def, _ := m.tools.Get("leetoclock_records")
	if _, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"subcommand":"player","user":"123456789012345678"}`)); err != nil {
		t.Fatalf("handler: %v", err)
	}
}
