package gbploader

import (
	"log/slog"
	"path/filepath"
	"plugin"
	"runtime/debug"

	"github.com/bwmarrin/discordgo"
	// gbp_coffee "github.com/toksikk/gbp-coffee/plugin"
	// gbp_eso "github.com/toksikk/gbp-eso/plugin"
	// gbp_gamerstatus "github.com/toksikk/gbp-gamerstatus/plugin"
	// gbp_leetoclock "github.com/toksikk/gbp-leetoclock/plugin"
	// gbp_stoll "github.com/toksikk/gbp-stoll/plugin"
	// gbp_wttrin "github.com/toksikk/gbp-wttrin/plugin"
)

var pluginStarter interface { // nolint:unused
	Start(*discordgo.Session)
}

var loadedPlugins map[string][2]string

// GetLoadedPlugins returns loaded plugins as string array
func GetLoadedPlugins() *map[string][2]string {
	return &loadedPlugins
}

// nolint:unused
func loadLibraryPlugin(pluginImportPath string, pluginName string, pluginStartFunction *func(discord *discordgo.Session), discord *discordgo.Session, buildInfo *debug.BuildInfo) {
	if buildInfo != nil {
		for _, dep := range buildInfo.Deps {
			if dep.Path == pluginImportPath {
				if _, alreadyLoaded := loadedPlugins[pluginName]; alreadyLoaded {
					slog.Info("Plugin already loaded. Skipping...", "plugin", pluginName)
					continue
				}
				slog.Info("Loading built-in plugin.", "plugin", pluginName, "version", dep.Version)
				addPluginToLoadedPlugins(pluginName, dep.Version, "compiled into Gidbig")
			}
		}
	} else {
		if _, alreadyLoaded := loadedPlugins[pluginName]; alreadyLoaded {
			slog.Info("Loading built-in plugin.", "plugin", pluginName, "version", "unknown version")
			addPluginToLoadedPlugins(pluginName, "unknown version", "compiled into Gidbig")
		}
	}
	(*pluginStartFunction)(discord)
	slog.Info("Loaded built-in plugin.", "plugin", pluginName)
}

func loadLibraryPlugins(discord *discordgo.Session) {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		slog.Warn("Could not read build info.")
	}

	slog.Info("Loading built-in plugins...", "build", buildInfo)

	// coffeeStart := gbp_coffee.Start
	// loadLibraryPlugin("github.com/toksikk/gbp-coffee", gbp_coffee.PluginName, &coffeeStart, discord, buildInfo)
	// gamerstatusStart := gbp_gamerstatus.Start
	// loadLibraryPlugin("github.com/toksikk/gbp-gamerstatus", gbp_gamerstatus.PluginName, &gamerstatusStart, discord, buildInfo)
	// leetoclockStart := gbp_leetoclock.Start
	// loadLibraryPlugin("github.com/toksikk/gbp-leetoclock", gbp_leetoclock.PluginName, &leetoclockStart, discord, buildInfo)
	// stollStart := gbp_stoll.Start
	// loadLibraryPlugin("github.com/toksikk/gbp-stoll", gbp_stoll.PluginName, &stollStart, discord, buildInfo)
	// wttrinStart := gbp_wttrin.Start
	// loadLibraryPlugin("github.com/toksikk/gbp-wttrin", gbp_wttrin.PluginName, &wttrinStart, discord, buildInfo)
	// esoStart := gbp_eso.Start
	// loadLibraryPlugin("github.com/toksikk/gbp-eso", gbp_eso.PluginName, &esoStart, discord, buildInfo)
}

// nolint:unused
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

// LoadPlugins from deps and from plugins directory
func LoadPlugins(discord *discordgo.Session) {
	loadedPlugins = make(map[string][2]string)
	loadBinaryPlugins(discord)
	loadLibraryPlugins(discord)
}
