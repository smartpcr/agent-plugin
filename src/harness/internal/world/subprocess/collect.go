package subprocess

import (
	"context"
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// DefaultRingBytes is the ring size of a collected stream when
// CollectOptions.TailBytes is zero: the tail the shell tools keep
// (shellexec/bash.Config.TailBytes, docs/harness/areas/execution.md §6.3).
const DefaultRingBytes = 64_000

// DefaultSpillBytes caps a collected stream's spill file when
// CollectOptions.SpillBytes is zero (shellexec/bash.Config.SpillBytes).
const DefaultSpillBytes = 64 << 20

// errCollectorClosed is what a Write after Close returns.
var errCollectorClosed = errors.New("subprocess: the collector is closed")

// CollectOptions says how a Collector bounds a stream.
type CollectOptions struct {
	// TailBytes is the ring size: the most of the stream held in memory, the
	// last bytes written. Zero is DefaultRingBytes.
	TailBytes int
	// Spill, when not nil, is the store the whole stream is saved to once it no
	// longer fits the ring. A stream that fits the ring creates no file.
	Spill spill.Store
	// SpillOwner is the session the spill file is reserved for (spill.Store.Reserve).
	SpillOwner string
	// SpillName is the suggested file name; empty is spill.DefaultName.
	SpillName string
	// SpillBytes caps the spill file; zero is DefaultSpillBytes. A stream over the
	// cap discards its spill, and only the tail is kept.
	SpillBytes int64
	// OnSpillError, when not nil, is called once, with the error, when the spill is
	// dropped: the store refused it or the stream passed the cap. Collection goes
	// on in memory either way.
	OnSpillError func(error)
}

// Collector is the collect output mode (DH-LIB-NODEPROC-R6, DH-D-EXEC-3): a ring
// that keeps the tail of a stream within a fixed bound, and, when asked, a bounded
// spill file that holds the whole stream. Its readers never consume: each holds
// its own byte offset into the whole stream, so any number of them see the same
// bytes. Bytes are never decoded; the tail is only cut so it does not start
// inside a UTF-8 character.
//
// A Collector serves one stream of one process: give it as Spec.Stdout or
// Spec.Stderr, and Start closes it once the child's stream is drained, which
// makes the spill reference valid. A Collector used on its own is closed by its
// owner.
type Collector struct {
	opts CollectOptions

	// writeMu serialises writers and guards the spill state below it, so disk
	// writes never hold mu, which readers take.
	writeMu    sync.Mutex
	closed     bool
	spillW     spill.Writer
	spillRef   spill.Ref
	spillGiven bool // the spill was dropped or closed; no new file is made

	mu       sync.Mutex
	ring     ring
	spillOut *event.Spill
	spillErr error
}

// NewCollector returns a collector bounded by o.
func NewCollector(o CollectOptions) *Collector {
	if o.TailBytes <= 0 {
		o.TailBytes = DefaultRingBytes
	}
	if o.SpillBytes <= 0 {
		o.SpillBytes = DefaultSpillBytes
	}
	if o.SpillName == "" {
		o.SpillName = spill.DefaultName
	}
	return &Collector{opts: o, ring: ring{size: o.TailBytes}}
}

// Write keeps p's bytes in the ring and, past the ring, in the spill. It never
// fails for a spill problem: the spill is dropped and collection goes on.
func (c *Collector) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return 0, errCollectorClosed
	}
	c.spillWrite(p)
	c.mu.Lock()
	c.ring.write(p)
	c.mu.Unlock()
	return len(p), nil
}

