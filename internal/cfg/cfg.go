package cfg

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"time"
	// Embed the zone database so tv.timezone validates in minimal images.
	_ "time/tzdata"

	"gopkg.in/yaml.v3"
)

// Defaults applied to optional operational tuning fields when absent from
// config.yaml, preserving legacy behavior.
const (
	defaultSoundboardQueueMaxDepth  = 6
	defaultGippityRateLimitPerHour  = 30
	defaultGippityToolMaxIterations = 3
	// defaultGippityClassifierRateLimit mirrors the gippity module's own
	// fallback. Applying it here keeps a merged settings validation, the module
	// and /status agreeing on the effective limit instead of each having its own
	// "unset means 30" rule.
	defaultGippityClassifierRateLimit = 30
	// defaultLLMMaxTokens caps one LLM completion. It mirrors llm.DefaultMaxTokens;
	// cfg is a leaf package and does not import llm, so the two constants are kept
	// equal by TestDefaultLLMMaxTokensMatchesLLM.
	defaultLLMMaxTokens = 300
)

// validReasoningEfforts are the reasoning_effort values accepted from config.
// The empty string omits the field so the provider default applies.
var validReasoningEfforts = map[string]bool{
	"":        true,
	"none":    true,
	"minimal": true,
	"low":     true,
	"medium":  true,
	"high":    true,
	"xhigh":   true,
	"max":     true,
}

// LeetoclockEmojisConfig holds optional custom Discord emoji IDs used as
// reactions during a game. Each value is a guild emoji ID; when omitted the
// module falls back to a standard Unicode equivalent.
type LeetoclockEmojisConfig struct {
	Zonk      string `yaml:"zonk,omitempty"`
	Lol       string `yaml:"lol,omitempty"`
	NotAmused string `yaml:"notamused,omitempty"`
	Wat       string `yaml:"wat,omitempty"`
}

// LeetoclockConfig configures the daily Leet o'Clock game module.
type LeetoclockConfig struct {
	// AnnouncementChannels receives the daily preparation announcement.
	AnnouncementChannels []string `yaml:"announcement_channels,omitempty"`
	// DebugChannel is an extra channel that receives announcements while
	// Debug is enabled. Empty in production.
	DebugChannel string `yaml:"debug_channel,omitempty"`
	// Debug runs the game one minute after start with a fast tick loop.
	Debug bool `yaml:"debug,omitempty"`
	// Emojis overrides the reaction emojis with custom guild emoji IDs.
	Emojis LeetoclockEmojisConfig `yaml:"emojis,omitempty"`
}

// EsoAPIConfig tunes the guards on the unauthenticated, LLM-billed /api/eso
// endpoint. Every field is optional: an omitted or non-positive value keeps the
// built-in default, which the web server applies in resolveEsoLimits.
type EsoAPIConfig struct {
	// RateLimitBurst is how many requests one client address may issue back to
	// back. Default 5.
	RateLimitBurst int `yaml:"rate_limit_burst,omitempty"`
	// RateLimitPerMinute is the sustained per-client refill rate. Default 30.
	RateLimitPerMinute int `yaml:"rate_limit_per_minute,omitempty"`
	// MaxConcurrentRequests caps simultaneous generations. Default 4.
	MaxConcurrentRequests int `yaml:"max_concurrent_requests,omitempty"`
	// RequestTimeoutSeconds bounds one generation. Default 15.
	RequestTimeoutSeconds int `yaml:"request_timeout_seconds,omitempty"`
}

// TriviaConfig tunes the trivia module's random drop-ins.
type TriviaConfig struct {
	// DropinChance is the probability (0..1) that a counted message triggers
	// a drop-in. Default 0.01; 0 disables drop-ins (counting continues).
	DropinChance float64 `yaml:"dropin_chance"`
	// DropinCooldown is the minimum Go duration between two drop-ins in one
	// guild. Default 24h.
	DropinCooldown string `yaml:"dropin_cooldown,omitempty"`
}

// Defaults for the trivia section.
const (
	defaultTriviaDropinChance   = 0.01
	defaultTriviaDropinCooldown = "24h"
)

// TriviaDropinCooldown returns the parsed drop-in cooldown, falling back to
// the default for an empty or unparsable value (Validate rejects those).
func (c *Config) TriviaDropinCooldown() time.Duration {
	d, err := time.ParseDuration(c.Trivia.DropinCooldown)
	if err != nil || d <= 0 {
		d, _ = time.ParseDuration(defaultTriviaDropinCooldown)
	}
	return d
}

