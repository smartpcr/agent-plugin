//go:build scenario

package scenarios

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
// delegated-execution, ...). A refused step carries the refusal in Reply or
// Detail (a target the sandbox hides, such as the host's pid outside the pid
// namespace, is logged refused with what was not found); a failed step carries
// the OS error in Detail, which for a denial is the platform's denial text.
type takeoverStep struct {
	Step    string `json:"step"`
	Outcome string `json:"outcome"`
	Reply   string `json:"reply"`
	Effect  string `json:"effect"`
	Detail  string `json:"detail"`
}

// takeoverOutcomes are the outcomes a step may log.
var takeoverOutcomes = []string{"succeeded", "refused", "failed", "pending", "unavailable"}

// takeoverCommonSteps are the steps the script logs on every platform and in
// every mode; control:host.stop is the last line of a complete log.
func takeoverCommonSteps() []string {
	s := []string{"read-secrets", "read-sessions"}
	for _, m := range controlMethods {
		s = append(s, "control:"+m)
	}
	return append(s, "get-no-cookie", "post-no-cookie", "get-cookie", "post-cookie", "exchange", "write-session", "write-layer", "tool-call")
}

// takeoverEffects is the effect class of each step that is not a harness
// interface: what a confined run must list as ungoverned when the step succeeds.
// A step without a class here (proc-mem, debugger, ...) must name its own.
var takeoverEffects = map[string]string{
	"read-secrets": "secret-read", "keychain-read": "secret-read", "credential-manager": "secret-read",
	"read-sessions": "harness-home", "list-secrets": "harness-home", "list-run": "harness-home",
	"write-session": "harness-home", "write-layer": "harness-home",
	"service-manager": "delegated-execution", "launchd": "delegated-execution", "scheduled-task": "delegated-execution",
	"com-server": "delegated-execution", "apple-event": "delegated-execution",
}

// takeoverPlatform is what one scenario adds to the shared run.
type takeoverPlatform struct {
	// steps are the platform's steps beyond takeoverCommonSteps.
	steps []string
	// backends are the backends a confined run may report; empty, any but none.
	backends []string
	// unavailableOK accepts a tool call that reports the host unavailable (DH-E-HOST-9).
	unavailableOK bool
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
	platform takeoverPlatform
}

// peerRuleText finds the peer rule dsh sandbox status shows.
var peerRuleText = regexp.MustCompile(`(?i)peer\s*rule\W+(exact|best-?effort|not-?applied)`)

// runTakeover runs the script once in mode with K stored in protection.
func runTakeover(t *testing.T, bin, protection, mode, network string, p takeoverPlatform) takeoverRun {
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
	cleanupShell(t, "takeover cleanup", expand(cleanup, map[string]string{"home": home}), takeoverCleanupWithin)
	cookie := strings.TrimSpace(mustShell(t, expand(signIn, map[string]string{"link": h.link})))
	rec := oneRecord(t, home)
	mustControl(t, rec.Socket, "launch.open", map[string]any{"print": true})
	planted := h.nextLink(t, h.link)

	r := takeoverRun{mode: mode, home: home, platform: p, nonce: fmt.Sprintf("takeover-nonce-%d", time.Now().UnixNano())}
	logPath, vars := filepath.Join(work, "takeover.log"), filepath.Join(work, "vars.json")
	data, _ := json.Marshal(map[string]any{"home": home, "socket": rec.Socket, "origin": rec.Origin, "pid": rec.PID, "dsh": bin,
		"cookie": cookie, "token": tokenOf(planted), "nonce": r.nonce, "log": logPath})
	if err := os.WriteFile(vars, data, 0o600); err != nil {
		t.Fatal(err)
	}
	model.script(bashCall(expand(command, map[string]string{"script": script, "vars": vars})))
	session := strings.TrimSpace(mustShell(t, expand(prompt, map[string]string{"origin": rec.Origin, "cookie": cookie, "workspace": work, "text": "Run the takeover script."})))
	r.shell = takeoverOutcome(t, model, 10*time.Minute)
	r.steps = takeoverLog(t, logPath, p.steps)
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
	t.Logf("%s: shell %s; peer rule %q; %d steps; %q pending", mode, r.shell.record(), r.peerRule, len(r.steps), r.pending)
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

// takeoverLog reads the script's log; an incomplete or malformed log fails the scenario.
func takeoverLog(t *testing.T, path string, platform []string) []takeoverStep {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("the takeover script left no log: %v", err)
	}
	defer f.Close()
	steps, err := parseTakeoverLog(f, platform)
	if err != nil {
		t.Fatalf("takeover log %s: %v", path, err)
	}
	return steps
}

