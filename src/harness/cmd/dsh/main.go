// Command dsh is the harness binary. It holds main only; every implementation
// package lives under src/harness/internal (RM-5).
package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

// version is set at release time with -ldflags "-X main.version=<v>". When empty,
// the module version recorded by the go command is used.
var version string

func main() {
	v := version
	if v == "" {
		v = "(devel)"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			v = info.Main.Version
		}
	}
	fmt.Fprintln(os.Stdout, "dsh", v)
}
