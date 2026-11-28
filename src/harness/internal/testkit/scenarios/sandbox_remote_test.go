//go:build scenario

package scenarios

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// The helpers of the Linux confinement scenarios of DH-S-13 (DH-E-SANDBOX-1,
// DH-E-NATIVESYSTEM-1 to -4; executed in DH-S-18, implementation-plan Stage 8.5).
// Each machine is reached by a real SSH login; the binary built from this
// checkout for the machine's architecture is copied to a scratch directory
// there and driven over its SDK protocol through the SSH session's standard
// input and output, so the product is reached only at its boundary. The model is
// the scripted endpoint (scripted_model_test.go) on this machine, forwarded to
// the same loopback port on the target (ssh -R): every command is one bash tool
// call the endpoint scripts, through dsh's whole pipeline, policy and sandbox
// seam, as a model's would.

// sandboxMachinesEnv lists the machines as ';'-separated role=alias items.
const sandboxMachinesEnv = "DSH_SCENARIO_SANDBOX1_MACHINES"

// sandboxRoles are the machine roles of sandboxMachinesEnv (e2e-scenarios.md
// Phase 9, variants of service:linux-sandbox-bwrap, service:linux-landlock-only
// and service:linux-unconfined).
var sandboxRoles = map[string]string{
	"bwrap":           "bwrap on an absolute path and unprivileged user namespaces (preflight \"bwrap Full\")",
	"landlock":        "no bwrap, a kernel with Landlock ABI >= 5 (preflight \"landlock\")",
	"unconfined":      "neither bwrap nor Landlock: Landlock disabled at boot (preflight \"Unconfined\")",
	"landlock-abi1-4": "no bwrap, a kernel whose Landlock ABI is 1, 2, 3 or 4",
	"musl":            "a musl-based system, no bwrap, a kernel with Landlock ABI >= 5",
}

// sandboxMachine returns the SSH alias sandboxMachinesEnv gives role.
func sandboxMachine(t *testing.T, role string) string {
	t.Helper()
	for _, item := range needList(t, sandboxMachinesEnv, "role=alias items; roles bwrap, landlock, unconfined, landlock-abi1-4, musl") {
		r, alias, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(alias) == "" {
			t.Fatalf("%s item %q is not role=alias", sandboxMachinesEnv, item)
		}
		if strings.TrimSpace(r) == role {
			return strings.TrimSpace(alias)
		}
	}
	t.Fatalf("%s names no %q machine: %s", sandboxMachinesEnv, role, sandboxRoles[role])
	return ""
}

// sandboxInputs are the run-time inputs every confinement scenario reads.
type sandboxInputs struct {
	layer string // template over {endpoint}, {extra} and {require}
	route *protocol.ModelRoute
}

func readSandboxInputs(t *testing.T) sandboxInputs {
	t.Helper()
	return sandboxInputs{
		layer: need(t, "DSH_SCENARIO_SANDBOX1_LAYER", "a layer over {endpoint}, {extra} and {require} registering DSH_SCENARIO_SANDBOX1_ROUTE on {endpoint} with a scenario-local key, enabling the bash tool, adding {extra} as a write root and setting the sandbox's require to {require} (any or full)"),
		route: &protocol.ModelRoute{
			Provider: need(t, "DSH_SCENARIO_SANDBOX1_ROUTE", "the route the layer registers on the scripted endpoint"),
			Model:    need(t, "DSH_SCENARIO_SANDBOX1_MODEL", "a model of that route"),
		},
	}
}

// pureGo is the release build: the binary for arch with cgo off.
func pureGo(t *testing.T, arch string) string {
	t.Helper()
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", arch)
	t.Setenv("CGO_ENABLED", "0")
	return dshBinary(t)
}

// remoteDsh is one target: its scratch directory, the copied binary and home,
// and the scenario's paths there: W the workspace (a git root), X an extra
// write root, O a directory outside both and B a file beside W.
type remoteDsh struct {
	*exec10Machine
	bin, ws, extra, outside, beside string
}

