// Package x is seeded for the test-layer build tags: its default tests pass, and a
// replay and a scenario test that always fail are excluded from the gate.
package x

// Add returns a + b.
func Add(a, b int) int { return a + b }
