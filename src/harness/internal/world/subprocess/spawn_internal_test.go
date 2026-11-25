package subprocess

import (
	"slices"
	"sync/atomic"
	"testing"
)

// CountSpawns makes the test's process creations countable: it returns the number
// of creation attempts whose argv holds token, from now until the test ends. It
// is the fake spawner of the plan-required scenarios (TC-04a).
func CountSpawns(t testing.TB, token string) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	saved := spawning
	spawning = func(argv []string) {
		if slices.Contains(argv, token) {
			n.Add(1)
		}
		saved(argv)
	}
	t.Cleanup(func() { spawning = saved })
	return &n
}
