package bot

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/settings"
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
	// The pool holds a single connection, so inside DB.Transaction always use
	// the provided tx handle for further queries — reusing this shared handle
	// there would block forever on the one connection.
	DB *gorm.DB

	// Stats collects StatsProviders from modules registered with the Bot. The
	// core module reads it for /status and /admin info.
	Stats *StatsRegistry

	// Settings is the runtime settings store backed by the shared database. A
	// module reads its hot keys through it so the owner can change them with
	// /admin config without a restart. It is nil in tests that build Deps by
	// hand, so modules must fall back to their Init-time values.
	Settings *settings.Store

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
