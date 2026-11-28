//go:build scenario

package scenarios

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
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

// exec1Quote quotes s as one word of a POSIX shell line.
func exec1Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// exec1Wait waits on the target, up to secs seconds, until every named file of
// dir is non-empty, and returns the pids they hold, by file name. Every pid found
// goes into into, under prefix plus its name, before it fails on a missing one,
// so the cleanup kills what a half-ready fixture started.
func exec1Wait(t *testing.T, m *exec10Machine, dir string, secs int, into map[string]int, prefix string, names ...string) map[string]int {
	t.Helper()
	pids, ok := exec1Ready(t, m, dir, secs, names...)
	for k, pid := range pids {
		into[prefix+k] = pid
	}
	if !ok {
		t.Fatalf("after %d s the fixture on %s wrote %v, want %v", secs, m.alias, pids, names)
	}
	return pids
}

// exec1Ready waits like exec1Wait but returns what it found and whether every
// named file held a pid, instead of failing.
func exec1Ready(t *testing.T, m *exec10Machine, dir string, secs int, names ...string) (map[string]int, bool) {
	t.Helper()
	var cond []string
	for _, n := range names {
		cond = append(cond, "[ -s "+exec1Quote(dir+"/"+n)+" ]")
	}
	m.must(t, "wait for the pids", fmt.Sprintf("i=0; until %s; do i=$((i+1)); [ $i -gt %d ] && break; sleep 0.05; done; true", strings.Join(cond, " && "), secs*20))
	pids := m.pids(t, dir, names...)
	return pids, len(pids) == len(names)
}

// exec1Sweep is the line that kills, on the target, everything a launch in the
// scratch directory dir may have left: the launch's session (its leader's pid in
// dir/start.pid; the start line runs in a session of its own) and every pid
// recorded in a pid file of pidDir or in home/dsh.pid, read from the files, so it
// needs no readiness to have been reached.
func exec1Sweep(dir, home, pidDir string) string {
	return "p=$(cat " + exec1Quote(dir+"/start.pid") + ` 2>/dev/null) && [ -n "$p" ] && kill -9 -"$p" 2>/dev/null; ` +
		"for f in " + exec1Quote(pidDir) + "/* " + exec1Quote(home+"/dsh.pid") + `; do [ -s "$f" ] && kill -9 $(cat "$f") 2>/dev/null; done; true`
}

// exec1Launch starts line on the target detached from the SSH session (so a
// survivor holding its output cannot hold the session open), in a session of its
// own whose leader's pid goes to m.dir/start.pid, and registers the cleanup that
// runs exec1Sweep before it waits for anything: a launch whose fixture or dsh.pid
// never appears is still ended. It returns the fixture's child and grandchild pids
// and dsh's, or an error naming what did not appear within secs seconds.
func exec1Launch(t *testing.T, m *exec10Machine, line, pidDir string, secs int) (map[string]int, int, error) {
	t.Helper()
	t.Cleanup(func() {
		if out, err := m.ssh(t, exec1Sweep(m.dir, m.home, pidDir), ""); err != nil {
			t.Logf("sweeping the launch on %s: %v\n%s", m.alias, err, out)
		}
	})
	m.must(t, "start dsh", "nohup setsid sh -c "+exec1Quote(line)+" </dev/null >"+exec1Quote(m.dir+"/start.out")+" 2>&1 & echo $! > "+exec1Quote(m.dir+"/start.pid"))
	tree, ok := exec1Ready(t, m, pidDir, secs, "child", "grandchild")
	if !ok {
		return tree, 0, fmt.Errorf("after %d s the fixture on %s wrote %v, want a child and a grandchild pid", secs, m.alias, tree)
	}
	host, ok := exec1Ready(t, m, m.home, secs, "dsh.pid")
	if !ok {
		return tree, 0, fmt.Errorf("after %d s dsh on %s wrote no %s/dsh.pid", secs, m.alias, m.home)
	}
	return tree, host["dsh.pid"], nil
}

// exec1Gone waits on the target, up to secs seconds, until pid runs nothing (gone
// or a zombie).
func exec1Gone(t *testing.T, m *exec10Machine, pid, secs int) {
	t.Helper()
	m.must(t, "wait for the pid to end", fmt.Sprintf(`i=0; while s=$(cut -d' ' -f3 /proc/%d/stat 2>/dev/null) && [ "$s" != Z ]; do i=$((i+1)); [ $i -gt %d ] && break; sleep 0.05; done; true`, pid, secs*20))
}

