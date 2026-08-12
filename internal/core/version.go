package gidbig

import (
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"runtime/debug"
	"strings"
)

var version = ""
var builddate = ""

func currentVersion() string {
	if version != "" {
		return version
	}
	if build, ok := debug.ReadBuildInfo(); ok && build.Main.Version != "" {
		version = build.Main.Version
	}
	if version == "" {
		version = "(devel)"
	}
	return version
}

// LogVersion print version to log
func LogVersion() {
	slog.Info("Gidbig", "version", currentVersion(), "built", builddate)
}

// Banner Print Version on stdout
func Banner(w io.Writer) {
	currentVersion()
	banner := []string{
		"\n       _     _ _     _       \n",
		"      (_)   | | |   (_)      \n",
		"  ____ _  _ | | | _  _  ____ \n",
		" / _  | |/ || | || \\| |/ _  |\n",
		"( ( | | ( (_| | |_) ) ( ( | |\n",
		" \\_|| |_|\\____|____/|_|\\_|| |\n",
		"(_____|               (_____| %s\n(%s)\n\n",
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
				_, _ = fmt.Fprint(w, v)
			}
		} else {
			if withoutWriter {
				fmt.Printf(v, version, builddate)
			} else {
				_, _ = fmt.Fprintf(w, v, version, builddate)
			}
		}
	}
}
