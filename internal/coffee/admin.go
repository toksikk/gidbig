package coffee

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// AdminSubcommandGroup returns the /admin coffee subcommand group definition.
func (m *Module) AdminSubcommandGroup() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
		Name:        "coffee",
		Description: "Coffee admin queries",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "beverages",
				Description: "Show beverage settings for a user or all users",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionUser,
						Name:        "user",
						Description: "Target user (omit for all)",
						Required:    false,
					},
				},
			},
		},
	}
}

// HandleAdminSubcommand handles /admin coffee subcommands.
func (m *Module) HandleAdminSubcommand(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	if sub.Name != "beverages" {
		return
	}
	targetID := adminOptUserID(s, sub.Options)
	if targetID != "" {
		pref, found := m.adminGetBeveragePreference(targetID)
		if !found {
			adminEphemeral(s, i, fmt.Sprintf("No beverage preference found for <@%s>.", targetID))
			return
		}
		adminEphemeral(s, i, fmt.Sprintf("<@%s>: %s (introduced: %v)", targetID, pref.BeverageEmoji, pref.HasSeenIntro))
		return
	}
	prefs, err := m.adminGetAllBeveragePreferences()
	if err != nil {
		adminEphemeral(s, i, fmt.Sprintf("Error querying beverage preferences: %v", err))
		return
	}
	if len(prefs) == 0 {
		adminEphemeral(s, i, "No beverage preferences stored.")
		return
	}
	var sb strings.Builder
	for _, p := range prefs {
		fmt.Fprintf(&sb, "<@%s>: %s (introduced: %v)\n", p.UserID, p.BeverageEmoji, p.HasSeenIntro)
	}
	adminEphemeral(s, i, sb.String())
}

func (m *Module) adminGetBeveragePreference(userID string) (*UserBeveragePreference, bool) {
	d := m.getDB()
	if d == nil {
		return nil, false
	}
	var pref UserBeveragePreference
	if err := d.Where("user_id = ?", userID).First(&pref).Error; err != nil {
		return nil, false
	}
	return &pref, true
}

func (m *Module) adminGetAllBeveragePreferences() ([]UserBeveragePreference, error) {
	d := m.getDB()
	if d == nil {
		return nil, errors.New("store not initialized")
	}
	var prefs []UserBeveragePreference
	result := d.Find(&prefs)
	return prefs, result.Error
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

func adminEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		slog.Error("coffee: admin respond failed", "error", err)
	}
}
