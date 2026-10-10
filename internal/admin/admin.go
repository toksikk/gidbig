package admin

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
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
	opts := make([]*discordgo.ApplicationCommandOption, 0, len(providers)+1)
	for _, p := range providers {
		opts = append(opts, p.AdminSubcommandGroup())
	}
	opts = append(opts,
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

func deferEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})
}

func editEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		slog.Error("admin: failed to edit interaction response", "error", err)
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
	if err := deferEphemeral(s, i); err != nil {
		slog.Error("admin: failed to defer interaction", "error", err)
		return
	}
	top := data.Options[0]
	switch top.Name {
	case "info":
		editEphemeral(s, i, "```"+infoFn(s)+"```")
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
