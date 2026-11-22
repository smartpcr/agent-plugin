//go:build scenario

package scenarios

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// exec10Tree is the first fixture, the one of DH-E-EXEC-1: it starts a child, which
// starts a grandchild that calls setsid, ignores SIGTERM and writes its pid; the
// child writes its own pid too. {pids} is the directory both write into.
const exec10Tree = `#!/bin/sh
sh -c '
  echo $$ > "{pids}/child"
  setsid sh -c "trap \"\" TERM; echo \$\$ > \"{pids}/grandchild\"; while :; do sleep 1; done" &
  while :; do sleep 1; done
'
`

// exec10Hog is the second fixture: it writes its pid, then allocates memory
// without bound (awk doubles a string until something stops it).
const exec10Hog = `#!/bin/sh
echo $$ > "{pids}/hog"
exec awk 'BEGIN { s = "x"; for (;;) s = s s }'
`

var (
	exec10Refused = regexp.MustCompile(`(?i)\b(refused|denied)\b`)
	exec10Warning = regexp.MustCompile(`(?im)^.*\bwarn\w*\b.*process-group.*$`)
	exec10Survive = regexp.MustCompile(`(?i)surviv`)
)

// exec10Machine is one Linux target reached over SSH: its alias, the scratch
// directory the scenario made there, and the paths below it.
type exec10Machine struct {
	alias, dir, home, log string
}

// ssh runs line on the target through "ssh -o BatchMode=yes", with stdin as its
// standard input, and returns its combined output.
func (m *exec10Machine) ssh(t *testing.T, line, stdin string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ssh", "-o", "BatchMode=yes", m.alias, line)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// must is ssh that fails the scenario on a non-zero exit.
func (m *exec10Machine) must(t *testing.T, what, line string) string {
	t.Helper()
	out, err := m.ssh(t, line, "")
	if err != nil {
		t.Fatalf("%s: %s on %s: %v\n%s", what, line, m.alias, err, out)
	}
	return out
}

// put writes data to path on the target, executable by its owner only.
func (m *exec10Machine) put(t *testing.T, path string, data []byte) {
	t.Helper()
	if out, err := m.ssh(t, "cat > '"+path+"' && chmod 700 '"+path+"'", string(data)); err != nil {
		t.Fatalf("copying %s to %s: %v\n%s", path, m.alias, err, out)
	}
}

// pids reads the pid files of dir on the target, by file name.
func (m *exec10Machine) pids(t *testing.T, dir string, names ...string) map[string]int {
	t.Helper()
	out := m.must(t, "read the fixture pids", "for f in "+strings.Join(names, " ")+"; do printf '%s=' $f; cat '"+dir+"'/$f 2>/dev/null; echo; done")
	pids := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		name, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if pid, err := strconv.Atoi(v); ok && err == nil {
			pids[name] = pid
		}
	}
	return pids
}

