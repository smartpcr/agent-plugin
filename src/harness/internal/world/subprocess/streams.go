package subprocess

import (
	"bytes"
	"errors"
	"io"
	"os"
)

// StdinMode is what a child reads as its standard input. Any io.Reader is one:
//
//   - nil is the null device (Ignore);
//   - StdinData gives fixed bytes, then end of file (Data);
//   - the read end of a pipe the caller writes, such as io.Pipe's, keeps the input
//     open for as long as the caller wants (Pipe);
//   - an *os.File is handed to the child as it is, with no copy through the host.
type StdinMode interface{ io.Reader }

// StdinData is the input mode that gives the child b, then end of file. b is
// copied, so the caller may reuse it.
func StdinData(b []byte) StdinMode { return bytes.NewReader(bytes.Clone(b)) }

// OutputMode is where a child's standard output or standard error goes. Any
// io.Writer is one:
//
//   - nil is the null device for Start; Run captures a nil stream instead;
//   - Discard is the null device for Start and Run alike (Ignore);
//   - Inherit hands the child the host's own stream: os.Stdout for Stdout,
//     os.Stderr for Stderr;
//   - an *os.File is handed to the child as it is, so the child writes to it
//     without passing through host memory: a file opened with os.O_APPEND keeps
//     what earlier runs wrote (DH-LIB-EXECA-R4);
//   - any other writer gets the bytes copied to it as the child writes them, such
//     as the write end of an io.Pipe the caller reads (Pipe).
//
// Collection with a bounded tail and a spill file (DH-LIB-NODEPROC-R6) is a later
// mode; Run is the capture available now.
type OutputMode interface{ io.Writer }

// Discard and Inherit are the output modes that are not writers of the caller's.
var (
	Discard OutputMode = discardMode{}
	Inherit OutputMode = inheritMode{}
)

// errModeOnly is what Discard and Inherit return when written to directly: they
// name a mode for Start and are never a destination themselves.
var errModeOnly = errors.New("subprocess: Inherit is an output mode, not a writer")

type discardMode struct{}

func (discardMode) Write(p []byte) (int, error) { return len(p), nil }

type inheritMode struct{}

func (inheritMode) Write([]byte) (int, error) { return 0, errModeOnly }

// stdinOf is the reader os/exec is given for m: nil is the null device.
func stdinOf(m StdinMode) io.Reader {
	if m == nil {
		return nil
	}
	return m
}

// outputOf is the writer os/exec is given for m; own is the host's stream that
// Inherit stands for. nil, for os/exec, is the null device.
func outputOf(m OutputMode, own *os.File) io.Writer {
	switch m.(type) {
	case nil, discardMode:
		return nil
	case inheritMode:
		return own
	}
	return m
}
