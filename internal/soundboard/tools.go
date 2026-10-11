package soundboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/toksikk/gidbig/internal/tool"
)

// listSoundsParameters is the JSON schema for the list_sounds tool.
var listSoundsParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "collection": {
      "type": "string",
      "description": "Optional collection prefix to list. Omit to list every collection."
    }
  }
}`)

// registerTools registers the tools owned by the soundboard module. It is
// called from Init after the audio collections are loaded.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "list_sounds",
		Description: "List the soundboard's sound collections and the sound names in each, so the model knows what can be played. Read-only. Optionally filter to one collection.",
		Parameters:  listSoundsParameters,
		Handler:     m.listSoundsHandler,
	})
}

// listSoundsHandler renders the loaded collections as one line per prefix. The
// data is the module's in-memory catalog, never a lookup with caller scope.
func (m *Module) listSoundsHandler(_ context.Context, _ tool.Env, args json.RawMessage) (string, error) {
	var in struct {
		Collection string `json:"collection"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &in)
	}
	filter := strings.ToLower(strings.TrimSpace(in.Collection))

	var b strings.Builder
	found := false
	for _, c := range m.collections {
		if filter != "" && strings.ToLower(c.Prefix) != filter {
			continue
		}
		found = true
		names := make([]string, 0, len(c.Sounds))
		for _, s := range c.Sounds {
			names = append(names, s.Name)
		}
		fmt.Fprintf(&b, "%s (%d): %s\n", c.Prefix, len(names), strings.Join(names, ", "))
	}
	if !found {
		if filter != "" {
			return fmt.Sprintf("no collection named %q", in.Collection), nil
		}
		return "no sound collections loaded", nil
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
