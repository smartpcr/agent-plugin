// Package race seeds a data race for the test gate.
package race

import (
	"sync"
	"testing"
)

func TestRace(t *testing.T) {
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	n := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		n++
	}()
	n++
	wg.Wait()
	if n == 0 {
		t.Fatal("unreachable")
	}
}
