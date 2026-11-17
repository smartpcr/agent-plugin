// Command suiterun runs the cordis behaviour suites (test/e2e/suites) against the
// Go port, with the Go binding's actions.
package main

import (
	"os"

	"github.com/smartpcr/agent-plugin/test/e2e/bindings/go/actions"
	"github.com/smartpcr/bdd-code-extension/runtimes/go/suite/suiterun"
)

func main() {
	os.Exit(suiterun.Main(os.Args[1:], os.Stdout, os.Stderr, actions.Extension))
}
