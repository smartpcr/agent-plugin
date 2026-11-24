package closure

import (
	"fmt"
	"strings"
)

// ScenarioRow is one scenario row a stage record owes: the scenario, its variant, its
// venue and its leg. A row whose venue or leg is edited no longer matches, so the
// owed row reads missing rather than letting its result go unjudged.
type ScenarioRow struct {
	Scenario, Variant, Venue, Leg string
}

// ManualCheck is one manual check a stage record owes: its step and the check itself,
// so a check cannot be replaced by a second copy of another check of the same step.
type ManualCheck struct {
	Step, Check string
}

// Obligations are the rows a stage record owes from the day Stage 1.1 created it
// (implementation-plan Stage 1.1: "every step has a commit SHA, manual checks and
// owner defaults recorded"): impl.md's steps of the stage, the manual checks it names
// (by step and check), the impl.md sec. 9.1 owner defaults whose "Confirm
// before" is the stage, and the scenario rows of its run. Deleting an owed row is a
// blocker, never a way to remove one.
type Obligations struct {
	Steps         []string
	ManualChecks  []ManualCheck
	OwnerDefaults []string
	Scenarios     []ScenarioRow
}

// Missing returns the owed rows rec does not have. With built the steps, manual
// checks and owner defaults are read; the scenario rows always are.
func (ob Obligations) Missing(rec *Record, built bool) []string {
	var out []string
	if built {
		have := map[string]bool{}
		for _, r := range rec.Steps.Rows {
			have[r.Get("step")] = true
		}
		for _, s := range ob.Steps {
			if !have[s] {
				out = append(out, fmt.Sprintf("step %s has no row in %s; the stage owes every step its commit", s, StepsHeading))
			}
		}
		checks := map[ManualCheck]int{}
		for _, r := range rec.ManualChecks.Rows {
			c := ManualCheck{r.Get("step"), normalDefault(r.Get("check"))}
			if checks[c]++; checks[c] == 2 {
				out = append(out, fmt.Sprintf("step %s: manual check %q is listed twice in %s; each check is recorded once", c.Step, r.Get("check"), ManualChecksHeading))
			}
		}
		for _, c := range ob.ManualChecks {
			if checks[ManualCheck{c.Step, normalDefault(c.Check)}] == 0 {
				out = append(out, fmt.Sprintf("step %s: manual check %q has no row in %s; the stage owes it", c.Step, c.Check, ManualChecksHeading))
			}
		}
		defaults := map[string]bool{}
		for _, r := range rec.OwnerDefaults.Rows {
			defaults[normalDefault(r.Get("default"))] = true
		}
		for _, d := range ob.OwnerDefaults {
			if !defaults[normalDefault(d)] {
				out = append(out, fmt.Sprintf("owner default %q has no row in %s; it is confirmed or its document changed before the stage closes (M2-C-4)", d, OwnerDefaultsHeading))
			}
		}
	}
	rows := map[ScenarioRow]bool{}
	for _, r := range rec.Scenarios.Rows {
		rows[ScenarioRow{r.Get("scenario"), r.Get("variant"), strings.ToLower(r.Get("venue")), strings.ToLower(r.Get("leg"))}] = true
	}
	for _, s := range ob.Scenarios {
		if !rows[s] {
			out = append(out, fmt.Sprintf("%s [%s] (venue %s, leg %s) has no row in %s; the stage owes it (M2-C-2)", s.Scenario, s.Variant, s.Venue, s.Leg, ScenariosHeading))
		}
	}
	return out
}

func normalDefault(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(s, "`", "")), " "))
}

