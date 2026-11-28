//go:build scenario

package scenarios

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/util/fsx until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// nodefsRounds is the number of power cuts per machine (DH-E-NODEFS-1).
const nodefsRounds = 100

const (
	// nodefsWriterUp bounds the time from DSH_SCENARIO_NODEFS_START to the round's
	// first new acknowledgement: the writer of a guest just restarted from a cut
	// (a Windows guest boots for minutes) must be replacing the file before the cut.
	nodefsWriterUp = 5 * time.Minute
	// nodefsCutWithin: the cut comes at a random moment within this after that
	// acknowledgement, while the loop replaces the file.
	nodefsCutWithin = 5 * time.Second
	// nodefsConfirmWithin bounds the recorder's write of its confirmation to a
	// writer; a writer that is gone does not hold the recorder up.
	nodefsConfirmWithin = 10 * time.Second
)

// nodefsHeader starts every version the writer publishes; the payload follows it.
// A guest agent that prints the file with Windows line endings turns the header's
// newline into CRLF, which is accepted.
var nodefsHeader = regexp.MustCompile(`^dsh-nodefs v=(\d+) len=(\d+) sha=([0-9a-f]{64})\r?\n`)

// nodefsVersion is version n of the replaced file: a header naming the payload's
// length and SHA-256, then 64 KiB of payload, so a torn file is detected.
func nodefsVersion(n int) []byte {
	payload := strings.Repeat(fmt.Sprintf("%08d", n), 8192)
	sum := sha256.Sum256([]byte(payload))
	return []byte(fmt.Sprintf("dsh-nodefs v=%d len=%d sha=%s\n%s", n, len(payload), hex.EncodeToString(sum[:]), payload))
}

