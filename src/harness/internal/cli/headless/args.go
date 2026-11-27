package headless

import (
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
)

// Args are the parsed words of `dsh headless` (app-cli.md §6.2.2).
type Args struct {
	JSON        bool
	Session     string
	AgentPreset string
	// Reasoning is auto, show or hide: where the model's reasoning is written.
	Reasoning string
	// ApprovalPolicy is ask or never; "" keeps the session's policy.
	ApprovalPolicy string
	// Command is the "/name input" line of --command, run through the command
	// registry instead of a task; "" for a task.
	Command string
	// Task is the joined task words; Stdin is set when the task is read from
	// standard input ("-" or no task word).
	Task  string
	Stdin bool
}

// valued are the flags that take a value.
var valued = map[string]bool{
	"--session": true, "--agent-preset": true, "--reasoning": true, "--approval-policy": true,
	"--permission-mode": true, "--add-dir": true, "--command": true,
}

// Parse reads the app words. A flag's value is the next word or follows "=";
// "--" ends the flags. A usage error is status 64.
func Parse(words []string) (Args, error) {
	a := Args{Reasoning: "auto"}
	var task []string
	flags := true
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !flags || !strings.HasPrefix(w, "--") || w == "-" {
			task = append(task, w)
			continue
		}
		if w == "--" {
			flags = false
			continue
		}
		name, value, inline := strings.Cut(w, "=")
		if name == "--json" {
			if inline {
				return Args{}, output.Usagef("--json takes no value")
			}
			a.JSON = true
			continue
		}
		if !valued[name] {
			return Args{}, output.Usagef("unknown flag %s", name)
		}
		if !inline {
			if i+1 >= len(words) {
				return Args{}, output.Usagef("%s needs a value", name)
			}
			i++
			value = words[i]
		}
		switch name {
		case "--session":
			a.Session = value
		case "--agent-preset":
			a.AgentPreset = value
		case "--reasoning":
			if value != "auto" && value != "show" && value != "hide" {
				return Args{}, output.Usagef("--reasoning is auto, show or hide, not %q", value)
			}
			a.Reasoning = value
		case "--approval-policy":
			if value != "ask" && value != "never" {
				return Args{}, output.Usagef("--approval-policy is ask or never, not %q", value)
			}
			a.ApprovalPolicy = value
		case "--command":
			if !strings.HasPrefix(strings.TrimSpace(value), "/") {
				return Args{}, output.Usagef(`--command is "/name input", not %q`, value)
			}
			a.Command = value
		default:
			// --permission-mode and --add-dir need A17's preset table and A10's
			// per-session roots, which this build's headless profile does not
			// mount yet: refused rather than ignored.
			return Args{}, output.Usagef("%s is not supported by this build", name)
		}
	}
	if a.Command != "" {
		if len(task) > 0 {
			return Args{}, output.Usagef("--command runs instead of a task: give no task words")
		}
		return a, nil
	}
	if len(task) == 0 || (len(task) == 1 && task[0] == "-") {
		a.Stdin = true
		return a, nil
	}
	a.Task = strings.Join(task, " ")
	return a, nil
}
