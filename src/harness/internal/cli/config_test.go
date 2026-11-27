package cli

import (
	"bytes"
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

// TC-13b, in process: validate rejects an invalid or duplicate operation of a
// user or overlay layer with status 78, naming it; only a built-in bundle's
// operation on an entry the profile leaves out is a note.
//
//dsh:test DH-T-BOOT-14
func TestConfigValidateRejectsLayerFaults(t *testing.T) {
	root := t.TempDir()
	cases := []struct{ name, file, text, want string }{
		{"bundle-again", "", "", `insert: entry "cli.headless" already exists`},
		{"duplicate", "dup.yml", "- insert:\n    - id: cli.headless\n      name: cli.headless\n", `insert: entry "cli.headless" already exists`},
		{"malformed", "malformed.yml", "- id: logging\n  merge: 5\n", "malformed.yml:"},
		{"unmatched", "u.yml", "- id: nobody\n  merge:\n    level: debug\n", `merge "nobody" matches no entry`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := filepath.Join("..", "boot", "profile", "bundles", "headless.yml")
			if c.file != "" {
				file = filepath.Join(t.TempDir(), c.file)
				if err := os.WriteFile(file, []byte(c.text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			r := adminRun(t, "", "--home", root, "config", "validate", "--profile", "headless", file)
			if r.status != output.Config || !strings.Contains(r.stderr, c.want) || strings.Contains(r.stdout, "valid") {
				t.Fatalf("status %d, want %d naming %q: %+v", r.status, output.Config, c.want, r)
			}
		})
	}
}

// TC-13b, in process: show keeps every composition-affecting value of an entry
// with the layer that supplied it (an expression that disables it in its
// source form, inject, isolate, intercept), in text and JSON, and --format yaml
// prints the tree as one loadable layer with the layers as comments.
//
//dsh:test DH-T-BOOT-14
func TestConfigShowKeepsEveryValue(t *testing.T) {
	root := t.TempDir()
	temphome.UserLayer(t, root, "- id: logging\n  disabled: !expr 'env.QUIET == \"1\"'\n"+
		"- id: session.core\n  inject: [session.store]\n  isolate:\n    util.egress: true\n  intercept:\n    util.egress:\n      mode: deny\n")

	r := adminRun(t, "", "--home", root, "config", "show", "headless")
	for _, want := range []string{
		`    disabled = !expr env.QUIET == "1"  # user` + "\n",
		"    inject = [session.store]  # user\n",
		"    isolate.util.egress = true  # user\n",
		`    intercept.util.egress = {"mode":"deny"}  # user` + "\n",
	} {
		if r.status != output.OK || !strings.Contains(r.stdout, want) {
			t.Errorf("config show lacks %q: %+v", want, r)
		}
	}

	r = adminRun(t, "", "--home", root, "config", "show", "headless", "--format", "json")
	var doc configShowDoc
	if r.status != output.OK || json.Unmarshal([]byte(r.stdout), &doc) != nil {
		t.Fatalf("config show --format json: %+v", r)
	}
	seen := 0
	for _, e := range doc.Entries {
		switch e.ID {
		case "logging":
			seen++
			if e.DisabledExpr != `env.QUIET == "1"` || e.Disabled {
				t.Errorf("logging = %+v", e)
			}
		case "session.core":
			seen++
			var intercept bytes.Buffer
			_ = json.Compact(&intercept, e.Intercept["util.egress"])
			if len(e.Inject) != 1 || e.Isolate["util.egress"] != "true" || intercept.String() != `{"mode":"deny"}` {
				t.Errorf("session.core = %+v", e)
			}
		}
	}
	if seen != 2 {
		t.Errorf("the JSON lacks logging or session.core: %s", r.stdout)
	}

	r = adminRun(t, "", "--home", root, "config", "show", "headless", "--format", "yaml")
	for _, want := range []string{
		"- insert:\n    - id: logging # bundle:admin\n",
		`      disabled: !expr env.QUIET == "1" # user` + "\n",
		"      inject: # user\n        - session.store\n",
	} {
		if r.status != output.OK || !strings.Contains(r.stdout, want) {
			t.Errorf("config show --format yaml lacks %q: %+v", want, r)
		}
	}
	if strings.Contains(r.stdout, "matches no entry") || !strings.Contains(r.stderr, "dsh: note: ") || traceLine(t, r.stderr) != "none" {
		t.Errorf("the dump mixes in the notes, or a trace: %+v", r)
	}
	if r := adminRun(t, "", "--home", root, "config", "show", "headless", "--format", "toml"); r.status != output.Usage {
		t.Errorf("--format toml: %+v", r)
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
