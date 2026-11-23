package catalog_test

import (
	"bytes"
	"flag"
	"fmt"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// recorder is a testing.TB that records a golden failure instead of failing.
type recorder struct {
	testing.TB
	failed []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.failed = append(r.failed, fmt.Sprintf(format, args...))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = append(r.failed, fmt.Sprintf(format, args...))
}
func (r *recorder) Logf(string, ...any) {}

// The committed catalogue is checked against a fresh render, so a file left
// stale by a change to the compiled-in list fails: here the list one item
// shorter than the build's (the file as an older build wrote it) is refused.
//
//dsh:test DH-T-BOOT-3
func TestStaleCatalogueFails(t *testing.T) {
	if f := flag.Lookup("update"); f != nil && f.Value.String() == "true" {
		t.Log("-update rewrites goldens; the stale check would record the stale list, so it runs without -update only")
		return
	}
	sets := builtin.Sets()
	last := len(sets) - 1
	older := append(append([][]catalog.Item(nil), sets[:last]...), sets[last][:len(sets[last])-1])
	stale, err := catalog.Build(older...)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := stale.Print(&out); err != nil {
		t.Fatal(err)
	}
	r := &recorder{TB: t}
	golden.Assert(r, "catalog", out.Bytes())
	if len(r.failed) != 1 {
		t.Fatalf("a catalogue one component short of the build passed the golden check: %q", r.failed)
	}
}
