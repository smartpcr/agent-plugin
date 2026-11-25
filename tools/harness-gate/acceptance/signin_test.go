package acceptance

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// signInIndex has one scenario of each credential, all built.
const signInIndex = `built-through: DH-S-17
stages:
  DH-S-17:
    - id: DH-E-LLM-1
      authored: DH-S-9
      credential: claude-session
    - id: DH-E-AGENT-1
      authored: DH-S-11
      credential: claude-session
    - id: DH-E-STORE-1
      authored: DH-S-7
      credential: none
    - id: DH-E-COPILOT-1
      authored: DH-S-9
      credential: copilot-session
`

var signInScenarios = []Scenario{
	{ID: "DH-E-LLM-1", File: "s/llm_scenario_test.go", Test: "TestLLM"},
	{ID: "DH-E-AGENT-1", File: "s/agent_scenario_test.go", Test: "TestAgent"},
	{ID: "DH-E-STORE-1", File: "s/store_scenario_test.go", Test: "TestStore"},
	{ID: "DH-E-COPILOT-1", File: "s/copilot_scenario_test.go", Test: "TestCopilot"},
}

// fakeProbe is an injected sign-in probe: it records each credential it is asked
// about and reports signedIn. No real client is run.
type fakeProbe struct {
	signedIn bool
	err      error
	asked    []Credential
}

func (f *fakeProbe) probe(_ context.Context, c Credential) (SignInStatus, error) {
	f.asked = append(f.asked, c)
	return SignInStatus{SignedIn: f.signedIn, Command: "fake status", Detail: "loggedIn=" + map[bool]string{true: "true", false: "false"}[f.signedIn]}, f.err
}

