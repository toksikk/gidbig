package admin

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/coffee"
	"github.com/toksikk/gidbig/internal/gippity"
)

var (
	ownerID string
	infoFn  func(s *discordgo.Session) string
)

// Start registers the /admin interaction handler.
func Start(s *discordgo.Session, oid string, info func(s *discordgo.Session) string) {
	ownerID = oid
	infoFn = info
	s.AddHandler(onAdminInteractionCreate)
	slog.Info("admin commands registered")
}

// Commands returns the /admin slash command definition.
func Commands() []*discordgo.ApplicationCommand {
	userOpt := func(desc string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: desc,
			Required:    false,
		}
	}
	return []*discordgo.ApplicationCommand{
		{
			Name:        "admin",
			Description: "Admin commands (owner only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
					Name:        "coffee",
					Description: "Coffee admin queries",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Name:        "beverages",
							Description: "Show beverage settings for a user or all users",
							Options:     []*discordgo.ApplicationCommandOption{userOpt("Target user (omit for all)")},
						},
					},
				},
				{
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
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "info",
					Description: "General bot info: uptime, guild count, loaded plugins",
				},
			},
		},
	}
}

func callerID(i *discordgo.InteractionCreate) string {
	if i.Member != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func ephemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		slog.Error("admin: failed to respond to interaction", "error", err)
	}
}

func onAdminInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	if data.Name != "admin" {
		return
	}
	if callerID(i) != ownerID {
		ephemeral(s, i, "Access denied.")
		return
	}
	if len(data.Options) == 0 {
		return
	}
	top := data.Options[0]
	switch top.Name {
	case "info":
		ephemeral(s, i, "```"+infoFn(s)+"```")
	case "coffee":
		if len(top.Options) > 0 {
			handleCoffeeAdmin(s, i, top.Options[0])
		}
	case "gippity":
		if len(top.Options) > 0 {
			handleGippityAdmin(s, i, top.Options[0])
		}
	}
}

func optUserID(s *discordgo.Session, opts []*discordgo.ApplicationCommandInteractionDataOption) string {
	for _, o := range opts {
		if o.Name == "user" {
			return o.UserValue(s).ID
		}
	}
	return ""
}

func handleCoffeeAdmin(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	if sub.Name != "beverages" {
		return
	}
	targetID := optUserID(s, sub.Options)
	if targetID != "" {
		pref, found := coffee.AdminGetBeveragePreference(targetID)
		if !found {
			ephemeral(s, i, fmt.Sprintf("No beverage preference found for <@%s>.", targetID))
			return
		}
		ephemeral(s, i, fmt.Sprintf("<@%s>: %s (introduced: %v)", targetID, pref.BeverageEmoji, pref.HasSeenIntro))
		return
	}
	prefs, err := coffee.AdminGetAllBeveragePreferences()
	if err != nil {
		ephemeral(s, i, fmt.Sprintf("Error querying beverage preferences: %v", err))
		return
	}
	if len(prefs) == 0 {
		ephemeral(s, i, "No beverage preferences stored.")
		return
	}
	var sb strings.Builder
	for _, p := range prefs {
		fmt.Fprintf(&sb, "<@%s>: %s (introduced: %v)\n", p.UserID, p.BeverageEmoji, p.HasSeenIntro)
	}
	ephemeral(s, i, sb.String())
}

func handleGippityAdmin(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	switch sub.Name {
	case "privacy":
		targetID := optUserID(s, sub.Options)
		if targetID != "" {
			privacy := gippity.AdminGetUserPrivacy(targetID)
			ephemeral(s, i, fmt.Sprintf("<@%s> privacy: %v (true = messages anonymized in AI context)", targetID, privacy))
			return
		}
		settings, err := gippity.AdminGetAllUserPrivacy()
		if err != nil {
			ephemeral(s, i, fmt.Sprintf("Error querying privacy settings: %v", err))
			return
		}
		if len(settings) == 0 {
			ephemeral(s, i, "No explicit privacy settings stored (all users default to: on).")
			return
		}
		var sb strings.Builder
		for uid, enabled := range settings {
			fmt.Fprintf(&sb, "<@%s>: %v\n", uid, enabled)
		}
		ephemeral(s, i, sb.String())
	case "history":
		targetID := optUserID(s, sub.Options)
		if targetID != "" {
			has := gippity.AdminHasConversationHistory(targetID)
			ephemeral(s, i, fmt.Sprintf("<@%s> has history: %v", targetID, has))
			return
		}
		users, err := gippity.AdminGetUsersWithHistory()
		if err != nil {
			ephemeral(s, i, fmt.Sprintf("Error querying history: %v", err))
			return
		}
		if len(users) == 0 {
			ephemeral(s, i, "No conversation history stored.")
			return
		}
		var sb strings.Builder
		for _, uid := range users {
			fmt.Fprintf(&sb, "<@%s>\n", uid)
		}
		ephemeral(s, i, "Users with stored history:\n"+sb.String())
	}
}
