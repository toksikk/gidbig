package openholidays

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/tool"
)

func callTool(t *testing.T, m *Module, args string) (string, error) {
	t.Helper()
	return m.holidaysToolHandler(context.Background(), tool.Env{}, json.RawMessage(args))
}

func TestRegisterTools(t *testing.T) {
	m, _ := newTestModule(t)
	m.tools = tool.NewRegistry()
	m.registerTools()
	d, ok := m.tools.Get("holidays")
	if !ok {
		t.Fatal("holidays not registered")
	}
	var schema map[string]any
	if err := json.Unmarshal(d.Parameters, &schema); err != nil || schema["type"] != "object" {
		t.Fatalf("schema invalid: %v", err)
	}
	New().registerTools() // nil registry is safe
}

func TestToolDefaultWindow(t *testing.T) {
	m, f := newTestModule(t)
	out, err := callTool(t, m, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "public holidays in DE from 2026-10-25 to 2026-11-24") {
		t.Fatalf("out:\n%s", out)
	}
	if !strings.Contains(f.lastQuery.Load().(string), "validTo=2026-11-24") {
		t.Errorf("query = %v", f.lastQuery.Load())
	}
}

func TestToolSubdivision(t *testing.T) {
	m, _ := newTestModule(t)
	out, err := callTool(t, m, `{"subdivision":"DE-BY","from":"2026-11-01","to":"2026-12-31"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Allerheiligen; regions: BY, NW") || strings.Contains(out, "Buß") {
		t.Fatalf("out:\n%s", out)
	}
}

func TestToolBadArgs(t *testing.T) {
	m, f := newTestModule(t)
	for _, args := range []string{`{"from":"x"}`, `{"type":"bank"}`, `{"country":"DEU"}`, `not json`} {
		if _, err := callTool(t, m, args); err == nil {
			t.Errorf("%s: no error", args)
		}
	}
	if f.holidayCalls.Load() != 0 {
		t.Error("API called for bad args")
	}
}

func TestToolAPIError(t *testing.T) {
	m, f := newTestModule(t)
	f.status = 500
	if out, err := callTool(t, m, `{}`); err == nil || out != "" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
