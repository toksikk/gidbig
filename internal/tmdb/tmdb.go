// Package tmdb implements the /wheretostream slash command and the
// streaming_providers LLM tool. Both look up where a movie or series streams
// in a region using TMDB watch-provider data (sourced from JustWatch).
package tmdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/util"
)

const (
	commandName = "wheretostream"
	// pickPrefix starts every select-menu custom ID: tmdb:pick:<userID>:<region>.
	pickPrefix = "tmdb:pick:"

	cacheTTL    = time.Hour
	workTimeout = 15 * time.Second
)

// Module implements bot.Module for /wheretostream.
type Module struct {
	client   *Client
	conf     *cfg.Config
	settings *settings.Store
	tools    *tool.Registry
	now      func() time.Time

	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	details   Details
	expiresAt time.Time
}

// New returns a new tmdb Module. Register it only when a token is configured.
func New() *Module {
	ctx, cancel := context.WithCancel(context.Background())
	return &Module{now: time.Now, ctx: ctx, cancel: cancel, cache: make(map[string]cacheEntry)}
}

func (m *Module) Name() string { return "tmdb" }

func (m *Module) Init(d bot.Deps) error {
	if d.Config == nil || d.Config.TMDB.ReadAccessToken == "" {
		return errors.New("tmdb: read_access_token is not set")
	}
	m.conf = d.Config
	m.settings = d.Settings
	m.tools = d.Tools
	m.client = NewClient(d.Config.TMDB.ReadAccessToken)
	m.registerTools()
	for _, cmd := range m.Commands() {
		d.Router.AddCommand(cmd, m.onCommand)
	}
	d.Router.AddComponent(pickPrefix, m.onPick)
	slog.Info("tmdb: initialized")
	return nil
}

// Commands returns the slash command definitions for this module.
func (m *Module) Commands() []*discordgo.ApplicationCommand {
	minLen, maxLen := 2, 2
	return []*discordgo.ApplicationCommand{{
		Name:        commandName,
		Description: "Find where a movie or series streams",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "Movie or series title", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "region", Description: "Two-letter country code (default from config, e.g. DE)", MinLength: &minLen, MaxLength: maxLen},
		},
	}}
}

func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown cancels in-flight lookups.
func (m *Module) Shutdown() error {
	m.cancel()
	return nil
}

// region returns the effective default region: settings override, then config.
func (m *Module) region() string {
	if m.settings != nil {
		if v := m.settings.String(settings.KeyTMDBRegion); v != "" {
			return v
		}
	}
	if m.conf != nil {
		return m.conf.TMDBRegion()
	}
	return cfg.DefaultTMDBRegion
}

// language returns the effective language: settings override, then config.
func (m *Module) language() string {
	if m.settings != nil {
		if v := m.settings.String(settings.KeyTMDBLanguage); v != "" {
			return v
		}
	}
	if m.conf != nil {
		return m.conf.TMDBLanguage()
	}
	return cfg.DefaultTMDBLanguage
}

// normalizeRegion uppercases a user-supplied region and checks its format.
// Empty input yields the default region.
func (m *Module) normalizeRegion(raw string) (string, error) {
	r := strings.ToUpper(strings.TrimSpace(raw))
	if r == "" {
		return m.region(), nil
	}
	if cfg.ValidateTMDBRegion(r) != nil {
		return "", fmt.Errorf("invalid region %q: use a two-letter country code like DE", raw)
	}
	return r, nil
}

// details returns cached details or fetches them. No lock is held across the
// HTTP call.
func (m *Module) details(ctx context.Context, kind string, id int) (Details, error) {
	lang := m.language()
	key := kind + ":" + strconv.Itoa(id) + ":" + lang
	now := m.now()
	m.mu.Lock()
	if e, ok := m.cache[key]; ok && now.Before(e.expiresAt) {
		m.mu.Unlock()
		return e.details, nil
	}
	m.mu.Unlock()

	d, err := m.client.Details(ctx, kind, id, lang)
	if err != nil {
		return Details{}, err
	}
	m.mu.Lock()
	for k, e := range m.cache {
		if !now.Before(e.expiresAt) {
			delete(m.cache, k)
		}
	}
	m.cache[key] = cacheEntry{details: d, expiresAt: m.now().Add(cacheTTL)}
	m.mu.Unlock()
	return d, nil
}

func (m *Module) onCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand || i.ApplicationCommandData().Name != commandName {
		return
	}
	var title, rawRegion string
	for _, o := range i.ApplicationCommandData().Options {
		switch o.Name {
		case "title":
			title = strings.TrimSpace(o.StringValue())
		case "region":
			rawRegion = o.StringValue()
		}
	}
	region, err := m.normalizeRegion(rawRegion)
	if err == nil && title == "" {
		err = errors.New("title must not be empty")
	}
	if err != nil {
		respondEphemeral(s, i, err.Error())
		return
	}
	userID := interactionUserID(i)

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		slog.Error("tmdb: failed to defer interaction", "error", err)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, workTimeout)
		defer cancel()
		edit := m.searchReply(ctx, title, userID, region)
		if _, err := s.InteractionResponseEdit(i.Interaction, edit); err != nil {
			slog.Error("tmdb: failed to edit interaction response", "error", err)
		}
	}()
}

