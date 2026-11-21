//go:build !race

package sqlite_test

// raceEnabled: this binary is not instrumented by the race detector.
const raceEnabled = false