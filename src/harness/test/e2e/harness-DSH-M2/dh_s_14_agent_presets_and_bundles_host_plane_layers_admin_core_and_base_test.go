//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// hplGoldenPath is the committed golden of TC-08g, relative to this package.
var hplGoldenPath = filepath.Join("..", "..", "..", "internal", "boot", "profile", "testdata", "composed.golden")

const hplHome = "hpl-home"

type hplState struct {
	// layer operations scenario
	def    profile.Definition
	files  map[string]string
	report *config.Report

	// admin scenario
	admin  profile.Definition
	layers []loader.Layer
	tree   []loader.EntryOptions
	issues []loader.Issue
	err    error
}

// ---- Layer operations (TC-08a) ----

func (s *hplState) bundlesStacked(a, b, c string) error {
	embedded := profile.BundleNames()
	for _, n := range []string{a, b, c} {
		if !slices.Contains(embedded, n) {
			return fmt.Errorf("bundle %s is not embedded (embedded: %v)", n, embedded)
		}
	}
	s.def = profile.Definition{Name: "hpl-ops", Bundles: []string{a, b, c}}
	return nil
}

func (s *hplState) userLayerRemovesAndMerges(removed, level, merged string) error {
	s.files = map[string]string{
		profile.UserLayerPath(hplHome): fmt.Sprintf("- id: %s\n  remove: true\n- id: %s\n  merge:\n    level: %s\n", removed, merged, level),
	}
	return nil
}

func (s *hplState) stackedAndPreviewed() error {
	read := func(p string) ([]byte, error) {
		if data, ok := s.files[p]; ok {
			return []byte(data), nil
		}
		return nil, fs.ErrNotExist
	}
	layers, issues, err := profile.Stack(s.def, profile.Sources{Home: hplHome, Read: read})
	if err != nil {
		return fmt.Errorf("stack: %w", err)
	}
	if len(issues) > 0 {
		return fmt.Errorf("stack issues: %v", issues)
	}
	var labels []string
	for _, l := range layers {
		labels = append(labels, l.Label)
	}
	want := []string{"bundle:admin", "bundle:core", "bundle:base", profile.ProfileLabel, profile.UserLabel, profile.LauncherLabel}
	if !slices.Equal(labels, want) {
		return fmt.Errorf("stack labels = %v, want %v", labels, want)
	}
	cat, err := builtin.Catalog()
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	r, err := profile.Preview(s.def, layers, config.CheckOptions{Catalog: cat})
	if err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	s.report = r
	return nil
}

func (s *hplState) previewNoIssue() error {
	if len(s.report.Issues) > 0 {
		return fmt.Errorf("preview issues: %v", s.report.Issues)
	}
	return nil
}

func (s *hplState) treeHolds(a, b, c string) error {
	var ids []string
	for _, e := range s.report.Tree {
		ids = append(ids, e.ID)
	}
	if want := []string{a, b, c}; !slices.Equal(ids, want) {
		return fmt.Errorf("tree = %v, want %v", ids, want)
	}
	return nil
}

func (s *hplState) configMerged(id, destination, level string) error {
	for _, e := range s.report.Tree {
		if e.ID != id {
			continue
		}
		var got map[string]any
		if err := json.Unmarshal(e.Config, &got); err != nil {
			return fmt.Errorf("%s config %s: %w", id, e.Config, err)
		}
		want := map[string]any{"destination": destination, "level": level}
		if len(got) != len(want) || got["destination"] != destination || got["level"] != level {
			return fmt.Errorf("%s config = %v, want %v", id, got, want)
		}
		return nil
	}
	return fmt.Errorf("no entry %s", id)
}

func (s *hplState) fieldSource(id, field, layer string) error {
	ex, err := s.report.Explain(id, field)
	if err != nil {
		return err
	}
	if ex.Layer != layer {
		return fmt.Errorf("%s %s supplied by %q, want %q", id, field, ex.Layer, layer)
	}
	for _, e := range s.report.Entries() {
		// Fields lists only values a layer above the inserting one supplied.
		if e.ID == id && e.Layer != layer && !slices.Contains(e.Fields, config.FieldOwner{Field: field, Layer: layer}) {
			return fmt.Errorf("preview entry %s fields = %v, want %s by %s", id, e.Fields, field, layer)
		}
	}
	return nil
}

func (s *hplState) entrySource(id, layer string) error {
	ex, err := s.report.Explain(id, "")
	if err != nil {
		return err
	}
	if ex.Layer != layer {
		return fmt.Errorf("entry %s supplied by %q, want %q", id, ex.Layer, layer)
	}
	for _, e := range s.report.Entries() {
		if e.ID == id && e.Layer != layer {
			return fmt.Errorf("preview entry %s layer = %q, want %q", id, e.Layer, layer)
		}
	}
	return nil
}

func (s *hplState) removedHasNoSource(id string) error {
	if ex, err := s.report.Explain(id, ""); err == nil {
		return fmt.Errorf("removed entry %s still explained by %q", id, ex.Layer)
	}
	if owner, ok := s.report.Index.Owner(id, ""); ok {
		return fmt.Errorf("removed entry %s still owned by %q", id, owner)
	}
	for _, e := range s.report.Entries() {
		if e.ID == id {
			return fmt.Errorf("removed entry %s still previewed", id)
		}
	}
	return nil
}

// ---- Admin profile composition (TC-08g, INV-10) ----

