// Package openholidays serves /holidays and the LLM holidays tool from the
// OpenHolidays API (openholidaysapi.org).
package openholidays

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // Europe/Berlin must resolve in the alpine image

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/util"
)

const (
	commandName = "holidays"

	typePublic = "public"
	typeSchool = "school"

	defaultCountry = "DE"

	holidayCacheTTL  = 6 * time.Hour
	metadataCacheTTL = 24 * time.Hour
	maxCacheEntries  = 256

	autocompleteBudget = 2 * time.Second
	autocompleteLimit  = 25
	choiceMaxRunes     = 100
)

var (
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
	regionRe  = regexp.MustCompile(`^[A-Z]{2}(-[A-Z0-9]+)+$`)
)

// holidayZone decides what "today" is; the default country is DE.
var holidayZone = loadZone("Europe/Berlin")

func loadZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		slog.Warn("openholidays: time zone unavailable, using local time", "Zone", name, "error", err)
		return time.Local
	}
	return loc
}

// errInput marks a user-fixable input problem.
var errInput = errors.New("invalid input")

func inputErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInput, fmt.Sprintf(format, a...))
}

// query is one validated holiday lookup.
type query struct {
	country, region, kind, from, to string
}

func (q query) key() string {
	return strings.Join([]string{q.country, q.region, q.kind, q.from, q.to}, "|")
}

// rawQuery holds unvalidated user or model input; empty fields use defaults.
type rawQuery struct {
	country, region, kind, from, to string
}

// Module implements bot.Module for the OpenHolidays API.
type Module struct {
	client       *client
	now          func() time.Time
	holidays     *flightCache[[]holiday]
	countries    *flightCache[[]country]
	subdivisions *flightCache[[]subdivision]
	tools        *tool.Registry

	// ctx is cancelled by Shutdown; wg tracks in-flight /holidays replies.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// mu guards closed and wg.Go so no reply starts while Shutdown waits.
	mu     sync.Mutex
	closed bool
}

// New returns a Module using the public OpenHolidays API.
func New() *Module {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Module{client: newClient(), now: time.Now, ctx: ctx, cancel: cancel}
	clock := func() time.Time { return m.now() }
	m.holidays = newFlightCache[[]holiday](ctx, holidayCacheTTL, maxCacheEntries, clock)
	m.countries = newFlightCache[[]country](ctx, metadataCacheTTL, maxCacheEntries, clock)
	m.subdivisions = newFlightCache[[]subdivision](ctx, metadataCacheTTL, maxCacheEntries, clock)
	return m
}

func (m *Module) Name() string { return "openholidays" }

func (m *Module) Init(d bot.Deps) error {
	m.tools = d.Tools
	m.registerTools()
	for _, cmd := range m.Commands() {
		d.Router.AddCommand(cmd, m.onInteractionCreate)
	}
	slog.Info("openholidays: initialized")
	return nil
}

func (m *Module) Commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{{
		Name:        commandName,
		Description: "Public or school holidays via OpenHolidays API",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionString,
				Name:         "country",
				Description:  "Country ISO code (default DE)",
				Autocomplete: true,
				MaxLength:    2,
			},
			{
				Type:         discordgo.ApplicationCommandOptionString,
				Name:         "region",
				Description:  "Subdivision code, e.g. DE-NW",
				Autocomplete: true,
				MaxLength:    20,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "type",
				Description: "Holiday type (default public)",
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "public", Value: typePublic},
					{Name: "school", Value: typeSchool},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "from",
				Description: "Start date YYYY-MM-DD (default today)",
				MaxLength:   10,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "to",
				Description: "End date YYYY-MM-DD (default from + 12 months)",
				MaxLength:   10,
			},
		},
	}}
}

func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown cancels in-flight lookups and waits for pending /holidays replies.
func (m *Module) Shutdown() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.cancel()
	m.wg.Wait()
	m.holidays.wait()
	m.countries.wait()
	m.subdivisions.wait()
	return nil
}

