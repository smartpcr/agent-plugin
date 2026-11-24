package local_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// TestGuardedWritesThroughDistinctProvidersHaveOneWinner: providers that share
// nothing but the lock directory (as two harness processes do) all write with
// the same version; the guard check and the publication are one step, so
// exactly one wins and the rest are fs/stale-version.
//
//dsh:test DH-T-FS-13
func TestGuardedWritesThroughDistinctProvidersHaveOneWinner(t *testing.T) {
	const writers = 32
	ctx := context.Background()
	dir, locks := t.TempDir(), t.TempDir()
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, dir)
	f := filepath.Join(dir, "f")
	if _, err := local.New().Write(ctx, fs.Ref{Path: f}, []byte("v1"), fs.NoGuard, p); err != nil {
		t.Fatal(err)
	}
	v1, _ := readVersion(t, local.New(), f)

	var wg sync.WaitGroup
	errs := make([]error, writers)
	start := make(chan struct{})
	for i := range writers {
		l := local.NewWithOptions(local.Options{LockDir: locks})
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = l.Write(ctx, fs.Ref{Path: f}, []byte("writer "+strconv.Itoa(i)), fs.ReplaceIfVersion{Version: v1}, p)
		}()
	}
	close(start)
	wg.Wait()

	won := -1
	for i, err := range errs {
		switch code := herr.Kind(err); {
		case err == nil:
			if won >= 0 {
				t.Fatalf("writers %d and %d both won with v1", won, i)
			}
			won = i
		case code != fs.CodeStaleVersion:
			t.Fatalf("writer %d: %v (code %q), want %q", i, err, code, fs.CodeStaleVersion)
		}
	}
	if won < 0 {
		t.Fatal("no writer won")
	}
	if b, _ := os.ReadFile(f); string(b) != "writer "+strconv.Itoa(won) {
		t.Fatalf("file holds %q, want the winner's bytes", b)
	}
}

const (
	helperEnv  = "DSH_FS_LOCAL_COUNTER_HELPER"
	helperRuns = 40
)

// TestGuardedWritesSerialiseAcrossProcesses: two helper processes and this one
// each add 1 to a counter file helperRuns times by read, guarded write, retry
// on fs/stale-version. Were the guard not taken under a cross-process lock, two
// processes would both publish over the same version and an increment would be
// lost.
//
//dsh:test DH-T-FS-13
func TestGuardedWritesSerialiseAcrossProcesses(t *testing.T) {
	if os.Getenv(helperEnv) != "" {
		if err := countUp(os.Getenv(helperEnv), os.Getenv(helperEnv+"_LOCKS")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	dir, locks := t.TempDir(), t.TempDir()
	f := filepath.Join(dir, "counter")
	if err := os.WriteFile(f, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const helpers = 2
	cmds := make([]*exec.Cmd, helpers)
	outs := make([]bytes.Buffer, helpers)
	for i := range cmds {
		cmds[i] = exec.Command(exe, "-test.run=^TestGuardedWritesSerialiseAcrossProcesses$", "-test.count=1")
		cmds[i].Env = append(os.Environ(), helperEnv+"="+f, helperEnv+"_LOCKS="+locks)
		cmds[i].Stdout, cmds[i].Stderr = &outs[i], &outs[i]
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	if err := countUp(f, locks); err != nil {
		t.Error(err)
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, outs[i].String())
		}
	}
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if want := strconv.Itoa((helpers + 1) * helperRuns); string(b) != want {
		t.Fatalf("counter = %s, want %s: an increment was lost", b, want)
	}
}

// countUp adds 1 to the counter file f helperRuns times, each a guarded
// read-modify-write retried on fs/stale-version.
func countUp(f, locks string) error {
	ctx := context.Background()
	l := local.NewWithOptions(local.Options{LockDir: locks})
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, filepath.Dir(f))
	for range helperRuns {
		for tries := 0; ; tries++ {
			if tries == 10000 {
				return fmt.Errorf("pid %d: no write landed in %d tries", os.Getpid(), tries)
			}
			rc, in, err := l.Open(ctx, fs.Ref{Path: f})
			if err != nil {
				continue // the file is being replaced
			}
			b := make([]byte, 32)
			n, _ := rc.Read(b)
			_ = rc.Close()
			v, err := strconv.Atoi(string(b[:n]))
			if err != nil {
				return fmt.Errorf("pid %d: counter %q: %v", os.Getpid(), b[:n], err)
			}
			_, err = l.Write(ctx, fs.Ref{Path: f}, []byte(strconv.Itoa(v+1)), fs.ReplaceIfVersion{Version: in.Version}, p)
			if err == nil {
				break
			}
			if herr.Kind(err) != fs.CodeStaleVersion {
				return fmt.Errorf("pid %d: write: %v", os.Getpid(), err)
			}
		}
	}
	return nil
}
