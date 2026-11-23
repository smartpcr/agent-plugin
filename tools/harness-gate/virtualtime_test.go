package main

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestSynctestTimerFinishesWithoutRealWaiting is the virtual-time clause of
// DH-ADR-TOOLCHAIN-2: a test that waits an hour on a timer inside a
// testing/synctest bubble sees the full hour pass on the bubble's clock, fires no
// earlier, and returns in a small fraction of that on the wall clock.
//
//dsh:test DH-T-TOOLCHAIN-2
func TestSynctestTimerFinishesWithoutRealWaiting(t *testing.T) {
	const wait = time.Hour
	wallStart := time.Now()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		timer := time.NewTimer(wait)
		defer timer.Stop()
		early := time.NewTimer(wait - time.Nanosecond)
		defer early.Stop()
		<-early.C
		select {
		case <-timer.C:
			t.Fatal("the timer fired before its duration")
		default:
		}
		fired := <-timer.C
		if got := fired.Sub(start); got != wait {
			t.Errorf("the timer fired after %v of virtual time, want %v", got, wait)
		}
	})
	if wall := time.Since(wallStart); wall >= time.Minute {
		t.Errorf("a %v virtual wait took %v of wall time, want well under a minute", wait, wall)
	}
}
