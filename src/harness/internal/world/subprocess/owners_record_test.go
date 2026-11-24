package subprocess_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// providerHostEnv makes a re-executed test binary play a host that starts the
// provider (OpenRegistry, with the real user service manager) on the run
// directory the value names ("-" for none), starts a RoleTree child, registers it
// and prints "provider-host <child> <grandchild> <owner kind> <scope unit or ->",
// then waits to be killed. It has no DSH_ prefix because BuildEnv removes every
// DSH_* name. init dispatches it, before TestMain.
const providerHostEnv = "SUBPROCESS_TEST_PROVIDER_HOST"

func init() {
	if dir, ok := os.LookupEnv(providerHostEnv); ok {
		os.Exit(providerHost(dir))
	}
}

func providerHost(dir string) int {
	if dir == "-" {
		dir = ""
	}
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "provider-host:", err)
		return 2
	}
	reg, _, err := subprocess.OpenRegistry(context.Background(), subprocess.ProviderOptions{
		RunDir:   dir,
		Announce: func(o subprocess.Owner) { fmt.Fprintf(os.Stderr, "announce: owner %s\n", o) },
	})
	if err != nil {
		return fail(err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	pr, pw := io.Pipe()
	p, err := subprocess.Start(context.Background(), subprocess.Spec{
		Argv: []string{exe}, Env: subprocess.Env{subprocesstest.RoleEnv + "=" + subprocesstest.RoleTree},
		Stdout: pw, Launch: subprocesstest.Launch,
	})
	if err != nil {
		return fail(err)
	}
	line, err := bufio.NewReader(pr).ReadString('\n')
	var child, grandchild int
	if _, serr := fmt.Sscanf(line, "tree %d %d", &child, &grandchild); err != nil || serr != nil {
		return fail(fmt.Errorf("tree child said %q: %v %v", line, err, serr))
	}
	reg.Add(p)
	if err := reg.OwnerRecordErr(); err != nil {
		return fail(err)
	}
	scope := subprocess.DelegatedScope()
	if scope == "" {
		scope = "-"
	}
	fmt.Printf("provider-host %d %d %s %s\n", child, grandchild, p.Owner().Kind, scope)
	select {}
}

// hostRun is a started provider host and what it printed.
type hostRun struct {
	host              *subprocess.Process
	child, grandchild int
	kind, scope       string
	stderr            *lockedBuilder
}

// startProviderHost starts a provider host on runDir and registers a cleanup
// that kills every pid it reported, since a killed host's survivors are nobody's.
func startProviderHost(t *testing.T, runDir string) hostRun {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	errs := &lockedBuilder{}
	env := subprocess.Env{providerHostEnv + "=" + runDir}
	if v, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+v)
	}
	for _, name := range []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "SYSTEMROOT"} {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	h := subprocesstest.Start(t, subprocess.Spec{Argv: []string{exe}, Env: env, Stdout: pw, Stderr: errs, Launch: subprocesstest.Launch})
	t.Cleanup(func() { _ = pr.Close() })
	line, err := bufio.NewReader(pr).ReadString('\n')
	r := hostRun{host: h, stderr: errs}
	if _, serr := fmt.Sscanf(line, "provider-host %d %d %s %s", &r.child, &r.grandchild, &r.kind, &r.scope); err != nil || serr != nil {
		t.Fatalf("provider host said %q (%v, %v); stderr: %s", line, err, serr, errs)
	}
	t.Cleanup(func() {
		for _, pid := range []int{r.child, r.grandchild} {
			if pr, err := os.FindProcess(pid); err == nil && !subprocesstest.Exited(pid) {
				_ = pr.Kill()
			}
		}
	})
	return r
}

// killHostOnly kills the host's own pid, never its group or job, as a crash does.
func killHostOnly(t *testing.T, h *subprocess.Process) {
	t.Helper()
	pr, err := os.FindProcess(h.Pid())
	if err != nil {
		t.Fatal(err)
	}
	if err := pr.Kill(); err != nil {
		t.Fatalf("kill host %d: %v", h.Pid(), err)
	}
	if err := h.Wait(); err == nil {
		t.Fatal("the killed host reported success")
	}
}

// recordEntry is the part of an owners.json entry the tests read.
type recordEntry struct {
	Host    int    `json:"host"`
	Pid     int    `json:"pid"`
	Kind    string `json:"kind"`
	Cgroup  string `json:"cgroup"`
	Started string `json:"started"`
}

