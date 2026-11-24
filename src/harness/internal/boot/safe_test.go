package boot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
)

// --safe on the launch request reaches the launcher layer through the boot's own
// Facts, Layers and Compose steps: the plugins group is disabled and owned by the
// launcher layer, and without --safe the launcher layer is empty (DH-ADR-BOOT-12).
//
//dsh:test DH-T-BOOT-23
func TestSafeLaunchDisablesThePluginsGroup(t *testing.T) {
	homeDir := t.TempDir()
	def := profile.Definition{Name: "probe"}
	layer := profile.LayerPath(homeDir, def.Name)
	if err := os.MkdirAll(filepath.Dir(layer), 0o700); err != nil {
		t.Fatal(err)
	}
	text := "- insert:\n    - id: plugins\n      group: true\n      children:\n        - id: demo\n          name: plugin.entry\n"
	if err := os.WriteFile(layer, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, safe := range []bool{false, true} {
		s := &State{Options: Options{Profile: def, Request: launch.Request{
			Args: []string{"dsh"}, Cwd: homeDir, Home: homeDir, Profile: def.Name, Safe: safe,
		}}}
		ctx := context.Background()
		for _, step := range []func(context.Context, *State) error{Real{}.Facts, Real{}.Layers, Real{}.Compose} {
			if err := step(ctx, s); err != nil {
				t.Fatalf("safe=%v: %v", safe, err)
			}
		}
		if len(s.Issues) > 0 {
			t.Fatalf("safe=%v: layer issues %v", safe, s.Issues)
		}
		if s.Data.Safe != safe {
			t.Fatalf("Data.Safe = %v, want %v", s.Data.Safe, safe)
		}
		launcher := s.Layers[len(s.Layers)-1]
		want := 0
		if safe {
			want = 1
		}
		if launcher.Label != profile.LauncherLabel || len(launcher.Ops) != want {
			t.Fatalf("safe=%v: launcher layer %s holds %v", safe, launcher.Label, launcher.Ops)
		}
		var group *loader.EntryOptions
		for i := range s.Composed {
			if s.Composed[i].ID == profile.PluginsGroup {
				group = &s.Composed[i]
			}
		}
		if group == nil || group.Disabled != safe {
			t.Errorf("safe=%v: plugins group = %+v", safe, group)
		}
		if owner, _ := s.Index.Owner(profile.PluginsGroup, "disabled"); safe && owner != profile.LauncherLabel {
			t.Errorf("owner of plugins.disabled = %q, want %q", owner, profile.LauncherLabel)
		}
	}
}
