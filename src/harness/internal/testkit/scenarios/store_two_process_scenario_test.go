//go:build scenario

package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

// storeStepTimeout bounds every process step of the store scenarios.
const storeStepTimeout = 60 * time.Second

var (
	storeSessionLine = regexp.MustCompile(`(?m)^session=(\S+)\s*$`)
	storePIDLine     = regexp.MustCompile(`(?m)^pid=(\d+)\s*$`)
)

// storeStep runs one bounded process step: a step that outlasts storeStepTimeout
// fails the scenario.
func storeStep(t *testing.T, what, line string) (string, error) {
	t.Helper()
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := shell(t, line)
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		return r.out, r.err
	case <-time.After(storeStepTimeout):
		t.Fatalf("%s: no result within %s: %s", what, storeStepTimeout, line)
		return "", nil
	}
}

// storeLog reads session id's log and summary straight from the home's database,
// failing on a seq gap or duplicate.
func storeLog(t *testing.T, root, id string) ([]store.Record, store.Summary) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), storeStepTimeout)
	defer cancel()
	s, err := sqlite.OpenHome(ctx, home.New(root), sqlite.WithKind("scenario"))
	if err != nil {
		t.Fatalf("opening the home's database: %v", err)
	}
	defer func() { _ = s.Close(ctx) }()
	sum, err := s.Stat(ctx, id)
	if err != nil {
		t.Fatalf("stat %s: %v", id, err)
	}
	r, err := s.OpenRead(ctx, id)
	if err != nil {
		t.Fatalf("reading %s: %v", id, err)
	}
	defer func() { _ = r.Close(ctx) }()
	recs, err := r.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil {
		t.Fatalf("reading %s: %v", id, err)
	}
	for i, rec := range recs {
		if rec.Seq != store.Seq(i) {
			t.Fatalf("record %d of %s has seq %d: the log has a gap or a duplicate", i, id, rec.Seq)
		}
	}
	return recs, sum
}