// ValidateTriviaDropinCooldown accepts an empty value (the default applies)
// or a positive Go duration.
func ValidateTriviaDropinCooldown(value string) error {
	if value == "" {
		return nil
	}
	if d, err := time.ParseDuration(value); err != nil || d <= 0 {
		return errors.New("must be a positive duration such as 24h")
	}
	return nil
}

// Config struct with all parameters
type Config struct {
	Discord struct {
		Token      string `yaml:"token"`
		OwnerID    string `yaml:"owner_id,omitempty"`
		ShardID    int    `yaml:"shard_id,omitempty" default:"0"`
		ShardCount int    `yaml:"shard_count,omitempty" default:"0"`
	} `yaml:"discord"`
	Web struct {
		Oauth struct {
			ClientID     string `yaml:"client_id"`
			ClientSecret string `yaml:"client_secret"`
			RedirectURI  string `yaml:"redirect_uri"`
		} `yaml:"oauth"`
		SessionSecret string `yaml:"session_secret"`
		Port          int    `yaml:"port,omitempty" default:"8080"`
		// Eso optionally overrides the /api/eso rate and concurrency limits.
		Eso EsoAPIConfig `yaml:"eso,omitempty"`
	} `yaml:"web"`
	// Database is deprecated and retained only to warn about the removed
	// database.path option. It no longer selects the file location: the shared
	// database is always shareddb.Filename in the working directory.
	Database struct {
		Path string `yaml:"path,omitempty"`
	} `yaml:"database,omitempty"`
	Gippity struct {
		AllowedGuilds            []string `yaml:"allowed_guilds"`
		IgnoredUsers             []string `yaml:"ignored_users"`
		RateLimitMessagesPerHour int      `yaml:"rate_limit_messages_per_hour,omitempty"`
		// ReplyInThread answers mentions inside a public thread started on the
		// mentioning message instead of writing into the channel itself.
		ReplyInThread bool `yaml:"reply_in_thread,omitempty"`
		// RespondToAllMessages lets the classifier decide whether to answer
		// messages that do not mention the bot. Default false keeps the
		// mention-only behaviour.
		RespondToAllMessages bool `yaml:"respond_to_all_messages,omitempty"`
		// ClassifierModel selects the model for that decision. Empty falls back
		// to llm.model.
		ClassifierModel string `yaml:"classifier_model,omitempty"`
		// ClassifierRateLimitPerHour caps classifier calls per user per hour.
		// Defaults to 30 when omitted.
		ClassifierRateLimitPerHour int `yaml:"classifier_rate_limit_per_hour,omitempty"`
		// ClassifierNames lists extra names a message may use to address the
		// bot. The bot's own Discord username and display name are always
		// included, so this is only needed for nicknames or aliases.
		ClassifierNames []string `yaml:"classifier_names,omitempty"`
		// DebugChannel restricts the bot to one channel while debug logging is
		// enabled. Empty means no restriction.
		DebugChannel string `yaml:"debug_channel,omitempty"`
		// Tools configures the LLM tool-calling loop that lets the answer path
		// consult internal data (for example chat history). Off by default:
		// when disabled the answer path is a single completion call.
		Tools struct {
			Enabled bool `yaml:"enabled,omitempty"`
			// MaxIterations caps tool-call rounds before the loop forces a
			// final answer. Defaults to 3 when omitted.
			MaxIterations int `yaml:"max_iterations,omitempty"`
			// Allowed optionally restricts which registered tools are offered
			// to the model. Empty means all registered tools.
			Allowed []string `yaml:"allowed,omitempty"`
			// ReasoningEffort is sent only on requests that carry tools, and is
			// cleared for the tool-free final call so that call may reason
			// again. Empty (default) omits the field; some providers forbid
			// tools together with reasoning and require "none" here.
			ReasoningEffort string `yaml:"reasoning_effort,omitempty"`
		} `yaml:"tools,omitempty"`
	} `yaml:"gippity"`
	Soundboard struct {
		QueueMaxDepth int `yaml:"queue_max_depth,omitempty"`
	} `yaml:"soundboard,omitempty"`
	Leetoclock LeetoclockConfig `yaml:"leetoclock,omitempty"`
	LLM        struct {
		Provider    string `yaml:"provider,omitempty"`
		Model       string `yaml:"model,omitempty"`
		VisionModel string `yaml:"vision_model,omitempty"`
		BaseURL     string `yaml:"base_url,omitempty"`
		HTTPReferer string `yaml:"http_referer,omitempty"`
		Title       string `yaml:"title,omitempty"`
		// APIKey is the provider credential. Optional: the provider's environment
		// variable (OPENAI_API_KEY / OPENROUTER_API_KEY) takes precedence when set.
		APIKey string `yaml:"api_key,omitempty"`
		// Personality is a custom persona string. When set it takes precedence
		// over Preset and the built-in default.
		Personality string `yaml:"personality,omitempty"`
		// Preset selects one of the predefined personas (see llm.PersonalityPresets).
		// Used only when Personality is empty. Unknown values fall back to the default.
		Preset string `yaml:"personality_preset,omitempty"`
		// MaxTokens caps one completion. It applies to the shared client, the
		// gippity answer path and the vision descriptions; the non-mention
		// classifier keeps its own much smaller bound. Defaults to 300.
		MaxTokens int `yaml:"max_tokens,omitempty"`
	} `yaml:"llm,omitempty"`
	// TMDB enables the optional /wheretostream module. An empty
	// ReadAccessToken disables it; a token change needs a restart.
	TMDB struct {
		// ReadAccessToken is TMDB's "API Read Access Token" (Bearer). Secret.
		ReadAccessToken string `yaml:"read_access_token,omitempty"`
		// Region is the default ISO 3166-1 country. Empty means DefaultTMDBRegion.
		Region string `yaml:"region,omitempty"`
		// Language is the default TMDB language. Empty means DefaultTMDBLanguage.
		Language string `yaml:"language,omitempty"`
	} `yaml:"tmdb,omitempty"`
	// TV enables the optional /tv module and the tv_program LLM tool, backed
	// by the free-epg.de German XMLTV feed. Off by default; changes need a
	// restart.
	TV TVConfig `yaml:"tv,omitempty"`
	// Trivia tunes the random chat-stat drop-ins. Changes need a restart.
	Trivia  TriviaConfig `yaml:"trivia,omitempty"`
	DevMode bool         `yaml:"dev_mode,omitempty" default:"false"`
	// Logs sizes the in-memory log window behind /admin logs show and the
	// /admin/logs web page. A change needs a restart.
	Logs struct {
		// WindowLines is how many recent log records are kept. Zero or absent
		// means DefaultLogWindowLines.
		WindowLines int `yaml:"window_lines,omitempty"`
	} `yaml:"logs,omitempty"`
}