// spillWrite saves p to the spill, starting the file with the whole stream so
// far the first time the stream outgrows the ring. writeMu is held.
func (c *Collector) spillWrite(p []byte) {
	if c.opts.Spill == nil || c.spillGiven || len(p) == 0 {
		return
	}
	if c.spillW == nil {
		// Only this writer changes the ring, under writeMu, so reading it here
		// needs no lock. Before the first overflow nothing was evicted: the ring
		// holds the whole stream from offset 0.
		if c.ring.total+int64(len(p)) <= int64(c.ring.size) {
			return
		}
		ref := c.opts.Spill.Reserve(c.opts.SpillOwner, c.opts.SpillName)
		w, err := c.opts.Spill.Create(context.Background(), ref, c.opts.SpillBytes)
		if err != nil {
			c.dropSpill(err)
			return
		}
		c.spillW, c.spillRef = w, ref
		if _, err := w.Write(c.ring.buf[:c.ring.total]); err != nil {
			c.dropSpill(err)
			return
		}
	}
	if _, err := c.spillW.Write(p); err != nil {
		c.dropSpill(err)
	}
}

// dropSpill releases a partial spill and records why. writeMu is held.
func (c *Collector) dropSpill(cause error) {
	if c.spillW != nil {
		if aerr := c.spillW.Abort(); aerr != nil {
			cause = errors.Join(cause, aerr)
		}
		c.spillW = nil
	}
	c.spillGiven = true
	msg := "the spill store refused the stream; only the tail is kept"
	if herr.Has(cause, spill.ReasonTooLarge) {
		msg = "the stream passed its spill cap; the spill was discarded and only the tail is kept"
	}
	err := herr.Wrap(ReasonSpillDropped, cause, msg)
	c.mu.Lock()
	c.spillErr = err
	c.mu.Unlock()
	if c.opts.OnSpillError != nil {
		c.opts.OnSpillError(err)
	}
}

// Close ends the stream: later writes fail, and a spill in progress is synced
// and its reference made valid (Collected.Spill). It returns the error that kept
// the spill from being saved, if any; it is idempotent.
func (c *Collector) Close() error {
	if c.finish() {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.spillErr
	}
	return nil
}

// finish is Close; it reports whether this call failed to save the spill.
func (c *Collector) finish() (failed bool) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return false
	}
	c.closed = true
	if c.spillW == nil {
		return false
	}
	w := c.spillW
	c.spillW = nil
	if err := w.Close(); err != nil {
		c.dropSpill(err)
		return true
	}
	c.spillGiven = true
	c.mu.Lock()
	c.spillOut = &event.Spill{Path: c.spillRef.ID, Bytes: w.Written()}
	c.mu.Unlock()
	return false
}

// Bound is the ring size: the most bytes of the stream held in memory.
func (c *Collector) Bound() int { return c.ring.size }

// Held is the memory the ring holds now, in bytes; it never exceeds Bound.
func (c *Collector) Held() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cap(c.ring.buf)
}

// Written is how many bytes the whole stream has written so far.
func (c *Collector) Written() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ring.total
}

// Since returns a copy of the stream's bytes from offset off to what is written
// now, and the offset the copy starts at. When bytes from off on have already
// left the ring, the copy starts at the oldest kept byte that begins a UTF-8
// character, and from is past off. Nothing is consumed: every call sees the ring
// as it is.
func (c *Collector) Since(off int64) (b []byte, from int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ring.since(off)
}

// Reader returns a reader with its own offset, starting at the beginning of the
// stream. Readers are independent of one another and of the collector.
func (c *Collector) Reader() *OutputReader { return &OutputReader{c: c} }

// Collected is a snapshot of a collected stream.
type Collected struct {
	// Tail is the end of the stream held in memory, cut so it does not start
	// inside a UTF-8 character.
	Tail []byte
	// TailFrom is the stream offset of Tail's first byte.
	TailFrom int64
	// Written is the size of the whole stream so far.
	Written int64
	// Truncated says Tail does not hold the whole stream.
	Truncated bool
	// Spill is the saved whole stream once the collector is closed: Path is the
	// spill id, Bytes its size. Nil when no spill was asked for, the stream fit
	// the ring, the spill was dropped, or the stream is still open.
	Spill *event.Spill
	// SpillErr says why the spill was dropped (subprocess/spill-dropped).
	SpillErr error
}

// Collected returns a snapshot of the stream.
func (c *Collector) Collected() Collected {
	c.mu.Lock()
	defer c.mu.Unlock()
	tail, from := c.ring.since(0)
	out := Collected{Tail: tail, TailFrom: from, Written: c.ring.total, Truncated: from > 0, SpillErr: c.spillErr}
	if c.spillOut != nil {
		s := *c.spillOut
		out.Spill = &s
	}
	return out
}

