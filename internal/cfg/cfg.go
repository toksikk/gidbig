package cfg

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

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
	DevMode bool `yaml:"dev_mode,omitempty" default:"false"`
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
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, errors.New("could not decode config: " + err.Error())
	}
	return &cfg, nil
}

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
