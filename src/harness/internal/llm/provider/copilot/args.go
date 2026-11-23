package copilot

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute/listing"
)

// The attempt files, inside the attempt directory. There is no system prompt
// file: the client cannot replace its own, so the harness system prompt is the
// leading <system> section of standard input (github-copilot-cli.md sec. 5).
const (
	ToolsFile = "tools.json"
	MCPFile   = "mcp.json"
	// CallsFile is where the listing server records a tools/call it refused.
	CallsFile = "mcp-calls.log"
)

// ToolPrefix is how the client names a tool of the listing server "dsh".
// Inference: the client names an MCP tool "<server>-<tool>"; the tool-name form
// is opaque (github-copilot-cli.md sec. 6) and DH-E-COPILOTCLI-1 (plan 16.5.9)
// pins it.
const ToolPrefix = listing.ServerName + "-"

// ArgsSpec is what one attempt's argument list depends on.
type ArgsSpec struct {
	// Dir is the attempt directory; clientroute.AttemptPlaceholder rebuilds the
	// logged list.
	Dir       string
	Model     string
	Effort    string
	SessionID string
	// Tools are the declared tools' harness names, in request order; the MCP
	// configuration is passed only when there is one.
	Tools []string
}

// Args is the argument list after the program (github-copilot-cli.md sec. 5.1),
// a pure function of a. No variable text is an argument (the request is on
// standard input), so a copilot.cmd shim is started with checked arguments.
// The order is fixed, so the list rebuilt from the log equals the one started.
func Args(a ArgsSpec) []string {
	args := []string{
		"--no-auto-update",
		"--output-format", "json", "--stream", "on",
		"--deny-tool", "shell", "--deny-tool", "write", "--deny-tool", "read", "--deny-tool", "url", "--deny-tool", "memory",
		"--disable-builtin-mcps", "--no-ask-user", "--no-custom-instructions", "--disallow-temp-dir",
	}
	if len(a.Tools) > 0 {
		args = append(args, "--additional-mcp-config", "@"+filepath.Join(a.Dir, MCPFile), "--deny-tool", listing.ServerName)
	}
	// Only the harness tools are visible; with none the value is empty.
	// Inference: an empty value hides every tool, and the value is one
	// comma-separated list (DH-E-COPILOTCLI-1 confirms both).
	visible := make([]string, len(a.Tools))
	for i, t := range a.Tools {
		visible[i] = ToolPrefix + t
	}
	args = append(args,
		"--available-tools", strings.Join(visible, ","),
		"--sync-session=off", "--disable-client-telemetry",
		"--session-id", a.SessionID,
		"--model", a.Model,
	)
	if a.Effort != "" {
		args = append(args, "--reasoning-effort", a.Effort)
	}
	return args
}

// ProbeArgs are the version probe's arguments: `copilot version` (no dashes)
// makes a network request and is not used (github-copilot-cli.md sec. 2).
var ProbeArgs = []string{"--version", "--no-auto-update"}

// The version floor and probe (github-copilot-cli.md sec. 5.5).
var (
	// Floor is the oldest client accepted: 1.0.115 is the version the route was
	// read against, and the JSONL schema is undocumented, so no older version is
	// assumed to match.
	Floor = clientroute.Version{Major: 1, Minor: 0, Patch: 115}
	// Checked is the newest version the route was read against (Help 1.0.115-7,
	// 2026-11-23); a newer one runs and is not marked checked.
	Checked = clientroute.Version{Major: 1, Minor: 0, Patch: 115}
	// VersionPattern is the whole first line of `copilot --version`
	// ("GitHub Copilot CLI 1.0.115-7."); the update hint after it is ignored.
	VersionPattern = regexp.MustCompile(`^GitHub Copilot CLI (\d+)\.(\d+)\.(\d+)(-\d+)?\.?$`)
)

// Env is the route's environment rules (github-copilot-cli.md sec. 5.3), on top
// of subprocess.BuildEnv's default rules and the secret store. COPILOT_HOME,
// COPILOT_CACHE_HOME, GH_HOST, COPILOT_GH_HOST and the proxy variables are kept:
// they say where the sign-in and the installed build live, and the harness
// passes them on without reading what they point at.
func Env() clientroute.EnvSpec {
	return clientroute.EnvSpec{
		Remove: []string{
			// Credentials: a token "takes precedence over previously stored
			// credentials", so none may reach the client.
			"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
			// Another provider (DH-ADR-LLM-3): bring-your-own-key and offline.
			"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_API_KEY",
			"COPILOT_PROVIDER_API_KEY_COMMAND", "COPILOT_PROVIDER_BEARER_TOKEN", "COPILOT_PROVIDER_HEADERS",
			"COPILOT_PROVIDERS_CONFIG", "COPILOT_OFFLINE",
			// The model is the route's --model, and is logged.
			"COPILOT_MODEL",
			// They approve tools.
			"COPILOT_ALLOW_ALL", "COPILOT_ASSISTED_APPROVAL",
			// They change which build runs.
			"COPILOT_CLI_VERSION", "COPILOT_CLI_DIST_DIR",
		},
		Patterns: []string{"COPILOT_PROVIDER_*"},
		Set: []string{
			"COPILOT_AUTO_UPDATE=false",
			"COPILOT_TELEMETRY_DISABLED=true",
		},
	}
}
