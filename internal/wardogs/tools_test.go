package wardogs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/tool"
)

func TestRegisterToolsRegistersWardogsStatus(t *testing.T) {
	m := newTestModule()
	m.tools = tool.NewRegistry()
	m.registerTools()

	if _, ok := m.tools.Get("wardogs_status"); !ok {
		t.Fatal("wardogs_status was not registered")
	}
}

func TestStatusToolHandlerReportsVerdict(t *testing.T) {
	m := newTestModule()
	m.tools = tool.NewRegistry()
	m.registerTools()

	def, _ := m.tools.Get("wardogs_status")
	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "WARDOGS") {
		t.Errorf("status output = %q", out)
	}
}

func TestRegisterToolsNilRegistryIsSafe(t *testing.T) {
	m := New()
	m.registerTools()
}
