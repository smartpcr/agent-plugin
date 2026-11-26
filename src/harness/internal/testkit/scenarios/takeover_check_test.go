//go:build scenario

package scenarios

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// completeTakeoverLog is a log with every common step plus platform, host.stop last.
func completeTakeoverLog(platform ...string) []string {
	var lines []string
	for _, s := range append(takeoverCommonSteps(), platform...) {
		if s != "control:host.stop" {
			lines = append(lines, fmt.Sprintf(`{"step":%q,"outcome":"refused"}`, s))
		}
	}
	return append(lines, `{"step":"control:host.stop","outcome":"refused"}`)
}

func TestTakeoverLogMustBeComplete(t *testing.T) {
	full := completeTakeoverLog("proc-mem")
	if steps, err := parseTakeoverLog(strings.NewReader(strings.Join(full, "\n")), []string{"proc-mem"}); err != nil || len(steps) != len(full) {
		t.Fatalf("complete log: %d steps, %v", len(steps), err)
	}
	without := func(step string) string {
		var keep []string
		for _, l := range full {
			if !strings.Contains(l, `"`+step+`"`) {
				keep = append(keep, l)
			}
		}
		return strings.Join(keep, "\n")
	}
	stopLast := func(lines ...string) string {
		return strings.Join(append(append([]string{}, full[:len(full)-1]...), append(lines, full[len(full)-1])...), "\n")
	}
	for name, c := range map[string]struct{ log, want string }{
		"empty":              {"", "logged no"},
		"common step":        {without("write-layer"), "logged no write-layer"},
		"platform step":      {without("proc-mem"), "logged no proc-mem"},
		"control attempt":    {without("control:launch.open"), "logged no control:launch.open"},
		"duplicate":          {stopLast(`{"step":"exchange","outcome":"succeeded"}`), "exchange is logged twice"},
		"invalid outcome":    {stopLast(`{"step":"extra","outcome":"maybe"}`), `ended "maybe"`},
		"no outcome":         {stopLast(`{"step":"extra"}`), `ended ""`},
		"no step":            {stopLast(`{"outcome":"refused"}`), "names no step"},
		"truncated":          {strings.Join(full, "\n") + "\n" + `{"step":"ext`, "line"},
		"host.stop not last": {strings.Join(append(append([]string{}, full...), `{"step":"extra","outcome":"failed"}`), "\n"), "want control:host.stop"},
		"line too long":      {stopLast(`{"step":"extra","outcome":"failed","detail":"` + strings.Repeat("x", 5<<20) + `"}`), "reading the log"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTakeoverLog(strings.NewReader(c.log), []string{"proc-mem"}); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("parseTakeoverLog = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestTakeoverConfinementIsWhatTheProductReports(t *testing.T) {
	out := func(state, backend string, missing ...string) shellOutcome {
		var s shellOutcome
		s.Confinement.State, s.Confinement.Backend, s.Confinement.Missing = state, backend, missing
		return s
	}
	readOnly := out("full", "bwrap")
	readOnly.Confinement.Mode = "read-only"
	for name, c := range map[string]struct {
		mode     string
		s        shellOutcome
		backends []string
		ok       bool
	}{
		"full":                    {"workspace-write", out("full", "bwrap"), []string{"bwrap"}, true},
		"partial naming missing":  {"workspace-write", out("partial", "landlock", "network"), nil, true},
		"unconfined when asked":   {"workspace-write", out("unconfined", "none"), nil, false},
		"no state":                {"workspace-write", out("", ""), nil, false},
		"partial missing nothing": {"workspace-write", out("partial", "landlock"), nil, false},
		"no backend":              {"workspace-write", out("full", "none"), nil, false},
		"other backend":           {"workspace-write", out("full", "landlock"), []string{"bwrap"}, false},
		"read-only for a write":   {"workspace-write", readOnly, []string{"bwrap"}, false},
		"read-only for danger":    {"danger-full-access", readOnly, nil, false},
		"danger unconfined":       {"danger-full-access", out("unconfined", "none"), nil, true},
		"danger confined":         {"danger-full-access", out("full", "bwrap"), nil, false},
	} {
		if c.s.Confinement.Mode == "" {
			c.s.Confinement.Mode = c.mode
		}
		if err := confinementError(c.mode, c.s, c.backends); (err == nil) != c.ok {
			t.Errorf("%s: confinementError = %v, want ok %v", name, err, c.ok)
		}
	}
	unreported := out("full", "bwrap")
	if err := confinementError("workspace-write", unreported, nil); err == nil {
		t.Error("confinementError passed a result that reports no mode")
	}
}

func TestTakeoverEffectOfASucceededStep(t *testing.T) {
	for _, c := range []struct {
		step takeoverStep
		want string
	}{
		{takeoverStep{Step: "keychain-read"}, "secret-read"},
		{takeoverStep{Step: "credential-manager", Effect: "secret-read"}, "secret-read"},
		{takeoverStep{Step: "launchd"}, "delegated-execution"},
		{takeoverStep{Step: "scheduled-task"}, "delegated-execution"},
		{takeoverStep{Step: "com-server"}, "delegated-execution"},
		{takeoverStep{Step: "proc-mem", Effect: "process-memory"}, "process-memory"},
	} {
		if got, err := stepEffect(c.step); err != nil || got != c.want {
			t.Errorf("stepEffect(%+v) = %q, %v, want %q", c.step, got, err, c.want)
		}
	}
	for _, s := range []takeoverStep{{Step: "debugger"}, {Step: "launchd", Effect: "secret-read"}} {
		if got, err := stepEffect(s); err == nil {
			t.Errorf("stepEffect(%+v) = %q, want an error", s, got)
		}
	}
}

// failingFS fails reading the file named bad.
type failingFS struct {
	fs.FS
	bad string
}

func (f failingFS) Open(name string) (fs.File, error) {
	if name == f.bad {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.FS.Open(name)
}

func TestTakeoverHomeInspectionFailsWhenIncomplete(t *testing.T) {
	home := fstest.MapFS{
		"sessions/a.json":       {Data: []byte("clean")},
		"secrets/key":           {Data: []byte("written nonce-1 here")},
		"layers/nonce-1.yml":    {Data: []byte("x")},
		"run/control.sock":      {Mode: fs.ModeSocket},
		"profiles/default.yaml": {Data: []byte("clean")},
	}
	found, err := homeHolds(home, "nonce-1")
	if err != nil || strings.Join(found, ",") != "layers/nonce-1.yml,secrets/key" {
		t.Fatalf("homeHolds = %v, %v, want both writes", found, err)
	}
	for _, bad := range []string{"secrets/key", "sessions"} {
		if _, err := homeHolds(failingFS{home, bad}, "nonce-1"); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("homeHolds with %s unreadable = %v, want the permission error", bad, err)
		}
	}
	if _, err := homeHolds(failingFS{home, "."}, "nonce-1"); err == nil {
		t.Error("homeHolds with an unreadable home passed")
	}
}

func TestTakeoverRefusalNeedsDenialEvidence(t *testing.T) {
	for _, s := range []takeoverStep{
		{Step: "control:host.status", Outcome: "refused", Reply: "connection refused"},
		{Step: "proc-mem", Outcome: "refused", Detail: "pid 42 not found in the sandbox"},
		{Step: "read-secrets", Outcome: "failed", Detail: "open /home/k: Permission denied"},
		{Step: "write-layer", Outcome: "failed", Detail: "Read-only file system"},
		{Step: "open-process", Outcome: "failed", Detail: "Access is denied."},
	} {
		if err := denialError(s); err != nil {
			t.Errorf("denialError(%+v) = %v, want a denial", s, err)
		}
	}
	for _, s := range []takeoverStep{
		{Step: "proc-mem", Outcome: "succeeded"},
		{Step: "proc-mem", Outcome: "pending", Detail: "still trying"},
		{Step: "read-secrets", Outcome: "unavailable", Detail: "no such tool"},
		{Step: "control:launch.open", Outcome: "refused"},
		{Step: "debugger", Outcome: "failed", Detail: "lldb: command not found"},
		{Step: "debugger", Outcome: "failed"},
	} {
		if err := denialError(s); err == nil {
			t.Errorf("denialError(%+v) passed, want an error", s)
		}
	}
}

func TestTakeoverPendingCountIsANumber(t *testing.T) {
	for in, want := range map[string]int{"0": 0, "1": 1, " 12\n": 12} {
		if n, err := pendingCount(in); err != nil || n != want {
			t.Errorf("pendingCount(%q) = %d, %v, want %d", in, n, err, want)
		}
	}
	for _, in := range []string{"", "00", "-1", "+1", "error", "1 approval", "1.0"} {
		if n, err := pendingCount(in); err == nil {
			t.Errorf("pendingCount(%q) = %d, want an error", in, n)
		}
	}
}

// TestTakeoverCleanupRunsAfterTheTestContextEnds registers the cleanup in a
// subtest and checks it ran once the subtest's context was canceled.
func TestTakeoverCleanupRunsAfterTheTestContextEnds(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "ran")
	t.Setenv("DSH_TEST_CLEANUP_MARK", mark)
	line := `echo ran > "$DSH_TEST_CLEANUP_MARK"`
	if runtime.GOOS == "windows" {
		line = `echo ran>%DSH_TEST_CLEANUP_MARK%`
	}
	var ctx context.Context
	t.Run("run", func(t *testing.T) {
		ctx = t.Context()
		cleanupShell(t, "cleanup", line, time.Minute)
	})
	if ctx.Err() == nil {
		t.Fatal("the subtest's context is still live")
	}
	if _, err := os.Stat(mark); err != nil {
		t.Errorf("the cleanup command did not run after the test context ended: %v", err)
	}
}

func TestTakeoverEmptyListingNeedsACompletedListingOrADenial(t *testing.T) {
	for _, s := range []takeoverStep{
		{Step: "list-secrets", Outcome: "succeeded"},
		{Step: "list-run", Outcome: "succeeded", Reply: " \n"},
		{Step: "list-secrets", Outcome: "failed", Detail: "ls: cannot open directory: Permission denied"},
		{Step: "list-run", Outcome: "refused", Detail: "no such directory in the sandbox"},
	} {
		if err := emptyListingError(s); err != nil {
			t.Errorf("emptyListingError(%+v) = %v, want ok", s, err)
		}
	}
	for _, s := range []takeoverStep{
		{Step: "list-secrets", Outcome: "succeeded", Reply: "key"},
		{Step: "list-secrets", Outcome: "failed", Detail: "ls: command not found"},
		{Step: "list-run", Outcome: "failed"},
		{Step: "list-run", Outcome: "pending"},
		{Step: "list-run", Outcome: "unavailable", Detail: "no shell"},
	} {
		if err := emptyListingError(s); err == nil {
			t.Errorf("emptyListingError(%+v) passed, want an error", s)
		}
	}
}

func TestTakeoverObservedOutcomeIsReported(t *testing.T) {
	const status = "backend seatbelt\nungoverned: cookie-replay, delegated-execution\nsecret-read: refused"
	exposed := []string{"cookie-replay", "delegated-execution"}
	for _, s := range []takeoverStep{
		{Step: "get-cookie", Outcome: "succeeded"},
		{Step: "post-cookie", Outcome: "refused", Reply: "auth/untrusted-peer"},
		{Step: "keychain-read", Outcome: "failed", Detail: "denied"},
		{Step: "launchd", Outcome: "succeeded"},
	} {
		if err := observedError(s, status, exposed); err != nil {
			t.Errorf("observedError(%+v) = %v, want ok", s, err)
		}
	}
	for name, c := range map[string]struct {
		s       takeoverStep
		status  string
		exposed []string
		want    string
	}{
		"replay admitted, not exposed": {takeoverStep{Step: "get-cookie", Outcome: "succeeded"}, status, []string{"delegated-execution"}, "does not list cookie-replay"},
		"replay not in status":         {takeoverStep{Step: "post-cookie", Outcome: "succeeded"}, "backend seatbelt", exposed, "does not show cookie-replay"},
		"refused, not in status":       {takeoverStep{Step: "apple-event", Outcome: "refused", Detail: "denied"}, "backend seatbelt", nil, "does not show delegated-execution"},
		"pending":                      {takeoverStep{Step: "launchd", Outcome: "pending"}, status, exposed, "observed to an end"},
		"unavailable":                  {takeoverStep{Step: "get-cookie", Outcome: "unavailable"}, status, exposed, "observed to an end"},
		"no class":                     {takeoverStep{Step: "debugger", Outcome: "succeeded"}, status, exposed, "no effect class"},
	} {
		if err := observedError(c.s, c.status, c.exposed); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: observedError = %v, want an error containing %q", name, err, c.want)
		}
	}
}

// TestShellTreeCleanupStopsDescendants starts a shell whose child keeps a
// grandchild holding the output, lets the subtest end, and checks the cleanup
// returned within its bound with every process of the tree gone.
func TestShellTreeCleanupStopsDescendants(t *testing.T) {
	line := echoTreeLine(t)
	var shellPID, grandchild int
	var ended time.Time
	t.Run("run", func(t *testing.T) {
		h := startShellTree(t, line)
		shellPID = h.Process.Pid()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for deadline := time.Now().Add(30 * time.Second); grandchild == 0; <-tick.C {
			if n, err := strconv.Atoi(strings.TrimSpace(h.Stdout.String())); err == nil {
				grandchild = n
			} else if time.Now().After(deadline) {
				t.Fatalf("no grandchild pid within 30s: %q %q", h.Stdout.String(), h.Stderr.String())
			}
		}
		ended = time.Now()
	})
	if took := time.Since(ended); took > 3*launcher.Grace+time.Second {
		t.Errorf("the cleanup took %s to stop the tree", took)
	}
	for _, pid := range []int{shellPID, grandchild} {
		if !subprocesstest.Exited(pid) {
			t.Errorf("pid %d survives the cleanup", pid)
		}
	}
}

// echoTreeLine is a shell line running the launcher's echo program in tree
// mode: it prints a grandchild's pid, and the grandchild holds the output open.
func echoTreeLine(t *testing.T) string {
	t.Helper()
	echo := launcher.Build(t, filepath.Join(repositoryRoot(t), "src", "harness", "internal", "testkit", "launcher"), "./testdata/echo")
	t.Setenv("DSH_TEST_ECHO", echo)
	if runtime.GOOS == "windows" {
		return `%DSH_TEST_ECHO% tree`
	}
	return `"$DSH_TEST_ECHO" tree`
}

// TestTakeoverCleanupIsBoundedWhenADescendantHoldsTheOutput runs the cleanup
// command through shellTree with a grandchild holding the output: it returns an
// error within its bound, and the whole tree is gone.
func TestTakeoverCleanupIsBoundedWhenADescendantHoldsTheOutput(t *testing.T) {
	line := echoTreeLine(t)
	const within = 10 * time.Second
	start := time.Now()
	h, err := shellTree(t, line, within)
	if took := time.Since(start); took > within+3*launcher.Grace+time.Second {
		t.Errorf("the cleanup command returned after %s", took)
	}
	if !errors.Is(err, launcher.ErrTimeout) {
		t.Errorf("shellTree = %v, want ErrTimeout", err)
	}
	grandchild, perr := strconv.Atoi(strings.TrimSpace(h.Stdout.String()))
	if perr != nil || grandchild == 0 {
		t.Fatalf("no grandchild pid in %q", h.Stdout.String())
	}
	for _, pid := range []int{h.Process.Pid(), grandchild} {
		if !subprocesstest.Exited(pid) {
			t.Errorf("pid %d survives the bounded cleanup", pid)
		}
	}
}

func TestTakeoverObservationMustEnd(t *testing.T) {
	exposed := []string{"secret-read"}
	for _, s := range []takeoverStep{
		{Step: "credential-manager", Outcome: "succeeded"},
		{Step: "scheduled-task", Outcome: "refused", Detail: "Access is denied."},
		{Step: "com-server", Outcome: "failed", Detail: "class not registered"},
	} {
		if err := observationError(s, exposed); err != nil {
			t.Errorf("observationError(%+v) = %v, want ok", s, err)
		}
	}
	for _, s := range []takeoverStep{
		{Step: "credential-manager", Outcome: "pending"},
		{Step: "scheduled-task", Outcome: "unavailable", Detail: "schtasks missing"},
		{Step: "com-server", Outcome: "pending", Detail: "waiting"},
		{Step: "scheduled-task", Outcome: "succeeded"},
	} {
		if err := observationError(s, exposed); err == nil {
			t.Errorf("observationError(%+v) passed, want an error", s)
		}
	}
}

func TestCredentialLeakFoundWithoutMarkers(t *testing.T) {
	const cookie, token = "c0ffee-session-value-123", "launch-token-abcdef0123"
	for _, s := range []string{
		`{"token":"anything"}`,
		`{"result":{"session":{"Cookie":"x"}}}`,
		`[{"credential":"k"}]`,
		cookie,
		`ok ` + token + ` done`,
		`{"value":"` + cookie + `"}`,
		`https://h/?token=abc`,
		`dsh-session-1=v`,
	} {
		if !holdsCredential(s, cookie, token) {
			t.Errorf("holdsCredential(%q) = false, want the leak found", s)
		}
	}
	for _, s := range []string{
		`{"pid":42,"origin":"http://127.0.0.1:1","socket":"/run/c.sock"}`,
		`{"token":""}`,
		`refused auth/untrusted-peer`,
		`{"state":"running","approvals":1}`,
	} {
		if holdsCredential(s, cookie, token, "short") {
			t.Errorf("holdsCredential(%q) = true, want no leak", s)
		}
	}
}

func TestPrefixWalkCoversEveryKind(t *testing.T) {
	const p = "https://proxy.example/dsh"
	ok := func(kind, method string, status int) browserResponse {
		return browserResponse{URL: p + "/x", Status: status, Kind: kind, Method: method}
	}
	full := []browserResponse{ok("document", "GET", 200), ok("asset", "GET", 200), ok("form", "POST", 303),
		{URL: p + "/events", Status: 200, ContentType: "text/event-stream", Method: "GET"}}
	if err := prefixCoverageError(full, p); err != nil {
		t.Fatalf("complete walk: %v", err)
	}
	for name, c := range map[string]struct {
		rs   []browserResponse
		want string
	}{
		"stream only":   {full[3:], "no document"},
		"no form":       {[]browserResponse{full[0], full[1], full[3]}, "no form"},
		"no asset":      {[]browserResponse{full[0], full[2], full[3]}, "no asset"},
		"form as GET":   {append(append([]browserResponse{}, full...), ok("form", "GET", 200)), "want a POST"},
		"failed asset":  {append(append([]browserResponse{}, full...), ok("asset", "GET", 404)), "answered 404"},
		"left prefix":   {append(append([]browserResponse{}, full...), browserResponse{URL: "https://proxy.example/x", Status: 200, Kind: "asset"}), "left the prefix"},
		"asset via 302": {[]browserResponse{full[0], ok("asset", "GET", 302), full[2], full[3]}, "no asset"},
	} {
		if err := prefixCoverageError(c.rs, p); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: prefixCoverageError = %v, want an error containing %q", name, err, c.want)
		}
	}
}
