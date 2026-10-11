package gidbig

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/toksikk/gidbig/internal/tool"
)

// botStatusParameters is the JSON schema for the bot_status tool.
var botStatusParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "module": {
      "type": "string",
      "description": "Optional module name to report. Omit to report every module."
    }
  }
}`)

// registerTools registers the tools owned by the core module. It is called from
// Init, after the stats registry is wired.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "bot_status",
		Description: "Report the bot's own runtime counters per module, for example stored messages or mentions. Read-only. Optionally filter by module name.",
		Parameters:  botStatusParameters,
		Handler:     m.botStatusHandler,
	})
}

// botStatusHandler aggregates every StatsProvider into compact one-line
// summaries. It returns only counters already exposed to /status, never tokens
// or secrets. The optional module argument filters by module name.
func (m *Module) botStatusHandler(ctx context.Context, _ tool.Env, args json.RawMessage) (string, error) {
	if m.stats == nil {
		return "", fmt.Errorf("bot_status: stats registry not initialized")
	}

	var in struct {
		Module string `json:"module"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &in)
	}

	stats := collectModuleStats(ctx, m.stats.All(), statusProviderTimeout)
	filter := strings.ToLower(strings.TrimSpace(in.Module))

	var b strings.Builder
	for _, ms := range stats {
		if filter != "" && strings.ToLower(ms.Name) != filter {
			continue
		}
		if ms.Err != nil {
			fmt.Fprintf(&b, "%s: n/a (%v)\n", ms.Name, ms.Err)
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", ms.Name, joinStats(ms.Stats.Summary))
	}
	if b.Len() == 0 {
		return fmt.Sprintf("no module named %q", in.Module), nil
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
