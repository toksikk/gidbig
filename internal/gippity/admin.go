package gippity

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// AdminSubcommandGroup returns the /admin gippity subcommand group definition.
func (m *Module) AdminSubcommandGroup() *discordgo.ApplicationCommandOption {
	userOpt := func(desc string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: desc,
			Required:    false,
		}
	}
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
		Name:        "gippity",
		Description: "Gippity admin queries",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "privacy",
				Description: "Show gippity privacy setting for a user or all users",
				Options:     []*discordgo.ApplicationCommandOption{userOpt("Target user (omit for all)")},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "history",
				Description: "Show whether a user has stored conversation history",
				Options:     []*discordgo.ApplicationCommandOption{userOpt("Target user (omit for all)")},
			},
		},
	}
}

// HandleAdminSubcommand handles /admin gippity subcommands. The interaction is
// already deferred ephemerally by the admin module.
func (m *Module) HandleAdminSubcommand(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	switch sub.Name {
	case "privacy":
		targetID := adminOptUserID(s, sub.Options)
		if targetID != "" {
			privacy := m.adminGetUserPrivacy(targetID)
			adminEditEphemeral(s, i, fmt.Sprintf("<@%s> privacy: %v (true = messages anonymized in AI context)", targetID, privacy))
			return
		}
		settings, err := m.adminGetAllUserPrivacy()
		if err != nil {
			adminEditEphemeral(s, i, fmt.Sprintf("Error querying privacy settings: %v", err))
			return
		}
		if len(settings) == 0 {
			adminEditEphemeral(s, i, "No explicit privacy settings stored (all users default to: on).")
			return
		}
		var sb strings.Builder
		for uid, enabled := range settings {
			fmt.Fprintf(&sb, "<@%s>: %v\n", uid, enabled)
		}
		adminEditEphemeral(s, i, sb.String())
	case "history":
		targetID := adminOptUserID(s, sub.Options)
		if targetID != "" {
			has := m.adminHasConversationHistory(targetID)
			adminEditEphemeral(s, i, fmt.Sprintf("<@%s> has history: %v", targetID, has))
			return
		}
		users, err := m.adminGetUsersWithHistory()
		if err != nil {
			adminEditEphemeral(s, i, fmt.Sprintf("Error querying history: %v", err))
			return
		}
		if len(users) == 0 {
			adminEditEphemeral(s, i, "No conversation history stored.")
			return
		}
		var sb strings.Builder
		for _, uid := range users {
			fmt.Fprintf(&sb, "<@%s>\n", uid)
		}
		adminEditEphemeral(s, i, "Users with stored history:\n"+sb.String())
	}
}

func adminOptUserID(s *discordgo.Session, opts []*discordgo.ApplicationCommandInteractionDataOption) string {
	for _, o := range opts {
		if o.Name == "user" {
			u := o.UserValue(s)
			if u == nil {
				return ""
			}
			return u.ID
		}
	}
	return ""
}

func adminEditEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		slog.Error("gippity: admin edit response failed", "error", err)
	}
}

// adminGetUserPrivacy returns true (privacy on/anonymized) for a user.
// Defaults to true if no explicit setting exists.
func (m *Module) adminGetUserPrivacy(userID string) bool {
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	var enabled int
	err := m.db.QueryRow(`SELECT privacy_enabled FROM user_privacy WHERE user_id = ?`, userID).Scan(&enabled)
	if err == sql.ErrNoRows {
		return true
	}
	if err != nil {
		slog.Error("gippity: admin error querying user_privacy", "error", err)
		return true
	}
	return enabled != 0
}

// adminGetAllUserPrivacy returns a map of userID -> privacy_enabled for all users
// with an explicit setting in the database.
func (m *Module) adminGetAllUserPrivacy() (map[string]bool, error) {
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	rows, err := m.db.Query(`SELECT user_id, privacy_enabled FROM user_privacy ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]bool)
	for rows.Next() {
		var uid string
		var enabled int
		if err := rows.Scan(&uid, &enabled); err != nil {
			return nil, err
		}
		result[uid] = enabled != 0
	}
	return result, nil
}

// adminHasConversationHistory returns true if the user has any stored chat messages.
func (m *Module) adminHasConversationHistory(userID string) bool {
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	var count int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM chat_history WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// adminGetUsersWithHistory returns all distinct user IDs that have stored chat history.
func (m *Module) adminGetUsersWithHistory() ([]string, error) {
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	rows, err := m.db.Query(`SELECT DISTINCT user_id FROM chat_history ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var users []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		users = append(users, uid)
	}
	return users, nil
}
