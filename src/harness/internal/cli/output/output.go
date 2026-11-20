// Package output is the one print and exit contract of dsh (DH-D-CLI-3; app-cli.md
// §6.2.3 and §6.2.4; tech-spec §5.5): every command returns its result through a
// Result writer bound to the result stream and returns an error, and the error is
// mapped to one diagnostic line and a status from one closed table.
//
// The result stream carries only the result. Progress, warnings and failures go to
// the diagnostic stream. Under --json the result is one JSON value (or JSON Lines
// for a streaming command), and a failure also ends the result stream with
// {"error":{"code","message"}}.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/cmdline"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Error codes of the command line itself.
const (
	// ReasonUsage: an unknown command or flag, a broken rule between flags, a
	// missing argument (status 2). It is declared by cmdline, which raises it.
	ReasonUsage = cmdline.ReasonUsage
	// ReasonFailed: a failure that carries no code of its own (status 1).
	ReasonFailed herr.Reason = "cli/failed"
	// ReasonUnavailable: the command needs a part this build does not wire yet
	// (status 4).
	ReasonUnavailable herr.Reason = "cli/unavailable"
	// ReasonClosed: the result stream was closed or refused a write (status 1).
	ReasonClosed herr.Reason = "cli/output-closed"
	// ReasonTwoDocuments: a command tried to write a second JSON value outside JSON
	// Lines mode; a defect of the command (status 70).
	ReasonTwoDocuments herr.Reason = "cli/two-documents"
)

// HelpHint is the one line a usage error adds after its own.
const HelpHint = "Run 'dsh --help' for usage."

// Mode is how a result is rendered.
type Mode int

const (
	// Text is for people; it may change between releases.
	Text Mode = iota
	// JSON is one JSON value; its field names are part of the contract.
	JSON
	// JSONLines is one JSON value per line, for a command that streams.
	JSONLines
)

// Result writes one command's result to the result stream. It is safe for
// concurrent use. A write that fails marks the stream closed; every later write
// returns ReasonClosed without touching the stream.
type Result struct {
	mu      sync.Mutex
	out     io.Writer
	diag    io.Writer
	mode    Mode
	values  int
	err     error
	errSent bool
}

// New returns a Result writing results to out and diagnostics to diag.
func New(out, diag io.Writer, mode Mode) *Result {
	return &Result{out: out, diag: diag, mode: mode}
}

// Mode returns the result's mode.
func (r *Result) Mode() Mode { return r.mode }

// JSON reports whether the result is JSON or JSON Lines.
func (r *Result) JSON() bool { return r.mode != Text }

// Diag returns the diagnostic stream: progress, prompts and warnings go there.
func (r *Result) Diag() io.Writer { return r.diag }

// Value writes v as the result: one indented JSON value under JSON, one compact line
// under JSON Lines, and under Text the value's fmt form on its own line. Under JSON a
// second value is ReasonTwoDocuments.
func (r *Result) Value(v any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var data []byte
	var err error
	switch r.mode {
	case JSON:
		if r.values > 0 {
			return herr.New(ReasonTwoDocuments, "a JSON result is one value")
		}
		data, err = json.MarshalIndent(v, "", "  ")
	case JSONLines:
		data, err = json.Marshal(v)
	default:
		data = []byte(fmt.Sprint(v))
	}
	if err != nil {
		return herr.Wrap(ReasonFailed, err, "encode result")
	}
	r.values++
	return r.write(append(data, '\n'))
}

// Text writes s as is under Text mode; under a JSON mode it is a defect to write
// text to the result stream, so it is refused with ReasonTwoDocuments.
func (r *Result) Text(s string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mode != Text {
		return herr.New(ReasonTwoDocuments, "text written to a JSON result")
	}
	return r.write([]byte(s))
}

// Line writes s and a newline under Text mode, as Text does.
func (r *Result) Line(s string) error { return r.Text(s + "\n") }

func (r *Result) write(data []byte) error {
	if r.err != nil {
		return r.err
	}
	if _, err := r.out.Write(data); err != nil {
		r.err = herr.Wrap(ReasonClosed, err, "write result")
		return r.err
	}
	return nil
}

// Fail reports err for the named command and returns its status: one line
// "dsh <command>: <domain>/<reason>: <message>" on the diagnostic stream (plus
// HelpHint for a usage error) and, under a JSON mode, {"error":{"code","message"}}
// as the last value of the result stream. A nil err is status OK and prints nothing.
func (r *Result) Fail(command string, err error) int {
	if err == nil {
		return OK
	}
	status := Status(err)
	if _, ok := herr.ReasonOf(err); !ok {
		err = herr.Wrap(ReasonFailed, err, "")
	}
	code := herr.Kind(err)
	prefix := "dsh"
	if command = strings.TrimSpace(command); command != "" {
		prefix += " " + command
	}
	fmt.Fprintf(r.diag, "%s: %s\n", prefix, oneLine(err.Error()))
	if code == ReasonUsage {
		fmt.Fprintln(r.diag, HelpHint)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mode != Text && !r.errSent {
		r.errSent = true
		var doc struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		doc.Error.Code, doc.Error.Message = string(code), err.Error()
		data, _ := json.Marshal(doc)
		_ = r.write(append(data, '\n'))
	}
	return status
}

// oneLine folds a multi-line message so a failure stays one diagnostic line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\r", "")), " ")
}

// Usagef returns a usage error (status 2) with the message.
func Usagef(format string, args ...any) error { return herr.Newf(ReasonUsage, format, args...) }

// IsUsage reports whether err is a usage error.
func IsUsage(err error) bool { return herr.Kind(err) == ReasonUsage }
