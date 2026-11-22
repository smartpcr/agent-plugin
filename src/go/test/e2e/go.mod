// The cordis module's Gherkin suites (harness-DSH), as a nested test-only module
// (M1-D-TOOL-4 (A)), so src/go itself requires nothing. It is never released, is not in
// go.work (RM-1 counts the three product modules only) and is run with GOWORK=off:
//
//	GOWORK=off go -C src/go/test/e2e test -tags e2e -count=1 ./...
module github.com/smartpcr/agent-plugin/src/go/test/e2e

go 1.26.0

require (
	// The Gherkin runner of every suite here; the same runner and version as
	// src/harness/test/e2e and tools/harness-gate, so the repository has one Gherkin
	// dialect; MIT. Test code only: it never reaches the cordis module's importers.
	github.com/cucumber/godog v0.16.0
	// The module under test. The placeholder version is resolved by the replace below.
	github.com/smartpcr/agent-plugin/src/go v0.0.0-00010101000000-000000000000
)

require (
	github.com/cucumber/gherkin/go/v42 v42.0.0 // indirect
	github.com/cucumber/messages/go/v34 v34.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
)

// The suites test the working-tree cordis module they sit in, never a published
// version, and this module is never released, so the directory replace cannot leak
// into a build of src/go or src/harness. A test module is the only class modcheck lets
// carry a replace (RM-4 forbids it in the product modules and go.work).
replace github.com/smartpcr/agent-plugin/src/go => ../..
