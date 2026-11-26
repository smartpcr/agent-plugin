//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// bcolBundles is the embedded bundle directory of the profile package, relative
// to this suite's directory; the optional list is read from the committed file.
const bcolBundles = "../../../internal/boot/profile"

type bcolWorld struct {
	optionalFile string
	optionalData []byte
	optional     []profile.OptionalItem

	catalogue []string
	mounted   map[string][]string
	err       error
	ran       bool

	def    profile.Definition
	layers []loader.Layer
	egress []sharing.Status
}

func bcolNames(cat *catalog.Catalog) []string {
	var names []string
	for _, it := range cat.Items() {
		names = append(names, it.Name)
	}
	return names
}

// bcolDefinitions is every profile the coverage check mounts: the five of
// DH-D-BUNDLE-1 and the other launchable built-ins.
func bcolDefinitions() []profile.Definition {
	out := profile.Definitions()
	for _, d := range profile.Builtins() {
		if !slices.ContainsFunc(out, func(o profile.Definition) bool { return o.Name == d.Name }) {
			out = append(out, d)
		}
	}
	return out
}

func (w *bcolWorld) committedOptional(rel string) error {
	w.optionalFile = filepath.Join(bcolBundles, filepath.FromSlash(rel))
	data, err := os.ReadFile(w.optionalFile)
	if err != nil {
		return err
	}
	w.optionalData = data
	items, err := profile.ParseOptional(rel, data)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("%s is empty", rel)
	}
	embedded, err := profile.Optional()
	if err != nil {
		return err
	}
	if !slices.Equal(items, embedded) {
		return fmt.Errorf("committed %s differs from the embedded optional list", rel)
	}
	w.optional = items
	mounted, err := profile.Mounted(bcolDefinitions())
	if err != nil {
		return err
	}
	w.mounted = mounted
	return nil
}

func (w *bcolWorld) shippedCovered() error {
	cat, err := builtin.Catalog()
	if err != nil {
		return err
	}
	if err := profile.Coverage(bcolNames(cat)); err != nil {
		return fmt.Errorf("shipped build not covered: %w", err)
	}
	return profile.CheckCoverage(bcolNames(cat), w.mounted, w.optional)
}

func (w *bcolWorld) plantedCatalogue(x string) error {
	planted := catalog.Item{Name: x, Component: cordis.Define(x, func(*cordis.Context, struct{}) error { return nil })}
	cat, err := catalog.BuildFor("linux", append(builtin.Sets(), []catalog.Item{planted})...)
	if err != nil {
		return err
	}
	w.catalogue = bcolNames(cat)
	if !slices.Contains(w.catalogue, x) {
		return fmt.Errorf("catalogue does not hold %s", x)
	}
	if _, ok := w.mounted[x]; ok {
		return fmt.Errorf("%s is mounted by %v; want no profile", x, w.mounted[x])
	}
	if slices.ContainsFunc(w.optional, func(it profile.OptionalItem) bool { return it.Name == x }) {
		return fmt.Errorf("%s is already on the optional list", x)
	}
	return nil
}

func (w *bcolWorld) coverageRuns() error {
	w.err = profile.CheckCoverage(w.catalogue, w.mounted, w.optional)
	w.ran = true
	return nil
}

func (w *bcolWorld) failsWith(reason string) error {
	if !w.ran {
		return fmt.Errorf("coverage check did not run")
	}
	if w.err == nil {
		return fmt.Errorf("coverage passed; want %s", reason)
	}
	if got := herr.Kind(w.err); string(got) != reason {
		return fmt.Errorf("reason = %q (%v), want %q", got, w.err, reason)
	}
	return nil
}

func (w *bcolWorld) failureNames(x string) error {
	want := x + " is mounted by no shipped layer and is not on the optional list"
	if w.err == nil || !strings.Contains(w.err.Error(), want) {
		return fmt.Errorf("err = %v, want it to contain %q", w.err, want)
	}
	return nil
}

func (w *bcolWorld) failureNamesNoOther() error {
	if n := strings.Count(w.err.Error(), "is mounted by no shipped layer"); n != 1 {
		return fmt.Errorf("err names %d unmounted components, want 1: %v", n, w.err)
	}
	if strings.Contains(w.err.Error(), "is on the optional list and mounted by") ||
		strings.Contains(w.err.Error(), "which is not in the catalogue") {
		return fmt.Errorf("err has other problems: %v", w.err)
	}
	return nil
}