// TestDurableReplaceSurvivesPowerLoss: a durable replace is complete and never
// older than its acknowledgement after a power cut (libs/node-fs-path-os-zlib.md
// DH-E-NODEFS-1; DH-LIB-NODEFS-R1; DH-D-FS-1; TC-21s5). Executed last in DH-S-18
// (step 18.11; architecture.md rule 7).
//
// Given a virtual machine per guest OS (Linux on KVM/libvirt, Windows on Hyper-V;
// macOS out of scope by platforms-windows-wsl) with a disk that can be cut off (a
// power-off, not a clean shutdown), running a loop that replaces a file durably with
// fsx.Replace and sends each acknowledged version to this second machine (the ack
// recorder), when the machine is powered off at a random moment and restarted, 100
// times, then the file is always complete and never older than the last
// acknowledged version. Cleanup: deleting the virtual disks is the operator's (a
// failing guest's overlay is kept); the recorder's version log is kept, in the
// file DSH_SCENARIO_REPORT names as the test reads it: <report>.detail, since
// scenario.Main keeps <report> for the scenario rows (scenario.DetailSuffix).
//
// The test has two roles. Inside a machine (DSH_SCENARIO_NODEFS_ROLE=writer) it is
// the loop: it replaces DSH_SCENARIO_NODEFS_FILE durably and sends each published
// version to DSH_SCENARIO_NODEFS_ACK until the machine loses power. On the second
// machine it is the controller and the recorder, with DSH_SCENARIO_VMS
// (";"-separated machine names), DSH_SCENARIO_NODEFS_LISTEN (the address the
// acknowledgements arrive at) and shell command lines over {vm}:
// DSH_SCENARIO_NODEFS_START starts the writer role in the machine in the background
// (the writer must not start on its own at boot), DSH_SCENARIO_NODEFS_POWEROFF cuts
// its power, DSH_SCENARIO_NODEFS_POWERON starts it and returns once it is
// reachable, and DSH_SCENARIO_NODEFS_READ prints the file on its standard output.
//
// Each round waits for the writer's first new acknowledgement, cuts at a random
// moment within nodefsCutWithin after it, restarts, reads, and judges the file
// against every version the recorder acknowledged. A version counts as
// acknowledged once the recorder has logged it and confirmed it to the writer,
// which writes no next version before that confirmation; so the boundary is the
// recorder's own, not a wait. The judgement and the bound are taken under one
// lock, which ties the round's writer connections to the version judged: an
// acknowledgement read on them later, however late, newer than that version fails
// the run (nodefsRecorder.serve).
//
// A run that would pass the 3 h variant limit is sharded (M2-R-06): the optional
// DSH_SCENARIO_NODEFS_ROUNDS selects rounds "i-j" of 1-100 (default all). A shard
// past round 1 continues the log of the shard before it, on the same guests:
// it refuses to start unless that log ends with every guest's round i-1 read and
// judged (not a round interrupted after its cut), and takes its acknowledged
// versions from it.
//
//dsh:scenario DH-E-NODEFS-1
func TestDurableReplaceSurvivesPowerLoss(t *testing.T) {
	if os.Getenv("DSH_SCENARIO_NODEFS_ROLE") == "writer" {
		nodefsWriter(t)
		return
	}
	vms := needList(t, "DSH_SCENARIO_VMS", "the virtual machines, ';'-separated")
	listen := need(t, "DSH_SCENARIO_NODEFS_LISTEN", "address the writers' acknowledgements arrive at")
	start := need(t, "DSH_SCENARIO_NODEFS_START", "command starting the writer role in {vm} in the background")
	powerOff := need(t, "DSH_SCENARIO_NODEFS_POWEROFF", "command cutting the power of {vm}")
	powerOn := need(t, "DSH_SCENARIO_NODEFS_POWERON", "command starting {vm} and waiting until it is reachable")
	read := need(t, "DSH_SCENARIO_NODEFS_READ", "command printing the replaced file of {vm}")
	reportPath := need(t, "DSH_SCENARIO_REPORT", "file the recorder's version log is appended to")
	from, to, err := nodefsRoundsOf(os.Getenv("DSH_SCENARIO_NODEFS_ROUNDS"))
	if err != nil {
		t.Fatal(err)
	}

	rec, err := openNodefsRecorder(t, listen, reportPath, vms, from, to)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("recorder on %s, version log %s, rounds %d-%d", rec.addr(), reportPath, from, to)
	run := func(template, vm string) error {
		line := expand(template, map[string]string{"vm": vm})
		if out, err := shell(t, line); err != nil {
			return fmt.Errorf("%w\n%s", err, out)
		}
		return nil
	}
	machine := nodefsMachine{
		start:    func(vm string) error { return run(start, vm) },
		powerOff: func(vm string) error { return run(powerOff, vm) },
		powerOn:  func(vm string) error { return run(powerOn, vm) },
		read: func(vm string) (string, error) {
			// Standard output only: a guest agent's warnings on standard error are
			// not the file.
			s := spawnShell(t, expand(read, map[string]string{"vm": vm}))
			_, err := s.wait()
			if err != nil {
				return "", fmt.Errorf("%w\n%s", err, s.host.Stderr.String())
			}
			return s.host.Stdout.String(), nil
		},
	}
	driveErr := nodefsDrive(t.Context(), rec, machine, vms, from, to, nodefsWriterUp, nodefsCutWithin)
	if err := errors.Join(driveErr, rec.close()); err != nil {
		t.Fatalf("%v\nthe recorder's version log: %s", err, reportPath)
	}
}

// nodefsRoundsOf parses DSH_SCENARIO_NODEFS_ROUNDS: empty or "all" for every
// round, or "i-j" within 1-nodefsRounds.
func nodefsRoundsOf(s string) (int, int, error) {
	if s == "" || s == "all" {
		return 1, nodefsRounds, nil
	}
	var from, to int
	if n, err := fmt.Sscanf(s, "%d-%d", &from, &to); err != nil || n != 2 || fmt.Sprintf("%d-%d", from, to) != s ||
		from < 1 || to > nodefsRounds || from > to {
		return 0, 0, fmt.Errorf("DSH_SCENARIO_NODEFS_ROUNDS %q: want \"all\" or i-j within 1-%d", s, nodefsRounds)
	}
	return from, to, nil
}

// nodefsMachine drives the guests: the scenario's shell command lines, or a
// stand-in in the scenario's own checks.
type nodefsMachine struct {
	start, powerOff, powerOn func(vm string) error
	read                     func(vm string) (string, error)
}

