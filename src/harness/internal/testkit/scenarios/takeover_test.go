//go:build scenario

package scenarios

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The shared runner of the takeover scenarios DH-E-HOST-7 (Linux), DH-E-HOST-8
// (macOS) and DH-E-HOST-9 (Windows), authored in DH-S-15 and run in DH-S-39
// (docs/harness/areas/host-telemetry.md): a command the shell tool runs for the
// model tries to become the host's owner, and every harness interface refuses it
// or, where the backend does not govern a path, the result says so.
//
// A run: a fresh home holding the key K (DSH_SCENARIO_TAKEOVER_STORE over {dsh},
// {home} and {protection}); the scripted model (scripted_model_test.go) behind a
// layer (DSH_SCENARIO_TAKEOVER_LAYER over {endpoint}, {mode}, {network} and
// {workspace}: the route, the sandbox mode, network allowed or denied, and a tool
// that always asks); the listening host on a terminal (startWebHost); one real
// browser signed in with the printed link (DSH_SCENARIO_TAKEOVER_SIGNIN over
// {link}, printing the session cookie's value); a launch token planted for the
// test (launch.open with print on the control endpoint); a session that runs one
// prompt (DSH_SCENARIO_TAKEOVER_PROMPT over {origin}, {cookie}, {workspace} and
// {text}, printing the session id) whose one scripted call is the bash tool
// running DSH_SCENARIO_TAKEOVER_COMMAND over {script} (DSH_SCENARIO_TAKEOVER_SCRIPT)
// and {vars}.
//
// {vars} is a JSON file in the workspace, never on the command line (tool
// arguments are logged in the home): home, socket, origin, pid, dsh, cookie, the
// planted token, a nonce to write wherever a write is tried, and log, the path in
// the workspace where the script appends one JSON object per step it tries
// (takeoverStep). Steps every platform logs: read-secrets, read-sessions,
// control:<method> for each control method (host.stop as the script's very last
// step, so a stop that is answered ends nothing the run still needs), get-no-cookie, post-no-cookie,
// get-cookie, post-cookie (an answer to the pending approval), exchange (the
// planted token), write-session, write-layer and tool-call (dsh tool call for the
// asking tool, which does not wait for the approval); each scenario names its
// platform's further steps. Afterwards DSH_SCENARIO_TAKEOVER_PENDING over
// {origin}, {cookie} and {session} prints the number of approvals pending, and
// DSH_SCENARIO_TAKEOVER_CLEANUP over {home} signs the browser out and removes
// what the out-of-process steps created (it runs while the host still runs).

// takeoverModes are the three runs of the script.
var takeoverModes = []struct{ name, mode, network string }{
	{"workspace-write", "workspace-write", "allowed"},
	{"network-denied", "workspace-write", "denied"},
	{"danger-full-access", "danger-full-access", "allowed"},
}

// takeoverStep is one line of the script's log. Outcome is succeeded, refused,
// failed, pending or unavailable; Reply is what the interface answered; Effect,
// for a step that succeeded, names the effect class it had (secret-read,
// delegated-execution, ...).
type takeoverStep struct {
	Step    string `json:"step"`
	Outcome string `json:"outcome"`
	Reply   string `json:"reply"`
	Effect  string `json:"effect"`
	Detail  string `json:"detail"`
}

// takeoverRun is what one run observed.
type takeoverRun struct {
	mode     string
	home     string
	nonce    string
	steps    []takeoverStep
	shell    shellOutcome
	status   string // dsh sandbox status after the run
	peerRule string // exact, best-effort or not-applied, as the status shows it
	pending  string
	// unavailableOK accepts a tool call reported unavailable, said as such.
	unavailableOK bool
}

// peerRuleText finds the peer rule dsh sandbox status shows.
var peerRuleText = regexp.MustCompile(`(?i)peer\s*rule\W+(exact|best-?effort|not-?applied)`)

