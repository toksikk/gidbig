package bot

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
	"github.com/toksikk/gidbig/internal/cfg"
)

// Deps holds shared dependencies injected into every Module.
type Deps struct {
	Session *discordgo.Session
	Config  *cfg.Config
	LLM     *openai.Client
	Logger  *slog.Logger
	OwnerID string
}
