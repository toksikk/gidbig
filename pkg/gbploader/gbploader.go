package gbploader

import (
	"path/filepath"
	"plugin"

	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
)

var pluginStarter interface { // nolint:unused
	Start(*discordgo.Session)
}

var loadedPlugins map[string][2]string

// GetLoadedPlugins returns loaded plugins as string array
func GetLoadedPlugins() *map[string][2]string {
	return &loadedPlugins
}

// LoadPlugins from plugins directory
func LoadPlugins(discord *discordgo.Session) {
	plugins, err := filepath.Glob("./plugins/*.so")
	if err != nil {
		log.Warn(err)
	}

	loadedPlugins = make(map[string][2]string)

	for _, v := range plugins {
		plugin, err := plugin.Open(v)
		if err != nil {
			log.Warn(err)
			continue
		}

		startFunc, err := plugin.Lookup("Start")
		if err != nil {
			log.Warn(err)
			continue
		}

		pluginName, err := plugin.Lookup("PluginName")
		if err != nil {
			log.Warn(err)
			continue
		}

		pluginVersion, err := plugin.Lookup("PluginVersion")
		if err != nil {
			log.Warn(err)
			continue
		}
		pluginBuilddate, err := plugin.Lookup("PluginBuilddate")
		if err != nil {
			log.Warn(err)
			continue
		}

		log.Infof("Loading plugin %s %s (%s)...", *pluginName.(*string), *pluginVersion.(*string), *pluginBuilddate.(*string))

		loadedPlugins[*pluginName.(*string)] = [2]string{*pluginVersion.(*string), *pluginBuilddate.(*string)}

		startFunc.(func(*discordgo.Session))(discord)
	}
}
