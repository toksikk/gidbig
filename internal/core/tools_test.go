package gidbig

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/tool"
)

func statsProvider(name string, counters ...bot.Stat) bot.StatsProvider {
	return bot.StatsFunc{
		ModuleName: name,
		Fn: func(context.Context) (bot.ModuleStats, error) {
			return bot.ModuleStats{Summary: counters}, nil
		},
	}
}

func newToolTestModule() *Module {
	reg := bot.NewStatsRegistry()
	reg.Register(statsProvider("gippity", bot.Stat{Name: "msgs", Value: "42"}, bot.Stat{Name: "mentions", Value: "7"}))
	reg.Register(statsProvider("coffee", bot.Stat{Name: "cups", Value: "3"}))
	return &Module{stats: reg, tools: tool.NewRegistry()}
}

func TestRegisterToolsRegistersBotStatus(t *testing.T) {
	m := newToolTestModule()
	m.registerTools()

	if _, ok := m.tools.Get("bot_status"); !ok {
		t.Fatal("bot_status was not registered")
	}
}

func TestBotStatusHandlerReportsAllModules(t *testing.T) {
	m := newToolTestModule()
	m.registerTools()

	def, ok := m.tools.Get("bot_status")
	if !ok {
		t.Fatal("bot_status missing")
	}
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "msgs") || !strings.Contains(out, "cups") {
		t.Errorf("output missing module counters: %q", out)
	}
	if !strings.HasPrefix(out, "runtime: ") {
		t.Errorf("output missing runtime line: %q", out)
	}
	for _, want := range []string{"heap=", "sys=", "goroutines=", "gc_cycles=", "uptime="} {
		if !strings.Contains(out, want) {
			t.Errorf("runtime line missing %q: %q", want, out)
		}
	}
}

func TestBotStatusHandlerRuntimeOnlyWithoutModules(t *testing.T) {
	m := &Module{stats: bot.NewStatsRegistry(), tools: tool.NewRegistry()}
	m.registerTools()

	def, _ := m.tools.Get("bot_status")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "runtime: ") || !strings.Contains(out, "heap=") {
		t.Errorf("runtime output = %q", out)
	}
}

func TestBotStatusHandlerFiltersByModule(t *testing.T) {
	m := newToolTestModule()
	m.registerTools()

	def, _ := m.tools.Get("bot_status")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"module":"coffee"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "coffee") || strings.Contains(out, "gippity") {
		t.Errorf("filter failed: %q", out)
	}

	unknown, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"module":"nope"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(unknown, "no module named") {
		t.Errorf("unknown module output = %q", unknown)
	}
}

func TestBotStatusHandlerWithoutStatsRegistry(t *testing.T) {
	m := &Module{tools: tool.NewRegistry()}
	m.registerTools()

	def, _ := m.tools.Get("bot_status")
	if _, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error when stats registry is nil")
	}
}
