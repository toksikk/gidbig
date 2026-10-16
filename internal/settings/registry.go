// Package settings holds the runtime overrides the owner changes through
// Discord. Values are stored in the shared SQLite database as key-value rows and
// layered over config.yaml, so a change applies without a restart for the hot
// keys the registry declares.
package settings

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/toksikk/gidbig/internal/cfg"
)

// Kind is the value type of a setting. It decides how a stored string is
// parsed; every value is stored as a string.
type Kind string

const (
	// KindBool is a true/false setting.
	KindBool Kind = "bool"
	// KindInt is a whole-number setting.
	KindInt Kind = "int"
	// KindString is a free-form setting.
	KindString Kind = "string"
	// KindStringList is a comma-separated list setting.
	KindStringList Kind = "stringlist"
)

// Source names where an effective value came from, lowest precedence first.
type Source string

const (
	// SourceDefault is the built-in default.
	SourceDefault Source = "default"
	// SourceYAML is the value read from config.yaml at boot.
	SourceYAML Source = "yaml"
	// SourceDB is an explicit runtime override, which beats config.yaml.
	SourceDB Source = "db"
)

// Registry key names. They are exported so a module reads its own knob through
// a constant instead of repeating the dotted string, and so this registry stays
// the single definition of what is runtime-editable.
const (
	KeyGippityRateLimitMessagesPerHour = "gippity.rate_limit_messages_per_hour"
	KeyGippityReplyInThread            = "gippity.reply_in_thread"
	KeyGippityRespondToAllMessages     = "gippity.respond_to_all_messages"
	KeyGippityClassifierModel          = "gippity.classifier_model"
	KeyGippityClassifierNames          = "gippity.classifier_names"
	KeyGippityClassifierRateLimit      = "gippity.classifier_rate_limit_per_hour"
	KeyGippityDebugChannel             = "gippity.debug_channel"
	KeyGippityToolsEnabled             = "gippity.tools.enabled"
	KeyGippityToolsMaxIterations       = "gippity.tools.max_iterations"
	KeyGippityToolsAllowed             = "gippity.tools.allowed"
	KeyGippityToolsReasoningEffort     = "gippity.tools.reasoning_effort"
	KeySoundboardQueueMaxDepth         = "soundboard.queue_max_depth"
	KeyLLMModel                        = "llm.model"
	KeyLLMVisionModel                  = "llm.vision_model"
	KeyLLMMaxTokens                    = "llm.max_tokens"
	KeyGippityAllowedGuilds            = "gippity.allowed_guilds"
	KeyGippityIgnoredUsers             = "gippity.ignored_users"
	KeyLLMBaseURL                      = "llm.base_url"
	KeyDevMode                         = "dev_mode"
)

// DevModeNote explains that only part of dev_mode is hot.
const DevModeNote = "only the log level (debug/info) changes at runtime; log format, source locations and discordgo logging need a restart"

// RestartRequiredNote is the hint shown for every cold key.
const RestartRequiredNote = "restart required: edit config.yaml"

// Key describes one runtime-tunable setting.
type Key struct {
	// Name is the dotted key the owner types.
	Name string
	// Kind is the value type used to parse stored strings.
	Kind Kind
	// Hot is true when a change applies to the next read without a restart.
	Hot bool
	// Cold keys are read once at boot. They are listed read-only with their
	// effective value and source, and every write is refused with
	// ErrRestartRequired, so the command cannot silently store a dead value.
	// A cold key has only Extract; Apply and Validate stay nil.
	Cold bool
	// Note is an optional warning shown next to the key by every surface.
	Note string
	// Secret keys are never listed, read or written. No key is secret today;
	// the flag keeps a future addition from leaking a credential.
	Secret bool
	// Extract reads the deploy-time value (config.yaml plus defaults).
	Extract func(*cfg.Config) string
	// Apply writes the raw value into a config copy so the merged result can be
	// re-validated with cfg.Validate.
	Apply func(*cfg.Config, string) error
	// Validate rejects a value this key can never use, and runs before Apply.
	// Nil means cfg.Validate is the only rule. It covers two cases: a rule cfg
	// does not express at all (an empty llm.model), and a rule cfg applies to the
	// merged config but that the store also needs to judge a single stored row —
	// without it, Effective would report a row as in force that nothing applies.
	Validate func(string) error
}

