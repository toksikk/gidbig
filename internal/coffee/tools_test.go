package coffee

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/util"
)

func registerCoffeeTools(t *testing.T) *Module {
	t.Helper()
	m := newTestModule(t)
	m.tools = tool.NewRegistry()
	m.registerTools()
	return m
}

func TestRegisterToolsRegistersCoffeeTools(t *testing.T) {
	m := registerCoffeeTools(t)

	for _, name := range []string{"coffee_status", "coffee_stats"} {
		if _, ok := m.tools.Get(name); !ok {
			t.Errorf("%s was not registered", name)
		}
	}
}

func TestCoffeeStatusHandlerReportsLevels(t *testing.T) {
	m := registerCoffeeTools(t)

	def, _ := m.tools.Get("coffee_status")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "Mild beans") {
		t.Errorf("status output missing machine levels: %q", out)
	}
}

func TestCoffeeStatsHandlerDefaultsToCaller(t *testing.T) {
	m := registerCoffeeTools(t)

	def, _ := m.tools.Get("coffee_stats")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1", UserID: "alice"}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "<@alice>") {
		t.Errorf("stats output missing caller: %q", out)
	}
}

func TestCoffeeToolsRequireGuild(t *testing.T) {
	m := registerCoffeeTools(t)

	for _, name := range []string{"coffee_status", "coffee_stats"} {
		def, _ := m.tools.Get(name)
		out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("%s handler: %v", name, err)
		}
		if !strings.Contains(out, "no guild available") {
			t.Errorf("%s without guild = %q", name, out)
		}
	}
}

func TestRegisterToolsNilRegistryIsSafe(t *testing.T) {
	m := New()
	m.registerTools()
}

func TestCoffeeStatsHandlerResolvesUserName(t *testing.T) {
	m := registerCoffeeTools(t)
	m.resolveMember = func(_ *discordgo.Session, guildID, query string) (util.MemberMatch, bool, string) {
		if guildID != "guild-1" || query != "s1kk" {
			return util.MemberMatch{}, false, "unexpected lookup"
		}
		return util.MemberMatch{ID: "42", Username: "sikk"}, true, ""
	}

	def, _ := m.tools.Get("coffee_stats")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"user":"s1kk"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "<@42>") {
		t.Errorf("resolved user output = %q, want mention of 42", out)
	}
}

func TestCoffeeStatsHandlerAmbiguousUserName(t *testing.T) {
	m := registerCoffeeTools(t)
	m.resolveMember = func(_ *discordgo.Session, _, _ string) (util.MemberMatch, bool, string) {
		return util.MemberMatch{}, false, `"sikk" matches several members; ask the user which one`
	}

	def, _ := m.tools.Get("coffee_stats")
	out, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"user":"sikk"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "several members") {
		t.Errorf("ambiguous user output = %q", out)
	}
}

func TestCoffeeStatsHandlerNumericUserSkipsResolution(t *testing.T) {
	m := registerCoffeeTools(t)
	m.resolveMember = func(_ *discordgo.Session, _, _ string) (util.MemberMatch, bool, string) {
		t.Fatal("resolver called for a numeric user ID")
		return util.MemberMatch{}, false, ""
	}

	def, _ := m.tools.Get("coffee_stats")
	if _, err := def.Handler(context.Background(), tool.Env{GuildID: "guild-1"}, json.RawMessage(`{"user":"123456789012345678"}`)); err != nil {
		t.Fatalf("handler: %v", err)
	}
}
