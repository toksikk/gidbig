package gamerstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/toksikk/gidbig/internal/tool"
)

// currentGameStatusParameters is the JSON schema for the current_game_status tool.
var currentGameStatusParameters = json.RawMessage(`{
  "type": "object",
  "properties": {}
}`)

// registerTools registers the tools owned by the gamerstatus module.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "current_game_status",
		Description: "Return the game status the bot currently shows on Discord (its 'now playing') and how long ago it was set. Read-only.",
		Parameters:  currentGameStatusParameters,
		Handler:     m.statusHandler,
	})
}

// statusHandler reports the last game status set by the rotation loop.
func (m *Module) statusHandler(_ context.Context, _ tool.Env, _ json.RawMessage) (string, error) {
	game, since := m.currentStatus()
	if game == "" {
		return "No game status is currently set.", nil
	}
	return fmt.Sprintf("Currently shown status: Playing %q (set %s).", game, formatAgo(m.now().Sub(since))), nil
}

// formatAgo renders a duration rounded down to whole minutes. Negative
// durations (wall clock jumped back) are intentionally clamped to "less than a
// minute ago".
func formatAgo(d time.Duration) string {
	minutes := int(d / time.Minute)
	switch {
	case minutes < 1:
		return "less than a minute ago"
	case minutes == 1:
		return "1 minute ago"
	default:
		return fmt.Sprintf("%d minutes ago", minutes)
	}
}
