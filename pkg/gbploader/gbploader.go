package gbploader

import (
	"log/slog"
	"path/filepath"
	"plugin"
	"runtime/debug"

	"github.com/bwmarrin/discordgo"
	gbp_coffee "github.com/toksikk/gbp-coffee/plugin"
	gbp_eso "github.com/toksikk/gbp-eso/plugin"
	gbp_gamerstatus "github.com/toksikk/gbp-gamerstatus/plugin"
	gbp_leetoclock "github.com/toksikk/gbp-leetoclock/plugin"
	gbp_stoll "github.com/toksikk/gbp-stoll/plugin"
	gbp_wttrin "github.com/toksikk/gbp-wttrin/plugin"
)

var pluginStarter interface { // nolint:unused
	Start(*discordgo.Session)
}

var loadedPlugins map[string][2]string

// GetLoadedPlugins returns loaded plugins as string array
func GetLoadedPlugins() *map[string][2]string {
	return &loadedPlugins
}

func loadLibraryPlugins(discord *discordgo.Session) {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		slog.Warn("Could not read build info.")
	}

	if ok {
		for _, dep := range buildInfo.Deps {
			if dep.Path == "github.com/toksikk/gbp-coffee" {
				slog.Info("Loading built-in plugin.", "plugin", gbp_coffee.PluginName, "version", dep.Version)
				addPluginToLoadedPlugins(gbp_coffee.PluginName, dep.Version, "compiled into Gidbig")
			}
			if dep.Path == "github.com/toksikk/gbp-gamerstatus" {
				slog.Info("Loading built-in plugin.", "plugin", gbp_gamerstatus.PluginName, "version", dep.Version)
				addPluginToLoadedPlugins(gbp_gamerstatus.PluginName, dep.Version, "compiled into Gidbig")
			}
			if dep.Path == "github.com/toksikk/gbp-wttrin" {
				slog.Info("Loading built-in plugin.", "plugin", gbp_wttrin.PluginName, "version", dep.Version)
				addPluginToLoadedPlugins(gbp_wttrin.PluginName, dep.Version, "compiled into Gidbig")
			}
			if dep.Path == "github.com/toksikk/gbp-leetoclock" {
				slog.Info("Loading built-in plugin.", "plugin", gbp_leetoclock.PluginName, "version", dep.Version)
				addPluginToLoadedPlugins(gbp_leetoclock.PluginName, dep.Version, "compiled into Gidbig")
			}
			if dep.Path == "github.com/toksikk/gbp-eso" {
				slog.Info("Loading built-in plugin.", "plugin", gbp_eso.PluginName, "version", dep.Version)
				addPluginToLoadedPlugins(gbp_eso.PluginName, dep.Version, "compiled into Gidbig")
			}
			if dep.Path == "github.com/toksikk/gbp-stoll" {
				slog.Info("Loading built-in plugin.", "plugin", gbp_stoll.PluginName, "version", dep.Version)
				addPluginToLoadedPlugins(gbp_stoll.PluginName, dep.Version, "compiled into Gidbig")
			}
		}
	} else {
		addPluginToLoadedPlugins(gbp_coffee.PluginName, "unknown version", "compiled into Gidbig")
		addPluginToLoadedPlugins(gbp_gamerstatus.PluginName, "unknown version", "compiled into Gidbig")
		addPluginToLoadedPlugins(gbp_wttrin.PluginName, "unknown version", "compiled into Gidbig")
		addPluginToLoadedPlugins(gbp_leetoclock.PluginName, "unknown version", "compiled into Gidbig")
		addPluginToLoadedPlugins(gbp_eso.PluginName, "unknown version", "compiled into Gidbig")
		addPluginToLoadedPlugins(gbp_stoll.PluginName, "unknown version", "compiled into Gidbig")
	}

	gbp_coffee.Start(discord)
	gbp_gamerstatus.Start(discord)
	gbp_wttrin.Start(discord)
	gbp_leetoclock.Start(discord)
	gbp_eso.Start(discord)
	gbp_stoll.Start(discord)
}

func addPluginToLoadedPlugins(pluginName string, pluginVersion string, pluginBuilddate string) {
	loadedPlugins[pluginName] = [2]string{pluginVersion, pluginBuilddate}
}

// LoadPlugins from plugins directory
func loadBinaryPlugins(discord *discordgo.Session) {
	plugins, err := filepath.Glob("./plugins/*.so")
	if err != nil {
		slog.Warn("Could not load plugins.", "error", err)
		return
	}

	for _, v := range plugins {
		plugin, err := plugin.Open(v)
		if err != nil {
			slog.Warn("Could not open plugin or plugin was already loaded as built-in.", "plugin", plugin, "error", err)
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

// LoadPlugins from deps and from plugins directory
func LoadPlugins(discord *discordgo.Session) {
	loadedPlugins = make(map[string][2]string)
	loadLibraryPlugins(discord)
	loadBinaryPlugins(discord)
}
