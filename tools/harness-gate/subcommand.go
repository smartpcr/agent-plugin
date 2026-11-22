package main

import (
	"flag"
	"fmt"
	"io"
)

// subcommandFlags parses the flags of subcommand name: --root and those add
// registers. It lives outside main.go because the e2e suites build the gate with
// main.go replaced by a one-gate driver ("go build -overlay"), and trace-tests
// (tracetests.go) still needs it there.
func subcommandFlags(name string, args []string, stderr io.Writer, add func(*flag.FlagSet)) (root string, ok bool) {
	flags := flag.NewFlagSet("harness-gate "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	r := flags.String("root", ".", "repository root")
	if add != nil {
		add(flags)
	}
	if err := flags.Parse(args); err != nil {
		return "", false
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "harness-gate %s: unexpected arguments %q\n", name, flags.Args())
		return "", false
	}
	return *r, true
}