// parseTakeoverLog reads one JSON object per line and checks that the log is
// complete: every common and platform step logged exactly once with a known
// outcome, and control:host.stop last, so an omitted, repeated or truncated
// attempt cannot pass.
func parseTakeoverLog(r io.Reader, platform []string) ([]takeoverStep, error) {
	var out []takeoverStep
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var s takeoverStep
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return nil, fmt.Errorf("line %d %q: %w", n, line, err)
		}
		switch {
		case s.Step == "":
			return nil, fmt.Errorf("line %d %q names no step", n, line)
		case !slices.Contains(takeoverOutcomes, s.Outcome):
			return nil, fmt.Errorf("line %d: %s ended %q, want one of %v", n, s.Step, s.Outcome, takeoverOutcomes)
		case seen[s.Step]:
			return nil, fmt.Errorf("line %d: %s is logged twice", n, s.Step)
		}
		seen[s.Step] = true
		out = append(out, s)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading the log: %w", err)
	}
	var missing []string
	for _, name := range append(takeoverCommonSteps(), platform...) {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the script logged no %s step", strings.Join(missing, ", "))
	}
	if last := out[len(out)-1].Step; last != "control:host.stop" {
		return nil, fmt.Errorf("the last step is %s, want control:host.stop", last)
	}
	return out, nil
}

// confined reports whether the run was asked to be confined; common has checked
// by then that the product reported the confinement asked for.
func (r takeoverRun) confined() bool { return r.mode != "danger-full-access" }

// confinementError checks the confinement the shell result reports against the
// run's mode: a confined mode needs a full or partial state (partial naming what
// is missing) on a real backend, one of backends when given; danger-full-access
// needs unconfined.
func confinementError(mode string, s shellOutcome, backends []string) error {
	c := s.Confinement
	if mode == "danger-full-access" {
		if c.State != "unconfined" {
			return fmt.Errorf("the run is reported %q, want unconfined", c.State)
		}
		return nil
	}
	switch {
	case c.State != "full" && c.State != "partial":
		return fmt.Errorf("the run is reported %q on backend %q, want full or partial confinement", c.State, c.Backend)
	case c.State == "partial" && len(c.Missing) == 0:
		return fmt.Errorf("the run is reported partial with nothing missing")
	case c.Backend == "" || c.Backend == "none":
		return fmt.Errorf("the run is reported %s on backend %q, want a confining backend", c.State, c.Backend)
	case len(backends) > 0 && !slices.Contains(backends, c.Backend):
		return fmt.Errorf("the run is confined by %q, want one of %v", c.Backend, backends)
	}
	return nil
}

// stepEffect is the effect class of a step that succeeded and is not a harness
// interface: the class takeoverEffects derives, which a logged Effect must
// agree with, or the logged Effect of a step without a derived class.
func stepEffect(s takeoverStep) (string, error) {
	derived, ok := takeoverEffects[s.Step]
	switch {
	case ok && s.Effect != "" && s.Effect != derived:
		return "", fmt.Errorf("%s succeeded logged as %s, but it is %s", s.Step, s.Effect, derived)
	case ok:
		return derived, nil
	case s.Effect != "":
		return s.Effect, nil
	}
	return "", fmt.Errorf("%s succeeded and names no effect class", s.Step)
}

// homeHolds returns the files of fsys that hold nonce in their name or content.
// Every entry is inspected; one that cannot be read is an error, never a pass.
// Entries that are not regular files (the control socket, links) hold no content.
func homeHolds(fsys fs.FS, nonce string) ([]string, error) {
	var found []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), nonce) {
			found = append(found, p)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(nonce)) {
			found = append(found, p)
		}
		return nil
	})
	return found, err
}

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

