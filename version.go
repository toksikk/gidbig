package main

import (
	"fmt"
	"runtime/debug"
)

var version = ""
var builddate = ""

// Banner Print Version on stdout
func Banner() {
	if version == "" {
		if build, ok := debug.ReadBuildInfo(); ok {
			version = build.Main.Version
		}
	}
	fmt.Printf("\n       _     _ _     _       \n")
	fmt.Printf("      (_)   | | |   (_)      \n")
	fmt.Printf("  ____ _  _ | | | _  _  ____ \n")
	fmt.Printf(" / _  | |/ || | || \\| |/ _  |\n")
	fmt.Printf("( ( | | ( (_| | |_) ) ( ( | |\n")
	fmt.Printf(" \\_|| |_|\\____|____/|_|\\_|| | %s\n", version)
	fmt.Printf("(_____|               (_____|\n\n")

}
