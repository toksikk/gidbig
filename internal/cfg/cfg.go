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
		Port int `yaml:"port,omitempty" default:"8080"`
	} `yaml:"web"`
	DevMode bool `yaml:"dev_mode,omitempty" default:"false"`
}

var initializedConfig Config = Config{}

// GetConfig returns the config struct
func GetConfig() *Config {
	if initializedConfig == (Config{}) {
		initializedConfig = *loadFile()
	}
	return &initializedConfig
}

func loadFile() *Config {
	configFile, err := os.Open("config.yaml")
	if err != nil {
		slog.Error("Could not load config file.", "error", err)
		os.Exit(1)
	}
	defer configFile.Close()

	cfg, err := decodeConfig(configFile)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
	initializedConfig = *cfg
	return &initializedConfig
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
	return &cfg, nil
}
