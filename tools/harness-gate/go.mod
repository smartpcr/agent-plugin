// The tools module of the repository (M1-D-TOOL-2 (a), DH-D-TOOLCHAIN-1): the gate
// runner, its own analyzers and the pinned linters it runs. It is not in go.work and is
// run with GOWORK=off, so nothing here reaches a product module's build list.
module github.com/smartpcr/agent-plugin/tools/harness-gate

go 1.27.4

require (
	// The Gherkin runner of the gate's own e2e suites in test/e2e/harness-DSH
	// (DH-T-TOOLCHAIN-*; the same runner and version as src/harness/test/e2e, so one
	// Gherkin dialect serves the repository); MIT. Test code only: no package of the
	// gate binary imports it.
	github.com/cucumber/godog v0.16.0
	// The analysis framework of the own analyzers (analyzers/: go/analysis,
	// unitchecker for "go vet -vettool", analysistest for their fixtures, QG-2);
	// BSD-3-Clause, maintained by the Go team. v0.53.0 is also the floor the tools
	// below require (govulncheck v1.10.0 requires v0.53.0, staticcheck v0.8.2 v0.51.0),
	// so the analyzers and the linters share one version.
	golang.org/x/tools v0.53.0
)

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/cucumber/gherkin/go/v42 v42.0.0 // indirect
	github.com/cucumber/messages/go/v34 v34.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.43.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/telemetry v0.0.0-20261116191925-e77a43154acb // indirect
	// The version pin of the govulncheck tool line below.
	golang.org/x/vuln v1.10.0 // indirect
	// The version pin of the staticcheck tool line below.
	honnef.co/go/tools v0.8.2 // indirect
)

tool (
	// The vulnerability check of the vulncheck gate (DH-ADR-TOOLCHAIN-9, M1-D-TOOL-1
	// (a)): v1.10.0, the newest release, verified to build and run on the go1.27.4
	// floor; BSD-3-Clause, maintained by the Go team. Run as
	// "go -C tools/harness-gate tool govulncheck".
	golang.org/x/vuln/cmd/govulncheck
	// The staticcheck gate's linter (DH-ADR-TOOLCHAIN-8): v0.8.2 (release 2026.2.2),
	// the newest release, verified to build and run on the go1.27.4 floor; MIT. Pinned
	// here, not installed per machine, so every lane runs the same checks. Run as
	// "go -C tools/harness-gate tool staticcheck".
	honnef.co/go/tools/cmd/staticcheck
)
