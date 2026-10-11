package soundboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/tool"
)

func newToolTestModule() *Module {
	return &Module{
		tools: tool.NewRegistry(),
		collections: []*soundCollection{
			{Prefix: "rick", Sounds: []*soundClip{{Name: "roll"}, {Name: "astley"}}},
			{Prefix: "bruh", Sounds: []*soundClip{{Name: "moment"}}},
		},
	}
}

func TestRegisterToolsRegistersListSounds(t *testing.T) {
	m := newToolTestModule()
	m.registerTools()

	if _, ok := m.tools.Get("list_sounds"); !ok {
		t.Fatal("list_sounds was not registered")
	}
}

func TestListSoundsHandlerListsEveryCollection(t *testing.T) {
	m := newToolTestModule()
	m.registerTools()

	def, _ := m.tools.Get("list_sounds")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	for _, want := range []string{"rick", "roll", "astley", "bruh", "moment"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestListSoundsHandlerFiltersByCollection(t *testing.T) {
	m := newToolTestModule()
	m.registerTools()

	def, _ := m.tools.Get("list_sounds")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"collection":"BRUH"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "bruh") || strings.Contains(out, "rick") {
		t.Errorf("filter failed: %s", out)
	}
}

func TestListSoundsHandlerUnknownCollection(t *testing.T) {
	m := newToolTestModule()
	m.registerTools()

	def, _ := m.tools.Get("list_sounds")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"collection":"nope"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "no collection named") {
		t.Errorf("unknown collection output = %q", out)
	}
}

func TestRegisterToolsNilRegistryIsSafe(t *testing.T) {
	m := &Module{}
	m.registerTools()
}
