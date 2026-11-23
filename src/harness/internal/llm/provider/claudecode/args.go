package claudecode

import (
	"path/filepath"
	"regexp"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
)

// The attempt files, inside the attempt directory.
const (
	SystemFile = "system.txt"
	ToolsFile  = "tools.json"
	MCPFile    = "mcp.json"
	// CallsFile is where the listing server records a tools/call it refused.
	CallsFile = "mcp-calls.log"
)

// ArgsSpec is what one attempt's argument list depends on.
type ArgsSpec struct {
	// Dir is the attempt directory; clientroute.AttemptPlaceholder rebuilds the
	// logged list.
	Dir       string
	Model     string
	Effort    string
	SessionID string
	// Tools is true when the request declares tools: the MCP configuration is
	// passed only then.
	Tools bool
}

// Args is the argument list after the program (claude-code-cli.md sec. 5.1),
// a pure function of a: no variable text is an argument, so a .cmd shim can be
// started with checked arguments. The order is fixed, so the list rebuilt from
// the log equals the one started.
func Args(a ArgsSpec) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json", "--verbose",
		"--include-partial-messages",
		"--system-prompt-file", filepath.Join(a.Dir, SystemFile),
		"--tools", "",
	}
	if a.Tools {
		args = append(args, "--strict-mcp-config", "--mcp-config", filepath.Join(a.Dir, MCPFile))
	}
	args = append(args,
		"--permission-mode", "dontAsk", "--permission-prompts", "none",
		"--max-turns", "1",
		"--no-session-persistence",
		"--session-id", a.SessionID,
		"--setting-sources", "local",
		"--model", a.Model,
	)
	if a.Effort != "" {
		args = append(args, "--effort", a.Effort)
	}
	return args
}

// The version floor and probe (claude-code-cli.md sec. 5.5).
var (
	// Floor is the oldest client accepted: 2.1.259 is the version the vendor
	// names for --permission-prompts, the newest flag of Args with a documented
	// minimum.
	Floor = clientroute.Version{Major: 2, Minor: 1, Patch: 259}
	// Checked is the newest version the route was read against (Help 2.1.312,
	// 2026-11-23); a newer one runs and is not marked checked.
	Checked = clientroute.Version{Major: 2, Minor: 1, Patch: 312}
	// VersionPattern is the whole of `claude --version`'s line.
	VersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+) \(Claude Code\)$`)
)

// Env is the route's environment rules (claude-code-cli.md sec. 5.3), on top
// of subprocess.BuildEnv's default rules and the secret store.
func Env() clientroute.EnvSpec {
	return clientroute.EnvSpec{
		Remove: []string{
			// The plan's four, then the vendor's credential variables.
			"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "GITHUB_TOKEN", "GH_TOKEN",
			"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN",
			"ANTHROPIC_AWS_API_KEY", "AWS_BEARER_TOKEN_BEDROCK",
			"ANTHROPIC_FOUNDRY_API_KEY", "ANTHROPIC_FOUNDRY_AUTH_TOKEN", "ANTHROPIC_PROFILE",
			"CLAUDE_CODE_CLIENT_KEY_PASSPHRASE",
			// Another endpoint, provider or account (DH-ADR-LLM-3).
			"ANTHROPIC_BASE_URL", "ANTHROPIC_FOUNDRY_RESOURCE",
			"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
			"CLAUDE_CODE_USE_MANTLE", "CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD",
			// The model is the route's --model, and is logged.
			"ANTHROPIC_MODEL",
			// Safety and mode.
			"CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_EVEN_MORE_DANGEROUSLY_SKIP_PERMISSIONS",
		},
		Patterns: []string{"ANTHROPIC_*_BASE_URL", "ANTHROPIC_DEFAULT_*_MODEL"},
		Set: []string{
			"CLAUDE_CODE_MAX_RETRIES=0",
			"CLAUDE_CODE_SKIP_PROMPT_HISTORY=1",
			"DISABLE_AUTOUPDATER=1",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		},
	}
}
