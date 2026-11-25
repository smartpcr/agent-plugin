package launch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

func absProgram(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

// TestPlanArgsRoundTrip: what the host puts on the stage's command line is the
// plan the stage reads, including a command whose own arguments look like flags.
func TestPlanArgsRoundTrip(t *testing.T) {
	exe := absProgram(t)
	canary := filepath.Join(t.TempDir(), "canary")
	for _, p := range []Plan{
		{Kind: Unconfined, Backend: sandbox.BackendNone, Control: 3, Status: 4, Argv: []string{exe}},
		{Kind: Confined, Backend: sandbox.BackendBwrap, Canary: canary, Control: 7, Status: 9, Argv: []string{exe, "--kind", "x", "--", "-v"}},
		{Kind: Unconfined, Backend: sandbox.BackendNone, Control: 5, Status: 6, Argv: []string{exe}, Keep: []uintptr{3, 4}},
	} {
		got, err := Parse(p.Args())
		if err != nil || !reflect.DeepEqual(got, p) {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", p.Args(), got, err, p)
		}
	}
}

// TestPlanRefusesDisagreement: a plan whose kind, backend and canary disagree,
// or that has no channel or no absolute command, never reaches ready.
func TestPlanRefusesDisagreement(t *testing.T) {
	exe := absProgram(t)
	canary := filepath.Join(t.TempDir(), "canary")
	ok := Plan{Kind: Confined, Backend: sandbox.BackendBwrap, Canary: canary, Control: 3, Status: 4, Argv: []string{exe}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("Validate(%+v) = %v", ok, err)
	}
	bad := map[string]func(*Plan){
		"no kind":             func(p *Plan) { p.Kind = "" },
		"confined under none": func(p *Plan) { p.Backend = sandbox.BackendNone },
		"unknown backend":     func(p *Plan) { p.Backend = "chroot" },
		"confined, no canary": func(p *Plan) { p.Canary = "" },
		"relative canary":     func(p *Plan) { p.Canary = "canary" },
		"unclean canary":      func(p *Plan) { p.Canary = canary + string(filepath.Separator) + ".." },
		"unconfined bwrap":    func(p *Plan) { p.Kind, p.Canary = Unconfined, "" },
		"unconfined canary":   func(p *Plan) { p.Kind, p.Backend = Unconfined, sandbox.BackendNone },
		"no control":          func(p *Plan) { p.Control = 0 },
		"same descriptors":    func(p *Plan) { p.Status = p.Control },
		"kept stage channel":  func(p *Plan) { p.Keep = []uintptr{p.Control} },
		"kept twice":          func(p *Plan) { p.Keep = []uintptr{5, 5} },
		"no command":          func(p *Plan) { p.Argv = nil },
		"relative command":    func(p *Plan) { p.Argv = []string{"sh"} },
	}
	for name, edit := range bad {
		p := ok
		edit(&p)
		if err := p.Validate(); !herr.Has(err, sandbox.CodeLaunchFailed) {
			t.Errorf("%s: Validate = %v, want %s", name, err, sandbox.CodeLaunchFailed)
		}
		if _, err := Parse(p.Args()); !herr.Has(err, sandbox.CodeLaunchFailed) {
			t.Errorf("%s: Parse = %v, want %s", name, err, sandbox.CodeLaunchFailed)
		}
	}
}

// TestParseNeedsTheSeparator: the command follows "--", so no command argument
// is ever read as a stage flag.
func TestParseNeedsTheSeparator(t *testing.T) {
	exe := absProgram(t)
	for _, args := range [][]string{
		nil,
		{"--kind", "unconfined", "--backend", "none", "--control-fd", "3", "--status-fd", "4", exe},
		{"--kind", "unconfined", "--backend", "none", "--control-fd", "3", "--status-fd", "4", "--"},
		{"--kind", "unconfined", "--backend", "none", "--control-fd", "-1", "--status-fd", "4", "--", exe},
		{"--bogus", "--", exe},
	} {
		if _, err := Parse(args); !herr.Has(err, sandbox.CodeLaunchFailed) {
			t.Errorf("Parse(%q) = %v, want %s", args, err, sandbox.CodeLaunchFailed)
		}
	}
}

// TestReadyAgreesWithItsPlan: the host accepts only a confined stage reporting
// its backend and canary refused, and an unconfined stage reporting backend none
// and canary not-tried; a written canary is a failed confinement.
func TestReadyAgreesWithItsPlan(t *testing.T) {
	confined := Plan{Kind: Confined, Backend: sandbox.BackendBwrap}
	unconfined := Plan{Kind: Unconfined, Backend: sandbox.BackendNone}
	cases := []struct {
		plan  Plan
		ready Ready
		ok    bool
	}{
		{confined, Ready{Backend: sandbox.BackendBwrap, Canary: CanaryRefused}, true},
		{confined, Ready{Backend: sandbox.BackendBwrap, Canary: CanaryWritten}, false},
		{confined, Ready{Backend: sandbox.BackendBwrap, Canary: CanaryNotTried}, false},
		{confined, Ready{Backend: sandbox.BackendLandlock, Canary: CanaryRefused}, false},
		{unconfined, Ready{Backend: sandbox.BackendNone, Canary: CanaryNotTried}, true},
		{unconfined, Ready{Backend: sandbox.BackendNone, Canary: CanaryWritten}, false},
		{unconfined, Ready{Backend: sandbox.BackendBwrap, Canary: CanaryNotTried}, false},
		{Plan{Backend: sandbox.BackendNone}, Ready{Backend: sandbox.BackendNone, Canary: CanaryNotTried}, false},
	}
	for _, c := range cases {
		err := c.ready.Agrees(c.plan)
		if c.ok != (err == nil) || err != nil && !herr.Has(err, sandbox.CodeLaunchFailed) {
			t.Errorf("%+v.Agrees(%s) = %v, want ok=%v", c.ready, c.plan, err, c.ok)
		}
	}
}

// TestTryCanary: a canary the stage can open for writing is written, a missing
// one proves nothing and ends the stage, and one it cannot open is refused.
func TestTryCanary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "canary")
	if err := os.WriteFile(path, []byte("canary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := tryCanary(path); c != CanaryWritten || err != nil {
		t.Errorf("tryCanary(writable) = %q, %v; want written", c, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "canary" {
		t.Errorf("tryCanary changed the canary to %q", got)
	}
	if c, err := tryCanary(filepath.Join(dir, "missing")); !herr.Has(err, sandbox.CodeLaunchFailed) {
		t.Errorf("tryCanary(missing) = %q, %v; want %s", c, err, sandbox.CodeLaunchFailed)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Errorf("tryCanary created the missing canary: %v", err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	c, err := tryCanary(path)
	if c == CanaryWritten {
		t.Skip("this user may write a read-only file (root); refused is not observable here")
	}
	if c != CanaryRefused || err != nil {
		t.Errorf("tryCanary(read-only) = %q, %v; want refused", c, err)
	}
}

// TestProveUnconfinedTriesNothing: an unconfined plan reports backend none,
// governs nothing and does not try a canary; a backend this build cannot confine
// under ends the stage before ready.
func TestProveUnconfinedTriesNothing(t *testing.T) {
	got, err := prove(Plan{Kind: Unconfined, Backend: sandbox.BackendNone})
	want := Ready{Backend: sandbox.BackendNone, Governed: []sandbox.Effect{}, Canary: CanaryNotTried}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("prove(unconfined) = %+v, %v; want %+v", got, err, want)
	}
	canary := filepath.Join(t.TempDir(), "canary")
	if err := os.WriteFile(canary, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := prove(Plan{Kind: Confined, Backend: sandbox.BackendLandlock, Canary: canary}); !herr.Has(err, sandbox.CodeLaunchFailed) {
		t.Errorf("prove(landlock) = %v, want %s", err, sandbox.CodeLaunchFailed)
	}
}
