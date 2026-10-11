package coffee

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/util"
)

// coffeeStatusParameters is the JSON schema for the coffee_status tool.
var coffeeStatusParameters = json.RawMessage(`{
  "type": "object",
  "properties": {}
}`)

// coffeeStatsParameters is the JSON schema for the coffee_stats tool.
var coffeeStatsParameters = json.RawMessage(`{
  "type": "object",
  "properties": {
    "user": {
      "type": "string",
      "description": "Discord user ID, username, nickname or mention whose coffee stats to report. Partial names are matched against this server's members. Omit for the caller."
    }
  }
}`)

// registerTools registers the tools owned by the coffee module. It is called
// from Init after the beverage store is open.
func (m *Module) registerTools() {
	if m.tools == nil {
		return
	}
	m.tools.Register(tool.Def{
		Name:        "coffee_status",
		Description: "Report the current Discord server's coffee machine levels (beans, water, milk, grounds, tea bags) and its top baristas/refillers. Read-only.",
		Parameters:  coffeeStatusParameters,
		Handler:     m.coffeeStatusHandler,
	})
	m.tools.Register(tool.Def{
		Name:        "coffee_stats",
		Description: "Report a user's coffee stats in the current Discord server: drinks by type, refills by part, grounds emptied. Read-only. Defaults to the caller.",
		Parameters:  coffeeStatsParameters,
		Handler:     m.coffeeStatsHandler,
	})
}

// coffeeStatusHandler renders the guild machine status. The guild is clamped to
// Env so the tool cannot read another server's machine.
func (m *Module) coffeeStatusHandler(_ context.Context, env tool.Env, _ json.RawMessage) (string, error) {
	if env.GuildID == "" {
		return "no guild available", nil
	}
	inv, err := m.getOrSeedInventory(env.GuildID)
	if err != nil {
		return "", fmt.Errorf("coffee_status: %w", err)
	}
	drinkers, _ := m.topDrinkers(env.GuildID, 3)
	refillers, _ := m.topRefillers(env.GuildID, 3)
	emptiers, _ := m.topGroundsEmptiers(env.GuildID, 3)
	slackers, _ := m.topSlackers(env.GuildID, 3)
	teaBags, _ := m.getTeaBagInventory(env.GuildID)
	return formatStatus(inv, drinkers, refillers, emptiers, slackers, teaBags), nil
}

// coffeeStatsHandler renders one user's stats, defaulting to the caller. The
// guild is clamped to Env; the user defaults to Env.UserID. A free-text name is
// resolved to a member of the current guild.
func (m *Module) coffeeStatsHandler(_ context.Context, env tool.Env, args json.RawMessage) (string, error) {
	if env.GuildID == "" {
		return "no guild available", nil
	}
	var in struct {
		User string `json:"user"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &in)
	}
	target := strings.TrimSpace(in.User)
	switch {
	case target == "":
		target = env.UserID
	case !util.IsUserID(target):
		match, ok, note := m.resolveUser(env.GuildID, target)
		if !ok {
			return note, nil
		}
		target = match.ID
	}
	if target == "" {
		return "no user available", nil
	}
	return m.buildUserStats(env.GuildID, target), nil
}

// resolveUser resolves a free-text user name to a guild member, defaulting to
// util.ResolveMemberQuery. It scopes the lookup to guildID so a name can never
// resolve to a member of another server.
func (m *Module) resolveUser(guildID, query string) (util.MemberMatch, bool, string) {
	if m.resolveMember == nil {
		return util.ResolveMemberQuery(m.session, guildID, query)
	}
	return m.resolveMember(m.session, guildID, query)
}
