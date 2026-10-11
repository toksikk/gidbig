package leetoclock

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/util"
)

// leetoclockRecordsParameters is the JSON schema for the leetoclock_records tool.
var leetoclockRecordsParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "subcommand": {
      "type": "string",
      "enum": ["top", "daily", "player"],
      "description": "top lists the best players over the range; daily lists one winner per calendar day; player lists one player's best attempts. Use daily for questions like 'who won each day'."
    },
    "period": {
      "type": "string",
      "enum": ["month", "today", "yesterday", "week", "7d", "30d", "year", "all"],
      "description": "Game date range. Defaults to the current month. Ignored when date or from/to is given."
    },
    "date": {
      "type": "string",
      "description": "One specific game date, YYYY-MM-DD. Use it to answer questions about a named day; it overrides period and from/to."
    },
    "from": {
      "type": "string",
      "description": "First game date of an inclusive range, YYYY-MM-DD. Use with to, or alone for everything up to today; overrides period."
    },
    "to": {
      "type": "string",
      "description": "Last game date of an inclusive range, YYYY-MM-DD. Use with from, or alone for everything up to that date; overrides period."
    },
    "scope": {
      "type": "string",
      "enum": ["server", "global"],
      "description": "server limits to the current server (default); global spans all servers."
    },
    "user": {
      "type": "string",
      "description": "Player to look up for subcommand \"player\": a Discord user ID, username, nickname or mention. Partial names are matched against this server's members. Omit for the caller."
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
		Description: "Look up Leet o'Clock reaction-time records for the current server or globally. Subcommands: top (best players over a period), daily (one winner per calendar day), player (one player's best attempts). Accepts a period preset, one date, or a from/to day range. Use daily to answer 'who won each day'. Read-only.",
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
		From       string `json:"from"`
		To         string `json:"to"`
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
	case "daily":
		r.subcommand = "daily"
	case "player":
		r.subcommand = "player"
	default:
		return `choose subcommand "top", "daily" or "player"`, nil
	}

	period, err := parsePeriod(in.Period)
	if err != nil {
		return "invalid period", nil
	}
	r.period, r.periodName = period, periodNames[period]

	day, err := parseRecordDay(in.Date)
	if err != nil {
		return "invalid date: use YYYY-MM-DD", nil
	}
	from, err := parseRecordDay(in.From)
	if err != nil {
		return "invalid from: use YYYY-MM-DD", nil
	}
	to, err := parseRecordDay(in.To)
	if err != nil {
		return "invalid to: use YYYY-MM-DD", nil
	}
	if !day.IsZero() && (!from.IsZero() || !to.IsZero()) {
		return "use either date or from/to, not both", nil
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return "from must not be after to", nil
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
		if raw := strings.TrimSpace(in.User); raw != "" && !util.IsUserID(raw) {
			match, ok, note := m.resolveUser(env.GuildID, raw)
			if !ok {
				return note, nil
			}
			r.userID = match.ID
		} else if raw != "" {
			r.userID = raw
		} else {
			r.userID = env.UserID
		}
		if r.userID == "" {
			return "no player given", nil
		}
	}

	if !day.IsZero() {
		r.day = day
		r.periodName = day.Format("2006-01-02")
	} else if !from.IsZero() || !to.IsZero() {
		r.from, r.to = from, to
		r.periodName = recordRangeName(from, to)
	}

	return m.recordResponse(env.GuildID, r)
}

// resolveUser resolves a free-text player name to a guild member, defaulting to
// util.ResolveMemberQuery. It scopes the lookup to guildID so a name can never
// resolve to a member of another server.
func (m *Module) resolveUser(guildID, query string) (util.MemberMatch, bool, string) {
	if m.resolveMember == nil {
		return util.ResolveMemberQuery(m.session, guildID, query)
	}
	return m.resolveMember(m.session, guildID, query)
}