// prepareRemote makes the scratch directory on alias and copies there the binary
// build makes for the target's architecture. At cleanup the directory is removed;
// when the scenario failed it is kept and its listing and the kernel version are
// logged.
func prepareRemote(t *testing.T, alias string, build func(*testing.T, string) string) *remoteDsh {
	t.Helper()
	m := &exec10Machine{alias: alias}
	arch := exec10Arch(t, m.must(t, "read the machine type", "uname -m"))
	data, err := os.ReadFile(build(t, arch))
	if err != nil {
		t.Fatal(err)
	}
	m.dir = strings.TrimSpace(m.must(t, "make the scratch directory", "mktemp -d"))
	m.home, m.log = m.dir+"/home", m.dir+"/host.log"
	r := &remoteDsh{exec10Machine: m, bin: m.dir + "/dsh", ws: m.dir + "/W", extra: m.dir + "/X", outside: m.dir + "/O", beside: m.dir + "/beside"}
	m.put(t, r.bin, data)
	m.must(t, "make the scenario directories", "mkdir -p '"+r.ws+"/.git' '"+r.extra+"' '"+r.outside+"' '"+m.home+"' && printf keep > '"+r.beside+"'")
	t.Logf("%s: kernel %s", alias, strings.TrimSpace(m.must(t, "read the kernel version", "uname -r")))
	t.Cleanup(func() {
		if t.Failed() {
			out, err := m.ssh(t, "uname -a; ls -laR '"+m.dir+"'", "")
			t.Logf("kept %s:%s (%v):\n%s", alias, m.dir, err, out)
			return
		}
		if out, err := m.ssh(t, "rm -rf '"+m.dir+"'", ""); err != nil {
			t.Logf("removing %s:%s: %v\n%s", alias, m.dir, err, out)
		}
	})
	return r
}

// dsh runs the copied binary on the target with args, on the scratch home.
func (r *remoteDsh) dsh(t *testing.T, args string) (string, error) {
	t.Helper()
	return r.ssh(t, "env DSH_HOME='"+r.home+"' '"+r.bin+"' "+args, "")
}

// status is "dsh sandbox status" on the target; it must exit 0.
func (r *remoteDsh) status(t *testing.T) string {
	t.Helper()
	out, err := r.dsh(t, "sandbox status")
	if err != nil {
		t.Fatalf("dsh sandbox status on %s: %v\n%s", r.alias, err, out)
	}
	t.Logf("%s: dsh sandbox status:\n%s", r.alias, out)
	return out
}

// exists reports whether path names anything on the target (a dangling link
// included).
func (r *remoteDsh) exists(t *testing.T, path string) bool {
	t.Helper()
	out := r.must(t, "look for "+path, "if [ -e '"+path+"' ] || [ -L '"+path+"' ]; then echo yes; else echo no; fi")
	return strings.TrimSpace(out) == "yes"
}

// layer writes the input layer, expanded for m and require, to the target.
func (r *remoteDsh) layer(t *testing.T, in sandboxInputs, m *scriptedModel, require, name string) string {
	t.Helper()
	path := r.dir + "/" + name + ".yaml"
	r.put(t, path, []byte(expand(in.layer, map[string]string{"endpoint": m.URL, "extra": r.extra, "require": require})))
	return path
}

// sdk starts bin (the copied binary unless another path on the target is given)
// on the target with layer, in "sdk" mode over the SSH session, with the scripted
// endpoint's port forwarded to the same port there. env are NAME=value entries
// of the host's own environment on the target.
func (r *remoteDsh) sdk(t *testing.T, bin, layer string, m *scriptedModel, env ...string) *sdkHost {
	t.Helper()
	u, err := url.Parse(m.URL)
	if err != nil {
		t.Fatal(err)
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatalf("ssh: %v", err)
	}
	forward := u.Host + ":" + u.Host
	args := []string{"-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes", "-R", forward, r.alias,
		"env", "DSH_HOME=" + r.home}
	args = append(append(args, env...), bin, "--layer", layer)
	var local []string
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		local = append(local, "SSH_AUTH_SOCK="+sock)
	}
	return startSDK(t, ssh, local, args)
}

// shellOutcome is the structured result of one shell tool call
// (world/shellexec/tools Result), as the model receives it.
type shellOutcome struct {
	Outcome  string `json:"outcome"`
	ExitCode int    `json:"exit_code"`
	Stdout   struct {
		Tail string `json:"tail"`
	} `json:"stdout"`
	Stderr struct {
		Tail string `json:"tail"`
	} `json:"stderr"`
	Confinement struct {
		Mode    string   `json:"mode"`
		State   string   `json:"state"`
		Backend string   `json:"backend"`
		Missing []string `json:"missing"`
		Exposed []string `json:"exposed"`
	} `json:"confinement"`
	Error string `json:"error"`
}

// record is the outcome in the form the evidence record copies: "<state>,
// <backend>, exit <n>, missing [..], exposed [..]".
func (s shellOutcome) record() string {
	return fmt.Sprintf("%s, %s, exit %d, missing [%s], exposed [%s]", s.Confinement.State, s.Confinement.Backend, s.ExitCode,
		strings.Join(s.Confinement.Missing, ","), strings.Join(s.Confinement.Exposed, ","))
}