// TestPreflightChecksTheSignIn is the scenario of plan Stage 19.2, "A missing
// sign-in stops the run": given an index entry with credential: claude-session and
// an injected status probe that reports signed out, the preflight fails naming the
// scenario and the client and plans nothing, so nothing starts. A credential: none
// entry runs no probe, and a signed-in probe lets the run proceed, each
// credential probed once however many scenarios need it (M1-G-TOOL-16).
//
//dsh:test DH-T-TOOLCHAIN-6
func TestPreflightChecksTheSignIn(t *testing.T) {
	x := mustParse(t, signInIndex)
	ctx := context.Background()
	require := func(ids ...string) map[string]string {
		return map[string]string{RequireEnv: strings.Join(ids, ",")}
	}

	t.Run("signed out stops the run", func(t *testing.T) {
		p := &fakeProbe{signedIn: false}
		plan, err := PreflightWith(ctx, require("DH-E-LLM-1", "DH-E-STORE-1"), x, signInScenarios, p.probe)
		if err == nil {
			t.Fatal("PreflightWith passed with the client signed out")
		}
		for _, want := range []string{"DH-E-LLM-1 needs claude-session", "Claude Code (claude)", "is not signed in"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("PreflightWith = %v, want it to name %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "DH-E-STORE-1") {
			t.Errorf("PreflightWith = %v: the credential: none scenario is not a sign-in failure", err)
		}
		if len(plan.Scenarios) != 0 {
			t.Errorf("a failed preflight planned %v; nothing may start", plan.Tests())
		}
		if !slices.Equal(p.asked, []Credential{CredentialClaude}) {
			t.Errorf("probed %v, want claude-session once", p.asked)
		}
	})

	t.Run("a probe that cannot decide stops the run", func(t *testing.T) {
		p := &fakeProbe{err: errors.New("claude: executable file not found")}
		plan, err := PreflightWith(ctx, require("DH-E-AGENT-1"), x, signInScenarios, p.probe)
		if err == nil || !strings.Contains(err.Error(), "DH-E-AGENT-1 needs claude-session") || !strings.Contains(err.Error(), "executable file not found") || len(plan.Scenarios) != 0 {
			t.Errorf("PreflightWith = %v, %v; want DH-E-AGENT-1 refused with the probe's error and no plan", plan.Tests(), err)
		}
	})

	t.Run("none runs no probe", func(t *testing.T) {
		p := &fakeProbe{signedIn: false}
		plan, err := PreflightWith(ctx, require("DH-E-STORE-1"), x, signInScenarios, p.probe)
		if err != nil || !slices.Equal(plan.Tests(), []string{"TestStore"}) {
			t.Errorf("PreflightWith = %v, %v; want TestStore planned", plan.Tests(), err)
		}
		if len(p.asked) != 0 || len(plan.SignIns) != 0 {
			t.Errorf("a credential: none run probed %v (sign-ins %v), want no probe", p.asked, plan.SignIns)
		}
		// With no probe at all a none-only stage still runs.
		if plan, err := Preflight(require("DH-E-STORE-1"), x, signInScenarios); err != nil || len(plan.Scenarios) != 1 {
			t.Errorf("Preflight with no probe = %v, %v; want TestStore planned", plan.Tests(), err)
		}
	})

	t.Run("signed in proceeds", func(t *testing.T) {
		p := &fakeProbe{signedIn: true}
		plan, err := PreflightWith(ctx, require("DH-E-LLM-1", "DH-E-AGENT-1", "DH-E-STORE-1"), x, signInScenarios, p.probe)
		if err != nil || !slices.Equal(plan.Tests(), []string{"TestLLM", "TestAgent", "TestStore"}) {
			t.Fatalf("PreflightWith = %v, %v; want all three planned", plan.Tests(), err)
		}
		if !slices.Equal(p.asked, []Credential{CredentialClaude}) {
			t.Errorf("probed %v, want claude-session once for two scenarios", p.asked)
		}
		if len(plan.SignIns) != 1 || !slices.Equal(plan.SignIns[0].Scenarios, []string{"DH-E-LLM-1", "DH-E-AGENT-1"}) {
			t.Fatalf("sign-ins = %+v, want one claude-session line naming both scenarios", plan.SignIns)
		}
		if got, want := plan.SignIns[0].String(), "sign-in claude-session via Claude Code (claude): `fake status`: loggedIn=true (needed by DH-E-LLM-1, DH-E-AGENT-1)"; got != want {
			t.Errorf("sign-in line = %q, want %q", got, want)
		}
	})

	t.Run("no probe refuses a credentialed scenario", func(t *testing.T) {
		plan, err := Preflight(require("DH-E-LLM-1"), x, signInScenarios)
		if err == nil || !strings.Contains(err.Error(), "DH-E-LLM-1 needs claude-session") || !strings.Contains(err.Error(), "no sign-in probe") || len(plan.Scenarios) != 0 {
			t.Errorf("Preflight = %v, %v; want DH-E-LLM-1 refused for want of a probe", plan.Tests(), err)
		}
	})

	t.Run("copilot fails closed through the status probe", func(t *testing.T) {
		run := func(context.Context, string, ...string) ([]byte, int, error) {
			t.Error("the Copilot probe ran a command")
			return nil, 0, nil
		}
		plan, err := PreflightWith(ctx, require("DH-E-COPILOT-1"), x, signInScenarios, StatusProbe(run))
		if err == nil || !strings.Contains(err.Error(), "DH-E-COPILOT-1 needs copilot-session") || !strings.Contains(err.Error(), "GitHub Copilot CLI (copilot)") || !strings.Contains(err.Error(), "no non-interactive sign-in status command") || len(plan.Scenarios) != 0 {
			t.Errorf("PreflightWith = %v, %v; want DH-E-COPILOT-1 refused, naming the client", plan.Tests(), err)
		}
	})
}

// TestClaudeStatusProbe: the Claude probe runs "claude auth status --json" through
// the injected runner and reads only loggedIn and authMethod; the email and the
// organisation in the object never reach the detail line (M1-G-TOOL-16).
func TestClaudeStatusProbe(t *testing.T) {
	tests := map[string]struct {
		out      string
		exit     int
		runErr   error
		signedIn bool
		detail   string
		fail     string
	}{
		"signed in":           {out: `{"loggedIn":true,"authMethod":"claude.ai","email":"someone@example.com","orgName":"Org"}`, signedIn: true, detail: "exit 0, loggedIn=true, authMethod=claude.ai"},
		"signed out":          {out: `{"loggedIn":false}`, exit: 1, detail: "exit 1, loggedIn=false"},
		"signed out, exit 0":  {out: `{"loggedIn":false,"authMethod":"none"}`, detail: "exit 0, loggedIn=false, authMethod=none"},
		"odd auth method":     {out: `{"loggedIn":true,"authMethod":"has spaces and @"}`, signedIn: true, detail: "exit 0, loggedIn=true"},
		"failed, no state":    {out: "Error: something", exit: 2, detail: "exit 2, no loggedIn field"},
		"format changed":      {out: `{"signedIn":true}`, fail: "has no boolean loggedIn"},
		"not installed":       {runErr: errors.New(`exec: "claude": executable file not found in %PATH%`), fail: "is Claude Code installed"},
		"logged in, non-zero": {out: `{"loggedIn":true}`, exit: 1, detail: "exit 1, loggedIn=true"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var ran []string
			run := func(_ context.Context, name string, args ...string) ([]byte, int, error) {
				ran = append([]string{name}, args...)
				return []byte(tt.out), tt.exit, tt.runErr
			}
			st, err := StatusProbe(run)(context.Background(), CredentialClaude)
			if got := strings.Join(ran, " "); got != ClaudeStatusCommand || st.Command != ClaudeStatusCommand {
				t.Errorf("ran %q (command %q), want %q", got, st.Command, ClaudeStatusCommand)
			}
			if tt.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tt.fail) {
					t.Errorf("probe = %+v, %v; want an error containing %q", st, err, tt.fail)
				}
				return
			}
			if err != nil || st.SignedIn != tt.signedIn || st.Detail != tt.detail {
				t.Errorf("probe = %+v, %v; want signed in %v, detail %q", st, err, tt.signedIn, tt.detail)
			}
			if strings.Contains(st.Detail, "example.com") || strings.Contains(st.Detail, "Org") {
				t.Errorf("detail %q carries account fields", st.Detail)
			}
		})
	}
	if _, err := StatusProbe(nil)(context.Background(), Credential("api-key")); err == nil {
		t.Error("an unknown credential was probed")
	}
}

func TestParseCredential(t *testing.T) {
	for _, c := range Credentials {
		if got, err := ParseCredential(string(c)); err != nil || got != c {
			t.Errorf("ParseCredential(%s) = %v, %v", c, got, err)
		}
	}
	if _, err := ParseCredential("Claude-Session"); err == nil {
		t.Error("ParseCredential accepted a value in another case")
	}
	if CredentialNone.Client() != "" || CredentialCopilot.Client() == "" {
		t.Errorf("Client() = %q, %q", CredentialNone.Client(), CredentialCopilot.Client())
	}
}
