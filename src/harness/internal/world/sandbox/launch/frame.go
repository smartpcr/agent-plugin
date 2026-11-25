package launch

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// A frame on the pair is a four-byte big-endian length and that many bytes of one
// JSON object. MaxFrame bounds the length: a longer, empty or cut frame is
// sandbox/launch-failed, never a partial message.
const (
	headerSize = 4
	MaxFrame   = 4096
)

// maxEffects and maxEffectName bound a ready's governed list.
const (
	maxEffects    = 32
	maxEffectName = 64
)

// The messages of the exchange.
const (
	msgReady      = "ready"
	msgGo         = "go"
	msgExecFailed = "exec-failed"
)

// Canary is the result of the stage's attempt to open the canary for writing.
type Canary string

// The canary results. A confined stage must report CanaryRefused; an unconfined
// one CanaryNotTried.
const (
	CanaryRefused  Canary = "refused"
	CanaryWritten  Canary = "written"
	CanaryNotTried Canary = "not-tried"
)

// Valid reports whether c is one of the three results.
func (c Canary) Valid() bool {
	return c == CanaryRefused || c == CanaryWritten || c == CanaryNotTried
}

// Ready is what the stage reports before any command exists: the backend it runs
// under, the effects it governs and the canary result. It is evidence for the
// host's decision, never the decision.
type Ready struct {
	Backend  sandbox.Backend
	Governed []sandbox.Effect
	Canary   Canary
}

// frame is the wire form of every message; a pointer field is present or absent.
type frame struct {
	Msg      string    `json:"msg"`
	Backend  *string   `json:"backend,omitempty"`
	Governed *[]string `json:"governed,omitempty"`
	Canary   *string   `json:"canary,omitempty"`
	Error    *string   `json:"error,omitempty"`
}

func failed(format string, args ...any) error {
	return herr.Newf(sandbox.CodeLaunchFailed, format, args...)
}

// validate refuses a ready the host could misread.
func (r Ready) validate() error {
	if !r.Backend.Valid() {
		return failed("ready names backend %q", r.Backend)
	}
	if !r.Canary.Valid() {
		return failed("ready names canary result %q", r.Canary)
	}
	if len(r.Governed) > maxEffects {
		return failed("ready names %d governed effects, more than %d", len(r.Governed), maxEffects)
	}
	seen := make(map[sandbox.Effect]bool, len(r.Governed))
	for _, e := range r.Governed {
		if !effectName(string(e)) {
			return failed("ready names governed effect %q", e)
		}
		if seen[e] {
			return failed("ready names governed effect %q twice", e)
		}
		seen[e] = true
	}
	return nil
}

// effectName accepts a lower-case word with digits and inner hyphens.
func effectName(s string) bool {
	if s == "" || len(s) > maxEffectName || s[0] < 'a' || s[0] > 'z' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// EncodeReady returns r as one frame, or sandbox/launch-failed when r is not a
// valid ready.
func EncodeReady(r Ready) ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	backend, canary := string(r.Backend), string(r.Canary)
	governed := make([]string, 0, len(r.Governed))
	for _, e := range r.Governed {
		governed = append(governed, string(e))
	}
	return encode(frame{Msg: msgReady, Backend: &backend, Governed: &governed, Canary: &canary})
}

// DecodeReady parses b, exactly one frame, as a ready. A truncated, oversized or
// malformed frame, or any other message, is sandbox/launch-failed.
func DecodeReady(b []byte) (Ready, error) {
	r := bytes.NewReader(b)
	f, err := readFrame(r)
	if err != nil {
		return Ready{}, err
	}
	if r.Len() != 0 {
		return Ready{}, failed("%d bytes follow the ready frame", r.Len())
	}
	return f.ready()
}

// ready returns f as a Ready, or sandbox/launch-failed.
func (f frame) ready() (Ready, error) {
	if f.Msg != msgReady {
		return Ready{}, failed("the stage sent %q, want ready", f.Msg)
	}
	if f.Backend == nil || f.Governed == nil || f.Canary == nil || f.Error != nil {
		return Ready{}, failed("ready must hold backend, governed and canary, and nothing else")
	}
	r := Ready{Backend: sandbox.Backend(*f.Backend), Canary: Canary(*f.Canary), Governed: []sandbox.Effect{}}
	for _, e := range *f.Governed {
		r.Governed = append(r.Governed, sandbox.Effect(e))
	}
	if err := r.validate(); err != nil {
		return Ready{}, err
	}
	return r, nil
}

// encode marshals f into a frame.
func encode(f frame) ([]byte, error) {
	body, err := json.Marshal(f)
	if err != nil {
		return nil, herr.Wrap(sandbox.CodeLaunchFailed, err, "encode "+f.Msg)
	}
	if len(body) > MaxFrame {
		return nil, failed("%s frame of %d bytes exceeds %d", f.Msg, len(body), MaxFrame)
	}
	out := make([]byte, headerSize, headerSize+len(body))
	binary.BigEndian.PutUint32(out, uint32(len(body)))
	return append(out, body...), nil
}

// writeFrame writes f to w in one write.
func writeFrame(w io.Writer, f frame) error {
	b, err := encode(f)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return herr.Wrap(sandbox.CodeLaunchFailed, err, "send "+f.Msg)
	}
	return nil
}

// errClosed is the end of file before the first byte of a frame: the other end
// closed the pair.
var errClosed = errors.New("the other end closed the channel")

// readFrame reads one frame from r. End of file before its first byte wraps
// errClosed; every failure is sandbox/launch-failed.
func readFrame(r io.Reader) (frame, error) {
	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return frame{}, herr.Wrap(sandbox.CodeLaunchFailed, errClosed, "read frame")
		}
		return frame{}, herr.Wrap(sandbox.CodeLaunchFailed, err, "read frame header")
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > MaxFrame {
		return frame{}, failed("frame length %d is outside 1..%d", n, MaxFrame)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return frame{}, herr.Wrap(sandbox.CodeLaunchFailed, fmt.Errorf("truncated frame: %w", err), "read frame body")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var f frame
	if err := dec.Decode(&f); err != nil {
		return frame{}, herr.Wrap(sandbox.CodeLaunchFailed, err, "decode frame")
	}
	if dec.More() || dec.InputOffset() != int64(len(bytes.TrimRight(body, " \t\r\n"))) {
		return frame{}, failed("frame holds more than one JSON value")
	}
	if !slices.Contains([]string{msgReady, msgGo, msgExecFailed}, f.Msg) {
		return frame{}, failed("unknown message %q", f.Msg)
	}
	return f, nil
}

// closed reports whether err is the other end closing the pair.
func closed(err error) bool { return errors.Is(err, errClosed) }
