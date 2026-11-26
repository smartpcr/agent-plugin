//go:build scenario

package scenarios

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
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
		"danger unconfined":       {"danger-full-access", out("unconfined", "none"), nil, true},
		"danger confined":         {"danger-full-access", out("full", "bwrap"), nil, false},
	} {
		if err := confinementError(c.mode, c.s, c.backends); (err == nil) != c.ok {
			t.Errorf("%s: confinementError = %v, want ok %v", name, err, c.ok)
		}
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