// m2Obligations are the rows of the harness:DSH-M2 records as Stage 1.1 created them.
var m2Obligations = map[string]Obligations{
	"DH-S-12": {
		Steps: []string{"12.1", "12.2", "12.3", "12.4", "12.5"},
		ManualChecks: []ManualCheck{
			{"12.2", "the write tool asked for a path outside the roots through a symbolic link: the denial (scratch directory)"},
			{"12.2", "a directory swapped for a link during a write: the fence held (scratch directory)"},
		},
		OwnerDefaults: []string{"Full-access label", "UTF-8-only text tools"},
	},
	"DH-S-13": {
		Steps: []string{"13.1", "13.2", "13.3", "13.4", "13.5", "13.6", "13.7", "13.8"},
		ManualChecks: []ManualCheck{
			{"13.4", "on a Linux host, a confined command that writes outside its roots: the denial and the reported enforcement"},
			{"13.3", "both backends removed: the command did not run"},
		},
		OwnerDefaults: []string{"`partial` when delegated execution cannot be stopped"},
	},
	"DH-S-14": {
		Steps: []string{"14.1", "14.2", "14.3", "14.4", "14.5"},
		ManualChecks: []ManualCheck{
			{"14.2", "the composed tree of each profile previewed: which layer supplied three chosen values"},
			{"14.3", "a preset edited while a test session runs: the session keeps its revision"},
		},
	},
	"DH-S-15": {
		Steps: []string{"15.1", "15.2", "15.3", "15.4", "15.5", "15.6", "15.7", "15.8"},
		ManualChecks: []ManualCheck{
			{"15.2", "a listener refuses a request without the cookie, from another origin, and from a process the host started for the model"},
			{"15.6", "one session driven by hand over stdio with typed JSON lines"},
		},
		OwnerDefaults: []string{"No SDK session grant", "Client time zone"},
	},
	"DH-S-16": {
		Steps: []string{"16.1", "16.2", "16.3", "16.4", "16.5", "16.6", "16.7", "16.8", "16.9", "16.10"},
		ManualChecks: []ManualCheck{
			{"16.3", "one `dsh headless` prompt on the signed-in Claude Code route (M2-I-1) with an approval answered on the terminal"},
			{"16.4", "Ctrl-C during a tool call"},
			{"16.5", "a second `dsh` beside a running host"},
		},
		OwnerDefaults: []string{"Second `dsh web` reuses the host", "Windows double-click", "`dsh headless` progress"},
		Scenarios: []ScenarioRow{
			{"DH-E-STORE-1", "Linux (WSL)", "guarded", "required"},
			{"DH-E-STORE-1", "Windows", "guarded", "required"},
			{"DH-E-STORE-1", "macOS", "guarded", "required"},
			{"DH-E-STORE-2", "Linux (WSL)", "guarded", "required"},
			{"DH-E-STORE-6", "Linux (WSL)", "guarded", "required"},
			{"DH-E-STORE-6", "Windows", "guarded", "required"},
			{"DH-E-STORE-6", "macOS", "guarded", "required"},
			{"DH-E-FS-3", "Linux (WSL)", "guarded", "required"},
			{"DH-E-FS-3", "Windows", "guarded", "required"},
			{"DH-E-FS-3", "macOS", "guarded", "required"},
		},
	},
	"DH-S-17": {
		Steps: []string{"17.1", "17.2", "17.3", "17.4", "17.5", "17.6", "17.7", "17.8", "17.9"},
		Scenarios: []ScenarioRow{
			{"DH-E-TOOLCHAIN-1", "Linux (E1)", "acceptance", "required"},
			{"DH-E-SESSION-1", "Linux (E1)", "acceptance", "required"},
			{"DH-E-SESSION-2", "Linux (E1)", "acceptance", "required"},
			{"DH-E-AGENT-1", "Linux (E1)", "acceptance", "required"},
			{"DH-E-AGENT-2", "Linux (E1)", "acceptance", "required"},
			{"DH-E-AGENT-3", "Linux (E1)", "acceptance", "required"},
			{"DH-E-AGENT-4", "Linux (E1)", "acceptance", "required"},
			{"DH-E-LLM-1", "claude-code", "acceptance", "required"},
			{"DH-E-LLM-1", "copilot-cli", "acceptance", "recorded"},
			{"DH-E-LLM-3", "claude-code", "acceptance", "required"},
			{"DH-E-LLM-3", "copilot-cli", "acceptance", "recorded"},
			{"DH-E-TOOLS-1", "Linux (E1)", "acceptance", "required"},
			{"DH-E-VIEW-1", "claude-code", "acceptance", "required"},
			{"DH-E-VIEW-1", "copilot-cli", "acceptance", "recorded"},
			{"DH-E-LOADER-1", "Linux (E1)", "acceptance", "required"},
			{"DH-E-SDK-1", "Linux (E1)", "acceptance", "required"},
			{"DH-E-SANDBOX-7", "Linux (E1)", "acceptance", "required"},
		},
	},
	"DH-S-18": {
		Steps: []string{"18.1", "18.2", "18.3", "18.4", "18.5", "18.6", "18.7", "18.8", "18.9", "18.10", "18.11"},
		Scenarios: []ScenarioRow{
			{"DH-E-TOOLCHAIN-2", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-TOOLCHAIN-2", "Windows", "acceptance", "required"},
			{"DH-E-HOST-4", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-HOST-4", "Windows", "acceptance", "required"},
			{"DH-E-HOST-4", "macOS", "acceptance", "required"},
			{"DH-E-EXEC-1", "Linux (E2)", "acceptance", "required"},
			{"DH-E-EXEC-10", "Linux (E2, over SSH)", "acceptance", "required"},
			{"DH-E-NATIVESYSTEM-1", "Linux (E2)", "acceptance", "required"},
			{"DH-E-NATIVESYSTEM-2", "Linux (E2)", "acceptance", "required"},
			{"DH-E-NATIVESYSTEM-3", "Linux (E2)", "acceptance", "required"},
			{"DH-E-NATIVESYSTEM-4", "Linux (E2)", "acceptance", "required"},
			{"DH-E-SANDBOX-1", "Linux (E2)", "acceptance", "required"},
			{"DH-E-FS-1", "Linux ext4", "acceptance", "required"},
			{"DH-E-FS-1", "Windows NTFS", "acceptance", "required"},
			{"DH-E-FS-1", "macOS", "acceptance", "required"},
			{"DH-E-FS-2", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-FS-2", "Windows", "acceptance", "required"},
			{"DH-E-FS-2", "macOS", "acceptance", "required"},
			{"DH-E-VIEW-5", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-RIPGREP-1", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-RIPGREP-1", "Windows", "acceptance", "required"},
			{"DH-E-RIPGREP-1", "macOS", "acceptance", "required"},
			{"DH-E-RIPGREP-2", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-RIPGREP-2", "Windows", "acceptance", "required"},
			{"DH-E-RIPGREP-2", "macOS", "acceptance", "required"},
			{"DH-E-CHOKIDAR-1", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-CHOKIDAR-1", "Windows", "acceptance", "required"},
			{"DH-E-CHOKIDAR-1", "macOS", "acceptance", "required"},
			{"DH-E-CHOKIDAR-2", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-CHOKIDAR-2", "Windows", "acceptance", "required"},
			{"DH-E-CHOKIDAR-2", "macOS", "acceptance", "required"},
			{"DH-E-CONTEXT-2", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-CONTEXT-2", "Windows", "acceptance", "required"},
			{"DH-E-STORAGE-6", "Linux (WSL)", "acceptance", "required"},
			{"DH-E-STORAGE-6", "Windows", "acceptance", "required"},
			{"DH-E-STORAGE-6", "macOS", "acceptance", "required"},
			{"DH-E-NODEFS-1", "Linux guest", "acceptance", "required"},
			{"DH-E-NODEFS-1", "Windows guest", "acceptance", "required"},
		},
	},
}
