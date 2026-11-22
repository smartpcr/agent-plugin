package testkit_test

import (
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
)

// A kit tree is live while its test runs and disposed, with the fibers loaded
// beneath it, once that test ends.
func TestTreeIsDisposedWhenItsTestEnds(t *testing.T) {
	var root, child *cordis.Context
	t.Run("use", func(t *testing.T) {
		root = testkit.Tree(t)
		f, err := root.Apply("child", func(c *cordis.Context) error { child = c; return nil })
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Wait(); err != nil {
			t.Fatal(err)
		}
		if root.Lifetime().Err() != nil || child.Lifetime().Err() != nil {
			t.Fatal("the tree is not live while its test runs")
		}
	})
	if root.Lifetime().Err() == nil {
		t.Error("the tree outlived its test")
	}
	if child.Lifetime().Err() == nil {
		t.Error("a fiber loaded beneath the tree outlived its test")
	}
}

// Buffer keeps every write of a writer racing a reader, and Reset empties it.
func TestBufferIsSafeForAWriterRacingAReader(t *testing.T) {
	var b testkit.Buffer
	const n = 200
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		for range n {
			if _, err := b.Write([]byte("x")); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
	}()
	for range n {
		if s := b.String(); strings.Trim(s, "x") != "" {
			t.Fatalf("a read saw %q, not only the writes", s)
		}
	}
	<-done
	if got := b.String(); got != strings.Repeat("x", n) {
		t.Fatalf("the buffer holds %d bytes, want %d", len(got), n)
	}
	b.Reset()
	if got := b.String(); got != "" {
		t.Fatalf("after Reset the buffer holds %q", got)
	}
	if n, err := b.Write([]byte("after")); err != nil || n != 5 || b.String() != "after" {
		t.Fatalf("a write after Reset: %d %v %q", n, err, b.String())
	}
}