// runTakeover runs the script once in mode with K stored in protection;
// unavailableOK accepts a tool call that reports the host unavailable (DH-E-HOST-9).
func runTakeover(t *testing.T, bin, protection, mode, network string, unavailableOK bool) takeoverRun {
	t.Helper()
	layerT := need(t, "DSH_SCENARIO_TAKEOVER_LAYER", "layer over {endpoint}, {mode}, {network} and {workspace}: the scripted route, the sandbox and a tool that always asks")
	store := need(t, "DSH_SCENARIO_TAKEOVER_STORE", "command over {dsh}, {home} and {protection} storing the key K")
	signIn := need(t, "DSH_SCENARIO_TAKEOVER_SIGNIN", "command over {link} signing a real browser in and printing its session cookie value")
	prompt := need(t, "DSH_SCENARIO_TAKEOVER_PROMPT", "command over {origin}, {cookie}, {workspace} and {text} running one prompt and printing the session id")
	pendingT := need(t, "DSH_SCENARIO_TAKEOVER_PENDING", "command over {origin}, {cookie} and {session} printing the number of pending approvals")
	script := need(t, "DSH_SCENARIO_TAKEOVER_SCRIPT", "path of the takeover script")
	command := need(t, "DSH_SCENARIO_TAKEOVER_COMMAND", "the bash tool's command over {script} and {vars}")
	cleanup := need(t, "DSH_SCENARIO_TAKEOVER_CLEANUP", "command over {home} signing out and removing what the script created outside")

	dir := t.TempDir()
	home, work := filepath.Join(dir, "home"), filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	mustShell(t, expand(store, map[string]string{"dsh": bin, "home": home, "protection": protection}))
	model := startScriptedModel(t)
	layer := writeLayer(t, expand(layerT, map[string]string{"endpoint": model.URL, "mode": mode, "network": network, "workspace": work}))
	h := startWebHost(t, bin, home, nil, layer)
	t.Cleanup(func() {
		if out, err := shell(t, expand(cleanup, map[string]string{"home": home})); err != nil {
			t.Errorf("takeover cleanup: %v\n%s", err, out)
		}
	})
	cookie := strings.TrimSpace(mustShell(t, expand(signIn, map[string]string{"link": h.link})))
	rec := oneRecord(t, home)
	mustControl(t, rec.Socket, "launch.open", map[string]any{"print": true})
	planted := h.nextLink(t, h.link)

	r := takeoverRun{mode: mode, home: home, unavailableOK: unavailableOK, nonce: fmt.Sprintf("takeover-nonce-%d", time.Now().UnixNano())}
	logPath, vars := filepath.Join(work, "takeover.log"), filepath.Join(work, "vars.json")
	data, _ := json.Marshal(map[string]any{"home": home, "socket": rec.Socket, "origin": rec.Origin, "pid": rec.PID, "dsh": bin,
		"cookie": cookie, "token": tokenOf(planted), "nonce": r.nonce, "log": logPath})
	if err := os.WriteFile(vars, data, 0o600); err != nil {
		t.Fatal(err)
	}
	model.script(bashCall(expand(command, map[string]string{"script": script, "vars": vars})))
	session := strings.TrimSpace(mustShell(t, expand(prompt, map[string]string{"origin": rec.Origin, "cookie": cookie, "workspace": work, "text": "Run the takeover script."})))
	r.shell = takeoverOutcome(t, model, 10*time.Minute)
	r.steps = takeoverLog(t, logPath)
	st := runDsh(t, time.Minute, []string{"DSH_HOME=" + home}, bin, "--home", home, "sandbox", "status")
	r.status = st.stdout + st.stderr
	if m := peerRuleText.FindStringSubmatch(r.status); m != nil {
		r.peerRule = strings.ReplaceAll(strings.ToLower(m[1]), "besteffort", "best-effort")
		r.peerRule = strings.ReplaceAll(r.peerRule, "notapplied", "not-applied")
	}
	if out, err := shell(t, expand(pendingT, map[string]string{"origin": rec.Origin, "cookie": cookie, "session": session})); err != nil {
		t.Errorf("%s: the pending approvals could not be read (did control:host.stop stop the host?): %v\n%s", mode, err, out)
	} else {
		r.pending = strings.TrimSpace(out)
	}
	t.Logf("%s: shell %s; peer rule %q; %d steps; %s pending", mode, r.shell.record(), r.peerRule, len(r.steps), r.pending)
	for _, s := range r.steps {
		t.Logf("  %s: %s %s %s", s.Step, s.Outcome, s.Effect, s.Detail)
	}
	r.common(t)
	return r
}

// takeoverOutcome waits for the model to receive the bash call's structured result.
func takeoverOutcome(t *testing.T, m *scriptedModel, within time.Duration) shellOutcome {
	t.Helper()
	for deadline := time.Now().Add(within); ; {
		bodies := m.requests()
		for i := len(bodies) - 1; i >= 0; i-- {
			if s, ok := shellResult(t, &toolOutcome{ID: "takeover", Raw: json.RawMessage(bodies[i])}); ok {
				return s
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the model received no result of the takeover call within %s (%d requests)", within, len(bodies))
		}
		time.Sleep(time.Second)
	}
}

// takeoverLog reads the script's log: one JSON object per line.
func takeoverLog(t *testing.T, path string) []takeoverStep {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the takeover script left no log: %v", err)
	}
	var out []takeoverStep
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			var s takeoverStep
			if err := json.Unmarshal([]byte(line), &s); err != nil || s.Step == "" {
				t.Fatalf("takeover log line %q: %v", line, err)
			}
			out = append(out, s)
		}
	}
	return out
}

