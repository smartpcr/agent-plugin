package subprocess

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// The modes map to what os/exec is given: nil and Discard are the null device,
// Inherit is the host's own stream, a writer is itself; StdinData copies its bytes.
// Inherit is never a writer itself.
func TestModesMapToTheStreams(t *testing.T) {
	var b strings.Builder
	for name, c := range map[string]struct {
		mode OutputMode
		want io.Writer
	}{
		"nil":     {nil, nil},
		"discard": {Discard, nil},
		"inherit": {Inherit, os.Stderr},
		"writer":  {&b, &b},
	} {
		if got := outputOf(c.mode, os.Stderr); got != c.want {
			t.Errorf("%s: outputOf = %v; want %v", name, got, c.want)
		}
	}
	if stdinOf(nil) != nil {
		t.Error("stdinOf(nil) is not the null device")
	}
	data := []byte("input")
	in := StdinData(data)
	data[0] = 'X'
	if got, _ := io.ReadAll(stdinOf(in)); string(got) != "input" {
		t.Errorf("StdinData gave %q; want the bytes as they were passed", got)
	}
	if n, err := Discard.Write([]byte("abc")); n != 3 || err != nil {
		t.Errorf("Discard.Write = %d, %v", n, err)
	}
	if _, err := Inherit.Write([]byte("abc")); !errors.Is(err, errModeOnly) {
		t.Errorf("Inherit.Write = %v; want %v", err, errModeOnly)
	}
}

// A capture keeps the exact bytes up to its limit and trips the stop once, on the
// first byte past it; the rest goes to the tail, whose omitted count is exact.
func TestCaptureKeepsTheHeadExactAndTheTail(t *testing.T) {
	trips := 0
	c := newCapture(RunOptions{MaxOutput: 4, TailBytes: 3}, func() { trips++ })
	for _, p := range []string{"ab", "cdef", "ghij"} {
		if n, err := c.Write([]byte(p)); n != len(p) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", p, n, err)
		}
	}
	out := c.output()
	if !bytes.Equal(out.Bytes, []byte("abcd")) || !out.Exceeded || out.Tail != "hij" || out.Omitted != 3 || trips != 1 {
		t.Fatalf("output = %+v after %d trips; want abcd, exceeded, tail hij, 3 omitted, one trip", out, trips)
	}
	if got := (*capture)(nil).output(); got.Bytes != nil || got.Exceeded {
		t.Fatalf("a stream that was not captured = %+v; want empty", got)
	}
}
