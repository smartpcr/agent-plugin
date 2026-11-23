package store_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// Notices calls its subscribers in subscription order, outside its lock (a
// subscriber may subscribe again), and a stopped subscriber is called no more.
func TestNoticesSubscribeSendStop(t *testing.T) {
	var (
		n   store.Notices
		mu  sync.Mutex
		got []string
	)
	record := func(name string) func(store.Degraded) {
		return func(d store.Degraded) {
			mu.Lock()
			got = append(got, name+":"+d.Session)
			mu.Unlock()
		}
	}
	n.Send(store.Degraded{Session: "nobody"})
	stopA := n.Subscribe(record("a"))
	var stopC func()
	n.Subscribe(func(d store.Degraded) {
		record("b")(d)
		if stopC == nil {
			stopC = n.Subscribe(record("c")) // re-entrant: Send holds no lock
		}
	})
	n.Send(store.Degraded{Session: "s1"})
	stopA()
	stopA() // idempotent
	n.Send(store.Degraded{Session: "s2"})
	stopC()
	n.Send(store.Degraded{Session: "s3"})
	want := []string{"a:s1", "b:s1", "b:s2", "c:s2", "b:s3"}
	if !slices.Equal(got, want) {
		t.Fatalf("notices = %v, want %v", got, want)
	}
}

// DerivedKey names a row by key and state version.
func TestDerivedKey(t *testing.T) {
	if k := store.DerivedKey(store.Checkpoint{Key: "view/list", StateVersion: 3}); k != "view/list@3" {
		t.Fatalf("DerivedKey = %q", k)
	}
	d := store.Degraded{Reason: store.DegradedDerived, Err: errors.New("refused")}
	if d.Reason != "derived-dropped" {
		t.Fatalf("Reason = %q", d.Reason)
	}
}
