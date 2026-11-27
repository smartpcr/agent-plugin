package cli

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// ghostLayer inserts a permission preset table whose default preset it lacks.
const ghostLayer = "- insert:\n    - id: permissions\n      name: permission.presets\n      config:\n        defaultPreset: ghost\n"

// TC-13b, in process: `dsh config show --profile headless` prints each value with
// the layer that supplied it, the user layer's included; it mounts no tree, so
// the component trace is empty, and it writes nothing to the home. Without the
// profile layer (--defaults) the stack leaves it out, and an operation of a
// bundle that matches no entry is listed with its layer.
//
//dsh:test DH-T-BOOT-14
func TestConfigShowConstructsNothing(t *testing.T) {
	root := t.TempDir()
	temphome.UserLayer(t, root, "- id: logging\n  merge:\n    level: debug\n")
	before := homeFiles(t, root)

	r := adminRun(t, "", "--home", root, "config", "show", "--profile", "headless")
	if r.status != output.OK {
		t.Fatalf("config show: %+v", r)
	}
	for _, want := range []string{
		"profile: headless\n",
		"  logging (boot.logging)  # bundle:admin\n",
		`    config.level = "debug"  # user` + "\n",
		`    config.destination = "stderr"  # bundle:admin` + "\n",
		"  cli.headless (cli.headless)  # bundle:headless\n",
		"issues:  none\n",
		`bundles/headless.yml:18:3: layer bundle:headless: merge "human.secrets" matches no entry`,
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("config show lacks %q:\n%s", want, r.stdout)
		}
	}
	if !strings.Contains(r.stdout, ", profile, user,") {
		t.Errorf("the layers do not include the profile layer:\n%s", r.stdout)
	}
	if got := traceLine(t, r.stderr); got != "none" {
		t.Errorf("trace = %q, want none", got)
	}
	if after := homeFiles(t, root); !maps.Equal(before, after) {
		t.Errorf("config show changed the home:\nbefore %v\nafter  %v", before, after)
	}

	r = adminRun(t, "", "--home", root, "config", "show", "headless", "--defaults", "--json")
	var doc configShowDoc
	if r.status != output.OK || json.Unmarshal([]byte(r.stdout), &doc) != nil {
		t.Fatalf("config show --defaults --json: %+v", r)
	}
	if doc.Profile != "headless" || strings.Contains(strings.Join(doc.Layers, ","), ",profile,") || len(doc.Issues) != 0 || len(doc.Notes) == 0 {
		t.Errorf("config show --defaults: %+v", doc)
	}

	missing := filepath.Join(t.TempDir(), "home")
	if r := adminRun(t, "", "--home", missing, "config", "show", "--profile", "sdk"); r.status != output.OK {
		t.Fatalf("config show on a home that does not exist: %+v", r)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("config show created the home: %v", err)
	}

	for _, args := range [][]string{
		{"config", "show"},
		{"config", "show", "headless", "--profile", "sdk"},
	} {
		if r := adminRun(t, "", append([]string{"--home", root}, args...)...); r.status != output.Usage {
			t.Errorf("dsh %v: status %d, want %d: %+v", args, r.status, output.Usage, r)
		}
	}
}

// TC-13b, in process: `dsh config validate` passes every built-in profile on a
// clean home, and a layer whose default permission preset names no preset of
// its table exits 78 naming the preset at its field, once; the same layer given
// as a file argument fails alike, and nothing is constructed.
//
//dsh:test DH-T-BOOT-14
func TestConfigValidateRejectsBadConfig(t *testing.T) {
	root := t.TempDir()
	r := adminRun(t, "", "--home", root, "config", "validate")
	if r.status != output.OK || r.stdout != "valid: web, headless, sdk, sdk-minimal, acp\n" || traceLine(t, r.stderr) != "none" {
		t.Fatalf("config validate on a clean home: %+v", r)
	}

	bad := filepath.Join(t.TempDir(), "bad.yml")
	if err := os.WriteFile(bad, []byte(ghostLayer), 0o600); err != nil {
		t.Fatal(err)
	}
	r = adminRun(t, "", "--home", root, "config", "validate", "--profile", "headless", bad)
	assertGhost(t, r, 1)

	temphome.UserLayer(t, root, ghostLayer)
	r = adminRun(t, "", "--home", root, "config", "validate", "--json")
	assertGhost(t, r, 5)
	if !strings.Contains(r.stdout+r.stderr, `"config/rejected"`) {
		t.Errorf("no JSON config/rejected error: %+v", r)
	}
}

func assertGhost(t *testing.T, r run, profiles int) {
	t.Helper()
	const msg = `permissions.config.defaultPreset: default permission preset "ghost" is not a preset of the table`
	out := r.stdout + r.stderr
	if r.status != output.Config || !strings.Contains(out, msg) {
		t.Fatalf("status %d, want %d naming the ghost preset: %+v", r.status, output.Config, r)
	}
	if n := strings.Count(out, msg); n != profiles {
		t.Errorf("the ghost preset is named %d times, want once for each of %d profiles: %s", n, profiles, out)
	}
	if got := traceLine(t, r.stderr); got != "none" {
		t.Errorf("trace = %q, want none", got)
	}
}

// TC-13b with the built binary: show prints each value with its layer and an
// empty component trace without creating the home; validate exits 78 naming the
// unresolvable default permission preset.
//
//dsh:test DH-T-BOOT-14
func TestConfigCommandsProcess(t *testing.T) {
	bin := buildDsh(t)
	root := t.TempDir()
	temphome.UserLayer(t, root, "- id: logging\n  merge:\n    level: debug\n")
	before := homeFiles(t, root)

	r := dshProcess(t, bin, "", "--home", root, "config", "show", "--profile", "headless")
	if r.status != output.OK || !strings.Contains(r.stdout, `    config.level = "debug"  # user`) || traceLine(t, r.stderr) != "none" {
		t.Fatalf("config show: %+v", r)
	}
	if after := homeFiles(t, root); !maps.Equal(before, after) {
		t.Errorf("config show changed the home:\nbefore %v\nafter  %v", before, after)
	}

	temphome.UserLayer(t, root, ghostLayer)
	r = dshProcess(t, bin, "", "--home", root, "config", "validate")
	if r.status != output.Config || !strings.Contains(r.stderr, `default permission preset "ghost"`) || traceLine(t, r.stderr) != "none" {
		t.Fatalf("config validate: %+v", r)
	}
}
