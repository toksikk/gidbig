package leetoclock

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
	"github.com/toksikk/gidbig/internal/tool"
)

// leetoclockRecordsParameters is the JSON schema for the leetoclock_records tool.
var leetoclockRecordsParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "subcommand": {
      "type": "string",
      "enum": ["top", "player"],
      "description": "top lists the best players; player lists one player's best attempts."
    },
    "period": {
      "type": "string",
      "enum": ["month", "7d", "30d", "all"],
      "description": "Game date range. Defaults to the current month. Ignored when date is given."
    },
    "date": {
      "type": "string",
      "description": "One specific game date, YYYY-MM-DD. Use it to answer questions about a named day; it overrides period."
    },
    "scope": {
      "type": "string",
      "enum": ["server", "global"],
      "description": "server limits to the current server (default); global spans all servers."
    },
    "user": {
      "type": "string",
      "description": "Discord user ID for subcommand \"player\". Omit for the caller."
    }
  },
  "required": ["subcommand"]
}`)

// registerTools registers the tools owned by the leetoclock module. It is
// called from Init after the score store is open.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "leetoclock_records",
		Description: "Look up Leet o'Clock reaction-time records: a top-players board or one player's best attempts, for the current server or globally. Read-only.",
		Parameters:  leetoclockRecordsParameters,
		Handler:     m.recordsHandler,
	})
}

// recordsHandler resolves the arguments into the same recordRequest used by the
// slash command and reuses recordResponse, so tool output matches the command.
// The guild is clamped to Env; the player defaults to Env.UserID.
func (m *Module) recordsHandler(_ context.Context, env tool.Env, args json.RawMessage) (string, error) {
	var in struct {
		Subcommand string `json:"subcommand"`
		Period     string `json:"period"`
		Date       string `json:"date"`
		Scope      string `json:"scope"`
		User       string `json:"user"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("leetoclock_records: invalid arguments: %w", err)
	}
	if env.GuildID == "" {
		return "no guild available", nil
	}

	r := recordRequest{period: datastore.PeriodMonth, periodName: "month", scope: "server"}
	switch strings.ToLower(strings.TrimSpace(in.Subcommand)) {
	case "top":
		r.subcommand = "top"
	case "player":
		r.subcommand = "player"
	default:
		return `choose subcommand "top" or "player"`, nil
	}

	switch strings.ToLower(strings.TrimSpace(in.Period)) {
	case "", "month":
		r.period, r.periodName = datastore.PeriodMonth, "month"
	case "7d":
		r.period, r.periodName = datastore.PeriodLast7Days, "7d"
	case "30d":
		r.period, r.periodName = datastore.PeriodLast30Days, "30d"
	case "all":
		r.period, r.periodName = datastore.PeriodAllTime, "all"
	default:
		return "invalid period", nil
	}

	day, err := parseRecordDay(in.Date)
	if err != nil {
		return "invalid date: use YYYY-MM-DD", nil
	}

	switch strings.ToLower(strings.TrimSpace(in.Scope)) {
	case "", "server":
		r.scope = "server"
	case "global":
		r.scope = "global"
	default:
		return "invalid scope", nil
	}

	if r.subcommand == "player" {
		r.userID = strings.TrimSpace(in.User)
		if r.userID == "" {
			r.userID = env.UserID
		}
		if r.userID == "" {
			return "no player given", nil
		}
	}

	if !day.IsZero() {
		r.day = day
		r.periodName = day.Format("2006-01-02")
	}

	return m.recordResponse(env.GuildID, r)
}
