package gidbig

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

var Version = ""
var Builddate = ""

// Banner Print Version on stdout
func Banner(w io.Writer) {
	if Version == "" {
		if build, ok := debug.ReadBuildInfo(); ok {
			Version = build.Main.Version
		}
	}
	banner := []string{
		"\n       _     _ _     _       \n",
		"      (_)   | | |   (_)      \n",
		"  ____ _  _ | | | _  _  ____ \n",
		" / _  | |/ || | || \\| |/ _  |\n",
		"( ( | | ( (_| | |_) ) ( ( | |\n",
		" \\_|| |_|\\____|____/|_|\\_|| |\n",
		"(_____|               (_____| %s\n\n",
	}

	for _, v := range banner {
		if !strings.Contains(v, "%s") {
			if w == nil {
				fmt.Printf(v)
			} else {
				fmt.Fprint(w, v)
			}
		} else {
			if w == nil {
				fmt.Printf(v, Version)
			} else {
				fmt.Fprintf(w, v, Version)
			}
		}
	}
}
