// Command go stands in for a go command whose version is not the floor and that
// GOTOOLCHAIN cannot switch: it answers "go version" with go1.99.0 and fails every
// other command (DH-T-TOOLCHAIN-9).
package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Printf("go version go1.99.0 %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return
	}
	fmt.Fprintf(os.Stderr, "fake go: %q is not supported\n", os.Args[1:])
	os.Exit(1)
}
