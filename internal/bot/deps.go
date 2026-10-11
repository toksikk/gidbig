package bot

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
	"github.com/toksikk/gidbig/internal/cfg"
	"gorm.io/gorm"
)

// Deps holds shared dependencies injected into every Module.
type Deps struct {
	Session *discordgo.Session
	Config  *cfg.Config
	LLM     *openai.Client
	Logger  *slog.Logger
	OwnerID string

	// DB is the shared gidbig database, opened once by the composition root.
	// Modules that persist data use it instead of opening the file themselves.
	// Modules must not close it; the composition root owns its lifecycle.
	DB *gorm.DB

	// Stats collects StatsProviders from modules registered with the Bot. The
	// core module reads it for /status and /admin info.
	Stats *StatsRegistry
}
