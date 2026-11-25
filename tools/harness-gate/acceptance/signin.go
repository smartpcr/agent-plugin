package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Credential is the signed-in session an indexed scenario needs (M1-G-TOOL-16).
// Under OWNER-2 (decision models-signed-in-claude-copilot) there is no API key:
// a real model is reached only through the user's own installed and signed-in
// client, so the credential of a scenario is the client it signs in through.
type Credential string

const (
	// CredentialNone is a scenario that reaches no model: no sign-in is checked.
	CredentialNone Credential = "none"
	// CredentialClaude is a scenario that needs the user's signed-in Claude Code.
	CredentialClaude Credential = "claude-session"
	// CredentialCopilot is a scenario that needs the user's signed-in GitHub
	// Copilot CLI.
	CredentialCopilot Credential = "copilot-session"
)

// Credentials are the accepted values of an index entry's credential field.
var Credentials = []Credential{CredentialNone, CredentialClaude, CredentialCopilot}

// ParseCredential returns the credential value names.
func ParseCredential(value string) (Credential, error) {
	c := Credential(value)
	if !slices.Contains(Credentials, c) {
		return "", fmt.Errorf("credential %q is not one of %s", value, credentialList())
	}
	return c, nil
}

func credentialList() string {
	names := make([]string, len(Credentials))
	for i, c := range Credentials {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

// Client is the client program a credential signs in through, as an operator
// names it; "" for CredentialNone.
func (c Credential) Client() string {
	switch c {
	case CredentialClaude:
		return "Claude Code (claude)"
	case CredentialCopilot:
		return "GitHub Copilot CLI (copilot)"
	}
	return ""
}

// SignInStatus is what a sign-in probe found: whether the client is signed in,
// the status command it ran, and a detail line. Detail holds only fields the probe
// parsed and allow-listed (the sign-in state and its method), never the command's
// raw output, so no account name, email or token reaches a record.
type SignInStatus struct {
	SignedIn bool
	Command  string
	Detail   string
}

// SignInProbe runs the non-interactive sign-in status check of the client of c on
// this host. An error means the check could not decide (the client is not
// installed, the command failed or timed out, or the client has no status
// command); the preflight treats it as not signed in.
type SignInProbe func(ctx context.Context, c Credential) (SignInStatus, error)

// SignIn is one checked credential of a preflight, with the planned scenarios
// that need it: the probe output an evidence record keeps (no token material).
type SignIn struct {
	Credential Credential
	Scenarios  []string
	Status     SignInStatus
}

// String is the record line of s.
func (s SignIn) String() string {
	return fmt.Sprintf("sign-in %s via %s: `%s`: %s (needed by %s)",
		s.Credential, s.Credential.Client(), s.Status.Command, s.Status.Detail, strings.Join(s.Scenarios, ", "))
}

// checkSignIns probes, once each, the credential of every planned scenario that
// needs one, and fails naming each scenario and its client when the client is not
// signed in or the check cannot decide. A CredentialNone scenario runs no probe; a
// nil probe fails every credentialed scenario, so nothing that needs a sign-in
// starts unchecked.
func checkSignIns(ctx context.Context, x *Index, plan []Scenario, probe SignInProbe) ([]SignIn, error) {
	var checked []SignIn
	for _, s := range plan {
		e, _ := x.Entry(s.ID)
		if e.Credential == CredentialNone || e.Credential == "" {
			continue
		}
		i := slices.IndexFunc(checked, func(c SignIn) bool { return c.Credential == e.Credential })
		if i < 0 {
			checked = append(checked, SignIn{Credential: e.Credential})
			i = len(checked) - 1
		}
		checked[i].Scenarios = append(checked[i].Scenarios, s.ID)
	}
	var errs []error
	for i := range checked {
		c := &checked[i]
		who := fmt.Sprintf("%s needs %s, signed in through %s", strings.Join(c.Scenarios, ", "), c.Credential, c.Credential.Client())
		if probe == nil {
			errs = append(errs, fmt.Errorf("%s: no sign-in probe is configured, so the sign-in cannot be checked", who))
			continue
		}
		st, err := probe(ctx, c.Credential)
		c.Status = st
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: the sign-in status check failed: %w", who, err))
		case !st.SignedIn:
			errs = append(errs, fmt.Errorf("%s: the client is not signed in on this host (`%s`: %s); sign in as yourself and run again", who, st.Command, st.Detail))
		}
	}
	return checked, errors.Join(errs...)
}

// CommandRunner runs name with args and returns its standard output and exit
// status. err is non-nil only when the program could not be run or did not end
// (not found, timed out); a non-zero exit is a status, not an error.
type CommandRunner func(ctx context.Context, name string, args ...string) (stdout []byte, exit int, err error)

// The non-interactive sign-in status commands (M1-G-TOOL-16).
//
// Claude Code 2.1.312 has "claude auth status [--json | --text]", "Show
// authentication status", JSON by default (Help 2.1.312, read 2026-11-23,
// docs/harness/libs/claude-code-cli.md). Its object carries the boolean loggedIn;
// the probe reads that and the authMethod string and nothing else, since the
// object also carries the account's email and organisation.
//
// The GitHub Copilot CLI 1.0.115 has no such command: its commands are app, login,
// help, init, config, update, version, workflow, sessions, memories, plugin, mcp,
// skill, instruction, lsp, sandbox and completion, and login only signs in (Help
// 1.0.115, read 2026-11-23, docs/harness/libs/github-copilot-cli.md). The only way
// to learn the sign-in is to send a prompt, which a preflight never does, so
// copilot-session fails closed until a status command exists.
var (
	claudeStatusArgs = []string{"auth", "status", "--json"}
	// claudeAuthMethod is the shape of an authMethod value the record may keep.
	claudeAuthMethod = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)
)

