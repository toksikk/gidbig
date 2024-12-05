package gidbig

import (
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
)

var version = ""
var builddate = ""

// LogVersion print version to log
func LogVersion() {
	slog.Info("Gidbig", "version", version, "built", builddate)
}

// Banner Print Version on stdout
func Banner(w io.Writer, loadedPlugins map[string][2]string) {
	if version == "" {
		if build, ok := debug.ReadBuildInfo(); ok {
			version = build.Main.Version
		}
	}
	banner := []string{
		"\n       _     _ _     _       \n",
		"      (_)   | | |   (_)      \n",
		"  ____ _  _ | | | _  _  ____ \n",
		" / _  | |/ || | || \\| |/ _  |\n",
		"( ( | | ( (_| | |_) ) ( ( | |\n",
		" \\_|| |_|\\____|____/|_|\\_|| |\n",
		"(_____|               (_____| %s\n(%s)\n\n",
	}

	bannerLoadedPlugins := []string{
		"\nLoaded Plugins: \n",
		"%s %s (%s)\n",
	}

	withoutWriter := w == nil

	if !strings.Contains(builddate, runtime.Version()) {
		builddate += " using " + runtime.Version()
	}

	for _, v := range banner {
		if !strings.Contains(v, "%s") {
			if withoutWriter {
				fmt.Print(v)
			} else {
				fmt.Fprint(w, v)
			}
		} else {
			if withoutWriter {
				fmt.Printf(v, version, builddate)
			} else {
				fmt.Fprintf(w, v, version, builddate)
			}
		}
	}

	var pluginNames []string
	for k := range loadedPlugins {
		pluginNames = append(pluginNames, k)
	}
	sort.Strings(pluginNames)

	if withoutWriter {
		fmt.Printf("%s", bannerLoadedPlugins[0])
	} else {
		fmt.Fprintf(w, "%s", bannerLoadedPlugins[0])
	}

	for _, v := range pluginNames {
		if withoutWriter {
			fmt.Printf(bannerLoadedPlugins[1], v, loadedPlugins[v][0], loadedPlugins[v][1])
		} else {
			fmt.Fprintf(w, bannerLoadedPlugins[1], v, loadedPlugins[v][0], loadedPlugins[v][1])
		}
	}
}