// exec1Prepare makes a scratch directory on the target behind alias with a home in
// it, copies the binary under test for the target's architecture there
// (exec10Binary), and registers the cleanup: the listed pids are killed, the
// listed leftover child cgroup directories removed, and the scratch directory
// removed unless the scenario failed or the removal fails (then it is kept, with
// the logs and the runtime owner file).
func exec1Prepare(t *testing.T, alias string, pids map[string]int, groups *[]string) (*exec10Machine, string) {
	t.Helper()
	m := &exec10Machine{alias: alias}
	arch := exec10Arch(t, m.must(t, "read the machine type", "uname -m"))
	bin := exec10Binary(t, arch)
	m.dir = strings.TrimSpace(m.must(t, "make the scratch directory", "mktemp -d"))
	m.home = m.dir + "/home"
	m.must(t, "make the home", "mkdir -m 700 "+exec1Quote(m.home))
	dsh := m.dir + "/dsh"
	m.put(t, dsh, bin)
	t.Cleanup(func() {
		m.kill(t, pids)
		for _, g := range *groups {
			if out, err := m.ssh(t, "rmdir "+exec1Quote(g), ""); err != nil {
				t.Logf("removing the cgroup %s on %s: %v\n%s", g, alias, err, out)
			}
		}
		if t.Failed() {
			t.Logf("kept %s:%s", alias, m.dir)
			return
		}
		if out, err := m.ssh(t, "rm -rf "+exec1Quote(m.dir), ""); err != nil {
			t.Logf("kept %s:%s, removing it failed: %v\n%s", alias, m.dir, err, out)
		}
	})
	return m, dsh
}

// exec1Vars writes the fixture of step to the target's scratch directory and
// returns the line variables for that step and the fixture's pid directory.
func exec1Vars(t *testing.T, m *exec10Machine, dsh, step string) (map[string]string, string) {
	t.Helper()
	pids := m.dir + "/pids-" + step
	m.must(t, "make the pid directory", "mkdir -p "+exec1Quote(pids))
	fixture := m.dir + "/fixture-" + step + ".sh"
	m.put(t, fixture, []byte(strings.ReplaceAll(exec1Fixture, "{pids}", pids)))
	return map[string]string{"dsh": dsh, "home": m.home, "fixture": fixture, "timeout": "2s", "log": m.dir + "/" + step + ".log"}, pids
}

var exec1Warning = regexp.MustCompile(`(?im)^.*\bwarn\w*\b.*process-group.*$`)

