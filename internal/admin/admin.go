package admin

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/gippity"
)

var (
	ownerID   string
	infoFn    func(s *discordgo.Session) string
	providers []bot.AdminProvider
)

// RegisterProvider registers a module as an admin subcommand provider.
// Must be called before Start.
func RegisterProvider(p bot.AdminProvider) {
	providers = append(providers, p)
}

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

	opts := make([]*discordgo.ApplicationCommandOption, 0, len(providers)+2)
	for _, p := range providers {
		opts = append(opts, p.AdminSubcommandGroup())
	}
	opts = append(opts,
		&discordgo.ApplicationCommandOption{
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
		&discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "info",
			Description: "General bot info: uptime, guild count, loaded plugins",
		},
	)

	return []*discordgo.ApplicationCommand{
		{
			Name:        "admin",
			Description: "Admin commands (owner only)",
			Options:     opts,
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
	case "gippity":
		if len(top.Options) > 0 {
			handleGippityAdmin(s, i, top.Options[0])
		}
	default:
		for _, p := range providers {
			if p.AdminSubcommandGroup().Name == top.Name {
				if len(top.Options) > 0 {
					p.HandleAdminSubcommand(s, i, top.Options[0])
				}
				return
			}
		}
	}
}

func optUserID(s *discordgo.Session, opts []*discordgo.ApplicationCommandInteractionDataOption) string {
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
