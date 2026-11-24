package profile

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// snapshot maps every file under root to its bytes, leaving out lock files (the
// layer lock a reset takes, like every edit).
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".lock") {
			return err
		}
		data, err := os.ReadFile(p)
		files[p] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestResetSetsTheProfileLayerAside is DH-T-BOOT-8: a reset renames the profile
// layer to a unique backup name (layer.yml.bak-<unix ms>) holding its exact bytes,
// leaves the home layer, other profiles and every other file byte-identical, and
// the next launch starts from an empty profile layer.
//
//dsh:test DH-T-BOOT-8
func TestResetSetsTheProfileLayerAside(t *testing.T) {
	root := t.TempDir()
	def, err := Lookup(Web)
	if err != nil {
		t.Fatal(err)
	}
	layer := LayerPath(root, def.Name)
	const broken = "# a layer that stops start-up\n- id: logging\n  merge:\n    level: [unclosed\n"
	writeFile(t, layer, broken)
	writeFile(t, UserLayerPath(root), "- id: logging\n  merge:\n    level: warn\n")
	writeFile(t, LayerPath(root, Headless), "- id: logging\n  merge:\n    level: error\n")
	writeFile(t, filepath.Join(filepath.Dir(layer), "profile.json"), `{"extends": "web"}`)
	// An earlier backup holds the name of this clock's millisecond, so the reset
	// takes the next free one and leaves the earlier backup alone.
	const ms = int64(1767225600000)
	taken := layer + config.BackupInfix + strconv.FormatInt(ms, 10)
	writeFile(t, taken, "an earlier backup\n")
	if _, _, err := Stack(def, Sources{Home: root}); herr.Kind(err) != ReasonBadLayer {
		t.Fatalf("the broken layer should stop the stack: %v", err)
	}
	before := snapshot(t, root)

	clock := func() time.Time { return time.UnixMilli(ms) }
	backup, err := reset(context.Background(), root, def.Name, clock)
	if err != nil {
		t.Fatal(err)
	}
	if want := layer + config.BackupInfix + strconv.FormatInt(ms+1, 10); backup != want {
		t.Fatalf("backup = %s, want %s", backup, want)
	}
	after := snapshot(t, root)
	want := map[string]string{}
	for p, data := range before {
		want[p] = data
	}
	delete(want, layer)
	want[backup] = broken
	if len(after) != len(want) {
		t.Errorf("files after reset = %d, want %d: %v", len(after), len(want), after)
	}
	for p, data := range want {
		if got, ok := after[p]; !ok || got != data {
			t.Errorf("%s = %q (present %v), want %q", p, got, ok, data)
		}
	}

	// The next launch reads the missing layer as an empty one: the tree is the
	// bundles and the home layer alone.
	layers, issues, err := Stack(def, Sources{Home: root})
	if err != nil || len(issues) > 0 {
		t.Fatalf("stack after reset: %v %v", err, issues)
	}
	var withoutProfile []loader.Layer
	for _, l := range layers {
		if l.Label == ProfileLabel {
			if !l.Writable || l.Path != layer || len(l.Ops) != 0 {
				t.Errorf("profile layer after reset = %+v, want the empty writable layer", l)
			}
			continue
		}
		withoutProfile = append(withoutProfile, l)
	}
	got, _, issues := loader.Compose(layers)
	wantTree, _, _ := loader.Compose(withoutProfile)
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(wantTree)
	if len(issues) > 0 || string(a) != string(b) {
		t.Errorf("tree after reset = %s (issues %v), want %s", a, issues, b)
	}

	// Nothing left to set aside: no backup, no error, nothing created.
	if again, err := reset(context.Background(), root, def.Name, clock); again != "" || err != nil {
		t.Errorf("second reset = %q, %v; want nothing to do", again, err)
	}
	if b, err := Reset(context.Background(), root, SDK); b != "" || err != nil {
		t.Errorf("reset of a profile without a layer = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Dir(LayerPath(root, SDK))); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("resetting a profile without a layer created its directory: %v", err)
	}
	for _, name := range []string{"", ".", "..", "../web", "a/b", `a\b`} {
		if _, err := Reset(context.Background(), root, name); herr.Kind(err) != ReasonUnknown {
			t.Errorf("Reset(%q) = %v, want %s", name, err, ReasonUnknown)
		}
	}

	// The public Reset names the backup by the clock.
	writeFile(t, layer, "- id: logging\n  disabled: true\n")
	backup, err = Reset(context.Background(), root, def.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^` + regexp.QuoteMeta(layer+config.BackupInfix) + `\d{13,}$`).MatchString(backup) {
		t.Errorf("backup name %s is not layer.yml.bak-<unix ms>", backup)
	}
	if data, err := os.ReadFile(backup); err != nil || string(data) != "- id: logging\n  disabled: true\n" {
		t.Errorf("backup bytes = %q, %v", data, err)
	}
	if data, err := os.ReadFile(taken); err != nil || string(data) != "an earlier backup\n" {
		t.Errorf("earlier backup changed: %q, %v", data, err)
	}
}
