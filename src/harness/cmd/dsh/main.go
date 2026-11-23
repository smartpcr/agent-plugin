// Command dsh is the harness binary. It holds main only; every implementation
// package lives under src/harness/internal (RM-5), the command tree in
// internal/cli (DH-D-CLI-1).
package main

import (
	"context"
	"os"

	"github.com/smartpcr/agent-plugin/src/harness/internal/cli"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute/listing"

	// The zone database is embedded so that zone names resolve the same on every
	// host, a Windows host without Go installed included (DH-T-TEMPORAL-5).
	_ "time/tzdata"
)

// version is set at release time with -ldflags "-X main.version=<v>". When empty,
// the module version recorded by the go command is used.
var version string

// exit is os.Exit; a test replaces it.
var exit = os.Exit

func main() {
	// The listing-only MCP server a signed-in client route's client starts
	// (DH-ADR-LLM-10) is a hidden mode dispatched before the command tree.
	if code, ok := listing.ServeIfAsked(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); ok {
		exit(code)
		return
	}
	exit(cli.Main(context.Background(), cli.ProcessEnv(version)))
}
