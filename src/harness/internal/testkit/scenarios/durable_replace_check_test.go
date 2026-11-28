//go:build scenario

package scenarios

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The checks below prove DH-E-NODEFS-1's controller and recorder against a
// stand-in guest; they are no evidence of durability, which only a real power cut
// gives (TC-21s5).

// standInGuest is a guest whose writer is nodefsWrite in a goroutine of this
// process and whose power-off stops it: what a cut does to the protocol, not to
// the disk.
type standInGuest struct {
	addr string
	dir  string
	mu   sync.Mutex
	stop map[string]func() error
	cuts int
	// lose, when set, replaces what read returns (a disk that loses writes).
	lose func(vm string, data string) string
}

func newStandInGuest(t *testing.T, addr string) *standInGuest {
	g := &standInGuest{addr: addr, dir: t.TempDir(), stop: map[string]func() error{}}
	t.Cleanup(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, stop := range g.stop {
			_ = stop()
		}
	})
	return g
}

func (g *standInGuest) path(vm string) string { return filepath.Join(g.dir, vm+".nodefs") }

func (g *standInGuest) machine() nodefsMachine {
	return nodefsMachine{
		start: func(vm string) error {
			conn, err := net.Dial("tcp", g.addr)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- nodefsWrite(ctx, g.path(vm), vm, conn) }()
			g.mu.Lock()
			defer g.mu.Unlock()
			g.stop[vm] = func() error {
				cancel()
				err := <-done
				_ = conn.Close()
				return err
			}
			return nil
		},
		powerOff: func(vm string) error {
			g.mu.Lock()
			stop := g.stop[vm]
			delete(g.stop, vm)
			g.cuts++
			g.mu.Unlock()
			if stop == nil {
				return nil
			}
			return stop()
		},
		powerOn: func(string) error { return nil },
		read: func(vm string) (string, error) {
			data, err := os.ReadFile(g.path(vm))
			if g.lose != nil {
				return g.lose(vm, string(data)), nil
			}
			return string(data), err
		},
	}
}

