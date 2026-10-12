package bot

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/tool"
	"gorm.io/gorm"
)

// Deps holds shared dependencies injected into every Module.
type Deps struct {
	Session *discordgo.Session
	Config  *cfg.Config
	LLM     *llm.Client
	Logger  *slog.Logger
	OwnerID string

	// DB is the shared gidbig database, opened once by the composition root.
	// Modules that persist data use it instead of opening the file themselves.
	// Modules must not close it; the composition root owns its lifecycle.
	DB *gorm.DB

	// Stats collects StatsProviders from modules registered with the Bot. The
	// core module reads it for /status and /admin info.
	Stats *StatsRegistry

	// Tools collects the LLM tool definitions modules register during Init.
	// The gippity answer loop reads it while composing an answer. A module
	// registers its own tools; no second wiring phase is needed.
	Tools *tool.Registry

	// Router is where a module registers its Discord handlers (commands,
	// components, messages, gateway events) during Init. The Bot sets it
	// before any module is registered; it is nil only in tests that call a
	// module's Init directly, in which case the Router methods are no-ops.
	Router *Router
}
