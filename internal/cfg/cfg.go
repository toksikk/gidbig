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

const s = "config.yaml"

// LoadConfigFile config.yaml and creates a Config struct
func LoadConfigFile() *Config {
	return loadFile(s)
}

func loadFile(cf string) *Config {
	config := &Config{}
	configFile, err := os.Open(cf)
	if err != nil {
		slog.Warn("Could not load config file.", "error", err)
	}
	defer configFile.Close()

	d := yaml.NewDecoder(configFile)

	if err := d.Decode(&config); err != nil {
		slog.Error("could not decode config.", "error", err)
	}

	return config
}
