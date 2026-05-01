package cfg

import (
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
	}
	defer configFile.Close()

	configDecoder := yaml.NewDecoder(configFile)

	if err := configDecoder.Decode(&initializedConfig); err != nil {
		slog.Error("could not decode config.", "error", err)
	}

	return &initializedConfig
}