// TVConfig configures the optional tv module. Every field has a safe zero
// value: disabled, the built-in channel list, DefaultTVCacheDir and
// DefaultTVRefreshInterval.
type TVConfig struct {
	Enabled bool `yaml:"enabled,omitempty"`
	// Regions lists free-epg.de country feeds (two-letter lower-case codes).
	// Empty means DefaultTVRegion only.
	Regions []string `yaml:"regions,omitempty"`
	// Channels replaces the built-in XMLTV channel ID list when non-empty. It
	// is required when Regions holds anything other than DefaultTVRegion,
	// because the built-in list only covers German channels.
	Channels []string `yaml:"channels,omitempty"`
	// Timezone is the IANA zone used to show times and day boundaries. Empty
	// means DefaultTVTimezone.
	Timezone string `yaml:"timezone,omitempty"`
	// CacheDir holds the downloaded feed. Empty means DefaultTVCacheDir.
	CacheDir string `yaml:"cache_dir,omitempty"`
	// RefreshInterval is the conditional-fetch period. Zero means
	// DefaultTVRefreshInterval; a set value must be at least MinTVRefreshInterval.
	RefreshInterval time.Duration `yaml:"refresh_interval,omitempty"`
}

// DefaultTVRegion is the feed region when tv.regions is unset.
const DefaultTVRegion = "de"

// DefaultTVTimezone is the display zone when tv.timezone is unset.
const DefaultTVTimezone = "Europe/Berlin"

var tvRegionPattern = regexp.MustCompile(`^[a-z]{2}$`)

// TVRegions returns tv.regions or a list holding DefaultTVRegion.
func (c *Config) TVRegions() []string {
	if len(c.TV.Regions) == 0 {
		return []string{DefaultTVRegion}
	}
	return c.TV.Regions
}

// TVTimezone returns tv.timezone or DefaultTVTimezone.
func (c *Config) TVTimezone() string {
	if c.TV.Timezone == "" {
		return DefaultTVTimezone
	}
	return c.TV.Timezone
}