// searchReply builds the deferred edit for a search: a select menu of matches,
// a not-found note, or an error note.
func (m *Module) searchReply(ctx context.Context, title, userID, region string) *discordgo.WebhookEdit {
	matches, err := m.client.SearchMulti(ctx, title, m.language())
	var content string
	var comps []discordgo.MessageComponent
	switch {
	case err != nil:
		slog.Error("tmdb: search failed", "error", err)
		content = userError(err)
	case len(matches) == 0:
		content = fmt.Sprintf("No movie or series found for `%s`.", sanitizeInline(title))
	default:
		content = fmt.Sprintf("Matches for `%s` (region %s) — pick one:", sanitizeInline(title), region)
		comps = []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				MenuType:    discordgo.StringSelectMenu,
				CustomID:    pickCustomID(userID, region),
				Placeholder: "Choose a movie or series",
				Options:     selectOptions(matches),
			},
		}}}
	}
	if comps == nil {
		comps = []discordgo.MessageComponent{}
	}
	return &discordgo.WebhookEdit{Content: &content, Components: &comps, AllowedMentions: util.NoMentions()}
}

func (m *Module) onPick(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	data := i.MessageComponentData()
	owner, region, err := parsePickID(data.CustomID)
	if err != nil {
		slog.Warn("tmdb: malformed pick custom id", "error", err)
		return
	}
	if interactionUserID(i) != owner {
		respondEphemeral(s, i, "Not your search — run /wheretostream yourself.")
		return
	}
	if len(data.Values) != 1 {
		return
	}
	kind, id, err := parsePickValue(data.Values[0])
	if err != nil {
		slog.Warn("tmdb: malformed pick value", "error", err)
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	}); err != nil {
		slog.Error("tmdb: failed to defer pick", "error", err)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, workTimeout)
		defer cancel()
		empty := []discordgo.MessageComponent{}
		content := ""
		edit := &discordgo.WebhookEdit{Content: &content, Components: &empty, AllowedMentions: util.NoMentions()}
		d, err := m.details(ctx, kind, id)
		if err != nil {
			slog.Error("tmdb: details lookup failed", "kind", kind, "id", id, "error", err)
			content = userError(err)
		} else {
			edit.Embeds = &[]*discordgo.MessageEmbed{buildEmbed(d, region)}
		}
		if _, err := s.InteractionResponseEdit(i.Interaction, edit); err != nil {
			slog.Error("tmdb: failed to edit pick response", "error", err)
		}
	}()
}

// pickCustomID encodes the invoking user and region into the menu's custom ID.
func pickCustomID(userID, region string) string { return pickPrefix + userID + ":" + region }

// parsePickID strictly decodes tmdb:pick:<numeric userID>:<REGION>.
func parsePickID(id string) (userID, region string, err error) {
	rest, ok := strings.CutPrefix(id, pickPrefix)
	if !ok {
		return "", "", fmt.Errorf("custom id %q lacks prefix", id)
	}
	userID, region, ok = strings.Cut(rest, ":")
	if !ok || !isDigits(userID) || cfg.ValidateTMDBRegion(region) != nil || region == "" {
		return "", "", fmt.Errorf("custom id %q is malformed", id)
	}
	return userID, region, nil
}

// parsePickValue strictly decodes movie:<id> or tv:<id>.
func parsePickValue(v string) (kind string, id int, err error) {
	kind, rawID, ok := strings.Cut(v, ":")
	if !ok || (kind != kindMovie && kind != kindTV) || !isDigits(rawID) {
		return "", 0, fmt.Errorf("pick value %q is malformed", v)
	}
	id, err = strconv.Atoi(rawID)
	if err != nil || id <= 0 {
		return "", 0, fmt.Errorf("pick value %q has a bad id", v)
	}
	return kind, id, nil
}

func isDigits(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// interactionUserID returns the invoking user's ID for guild and DM interactions.
func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// ephemeralResponse builds an ephemeral reply.
func ephemeralResponse(content string) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:         content,
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: util.NoMentions(),
		},
	}
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if err := s.InteractionRespond(i.Interaction, ephemeralResponse(content)); err != nil {
		slog.Error("tmdb: failed to send ephemeral reply", "error", err)
	}
}

// userError maps an error to a short user-facing message without internals.
func userError(err error) string {
	if errors.Is(err, ErrRateLimited) {
		return ErrRateLimited.Error() + "."
	}
	return "Could not reach TMDB right now. Try again in a minute."
}

// sanitizeInline keeps user text inside an inline-code span.
func sanitizeInline(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '`' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
	return truncateRunes(s, 100)
}
