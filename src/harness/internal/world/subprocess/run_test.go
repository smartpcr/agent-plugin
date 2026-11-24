package subprocess_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// Run tells its five outcomes apart: exit 0 and exit 3 are results, not errors;
// an absent program never started; a deadline and a cancellation each kill the
// tree at once, grandchild included, and say which of the two ended the run.
//
//dsh:test DH-T-EXECA-1
func TestRunTellsTheOutcomesApart(t *testing.T) {
	ctx := context.Background()
	for _, code := range []int{0, 3} {
		t.Run(fmt.Sprintf("exit %d", code), func(t *testing.T) {
			res, err := subprocess.Run(ctx, subprocesstest.Spec(t, subprocesstest.RoleExit, fmt.Sprint(code)), subprocess.RunOptions{})
			if err != nil || !res.Started || res.ExitCode != code {
				t.Fatalf("Run = %+v, %v; want started, exit code %d and no error", res, err, code)
			}
		})
	}
	t.Run("absent program", func(t *testing.T) {
		spec := subprocesstest.Spec(t, subprocesstest.RoleExit)
		spec.Argv = []string{filepath.Join(t.TempDir(), "no-such-program")}
		res, err := subprocess.Run(ctx, spec, subprocess.RunOptions{})
		if res.Started || res.ExitCode != -1 || herr.Kind(err) != subprocess.ReasonStartFailed {
			t.Fatalf("Run = %+v, %v; want never started (%s)", res, err, subprocess.ReasonStartFailed)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		// The tree reports its pids well inside the deadline; the deadline then
		// ends a run that would otherwise never end.
		const deadline = 5 * time.Second
		start := time.Now()
		res, err := subprocess.Run(ctx, subprocesstest.Spec(t, subprocesstest.RoleTree), subprocess.RunOptions{Timeout: deadline})
		took := time.Since(start)
		if herr.Kind(err) != subprocess.ReasonDeadline || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run = %v; want %s wrapping the deadline", err, subprocess.ReasonDeadline)
		}
		if took > deadline+5*time.Second {
			t.Errorf("Run returned %v after the start; the tree was not killed at once", took)
		}
		treeGone(t, string(res.Stdout.Bytes))
	})
	t.Run("cancelled", func(t *testing.T) {
		cctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		stop := errors.New("the caller gave up")
		spec := subprocesstest.Spec(t, subprocesstest.RoleTree)
		line := &lineWriter{seen: func() { cancel(stop) }}
		spec.Stdout = line
		res, err := subprocess.Run(cctx, spec, subprocess.RunOptions{})
		if herr.Kind(err) != subprocess.ReasonCanceled || !errors.Is(err, stop) {
			t.Fatalf("Run = %v; want %s wrapping the caller's cause", err, subprocess.ReasonCanceled)
		}
		if len(res.Stdout.Bytes) != 0 {
			t.Errorf("Run captured %q from a stream the Spec set", res.Stdout.Bytes)
		}
		treeGone(t, line.String())
	})
}

// The plan's scenario "Run caps output and ends the tree": a child that writes one
// byte over the cap after it started a grandchild gets the cap error, and neither
// pid is alive when Run returns. The byte past the cap is the kept tail.
//
//dsh:test DH-T-EXECA-2
func TestRunCapsOutputAndEndsTheTree(t *testing.T) {
	const limit = 4096
	spec := subprocesstest.Spec(t, subprocesstest.RoleFlood, fmt.Sprint(limit+1))
	res, err := subprocess.Run(context.Background(), spec, subprocess.RunOptions{MaxOutput: limit, Timeout: 60 * time.Second})
	if herr.Kind(err) != subprocess.ReasonOutputLimit {
		t.Fatalf("Run = %v; want %s", err, subprocess.ReasonOutputLimit)
	}
	if !strings.Contains(err.Error(), "standard output") {
		t.Errorf("the limit error %q does not name the stream", err)
	}
	out := res.Stdout
	if !out.Exceeded || len(out.Bytes) != limit || out.Tail != "x" || out.Omitted != 0 || res.Stderr.Exceeded {
		t.Fatalf("stdout = %d bytes, exceeded %v, tail %q, omitted %d (stderr exceeded %v); want the cap, exceeded, tail \"x\"",
			len(out.Bytes), out.Exceeded, out.Tail, out.Omitted, res.Stderr.Exceeded)
	}
	treeGone(t, string(res.Stderr.Bytes))
}