// nodefsDrive runs rounds from-to on each guest in turn. The first round that
// fails ends the run: it is recorded as a fail line naming the guest, whose
// overlay is to be kept. An acknowledgement that arrives after its round was
// judged, newer than the version judged, fails the run at the next check.
func nodefsDrive(ctx context.Context, rec *nodefsRecorder, m nodefsMachine, vms []string, from, to int, up, cutWithin time.Duration) error {
	for _, vm := range vms {
		for round := from; round <= to; round++ {
			if err := rec.violated(); err != nil {
				return err
			}
			if err := nodefsRound(ctx, rec, m, vm, round, up, cutWithin); err != nil {
				rec.record("fail vm=%s round=%d acked=%d: %s", vm, round, rec.last(vm), strings.ReplaceAll(err.Error(), "\n", " "))
				return fmt.Errorf("%s round %d: %w (keep the overlay of %s)", vm, round, err, vm)
			}
		}
	}
	return rec.violated()
}

// nodefsRound is one power cut of vm.
func nodefsRound(ctx context.Context, rec *nodefsRecorder, m nodefsMachine, vm string, round int, up, cutWithin time.Duration) error {
	rec.begin(vm, round)
	before := rec.last(vm)
	if err := m.start(vm); err != nil {
		return fmt.Errorf("starting the writer: %w", err)
	}
	if err := rec.waitAbove(ctx, vm, before, up); err != nil {
		return err
	}
	select {
	case <-time.After(rand.N(cutWithin)):
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := m.powerOff(vm); err != nil {
		return fmt.Errorf("cutting the power: %w", err)
	}
	rec.record("cut vm=%s round=%d acked=%d", vm, round, rec.last(vm))
	if err := m.powerOn(vm); err != nil {
		return fmt.Errorf("restarting: %w", err)
	}
	data, err := m.read(vm)
	if err != nil {
		return fmt.Errorf("reading the file: %w", err)
	}
	// The bound is every version the recorder confirmed before judge holds its
	// lock; any acknowledgement read later on this round's connections is checked
	// against the version judged here (serve), however late it arrives.
	_, err = rec.judge(vm, round, data)
	return err
}

// nodefsJudge returns the version data holds, or why it is not a complete version
// at least as new as acked. One trailing line ending, which a guest agent may add
// when it prints the file, is not part of the file.
func nodefsJudge(data string, acked int) (int, error) {
	m := nodefsHeader.FindStringSubmatch(data)
	if m == nil {
		return 0, fmt.Errorf("the file is not a complete version: %.80q", data)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("the file names version %q, which the writer never publishes", m[1])
	}
	body := strings.TrimSuffix(strings.TrimSuffix(data[len(m[0]):], "\n"), "\r")
	if got := strings.TrimRight(m[0], "\r\n") + "\n" + body; got != string(nodefsVersion(n)) {
		return n, fmt.Errorf("version %d is torn: %d bytes after its header, want its %s", n, len(body), m[2])
	}
	if n < acked {
		return n, fmt.Errorf("the file holds version %d, older than the acknowledged %d", n, acked)
	}
	return n, nil
}

// nodefsRecorder is service:ack-recorder: it listens for the writers'
// acknowledgements ("<vm> <version>" lines), appends each, with the run's events,
// to an append-only version log on this machine's disk, and only then confirms
// it to the writer ("ok <version>"), which writes no next version before that
// confirmation. A version is acknowledged once the recorder has logged it, so at
// most one version the writer published is unacknowledged at a cut. Log lines are
// "<sequence> <UTC time> <event> key=value...". Its events are start, seed (a
// shard's acknowledged version taken from the log), round, ack, cut, read (a round
// judged complete and new enough), late (an acknowledgement read after its round
// was judged, no newer than the version judged), fail, unconfirmed (a confirmation
// the writer's connection did not take) and junk (a line that is no
// acknowledgement). The log is kept whatever the result.
type nodefsRecorder struct {
	ln        net.Listener
	wg        sync.WaitGroup
	mu        sync.Mutex
	conns     map[net.Conn]*nodefsJudged // each writer's connection -> its round's judgement, nil until judged
	acked     map[string]int             // vm -> highest acknowledged version
	round     map[string]int             // vm -> the round in progress
	violation error                      // the first acknowledgement newer than its round's judged version
	seq       int
	log       *os.File
	err       error // the first write error of the log
	closed    bool
	closeErr  error
}

// nodefsJudged is the judgement of the round a writer's connection served: its
// acknowledgements read afterwards must be no newer than version.
type nodefsJudged struct {
	vm             string
	round, version int
}

// openNodefsRecorder listens on listen and appends to the log at logPath for
// rounds from-to of vms; from > 1 continues a sharded run (continueLog). The
// recorder is closed when t ends, if not before.
func openNodefsRecorder(t testing.TB, listen, logPath string, vms []string, from, to int) (*nodefsRecorder, error) {
	r := &nodefsRecorder{acked: map[string]int{}, round: map[string]int{}, conns: map[net.Conn]*nodefsJudged{}}
	if from > 1 {
		if err := r.continueLog(logPath, vms, from); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the recorder's version log: %w", err)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	r.log, r.ln = f, ln
	// close closes ln; closing it again is a no-op error, and names it for QG-5.
	t.Cleanup(func() { _ = r.close(); _ = ln.Close() })
	r.record("start rounds=%d-%d vms=%s", from, to, strings.Join(vms, ";"))
	for _, vm := range vms {
		if n := r.acked[vm]; n > 0 {
			r.record("seed vm=%s acked=%d", vm, n)
		}
	}
	r.wg.Add(1)
	go r.accept()
	return r, nil
}

// continueLog takes the acknowledged versions of a sharded run from the log at
// path, refusing unless the log ends each of vms with round from-1 read: a shard
// neither skips a round, follows a failed one, nor resumes after one interrupted
// before its read (a round or cut line last). Any fail line of the run refuses
// it, wherever it stands: a late acknowledgement fails a round already read. The
// run's lines start at its last "start rounds=1-..." line, the only boundary
// that clears a failure; the sequence continues the log's.
func (r *nodefsRecorder) continueLog(path string, vms []string, from int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rounds from %d continue a sharded run, whose version log is unreadable: %w", from, err)
	}
	acked, ended := map[string]int{}, map[string]string{}
	failed := "" // the run's first fail line: sticky until a fresh run starts
	failedVM := ""
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		if s, err := strconv.Atoi(f[0]); err == nil {
			r.seq = max(r.seq, s)
		}
		kv := map[string]string{}
		for _, field := range f[3:] {
			if k, v, ok := strings.Cut(field, "="); ok {
				kv[k] = strings.TrimSuffix(v, ":")
			}
		}
		vm := kv["vm"]
		switch f[2] {
		case "start":
			if strings.HasPrefix(kv["rounds"], "1-") {
				acked, ended = map[string]int{}, map[string]string{}
				failed, failedVM = "", ""
			}
		case "ack", "seed":
			v := kv["version"]
			if f[2] == "seed" {
				v = kv["acked"]
			}
			if n, err := strconv.Atoi(v); err == nil {
				acked[vm] = max(acked[vm], n)
			}
		case "round", "cut", "read", "fail":
			// A round or cut after the last read is a round left unjudged: its
			// file, written before a cut, is the evidence a new writer would replace.
			ended[vm] = f[2] + " round " + kv["round"]
			// A failure is final for the run, even one logged before a later read
			// (an acknowledgement read late, after its round was judged).
			if f[2] == "fail" && failed == "" {
				failed, failedVM = "fail round "+kv["round"], vm
			}
		}
	}
	if failed != "" {
		return fmt.Errorf("rounds from %d continue a sharded run, but the version log %s records %q of %s: the run failed; keep the overlay of %s and rerun all rounds on a fresh overlay",
			from, path, failed, failedVM, failedVM)
	}
	for _, vm := range vms {
		if want := fmt.Sprintf("read round %d", from-1); ended[vm] != want {
			hint := ""
			if ev, _, _ := strings.Cut(ended[vm], " "); ev == "round" || ev == "cut" {
				hint = fmt.Sprintf("; that round was never judged: keep the overlay of %s, judge its file by hand, and rerun all rounds on a fresh overlay", vm)
			}
			return fmt.Errorf("rounds from %d continue a sharded run, but the version log %s ends %s's rounds with %q, want %q%s",
				from, path, vm, ended[vm], want, hint)
		}
	}
	r.acked = acked
	return nil
}

