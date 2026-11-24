package subprocess

import (
	"errors"
	"io"
	"os"
	"strings"
)

// Confirmation is the answer Confirm returns.
type Confirmation string

// The answers.
const (
	// Confirmed: a person answered "y" or "yes".
	Confirmed Confirmation = "confirmed"
	// Declined: a person answered anything else.
	Declined Confirmation = "declined"
	// Refused: nobody could answer. Standard input is not a terminal (a pipe, a
	// file), or it ended before a line, so nothing was asked of a person.
	Refused Confirmation = "refused"
)

// maxConfirmLine bounds the answer Confirm reads; a longer line declines.
const maxConfirmLine = 1024

// isTerminal reports whether f is a terminal: a character device, as
// os.File.Stat reports it. The null device is one too; it ends at once, which
// Confirm refuses. Internal tests replace it to read an answer from a pipe.
var isTerminal = func(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Confirm asks a person to confirm: it writes prompt to out (nil writes nothing)
// and reads one line
// from in, normally os.Stdin (DH-LIB-NODEPROC-R15). When in is not a terminal it
// returns Refused without writing the prompt or reading anything, so a script
// that pipes an answer in can never confirm on a person's behalf. "y" or "yes", in
// any case and with surrounding space, confirms; any other line declines; end of
// input before a line is Refused. A write or read failure is Refused with the
// error.
func Confirm(in *os.File, out io.Writer, prompt string) (Confirmation, error) {
	if in == nil || !isTerminal(in) {
		return Refused, nil
	}
	if out != nil {
		if _, err := io.WriteString(out, prompt); err != nil {
			return Refused, err
		}
	}
	line, err := readLine(in)
	switch {
	case errors.Is(err, io.EOF) && line == "":
		return Refused, nil
	case err != nil && !errors.Is(err, io.EOF):
		return Refused, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return Confirmed, nil
	}
	return Declined, nil
}

// readLine reads up to a line feed one byte at a time, so nothing after the line
// is taken from in; a line past maxConfirmLine is cut there.
func readLine(in io.Reader) (string, error) {
	var b strings.Builder
	one := make([]byte, 1)
	for b.Len() < maxConfirmLine {
		n, err := in.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				return b.String(), nil
			}
			b.WriteByte(one[0])
		}
		if err != nil {
			return b.String(), err
		}
	}
	return b.String(), nil
}