// TestTwoProcessesShareOneHomeWithOneWriter: two real dsh processes on one home
// have one writer per session and take a dead holder's session over safely
// (areas/session-store.md DH-E-STORE-1; DH-D-STORE-3, DH-D-STORE-2). Guarded;
// executed in DH-S-16 (step 16.8) on scratch homes; authored in DH-S-7.
//
// Given an empty home and processes A and B each started as a headless host on
// it with the replay model provider, when (1) A creates a session and runs a
// turn, (2) B tries to resume it, (3) A is killed by the operating system during
// a second turn, (4) B resumes the session and runs a turn and (5) a second copy
// of A is started and tries to write the session, then at (2) B is refused with
// session/owned naming A's process id and the log is unchanged; at (4) B's resume
// succeeds, the epoch has risen by one and the interrupted turn is closed with
// turn/end interrupted; at (5) the new process is refused with session/owned
// naming B; the log has seq 0 to n with no gap or duplicate, and A's lock file
// and hosts row are gone. Each process step has a 60 second timeout.
//
// Inputs are shell command lines over {home}, {proc} (a, b or a2) and {session}:
// DSH_SCENARIO_STORE1_START starts {proc} as a headless host on {home} in the
// foreground; DSH_SCENARIO_STORE1_PID prints "pid=<n>" for {proc};
// DSH_SCENARIO_STORE1_CREATE makes {proc} create a session, run one turn and
// print "session=<id>"; DSH_SCENARIO_STORE1_RESUME makes {proc} resume {session}
// and run one turn, exiting non-zero with the refusal on stdout/stderr when it is
// refused; DSH_SCENARIO_STORE1_BEGIN makes {proc} start a long turn on {session}
// and returns once the turn is recorded; DSH_SCENARIO_STORE1_KILL kills {proc}
// without a clean stop.
//
//dsh:scenario DH-E-STORE-1
func TestTwoProcessesShareOneHomeWithOneWriter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	startT := need(t, "DSH_SCENARIO_STORE1_START", "command starting {proc} as a headless host on {home}")
	pidT := need(t, "DSH_SCENARIO_STORE1_PID", "command printing pid=<n> for {proc}")
	createT := need(t, "DSH_SCENARIO_STORE1_CREATE", "command making {proc} create a session and run a turn")
	resumeT := need(t, "DSH_SCENARIO_STORE1_RESUME", "command making {proc} resume {session} and run a turn")
	beginT := need(t, "DSH_SCENARIO_STORE1_BEGIN", "command making {proc} start a long turn on {session}")
	killT := need(t, "DSH_SCENARIO_STORE1_KILL", "command killing {proc} without a clean stop")
	var id string
	with := func(proc string) map[string]string {
		return map[string]string{"home": root, "proc": proc, "session": id}
	}
	pidOf := func(proc string) int {
		out, err := storeStep(t, proc+" pid", expand(pidT, with(proc)))
		m := storePIDLine.FindStringSubmatch(out)
		if err != nil || m == nil {
			t.Fatalf("no pid for %s: %v\n%s", proc, err, out)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	refusedNaming := func(out string, pid int) bool {
		return strings.Contains(out, "session/owned") && strings.Contains(out, strconv.Itoa(pid))
	}
	if err := home.Ensure(home.New(root)); err != nil {
		t.Fatal(err)
	}
	keep := false
	t.Cleanup(func() {
		if !keep && !t.Failed() {
			_ = os.RemoveAll(root)
		} else {
			t.Logf("kept the home at %s", root)
		}
	})

	a := startShell(t, expand(startT, with("a")))
	b := startShell(t, expand(startT, with("b")))
	pidA, pidB := pidOf("a"), pidOf("b")

	// (1)
	out, err := storeStep(t, "A creates", expand(createT, with("a")))
	m := storeSessionLine.FindStringSubmatch(out)
	if err != nil || m == nil {
		t.Fatalf("A could not create a session: %v\n%s", err, out)
	}
	id = m[1]
	before, sum1 := storeLog(t, root, id)

	// (2)
	out, err = storeStep(t, "B resumes A's session", expand(resumeT, with("b")))
	if err == nil || !refusedNaming(out, pidA) {
		t.Fatalf("B was not refused with session/owned naming A (pid %d): %v\n%s", pidA, err, out)
	}
	if after, _ := storeLog(t, root, id); len(after) != len(before) {
		t.Fatalf("the refused resume changed the log: %d records, then %d", len(before), len(after))
	}

	// (3)
	if out, err := storeStep(t, "A begins a second turn", expand(beginT, with("a"))); err != nil {
		t.Fatalf("A could not begin a second turn: %v\n%s", err, out)
	}
	if out, err := storeStep(t, "kill A", expand(killT, with("a"))); err != nil {
		t.Fatalf("killing A: %v\n%s", err, out)
	}
	deadline := time.Now().Add(storeStepTimeout)
	for !a.exited() {
		if time.Now().After(deadline) {
			t.Fatal("A did not exit after the kill")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// (4)
	if out, err := storeStep(t, "B resumes", expand(resumeT, with("b"))); err != nil {
		t.Fatalf("B's resume after A died failed: %v\n%s", err, out)
	}
	recs, sum4 := storeLog(t, root, id)
	if sum4.LeaseEpoch != sum1.LeaseEpoch+1 {
		t.Fatalf("epoch = %d after the takeover, want %d", sum4.LeaseEpoch, sum1.LeaseEpoch+1)
	}
	if sum4.Holder == nil || sum4.Holder.PID != pidB {
		t.Fatalf("holder = %+v after the takeover, want B (pid %d)", sum4.Holder, pidB)
	}
	interrupted := false
	for _, r := range recs {
		if r.Type == "turn/end" && strings.Contains(r.Payload, "interrupted") {
			interrupted = true
		}
	}
	if !interrupted {
		keep = true
		t.Fatal("the turn A was killed in is not closed with turn/end interrupted")
	}
	if locks, _ := filepath.Glob(filepath.Join(home.New(root).Run(), strconv.Itoa(pidA)+"-*.lock")); len(locks) != 0 {
		t.Fatalf("A's lock file is still there: %v", locks)
	}

	// (5)
	a2 := startShell(t, expand(startT, with("a2")))
	pidOf("a2")
	out, err = storeStep(t, "the new A resumes", expand(resumeT, with("a2")))
	if err == nil || !refusedNaming(out, pidB) {
		t.Fatalf("the new A was not refused with session/owned naming B (pid %d): %v\n%s", pidB, err, out)
	}
	final, sum5 := storeLog(t, root, id)
	if len(final) != len(recs) || sum5.LeaseEpoch != sum4.LeaseEpoch {
		keep = true
		t.Fatalf("the refused write changed the session: %d records epoch %d, then %d epoch %d", len(recs), sum4.LeaseEpoch, len(final), sum5.LeaseEpoch)
	}

	for _, p := range []string{"a2", "b"} {
		_, _ = storeStep(t, "stop "+p, expand(killT, with(p)))
	}
	_, _ = a2, b
}
