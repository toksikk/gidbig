// Package openmeteo serves /weather and the LLM weather tool from Open-Meteo.
package openmeteo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/util"
)

const (
	forecastCacheTTL = 10 * time.Minute
	geocodeCacheTTL  = 24 * time.Hour
	maxCacheEntries  = 256
	maxLocationLen   = 100
	minDays          = 1
	maxDays          = 16
	defaultDays      = 7
)

// errNotFound reports that geocoding returned no result.
var errNotFound = errors.New("location not found")

type geoResult struct {
	place place
	found bool
}

// Module implements bot.Module for Open-Meteo weather.
type Module struct {
	client    *client
	now       func() time.Time
	forecasts *flightCache[forecastResponse]
	geocodes  *flightCache[geoResult]
	tools     *tool.Registry

	// ctx is cancelled by Shutdown; wg tracks in-flight /weather replies.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// mu guards closed and wg.Go so no reply starts while Shutdown waits.
	mu     sync.Mutex
	closed bool
}

// New returns a Module using the public Open-Meteo endpoints.
func New() *Module {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Module{client: newClient(), now: time.Now, ctx: ctx, cancel: cancel}
	clock := func() time.Time { return m.now() }
	m.forecasts = newFlightCache[forecastResponse](ctx, forecastCacheTTL, maxCacheEntries, clock)
	m.geocodes = newFlightCache[geoResult](ctx, geocodeCacheTTL, maxCacheEntries, clock)
	return m
}

func (m *Module) Name() string { return "openmeteo" }

func (m *Module) Init(d bot.Deps) error {
	m.tools = d.Tools
	m.registerTools()
	for _, cmd := range m.Commands() {
		d.Router.AddCommand(cmd, m.onInteractionCreate)
	}
	slog.Info("openmeteo: initialized")
	return nil
}

func (m *Module) Commands() []*discordgo.ApplicationCommand {
	minD, maxD := float64(minDays), float64(maxDays)
	return []*discordgo.ApplicationCommand{{
		Name:        "weather",
		Description: "Weather via Open-Meteo",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "location",
				Description: "City or place name",
				Required:    true,
				MaxLength:   maxLocationLen,
			},
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "forecast",
				Description: "Show the daily forecast instead of current conditions",
			},
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "days",
				Description: "Forecast days (1-16, default 7); only used with forecast:true",
				MinValue:    &minD,
				MaxValue:    maxD,
			},
		},
	}}
}

func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown cancels in-flight lookups and waits for pending /weather replies.
func (m *Module) Shutdown() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.cancel()
	m.wg.Wait()
	m.forecasts.wait()
	m.geocodes.wait()
	return nil
}

func clampDays(d int) int {
	return max(minDays, min(maxDays, d))
}

// lookup resolves the location and fetches the forecast. When hasCoords is
// true the location is only a label and no geocoding happens.
func (m *Module) lookup(ctx context.Context, location string, hasCoords bool, lat, lon float64, days int) (place, forecastResponse, error) {
	var p place
	if hasCoords {
		p = place{Name: location, Latitude: lat, Longitude: lon}
	} else {
		key := strings.ToLower(location)
		res, err := m.geocodes.get(ctx, key, func(ctx context.Context) (geoResult, error) {
			pl, found, err := m.client.geocode(ctx, location)
			return geoResult{place: pl, found: found}, err
		})
		if err != nil {
			return place{}, forecastResponse{}, fmt.Errorf("geocode %q: %w", location, err)
		}
		if !res.found {
			return place{}, forecastResponse{}, errNotFound
		}
		p = res.place
	}
	key := fmt.Sprintf("%.2f,%.2f,%d", round2(p.Latitude), round2(p.Longitude), days)
	fc, err := m.forecasts.get(ctx, key, func(ctx context.Context) (forecastResponse, error) {
		return m.client.forecast(ctx, p.Latitude, p.Longitude, days)
	})
	if err != nil {
		return place{}, forecastResponse{}, fmt.Errorf("forecast: %w", err)
	}
	return p, fc, nil
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand || i.ApplicationCommandData().Name != "weather" {
		return
	}
	data := i.ApplicationCommandData()
	opt := data.GetOption("location")
	if opt == nil {
		return
	}
	location := strings.TrimSpace(opt.StringValue())
	if location == "" || utf8.RuneCountInString(location) > maxLocationLen {
		if err := s.InteractionRespond(i.Interaction, invalidLocationResponse()); err != nil {
			slog.Error("openmeteo: failed to respond to interaction", "error", err)
		}
		return
	}
	forecast := false
	if o := data.GetOption("forecast"); o != nil {
		forecast = o.BoolValue()
	}
	days := defaultDays
	if o := data.GetOption("days"); o != nil && forecast {
		days = clampDays(int(o.IntValue()))
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		slog.Error("openmeteo: failed to respond to interaction", "error", err)
		return
	}
	if !m.startReply(func() { m.replyWeather(s, i, location, forecast, days) }) {
		editReply(s, i, "Bot is shutting down, try again later.")
	}
}

// startReply runs fn in a tracked goroutine unless the module is shutting down.
func (m *Module) startReply(fn func()) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	m.wg.Go(fn)
	return true
}

func (m *Module) replyWeather(s *discordgo.Session, i *discordgo.InteractionCreate, location string, forecast bool, days int) {
	ctx, cancel := context.WithTimeout(m.ctx, 2*httpTimeout)
	defer cancel()
	editReply(s, i, m.composeDiscord(ctx, location, forecast, days))
}

func editReply(s *discordgo.Session, i *discordgo.InteractionCreate, body string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &body, AllowedMentions: util.NoMentions()}); err != nil {
		slog.Error("openmeteo: failed to edit interaction response", "ChannelID", i.ChannelID, "error", err)
	}
}

// invalidLocationResponse is the ephemeral reply for an empty or overlong location.
func invalidLocationResponse() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Please give a location of 1–%d characters.", maxLocationLen),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}
}

// composeDiscord renders the /weather reply, including user-facing errors.
func (m *Module) composeDiscord(ctx context.Context, location string, forecast bool, days int) string {
	if !forecast {
		days = 1
	}
	shown := strings.ReplaceAll(location, "`", "'")
	p, fc, err := m.lookup(ctx, location, false, 0, 0, days)
	if errors.Is(err, errNotFound) {
		return "Location not found: `" + shown + "`"
	}
	if err != nil {
		slog.Error("openmeteo: weather lookup failed", "Location", location, "error", err)
		return "Failed to get weather for `" + shown + "`"
	}
	if forecast {
		return buildDiscordForecast(p, fc)
	}
	return buildDiscordCurrent(p, fc)
}