// OutputReader reads a collected stream from its own offset; reading advances
// only this reader.
type OutputReader struct {
	c   *Collector
	off int64
}

// Next returns the bytes written since the previous Next and advances past them.
// skipped counts the bytes that left the ring before this reader saw them.
func (r *OutputReader) Next() (b []byte, skipped int64) {
	b, from := r.c.Since(r.off)
	skipped = from - r.off
	r.off = from + int64(len(b))
	return b, skipped
}

// Offset is the stream offset the next Next starts at.
func (r *OutputReader) Offset() int64 { return r.off }

// Stream names a collected standard stream (Process.Collected).
type Stream int

// The streams a Collector may serve.
const (
	StreamStdout Stream = iota
	StreamStderr
)

func (s Stream) String() string {
	if s == StreamStderr {
		return "standard error"
	}
	return "standard output"
}

// Collected is the Collector given as s's OutputMode in the Spec, or nil when
// that stream is not collected.
func (p *Process) Collected(s Stream) *Collector {
	if s < 0 || int(s) >= len(p.collected) {
		return nil
	}
	return p.collected[s]
}

// collectorsOf finds the Collectors among spec's output modes.
func collectorsOf(spec Spec) (out [2]*Collector) {
	if c, ok := spec.Stdout.(*Collector); ok {
		out[StreamStdout] = c
	}
	if c, ok := spec.Stderr.(*Collector); ok {
		out[StreamStderr] = c
	}
	return out
}

// ring keeps the last size bytes of a stream. Until the stream first outgrows it,
// buf grows up to size and holds the stream from offset 0; after that buf is
// exactly size bytes and the byte at stream offset o sits at buf[o % size].
type ring struct {
	size  int
	buf   []byte
	total int64
}

func (r *ring) write(p []byte) {
	if len(p) == 0 {
		return
	}
	if int64(len(r.buf)) == r.total && len(r.buf)+len(p) <= r.size {
		r.buf = r.grow(len(p))
		r.buf = append(r.buf, p...)
		r.total += int64(len(p))
		return
	}
	if len(r.buf) < r.size {
		// Offsets below size sit at their own index, so the linear contents are
		// already in ring order.
		full := make([]byte, r.size)
		copy(full, r.buf)
		r.buf = full
	}
	skip := 0
	if len(p) > r.size {
		skip = len(p) - r.size
	}
	data := p[skip:]
	at := int((r.total + int64(skip)) % int64(r.size))
	n := copy(r.buf[at:], data)
	copy(r.buf, data[n:])
	r.total += int64(len(p))
}

// grow returns buf with room for n more bytes, never more than size in all.
func (r *ring) grow(n int) []byte {
	need := len(r.buf) + n
	if need <= cap(r.buf) {
		return r.buf
	}
	c := min(r.size, max(2*cap(r.buf), need, 512))
	b := make([]byte, len(r.buf), c)
	copy(b, r.buf)
	return b
}

// since copies the stream from off (clamped to what is kept) to the end.
func (r *ring) since(off int64) ([]byte, int64) {
	start := r.total - int64(len(r.buf))
	if off > r.total {
		off = r.total
	}
	if off < start {
		off = start
		// Bytes before start are gone: do not begin inside a character.
		for i := 0; i < utf8.UTFMax-1 && off < r.total && !utf8.RuneStart(r.at(off)); i++ {
			off++
		}
	}
	n := int(r.total - off)
	out := make([]byte, n)
	if n == 0 {
		return out, off
	}
	if r.total <= int64(r.size) {
		copy(out, r.buf[off:])
		return out, off
	}
	at := int(off % int64(r.size))
	k := copy(out, r.buf[at:])
	copy(out[k:], r.buf[:n-k])
	return out, off
}

func (r *ring) at(off int64) byte {
	if r.total <= int64(r.size) {
		return r.buf[off]
	}
	return r.buf[off%int64(r.size)]
}
