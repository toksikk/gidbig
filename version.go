package gidbig

import (
	"fmt"
	"io"
	"runtime/debug"
	"sort"
	"strings"
)

var version = ""
var builddate = "" // nolint:unused

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
		"(_____|               (_____| %s (%s)\n\n",
	}

	bannerLoadedPlugins := []string{
		"\nLoaded Plugins: \n",
		"%s %s (%s)\n",
	}

	for _, v := range banner {
		if !strings.Contains(v, "%s") {
			if w == nil {
				fmt.Print(v)
			} else {
				fmt.Fprint(w, v)
			}
		} else {
			if w == nil {
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

	for _, v := range pluginNames {
		if w == nil {
			fmt.Printf(bannerLoadedPlugins[0])
			fmt.Printf(bannerLoadedPlugins[1], v, loadedPlugins[v][0], loadedPlugins[v][1])
		} else {
			fmt.Fprintf(w, bannerLoadedPlugins[0])
			fmt.Fprintf(w, bannerLoadedPlugins[1], v, loadedPlugins[v][0], loadedPlugins[v][1])
		}
	}
}
