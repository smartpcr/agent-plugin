package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

type run struct {
	status         int
	stdout, stderr string
}

// dispatch runs one command line in process with captured streams.
func dispatch(t *testing.T, launchFn LaunchFunc, args ...string) run {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Env{
		Request: launch.Request{Args: append([]string{"dsh"}, args...),
			Build: launch.BuildIdentity{Version: "v1.2.3", Revision: "abc123", GoVersion: "go1.26.0"}},
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, Launch: launchFn,
	}
	status := Main(context.Background(), env)
	return run{status, out.String(), errOut.String()}
}

// TC-06a: an unknown launcher flag is one usage line plus the help hint on standard
// error, nothing on standard output, and status 2.
func TestUsageErrorExits2(t *testing.T) {
	r := dispatch(t, nil, "--no-such-flag")
	if r.status != 2 {
		t.Errorf("status = %d, want 2", r.status)
	}
	want := "dsh: cli/usage: unknown flag: --no-such-flag\n" + output.HelpHint + "\n"
	if r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
}

func TestUsageErrorsOfCommands(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"version", "--nope"},
		{"catalog", "bogus"},
		{"catalog", "--nope"},
		{"completion"},
		{"completion", "tcsh"},
		{"help", "no-such"},
		{"--layer", "x.yml", "version"},
		{"--version", "--profile", "web"},
	} {
		r := dispatch(t, nil, args...)
		lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
		if r.status != output.Usage || len(lines) != 2 || !strings.Contains(lines[0], "cli/usage: ") || lines[1] != output.HelpHint {
			t.Errorf("dsh %v: status %d, stderr %q", args, r.status, r.stderr)
		}
	}
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-V"}, {"version"}} {
		r := dispatch(t, nil, args...)
		if r.status != 0 || !strings.HasPrefix(r.stdout, "dsh v1.2.3 (abc123; go1.26.0; ") || r.stderr != "" {
			t.Errorf("dsh %v = %+v", args, r)
		}
	}
	r := dispatch(t, nil, "version", "--json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil || r.status != 0 || doc["version"] != "v1.2.3" || doc["go"] != "go1.26.0" {
		t.Errorf("version --json = %+v (%v)", r, err)
	}
}

func TestHelpIsStatus0OnStdout(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"help", "catalog", "commands"}, {"catalog", "commands", "-h"},
		{"catalog"}, {"catalog", "--help"}, {"help", "web"}} {
		r := dispatch(t, nil, args...)
		if r.status != 0 || !strings.HasPrefix(r.stdout, "Usage: dsh") || r.stderr != "" {
			t.Errorf("dsh %v = %+v", args, r)
		}
	}
	if r := dispatch(t, nil, "help", "catalog"); !strings.Contains(r.stdout, "commands  Print the command reference") {
		t.Errorf("group help lacks its commands:\n%s", r.stdout)
	}
}

func TestProfileLaunch(t *testing.T) {
	var got Launch
	fn := func(_ context.Context, l Launch) error { got = l; return l.Result.Line("launched") }
	r := dispatch(t, fn, "--home", "h", "--layer", "a.yml", "--layer", "b.yml", "--safe", "headless", "run it", "--help", "--layer", "inner")
	if r.status != 0 || r.stdout != "launched\n" {
		t.Fatalf("launch = %+v", r)
	}
	req := got.Request
	if got.Profile.Name != profile.Headless || req.Profile != profile.Headless || req.Home != "h" || !req.Safe ||
		!slices.Equal(req.Overlays, []string{"a.yml", "b.yml"}) || !slices.Equal(req.Words, []string{"run it", "--help", "--layer", "inner"}) {
		t.Errorf("launch = %+v", got)
	}

	r = dispatch(t, fn, "--profile", "sdk", "version")
	if r.status != 0 || got.Profile.Name != profile.SDK || !slices.Equal(got.Request.Words, []string{"version"}) {
		t.Errorf("--profile sdk version = %+v, %+v", r, got.Request)
	}
}

// app-cli.md §6.2.1: after --profile <name> every remaining word is an app word,
// even one that is also a launcher option.
func TestExplicitProfileEndsLauncherOptions(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		home  string
		words []string
	}{
		{[]string{"--profile", "headless", "--json"}, "", []string{"--json"}},
		{[]string{"--profile=sdk", "--help"}, "", []string{"--help"}},
		{[]string{"--profile", "sdk", "-h"}, "", []string{"-h"}},
		{[]string{"--home", "h", "--profile=headless", "--home", "app", "--profile", "web", "--version", "--layer", "x", "--"},
			"h", []string{"--home", "app", "--profile", "web", "--version", "--layer", "x", "--"}},
		{[]string{"--profile", "web"}, "", nil},
	} {
		var got Launch
		fn := func(_ context.Context, l Launch) error { got = l; return nil }
		r := dispatch(t, fn, tc.args...)
		if r.status != 0 || r.stdout != "" || r.stderr != "" {
			t.Errorf("dsh %q = %+v", tc.args, r)
			continue
		}
		if got.Request.Home != tc.home || !slices.Equal(got.Request.Words, tc.words) {
			t.Errorf("dsh %q launched home %q words %q, want %q %q", tc.args, got.Request.Home, got.Request.Words, tc.home, tc.words)
		}
	}
	if r := dispatch(t, nil, "--profile"); r.status != output.Usage {
		t.Errorf("--profile without a name = %+v", r)
	}
}