// alive returns the names of the pids that are running processes on the target (a
// zombie runs nothing and counts as gone).
func (m *exec10Machine) alive(t *testing.T, pids map[string]int) []string {
	t.Helper()
	var list []string
	for _, pid := range pids {
		list = append(list, strconv.Itoa(pid))
	}
	if len(list) == 0 {
		return nil
	}
	out := m.must(t, "check the fixture pids", "for p in "+strings.Join(list, " ")+"; do s=$(cut -d' ' -f3 /proc/$p/stat 2>/dev/null) && [ \"$s\" != Z ] && echo $p; done; true")
	var names []string
	for name, pid := range pids {
		if slices.Contains(strings.Fields(out), strconv.Itoa(pid)) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// kill sends SIGKILL to every listed pid still running; it is the scenario's
// cleanup.
func (m *exec10Machine) kill(t *testing.T, pids map[string]int) {
	t.Helper()
	var list []string
	for _, pid := range pids {
		list = append(list, strconv.Itoa(pid))
	}
	if len(list) > 0 {
		if out, err := m.ssh(t, "kill -9 "+strings.Join(list, " ")+" 2>/dev/null; true", ""); err != nil {
			t.Logf("killing %v on %s: %v\n%s", list, m.alias, err, out)
		}
	}
}

// scopes returns the user scope units on the target whose name matches want.
func (m *exec10Machine) scopes(t *testing.T, want *regexp.Regexp) []string {
	t.Helper()
	out := m.must(t, "list the user scopes", "systemctl --user list-units --type=scope --all --plain --no-legend --no-pager")
	var units []string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && want.MatchString(f[0]) {
			units = append(units, f[0])
		}
	}
	slices.Sort(units)
	return units
}

// childGroups lists the control groups below the scope unit's group.
func (m *exec10Machine) childGroups(t *testing.T, unit string) []string {
	t.Helper()
	cg := strings.TrimSpace(m.must(t, "read the scope's control group", "systemctl --user show -p ControlGroup --value '"+unit+"'"))
	if cg == "" {
		t.Fatalf("the scope %s on %s has no control group", unit, m.alias)
	}
	out := m.must(t, "list the scope's child groups", "find '/sys/fs/cgroup"+cg+"' -mindepth 1 -type d 2>/dev/null; true")
	groups := strings.Fields(out)
	slices.Sort(groups)
	return groups
}

// exec10Arch maps "uname -m" to GOARCH.
func exec10Arch(t *testing.T, machine string) string {
	t.Helper()
	switch strings.TrimSpace(machine) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	t.Fatalf("the target's machine is %q; this scenario builds dsh for amd64 and arm64 only", machine)
	return ""
}

// TestLinuxSSHLoginDelegatedScopeOwnsTree: Linux over a real SSH login, the host
// obtains a delegated scope from the user service manager and owns the process
// tree (areas/execution.md DH-E-EXEC-10; DH-D-EXEC-2, DH-ADR-EXEC-2, DH-ADR-40).
// Executed in DH-S-18 (step 18.3); authored in DH-S-6 (gap-closure Stage 13.6,
// M1-G-CLOSE-7). It is not guarded: it changes nothing outside the scratch
// directory it makes on each target and the scope its own host requests.
//
// Given two Linux machines with systemd and cgroup v2, each reached by a real SSH
// login as an ordinary user (so the login's own control group is not writable by
// that user): on the first the user manager runs, on the second it is stopped; the
// binary built from this checkout for the target's architecture, copied to a
// scratch directory there; the first fixture (a grandchild that calls setsid and
// ignores SIGTERM, as in DH-E-EXEC-1) and a second fixture that allocates memory
// without bound. When, on the first machine, (1) dsh is started in the SSH
// session and the owner it reports is read, (2) the first fixture runs through the
// one-shot bash tool with a 2 s timeout, (3) the second runs with Spec.Limits set
// to 256 MiB, (4) a command connects to the host's control socket, and on the
// second machine (5) steps 1 and 2 are repeated, then on the first machine the
// host made exactly one scope request at start and none per command (one new
// scope matching DH_SCENARIO_EXEC10_SCOPE, the same one after every command), the
// results say "owner: cgroup", no fixture process survives the timeout and the
// child group is removed, the second fixture is ended by the limit and its result
// says "limits: enforced", and the connection is refused with the host log
// reporting PeerTree "Exact"; on the second machine one warning names the
// process-group rung, the result says "owner: process-group" and "limits:
// unavailable", and the detached grandchild is reported as a survivor. Cleanup:
// listed pids are killed, the host is stopped and the scope must be gone
// (systemctl --user); if a scope or a group remains, its name and the host log are
// kept, with the scratch directory.
//
// Inputs. DSH_SCENARIO_EXEC10_SSH and DSH_SCENARIO_EXEC10_SSH_NOMANAGER are the
// SSH aliases of the two machines (as "ssh <alias>" reaches them with BatchMode);
// DSH_SCENARIO_EXEC10_SCOPE is the expected scope: a regular expression the name
// of the user scope unit the host requests matches (for example
// `^dsh-.*\.scope$`). The dsh command lines run on the target over {dsh}, {home},
// {log}, {command}, {timeout} and {memory}, where {dsh} is the binary this
// scenario built and copied (each line that runs dsh must run {dsh}):
// DSH_SCENARIO_EXEC10_START starts the host on {home} in the background, logging
// to {log}, and returns once it is ready, printing the owner it reports;
// DSH_SCENARIO_EXEC10_RUN runs the one-shot bash tool through that host with
// {command}, timeout {timeout} and memory limit {memory} (empty for none),
// printing the tool result; DSH_SCENARIO_EXEC10_CONNECT is the {command} that
// connects to the control socket of the host on {home} and prints the answer;
// DSH_SCENARIO_EXEC10_STOP stops the host on {home}. A missing input fails the
// scenario, naming the variable; it never skips.
//
//dsh:scenario DH-E-EXEC-10
func TestLinuxSSHLoginDelegatedScopeOwnsTree(t *testing.T) {
	managed := need(t, "DSH_SCENARIO_EXEC10_SSH", "SSH alias of a Linux machine with systemd, cgroup v2 and a running user manager, logged in as an ordinary user")
	unmanaged := need(t, "DSH_SCENARIO_EXEC10_SSH_NOMANAGER", "SSH alias of a second such machine on which the user manager is stopped")
	scopeText := need(t, "DSH_SCENARIO_EXEC10_SCOPE", "regular expression the name of the scope unit the host requests matches")
	startT := need(t, "DSH_SCENARIO_EXEC10_START", "command starting the host on {home} in the background, log to {log}, printing the owner it reports once ready")
	runT := need(t, "DSH_SCENARIO_EXEC10_RUN", "command running the one-shot bash tool through the host on {home} with {command}, {timeout} and {memory}")
	connectT := need(t, "DSH_SCENARIO_EXEC10_CONNECT", "command, run as the tool's {command}, connecting to the control socket of the host on {home}")
	stopT := need(t, "DSH_SCENARIO_EXEC10_STOP", "command stopping the host on {home}")
	scope, err := regexp.Compile(scopeText)
	if err != nil {
		t.Fatalf("DSH_SCENARIO_EXEC10_SCOPE is not a regular expression: %v", err)
	}
	for name, line := range map[string]string{"DSH_SCENARIO_EXEC10_START": startT, "DSH_SCENARIO_EXEC10_RUN": runT, "DSH_SCENARIO_EXEC10_STOP": stopT} {
		if !strings.Contains(line, "{dsh}") {
			t.Fatalf("%s does not run {dsh}, the binary built from this checkout: %s", name, line)
		}
	}

	// prepare makes a scratch directory on the target, copies the binary built for
	// its architecture and the two fixtures there, and registers the cleanup that
	// kills the listed pids, stops the host and removes the directory (kept, with
	// the host log, when a scope remains or the scenario failed).
	prepare := func(t *testing.T, alias string, pids map[string]int, leftover func() []string) (*exec10Machine, map[string]string) {
		m := &exec10Machine{alias: alias}
		arch := exec10Arch(t, m.must(t, "read the machine type", "uname -m"))
		// The binary is cross-built for the target; the variables are restored when
		// the subtest ends.
		t.Setenv("GOOS", "linux")
		t.Setenv("GOARCH", arch)
		t.Setenv("CGO_ENABLED", "0")
		bin, err := os.ReadFile(dshBinary(t))
		if err != nil {
			t.Fatal(err)
		}
		m.dir = strings.TrimSpace(m.must(t, "make the scratch directory", "mktemp -d"))
		m.home, m.log = m.dir+"/home", m.dir+"/host.log"
		vars := map[string]string{"dsh": m.dir + "/dsh", "home": m.home, "log": m.log}
		m.put(t, vars["dsh"], bin)
		for name, fixture := range map[string]string{"tree": exec10Tree, "hog": exec10Hog} {
			m.must(t, "make the pid directory", "mkdir -p '"+m.dir+"/pids-"+name+"'")
			m.put(t, m.dir+"/"+name+".sh", []byte(strings.ReplaceAll(fixture, "{pids}", m.dir+"/pids-"+name)))
		}
		t.Cleanup(func() {
			m.kill(t, pids)
			if out, err := m.ssh(t, expand(stopT, vars), ""); err != nil {
				t.Errorf("stopping the host on %s: %v\n%s", alias, err, out)
			}
			left := leftover()
			if len(left) > 0 {
				t.Errorf("after the host stopped on %s, %v remain", alias, left)
			}
			if len(left) > 0 || t.Failed() {
				log, err := m.ssh(t, "cat '"+m.log+"'", "")
				t.Logf("kept %s:%s; the host log (%v):\n%s", alias, m.dir, err, log)
				return
			}
			if out, err := m.ssh(t, "rm -rf '"+m.dir+"'", ""); err != nil {
				t.Logf("removing %s:%s: %v\n%s", alias, m.dir, err, out)
			}
		})
		return m, vars
	}
	run := func(t *testing.T, m *exec10Machine, vars map[string]string, command, timeout, memory string) string {
		v := map[string]string{"command": command, "timeout": timeout, "memory": memory}
		for k, val := range vars {
			v[k] = val
		}
		return m.must(t, "run the bash tool", expand(runT, v))
	}
	says := func(t *testing.T, what, out string, want ...string) {
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%s does not say %q:\n%s", what, w, out)
			}
		}
	}

	t.Run("user manager running: one delegated scope owns the tree", func(t *testing.T) {
		pids := map[string]int{}
		var m *exec10Machine
		var hostScope string
		var before []string
		m, vars := prepare(t, managed, pids, func() []string {
			var left []string
			if hostScope != "" && slices.Contains(m.scopes(t, scope), hostScope) {
				left = append(left, "scope "+hostScope)
			}
			return left
		})
		before = m.scopes(t, scope)

		// (1) The host reports the cgroup owner and made exactly one scope request.
		started := m.must(t, "start the host", expand(startT, vars))
		says(t, "the start", started, "owner: cgroup")
		var added []string
		for _, u := range m.scopes(t, scope) {
			if !slices.Contains(before, u) {
				added = append(added, u)
			}
		}
		if len(added) != 1 {
			t.Fatalf("starting the host added scopes %v matching %s, want exactly one", added, scope)
		}
		hostScope = added[0]
		groups := m.childGroups(t, hostScope)

		// (2) A 2 s timeout leaves no fixture process and removes the child group.
		out := run(t, m, vars, m.dir+"/tree.sh", "2s", "")
		for k, v := range m.pids(t, m.dir+"/pids-tree", "child", "grandchild") {
			pids[k] = v
		}
		if len(pids) != 2 {
			t.Fatalf("the tree fixture wrote %v, want a child and a grandchild pid", pids)
		}
		says(t, "the timed-out result", out, "owner: cgroup", "stop: timeout")
		if alive := m.alive(t, pids); len(alive) > 0 {
			t.Errorf("after the timeout the fixture's %v are alive", alive)
		}
		if after := m.childGroups(t, hostScope); !slices.Equal(after, groups) {
			t.Errorf("the scope's child groups are %v after the run, %v before: the child group is not removed", after, groups)
		}

		// (3) The memory limit ends the second fixture and is enforced.
		out = run(t, m, vars, m.dir+"/hog.sh", "60s", "256MiB")
		hog := m.pids(t, m.dir+"/pids-hog", "hog")
		pids["hog"] = hog["hog"]
		if hog["hog"] == 0 {
			t.Errorf("the hog fixture wrote no pid:\n%s", out)
		}
		says(t, "the limited result", out, "limits: enforced")
		if alive := m.alive(t, hog); len(alive) > 0 {
			t.Errorf("the memory limit did not end the hog fixture (pid %d)", hog["hog"])
		}

		// (4) A command cannot reach the host's control socket.
		out = run(t, m, vars, expand(connectT, vars), "10s", "")
		if !exec10Refused.MatchString(out) {
			t.Errorf("the control-socket connection from a command is not refused:\n%s", out)
		}
		log := m.must(t, "read the host log", "cat '"+m.log+"'")
		if !strings.Contains(log, "PeerTree") || !strings.Contains(log, "Exact") {
			t.Errorf("the host log does not report PeerTree Exact for the refused peer:\n%s", log)
		}

		// None of the commands requested another scope.
		var now []string
		for _, u := range m.scopes(t, scope) {
			if !slices.Contains(before, u) {
				now = append(now, u)
			}
		}
		if !slices.Equal(now, []string{hostScope}) {
			t.Errorf("after the commands the new scopes are %v, want only %s: a command requested a scope", now, hostScope)
		}
	})

	t.Run("user manager stopped: process-group rung, a survivor reported", func(t *testing.T) {
		pids := map[string]int{}
		m, vars := prepare(t, unmanaged, pids, func() []string { return nil })

		// (5) Steps 1 and 2 again.
		started := m.must(t, "start the host", expand(startT, vars))
		says(t, "the start", started, "owner: process-group")
		out := run(t, m, vars, m.dir+"/tree.sh", "2s", "")
		for k, v := range m.pids(t, m.dir+"/pids-tree", "child", "grandchild") {
			pids[k] = v
		}
		if pids["grandchild"] == 0 {
			t.Fatalf("the tree fixture wrote %v, want a grandchild pid", pids)
		}
		says(t, "the timed-out result", out, "owner: process-group", "limits: unavailable")
		if !strings.Contains(out, strconv.Itoa(pids["grandchild"])) || !exec10Survive.MatchString(out) {
			t.Errorf("the result does not report the detached grandchild (pid %d) as a survivor:\n%s", pids["grandchild"], out)
		}
		log := m.must(t, "read the host log", "cat '"+m.log+"'")
		if n := len(exec10Warning.FindAllString(log, -1)); n != 1 {
			t.Errorf("%d warnings name the process-group rung, want 1:\n%s", n, log)
		}
	})
}
