package gbploader

import (
	"path/filepath"
	"plugin"

	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
)

var pluginStarter interface {
	Start(*discordgo.Session)
}

func LoadPlugins(discord *discordgo.Session) {
	plugins, err := filepath.Glob("./plugins/*.so")
	if err != nil {
		log.Warn(err)
	}

	for _, v := range plugins {
		plugin, err := plugin.Open(v)
		if err != nil {
			log.Warn(err)
			break
		}

		startFunc, err := plugin.Lookup("Start")
		if err != nil {
			log.Warn(err)
			break
		}

		pluginName, err := plugin.Lookup("PluginName")
		if err != nil {
			log.Warn(err)
			break
		}

		pluginVersion, err := plugin.Lookup("PluginVersion")
		if err != nil {
			log.Warn(err)
			break
		}

		log.Infof("Loading plugin %s %s...", *pluginName.(*string), *pluginVersion.(*string))

		startFunc.(func(*discordgo.Session))(discord)
	}
}
