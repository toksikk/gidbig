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
// from the config when running without a composition root. The owner gate runs
// through the router's middleware chain rather than a hand-wired wrapping.
func (m *Module) Init(d bot.Deps) error {
	m.ownerID = d.OwnerID
	if m.ownerID == "" && d.Config != nil {
		m.ownerID = d.Config.Discord.OwnerID
	}
	if m.ownerID == "" {
		slog.Warn("admin: no owner ID configured; /admin denies all callers")
	}
	d.Router.AddCommand(m.command(), m.onAdminInteractionCreate, bot.OwnerOnly(m.ownerID))
	slog.Info("admin: initialized", "providers", len(m.providers))
	return nil
}

// command returns the /admin command definition, built from the registered
// providers plus the built-in info subcommand.
func (m *Module) command() *discordgo.ApplicationCommand {
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

	return &discordgo.ApplicationCommand{
		Name:        "admin",
		Description: "Admin commands (owner only)",
		Options:     opts,
	}
}

// Background returns no background tasks for this module.
func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown is a no-op: admin owns no resources.
func (m *Module) Shutdown() error { return nil }

// onAdminInteractionCreate routes /admin interactions to the handler. The
// router dispatches by command name and applies the OwnerOnly middleware, so
// this only has to guard direct calls and forward.
func (m *Module) onAdminInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if i.ApplicationCommandData().Name != "admin" {
		return
	}
	m.handleAdminCommand(s, i)
}

// handleAdminCommand defers an ephemeral response and dispatches the subcommand
// to the built-in info renderer or the matching provider. Access control is the
// OwnerOnly middleware on the /admin command.
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
	return bot.DeferReply(s, i, true)
}

func editEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	bot.EditReply(s, i, &discordgo.WebhookEdit{Content: &content})
}