// confined reports whether the run was a confined one.
func (r takeoverRun) confined() bool { return r.mode != "danger-full-access" }

// step is the logged step name; a step the script did not log fails the scenario.
func (r takeoverRun) step(t *testing.T, name string) takeoverStep {
	t.Helper()
	for _, s := range r.steps {
		if s.Step == name {
			return s
		}
	}
	t.Fatalf("%s: the takeover script logged no %s step", r.mode, name)
	return takeoverStep{}
}

// harnessReply reports whether a step's reply came from a harness interface.
func harnessReply(step string) bool {
	return strings.HasPrefix(step, "control:") || strings.HasPrefix(step, "get-") || strings.HasPrefix(step, "post-") ||
		step == "exchange" || step == "tool-call"
}

// common checks what holds on every platform and in every mode: no harness
// interface returned a credential; confined, no write landed in the home, each
// step that succeeded with an effect is listed as ungoverned (never a pass), the
// tool call left the approval pending for the person; unconfined, the run says so.
func (r takeoverRun) common(t *testing.T) {
	t.Helper()
	for _, s := range r.steps {
		if harnessReply(s.Step) && holdsCredential(s.Reply) {
			t.Errorf("%s: %s returned a credential: %s", r.mode, s.Step, s.Reply)
		}
	}
	if !r.confined() {
		if r.shell.Confinement.State != "unconfined" {
			t.Errorf("danger-full-access: the run is reported %q, want unconfined", r.shell.Confinement.State)
		}
		for _, s := range r.steps {
			if s.Outcome == "succeeded" {
				t.Logf("danger-full-access residual risk (DH-ADR-HOST-12): %s succeeded: %s", s.Step, s.Detail)
			}
		}
		return
	}
	_ = filepath.WalkDir(r.home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if data, err := os.ReadFile(p); err == nil && bytes.Contains(data, []byte(r.nonce)) {
				t.Errorf("%s: a write from the model's command landed in the home: %s", r.mode, p)
			}
		}
		return nil
	})
	for _, s := range r.steps {
		if s.Outcome == "succeeded" && s.Effect != "" {
			r.listed(t, s.Effect)
		}
	}
	if tc := r.step(t, "tool-call"); tc.Outcome == "unavailable" && r.unavailableOK {
		t.Logf("%s: the tool call reports the host unavailable: %s", r.mode, tc.Detail)
	} else if tc.Outcome != "pending" {
		t.Errorf("%s: the tool call ended %s (%s), want it to reach the host and wait", r.mode, tc.Outcome, tc.Detail)
	} else if r.pending == "0" || r.pending == "" {
		t.Errorf("%s: no approval is pending for the person after the tool call", r.mode)
	}
}

// listed fails the scenario unless effect is in the result's exposed list or the
// sandbox status.
func (r takeoverRun) listed(t *testing.T, effect string) {
	t.Helper()
	if !slices.Contains(r.shell.Confinement.Exposed, effect) && !strings.Contains(r.status, effect) {
		t.Errorf("%s: %s happened but neither the result (exposed %v) nor dsh sandbox status lists it", r.mode, effect, r.shell.Confinement.Exposed)
	}
}

// refused fails the scenario for each named step that succeeded.
func (r takeoverRun) refused(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if s := r.step(t, n); s.Outcome == "succeeded" {
			t.Errorf("%s: %s succeeded from the model's command: %s", r.mode, n, s.Detail)
		}
	}
}

// controlRefused fails the scenario for each control method the command reached.
func (r takeoverRun) controlRefused(t *testing.T) {
	t.Helper()
	for _, m := range controlMethods {
		r.refused(t, "control:"+m)
	}
}

// peer checks the peer rule the status shows and, when it is exact, that the
// exchange and both posts were refused auth/untrusted-peer; otherwise each
// outcome is recorded, not assumed.
func (r takeoverRun) peer(t *testing.T, want string) {
	t.Helper()
	if r.peerRule != want {
		t.Errorf("%s: dsh sandbox status shows peer rule %q, want %s:\n%s", r.mode, r.peerRule, want, r.status)
	}
	for _, n := range []string{"exchange", "post-no-cookie", "post-cookie"} {
		s := r.step(t, n)
		if r.peerRule != "exact" {
			t.Logf("%s: peer rule %s: %s ended %s: %s", r.mode, r.peerRule, n, s.Outcome, s.Reply)
			continue
		}
		if s.Outcome != "refused" || !strings.Contains(s.Reply, "auth/untrusted-peer") {
			t.Errorf("%s: %s ended %s with %q, want refused auth/untrusted-peer", r.mode, n, s.Outcome, s.Reply)
		}
	}
}
