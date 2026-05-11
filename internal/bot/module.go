package bot

import (
	"context"

	"github.com/bwmarrin/discordgo"
)

// EventListener is a Discord event listener compatible with discordgo.AddHandler.
type EventListener any

// ComponentHandler pairs a custom-ID prefix with its interaction handler.
type ComponentHandler struct {
	Prefix  string
	Handler func(*discordgo.Session, *discordgo.InteractionCreate)
}

// BackgroundTask is a context-aware goroutine descriptor for the background supervisor.
type BackgroundTask struct {
	Name string
	Run  func(ctx context.Context)
}

// Module is the interface all bot modules implement.
type Module interface {
	Name() string
	Init(d Deps) error
	Commands() []*discordgo.ApplicationCommand
	Listeners() []EventListener
	Components() []ComponentHandler
	Background() []BackgroundTask
	Shutdown() error
}