func readRecord(t *testing.T, runDir string) []recordEntry {
	t.Helper()
	// On Windows a read can meet the moment the record is renamed into place.
	var b []byte
	err := procwait.Poll(context.Background(), func() bool {
		var rerr error
		b, rerr = os.ReadFile(filepath.Join(runDir, subprocess.OwnersFile))
		return rerr == nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int           `json:"version"`
		Owners  []recordEntry `json:"owners"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.Version != 1 {
		t.Fatalf("owners.json = %s: %v", b, err)
	}
	return doc.Owners
}

// openAsNextHost starts the provider in this test process, as the next host on
// the same home. The rung is fixed first, so this process never asks the user
// service manager for a scope of its own.
func openAsNextHost(t *testing.T, runDir string) (*subprocess.Registry, []subprocess.Leftover) {
	t.Helper()
	_ = subprocess.TreeOwner()
	reg, left, err := subprocess.OpenRegistry(context.Background(), subprocess.ProviderOptions{RunDir: runDir})
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	return reg, left
}

// The registry records each tree it holds in run/owners.json, with the host,
// pid, owner kind, control group and start time, and removes the entry once the
// tree is gone (19.4.2).
//
//dsh:test DH-T-EXEC-5
func TestOwnerRecordFollowsTheRegistry(t *testing.T) {
	runDir := t.TempDir()
	reg, left := openAsNextHost(t, runDir)
	if len(left) != 0 {
		t.Fatalf("leftovers on a new home = %v", left)
	}
	p := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleStubborn))
	remove := reg.Add(p)
	defer remove()
	if err := reg.OwnerRecordErr(); err != nil {
		t.Fatalf("record: %v", err)
	}
	got := readRecord(t, runDir)
	if len(got) != 1 || got[0].Pid != p.Pid() || got[0].Host != os.Getpid() || got[0].Kind != string(p.Owner().Kind) || got[0].Started == "" {
		t.Fatalf("record while the tree runs = %+v; want one entry for pid %d of host %d, kind %s", got, p.Pid(), os.Getpid(), p.Owner().Kind)
	}
	if (got[0].Cgroup != "") != (p.Owner().Kind == subprocess.ControlGroup) {
		t.Fatalf("record cgroup %q for owner %s; want a path exactly on the control-group rung", got[0].Cgroup, p.Owner())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := p.StopTree(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := procwait.Poll(ctx, func() bool { return len(readRecord(t, runDir)) == 0 }); err != nil {
		t.Fatalf("record after the tree left = %+v (record error %v): %v", readRecord(t, runDir), reg.OwnerRecordErr(), err)
	}
	if err := reg.OwnerRecordErr(); err != nil {
		t.Fatalf("record: %v", err)
	}
}

// The leftover report (the leftover-report row of DH-T-EXEC-5): an entry whose
// host no longer runs (here its pid is a running process with another start
// identity, as after pid reuse) is reported with its live members and kept for
// the next start; it is not stopped. An entry of a running host is neither
// reported nor dropped, and an unreadable record is an error that leaves it as
// it is.
//
//dsh:test DH-T-EXEC-5
func TestLeftoverReportNamesSurvivorsOfADeadHost(t *testing.T) {
	survivor := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleStubborn))
	live := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleStubborn))
	runDir := t.TempDir()
	record := fmt.Sprintf(`{"version":1,"owners":[
		{"host":%d,"host_start":"not-its-start","pid":%d,"kind":"process-group","started":"2026-01-02T03:04:05Z"},
		{"host":%d,"pid":%d,"kind":"process-group","started":"2026-01-02T03:04:05Z"}]}`,
		live.Pid(), survivor.Pid(), live.Pid(), survivor.Pid())
	if err := os.WriteFile(filepath.Join(runDir, subprocess.OwnersFile), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, left := openAsNextHost(t, runDir)
		if len(left) != 1 || left[0].Host != live.Pid() || left[0].Pid != survivor.Pid() || !slices.Contains(left[0].Survivors, survivor.Pid()) {
			t.Fatalf("leftovers = %v; want the survivor %d of the dead host", left, survivor.Pid())
		}
		if s := left[0].String(); !strings.Contains(s, fmt.Sprint(survivor.Pid())) || !strings.Contains(s, "left running") {
			t.Errorf("Leftover.String = %q", s)
		}
		if got := readRecord(t, runDir); len(got) != 2 {
			t.Fatalf("record after the report = %+v; want both entries kept", got)
		}
		if !subprocesstest.Alive(survivor.Pid()) || subprocesstest.Exited(survivor.Pid()) {
			t.Fatalf("survivor %d was stopped by the report", survivor.Pid())
		}
	}

	if err := os.WriteFile(filepath.Join(runDir, subprocess.OwnersFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, left, err := subprocess.OpenRegistry(context.Background(), subprocess.ProviderOptions{RunDir: runDir})
	if herr.Kind(err) != subprocess.ReasonTreeOwner || reg == nil || left != nil {
		t.Fatalf("OpenRegistry on an unreadable record = %v, %v, %v; want a usable registry and %s", reg, left, err, subprocess.ReasonTreeOwner)
	}
	if b, _ := os.ReadFile(filepath.Join(runDir, subprocess.OwnersFile)); string(b) != "{not json" {
		t.Fatalf("the unreadable record was rewritten: %q", b)
	}
	if _, _, err := subprocess.OpenRegistry(context.Background(), subprocess.ProviderOptions{RunDir: filepath.Join(runDir, "missing")}); err == nil {
		t.Fatal("OpenRegistry on a missing run directory: no error")
	}
}

// Scenario "Leftovers reported at the next start" (19.4.2): a host is killed
// while its owner record is live; a new host on the same home reports the
// survivor pids and does not kill them. On Linux the direct child dies with the
// host (the parent-death signal) and the grandchild survives, in the tree's
// control group or process group. On Windows the host's job ends the tree when
// the host dies, so any pid reported is one of the tree's not yet ended, and the
// entry is dropped once none is left. The test then kills the survivors itself.
//
//dsh:test DH-T-EXEC-5
func TestKilledHostLeftoversAreReportedNotKilled(t *testing.T) {
	runDir := t.TempDir()
	h := startProviderHost(t, runDir)
	t.Logf("host %d: owner %s, scope %s, child %d, grandchild %d; stderr: %s", h.host.Pid(), h.kind, h.scope, h.child, h.grandchild, h.stderr)
	rec := readRecord(t, runDir)
	if len(rec) != 1 || rec[0].Host != h.host.Pid() || rec[0].Pid != h.child || rec[0].Kind != h.kind {
		t.Fatalf("record of the live host = %+v; want its child %d (%s)", rec, h.child, h.kind)
	}
	if runtime.GOOS == "windows" && h.kind != string(subprocess.JobObject) {
		t.Fatalf("owner on Windows = %s; want job-object", h.kind)
	}

	killHostOnly(t, h.host)
	_, left := openAsNextHost(t, runDir)
	t.Logf("leftovers: %v", left)
	tree := []int{h.child, h.grandchild}
	for _, l := range left {
		for _, pid := range l.Survivors {
			if !slices.Contains(tree, pid) {
				t.Errorf("leftover %v names pid %d outside the killed host's tree %v", l, pid, tree)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if runtime.GOOS == "windows" {
		if err := procwait.Poll(ctx, func() bool { return subprocesstest.Exited(h.child) && subprocesstest.Exited(h.grandchild) }); err != nil {
			t.Fatalf("the killed host's job did not end its tree: %v", err)
		}
	} else {
		if len(left) != 1 || left[0].Host != h.host.Pid() || left[0].Pid != h.child || !slices.Contains(left[0].Survivors, h.grandchild) {
			t.Fatalf("leftovers = %v; want the grandchild %d of host %d", left, h.grandchild, h.host.Pid())
		}
		// Watched for 200ms after the report: the survivor must not be stopped in that window.
		watch, stop := context.WithTimeout(ctx, 200*time.Millisecond)
		defer stop()
		if procwait.Poll(watch, func() bool { return subprocesstest.Exited(h.grandchild) }) == nil {
			t.Fatalf("survivor %d was stopped after the report", h.grandchild)
		}
		pr, err := os.FindProcess(h.grandchild)
		if err != nil {
			t.Fatal(err)
		}
		_ = pr.Kill()
		if err := procwait.Poll(ctx, func() bool { return subprocesstest.Exited(h.child) && subprocesstest.Exited(h.grandchild) }); err != nil {
			t.Fatalf("tree %v still running after the test's kill: %v", tree, err)
		}
	}
	// Once nothing survives, the next start reports nothing and drops the entry.
	if err := procwait.Poll(ctx, func() bool {
		_, left := openAsNextHost(t, runDir)
		return len(left) == 0 && len(readRecord(t, runDir)) == 0
	}); err != nil {
		t.Fatalf("leftovers after the survivors left: record %+v: %v", readRecord(t, runDir), err)
	}
	if rec[0].Cgroup != "" {
		if _, err := os.Stat(rec[0].Cgroup); err == nil {
			t.Errorf("the dead host's empty control group %s was not removed", rec[0].Cgroup)
		}
	}
}

// lockedBuilder is a strings.Builder safe for the copy goroutine and the test.
type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *lockedBuilder) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedBuilder) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}
