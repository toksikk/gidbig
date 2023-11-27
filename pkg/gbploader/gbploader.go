package gbploader

import (
	"log/slog"
	"path/filepath"
	"plugin"

	"github.com/bwmarrin/discordgo"
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
		slog.Warn("Could not load plugins.", "error", err)
		return
	}

	loadedPlugins = make(map[string][2]string)

	for _, v := range plugins {
		plugin, err := plugin.Open(v)
		if err != nil {
			slog.Warn("Could not open plugin.", "plugin", plugin, "error", err)
			continue
		}

		startFunc, err := plugin.Lookup("Start")
		if err != nil {
			slog.Warn("Could not find Start function in plugin.", "plugin", plugin, "error", err)
			continue
		}

		pluginName, err := plugin.Lookup("PluginName")
		if err != nil {
			slog.Warn("Could not find PluginName in plugin.", "plugin", plugin, "error", err)
			continue
		}

		pluginVersion, err := plugin.Lookup("PluginVersion")
		if err != nil {
			slog.Warn("Could not find PluginVersion in plugin.", "plugin", plugin, "error", err)
			continue
		}
		pluginBuilddate, err := plugin.Lookup("PluginBuilddate")
		if err != nil {
			slog.Warn("Could not find PluginBuilddate in plugin.", "plugin", plugin, "error", err)
			continue
		}

		slog.Info("Loading plugin.", "plugin", *pluginName.(*string), "version", *pluginVersion.(*string), "builddate", *pluginBuilddate.(*string))

		loadedPlugins[*pluginName.(*string)] = [2]string{*pluginVersion.(*string), *pluginBuilddate.(*string)}

		startFunc.(func(*discordgo.Session))(discord)
	}
}