// TestLinuxWholeProcessTreeIsKilled: Linux: a whole process tree is killed through
// its control group (areas/execution.md DH-E-EXEC-1; DH-D-EXEC-2). Executed in
// DH-S-18 (step 18.3) on team-kept Linux VMs over SSH; authored in DH-S-6.
//
// Given the binary under test for the target's architecture (exec10Binary: the
// rehearsal's binary under the DH-S-18 hand-off), copied over SSH to a Linux VM with
// cgroup v2 where the login user's cgroup is delegated by its running user manager,
// and a fixture program that starts a child, which starts a grandchild that calls
// setsid, ignores SIGTERM and writes its pid to a file, when (1) the one-shot bash
// tool runs the fixture with a 2 s timeout, (2) the same run is repeated and the dsh
// process is sent SIGKILL mid-run, and (3) it is repeated on the second VM, with no
// writable cgroup and no user service manager, then after (1) no fixture pid exists,
// the result says "stop: timeout" and "owner: cgroup" and the child cgroup directory
// is removed; after (2) the direct child is gone and the next dsh start on that home
// lists the surviving members; in (3) the result says "owner: process-group" and one
// warning was logged. Every process, pid file, cgroup and log the scenario checks is
// the VM's, read over SSH; the runner only drives. Cleanup: listed pids are killed
// and leftover cgroup directories removed; the scratch directory, with the runtime
// owner file, is kept if the scenario failed or its removal fails.
//
// Inputs. DSH_SCENARIO_EXEC10_SSH and DSH_SCENARIO_EXEC10_SSH_NOMANAGER are the SSH
// aliases of the two VMs, as in DH-E-EXEC-10 (variants.md: both scenarios share the
// machine set). The command lines run on the VM over {dsh}, {home}, {fixture},
// {timeout} and {log}, where {dsh} is the binary this scenario copied; each line
// that runs dsh must run {dsh}, so no other installation is exercised:
// DSH_SCENARIO_EXEC1_RUN runs the bash tool once through {dsh} on {home} with the
// command {fixture} and timeout {timeout}, printing the tool result, its log written
// to {log}; DSH_SCENARIO_EXEC1_START is the same run started in the foreground with
// exec, so the shell's pid is dsh's, writing that pid to {home}/dsh.pid (the
// scenario starts it detached from the SSH session); DSH_SCENARIO_EXEC1_CGROUPS
// prints the child cgroup directories dsh created for {home}, one per line. A
// missing input fails the scenario, naming the variable; it never skips.
//
//dsh:scenario DH-E-EXEC-1
func TestLinuxWholeProcessTreeIsKilled(t *testing.T) {
	managed := need(t, "DSH_SCENARIO_EXEC10_SSH", "SSH alias of a Linux VM with cgroup v2 delegated by a running user manager, logged in as an ordinary user")
	unmanaged := need(t, "DSH_SCENARIO_EXEC10_SSH_NOMANAGER", "SSH alias of a second such VM with no writable cgroup and no user service manager")
	run := need(t, "DSH_SCENARIO_EXEC1_RUN", "command running the bash tool once through {dsh} on {home} with {fixture} and {timeout}, log to {log}")
	start := need(t, "DSH_SCENARIO_EXEC1_START", "the same run, exec'd in the foreground, writing dsh's pid to {home}/dsh.pid")
	cgroups := need(t, "DSH_SCENARIO_EXEC1_CGROUPS", "command printing the child cgroup directories dsh created for {home}")
	for name, line := range map[string]string{"DSH_SCENARIO_EXEC1_RUN": run, "DSH_SCENARIO_EXEC1_START": start} {
		if !strings.Contains(line, "{dsh}") {
			t.Fatalf("%s does not run {dsh}, the binary under test: %s", name, line)
		}
	}

	pids := map[string]int{}
	var groups []string
	m, dsh := exec1Prepare(t, managed, pids, &groups)

	t.Run("timeout kills the tree", func(t *testing.T) {
		v, pidDir := exec1Vars(t, m, dsh, "timeout")
		out := m.must(t, "run the bash tool", expand(run, v))
		tree := exec1Wait(t, m, pidDir, 1, pids, "timeout-", "child", "grandchild")
		if alive := m.alive(t, tree); len(alive) > 0 {
			t.Errorf("after the timeout the fixture's %v are alive on %s", alive, m.alias)
		}
		for _, want := range []string{"stop: timeout", "owner: cgroup"} {
			if !strings.Contains(out, want) {
				t.Errorf("the result does not say %q:\n%s", want, out)
			}
		}
		listed := strings.Fields(m.must(t, "list the child cgroups", expand(cgroups, v)))
		if len(listed) > 0 {
			var test []string
			for _, d := range listed {
				test = append(test, exec1Quote(d))
			}
			left := strings.Fields(m.must(t, "check the child cgroups", "for d in "+strings.Join(test, " ")+`; do [ -d "$d" ] && echo "$d"; done; true`))
			for _, d := range left {
				t.Errorf("the child cgroup %s on %s is not removed", d, m.alias)
			}
			groups = append(groups, left...)
		}
	})

	t.Run("SIGKILL of dsh leaves survivors the next start lists", func(t *testing.T) {
		v, pidDir := exec1Vars(t, m, dsh, "sigkill")
		v["timeout"] = "60s"
		tree, host, err := exec1Launch(t, m, expand(start, v), pidDir, 10)
		for k, pid := range tree {
			pids["sigkill-"+k] = pid
		}
		if err != nil {
			t.Fatal(err)
		}
		pids["dsh"] = host
		m.must(t, "SIGKILL dsh", fmt.Sprintf("kill -9 %d", host))
		exec1Gone(t, m, host, 10)
		if alive := m.alive(t, map[string]int{"dsh": host}); len(alive) > 0 {
			t.Fatalf("dsh (pid %d) on %s runs on after its SIGKILL", host, m.alias)
		}
		exec1Gone(t, m, tree["child"], 5)
		if alive := m.alive(t, map[string]int{"child": tree["child"]}); len(alive) > 0 {
			t.Errorf("the direct child (pid %d) outlives dsh's SIGKILL", tree["child"])
		}
		next, nextDir := exec1Vars(t, m, dsh, "next")
		out, _ := m.ssh(t, expand(run, next), "")
		for k, pid := range m.pids(t, nextDir, "child", "grandchild") {
			pids["next-"+k] = pid
		}
		for _, name := range m.alive(t, tree) {
			if pid := tree[name]; !strings.Contains(out, strconv.Itoa(pid)) {
				t.Errorf("the next start does not list the surviving %s (pid %d):\n%s", name, pid, out)
			}
		}
	})

	t.Run("no cgroup falls back to the process group", func(t *testing.T) {
		left := map[string]int{}
		var none []string
		n, ndsh := exec1Prepare(t, unmanaged, left, &none)
		v, pidDir := exec1Vars(t, n, ndsh, "nocgroup")
		out := n.must(t, "run the bash tool", expand(run, v))
		exec1Wait(t, n, pidDir, 1, left, "", "child", "grandchild")
		if !strings.Contains(out, "owner: process-group") {
			t.Errorf("the result does not say owner: process-group:\n%s", out)
		}
		log := n.must(t, "read the log", "cat "+exec1Quote(v["log"]))
		if c := len(exec1Warning.FindAllString(log, -1)); c != 1 {
			t.Errorf("%d process-group warnings logged, want 1:\n%s", c, log)
		}
	})
}