// registry is the set of known keys: hot runtime-editable ones plus the
// read-only cold ones appended from coldKeys. Secrets (discord.token,
// web.oauth.client_secret, web.session_secret, llm.api_key) are deliberately
// absent, so they stay unknown everywhere.
var registry = append([]Key{
	{
		// cfg.Validate already rejects a non-positive value; repeating it here
		// lets the store tell that such a row is not in force.
		Name:     KeyGippityRateLimitMessagesPerHour,
		Kind:     KindInt,
		Hot:      true,
		Extract:  extractInt(func(c *cfg.Config) *int { return &c.Gippity.RateLimitMessagesPerHour }),
		Apply:    applyInt(func(c *cfg.Config) *int { return &c.Gippity.RateLimitMessagesPerHour }),
		Validate: requirePositiveInt,
	},
	{
		Name:    KeyGippityReplyInThread,
		Kind:    KindBool,
		Hot:     true,
		Extract: extractBool(func(c *cfg.Config) *bool { return &c.Gippity.ReplyInThread }),
		Apply:   applyBool(func(c *cfg.Config) *bool { return &c.Gippity.ReplyInThread }),
	},
	{
		Name:    KeyGippityRespondToAllMessages,
		Kind:    KindBool,
		Hot:     true,
		Extract: extractBool(func(c *cfg.Config) *bool { return &c.Gippity.RespondToAllMessages }),
		Apply:   applyBool(func(c *cfg.Config) *bool { return &c.Gippity.RespondToAllMessages }),
	},
	{
		Name:    KeyGippityClassifierModel,
		Kind:    KindString,
		Hot:     true,
		Extract: extractString(func(c *cfg.Config) *string { return &c.Gippity.ClassifierModel }),
		Apply:   applyString(func(c *cfg.Config) *string { return &c.Gippity.ClassifierModel }),
	},
	{
		Name:    KeyGippityClassifierNames,
		Kind:    KindStringList,
		Hot:     true,
		Extract: extractList(func(c *cfg.Config) *[]string { return &c.Gippity.ClassifierNames }),
		Apply:   applyList(func(c *cfg.Config) *[]string { return &c.Gippity.ClassifierNames }),
	},
	{
		Name:     KeyGippityClassifierRateLimit,
		Kind:     KindInt,
		Hot:      true,
		Extract:  extractInt(func(c *cfg.Config) *int { return &c.Gippity.ClassifierRateLimitPerHour }),
		Apply:    applyInt(func(c *cfg.Config) *int { return &c.Gippity.ClassifierRateLimitPerHour }),
		Validate: requirePositiveInt,
	},
	{
		Name:    KeyGippityDebugChannel,
		Kind:    KindString,
		Hot:     true,
		Extract: extractString(func(c *cfg.Config) *string { return &c.Gippity.DebugChannel }),
		Apply:   applyString(func(c *cfg.Config) *string { return &c.Gippity.DebugChannel }),
	},
	{
		Name:    KeyGippityToolsEnabled,
		Kind:    KindBool,
		Hot:     true,
		Extract: extractBool(func(c *cfg.Config) *bool { return &c.Gippity.Tools.Enabled }),
		Apply:   applyBool(func(c *cfg.Config) *bool { return &c.Gippity.Tools.Enabled }),
	},
	{
		// Deliberately no Validate: cfg.Validate only forbids a non-positive cap
		// while tools are enabled, and with tools off the loop makes no rounds, so
		// a stored 0 is a real, honoured value rather than an unusable row.
		Name:    KeyGippityToolsMaxIterations,
		Kind:    KindInt,
		Hot:     true,
		Extract: extractInt(func(c *cfg.Config) *int { return &c.Gippity.Tools.MaxIterations }),
		Apply:   applyInt(func(c *cfg.Config) *int { return &c.Gippity.Tools.MaxIterations }),
	},
	{
		Name:    KeyGippityToolsAllowed,
		Kind:    KindStringList,
		Hot:     true,
		Extract: extractList(func(c *cfg.Config) *[]string { return &c.Gippity.Tools.Allowed }),
		Apply:   applyList(func(c *cfg.Config) *[]string { return &c.Gippity.Tools.Allowed }),
	},
	{
		// cfg.Validate already rejects an unknown enum value; reusing its exported
		// rule lets the store tell that such a row is not in force, without a
		// second copy of the accepted list.
		Name:     KeyGippityToolsReasoningEffort,
		Kind:     KindString,
		Hot:      true,
		Extract:  extractString(func(c *cfg.Config) *string { return &c.Gippity.Tools.ReasoningEffort }),
		Apply:    applyString(func(c *cfg.Config) *string { return &c.Gippity.Tools.ReasoningEffort }),
		Validate: cfg.ValidateReasoningEffort,
	},
	{
		// cfg.Validate already rejects a non-positive value; repeating it here
		// lets the store tell that such a row is not in force.
		Name:     KeySoundboardQueueMaxDepth,
		Kind:     KindInt,
		Hot:      true,
		Extract:  extractInt(func(c *cfg.Config) *int { return &c.Soundboard.QueueMaxDepth }),
		Apply:    applyInt(func(c *cfg.Config) *int { return &c.Soundboard.QueueMaxDepth }),
		Validate: requirePositiveInt,
	},
	{
		Name:     KeyLLMModel,
		Kind:     KindString,
		Hot:      true,
		Extract:  extractString(func(c *cfg.Config) *string { return &c.LLM.Model }),
		Apply:    applyString(func(c *cfg.Config) *string { return &c.LLM.Model }),
		Validate: requireNonEmpty,
	},
	{
		// One cap for every completion the bot makes except the non-mention
		// classifier, which keeps its own much smaller bound so it can only
		// answer YES or NO.
		Name:     KeyLLMMaxTokens,
		Kind:     KindInt,
		Hot:      true,
		Extract:  extractInt(func(c *cfg.Config) *int { return &c.LLM.MaxTokens }),
		Apply:    applyInt(func(c *cfg.Config) *int { return &c.LLM.MaxTokens }),
		Validate: requirePositiveInt,
	},
	{
		// An empty llm.vision_model means "follow llm.model", so unlike llm.model
		// an empty value is meaningful here and is accepted.
		Name:    KeyLLMVisionModel,
		Kind:    KindString,
		Hot:     true,
		Extract: extractString(func(c *cfg.Config) *string { return &c.LLM.VisionModel }),
		Apply:   applyString(func(c *cfg.Config) *string { return &c.LLM.VisionModel }),
	},
	{
		// cfg.Validate already rejects an empty list; repeating it here lets the
		// store tell that such a row is not in force.
		Name:     KeyGippityAllowedGuilds,
		Kind:     KindStringList,
		Hot:      true,
		Extract:  extractList(func(c *cfg.Config) *[]string { return &c.Gippity.AllowedGuilds }),
		Apply:    applyList(func(c *cfg.Config) *[]string { return &c.Gippity.AllowedGuilds }),
		Validate: requireNonEmptyList,
	},
	{
		// An empty list is meaningful (nobody ignored); only an empty item is
		// rejected.
		Name:     KeyGippityIgnoredUsers,
		Kind:     KindStringList,
		Hot:      true,
		Extract:  extractList(func(c *cfg.Config) *[]string { return &c.Gippity.IgnoredUsers }),
		Apply:    applyList(func(c *cfg.Config) *[]string { return &c.Gippity.IgnoredUsers }),
		Validate: rejectEmptyItems,
	},
	{
		// Only the slog level follows a runtime change; the handler format,
		// AddSource and the discordgo log level are chosen once at boot.
		Name:    KeyDevMode,
		Kind:    KindBool,
		Hot:     true,
		Note:    DevModeNote,
		Extract: extractBool(func(c *cfg.Config) *bool { return &c.DevMode }),
		Apply:   applyBool(func(c *cfg.Config) *bool { return &c.DevMode }),
	},
}, coldKeys...)

