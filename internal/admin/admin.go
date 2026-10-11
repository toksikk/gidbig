package admin

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

// Module implements bot.Module for the owner-only /admin command. It discovers
// the bot.AdminProvider modules handed to New at construction and exposes each
// provider's subcommand group under /admin, so adding a provider needs no edit
// here. All former package-level state now lives on this struct.
type Module struct {
	ownerID   string
	providers []bot.AdminProvider
	info      func(*discordgo.Session) string

	// dispatch is the owner-gated command handler, built in Init once ownerID is
	// known. Listeners filters interactions down to /admin before calling it.
	dispatch bot.HandlerFunc
}

// New returns an admin Module that exposes each provider's subcommand group
// under /admin.
func New(providers ...bot.AdminProvider) *Module {
	return &Module{providers: providers}
}

// SetInfoText injects the renderer for the built-in /admin info subcommand. The
// composition root owns it because it aggregates the shared status snapshot.
func (m *Module) SetInfoText(fn func(*discordgo.Session) string) { m.info = fn }

// Name returns the module's identifier.
func (m *Module) Name() string { return "admin" }

// Init wires the owner gate from Deps. The owner ID comes from Deps.OwnerID, or
// from the config when running without a composition root.
func (m *Module) Init(d bot.Deps) error {
	switch {
	case d.OwnerID != "":
		m.ownerID = d.OwnerID
	case d.Config != nil:
		m.ownerID = d.Config.Discord.OwnerID
	}
	m.dispatch = bot.OwnerOnly(m.ownerID)(m.handleAdminCommand)
	slog.Info("admin: initialized", "providers", len(m.providers))
	return nil
}

// Commands returns the /admin slash command definition, built from the
// registered providers plus the built-in info subcommand.
func (m *Module) Commands() []*discordgo.ApplicationCommand {
	opts := make([]*discordgo.ApplicationCommandOption, 0, len(m.providers)+1)
	for _, p := range m.providers {
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

// Listeners returns the /admin interaction listener.
func (m *Module) Listeners() []bot.EventListener {
	return []bot.EventListener{m.onAdminInteractionCreate}
}

// Components returns no message-component handlers for this module.
func (m *Module) Components() []bot.ComponentHandler { return nil }

// Background returns no background tasks for this module.
func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown is a no-op: admin owns no resources.
func (m *Module) Shutdown() error { return nil }

// onAdminInteractionCreate routes /admin interactions to the owner-gated
// dispatch. Every module listener sees every interaction, so non-matching
// interactions must be ignored without a response.
func (m *Module) onAdminInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if i.ApplicationCommandData().Name != "admin" {
		return
	}
	m.dispatch(s, i)
}

// handleAdminCommand runs behind bot.OwnerOnly. It defers an ephemeral response
// and dispatches the subcommand to the built-in info renderer or the matching
// provider.
func (m *Module) handleAdminCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return
	}
	if err := deferEphemeral(s, i); err != nil {
		slog.Error("admin: failed to defer interaction", "error", err)
		return
	}
	top := data.Options[0]
	if top.Name == "info" {
		if m.info == nil {
			editEphemeral(s, i, "Info unavailable.")
			return
		}
		editEphemeral(s, i, "```"+m.info(s)+"```")
		return
	}
	for _, p := range m.providers {
		if p.AdminSubcommandGroup().Name == top.Name {
			if len(top.Options) > 0 {
				p.HandleAdminSubcommand(s, i, top.Options[0])
			}
			return
		}
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
