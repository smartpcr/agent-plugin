package jsonrpc

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"sync"
)

// DefaultMaxLineBytes bounds one line when Options leave the bound unset (16 MiB).
const DefaultMaxLineBytes = 16 << 20

// maxReadBuffer caps the read buffer below a large line bound, so the memory a
// reader holds grows with the lines it is sent, never with the bound alone.
const maxReadBuffer = 64 << 10

// Reader reads newline-delimited frames from an untrusted peer. A line holds at most
// MaxLineBytes bytes before its newline; a longer line fails with
// jsonrpc/line-too-long without being buffered past the bound, and that error is
// final: a reader that lost a frame boundary cannot find the next one.
type Reader struct {
	br  *bufio.Reader
	max int
	buf []byte
	err error
}

// NewReader returns a reader of r with the line bound maxLineBytes
// (DefaultMaxLineBytes when not positive).
func NewReader(r io.Reader, maxLineBytes int) *Reader {
	if maxLineBytes <= 0 {
		maxLineBytes = DefaultMaxLineBytes
	}
	size := min(maxLineBytes+1, maxReadBuffer)
	return &Reader{br: bufio.NewReaderSize(r, max(size, 16)), max: maxLineBytes}
}

// MaxLineBytes returns the line bound.
func (r *Reader) MaxLineBytes() int { return r.max }

// ReadLine returns the next line without its newline (and without a carriage return
// before it). The slice is valid until the next call. At the end of the input it
// returns io.EOF; a last line without a newline is returned first.
func (r *Reader) ReadLine() ([]byte, error) {
	if r.err != nil {
		return nil, r.err
	}
	r.buf = r.buf[:0]
	for {
		chunk, err := r.br.ReadSlice('\n')
		content := chunk
		if err == nil {
			content = chunk[:len(chunk)-1]
		}
		if len(r.buf)+len(content) > r.max {
			r.err = newError(ReasonLineTooLong, "a line is longer than "+strconv.Itoa(r.max)+" bytes")
			r.buf = nil
			return nil, r.err
		}
		r.grow(len(content))
		r.buf = append(r.buf, content...)
		switch {
		case err == nil:
			return bytes.TrimSuffix(r.buf, []byte{'\r'}), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(r.buf) > 0:
			r.err = io.EOF
			return bytes.TrimSuffix(r.buf, []byte{'\r'}), nil
		default:
			r.err = err
			return nil, err
		}
	}
}

// grow makes room for n more bytes without letting the capacity pass the bound.
func (r *Reader) grow(n int) {
	need := len(r.buf) + n
	if need <= cap(r.buf) {
		return
	}
	c := min(max(2*cap(r.buf), need, 512), r.max)
	nb := make([]byte, len(r.buf), c)
	copy(nb, r.buf)
	r.buf = nb
}

// Read returns the next frame. Blank lines are skipped. A line that does not decode
// fails with its table error and the reader continues with the next line; a line
// over the bound, a read error and the end of input are final.
func (r *Reader) Read() (Frame, error) {
	for {
		line, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		return Decode(line)
	}
}

// Writer writes frames, one line each, to a peer. It is safe for concurrent use;
// each frame is written with one Write call, so frames never interleave.
type Writer struct {
	mu  sync.Mutex
	w   io.Writer
	max int
}

// NewWriter returns a writer to w with the line bound maxLineBytes
// (DefaultMaxLineBytes when not positive).
func NewWriter(w io.Writer, maxLineBytes int) *Writer {
	if maxLineBytes <= 0 {
		maxLineBytes = DefaultMaxLineBytes
	}
	return &Writer{w: w, max: maxLineBytes}
}

// Write encodes f and writes it with its newline. A frame whose line would be over
// the bound fails with jsonrpc/line-too-long and nothing is written.
func (w *Writer) Write(f Frame) error {
	line, err := encodeLine(f, w.max)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.w.Write(line)
	return err
}

// encodeLine returns the line of f, newline included, within the bound.
func encodeLine(f Frame, maxLineBytes int) ([]byte, error) {
	b, err := Encode(f)
	if err != nil {
		return nil, err
	}
	if len(b) > maxLineBytes {
		return nil, newError(ReasonLineTooLong, "the frame is "+strconv.Itoa(len(b))+" bytes, over the bound of "+strconv.Itoa(maxLineBytes))
	}
	return append(b, '\n'), nil
}
