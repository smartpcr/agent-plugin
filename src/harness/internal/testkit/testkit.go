// Package testkit composes real harness components against scripted boundaries
// (DH-D-UTIL-6, DH-ADR-UTIL-6). Its packages hold the shared stand-ins: clock
// (virtual time in a synctest bubble), temphome (private homes), model (a
// scripted model, behind a fake transport or chunk-level), egresstest (the
// recording egress), faultserver (a loopback HTTP/SSE fault server), compose (a
// real boot tree from a shipped bundle over those stand-ins), answerer (a
// scripted human channel), permit (egress permits) and
// launcher (a program started through internal/world/subprocess and spoken to
// over JSON-RPC on its stdio). This package holds what they all need: a disposable
// component tree and a goroutine-safe buffer.
//
// Every helper takes a testing.TB and registers each release it acquires with
// t.Cleanup (QG-5): a test never closes what the kit opened. Only tests and the
// kit's own packages import the kit (archtest, DH-T-UTIL-11).
package testkit

import (
	"bytes"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Tree is a fresh component tree with no log exporter; it is disposed when the
// test ends, unloading every fiber and effect loaded beneath it.
func Tree(t testing.TB) *cordis.Context {
	t.Helper()
	root := cordis.New(cordis.WithExporters())
	t.Cleanup(root.Dispose)
	return root
}

// Buffer is a bytes.Buffer safe for one writer goroutine racing a reader, the
// shape of a component's output stream read by the test. With a limit set it
// keeps at most that many bytes and counts the rest as dropped.
type Buffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	limit   int
	dropped int64
}

// SetLimit makes the buffer keep at most n bytes (0: no limit); a write past the
// limit is accepted in full, so the writer never blocks, and its excess is dropped.
func (b *Buffer) SetLimit(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.limit = n
}

// Write appends p, up to the limit.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit > 0 && b.buf.Len()+len(p) > b.limit {
		keep := max(b.limit-b.buf.Len(), 0)
		b.dropped += int64(len(p) - keep)
		b.buf.Write(p[:keep])
		return len(p), nil
	}
	return b.buf.Write(p)
}

// Dropped is how many written bytes the limit dropped.
func (b *Buffer) Dropped() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// String is everything written so far.
func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Reset discards everything written so far.
func (b *Buffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}
