package sqlite_test

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// The re-exec'd children of the follow, duty and deletion tests (DH-T-STORE-11,
// DH-T-STORE-14). Each is TestMaintChild with a role:
//
//   - append: creates session "f" and reports "ready"; on "go" it appends 10
//     events, one batch each, reporting "committed seq=<n> at=<unix ns>" after
//     each commit.
//   - duty: runs duty "d" (every hour) and reports "duty ran=<bool>
//     instance=<id>".
//   - delete: deletes session DSH_SQLITE_MAINT_ARG with chunks of 100 events and
//     the spill directory DSH_SQLITE_MAINT_SPILL; after the chunk that brings the
//     removed events to 500 or more it reports "chunk deleted=<n>" and stops
//     there, as a process killed mid-deletion would.
//
// None of them closes its store: the parent kills them.
const (
	maintChildEnv = "DSH_SQLITE_MAINT_CHILD"
	maintRoleEnv  = "DSH_SQLITE_MAINT_ROLE"
	maintArgEnv   = "DSH_SQLITE_MAINT_ARG"
	maintSpillEnv = "DSH_SQLITE_MAINT_SPILL"
)

func TestMaintChild(t *testing.T) {
	path := os.Getenv(maintChildEnv)
	if path == "" {
		t.Skip("the child of the follow, duty and deletion tests")
	}
	in := bufio.NewScanner(os.Stdin)
	s, err := sqlite.Open(bg, path, sqlite.WithKind("child"), sqlite.WithDeleteChunk(100),
		sqlite.WithSpillDir(os.Getenv(maintSpillEnv)))
	if err != nil {
		t.Fatal(err)
	}
	// Reachable until the parent kills the child: a collected store drops its
	// liveness lock (see TestMigrateChild).
	defer runtime.KeepAlive(s)
	switch os.Getenv(maintRoleEnv) {
	case "append":
		w, err := s.Create(bg, store.Header{ID: "f"}, store.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout.WriteString("ready\n")
		for in.Scan() && in.Text() != "go" {
		}
		for seq := store.Seq(0); seq < 10; seq++ {
			if err := w.Append(bg, store.Batch{First: seq, Records: []store.Record{call(t, seq)}}); err != nil {
				t.Fatal(err)
			}
			os.Stdout.WriteString("committed seq=" + strconv.Itoa(int(seq)) + " at=" + strconv.FormatInt(time.Now().UnixNano(), 10) + "\n")
			<-time.After(20 * time.Millisecond) // spaced commits, so the follower polls between them
		}
	case "duty":
		ran, err := s.RunDuty(bg, "d", time.Hour, func(_ context.Context) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout.WriteString("duty ran=" + strconv.FormatBool(ran) + " instance=" + s.Instance() + "\n")
	case "delete":
		stopped := make(chan struct{})
		sqlite.SetAfterDeleteChunk(func(id string, deleted int64) {
			if deleted >= 500 {
				os.Stdout.WriteString("chunk deleted=" + strconv.FormatInt(deleted, 10) + "\n")
				close(stopped)
				select {} // killed here, between two chunks
			}
		})
		done := make(chan struct{})
		t.Cleanup(func() { <-done })
		go func() {
			defer close(done)
			err := s.Delete(bg, os.Getenv(maintArgEnv), store.DeleteOptions{})
			os.Stdout.WriteString("deleted err=" + strconv.Quote(errString(err)) + "\n")
		}()
		select {
		case <-stopped:
		case <-done:
		}
	default:
		t.Fatalf("unknown role %q", os.Getenv(maintRoleEnv))
	}
	for in.Scan() {
	}
	t.Fatal("the parent did not kill the child")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// spawnMaint starts TestMaintChild on path in role.
func spawnMaint(t *testing.T, path, role, arg, spill string) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMaintChild$", "-test.count=1", "-test.v")
	cmd.Env = append(childEnviron(), maintChildEnv+"="+path, maintRoleEnv+"="+role, maintArgEnv+"="+arg,
		maintSpillEnv+"="+spill)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &child{t: t, cmd: cmd, stdin: stdin, out: bufio.NewScanner(out)}
	t.Cleanup(func() { stop(cmd) })
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return c
}

// lines sends the child's report lines that start with prefix on a channel,
// read on their own goroutine.
func (c *child) lines(prefix string) <-chan string {
	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		for c.out.Scan() {
			if strings.HasPrefix(c.out.Text(), prefix) {
				ch <- c.out.Text()
			}
		}
	}()
	return ch
}
