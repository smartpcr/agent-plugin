package subprocess_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// The confirmation helper returns "refused" when standard input is a pipe, and
// asks nothing and reads nothing: the answer piped in is still in the pipe. A
// regular file, the null device and no input at all are refused the same way.
// (The other half of DH-T-NODEPROC-10, that no package but this one imports
// os/exec, is the archtest rule onespawn and DH-T-EXEC-1.)
//
//dsh:test DH-T-NODEPROC-10
func TestConfirmRefusesWithoutATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := io.WriteString(w, "yes\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	var prompt strings.Builder
	got, err := subprocess.Confirm(r, &prompt, "Publish? [y/N] ")
	if got != subprocess.Refused || err != nil || prompt.Len() != 0 {
		t.Fatalf("Confirm(pipe) = %q, %v, prompted %q; want refused, no error and no prompt", got, err, prompt.String())
	}
	if rest, _ := io.ReadAll(r); string(rest) != "yes\n" {
		t.Fatalf("the pipe holds %q after Confirm; want the answer unread", rest)
	}

	file := filepath.Join(t.TempDir(), "answer")
	if err := os.WriteFile(file, []byte("yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"regular file": file, "null device": os.DevNull} {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := subprocess.Confirm(f, nil, "Publish? ")
		_ = f.Close()
		if got != subprocess.Refused || err != nil {
			t.Errorf("Confirm(%s) = %q, %v; want refused", name, got, err)
		}
	}
	if got, err := subprocess.Confirm(nil, nil, "Publish? "); got != subprocess.Refused || err != nil {
		t.Errorf("Confirm(nil) = %q, %v; want refused", got, err)
	}
}