// parseQuery validates raw input. defaultSpan is the range used when to is
// empty: to = from + defaultSpan - 1 day.
func (m *Module) parseQuery(r rawQuery, defaultSpan func(time.Time) time.Time) (query, error) {
	q := query{
		country: strings.ToUpper(strings.TrimSpace(r.country)),
		region:  strings.ToUpper(strings.TrimSpace(r.region)),
		kind:    strings.ToLower(strings.TrimSpace(r.kind)),
	}
	if q.kind == "" {
		q.kind = typePublic
	}
	if q.kind != typePublic && q.kind != typeSchool {
		return query{}, inputErr("type must be %q or %q", typePublic, typeSchool)
	}
	if q.region != "" {
		if !regionRe.MatchString(q.region) {
			return query{}, inputErr("region %q is not a subdivision code like DE-NW", q.region)
		}
		if q.country == "" {
			q.country = q.region[:2]
		}
		if !strings.HasPrefix(q.region, q.country+"-") {
			return query{}, inputErr("region %s does not belong to country %s", q.region, q.country)
		}
	}
	if q.country == "" {
		q.country = defaultCountry
	}
	if !countryRe.MatchString(q.country) {
		return query{}, inputErr("country %q is not a 2-letter ISO code", q.country)
	}

	now := m.now().In(holidayZone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from := today
	if s := strings.TrimSpace(r.from); s != "" {
		t, err := time.Parse(dateLayout, s)
		if err != nil {
			return query{}, inputErr("from %q is not a YYYY-MM-DD date", s)
		}
		from = t
	}
	to := defaultSpan(from)
	if s := strings.TrimSpace(r.to); s != "" {
		t, err := time.Parse(dateLayout, s)
		if err != nil {
			return query{}, inputErr("to %q is not a YYYY-MM-DD date", s)
		}
		to = t
	}
	if to.Before(from) {
		return query{}, inputErr("to must not be before from")
	}
	if !to.Before(from.AddDate(3, 0, 0)) {
		return query{}, inputErr("range must be shorter than 3 years")
	}
	q.from, q.to = from.Format(dateLayout), to.Format(dateLayout)
	return q, nil
}

func spanMonths(n int) func(time.Time) time.Time {
	return func(t time.Time) time.Time { return t.AddDate(0, n, -1) }
}

func spanDaysFrom(n int) func(time.Time) time.Time {
	return func(t time.Time) time.Time { return t.AddDate(0, 0, n) }
}

// getCountries returns the cached country list.
func (m *Module) getCountries(ctx context.Context) ([]country, error) {
	return m.countries.get(ctx, "countries", m.client.countries)
}

// getSubdivisions returns the cached, flattened subdivisions of a country.
func (m *Module) getSubdivisions(ctx context.Context, countryCode string) ([]subdivision, error) {
	return m.subdivisions.get(ctx, countryCode, func(ctx context.Context) ([]subdivision, error) {
		return m.client.subdivisions(ctx, countryCode)
	})
}

// checkCountry rejects a country the API does not know. When the country
// list cannot be loaded the check is skipped and the holiday call decides.
func (m *Module) checkCountry(ctx context.Context, code string) error {
	list, err := m.getCountries(ctx)
	if err != nil {
		slog.Warn("openholidays: country list unavailable, skipping check", "error", err)
		return nil
	}
	for _, c := range list {
		if strings.EqualFold(c.IsoCode, code) {
			return nil
		}
	}
	return inputErr("country %s is not supported by OpenHolidays", code)
}

// lookup fetches, region-filters and sorts the holidays for q.
func (m *Module) lookup(ctx context.Context, q query) ([]holiday, error) {
	hs, err := m.holidays.get(ctx, q.key(), func(ctx context.Context) ([]holiday, error) {
		return m.client.holidays(ctx, q.kind, q.country, q.region, q.from, q.to)
	})
	if err != nil {
		return nil, fmt.Errorf("holidays %s: %w", q.key(), err)
	}
	out := filterRegion(append([]holiday(nil), hs...), q.region)
	sortHolidays(out)
	return out, nil
}

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// The router routes the command and its autocomplete here; switch on the
	// type first because ApplicationCommandData panics on other types.
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		if i.ApplicationCommandData().Name == commandName {
			m.handleCommand(s, i)
		}
	case discordgo.InteractionApplicationCommandAutocomplete:
		if i.ApplicationCommandData().Name == commandName {
			m.handleAutocomplete(s, i)
		}
	}
}

func stringOpt(data discordgo.ApplicationCommandInteractionData, name string) string {
	if o := data.GetOption(name); o != nil {
		if v, ok := o.Value.(string); ok {
			return v
		}
	}
	return ""
}

func (m *Module) handleCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	q, err := m.parseQuery(rawQuery{
		country: stringOpt(data, "country"),
		region:  stringOpt(data, "region"),
		kind:    stringOpt(data, "type"),
		from:    stringOpt(data, "from"),
		to:      stringOpt(data, "to"),
	}, spanMonths(12))
	if err != nil {
		if rErr := s.InteractionRespond(i.Interaction, ephemeral(userMessage(err))); rErr != nil {
			slog.Error("openholidays: failed to respond to interaction", "error", rErr)
		}
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		slog.Error("openholidays: failed to respond to interaction", "error", err)
		return
	}
	if !m.startReply(func() { m.reply(s, i, q) }) {
		editReply(s, i, msgShuttingDown)
	}
}

// userMessage turns a validation error into reply text.
func userMessage(err error) string {
	return "Invalid input: " + strings.TrimPrefix(err.Error(), errInput.Error()+": ")
}

func ephemeral(content string) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:         content,
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: util.NoMentions(),
		},
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

func (m *Module) reply(s *discordgo.Session, i *discordgo.InteractionCreate, q query) {
	ctx, cancel := context.WithTimeout(m.ctx, 2*httpTimeout)
	defer cancel()
	// The country check may fetch, so it runs after the deferred response to
	// stay inside Discord's 3 s window; its error is therefore public.
	if err := m.checkCountry(ctx, q.country); err != nil {
		editReply(s, i, userMessage(err))
		return
	}
	editReply(s, i, m.composeDiscord(ctx, q))
}

// composeDiscord renders the /holidays reply, including user-facing errors.
func (m *Module) composeDiscord(ctx context.Context, q query) string {
	hs, err := m.lookup(ctx, q)
	if err != nil {
		slog.Error("openholidays: lookup failed", "Query", q.key(), "error", err)
		return "Failed to get holidays, try again later.\n" + attribution
	}
	return buildDiscord(q, hs)
}

func editReply(s *discordgo.Session, i *discordgo.InteractionCreate, body string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &body, AllowedMentions: util.NoMentions()}); err != nil {
		slog.Error("openholidays: failed to edit interaction response", "ChannelID", i.ChannelID, "error", err)
	}
}

// msgShuttingDown answers a /holidays request that arrives during shutdown.
const msgShuttingDown = "Bot is shutting down, try again later."

func (m *Module) handleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	ctx, cancel := context.WithTimeout(m.ctx, autocompleteBudget)
	defer cancel()
	choices := m.autocompleteChoices(ctx, data)
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	}); err != nil {
		slog.Error("openholidays: failed to respond to autocomplete", "error", err)
	}
}

// autocompleteChoices returns up to 25 prefix matches for the focused option.
// Errors yield an empty list so Discord still gets an answer in time.
func (m *Module) autocompleteChoices(ctx context.Context, data discordgo.ApplicationCommandInteractionData) []*discordgo.ApplicationCommandOptionChoice {
	choices := []*discordgo.ApplicationCommandOptionChoice{}
	var focused *discordgo.ApplicationCommandInteractionDataOption
	for _, o := range data.Options {
		if o.Focused {
			focused = o
			break
		}
	}
	if focused == nil {
		return choices
	}
	prefix, _ := focused.Value.(string)
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	add := func(code, label string, names ...string) bool {
		match := prefix == "" || strings.HasPrefix(strings.ToLower(code), prefix)
		for _, n := range names {
			match = match || strings.HasPrefix(strings.ToLower(n), prefix)
		}
		if match {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: truncateRunes(label, choiceMaxRunes), Value: code})
		}
		return len(choices) < autocompleteLimit
	}
	switch focused.Name {
	case "country":
		list, err := m.getCountries(ctx)
		if err != nil {
			slog.Warn("openholidays: country autocomplete failed", "error", err)
			return choices
		}
		for _, c := range list {
			name := pickName(c.Name)
			if !add(c.IsoCode, c.IsoCode+" — "+name, name) {
				break
			}
		}
	case "region":
		cc := strings.ToUpper(strings.TrimSpace(stringOpt(data, "country")))
		if !countryRe.MatchString(cc) {
			cc = defaultCountry
		}
		list, err := m.getSubdivisions(ctx, cc)
		if err != nil {
			slog.Warn("openholidays: region autocomplete failed", "Country", cc, "error", err)
			return choices
		}
		for _, sd := range list {
			name := pickName(sd.Name)
			if !add(sd.Code, sd.Code+" — "+name, name, sd.ShortName) {
				break
			}
		}
	}
	return choices
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