// standInRun opens a recorder on a loopback port with its log at logPath and a
// stand-in guest writing to it.
func standInRun(t *testing.T, logPath string, vms []string, from, to int) (*nodefsRecorder, *standInGuest) {
	t.Helper()
	rec, err := openNodefsRecorder(t, "127.0.0.1:0", logPath, vms, from, to)
	if err != nil {
		t.Fatal(err)
	}
	return rec, newStandInGuest(t, rec.addr())
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNodefsJudge(t *testing.T) {
	v7 := string(nodefsVersion(7))
	header, payload, _ := strings.Cut(v7, "\n")
	tests := []struct {
		name  string
		data  string
		acked int
		want  string // "" passes
	}{
		{"complete and acknowledged", v7, 7, ""},
		{"newer than acknowledged", v7, 6, ""},
		{"never acknowledged at all", v7, 0, ""},
		{"a trailing newline of the guest agent", v7 + "\n", 7, ""},
		{"Windows line endings of the guest agent", header + "\r\n" + payload + "\r\n", 7, ""},
		{"older than acknowledged", v7, 8, "older than the acknowledged 8"},
		{"truncated payload", v7[:len(v7)-1], 7, "version 7 is torn"},
		{"header only", header + "\n", 7, "version 7 is torn"},
		{"trailing bytes", v7 + "00000007", 7, "version 7 is torn"},
		{"another version's payload", header + "\n" + strings.Repeat("00000006", 8192), 7, "version 7 is torn"},
		{"header naming another length", strings.Replace(v7, "len=65536", "len=65535", 1), 7, "version 7 is torn"},
		{"empty file", "", 0, "not a complete version"},
		{"half a header", header[:20], 0, "not a complete version"},
		{"version zero", strings.Replace(v7, "v=7", "v=0", 1), 0, "never publishes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := nodefsJudge(tt.data, tt.acked)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("judged %v, want it to pass", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("judged %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestNodefsRoundsOf(t *testing.T) {
	for in, want := range map[string][2]int{"": {1, 100}, "all": {1, 100}, "1-50": {1, 50}, "51-100": {51, 100}, "7-7": {7, 7}} {
		if from, to, err := nodefsRoundsOf(in); err != nil || from != want[0] || to != want[1] {
			t.Errorf("nodefsRoundsOf(%q) = %d, %d, %v; want %v", in, from, to, err, want)
		}
	}
	for _, in := range []string{"0-5", "5-4", "1-101", "1-50x", "50", "-1-5", "1 - 50"} {
		if _, _, err := nodefsRoundsOf(in); err == nil {
			t.Errorf("nodefsRoundsOf(%q) accepted", in)
		}
	}
}

// Every round is cut while the writer replaces the file, the file read after each
// is judged, and the log keeps every event of a passing run.
func TestNodefsDriveAgainstAStandInGuest(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "nodefs.log")
	vms := []string{"linux-guest", "windows-guest"}
	rec, g := standInRun(t, logPath, vms, 1, 3)
	if err := nodefsDrive(t.Context(), rec, g.machine(), vms, 1, 3, 10*time.Second, 50*time.Millisecond, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := rec.close(); err != nil {
		t.Fatal(err)
	}
	if g.cuts != 6 {
		t.Errorf("%d cuts, want 6", g.cuts)
	}
	log := readLog(t, logPath)
	for _, vm := range vms {
		for round := 1; round <= 3; round++ {
			for _, ev := range []string{"round", "cut", "read"} {
				if want := fmt.Sprintf(" %s vm=%s round=%d ", ev, vm, round); !strings.Contains(log, want) && !strings.Contains(log, strings.TrimSuffix(want, " ")+"\n") {
					t.Errorf("the log has no %q:\n%.2000s", want, log)
				}
			}
		}
		if !strings.Contains(log, " ack vm="+vm+" round=3 version=") {
			t.Errorf("the log has no acknowledgement of %s in round 3", vm)
		}
	}
	prev := 0
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var seq int
		if _, err := fmt.Sscanf(line, "%d ", &seq); err != nil || seq != prev+1 {
			t.Fatalf("line %q breaks the sequence after %d", line, prev)
		}
		prev = seq
	}
}

// A disk that loses an acknowledged version fails the round, and the log names it.
func TestNodefsDriveCatchesALostAcknowledgedVersion(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "nodefs.log")
	rec, g := standInRun(t, logPath, []string{"vm1"}, 1, 3)
	// The writer acknowledges every version it published, so the version on disk
	// is acknowledged once its line arrives; the disk then returns the one before.
	g.lose = func(vm string, data string) string {
		n, err := nodefsJudge(data, 0)
		if err != nil || n < 2 {
			return data
		}
		if err := rec.waitAbove(t.Context(), vm, n-1, 10*time.Second); err != nil {
			t.Error(err)
		}
		return string(nodefsVersion(n - 1))
	}
	err := nodefsDrive(t.Context(), rec, g.machine(), []string{"vm1"}, 1, 3, 10*time.Second, 50*time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "older than the acknowledged") || !strings.Contains(err.Error(), "keep the overlay of vm1") {
		t.Fatalf("drive = %v, want the lost version", err)
	}
	_ = rec.close()
	if log := readLog(t, logPath); !strings.Contains(log, " fail vm=vm1 round=") || !strings.Contains(log, "older than the acknowledged") {
		t.Errorf("the log names no failed round:\n%.2000s", log)
	}
}

// An acknowledgement the host has not read when the read returns still counts:
// the writer's connection open at the cut is read until it settles, and the file
// is judged against what it carried.
func TestNodefsJudgesAgainstAcksDeliveredAfterTheRead(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "nodefs.log")
	rec, _ := standInRun(t, logPath, []string{"vm1"}, 1, 1)
	var conn net.Conn
	m := nodefsMachine{
		start: func(vm string) error {
			c, err := net.Dial("tcp", rec.addr())
			if err != nil {
				return err
			}
			conn = c
			t.Cleanup(func() { _ = c.Close() })
			_, err = fmt.Fprintf(c, "%s 1\n", vm)
			return err
		},
		// The guest acknowledged version 1000 before the cut; its line reaches the
		// recorder only after the read below returned version 1.
		powerOff: func(vm string) error {
			late := time.AfterFunc(300*time.Millisecond, func() { fmt.Fprintf(conn, "%s 1000\n", vm) })
			t.Cleanup(func() { late.Stop() })
			return nil
		},
		powerOn: func(string) error { return nil },
		read:    func(string) (string, error) { return string(nodefsVersion(1)), nil },
	}
	err := nodefsDrive(t.Context(), rec, m, []string{"vm1"}, 1, 1, 10*time.Second, 50*time.Millisecond, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "older than the acknowledged 1000") {
		t.Fatalf("drive = %v, want the acknowledgement delivered after the read to count", err)
	}
	_ = rec.close()
	if log := readLog(t, logPath); !strings.Contains(log, " ack vm=vm1 round=1 version=1000\n") {
		t.Errorf("the log has no late acknowledgement in round 1:\n%.2000s", log)
	}
}

// A writer that has not started yet is never cut: the round fails before the cut.
func TestNodefsDriveNeedsTheWriterRunningBeforeTheCut(t *testing.T) {
	rec, g := standInRun(t, filepath.Join(t.TempDir(), "nodefs.log"), []string{"vm1"}, 1, 1)
	m := g.machine()
	m.start = func(string) error { return nil }
	err := nodefsDrive(t.Context(), rec, m, []string{"vm1"}, 1, 1, 200*time.Millisecond, 50*time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "acknowledged no version") {
		t.Fatalf("drive = %v, want no acknowledgement", err)
	}
	if g.cuts != 0 {
		t.Errorf("%d cuts of a guest whose writer never acknowledged", g.cuts)
	}
}

// A shard continues the log of the shard before it, on the same guest, with its
// acknowledged versions; it refuses a gap and a failed predecessor.
func TestNodefsShardContinuesTheLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "nodefs.log")
	rec, g := standInRun(t, logPath, []string{"vm1"}, 1, 2)
	if err := nodefsDrive(t.Context(), rec, g.machine(), []string{"vm1"}, 1, 2, 10*time.Second, 50*time.Millisecond, time.Second); err != nil {
		t.Fatal(err)
	}
	acked := rec.last("vm1")
	if err := rec.close(); err != nil {
		t.Fatal(err)
	}

	if _, err := openNodefsRecorder(t, "127.0.0.1:0", logPath, []string{"vm1"}, 4, 5); err == nil || !strings.Contains(err.Error(), `want "read round 3"`) {
		t.Errorf("a shard skipping round 3 opened: %v", err)
	}
	if _, err := openNodefsRecorder(t, "127.0.0.1:0", logPath, []string{"vm1", "vm2"}, 3, 4); err == nil || !strings.Contains(err.Error(), "ends vm2's rounds") {
		t.Errorf("a shard naming a guest the log never ran opened: %v", err)
	}
	if _, err := openNodefsRecorder(t, "127.0.0.1:0", filepath.Join(dir, "absent.log"), []string{"vm1"}, 3, 4); err == nil {
		t.Error("a shard with no log opened")
	}

	next, err := openNodefsRecorder(t, "127.0.0.1:0", logPath, []string{"vm1"}, 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.last("vm1"); got != acked {
		t.Errorf("the shard starts from acknowledged %d, want %d", got, acked)
	}
	g.addr = next.addr()
	if err := nodefsDrive(t.Context(), next, g.machine(), []string{"vm1"}, 3, 4, 10*time.Second, 50*time.Millisecond, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := next.close(); err != nil {
		t.Fatal(err)
	}
	log := readLog(t, logPath)
	if !strings.Contains(log, fmt.Sprintf(" seed vm=vm1 acked=%d\n", acked)) || !strings.Contains(log, " read vm=vm1 round=4 ") {
		t.Errorf("the continued log lacks its seed or round 4:\n%.3000s", log)
	}

	failed := filepath.Join(dir, "failed.log")
	if err := os.WriteFile(failed, []byte("1 t start rounds=1-100 vms=vm1\n2 t read vm=vm1 round=1 version=3 acked=3\n3 t fail vm=vm1 round=2 acked=9: torn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openNodefsRecorder(t, "127.0.0.1:0", failed, []string{"vm1"}, 2, 3); err == nil || !strings.Contains(err.Error(), `"fail round 2"`) {
		t.Errorf("a shard after a failed round opened: %v", err)
	}
	if _, err := openNodefsRecorder(t, "127.0.0.1:0", failed, []string{"vm1"}, 3, 4); err == nil {
		t.Error("a shard after a failed round opened")
	}

	// A shard stopped after round 41's cut, before its read: the file on the
	// guest is round 41's unjudged evidence, which a new writer would replace.
	for name, tail := range map[string]string{
		"after the cut":    "3 t round vm=vm1 round=41\n4 t ack vm=vm1 round=41 version=12\n5 t cut vm=vm1 round=41 acked=12\n",
		"before the cut":   "3 t round vm=vm1 round=41\n",
		"a later shard on": "3 t round vm=vm1 round=41\n4 t cut vm=vm1 round=41 acked=11\n5 t start rounds=41-60 vms=vm1\n",
	} {
		interrupted := filepath.Join(dir, "interrupted.log")
		if err := os.WriteFile(interrupted, []byte("1 t start rounds=1-40 vms=vm1\n2 t read vm=vm1 round=40 version=11 acked=11\n"+tail), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, from := range []int{41, 42} {
			if _, err := openNodefsRecorder(t, "127.0.0.1:0", interrupted, []string{"vm1"}, from, 60); err == nil || !strings.Contains(err.Error(), "never judged") {
				t.Errorf("%s: a shard from %d after an interrupted round 41 opened: %v", name, from, err)
			}
		}
	}
}
