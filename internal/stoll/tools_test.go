package stoll

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/tool"
)

func TestRegisterToolsRegistersStollQuote(t *testing.T) {
	m := New()
	m.tools = tool.NewRegistry()
	m.registerTools()

	if _, ok := m.tools.Get("stoll_quote"); !ok {
		t.Fatal("stoll_quote was not registered")
	}
}

func TestQuoteHandlerReturnsQuote(t *testing.T) {
	m := New()
	m.tools = tool.NewRegistry()
	m.registerTools()

	def, _ := m.tools.Get("stoll_quote")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "Dr. Axel Stoll") {
		t.Errorf("quote output = %q", out)
	}
}

func TestRegisterToolsNilRegistryIsSafe(t *testing.T) {
	m := New()
	m.registerTools()
}
