// Command genschema writes the derived protocol schema file. It is run by
// go generate in src/sdk/protocol:
//
//	go -C src/sdk generate ./protocol
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

// run writes protocol.Document() to the one path in args and returns the exit code.
func run(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: genschema <output file>")
		return 2
	}
	doc, err := protocol.Document()
	if err == nil {
		err = os.WriteFile(args[0], doc, 0o644)
	}
	if err != nil {
		fmt.Fprintln(stderr, "genschema:", err)
		return 1
	}
	return 0
}