func (s *hplState) adminDefinition() error {
	for _, d := range profile.Definitions() {
		if d.Name == profile.Admin {
			s.admin = d
		}
	}
	if s.admin.Name == "" {
		return errors.New("no admin definition")
	}
	if !slices.Equal(s.admin.Bundles, []string{"admin"}) {
		return fmt.Errorf("admin bundles = %v, want [admin]", s.admin.Bundles)
	}
	layers, err := profile.BundleLayers(s.admin)
	if err != nil {
		return err
	}
	s.layers = layers
	return nil
}

func (s *hplState) adminComposes() error {
	s.tree, s.issues, s.err = profile.Compose(s.admin, s.layers)
	return nil
}

func (s *hplState) noFailureNoIssue() error {
	if s.err != nil {
		return fmt.Errorf("compose: %w", s.err)
	}
	if len(s.issues) > 0 {
		return fmt.Errorf("compose issues: %v", s.issues)
	}
	return nil
}

// hplGoldenSection returns the tree composed.golden records for name: the lines
// under "# <name>" up to the next profile header, comment lines dropped.
func hplGoldenSection(name string) (string, error) {
	data, err := os.ReadFile(hplGoldenPath)
	if err != nil {
		return "", fmt.Errorf("read committed golden: %w", err)
	}
	headers := map[string]bool{}
	for _, d := range append(profile.Definitions(), profile.Builtins()...) {
		headers["# "+d.Name] = true
	}
	var out []string
	in, found := false, false
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if headers[line] {
			if in {
				break
			}
			in = line == "# "+name
			found = found || in
			continue
		}
		if in && !strings.HasPrefix(line, "# ") {
			out = append(out, line)
		}
	}
	if !found {
		return "", fmt.Errorf("composed.golden has no section %s", name)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n"), nil
}

func (s *hplState) adminEqualsGolden() error {
	want, err := hplGoldenSection(profile.Admin)
	if err != nil {
		return err
	}
	got, err := json.MarshalIndent(s.tree, "", "  ")
	if err != nil {
		return err
	}
	if string(got) != want {
		return fmt.Errorf("admin differs from its section of %s:\n%s", hplGoldenPath, golden.Diff(want, string(got)))
	}
	return nil
}

func (s *hplState) adminNotEmpty() error {
	if len(s.tree) == 0 {
		return errors.New("admin composes to nothing")
	}
	return nil
}

func hplWalk(entries []loader.EntryOptions, fn func(loader.EntryOptions)) {
	for _, e := range entries {
		fn(e)
		hplWalk(e.Children, fn)
	}
}

func (s *hplState) adminActsOnNothing() error {
	// Carriers are every Required id of a launchable profile beyond the spine.
	carriers := map[string]bool{"wire.acp": true}
	for _, d := range append(profile.Definitions(), profile.Builtins()...) {
		for _, id := range d.Required {
			if !slices.Contains(profile.Spine, id) {
				carriers[id] = true
			}
		}
	}
	acting := []string{"agent.", "llm.", "wire.", "cli.", "host.web", "host.listen", "plugin.", "boot.plugins", "subagent.", "ui."}
	var bad []string
	hplWalk(s.tree, func(e loader.EntryOptions) {
		if carriers[e.Name] || carriers[e.ID] || slices.ContainsFunc(acting, func(p string) bool {
			return strings.HasPrefix(e.Name, p) || strings.HasPrefix(e.ID, p)
		}) {
			bad = append(bad, e.ID+" ("+e.Name+")")
		}
	})
	if len(bad) > 0 {
		return fmt.Errorf("admin mounts %v: no agent, model adapter, carrier or plugin (INV-10)", bad)
	}
	return nil
}

func InitializeScenario_dh_s_14_agent_presets_and_bundles_host_plane_layers_admin_core_and_base(ctx *godog.ScenarioContext) {
	s := &hplState{}
	ctx.Step(`^the embedded bundles "([^"]*)", "([^"]*)" and "([^"]*)" stacked in that order$`, s.bundlesStacked)
	ctx.Step(`^a user layer file that removes "([^"]*)" and merges level "([^"]*)" into the "([^"]*)" config$`, s.userLayerRemovesAndMerges)
	ctx.Step(`^the layers are stacked and previewed$`, s.stackedAndPreviewed)
	ctx.Step(`^the preview reports no issue$`, s.previewNoIssue)
	ctx.Step(`^the composed tree holds "([^"]*)", "([^"]*)" and "([^"]*)" in that order$`, s.treeHolds)
	ctx.Step(`^the "([^"]*)" config is merged key-wise to destination "([^"]*)" and level "([^"]*)"$`, s.configMerged)
	ctx.Step(`^the source of "([^"]*)" field "([^"]*)" is layer "([^"]*)"$`, s.fieldSource)
	ctx.Step(`^the source of entry "([^"]*)" is layer "([^"]*)"$`, s.entrySource)
	ctx.Step(`^the removed entry "([^"]*)" has no recorded source layer$`, s.removedHasNoSource)

	ctx.Step(`^the committed admin definition and its embedded bundles$`, s.adminDefinition)
	ctx.Step(`^the admin profile composes$`, s.adminComposes)
	ctx.Step(`^the composition reports no failure and no issue$`, s.noFailureNoIssue)
	ctx.Step(`^the composed admin tree equals its section of the committed composed\.golden$`, s.adminEqualsGolden)
	ctx.Step(`^the composed admin tree is not empty$`, s.adminNotEmpty)
	ctx.Step(`^the composed admin tree holds no agent, model adapter, carrier or plugin component$`, s.adminActsOnNothing)
}

func TestE2E_dh_s_14_agent_presets_and_bundles_host_plane_layers_admin_core_and_base(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_14_agent_presets_and_bundles_host_plane_layers_admin_core_and_base,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_14_agent_presets_and_bundles_host_plane_layers_admin_core_and_base.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
