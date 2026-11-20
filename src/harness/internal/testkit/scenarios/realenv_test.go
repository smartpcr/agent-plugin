//go:build scenario

package scenarios

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// need returns the run-time input name, which an operator of the DH-S-18 run sets
// to describe the real environment. A missing input fails the scenario: a scenario
// that cannot run never skips (DH-O-15).
func need(t *testing.T, name, what string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Fatalf("%s is not set: %s", name, what)
	}
	return v
}

// items splits a list input on ";" and drops empty items.
func items(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ";") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// expand replaces each {key} of template with vars[key].
func expand(template string, vars map[string]string) string {
	for k, v := range vars {
		template = strings.ReplaceAll(template, "{"+k+"}", v)
	}
	return template
}

// shell runs command line through the platform shell (sh -c, or cmd /C on
// Windows) and returns its combined output.
func shell(t *testing.T, line string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sh", "-c", line)
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(t.Context(), "cmd", "/C", line)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mustShell is shell that fails the scenario on a non-zero exit.
func mustShell(t *testing.T, line string) string {
	t.Helper()
	out, err := shell(t, line)
	if err != nil {
		t.Fatalf("%s: %v\n%s", line, err, out)
	}
	return out
}