// sandboxVerdict is the head of the "dsh sandbox status" text report (cli
// sandboxReport): the state (Full, Partial or Unconfined), the backend, and
// the missing and exposed effects as listed ("none" when empty). The backend
// lines after it name every backend of the chain, available or not, so only
// this head says which one the machine has.
type sandboxVerdict struct {
	State, Backend, Missing, Exposed string
}

// readVerdict reads the head of the status report st; a field it lacks stays
// empty.
func readVerdict(st string) sandboxVerdict {
	var v sandboxVerdict
	for _, line := range strings.Split(st, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "state":
			v.State = val
		case "backend":
			v.Backend = val
		case "missing":
			v.Missing = val
		case "exposed":
			v.Exposed = val
		case "backends":
			return v
		}
	}
	return v
}

// String is the verdict in the form the evidence record copies: "<State>
// <backend>, missing [..], exposed [..]".
func (v sandboxVerdict) String() string {
	return fmt.Sprintf("%s %s, missing [%s], exposed [%s]", v.State, v.Backend, v.Missing, v.Exposed)
}

// verdictProblem is why v is not backend in one of states (Full or Partial),
// or "" when it is; a Partial verdict must name a missing effect.
func verdictProblem(v sandboxVerdict, backend string, states ...string) string {
	switch {
	case v.Backend != backend || !slices.Contains(states, v.State):
		return fmt.Sprintf("status reports %s; want backend %s in state %v", v, backend, states)
	case v.State == "Partial" && (v.Missing == "" || v.Missing == "none"):
		return fmt.Sprintf("status reports %s: Partial without naming a missing effect", v)
	}
	return ""
}

// mustVerdict is the status oracle of a confining machine: "dsh sandbox status"
// on r reports backend in one of states. The verdict is logged as the line the
// evidence record copies.
func (r *remoteDsh) mustVerdict(t *testing.T, backend string, states ...string) {
	t.Helper()
	v := readVerdict(r.status(t))
	t.Logf("%s: enforcement %s", r.alias, v)
	if p := verdictProblem(v, backend, states...); p != "" {
		t.Errorf("%s: %s", r.alias, p)
	}
}

// shellResult finds the structured shell result in the tool result o: the JSON
// object with an "outcome" member in any text of the logged message.
func shellResult(t *testing.T, o *toolOutcome) (shellOutcome, bool) {
	t.Helper()
	var doc any
	if err := json.Unmarshal(o.Raw, &doc); err != nil {
		t.Fatalf("the result of %s is not JSON: %v", o.ID, err)
	}
	var found *shellOutcome
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			var s shellOutcome
			if found == nil && strings.Contains(x, `"outcome"`) && json.Unmarshal([]byte(x), &s) == nil && s.Outcome != "" {
				found = &s
			}
		}
	}
	walk(doc)
	if found == nil {
		return shellOutcome{}, false
	}
	return *found, true
}

// bashCall is one scripted call of the bash tool.
func bashCall(command string) scriptedCall {
	return scriptedCall{Name: "bash", Args: map[string]any{"command": command}, Tag: command}
}

// runCommands scripts one bash call per command, runs one prompt in session and
// returns the outcomes in order; every scripted call must have been made.
func runCommands(t *testing.T, h *sdkHost, m *scriptedModel, session string, commands ...string) []*toolOutcome {
	t.Helper()
	for _, c := range commands {
		m.script(bashCall(c))
	}
	got := toolOutcomes(t, h.runPrompt(t, session, "Run the scripted commands.", 10*time.Minute))
	if n := m.pending(); n != 0 || len(got) != len(commands) {
		t.Fatalf("the turn made %d of %d scripted calls (%d not made)", len(got), len(commands), n)
	}
	return got
}

// mustExit is the shell result of o, which must be a command that ran and exited
// with code.
func mustExit(t *testing.T, o *toolOutcome, command string, code int) shellOutcome {
	t.Helper()
	s, ok := shellResult(t, o)
	if !ok || s.Outcome != "exited" {
		t.Fatalf("%q did not run to an exit: %s", command, o.Raw)
	}
	if s.ExitCode != code {
		t.Errorf("%q exited %d, want %d as the command's own: %s", command, s.ExitCode, code, o.Raw)
	}
	return s
}

// mustRefuse checks that o is a start refused with one of codes, before any
// command ran.
func mustRefuse(t *testing.T, o *toolOutcome, command string, codes ...string) {
	t.Helper()
	if s, ok := shellResult(t, o); ok && s.Outcome == "exited" {
		t.Fatalf("%q ran (exit %d); want its start refused with one of %v", command, s.ExitCode, codes)
	}
	if !o.IsError || !slices.ContainsFunc(codes, o.has) {
		t.Fatalf("%q answered %s; want an error naming one of %v", command, o.Raw, codes)
	}
}