// addr is the address the recorder listens on.
func (r *nodefsRecorder) addr() string { return r.ln.Addr().String() }

func (r *nodefsRecorder) accept() {
	defer r.wg.Done()
	for {
		conn, err := r.ln.Accept()
		if err != nil {
			return
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			_ = conn.Close()
			return
		}
		r.conns[conn] = nil
		r.wg.Add(1)
		r.mu.Unlock()
		go r.serve(conn)
	}
}

// serve reads one writer's acknowledgements and confirms each it logged. A
// writer's connection dies with its machine and never closes: close closes it.
// An acknowledgement read after its connection's round was judged confirms
// nothing; newer than the version judged, it is a version the file lost.
func (r *nodefsRecorder) serve(conn net.Conn) {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		delete(r.conns, conn)
		r.mu.Unlock()
		_ = conn.Close()
	}()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		vm, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		n, err := strconv.Atoi(v)
		if !ok || err != nil || n < 1 || strings.ContainsAny(vm, " =") {
			r.record("junk %q", sc.Text())
			continue
		}
		r.mu.Lock()
		if j := r.conns[conn]; j != nil {
			if n > j.version {
				msg := fmt.Sprintf("%s acknowledged version %d after round %d was judged at version %d", vm, n, j.round, j.version)
				r.writeLocked("fail vm=%s round=%d acked=%d: %s", j.vm, j.round, n, msg)
				if r.violation == nil {
					r.violation = fmt.Errorf("%s (keep the overlay of %s)", msg, j.vm)
				}
			} else {
				r.writeLocked("late vm=%s round=%d version=%d", vm, j.round, n)
			}
			r.mu.Unlock()
			continue
		}
		r.acked[vm] = max(r.acked[vm], n)
		r.writeLocked("ack vm=%s round=%d version=%d", vm, r.round[vm], n)
		logged := r.err == nil
		r.mu.Unlock()
		if !logged {
			// Not durably logged: left unconfirmed; close reports r.err.
			continue
		}
		// Confirmed only once logged and synced; a writer gone with its machine never reads it.
		_ = conn.SetWriteDeadline(time.Now().Add(nodefsConfirmWithin))
		if _, err := fmt.Fprintf(conn, "ok %d\n", n); err != nil {
			r.record("unconfirmed vm=%s version=%d: %s", vm, n, strings.ReplaceAll(err.Error(), "\n", " "))
		}
	}
}

