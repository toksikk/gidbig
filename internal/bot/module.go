package bot

import (
	"context"

	"github.com/bwmarrin/discordgo"
)

// BackgroundTask is a context-aware goroutine descriptor for the background supervisor.
type BackgroundTask struct {
	Name string
	Run  func(ctx context.Context)
}

// Module is the interface all bot modules implement. A module registers its
// Discord handlers (slash commands, components, messages, gateway events)
// through Deps.Router during Init.
type Module interface {
	Name() string
	Init(d Deps) error
	Background() []BackgroundTask
	Shutdown() error
}

// AdminProvider allows a Module to expose a subcommand group under /admin.
type AdminProvider interface {
	AdminSubcommandGroup() *discordgo.ApplicationCommandOption
	HandleAdminSubcommand(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption)
}