// coldKeys are the non-secret settings read only at boot.
var coldKeys = []Key{
	cold("discord.owner_id", KindString, extractString(func(c *cfg.Config) *string { return &c.Discord.OwnerID })),
	cold("discord.shard_id", KindInt, extractInt(func(c *cfg.Config) *int { return &c.Discord.ShardID })),
	cold("discord.shard_count", KindInt, extractInt(func(c *cfg.Config) *int { return &c.Discord.ShardCount })),
	cold("web.port", KindInt, extractInt(func(c *cfg.Config) *int { return &c.Web.Port })),
	cold("web.oauth.client_id", KindString, extractString(func(c *cfg.Config) *string { return &c.Web.Oauth.ClientID })),
	cold("web.oauth.redirect_uri", KindString, extractString(func(c *cfg.Config) *string { return &c.Web.Oauth.RedirectURI })),
	cold("web.eso.rate_limit_burst", KindInt, extractInt(func(c *cfg.Config) *int { return &c.Web.Eso.RateLimitBurst })),
	cold("web.eso.rate_limit_per_minute", KindInt, extractInt(func(c *cfg.Config) *int { return &c.Web.Eso.RateLimitPerMinute })),
	cold("web.eso.max_concurrent_requests", KindInt, extractInt(func(c *cfg.Config) *int { return &c.Web.Eso.MaxConcurrentRequests })),
	cold("web.eso.request_timeout_seconds", KindInt, extractInt(func(c *cfg.Config) *int { return &c.Web.Eso.RequestTimeoutSeconds })),
	cold("leetoclock.announcement_channels", KindStringList, extractList(func(c *cfg.Config) *[]string { return &c.Leetoclock.AnnouncementChannels })),
	cold("leetoclock.debug_channel", KindString, extractString(func(c *cfg.Config) *string { return &c.Leetoclock.DebugChannel })),
	cold("leetoclock.debug", KindBool, extractBool(func(c *cfg.Config) *bool { return &c.Leetoclock.Debug })),
	cold("leetoclock.emojis.zonk", KindString, extractString(func(c *cfg.Config) *string { return &c.Leetoclock.Emojis.Zonk })),
	cold("leetoclock.emojis.lol", KindString, extractString(func(c *cfg.Config) *string { return &c.Leetoclock.Emojis.Lol })),
	cold("leetoclock.emojis.notamused", KindString, extractString(func(c *cfg.Config) *string { return &c.Leetoclock.Emojis.NotAmused })),
	cold("leetoclock.emojis.wat", KindString, extractString(func(c *cfg.Config) *string { return &c.Leetoclock.Emojis.Wat })),
	cold("llm.provider", KindString, extractString(func(c *cfg.Config) *string { return &c.LLM.Provider })),
	cold(KeyLLMBaseURL, KindString, extractString(func(c *cfg.Config) *string { return &c.LLM.BaseURL })),
	cold("llm.http_referer", KindString, extractString(func(c *cfg.Config) *string { return &c.LLM.HTTPReferer })),
	cold("llm.title", KindString, extractString(func(c *cfg.Config) *string { return &c.LLM.Title })),
	cold("llm.personality", KindString, extractString(func(c *cfg.Config) *string { return &c.LLM.Personality })),
	cold("llm.personality_preset", KindString, extractString(func(c *cfg.Config) *string { return &c.LLM.Preset })),
}

