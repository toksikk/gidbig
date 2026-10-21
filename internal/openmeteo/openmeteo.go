// Package openmeteo serves /weather and the LLM weather tool from Open-Meteo.
package openmeteo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
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
}

// New returns a Module using the public Open-Meteo endpoints.
func New() *Module {
	m := &Module{client: newClient(), now: time.Now}
	clock := func() time.Time { return m.now() }
	m.forecasts = newFlightCache[forecastResponse](forecastCacheTTL, maxCacheEntries, clock)
	m.geocodes = newFlightCache[geoResult](geocodeCacheTTL, maxCacheEntries, clock)
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
				Description: "Forecast days (1-16, default 7)",
				MinValue:    &minD,
				MaxValue:    maxD,
			},
		},
	}}
}

func (m *Module) Background() []bot.BackgroundTask { return nil }
func (m *Module) Shutdown() error                  { return nil }

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
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*httpTimeout)
		defer cancel()
		body := m.composeDiscord(ctx, location, forecast, days)
		if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &body, AllowedMentions: util.NoMentions()}); err != nil {
			slog.Error("openmeteo: failed to edit interaction response", "ChannelID", i.ChannelID, "error", err)
		}
	}()
}

// composeDiscord renders the /weather reply, including user-facing errors.
func (m *Module) composeDiscord(ctx context.Context, location string, forecast bool, days int) string {
	if !forecast {
		days = 1
	}
	shown := strings.ReplaceAll(location, "`", "'")
	p, fc, err := m.lookup(ctx, location, false, 0, 0, days)
	if errors.Is(err, errNotFound) {
		return "Ort nicht gefunden: `" + shown + "`"
	}
	if err != nil {
		slog.Error("openmeteo: weather lookup failed", "Location", location, "error", err)
		return "Wetter für `" + shown + "` konnte nicht abgerufen werden"
	}
	if forecast {
		return buildDiscordForecast(p, fc, discordMaxLen)
	}
	return buildDiscordCurrent(p, fc)
}
