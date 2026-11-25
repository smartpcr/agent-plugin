package subprocess

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// With a terminal (a pipe stands in for one), Confirm writes the prompt and reads
// exactly one line: "y" or "yes" confirms, any other line declines, end of input
// before a line is refused, and what follows the line stays unread.
func TestConfirmReadsOneLineFromATerminal(t *testing.T) {
	saved := isTerminal
	isTerminal = func(*os.File) bool { return true }
	t.Cleanup(func() { isTerminal = saved })

	for _, c := range []struct {
		input string
		want  Confirmation
		rest  string
	}{
		{"yes\nnext line\n", Confirmed, "next line\n"},
		{" Y \r\n", Confirmed, ""},
		{"y", Confirmed, ""},
		{"no\n", Declined, ""},
		{"\n", Declined, ""},
		{strings.Repeat("y", maxConfirmLine+1) + "\n", Declined, "y\n"},
		{"", Refused, ""},
	} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, c.input); err != nil {
			t.Fatal(err)
		}
		_ = w.Close()
		var prompt strings.Builder
		got, err := Confirm(r, &prompt, "Go? ")
		rest, _ := io.ReadAll(r)
		_ = r.Close()
		if got != c.want || err != nil || prompt.String() != "Go? " || string(rest) != c.rest {
			t.Errorf("Confirm(%.20q) = %q, %v, prompted %q, left %q; want %q and %q left", c.input, got, err, prompt.String(), rest, c.want, c.rest)
		}
	}
}

// A prompt that cannot be written, or input that cannot be read, is refused with
// the error.
func TestConfirmFailuresAreRefused(t *testing.T) {
	saved := isTerminal
	isTerminal = func(*os.File) bool { return true }
	t.Cleanup(func() { isTerminal = saved })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	broken := errors.New("broken")
	if got, err := Confirm(r, failingWriter{broken}, "Go? "); got != Refused || !errors.Is(err, broken) {
		t.Errorf("Confirm with a failing prompt = %q, %v; want refused with the error", got, err)
	}
	_ = r.Close()
	if got, err := Confirm(r, nil, "Go? "); got != Refused || err == nil {
		t.Errorf("Confirm on a closed input = %q, %v; want refused with the error", got, err)
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }
