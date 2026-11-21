//go:build scenario

package scenarios

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// exec1Fixture is the fixture program: it starts a child, which starts a grandchild
// that calls setsid, ignores SIGTERM and writes its pid; the child writes its own
// pid too. {pids} is the directory both write into.
const exec1Fixture = `#!/bin/sh
sh -c '
  echo $$ > "{pids}/child"
  setsid sh -c "trap \"\" TERM; echo \$\$ > \"{pids}/grandchild\"; while :; do sleep 1; done" &
  while :; do sleep 1; done
'
`

// exec1Pids reads the pids the fixture wrote, waiting up to within for both.
func exec1Pids(t *testing.T, dir string, within time.Duration) map[string]int {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		pids := map[string]int{}
		for _, name := range []string{"child", "grandchild"} {
			if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					pids[name] = pid
				}
			}
		}
		if len(pids) == 2 {
			return pids
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s the fixture wrote %v, want a child and a grandchild pid", within, pids)
		}
		<-time.After(50 * time.Millisecond)
	}
}

// procAlive reports whether pid is a running process on Linux.
func procAlive(pid int) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	// A zombie is gone for the purpose of a tree kill: it runs nothing.
	fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
	return len(fields) == 0 || fields[0] != "Z"
}

// killListed kills every pid still alive; it is the scenario's cleanup.
func killListed(pids map[string]int) {
	for _, pid := range pids {
		if procAlive(pid) {
			_ = sigkill(pid)
		}
	}
}

// sigkill sends SIGKILL to pid.
func sigkill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(os.Kill)
}

var exec1Warning = regexp.MustCompile(`(?im)^.*\bwarn\w*\b.*process-group.*$`)

// TestLinuxWholeProcessTreeIsKilled: Linux: a whole process tree is killed through
// its control group (areas/execution.md DH-E-EXEC-1; DH-D-EXEC-2). Executed in
// DH-S-18 (step 18.3) on a Linux host with cgroup v2; authored in DH-S-6.
//
// Given the binary built from this checkout, a Linux host with cgroup v2 where the
// test user's cgroup is writable and a
// fixture program that starts a child, which starts a grandchild that calls setsid,
// ignores SIGTERM and writes its pid to a file, when (1) the one-shot bash tool runs
// the fixture with a 2 s timeout, (2) the same run is repeated and the dsh process
// is sent SIGKILL mid-run, and (3) it is repeated on a host with no writable cgroup
// and no user service manager (a container without systemd), then after (1) no
// fixture pid exists, the result says "stop: timeout" and "owner: cgroup" and the
// child cgroup directory is removed; after (2) the direct child is gone and the
// next dsh start on that home lists the surviving members; in (3) the result says
// "owner: process-group" and one warning was logged. Cleanup: listed pids are
// killed and leftover cgroup directories removed; the runtime owner file is kept
// if removal fails.
//
// Inputs are shell command lines over {dsh}, {home}, {fixture}, {timeout} and {log},
// where {dsh} is the binary this scenario builds from the checkout under test; each
// line that runs dsh must run {dsh}, so no other installation is exercised:
// DSH_SCENARIO_EXEC1_RUN runs the bash tool once through {dsh} on {home} with the
// command {fixture} and timeout {timeout}, printing the tool result, its log written
// to {log}; DSH_SCENARIO_EXEC1_START is the same run started in the foreground with
// exec, so the shell's pid is dsh's, writing that pid to {home}/dsh.pid;
// DSH_SCENARIO_EXEC1_CGROUPS prints the child cgroup directories dsh created for
// {home}, one per line; DSH_SCENARIO_EXEC1_NOCGROUP is DSH_SCENARIO_EXEC1_RUN inside
// a container with no writable cgroup and no user service manager, sharing {dsh},
// {home} and the fixture's directory.
//
//dsh:scenario DH-E-EXEC-1
func TestLinuxWholeProcessTreeIsKilled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Fatalf("DH-E-EXEC-1 runs on Linux only; this runner is %s (the run is open, not passed)", runtime.GOOS)
	}
	bin := dshBinary(t)
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	vars := func(step string) (map[string]string, string) {
		pids := filepath.Join(dir, "pids-"+step)
		if err := os.MkdirAll(pids, 0o700); err != nil {
			t.Fatal(err)
		}
		fixture := filepath.Join(dir, "fixture-"+step+".sh")
		if err := os.WriteFile(fixture, []byte(strings.ReplaceAll(exec1Fixture, "{pids}", pids)), 0o700); err != nil {
			t.Fatal(err)
		}
		return map[string]string{"dsh": bin, "home": home, "fixture": fixture, "timeout": "2s", "log": filepath.Join(dir, step+".log")}, pids
	}
	run := need(t, "DSH_SCENARIO_EXEC1_RUN", "command running the bash tool once through {dsh} on {home} with {fixture} and {timeout}, log to {log}")
	start := need(t, "DSH_SCENARIO_EXEC1_START", "the same run, exec'd in the foreground, writing dsh's pid to {home}/dsh.pid")
	cgroups := need(t, "DSH_SCENARIO_EXEC1_CGROUPS", "command printing the child cgroup directories dsh created for {home}")
	noCgroup := need(t, "DSH_SCENARIO_EXEC1_NOCGROUP", "the run inside a container with no writable cgroup and no user service manager")
	for name, line := range map[string]string{"DSH_SCENARIO_EXEC1_RUN": run, "DSH_SCENARIO_EXEC1_START": start, "DSH_SCENARIO_EXEC1_NOCGROUP": noCgroup} {
		if !strings.Contains(line, "{dsh}") {
			t.Fatalf("%s does not run {dsh}, the binary built from this checkout: %s", name, line)
		}
	}

	t.Run("timeout kills the tree", func(t *testing.T) {
		v, pidDir := vars("timeout")
		out := mustShell(t, expand(run, v))
		pids := exec1Pids(t, pidDir, time.Second)
		t.Cleanup(func() { killListed(pids) })
		for name, pid := range pids {
			if procAlive(pid) {
				t.Errorf("after the timeout the fixture's %s (pid %d) is alive", name, pid)
			}
		}
		for _, want := range []string{"stop: timeout", "owner: cgroup"} {
			if !strings.Contains(out, want) {
				t.Errorf("the result does not say %q:\n%s", want, out)
			}
		}
		for _, d := range strings.Fields(mustShell(t, expand(cgroups, v))) {
			if _, err := os.Stat(d); err == nil {
				t.Errorf("the child cgroup %s is not removed", d)
				t.Cleanup(func() { _ = os.Remove(d) })
			}
		}
	})

	t.Run("SIGKILL of dsh leaves survivors the next start lists", func(t *testing.T) {
		v, pidDir := vars("sigkill")
		v["timeout"] = "60s"
		r := startShell(t, expand(start, v))
		pids := exec1Pids(t, pidDir, 10*time.Second)
		t.Cleanup(func() { killListed(pids) })
		data, err := os.ReadFile(filepath.Join(home, "dsh.pid"))
		if err != nil {
			t.Fatalf("dsh's pid: %v", err)
		}
		dsh, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		if err := sigkill(dsh); err != nil {
			t.Fatalf("SIGKILL dsh (pid %d): %v", dsh, err)
		}
		// A survivor holding the start line's output pipe would keep r.done open;
		// the registered killListed cleanup runs before startShell's wait.
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
			t.Fatalf("the start line did not return after dsh's SIGKILL: %s", r.line)
		}
		deadline := time.Now().Add(5 * time.Second)
		for procAlive(pids["child"]) && time.Now().Before(deadline) {
			<-time.After(50 * time.Millisecond)
		}
		if procAlive(pids["child"]) {
			t.Errorf("the direct child (pid %d) outlives dsh's SIGKILL", pids["child"])
		}
		next, _ := vars("next")
		out, _ := shell(t, expand(run, next))
		for name, pid := range pids {
			if procAlive(pid) && !strings.Contains(out, strconv.Itoa(pid)) {
				t.Errorf("the next start does not list the surviving %s (pid %d):\n%s", name, pid, out)
			}
		}
	})

	t.Run("no cgroup falls back to the process group", func(t *testing.T) {
		v, pidDir := vars("nocgroup")
		out := mustShell(t, expand(noCgroup, v))
		pids := exec1Pids(t, pidDir, time.Second)
		t.Cleanup(func() { killListed(pids) })
		if !strings.Contains(out, "owner: process-group") {
			t.Errorf("the result does not say owner: process-group:\n%s", out)
		}
		log, err := os.ReadFile(v["log"])
		if err != nil {
			t.Fatal(err)
		}
		if n := len(exec1Warning.FindAll(log, -1)); n != 1 {
			t.Errorf("%d process-group warnings logged, want 1:\n%s", n, log)
		}
	})
}
