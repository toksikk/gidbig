package anticheat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/tool"
)

func registerAnticheatTools(t *testing.T) *Module {
	t.Helper()
	m := newTestModule(staticFetch(sampleCSV))
	m.tools = tool.NewRegistry()
	m.registerTools()
	return m
}

func TestRegisterToolsRegistersAnticheatLookup(t *testing.T) {
	m := registerAnticheatTools(t)

	if _, ok := m.tools.Get("anticheat_lookup"); !ok {
		t.Fatal("anticheat_lookup was not registered")
	}
}

func TestLookupToolHandlerFindsGame(t *testing.T) {
	m := registerAnticheatTools(t)

	def, _ := m.tools.Get("anticheat_lookup")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"query":"Halo"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "Halo Infinite") {
		t.Errorf("lookup output = %q", out)
	}
}

func TestLookupToolHandlerNoMatch(t *testing.T) {
	m := registerAnticheatTools(t)

	def, _ := m.tools.Get("anticheat_lookup")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"query":"Nonexistent Game"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "no anti-cheat entry") {
		t.Errorf("no-match output = %q", out)
	}
}

func TestLookupToolHandlerEmptyQuery(t *testing.T) {
	m := registerAnticheatTools(t)

	def, _ := m.tools.Get("anticheat_lookup")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"query":"  "}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "no query given") {
		t.Errorf("empty query output = %q", out)
	}
}

func TestRegisterToolsNilRegistryIsSafe(t *testing.T) {
	m := New()
	m.registerTools()
}