// validateTV checks the tv section; it is only called when tv is enabled.
func validateTV(c *Config) error {
	if c.TV.RefreshInterval != 0 && c.TV.RefreshInterval < MinTVRefreshInterval {
		return fmt.Errorf("tv.refresh_interval must be at least %s when tv.enabled is true", MinTVRefreshInterval)
	}
	seen := map[string]bool{}
	for _, r := range c.TV.Regions {
		if !tvRegionPattern.MatchString(r) {
			return fmt.Errorf("tv.regions entry %q must be a two-letter lower-case country code like de", r)
		}
		if seen[r] {
			return fmt.Errorf("tv.regions lists %q twice", r)
		}
		seen[r] = true
	}
	if len(c.TV.Channels) == 0 {
		for _, r := range c.TVRegions() {
			if r != DefaultTVRegion {
				return fmt.Errorf("tv.channels is required when tv.regions includes %q: the built-in channel list only covers %q", r, DefaultTVRegion)
			}
		}
	}
	if _, err := time.LoadLocation(c.TVTimezone()); err != nil {
		return fmt.Errorf("tv.timezone %q is not a known IANA time zone: %w", c.TV.Timezone, err)
	}
	return nil
}

// DefaultTVCacheDir is the feed cache directory when tv.cache_dir is unset.
const DefaultTVCacheDir = "cache"

// DefaultTVRefreshInterval is the fetch period when tv.refresh_interval is unset.
const DefaultTVRefreshInterval = 6 * time.Hour

// MinTVRefreshInterval is the shortest accepted tv.refresh_interval.
const MinTVRefreshInterval = time.Hour

// TVCacheDir returns tv.cache_dir or DefaultTVCacheDir.
func (c *Config) TVCacheDir() string {
	if c.TV.CacheDir == "" {
		return DefaultTVCacheDir
	}
	return c.TV.CacheDir
}

// TVRefreshInterval returns tv.refresh_interval or DefaultTVRefreshInterval.
func (c *Config) TVRefreshInterval() time.Duration {
	if c.TV.RefreshInterval == 0 {
		return DefaultTVRefreshInterval
	}
	return c.TV.RefreshInterval
}

// DefaultTMDBRegion is the watch-provider region when tmdb.region is unset.
const DefaultTMDBRegion = "DE"

// DefaultTMDBLanguage is the TMDB language when tmdb.language is unset.
const DefaultTMDBLanguage = "de-DE"

var (
	tmdbRegionPattern   = regexp.MustCompile(`^[A-Z]{2}$`)
	tmdbLanguagePattern = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)
)

// ValidateTMDBRegion rejects a non-empty value that is not a two-letter
// uppercase ISO 3166-1 code.
func ValidateTMDBRegion(value string) error {
	if value == "" || tmdbRegionPattern.MatchString(value) {
		return nil
	}
	return fmt.Errorf("must be a two-letter uppercase country code (e.g. DE), got %q", value)
}

// ValidateTMDBLanguage rejects a non-empty value that is not like "de" or "de-DE".
func ValidateTMDBLanguage(value string) error {
	if value == "" || tmdbLanguagePattern.MatchString(value) {
		return nil
	}
	return fmt.Errorf("must look like de or de-DE, got %q", value)
}

// TMDBRegion returns tmdb.region or DefaultTMDBRegion.
func (c *Config) TMDBRegion() string {
	if c.TMDB.Region == "" {
		return DefaultTMDBRegion
	}
	return c.TMDB.Region
}

// TMDBLanguage returns tmdb.language or DefaultTMDBLanguage.
func (c *Config) TMDBLanguage() string {
	if c.TMDB.Language == "" {
		return DefaultTMDBLanguage
	}
	return c.TMDB.Language
}

// DefaultLogWindowLines is the log window size when logs.window_lines is unset.
const DefaultLogWindowLines = 2000

// maxLogWindowLines bounds logs.window_lines so a typo cannot exhaust memory.
const maxLogWindowLines = 100000

// LogWindowLines returns the configured log window size, or
// DefaultLogWindowLines when unset.
func (c *Config) LogWindowLines() int {
	if c.Logs.WindowLines == 0 {
		return DefaultLogWindowLines
	}
	return c.Logs.WindowLines
}

var initializedConfig *Config

// GetConfig returns the config struct
func GetConfig() *Config {
	if initializedConfig == nil {
		initializedConfig = loadFile()
	}
	return initializedConfig
}

func loadFile() *Config {
	configFile, err := os.Open("config.yaml")
	if err != nil {
		slog.Error("Could not load config file.", "error", err)
		os.Exit(1)
	}
	defer func() { _ = configFile.Close() }()

	cfg, err := decodeConfig(configFile)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
	initializedConfig = cfg
	return initializedConfig
}

