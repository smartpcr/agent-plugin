package sqlite_test

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// The re-exec'd children of the lease tests (DH-T-STORE-6, DH-T-STORE-7). Each
// is TestLeaseChild with a role:
//
//   - hold: holds the lease of session "s" (creating it) after three appends and
//     reports "holding instance=<id> epoch=<n> pid=<pid>"; then, per "append" on
//     stdin, appends one event and reports "append ok" or "append err=<code>".
//   - open: opens the store, reports "ready instance=<id>", waits for "go" and
//     tries OpenWrite on "s", reporting "result opened epoch=<n>" or "result
//     refused holder=<id> pid=<pid>"; it then keeps what it got until killed.
//   - read: reads the file DSH_SQLITE_LEASE_ARG through os and reports "result
//     read n=<bytes>" or "result error <err>".
//
// None of them closes its store: the parent kills them.
const (
	leaseChildEnv = "DSH_SQLITE_LEASE_CHILD"
	leaseRoleEnv  = "DSH_SQLITE_LEASE_ROLE"
	leaseArgEnv   = "DSH_SQLITE_LEASE_ARG"
)

func TestLeaseChild(t *testing.T) {
	path := os.Getenv(leaseChildEnv)
	if path == "" {
		t.Skip("the child of the lease tests")
	}
	in := bufio.NewScanner(os.Stdin)
	if os.Getenv(leaseRoleEnv) == "read" {
		data, err := os.ReadFile(os.Getenv(leaseArgEnv))
		if err != nil {
			fmt.Printf("result error %v\n", err)
			return
		}
		fmt.Printf("result read n=%d\n", len(data))
		return
	}
	s, err := sqlite.Open(bg, path, sqlite.WithKind("child"))
	if err != nil {
		t.Fatal(err)
	}
	switch os.Getenv(leaseRoleEnv) {
	case "hold":
		w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		next := store.Seq(0)
		for ; next < 3; next++ {
			if err := w.Append(bg, store.Batch{First: next, Records: []store.Record{call(t, next)}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Flush(bg); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("holding instance=%s epoch=%d pid=%d\n", s.Instance(), w.Epoch(), os.Getpid())
		for in.Scan() {
			if in.Text() != "append" {
				continue
			}
			if err := w.Append(bg, store.Batch{First: next, Records: []store.Record{call(t, next)}}); err != nil {
				fmt.Printf("append err=%s\n", codeOf(err))
				continue
			}
			next++
			fmt.Println("append ok")
		}
	case "open":
		fmt.Printf("ready instance=%s\n", s.Instance())
		for in.Scan() && in.Text() != "go" {
		}
		w, err := s.OpenWrite(bg, "s", store.OpenOptions{})
		var owned *store.OwnedError
		switch {
		case err == nil:
			fmt.Printf("result opened epoch=%d\n", w.Epoch())
		case errors.As(err, &owned):
			fmt.Printf("result refused holder=%s pid=%d\n", owned.Holder.Instance, owned.Holder.PID)
		default:
			fmt.Printf("result error %s %v\n", codeOf(err), err)
		}
		for in.Scan() {
		}
	default:
		t.Fatalf("unknown role %q", os.Getenv(leaseRoleEnv))
	}
	t.Fatal("the parent did not kill the child")
}

// codeOf is the harness code of err.
func codeOf(err error) store.Code {
	var owned *store.OwnedError
	if errors.As(err, &owned) {
		return store.CodeOwned
	}
	var se *store.Error
	if errors.As(err, &se) {
		return se.Code
	}
	return "?"
}

// spawn starts TestLeaseChild on path in role, with arg.
func spawn(t *testing.T, path, role, arg string) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLeaseChild$", "-test.count=1", "-test.v")
	cmd.Env = append(childEnviron(), leaseChildEnv+"="+path, leaseRoleEnv+"="+role, leaseArgEnv+"="+arg)
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

// field is the value of key=<value> in a child's report line.
func field(t *testing.T, line, key string) string {
	t.Helper()
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	t.Fatalf("%q has no %s", line, key)
	return ""
}

// holdIn starts a child holding "s" on path and returns it with its instance id.
func holdIn(t *testing.T, path string) (*child, string) {
	t.Helper()
	a := spawn(t, path, "hold", "")
	line := a.expect("holding")
	if field(t, line, "epoch") != "1" {
		t.Fatalf("holder reported %q; want epoch 1", line)
	}
	return a, field(t, line, "instance")
}

// DH-T-STORE-6 / TC-07: while process A holds session S, a second writer is
// refused, from another process and from this one, with A's identity, and S's
// log holds only A's events.
func TestSecondWriterRefusedAcrossProcesses(t *testing.T) {
	path := dbPath(t)
	a, inst := holdIn(t, path)
	pid := a.cmd.Process.Pid

	b := spawn(t, path, "open", "")
	b.expect("ready")
	b.send("go")
	if line := b.expect("result"); !strings.HasPrefix(line, "result refused") ||
		field(t, line, "holder") != inst || field(t, line, "pid") != fmt.Sprint(pid) {
		t.Fatalf("process B: %q; want refused naming A (%s, pid %d)", line, inst, pid)
	}

	s := open(t, path)
	_, err := s.OpenWrite(bg, "s", store.OpenOptions{})
	var owned *store.OwnedError
	if !errors.As(err, &owned) || !errors.Is(err, store.ErrOwned) || owned.Holder.Instance != inst ||
		owned.Holder.PID != pid || owned.Holder.Kind != "child" || owned.Holder.Since.IsZero() {
		t.Fatalf("OpenWrite in this process = %v; want session/owned naming A", err)
	}
	if err := s.Delete(bg, "s", store.DeleteOptions{}); !errors.Is(err, store.ErrOwned) {
		t.Fatalf("Delete while A lives = %v", err)
	}
	sum, err := s.Stat(bg, "s")
	if err != nil || sum.NextSeq != 3 || sum.LeaseEpoch != 1 || sum.Holder == nil || sum.Holder.Instance != inst {
		t.Fatalf("after the refusals: %+v, %v; want A's three events at epoch 1", sum, err)
	}
	if events, next := countEvents(t, fresh(t, path), "s"); events != 3 || next != 3 {
		t.Fatalf("the log holds %d events (next %d); want only A's 3", events, next)
	}
	a.send("append")
	if line := a.expect("append"); line != "append ok" {
		t.Fatalf("A after the refusals: %q", line)
	}
}

// DH-T-STORE-6 / DH-T-STORE-7 / TC-07: once process A holding S is killed, B
// acquires A's liveness lock inside the write transaction of OpenWrite, takes
// the lease at epoch+1 and removes A's lock file and hosts row; an append still
// carrying A's epoch writes nothing.
func TestDeadHolderTakenOver(t *testing.T) {
	path := dbPath(t)
	a, inst := holdIn(t, path)
	// B opens while A lives, so its sweep at open leaves A alone and the
	// takeover below is OpenWrite's probe.
	b := open(t, path)
	lock := sqlite.LockPath(b, inst)
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("A's liveness lock file: %v", err)
	}
	if n := countWhere(t, fresh(t, path), `SELECT count(*) FROM hosts WHERE instance = ?`, inst); n != 1 {
		t.Fatalf("A's hosts rows: %d", n)
	}
	a.kill()

	w, err := b.OpenWrite(bg, "s", store.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenWrite after A died: %v", err)
	}
	defer w.Close(bg)
	if w.Epoch() != 2 {
		t.Fatalf("epoch %d; want 2", w.Epoch())
	}
	sum, err := b.Stat(bg, "s")
	if err != nil || sum.LeaseEpoch != 2 || sum.Holder == nil || sum.Holder.Instance != b.Instance() || sum.NextSeq != 3 {
		t.Fatalf("after the takeover: %+v, %v", sum, err)
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("A's lock file after the takeover: %v; want it gone", err)
	}
	if n := countWhere(t, fresh(t, path), `SELECT count(*) FROM hosts WHERE instance = ?`, inst); n != 0 {
		t.Fatalf("A's hosts row survived the takeover")
	}

	stale, err := sqlite.StaleWriter(bg, b, "s", inst, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.Append(bg, store.Batch{First: 3, Records: []store.Record{call(t, 3)}}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("an append with A's epoch = %v; want session/lease-lost", err)
	}
	if err := stale.Flush(bg); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("a flush with A's epoch = %v; want session/lease-lost", err)
	}
	if events, next := countEvents(t, fresh(t, path), "s"); events != 3 || next != 3 {
		t.Fatalf("the stale append wrote: %d events, next %d", events, next)
	}
	// A's records were stamped by A's clock; this process's call counter may
	// lag it.
	ms += 1_000_000
	if err := w.Append(bg, store.Batch{First: 3, Records: []store.Record{call(t, 3)}}); err != nil {
		t.Fatalf("the new holder's append: %v", err)
	}
}

// DH-T-STORE-6: two processes racing for a dead holder's lease produce one
// winner; the loser is refused naming it.
func TestRaceForDeadLeaseHasOneWinner(t *testing.T) {
	path := dbPath(t)
	a, _ := holdIn(t, path)
	a.kill()
	racers := []*child{spawn(t, path, "open", ""), spawn(t, path, "open", "")}
	ids := make([]string, len(racers))
	for i, c := range racers {
		ids[i] = field(t, c.expect("ready"), "instance")
	}
	for _, c := range racers {
		c.send("go")
	}
	won, refused := -1, -1
	var lines []string
	for i, c := range racers {
		line := c.expect("result")
		lines = append(lines, line)
		switch {
		case strings.HasPrefix(line, "result opened"):
			if won >= 0 {
				t.Fatalf("two winners: %q", lines)
			}
			won = i
			if field(t, line, "epoch") != "2" {
				t.Fatalf("winner %q; want epoch 2", line)
			}
		case strings.HasPrefix(line, "result refused"):
			refused = i
		default:
			t.Fatalf("racer %d: %q", i, line)
		}
	}
	if won < 0 || refused < 0 {
		t.Fatalf("want one winner and one refusal: %q", lines)
	}
	if got := field(t, lines[refused], "holder"); got != ids[won] {
		t.Fatalf("the loser was refused naming %s; want the winner %s", got, ids[won])
	}
	sum, err := open(t, path).Stat(bg, "s")
	if err != nil || sum.LeaseEpoch != 2 || sum.Holder == nil || sum.Holder.Instance != ids[won] {
		t.Fatalf("after the race: %+v, %v", sum, err)
	}
}

// DH-T-STORE-6: a forced takeover from a live holder in another process works,
// and the displaced holder's next append is session/lease-lost and writes
// nothing.
func TestForcedTakeoverAcrossProcesses(t *testing.T) {
	path := dbPath(t)
	a, _ := holdIn(t, path)
	b := open(t, path)
	w, err := b.OpenWrite(bg, "s", store.OpenOptions{Takeover: store.Force})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(bg)
	if w.Epoch() != 2 {
		t.Fatalf("forced epoch %d; want 2", w.Epoch())
	}
	a.send("append")
	if line := a.expect("append"); line != "append err="+string(store.CodeLeaseLost) {
		t.Fatalf("the displaced holder's append: %q", line)
	}
	if events, next := countEvents(t, fresh(t, path), "s"); events != 3 || next != 3 {
		t.Fatalf("the displaced append wrote: %d events, next %d", events, next)
	}
}

// DH-T-STORE-7: while an instance lives, opening and closing the database file
// (and its lock file) through os in its process does not release its liveness
// lock, and another process can read the lock file.
func TestLivenessLockSurvivesOSOpenAndClose(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(bg)
	lock := sqlite.LockPath(s, s.Instance())
	for _, p := range []string{path, lock} {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.ReadFile(p); err != nil {
			t.Fatalf("reading %s through os: %v", p, err)
		}
	}
	b := spawn(t, path, "open", "")
	b.expect("ready")
	b.send("go")
	if line := b.expect("result"); !strings.HasPrefix(line, "result refused") || field(t, line, "holder") != s.Instance() {
		t.Fatalf("after os open and close: %q; want refused naming this instance", line)
	}
	r := spawn(t, path, "read", lock)
	if line := r.expect("result"); !strings.HasPrefix(line, "result read") {
		t.Fatalf("another process reading the lock file: %q", line)
	}
}

// The liveness lock lives from Open to Close: Close removes the lock file.
func TestCloseReleasesTheLivenessLock(t *testing.T) {
	path := dbPath(t)
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	lock := sqlite.LockPath(s, s.Instance())
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("lock file while open: %v", err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock file after Close: %v; want it gone", err)
	}
}