// ClaudeStatusCommand is the status command of CredentialClaude, as recorded.
var ClaudeStatusCommand = "claude " + strings.Join(claudeStatusArgs, " ")

// StatusProbe is the sign-in probe that runs each client's own status command
// through run (M1-G-TOOL-16). The caller gives run an environment without the
// credential variables a client would prefer to its sign-in, so a key or token in
// the environment never counts as a session.
func StatusProbe(run CommandRunner) SignInProbe {
	return func(ctx context.Context, c Credential) (SignInStatus, error) {
		switch c {
		case CredentialClaude:
			return claudeStatus(ctx, run)
		case CredentialCopilot:
			return SignInStatus{Command: "(none)", Detail: "no status command"},
				errors.New("the GitHub Copilot CLI has no non-interactive sign-in status command (Help 1.0.115); its sign-in cannot be checked without sending a prompt, so a copilot-session scenario does not start")
		}
		return SignInStatus{}, fmt.Errorf("credential %q has no sign-in probe", c)
	}
}

// claudeStatus runs "claude auth status --json" and reads loggedIn.
func claudeStatus(ctx context.Context, run CommandRunner) (SignInStatus, error) {
	st := SignInStatus{Command: ClaudeStatusCommand}
	out, code, err := run(ctx, "claude", claudeStatusArgs...)
	if err != nil {
		st.Detail = "not run"
		return st, fmt.Errorf("%s: %w (is Claude Code installed and on PATH?)", st.Command, err)
	}
	var v struct {
		LoggedIn   *bool  `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if jerr := json.Unmarshal(out, &v); jerr != nil || v.LoggedIn == nil {
		st.Detail = fmt.Sprintf("exit %d, no loggedIn field", code)
		if code != 0 {
			// A failed status command with no state to read is not a sign-in.
			return st, nil
		}
		return st, fmt.Errorf("%s: exit 0 but its output has no boolean loggedIn; the status format changed", st.Command)
	}
	st.SignedIn = *v.LoggedIn && code == 0
	st.Detail = fmt.Sprintf("exit %d, loggedIn=%t", code, *v.LoggedIn)
	if claudeAuthMethod.MatchString(v.AuthMethod) {
		st.Detail += ", authMethod=" + v.AuthMethod
	}
	return st, nil
}
