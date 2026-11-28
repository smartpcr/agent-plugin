package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/sandboxstatus"
)

// runSandboxPreflight is "harness-gate sandbox-preflight [--status file]": the
// guarded preflight of the WSL lane (lanes/wsl.sh --guarded-preflight; Stage
// 6.10, architecture.md I-8). It reads a `dsh sandbox status` text report from
// file, or standard input when file is "-" or unset, prints the backend and
// state it read and exits 0 only for backend bwrap, state Full; 1 for any other
// report or one it cannot read (never a skip); 2 on a usage error. wsl.sh
// strips the "harness-gate sandbox-preflight: passed: " prefix from stdout to
// build its success line, so TestSandboxPreflight pins that wording.
//
// With --service it decides that provided service's preflight instead
// (sandboxstatus.Preflight; the s18 jobs of Stage 8.1): service:linux-sandbox-bwrap,
// service:linux-landlock-only or service:linux-unconfined. An unknown service
// fails with exit 1 like a wrong report. --preflight names the machine kind (the
// variant's Preflight column, e.g. "landlock ABI 1-4"); it is required when the
// service has more than one, and is a usage error without --service.
func runSandboxPreflight(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var status, service, preflight *string
	if _, ok := subcommandFlags("sandbox-preflight", args, stderr, func(f *flag.FlagSet) {
		status = f.String("status", "-", `the "dsh sandbox status" report to check; "-" reads standard input`)
		service = f.String("service", "", `the provided service whose preflight decides (default: the guarded bwrap Full check)`)
		preflight = f.String("preflight", "", `the service's preflight text, as variants.md's Preflight column writes it`)
	}); !ok {
		return 2
	}
	if *preflight != "" && *service == "" {
		fmt.Fprintln(stderr, "harness-gate sandbox-preflight: --preflight needs --service")
		return 2
	}
	var b []byte
	var err error
	if *status == "-" {
		b, err = io.ReadAll(stdin)
	} else {
		b, err = os.ReadFile(*status)
	}
	if err != nil {
		fmt.Fprintf(stdout, "harness-gate sandbox-preflight: FAILED: %v\n", err)
		return 1
	}
	check := sandboxstatus.Guarded
	if *service != "" {
		check = func(report string) (sandboxstatus.Status, error) {
			return sandboxstatus.Preflight(*service, *preflight, report)
		}
	}
	s, err := check(string(b))
	if err != nil {
		fmt.Fprintf(stdout, "harness-gate sandbox-preflight: FAILED: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "harness-gate sandbox-preflight: passed: %s\n", s)
	return 0
}
