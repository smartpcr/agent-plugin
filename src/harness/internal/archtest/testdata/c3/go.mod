// Fixture for TestSeededTreeFailsTheC3Rules: the cycle C-3 import rules over a
// tree with seeded violations. It shares the harness module path so the rules
// apply; GOWORK=off keeps it out of the workspace.
module github.com/smartpcr/agent-plugin/src/harness

go 1.27.4