// addOptional appends a row to a copy of the committed optional.yml and parses
// the result as the build would, so the list under test is the file's.
func (w *bcolWorld) addOptional(x, owner, reason string) error {
	data := slices.Clone(w.optionalData)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, fmt.Sprintf("- name: %s\n  owner: %s\n  reason: %s\n", x, owner, reason)...)
	path := filepath.Join(os.TempDir(), fmt.Sprintf("bcol-optional-%d.yml", os.Getpid()))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	defer os.Remove(path)
	read, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	items, err := profile.ParseOptional("optional.yml", read)
	if err != nil {
		return err
	}
	if len(items) != len(w.optional)+1 || items[len(items)-1].Name != x {
		return fmt.Errorf("edited list has %d rows, want %d ending in %s", len(items), len(w.optional)+1, x)
	}
	w.optional = items
	w.ran = false
	return nil
}

func (w *bcolWorld) passes() error {
	if !w.ran {
		return fmt.Errorf("coverage check did not run")
	}
	if w.err != nil {
		return fmt.Errorf("coverage failed: %v", w.err)
	}
	return nil
}

func (w *bcolWorld) defaultProfile(name string) error {
	i := slices.IndexFunc(bcolDefinitions(), func(d profile.Definition) bool { return d.Name == name })
	if i < 0 {
		return fmt.Errorf("no default profile %q", name)
	}
	w.def = bcolDefinitions()[i]
	layers, err := profile.BundleLayers(w.def)
	if err != nil {
		return err
	}
	if len(layers) == 0 {
		return fmt.Errorf("%s composes no layers", name)
	}
	for _, l := range layers {
		if l.Label == profile.UserLabel {
			return fmt.Errorf("%s bundle layers include a user layer", name)
		}
	}
	w.layers = layers
	return nil
}

func (w *bcolWorld) egressComputed() error {
	tree, _, _ := loader.Compose(w.layers)
	got, err := profile.Egress(tree)
	if err != nil {
		return fmt.Errorf("%s: %w", w.def.Name, err)
	}
	w.egress = got
	return nil
}

func (w *bcolWorld) egressEmpty() error {
	if len(w.egress) != 0 {
		return fmt.Errorf("%s: egress set %+v, want empty (NG-7)", w.def.Name, w.egress)
	}
	return nil
}

func (w *bcolWorld) routeAppears(kind, endpoint string) error {
	on, err := profile.Merge("sharing", map[string]any{"kinds": map[string]any{
		kind: map[string]any{"enabled": true, "endpoint": endpoint},
	}})
	if err != nil {
		return err
	}
	layers := append(slices.Clone(w.layers), loader.Layer{Label: profile.UserLabel, Ops: []loader.Op{on}})
	tree, _, _ := loader.Compose(layers)
	got, err := profile.Egress(tree)
	if err != nil {
		return err
	}
	if len(got) != 1 || string(got[0].Kind) != kind || got[0].Endpoint != endpoint {
		return fmt.Errorf("%s with route: egress set %+v, want only %s to %s", w.def.Name, got, kind, endpoint)
	}
	return nil
}

func InitializeScenario_dh_s_14_agent_presets_and_bundles_bundle_coverage_check_and_optional_list(ctx *godog.ScenarioContext) {
	w := &bcolWorld{}
	ctx.Step(`^the committed optional list "([^"]*)"$`, w.committedOptional)
	ctx.Step(`^the shipped build catalogue passes the coverage check$`, w.shippedCovered)
	ctx.Step(`^an in-memory catalogue of the shipped build plus component "([^"]*)" in no profile$`, w.plantedCatalogue)
	ctx.Step(`^the coverage check runs$`, w.coverageRuns)
	ctx.Step(`^it fails with reason "([^"]*)"$`, w.failsWith)
	ctx.Step(`^the failure names "([^"]*)" as mounted by no shipped layer and not on the optional list$`, w.failureNames)
	ctx.Step(`^the failure names no other unmounted component$`, w.failureNamesNoOther)
	ctx.Step(`^"([^"]*)" is added to the optional list with owner "([^"]*)" and reason "([^"]*)"$`, w.addOptional)
	ctx.Step(`^it passes$`, w.passes)

	ctx.Step(`^the default profile "([^"]*)" composed from its shipped bundles with no route configured$`, w.defaultProfile)
	ctx.Step(`^the egress set is computed$`, w.egressComputed)
	ctx.Step(`^the egress set is empty$`, w.egressEmpty)
	ctx.Step(`^configuring a "([^"]*)" route to "([^"]*)" on it puts exactly that kind in the egress set$`, w.routeAppears)
}

func TestE2E_dh_s_14_agent_presets_and_bundles_bundle_coverage_check_and_optional_list(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_14_agent_presets_and_bundles_bundle_coverage_check_and_optional_list,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_14_agent_presets_and_bundles_bundle_coverage_check_and_optional_list.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