// Decode reads YAML from r into a Config and applies the optional-field
// defaults. It deliberately does not validate: call Validate on the result.
// Keeping decode and validate separate lets a runtime settings write re-check a
// merged config with exactly the rules the boot path uses, instead of growing a
// second set of rules.
func Decode(r io.Reader) (*Config, error) {
	cfg := Config{}
	cfg.Gippity.RateLimitMessagesPerHour = defaultGippityRateLimitPerHour
	cfg.Gippity.ClassifierRateLimitPerHour = defaultGippityClassifierRateLimit
	cfg.Gippity.Tools.MaxIterations = defaultGippityToolMaxIterations
	cfg.LLM.MaxTokens = defaultLLMMaxTokens
	cfg.Soundboard.QueueMaxDepth = defaultSoundboardQueueMaxDepth
	cfg.Trivia.DropinChance = defaultTriviaDropinChance
	cfg.Trivia.DropinCooldown = defaultTriviaDropinCooldown
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, errors.New("could not decode config: " + err.Error())
	}
	return &cfg, nil
}

// minSessionSecretLen is the shortest web.session_secret accepted without a
// warning; the cookie key is sha256(secret) with no KDF.
const minSessionSecretLen = 32

// Validate rejects a Config that cannot run: the required fields plus the
// positive-value and enum rules for the optional tuning fields.
func Validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if cfg.Discord.Token == "" {
		return errors.New("discord.token is required but not set in config.yaml")
	}
	if cfg.Web.Port != 0 && cfg.Web.SessionSecret == "" {
		return errors.New("web.session_secret is required when web.port is set")
	}
	if cfg.Web.Port != 0 && len(cfg.Web.SessionSecret) < minSessionSecretLen {
		slog.Warn("web.session_secret is short and can be brute-forced; generate one with: openssl rand -base64 32", "length", len(cfg.Web.SessionSecret), "minimum", minSessionSecretLen)
	}
	if len(cfg.Gippity.AllowedGuilds) == 0 {
		return errors.New("gippity.allowed_guilds is required and cannot be empty")
	}
	if cfg.Gippity.RateLimitMessagesPerHour <= 0 {
		return errors.New("gippity.rate_limit_messages_per_hour must be greater than zero")
	}
	if cfg.Gippity.ClassifierRateLimitPerHour <= 0 {
		return errors.New("gippity.classifier_rate_limit_per_hour must be greater than zero")
	}
	if cfg.Gippity.Tools.Enabled && cfg.Gippity.Tools.MaxIterations <= 0 {
		return errors.New("gippity.tools.max_iterations must be greater than zero when gippity.tools.enabled is true")
	}
	if err := ValidateReasoningEffort(cfg.Gippity.Tools.ReasoningEffort); err != nil {
		return fmt.Errorf("gippity.tools.reasoning_effort %w", err)
	}
	if cfg.Soundboard.QueueMaxDepth <= 0 {
		return errors.New("soundboard.queue_max_depth must be greater than zero")
	}
	if cfg.LLM.MaxTokens <= 0 {
		return errors.New("llm.max_tokens must be greater than zero")
	}
	if cfg.Logs.WindowLines < 0 || cfg.Logs.WindowLines > maxLogWindowLines {
		return fmt.Errorf("logs.window_lines must be between 0 and %d", maxLogWindowLines)
	}
	if err := ValidateTMDBRegion(cfg.TMDB.Region); err != nil {
		return fmt.Errorf("tmdb.region %w", err)
	}
	if err := ValidateTMDBLanguage(cfg.TMDB.Language); err != nil {
		return fmt.Errorf("tmdb.language %w", err)
	}
	if cfg.Trivia.DropinChance < 0 || cfg.Trivia.DropinChance > 1 {
		return errors.New("trivia.dropin_chance must be between 0 and 1")
	}
	if err := ValidateTriviaDropinCooldown(cfg.Trivia.DropinCooldown); err != nil {
		return errors.New("trivia.dropin_cooldown must be a positive duration such as 24h")
	}
	if cfg.TV.Enabled {
		if err := validateTV(cfg); err != nil {
			return err
		}
	}
	return nil
}

// ValidateReasoningEffort rejects a reasoning_effort value no provider accepts.
// It is exported so the runtime settings registry can judge a single stored row
// with the same rule instead of keeping a second copy of the list.
func ValidateReasoningEffort(value string) error {
	if validReasoningEfforts[value] {
		return nil
	}
	return errors.New("must be one of: none, minimal, low, medium, high, xhigh, max (or empty to omit)")
}

// decodeConfig decodes YAML from r into a Config and validates required fields.
func decodeConfig(r io.Reader) (*Config, error) {
	cfg, err := Decode(r)
	if err != nil {
		return nil, err
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
