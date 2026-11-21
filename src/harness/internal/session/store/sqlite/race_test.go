//go:build race

package sqlite_test

// raceEnabled: this binary is instrumented by the race detector, so wall-clock
// figures are not the real code's.
const raceEnabled = true