// app-cli.md §6.2.3: under --json a usage error also ends the result stream with the
// error document.
func TestJSONUsageErrorWritesErrorDocument(t *testing.T) {
	for _, args := range [][]string{{"version", "--json", "--nope"}, {"version", "--nope", "--json=true"}, {"catalog", "commands", "--json", "extra"}} {
		r := dispatch(t, nil, args...)
		var doc struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil || doc.Error.Code != "cli/usage" || doc.Error.Message == "" {
			t.Errorf("dsh %q stdout = %q (%v)", args, r.stdout, err)
		}
		if r.status != output.Usage || !strings.HasSuffix(r.stderr, output.HelpHint+"\n") {
			t.Errorf("dsh %q = %+v", args, r)
		}
	}
	if r := dispatch(t, nil, "version", "--json=false", "--nope"); r.status != output.Usage || r.stdout != "" {
		t.Errorf("--json=false usage error = %+v", r)
	}
}

type closedWriter struct{}

func (closedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// Help printed by any route goes through the result writer: a closed stdout is
// cli/output-closed, not a silent success.
func TestHelpToClosedStdoutFails(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, {"help", "web"}, {"catalog"}, {"catalog", "--help"}, {"catalog", "commands", "-h"}, {"help", "catalog"}} {
		var errOut bytes.Buffer
		env := Env{Request: launch.Request{Args: append([]string{"dsh"}, args...)}, Stdout: closedWriter{}, Stderr: &errOut}
		status := Main(context.Background(), env)
		if status != output.Failed || !strings.Contains(errOut.String(), "cli/output-closed") {
			t.Errorf("dsh %q to a closed stdout: status %d, stderr %q", args, status, errOut.String())
		}
	}
}

func TestUnknownProfileAndUnwiredLaunch(t *testing.T) {
	r := dispatch(t, nil, "no-such-profile")
	if r.status != output.Invalid || !strings.HasPrefix(r.stderr, "dsh no-such-profile: profile/unknown: ") {
		t.Errorf("unknown profile = %+v", r)
	}
	r = dispatch(t, nil, "web")
	if r.status != output.Unavailable || !strings.HasPrefix(r.stderr, "dsh web: cli/unavailable: ") {
		t.Errorf("unwired launch = %+v", r)
	}
	r = dispatch(t, nil, "sandbox-launch", "--anything")
	if r.status != output.Unavailable {
		t.Errorf("hidden mode = %+v", r)
	}
}

func TestLaunchErrorIsMapped(t *testing.T) {
	fn := func(context.Context, Launch) error { return context.Canceled }
	if r := dispatch(t, fn, "web"); r.status != output.Cancelled {
		t.Errorf("cancelled launch = %+v", r)
	}
}

func TestCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		r := dispatch(t, nil, "completion", shell)
		if r.status != 0 || !strings.Contains(r.stdout, "dsh") || r.stderr != "" {
			t.Errorf("completion %s: status %d stderr %q", shell, r.status, r.stderr)
		}
	}
}

func TestReservedWordsAreNoProfiles(t *testing.T) {
	reserved := Reserved()
	for _, w := range []string{"help", "version", "catalog", "completion", "sandbox-launch", "ssh-helper"} {
		if !slices.Contains(reserved, w) {
			t.Errorf("%s is not reserved", w)
		}
	}
	for _, name := range profile.Names() {
		if slices.Contains(reserved, name) {
			t.Errorf("profile %s is a reserved word", name)
		}
	}
}

// The generated CLI reference equals the committed golden.
func TestReferenceIsCurrent(t *testing.T) {
	golden.Assert(t, "reference", Reference())
	r := dispatch(t, nil, "catalog", "commands")
	if r.status != 0 || r.stdout != Reference() {
		t.Errorf("catalog commands differs from Reference: %+v", r.status)
	}
	r = dispatch(t, nil, "catalog", "commands", "--json")
	var doc ReferenceDocument
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil || len(doc.Commands) == 0 || len(doc.Statuses) == 0 {
		t.Errorf("catalog commands --json: %v %+v", err, r)
	}
}
