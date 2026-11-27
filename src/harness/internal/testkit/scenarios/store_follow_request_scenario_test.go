//go:build scenario

package scenarios

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/storage/home until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

var (
	storeFollowLine  = regexp.MustCompile(`(?m)^seq=(\d+) type=(\S+) at=(\d+)\s*$`)
	storeRequestLine = regexp.MustCompile(`(?m)^request=(\S+)\s*$`)
	storeStateLine   = regexp.MustCompile(`(?m)^state=(\S+) by=(\d*)\s*$`)
	storeFollowReady = regexp.MustCompile(`(?m)^following from=(\d+)\s*$`)
)

// followed is one event a follower printed: its seq, type and the wall-clock
// moment (unix nanoseconds) the follower received it.
type followed struct {
	seq int
	typ string
	at  time.Time
}

// readFollowed parses the follower's output file; a missing file is no events.
func readFollowed(t *testing.T, path string) []followed {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var got []followed
	for _, m := range storeFollowLine.FindAllStringSubmatch(string(b), -1) {
		seq, _ := strconv.Atoi(m[1])
		ns, _ := strconv.ParseInt(m[3], 10, 64)
		got = append(got, followed{seq: seq, typ: m[2], at: time.Unix(0, ns)})
	}
	return got
}

// TestNonHolderFollowsAndHandsItsHolderAChange: a process that does not hold a
// session follows it live and hands its holder changes through requests, and a
// request whose holder dies is applied exactly once by the process that takes
// the lease over (areas/session-store.md DH-E-STORE-5; DH-D-STORE-8,
// DH-D-STORE-11). Executed in DH-S-35 (step 35.7); authored in DH-S-7, whose
// Stage 7.3 builds the follow it proves; the request path is built later.
//
// Given an empty home and processes A and B each started as a headless host on
// it with the replay model provider, when (1) A creates a session and runs a
// turn, (2) B follows the session read-only while A runs a second turn, (3) B
// files one log-only request (a feedback entry) for it and (4) A is paused from
// applying requests, B files a second request (which stays pending), A is killed
// before it applies it and B takes the free session's lease through a
// maintenance append, then at (2) B receives every event of the turn
// once, in order, each within twice the follow interval of its commit; at (3) the
// request becomes applied and its feedback event is in the log once; at (4) the
// second request is applied exactly once, by B, and its row says so; the log has
// no seq gap or duplicate. Each process step has a 60 second timeout.
//
// Inputs are shell command lines over {home}, {proc} (a, b or follower, the last
// only for the kill), {session}, {out}
// and {request}: DSH_SCENARIO_STORE5_START starts {proc} as a headless host on
// {home} in the foreground; DSH_SCENARIO_STORE5_PID prints "pid=<n>" for {proc};
// DSH_SCENARIO_STORE5_CREATE makes {proc} create a session, run one turn and
// print "session=<id>"; DSH_SCENARIO_STORE5_TURN makes {proc} run one turn on
// {session}; DSH_SCENARIO_STORE5_FOLLOW makes {proc} follow {session} from its
// next seq, appending first "following from=<n>" once subscribed, then "seq=<n> type=<t> at=<unix ns>" per event received to
// {out}, until it is stopped; DSH_SCENARIO_STORE5_FEEDBACK makes {proc} file a
// feedback request for {session} and print "request=<id>";
// DSH_SCENARIO_STORE5_PAUSE makes {proc} stop applying requests and returns once
// it has (requests filed after stay pending while it lives);
// DSH_SCENARIO_STORE5_STATE prints "state=<s> by=<pid>" for {request};
// DSH_SCENARIO_STORE5_MAINTAIN makes {proc} take {session}'s lease through a
// maintenance append; DSH_SCENARIO_STORE5_KILL kills {proc} without a clean stop.
// DSH_SCENARIO_STORE5_INTERVAL is the follow interval as a Go duration.
//
//dsh:scenario DH-E-STORE-5
func TestNonHolderFollowsAndHandsItsHolderAChange(t *testing.T) {
	startT := need(t, "DSH_SCENARIO_STORE5_START", "command starting {proc} as a headless host on {home}")
	pidT := need(t, "DSH_SCENARIO_STORE5_PID", "command printing pid=<n> for {proc}")
	createT := need(t, "DSH_SCENARIO_STORE5_CREATE", "command making {proc} create a session and run a turn")
	turnT := need(t, "DSH_SCENARIO_STORE5_TURN", "command making {proc} run a turn on {session}")
	followT := need(t, "DSH_SCENARIO_STORE5_FOLLOW", "command making {proc} follow {session} into {out}")
	feedbackT := need(t, "DSH_SCENARIO_STORE5_FEEDBACK", "command making {proc} file a feedback request for {session}")
	pauseT := need(t, "DSH_SCENARIO_STORE5_PAUSE", "command making {proc} stop applying requests")
	stateT := need(t, "DSH_SCENARIO_STORE5_STATE", "command printing state=<s> by=<pid> for {request}")
	maintainT := need(t, "DSH_SCENARIO_STORE5_MAINTAIN", "command making {proc} take {session}'s lease by a maintenance append")
	killT := need(t, "DSH_SCENARIO_STORE5_KILL", "command killing {proc} without a clean stop")
	interval, err := time.ParseDuration(need(t, "DSH_SCENARIO_STORE5_INTERVAL", "the follow interval as a Go duration"))
	if err != nil || interval <= 0 {
		t.Fatalf("DSH_SCENARIO_STORE5_INTERVAL: %v", err)
	}

	dir := t.TempDir()
	root, out := filepath.Join(dir, "home"), filepath.Join(dir, "followed.txt")
	var id, req string
	with := func(proc string) map[string]string {
		return map[string]string{"home": root, "proc": proc, "session": id, "out": out, "request": req}
	}
	pidOf := func(proc string) int {
		o, err := storeStep(t, proc+" pid", expand(pidT, with(proc)))
		m := storePIDLine.FindStringSubmatch(o)
		if err != nil || m == nil {
			t.Fatalf("no pid for %s: %v\n%s", proc, err, o)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	file := func() string {
		o, err := storeStep(t, "B files a request", expand(feedbackT, with("b")))
		m := storeRequestLine.FindStringSubmatch(o)
		if err != nil || m == nil {
			t.Fatalf("B could not file a request: %v\n%s", err, o)
		}
		return m[1]
	}
	awaitApplied := func(want int) {
		deadline := time.Now().Add(storeStepTimeout)
		for {
			o, err := storeStep(t, "request state", expand(stateT, with("b")))
			m := storeStateLine.FindStringSubmatch(o)
			if err != nil || m == nil {
				t.Fatalf("no state for request %s: %v\n%s", req, err, o)
			}
			if m[1] == "applied" {
				if by, _ := strconv.Atoi(m[2]); want != 0 && by != want {
					t.Fatalf("request %s was applied by pid %s, want %d", req, m[2], want)
				}
				return
			}
			if m[1] != "pending" || time.Now().After(deadline) {
				t.Fatalf("request %s is %s, want applied", req, m[1])
			}
			<-time.After(100 * time.Millisecond)
		}
	}
	feedbackOnce := func(what string) {
		recs, _ := storeLog(t, root, id)
		n := 0
		for _, r := range recs {
			if strings.Contains(r.Type, "feedback") && strings.Contains(r.Payload, req) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%s: the log has %d feedback events of request %s, want 1", what, n, req)
		}
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
	pidOf("a")
	pidB := pidOf("b")

	// (1)
	o, err := storeStep(t, "A creates", expand(createT, with("a")))
	m := storeSessionLine.FindStringSubmatch(o)
	if err != nil || m == nil {
		t.Fatalf("A could not create a session: %v\n%s", err, o)
	}
	id = m[1]
	before, _ := storeLog(t, root, id)

	// (2)
	follower := startShell(t, expand(followT, with("b")))
	// The follower starts from the session's next seq; A's turn must not begin
	// before it has subscribed, or the turn's first events precede its start.
	for deadline := time.Now().Add(storeStepTimeout); ; <-time.After(interval) {
		b, _ := os.ReadFile(out)
		if m := storeFollowReady.FindSubmatch(b); m != nil {
			if from, _ := strconv.Atoi(string(m[1])); from != len(before) {
				t.Fatalf("B follows from seq %d, want %d (the session's next seq)", from, len(before))
			}
			break
		}
		if follower.exited() || time.Now().After(deadline) {
			t.Fatalf("B's follower never reported it was subscribed")
		}
	}
	if o, err := storeStep(t, "A runs a second turn", expand(turnT, with("a"))); err != nil {
		t.Fatalf("A's second turn failed: %v\n%s", err, o)
	}
	turn, _ := storeLog(t, root, id)
	turn = turn[len(before):]
	var got []followed
	for deadline := time.Now().Add(storeStepTimeout); len(got) < len(turn); <-time.After(interval) {
		if got = readFollowed(t, out); time.Now().After(deadline) || follower.exited() {
			break
		}
	}
	if len(got) != len(turn) {
		keep = true
		t.Fatalf("B received %d events of the turn, want %d: %+v", len(got), len(turn), got)
	}
	for i, r := range turn {
		g := got[i]
		if g.seq != int(r.Seq) || g.typ != r.Type {
			keep = true
			t.Fatalf("B's event %d is seq %d %s, want seq %d %s", i, g.seq, g.typ, r.Seq, r.Type)
		}
		// The event's time is stamped before its commit, in milliseconds, so the measure
		// is conservative by the commit's own duration.
		if late := g.at.Sub(time.UnixMilli(r.Time)); late > 2*interval+time.Millisecond {
			keep = true
			t.Fatalf("B received seq %d %s after it was stamped, over twice the interval %s", r.Seq, late, interval)
		}
	}
	_, _ = storeStep(t, "stop following", expand(killT, with("follower")))

	// (3)
	req = file()
	awaitApplied(0)
	feedbackOnce("the first request")

	// (4)
	if o, err := storeStep(t, "pause A", expand(pauseT, with("a"))); err != nil {
		t.Fatalf("A could not be paused from applying requests: %v\n%s", err, o)
	}
	req = file()
	if o, err := storeStep(t, "request state", expand(stateT, with("b"))); err != nil || !strings.HasPrefix(strings.TrimSpace(o), "state=pending") {
		t.Fatalf("request %s filed while A lives and is paused: %v\n%s; want pending", req, err, o)
	}
	if _, err := storeStep(t, "kill A", expand(killT, with("a"))); err != nil {
		t.Fatalf("killing A: %v", err)
	}
	for deadline := time.Now().Add(storeStepTimeout); !a.exited(); <-time.After(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("A did not exit after the kill")
		}
	}
	if o, err := storeStep(t, "B takes the lease", expand(maintainT, with("b"))); err != nil {
		t.Fatalf("B could not take the free session's lease: %v\n%s", err, o)
	}
	awaitApplied(pidB)
	feedbackOnce("the second request")
	storeLog(t, root, id)

	_, _ = storeStep(t, "stop B", expand(killT, with("b")))
	_, _ = b, follower
}
