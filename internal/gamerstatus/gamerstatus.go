package gamerstatus

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/util"
)

var games = []string{
	"Terranigma",
	"Secret of Mana",
	"Quake III Arena",
	"Duke Nukem 3D",
	"Monkey Island 2: LeChuck's Revenge",
	"Turtles in Time",
	"Unreal Tournament",
	"Half-Life",
	"Half-Life 2",
	"Warcraft II",
	"Starcraft",
	"Diablo",
	"Diablo II",
	"A Link to the Past",
	"Ocarina of Time",
	"Star Fox",
	"Tetris",
	"Pokémon Red",
	"Pokémon Blue",
	"Die Siedler II",
	"Day of the Tentacle",
	"Maniac Mansion",
	"Prince of Persia",
	"Super Mario Kart",
	"Pac-Man",
	"Frogger",
	"Donkey Kong",
	"Donkey Kong Country",
	"Asteroids",
	"Doom",
	"Breakout",
	"Street Fighter II",
	"Wolfenstein 3D",
	"Mega Man",
	"Myst",
	"R-Type",
}

// Module implements bot.Module for rotating the bot's game status.
type Module struct {
	session      *discordgo.Session
	initialDelay time.Duration
	rotationMin  time.Duration
	rotationMax  time.Duration
}

// New returns a new gamerstatus Module with production timing defaults.
func New() *Module {
	return &Module{
		initialDelay: 5 * time.Minute,
		rotationMin:  5 * time.Minute,
		rotationMax:  15 * time.Minute,
	}
}

func (m *Module) Name() string { return "gamerstatus" }

func (m *Module) Init(d bot.Deps) error {
	m.session = d.Session
	slog.Info("gamerstatus: initialized")
	return nil
}

func (m *Module) Commands() []*discordgo.ApplicationCommand { return nil }
func (m *Module) Listeners() []bot.EventListener           { return nil }
func (m *Module) Components() []bot.ComponentHandler       { return nil }
func (m *Module) Shutdown() error                          { return nil }

func (m *Module) Background() []bot.BackgroundTask {
	return []bot.BackgroundTask{
		{Name: "gamerstatus/rotate", Run: m.runStatusLoop},
	}
}

func (m *Module) runStatusLoop(ctx context.Context) {
	select {
	case <-time.After(m.initialDelay):
	case <-ctx.Done():
		return
	}

	for {
		if err := m.session.UpdateStreamingStatus(1, "", ""); err != nil {
			slog.Error("gamerstatus: could not clear streaming status", "error", err)
		}

		var game string
		if util.IsSpecial() {
			game = string(util.Cl)
		} else {
			game = games[rand.Intn(len(games))]
		}
		if err := m.session.UpdateGameStatus(0, game); err != nil {
			slog.Error("gamerstatus: could not set game status", "error", err)
		}

		interval := m.rotationMin + time.Duration(rand.Int63n(int64(m.rotationMax-m.rotationMin)))
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return
		}
	}
}
