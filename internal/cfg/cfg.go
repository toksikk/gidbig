package cfg

import (
	"errors"
	"io"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

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
		AllowedGuilds []string `yaml:"allowed_guilds"`
		IgnoredUsers  []string `yaml:"ignored_users"`
	} `yaml:"gippity"`
	LLM struct {
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
	var cfg Config
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
	return &cfg, nil
}
