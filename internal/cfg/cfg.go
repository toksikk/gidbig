package cfg

import (
	"errors"
	"io"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

// Defaults applied to optional operational tuning fields when absent from
// config.yaml, preserving legacy behavior.
const (
	defaultSoundboardQueueMaxDepth = 6
	defaultGippityRateLimitPerHour = 30
)

// LeetoclockConfig configures the daily Leet o'Clock game module.
type LeetoclockConfig struct {
	// AnnouncementChannels receives the daily preparation announcement.
	AnnouncementChannels []string `yaml:"announcement_channels,omitempty"`
	// DebugChannel is an extra channel that receives announcements while
	// Debug is enabled. Empty in production.
	DebugChannel string `yaml:"debug_channel,omitempty"`
	// Debug runs the game one minute after start with a fast tick loop.
	Debug bool `yaml:"debug,omitempty"`
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
	} `yaml:"web"`
	Database struct {
		Path string `yaml:"path,omitempty"`
	} `yaml:"database,omitempty"`
	Gippity struct {
		AllowedGuilds            []string `yaml:"allowed_guilds"`
		IgnoredUsers             []string `yaml:"ignored_users"`
		RateLimitMessagesPerHour int      `yaml:"rate_limit_messages_per_hour,omitempty"`
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
		// Personality is a custom persona string. When set it takes precedence
		// over Preset and the built-in default.
		Personality string `yaml:"personality,omitempty"`
		// Preset selects one of the predefined personas (see llm.PersonalityPresets).
		// Used only when Personality is empty. Unknown values fall back to the default.
		Preset string `yaml:"personality_preset,omitempty"`
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

// decodeConfig decodes YAML from r into a Config and validates required fields.
func decodeConfig(r io.Reader) (*Config, error) {
	cfg := Config{}
	cfg.Gippity.RateLimitMessagesPerHour = defaultGippityRateLimitPerHour
	cfg.Soundboard.QueueMaxDepth = defaultSoundboardQueueMaxDepth
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, errors.New("could not decode config: " + err.Error())
	}
	if cfg.Discord.Token == "" {
		return nil, errors.New("discord.token is required but not set in config.yaml")
	}
	if cfg.Web.Port != 0 && cfg.Web.SessionSecret == "" {
		return nil, errors.New("web.session_secret is required when web.port is set")
	}
	if len(cfg.Gippity.AllowedGuilds) == 0 {
		return nil, errors.New("gippity.allowed_guilds is required and cannot be empty")
	}
	if cfg.Gippity.RateLimitMessagesPerHour <= 0 {
		return nil, errors.New("gippity.rate_limit_messages_per_hour must be greater than zero")
	}
	if cfg.Soundboard.QueueMaxDepth <= 0 {
		return nil, errors.New("soundboard.queue_max_depth must be greater than zero")
	}
	return &cfg, nil
}
