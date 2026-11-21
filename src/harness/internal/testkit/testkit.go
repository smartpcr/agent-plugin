// Package testkit composes real harness components against scripted boundaries
// (DH-D-UTIL-6, DH-ADR-UTIL-6). Its packages hold the shared stand-ins: clock (a
// virtual clock), temphome (private homes), model (a scripted model behind a fake
// transport), answerer (a scripted human channel), permit (egress permits) and
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
// shape of a component's output stream read by the test.
type Buffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
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
