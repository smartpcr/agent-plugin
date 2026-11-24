//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// pdbGoldenPath is the committed golden of TC-08g, relative to this package.
var pdbGoldenPath = filepath.Join("..", "..", "..", "internal", "boot", "profile", "testdata", "composed.golden")

type pdbState struct {
	// golden scenario
	defs     []profile.Definition
	bundles  map[string][]loader.Layer
	rendered string

	// required scenario
	def     profile.Definition
	carrier string
	layers  []loader.Layer
	err     error
}

// ---- Composed profiles match the golden (TC-08g) ----

// renderedDefinitions are the five definitions of DH-D-BUNDLE-1 followed by the
// other launchable profiles, each once: the set composed.golden records.
func renderedDefinitions() []profile.Definition {
	out := profile.Definitions()
	for _, d := range profile.Builtins() {
		if !slices.ContainsFunc(out, func(o profile.Definition) bool { return o.Name == d.Name }) {
			out = append(out, d)
		}
	}
	return out
}

func (s *pdbState) fiveDefinitionsAndBundles() error {
	s.defs = renderedDefinitions()
	s.bundles = map[string][]loader.Layer{}
	embedded := profile.BundleNames()
	for _, d := range s.defs {
		for _, name := range d.Bundles {
			if !slices.Contains(embedded, name) {
				return fmt.Errorf("%s: bundle %s is not embedded (embedded: %v)", d.Name, name, embedded)
			}
		}
		layers, err := profile.BundleLayers(d)
		if err != nil {
			return fmt.Errorf("%s: bundle layers: %w", d.Name, err)
		}
		if len(layers) != len(d.Bundles) {
			return fmt.Errorf("%s: %d bundle layers for bundles %v", d.Name, len(layers), d.Bundles)
		}
		for i, l := range layers {
			if want := profile.BundleLabel + d.Bundles[i]; l.Label != want {
				return fmt.Errorf("%s: layer %d is %q, want %q", d.Name, i, l.Label, want)
			}
		}
		s.bundles[d.Name] = layers
	}
	return nil
}

func (s *pdbState) composerRendersEachProfile() error {
	var out bytes.Buffer
	for _, d := range s.defs {
		tree, issues, err := profile.Compose(d, s.bundles[d.Name])
		// The spine and web carriers are later stages' entries, so a Required miss
		// is expected and recorded by the header; any other failure is not.
		if err != nil && herr.Kind(err) != profile.ReasonMissingRequired {
			return fmt.Errorf("%s: compose: %w", d.Name, err)
		}
		var skipped []string
		for _, issue := range issues {
			if !strings.HasSuffix(issue.Message, " matches no entry") {
				return fmt.Errorf("%s: unexpected compose issue: %s", d.Name, issue.Error())
			}
			skipped = append(skipped, issue.Error())
		}
		b, err := json.MarshalIndent(tree, "", "  ")
		if err != nil {
			return fmt.Errorf("%s: marshal: %w", d.Name, err)
		}
		out.WriteString("# " + d.Name + "\n")
		if len(d.Required) > 0 {
			out.WriteString("# required: " + strings.Join(d.Required, ", ") + "\n")
		}
		for _, sk := range skipped {
			out.WriteString("# skipped, target not built: " + sk + "\n")
		}
		out.Write(b)
		out.WriteString("\n")
	}
	s.rendered = out.String()
	return nil
}

func (s *pdbState) definitionsInOrder() error {
	var names []string
	for _, d := range profile.Definitions() {
		names = append(names, d.Name)
	}
	want := []string{profile.Admin, profile.Core, profile.Web, profile.Headless, profile.SDK}
	if !slices.Equal(names, want) {
		return fmt.Errorf("definitions = %v, want %v", names, want)
	}
	return nil
}

func (s *pdbState) renderedEqualsGolden() error {
	want, err := os.ReadFile(pdbGoldenPath)
	if err != nil {
		return fmt.Errorf("read committed golden: %w", err)
	}
	if s.rendered == "" {
		return errors.New("nothing was rendered")
	}
	if string(want) != s.rendered {
		return fmt.Errorf("rendered profiles differ from %s:\n%s", pdbGoldenPath, golden.Diff(string(want), s.rendered))
	}
	return nil
}

// ---- Required component missing fails composition (TC-08a) ----

func (s *pdbState) headlessDefinition() error {
	d, err := profile.Lookup(profile.Headless)
	if err != nil {
		return err
	}
	if len(d.Required) == 0 {
		return errors.New("headless has no Required list")
	}
	s.def = d
	s.carrier = d.Required[len(d.Required)-1]
	if s.carrier != "cli.headless" {
		return fmt.Errorf("headless carrier = %q, want cli.headless", s.carrier)
	}
	return nil
}

func (s *pdbState) headlessBundleLackingCarrier() error {
	var entries []loader.EntryOptions
	for _, id := range s.def.Required {
		if id == s.carrier {
			continue
		}
		entries = append(entries, loader.EntryOptions{ID: id, Name: id})
	}
	s.layers = []loader.Layer{{
		Label: profile.BundleLabel + profile.Headless,
		Ops:   []loader.Op{{Kind: loader.OpInsert, Entries: entries}},
	}}
	return nil
}

func (s *pdbState) compositionRuns() error {
	_, _, s.err = profile.Compose(s.def, s.layers)
	return nil
}

func (s *pdbState) compositionFailsWith(reason string) error {
	if s.err == nil {
		return errors.New("composition succeeded, want failure")
	}
	if got := herr.Kind(s.err); string(got) != reason {
		return fmt.Errorf("reason = %q, want %q (err: %v)", got, reason, s.err)
	}
	return nil
}

func (s *pdbState) failureNamesCarrier() error {
	if !strings.Contains(s.err.Error(), "lacks "+s.carrier) {
		return fmt.Errorf("error %q does not name missing %s", s.err, s.carrier)
	}
	return nil
}

func (s *pdbState) failureNamesNoOther() error {
	msg := s.err.Error()
	head, _, _ := strings.Cut(msg, ", which its Required list names")
	for _, id := range s.def.Required {
		if id != s.carrier && strings.Contains(head, id) {
			return fmt.Errorf("error %q names present entry %s", msg, id)
		}
	}
	return nil
}

func InitializeScenario_dh_s_14_agent_presets_and_bundles_profile_definitions_and_bundle_membership(ctx *godog.ScenarioContext) {
	s := &pdbState{}
	ctx.Step(`^the five profile definitions and their embedded bundles$`, s.fiveDefinitionsAndBundles)
	ctx.Step(`^the composer renders each profile$`, s.composerRendersEachProfile)
	ctx.Step(`^the definitions are admin, core, web, headless and sdk in that order$`, s.definitionsInOrder)
	ctx.Step(`^the rendered output equals the committed composed\.golden$`, s.renderedEqualsGolden)

	ctx.Step(`^the headless profile definition$`, s.headlessDefinition)
	ctx.Step(`^an in-memory headless bundle holding every Required entry except the carrier$`, s.headlessBundleLackingCarrier)
	ctx.Step(`^composition runs$`, s.compositionRuns)
	ctx.Step(`^composition fails with reason "([^"]*)"$`, s.compositionFailsWith)
	ctx.Step(`^the failure names the missing carrier component$`, s.failureNamesCarrier)
	ctx.Step(`^the failure names no other Required entry$`, s.failureNamesNoOther)
}

func TestE2E_dh_s_14_agent_presets_and_bundles_profile_definitions_and_bundle_membership(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_14_agent_presets_and_bundles_profile_definitions_and_bundle_membership,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_14_agent_presets_and_bundles_profile_definitions_and_bundle_membership.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