// common checks what holds on every platform and in every mode: the product
// reported the confinement the mode asks for; no harness interface returned a
// credential; confined, no write landed in the home and both home writes were
// refused, each step that succeeded is listed as ungoverned under its effect
// class (never a pass), the tool call left the approval pending for the person;
// unconfined, whatever succeeded is logged as residual risk.
func (r takeoverRun) common(t *testing.T) {
	t.Helper()
	if err := confinementError(r.mode, r.shell, r.platform.backends); err != nil {
		t.Fatalf("%s: %v (shell %s)", r.mode, err, r.shell.record())
	}
	for _, s := range r.steps {
		if harnessReply(s.Step) && holdsCredential(s.Reply) {
			t.Errorf("%s: %s returned a credential: %s", r.mode, s.Step, s.Reply)
		}
	}
	if !r.confined() {
		for _, s := range r.steps {
			if s.Outcome == "succeeded" {
				t.Logf("danger-full-access residual risk (DH-ADR-HOST-12): %s succeeded: %s", s.Step, s.Detail)
			}
		}
		return
	}
	found, err := homeHolds(os.DirFS(r.home), r.nonce)
	if err != nil {
		t.Errorf("%s: the home could not be inspected for the model's writes: %v", r.mode, err)
	}
	for _, p := range found {
		t.Errorf("%s: a write from the model's command landed in the home: %s", r.mode, filepath.Join(r.home, filepath.FromSlash(p)))
	}
	r.refused(t, "write-session", "write-layer")
	for _, s := range r.steps {
		if s.Outcome != "succeeded" || harnessReply(s.Step) {
			continue
		}
		if effect, err := stepEffect(s); err != nil {
			t.Errorf("%s: %v", r.mode, err)
		} else {
			r.listed(t, effect)
		}
	}
	if tc := r.step(t, "tool-call"); tc.Outcome == "unavailable" && r.platform.unavailableOK {
		t.Logf("%s: the tool call reports the host unavailable: %s", r.mode, tc.Detail)
	} else if tc.Outcome != "pending" {
		t.Errorf("%s: the tool call ended %s (%s), want it to reach the host and wait", r.mode, tc.Outcome, tc.Detail)
	} else if n, err := pendingCount(r.pending); err != nil {
		t.Errorf("%s: %v", r.mode, err)
	} else if n == 0 {
		t.Errorf("%s: no approval is pending for the person after the tool call", r.mode)
	}
}

// pendingCount parses what DSH_SCENARIO_TAKEOVER_PENDING printed: a decimal
// count of zero or more, digits only; anything else is an error, never a count.
func pendingCount(out string) (int, error) {
	s := strings.TrimSpace(out)
	if !pendingDigits.MatchString(s) {
		return 0, fmt.Errorf("the pending approvals read %q, want a count", out)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("the pending approvals read %q: %w", out, err)
	}
	return n, nil
}

var pendingDigits = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// denialMark is how a failed step's Detail shows that the OS denied the attempt
// (rather than that the script broke): the error text of the platform's denial.
var denialMark = regexp.MustCompile(`(?i)permission denied|operation not permitted|access is denied|access denied|not permitted|read-only file system|\beacces\b|\beperm\b|\berofs\b|refused|denied`)

// denialError checks that s is evidence of a denial: refused with the
// interface's answer or a detail, or failed with the OS's denial in Detail. A
// step that succeeded, is still pending, reports unavailable, or failed for
// another reason is not a refusal.
func denialError(s takeoverStep) error {
	switch s.Outcome {
	case "refused":
		if strings.TrimSpace(s.Reply) == "" && strings.TrimSpace(s.Detail) == "" {
			return fmt.Errorf("%s is logged refused with no reply or detail to show it", s.Step)
		}
		return nil
	case "failed":
		if !denialMark.MatchString(s.Detail) {
			return fmt.Errorf("%s failed with %q, which shows no denial", s.Step, s.Detail)
		}
		return nil
	}
	return fmt.Errorf("%s ended %s (%s), want it refused or denied", s.Step, s.Outcome, s.Detail)
}

// takeoverCleanupWithin bounds the cleanup command.
const takeoverCleanupWithin = 2 * time.Minute

// cleanupShell registers line to run at cleanup on a context of its own,
// bounded by within: T.Context is canceled before cleanup callbacks run, so a
// command bound to it would never start.
func cleanupShell(t *testing.T, what, line string, within time.Duration) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), within)
		defer cancel()
		if out, err := shellContext(ctx, line); err != nil {
			t.Errorf("%s: %v\n%s", what, err, out)
		}
	})
}

// listed fails the scenario unless effect is in the result's exposed list or the
// sandbox status.
func (r takeoverRun) listed(t *testing.T, effect string) {
	t.Helper()
	if !slices.Contains(r.shell.Confinement.Exposed, effect) && !strings.Contains(r.status, effect) {
		t.Errorf("%s: %s happened but neither the result (exposed %v) nor dsh sandbox status lists it", r.mode, effect, r.shell.Confinement.Exposed)
	}
}

// refused fails the scenario for each named step that is not evidence of a
// denial (denialError): succeeded, pending, unavailable or failed otherwise.
func (r takeoverRun) refused(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := denialError(r.step(t, n)); err != nil {
			t.Errorf("%s: %v", r.mode, err)
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
