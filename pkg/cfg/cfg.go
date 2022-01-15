package cfg

import (
	"os"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Token       string `yaml:"token"`
	Shard       string `yaml:"shard"`
	ShardCount  string `yaml:"shardcount"`
	Owner       string `yaml:"owner"`
	Port        int    `yaml:"port"`
	RedirectURL string `yaml:"redirecturl"`
	Ci          int    `yaml:"ci"`
	Cs          string `yaml:"cs"`
}

func LoadConfigFile() Config {
	config := Config{}
	configFile, err := os.Open("config.yaml")
	if err != nil {
		log.Warningln("Could not load config file.", err)
	}
	defer configFile.Close()

	d := yaml.NewDecoder(configFile)

	if err := d.Decode(&config); err != nil {
	}

	return config
}
