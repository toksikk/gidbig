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
		return normalizeVersion(version)
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		version = versionFromBuildInfo(build)
	}
	if version == "" {
		version = "(devel)"
	}
	return version
}

// normalizeVersion makes sure a bare commit hash (for example the fallback of
// `git describe --always` when a shallow clone has no tags) is never presented
// as the whole version. A hash alone carries no release context, so it is
// wrapped in a "(devel <hash>)" form, matching the development-build output.
func normalizeVersion(v string) string {
	if v == "" || strings.HasPrefix(v, "v") || strings.HasPrefix(v, "(") {
		return v
	}
	if isCommitHash(v) {
		return "(devel " + v + ")"
	}
	return v
}

func isCommitHash(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func versionFromBuildInfo(build *debug.BuildInfo) string {
	resolved := build.Main.Version
	revision := ""
	modified := false
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if resolved != "" && resolved != "(devel)" {
		return resolved
	}
	if revision == "" {
		return "(devel)"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		revision += " dirty"
	}
	return "(devel " + revision + ")"
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
