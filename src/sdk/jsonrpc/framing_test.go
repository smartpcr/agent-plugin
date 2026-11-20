package jsonrpc

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

// TestReaderRejectsOversizedLine is TC-04a: one line one byte over the bound fails
// with jsonrpc/line-too-long, the buffer never grows past the bound, and the fault
// is final.
func TestReaderRejectsOversizedLine(t *testing.T) {
	const bound = 1024
	line := strings.Repeat("a", bound+1) + "\n" + `{"jsonrpc":"2.0","method":"x"}` + "\n"
	r := NewReader(strings.NewReader(line), bound)
	got, err := r.ReadLine()
	if got != nil {
		t.Fatalf("ReadLine returned %d bytes, want none", len(got))
	}
	if !HasReason(err, ReasonLineTooLong) {
		t.Fatalf("err = %v, want %s", err, ReasonLineTooLong)
	}
	if cap(r.buf) > bound {
		t.Fatalf("buffer capacity %d passed the bound %d", cap(r.buf), bound)
	}
	if _, err := r.Read(); !HasReason(err, ReasonLineTooLong) {
		t.Fatalf("second read err = %v, want the sticky %s", err, ReasonLineTooLong)
	}
}

// endless yields an unending line of 'a' without holding it in memory.
type endless struct{ n int }

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	e.n += len(p)
	return len(p), nil
}

func TestReaderDoesNotAllocatePastBound(t *testing.T) {
	const bound = 256 << 10
	src := &endless{}
	r := NewReader(src, bound)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := r.ReadLine()
	runtime.ReadMemStats(&after)
	if !HasReason(err, ReasonLineTooLong) {
		t.Fatalf("err = %v, want %s", err, ReasonLineTooLong)
	}
	// The line buffer may grow by doubling up to the bound: below 2x the bound in
	// total, plus slack for the error value and the runtime.
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 2*bound+64<<10 {
		t.Fatalf("reading an over-long line allocated %d bytes for a bound of %d", alloc, bound)
	}
	if src.n > bound+maxReadBuffer+1 {
		t.Fatalf("read %d bytes of the line; it should stop just past the bound %d", src.n, bound)
	}
}

func TestReaderLines(t *testing.T) {
	const bound = 8
	in := strings.Repeat("b", bound) + "\n" + "cr\r\n" + "\n" + "last"
	r := NewReader(strings.NewReader(in), bound)
	for _, want := range []string{strings.Repeat("b", bound), "cr", "", "last"} {
		got, err := r.ReadLine()
		if err != nil {
			t.Fatalf("ReadLine: %v (want %q)", err, want)
		}
		if string(got) != want {
			t.Fatalf("ReadLine = %q, want %q", got, want)
		}
	}
	if _, err := r.ReadLine(); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if r.MaxLineBytes() != bound {
		t.Fatalf("MaxLineBytes = %d", r.MaxLineBytes())
	}
}

func TestReaderLongLineWithinBound(t *testing.T) {
	const bound = 3 * maxReadBuffer
	line := strings.Repeat("c", bound)
	r := NewReader(strings.NewReader(line+"\n"), bound)
	got, err := r.ReadLine()
	if err != nil || string(got) != line {
		t.Fatalf("ReadLine = %d bytes, %v; want %d bytes", len(got), err, bound)
	}
}

type failingReader struct{}

var errBroken = errors.New("broken pipe")

func (failingReader) Read([]byte) (int, error) { return 0, errBroken }

func TestReaderReadError(t *testing.T) {
	r := NewReader(failingReader{}, 0)
	if r.MaxLineBytes() != DefaultMaxLineBytes {
		t.Fatalf("default bound = %d", r.MaxLineBytes())
	}
	if _, err := r.Read(); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v, want %v", err, errBroken)
	}
}

func TestReaderRead(t *testing.T) {
	in := "\n  \n" + `{"jsonrpc":"2.0","method":"a"}` + "\n" + "nope\n" + `{"jsonrpc":"2.0","method":"b"}`
	r := NewReader(strings.NewReader(in), 64)
	f, err := r.Read()
	if n, ok := f.(*Notification); err != nil || !ok || n.Method != "a" {
		t.Fatalf("Read = %#v, %v", f, err)
	}
	if _, err := r.Read(); !HasReason(err, ReasonParseError) {
		t.Fatalf("err = %v, want %s", err, ReasonParseError)
	}
	f, err = r.Read()
	if n, ok := f.(*Notification); err != nil || !ok || n.Method != "b" {
		t.Fatalf("Read after a bad line = %#v, %v", f, err)
	}
	if _, err := r.Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errBroken }

func TestWriter(t *testing.T) {
	var b bytes.Buffer
	w := NewWriter(&b, 0)
	if err := w.Write(&Notification{Method: "m", Params: []byte(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), `{"jsonrpc":"2.0","method":"m","params":{"a":1}}`+"\n"; got != want {
		t.Fatalf("wrote %q, want %q", got, want)
	}

	small := NewWriter(&b, 16)
	b.Reset()
	if err := small.Write(&Notification{Method: "too-long-for-the-bound"}); !HasReason(err, ReasonLineTooLong) {
		t.Fatalf("err = %v, want %s", err, ReasonLineTooLong)
	}
	if b.Len() != 0 {
		t.Fatalf("an over-bound frame wrote %q", b.String())
	}
	if err := small.Write(&Notification{}); !HasReason(err, ReasonInvalidRequest) {
		t.Fatalf("err = %v, want %s", err, ReasonInvalidRequest)
	}
	if err := NewWriter(failingWriter{}, 0).Write(&Notification{Method: "m"}); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v, want %v", err, errBroken)
	}
}