// cold builds a read-only key.
func cold(name string, kind Kind, extract func(*cfg.Config) string) Key {
	return Key{Name: name, Kind: kind, Cold: true, Note: RestartRequiredNote, Extract: extract}
}

// byName indexes the registry once at package init.
var byName = func() map[string]Key {
	m := make(map[string]Key, len(registry))
	for _, k := range registry {
		m[k.Name] = k
	}
	return m
}()

// Lookup returns the registry entry for a key name. A secret key is reported as
// unknown, so a caller cannot probe for the existence of a credential.
func Lookup(name string) (Key, bool) {
	k, ok := byName[name]
	if !ok || k.Secret {
		return Key{}, false
	}
	return k, true
}

// Keys returns every registered key in name order, excluding secret keys so a
// listing can never surface one.
func Keys() []Key {
	out := make([]Key, 0, len(registry))
	for _, k := range registry {
		if k.Secret {
			continue
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func extractBool(get func(*cfg.Config) *bool) func(*cfg.Config) string {
	return func(c *cfg.Config) string { return strconv.FormatBool(*get(c)) }
}

func extractInt(get func(*cfg.Config) *int) func(*cfg.Config) string {
	return func(c *cfg.Config) string { return strconv.Itoa(*get(c)) }
}

func extractString(get func(*cfg.Config) *string) func(*cfg.Config) string {
	return func(c *cfg.Config) string { return *get(c) }
}

func extractList(get func(*cfg.Config) *[]string) func(*cfg.Config) string {
	return func(c *cfg.Config) string { return formatList(*get(c)) }
}

func applyBool(get func(*cfg.Config) *bool) func(*cfg.Config, string) error {
	return func(c *cfg.Config, v string) error {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("must be true or false, got %q", v)
		}
		*get(c) = b
		return nil
	}
}

func applyInt(get func(*cfg.Config) *int) func(*cfg.Config, string) error {
	return func(c *cfg.Config, v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("must be a whole number, got %q", v)
		}
		*get(c) = n
		return nil
	}
}

func applyString(get func(*cfg.Config) *string) func(*cfg.Config, string) error {
	return func(c *cfg.Config, v string) error {
		*get(c) = v
		return nil
	}
}

func applyList(get func(*cfg.Config) *[]string) func(*cfg.Config, string) error {
	return func(c *cfg.Config, v string) error {
		*get(c) = parseList(v)
		return nil
	}
}

// parseList splits a comma- or newline-separated value into a trimmed list with
// no empty entries. An empty value yields an empty list.
func parseList(v string) []string {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// formatList renders a list in the form parseList accepts.
func formatList(v []string) string { return strings.Join(v, ",") }

// requirePositiveInt rejects zero and negative numbers, and values that are not
// whole numbers.
func requirePositiveInt(raw string) error {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("must be a whole number, got %q", raw)
	}
	if n <= 0 {
		return fmt.Errorf("must be greater than zero, got %d", n)
	}
	return nil
}

// requireNonEmptyList rejects an empty list and a list with an empty item.
func requireNonEmptyList(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("must contain at least one item")
	}
	fields := strings.Split(strings.ReplaceAll(raw, "\n", ","), ",")
	if len(fields) == 0 {
		return errors.New("must contain at least one item")
	}
	for _, f := range fields {
		if strings.TrimSpace(f) == "" {
			return errors.New("must not contain an empty item")
		}
	}
	return nil
}

// rejectEmptyItems accepts an empty list but rejects an empty item in a
// non-empty one.
func rejectEmptyItems(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return requireNonEmptyList(raw)
}

// requireNonEmpty rejects an empty value. An empty model would either be
// invalid (openrouter) or silently mean "use the built-in default", so the
// owner is told to use unset instead.
func requireNonEmpty(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("must not be empty; use unset to fall back to config.yaml")
	}
	return nil
}