// judge judges data, vm's file read after round's cut, against every version the
// recorder acknowledged, and ties each writer's connection open now to this
// judgement, all under one hold of r.mu: an acknowledgement is either in the
// bound or checked against the version judged when it is read (serve). The
// round's writer is among those connections: its acknowledgement started the cut.
func (r *nodefsRecorder) judge(vm string, round int, data string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	acked := r.acked[vm]
	n, err := nodefsJudge(data, acked)
	if err != nil {
		return n, err
	}
	for conn, j := range r.conns {
		if j == nil {
			r.conns[conn] = &nodefsJudged{vm: vm, round: round, version: n}
		}
	}
	r.writeLocked("read vm=%s round=%d version=%d acked=%d", vm, round, n, acked)
	return n, nil
}

// violated is the first acknowledgement read newer than its round's judged
// version, if any.
func (r *nodefsRecorder) violated() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.violation
}

// writeLocked appends one event to the log and syncs it, so an event survives a
// crash of the recorder's host once writeLocked returns with r.err nil; r.mu is
// held.
func (r *nodefsRecorder) writeLocked(format string, args ...any) {
	if r.err != nil {
		return
	}
	r.seq++
	if _, r.err = fmt.Fprintf(r.log, "%d %s "+format+"\n", append([]any{r.seq, time.Now().UTC().Format(time.RFC3339Nano)}, args...)...); r.err != nil {
		return
	}
	if err := r.log.Sync(); err != nil {
		r.err = fmt.Errorf("syncing the recorder's version log: %w", err)
	}
}

