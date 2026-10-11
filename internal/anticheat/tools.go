package anticheat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/toksikk/gidbig/internal/tool"
)

// anticheatLookupParameters is the JSON schema for the anticheat_lookup tool.
var anticheatLookupParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Game title or anti-cheat vendor to search for."
    }
  },
  "required": ["query"]
}`)

// registerTools registers the tools owned by the anticheat module.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "anticheat_lookup",
		Description: "Look up a game title or anti-cheat vendor in the GamingOnLinux Linux/Steam Deck anti-cheat compatibility list. Read-only, served from the module's cached dataset.",
		Parameters:  anticheatLookupParameters,
		Handler:     m.lookupToolHandler,
	})
}

// lookupToolHandler searches the cached compatibility list. It reuses the same
// cache path and formatter as the /anticheat command, so the upstream CSV is
// fetched at most once per TTL and never from a model-supplied URL.
func (m *Module) lookupToolHandler(ctx context.Context, _ tool.Env, args json.RawMessage) (string, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("anticheat_lookup: invalid arguments: %w", err)
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return "no query given", nil
	}

	entries, err := m.getDataset(ctx)
	if err != nil {
		return "", fmt.Errorf("anticheat_lookup: %w", err)
	}
	matches := searchEntries(entries, query)
	if len(matches) == 0 {
		return fmt.Sprintf("no anti-cheat entry matching %q", query), nil
	}
	return formatResults(matches, query), nil
}