// Captured output is the exact bytes the program wrote, with a final newline, without
// one, and binary; output that fills the limit exactly is a result, one byte more
// stops the run and is reported, on either stream.
//
//dsh:test DH-T-EXECA-2
func TestRunCapturesExactBytes(t *testing.T) {
	ctx := context.Background()
	for name, data := range map[string][]byte{
		"final newline":    []byte("line one\nline two\n"),
		"no final newline": []byte("no newline at the end"),
		"binary":           {0x00, 0x01, 0xff, 0xfe, '\r', '\n', 0x80, 0x00, '\n', '\n'},
	} {
		for _, stream := range []string{"stdout", "stderr"} {
			t.Run(name+"/"+stream, func(t *testing.T) {
				spec := subprocesstest.Spec(t, subprocesstest.RoleWrite, stream, hex.EncodeToString(data))
				got := func(r subprocess.Result) subprocess.Output {
					if stream == "stdout" {
						return r.Stdout
					}
					return r.Stderr
				}
				res, err := subprocess.Run(ctx, spec, subprocess.RunOptions{MaxOutput: len(data)})
				if out := got(res); err != nil || out.Exceeded || !bytes.Equal(out.Bytes, data) {
					t.Fatalf("at the limit: Run = %v, %s %q (exceeded %v); want %q", err, stream, out.Bytes, out.Exceeded, data)
				}
				res, err = subprocess.Run(ctx, spec, subprocess.RunOptions{MaxOutput: len(data) - 1})
				out := got(res)
				if herr.Kind(err) != subprocess.ReasonOutputLimit || !out.Exceeded || !bytes.Equal(out.Bytes, data[:len(data)-1]) {
					t.Fatalf("one byte over: Run = %v, %s %q (exceeded %v); want %s and the first %d bytes",
						err, stream, out.Bytes, out.Exceeded, subprocess.ReasonOutputLimit, len(data)-1)
				}
			})
		}
	}
}

// A stream the Spec sets to a file opened for append goes to the file without
// passing through Run: two runs leave both writes in it, in order.
//
//dsh:test DH-T-EXECA-3
func TestRunAppendsAStreamToAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "errors.log")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, text := range []string{"first run\n", "second run\n"} {
		spec := subprocesstest.Spec(t, subprocesstest.RoleWrite, "stderr", hex.EncodeToString([]byte(text)))
		spec.Stderr = f
		res, err := subprocess.Run(context.Background(), spec, subprocess.RunOptions{})
		if err != nil || res.ExitCode != 0 || len(res.Stderr.Bytes) != 0 {
			t.Fatalf("Run = %+v, %v; want exit 0 and nothing captured from the file stream", res, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first run\nsecond run\n" {
		t.Fatalf("file holds %q; want both runs in order", got)
	}
}

// treeGone reads "tree <child> <grandchild>" from what the tree child said and
// checks that both have exited.
func treeGone(t *testing.T, said string) {
	t.Helper()
	var child, grandchild int
	if _, err := fmt.Sscanf(said, "tree %d %d", &child, &grandchild); err != nil {
		t.Fatalf("the tree child said %q: %v", said, err)
	}
	// Exited, not !Alive: an orphaned grandchild is reaped by its reaper on its own
	// time, and until then kill(pid, 0) still finds the zombie.
	if !subprocesstest.Exited(child) || !subprocesstest.Exited(grandchild) {
		t.Fatalf("after Run: child %d exited %v, grandchild %d exited %v", child, subprocesstest.Exited(child),
			grandchild, subprocesstest.Exited(grandchild))
	}
}

// lineWriter keeps what is written and calls seen once, when the first line is
// complete.
type lineWriter struct {
	mu   sync.Mutex
	b    strings.Builder
	seen func()
	done bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b.Write(p)
	if !w.done && strings.Contains(w.b.String(), "\n") {
		w.done = true
		w.seen()
	}
	return len(p), nil
}

func (w *lineWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}