// record appends one event to the log.
func (r *nodefsRecorder) record(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writeLocked(format, args...)
}

// begin marks round as vm's round in progress.
func (r *nodefsRecorder) begin(vm string, round int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.round[vm] = round
	r.writeLocked("round vm=%s round=%d", vm, round)
}

// last is vm's highest acknowledged version.
func (r *nodefsRecorder) last(vm string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.acked[vm]
}

// waitAbove waits until vm has acknowledged a version above above.
func (r *nodefsRecorder) waitAbove(ctx context.Context, vm string, above int, within time.Duration) error {
	deadline := time.After(within)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for r.last(vm) <= above {
		select {
		case <-tick.C:
		case <-deadline:
			return fmt.Errorf("the writer acknowledged no version after %d within %s of its start", above, within)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// close stops listening, closes every writer's connection, waits for them, and
// syncs and closes the log; it returns the log's first write error and the first
// acknowledgement read newer than its round's judged version, if any.
func (r *nodefsRecorder) close() error {
	r.mu.Lock()
	if r.closed {
		defer r.mu.Unlock()
		return r.closeErr
	}
	r.closed = true
	conns := slices.Collect(maps.Keys(r.conns))
	r.mu.Unlock()
	_ = r.ln.Close()
	for _, conn := range conns {
		_ = conn.Close()
	}
	r.wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeErr = errors.Join(r.err, r.log.Sync(), r.log.Close())
	if r.closeErr != nil {
		r.closeErr = fmt.Errorf("the recorder's version log: %w", r.closeErr)
	}
	r.closeErr = errors.Join(r.closeErr, r.violation)
	return r.closeErr
}

// nodefsWriter is the writer role: replace the file durably, acknowledge each
// published version, until the machine loses power.
func nodefsWriter(t *testing.T) {
	path := need(t, "DSH_SCENARIO_NODEFS_FILE", "the file the writer replaces")
	ack := need(t, "DSH_SCENARIO_NODEFS_ACK", "address of the controller's acknowledgement listener")
	vm := need(t, "DSH_SCENARIO_NODEFS_VM", "the name of this machine, as the controller knows it")
	conn, err := net.Dial("tcp", ack)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := nodefsWrite(t.Context(), path, vm, conn); err != nil {
		t.Fatal(err)
	}
}

// nodefsWrite replaces path with version after version through fsx.Replace,
// durably, sending "<vm> <version>" to ack after each returns and waiting for the
// recorder's "ok <version>" before the next, until ctx ends. It continues from
// the version on disk, so versions only grow across rounds. A version is
// acknowledged only once Replace has returned nil: an error, including
// fsx.ErrDurabilityUnknown, ends the loop unacknowledged; so does any reply but
// the confirmation of the version sent.
func nodefsWrite(ctx context.Context, path, vm string, ack io.ReadWriter) error {
	n := 0
	if data, err := os.ReadFile(path); err == nil {
		if m := nodefsHeader.FindSubmatch(data); m != nil {
			n, _ = strconv.Atoi(string(m[1]))
		}
	}
	replies := bufio.NewReader(ack)
	for ctx.Err() == nil {
		n++
		if err := fsx.Replace(path, fsx.Bytes(nodefsVersion(n)), fsx.Options{Perm: 0o600, Durable: true}); err != nil {
			return fmt.Errorf("version %d: %w", n, err)
		}
		if _, err := fmt.Fprintf(ack, "%s %d\n", vm, n); err != nil {
			return fmt.Errorf("acknowledging %d: %w", n, err)
		}
		reply, err := replies.ReadString('\n')
		if err != nil {
			return fmt.Errorf("version %d unconfirmed: %w", n, err)
		}
		if want := fmt.Sprintf("ok %d", n); strings.TrimSpace(reply) != want {
			return fmt.Errorf("version %d unconfirmed: the recorder replied %q, not %q", n, strings.TrimSpace(reply), want)
		}
	}
	return nil
}
