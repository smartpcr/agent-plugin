module example.com/untidy

go 1.27.4

// Seeded for the modules gate (Seeded-violation modules, tools/harness-gate/README.md):
// nothing imports golang.org/x/mod, so "go mod tidy -diff" removes this line.
require golang.org/x/mod v0.43.0
