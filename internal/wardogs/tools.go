package wardogs

import (
	"context"
	"encoding/json"

	"github.com/toksikk/gidbig/internal/tool"
)

// wardogsStatusParameters is the JSON schema for the wardogs_status tool.
var wardogsStatusParameters = json.RawMessage(`{
  "type": "object",
  "properties": {}
}`)

// registerTools registers the tools owned by the wardogs module.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "wardogs_status",
		Description: "Report whether WARDOGS officially supports Linux/Proton according to the doeswardogshavelinux.support tracker. Read-only, served from the module's cached status.",
		Parameters:  wardogsStatusParameters,
		Handler:     m.statusToolHandler,
	})
}

// statusToolHandler returns the cached tracker verdict, reusing the same cache
// path and formatter as /wardogs. It never fetches a model-supplied URL.
func (m *Module) statusToolHandler(ctx context.Context, _ tool.Env, _ json.RawMessage) (string, error) {
	status, err := m.statusCached(ctx)
	if err != nil {
		return "", err
	}
	return formatStatus(status), nil
}
