package profile

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// bundles are the bundle layers compiled into the binary (DH-ADR-BOOT-2). Their
// content is DH-D-BUNDLE-1's; each is a layer file in the harness YAML dialect.
//
//go:embed bundles/*.yml
var bundles embed.FS

// Layer labels of the stack, lowest first. A bundle layer is BundleLabel+name and an
// overlay OverlayLabel+index.
const (
	BundleLabel   = "bundle:"
	ProfileLabel  = "profile"
	UserLabel     = "user"
	OverlayLabel  = "overlay:"
	LauncherLabel = "launcher"
)

// Files this package owns under <home>/config (boot.md §6.1).
const (
	// ProfilesDir holds one directory per profile with a layer.
	ProfilesDir = "profiles"
	// LayerFile is the name of the profile layer and of the user (home) layer.
	LayerFile = "layer.yml"
)

// LayerPath returns <home>/config/profiles/<name>/layer.yml: the profile layer and
// the one writable layer of a launch.
func LayerPath(homeRoot, name string) string {
	return filepath.Join(home.New(homeRoot).Config(), ProfilesDir, name, LayerFile)
}

// UserLayerPath returns <home>/config/layer.yml: written by a person, never by dsh.
func UserLayerPath(homeRoot string) string {
	return filepath.Join(home.New(homeRoot).Config(), LayerFile)
}

// BundleNames lists the embedded bundles, sorted.
func BundleNames() []string {
	files, _ := fs.Glob(bundles, "bundles/*.yml")
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = path.Base(f[:len(f)-len(".yml")])
	}
	return names
}

// Bundle returns the embedded bundle called name as a read-only layer labelled
// BundleLabel+name. A missing or unparsable bundle is ReasonBadBundle.
func Bundle(name string) (loader.Layer, error) {
	file := "bundles/" + name + ".yml"
	data, err := bundles.ReadFile(file)
	if err != nil {
		return loader.Layer{}, herr.Wrap(ReasonBadBundle, err, "bundle "+name)
	}
	layer, issues, err := ParseLayer(BundleLabel+name, file, data)
	if err == nil && len(issues) > 0 {
		err = &loader.ParseError{Issues: issues}
	}
	if err != nil {
		return loader.Layer{}, herr.Wrap(ReasonBadBundle, err, "bundle "+name)
	}
	return layer, nil
}

// ParseLayer decodes one layer file with the codec of its extension (YAML for .yml
// and .yaml). Empty or blank data is an empty layer. Issues are the operations that
// could not be decoded; Compose skips them.
func ParseLayer(label, file string, data []byte) (loader.Layer, []loader.Issue, error) {
	config.Register()
	layer := loader.Layer{Label: label, Ops: []loader.Op{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return layer, nil, nil
	}
	doc, err := loader.ParseLayer(nil, file, data)
	if err != nil {
		return loader.Layer{}, nil, err
	}
	layer.Ops = doc.Ops
	return layer, doc.Issues, nil
}

// ReadFile reads one file; a missing file satisfies errors.Is(err, fs.ErrNotExist).
type ReadFile func(path string) ([]byte, error)

// Sources are the inputs of a launch's stack beyond the profile.
type Sources struct {
	// Home is the resolved home root.
	Home string
	// Overlays are the --layer files, lowest first.
	Overlays []string
	// Launcher are the in-memory operations of launcher flags; the top layer.
	Launcher []loader.Op
	// Read reads a layer file; nil is os.ReadFile.
	Read ReadFile
}

// Stack builds the ordered layers of one launch of def: its bundles, the profile
// layer (the one writable layer), the user layer, the overlays and the launcher
// layer. A missing profile or user layer file is an empty layer; a missing overlay
// or a file that does not parse is ReasonBadLayer. The issues are the skipped
// operations of the files read.
func Stack(def Definition, src Sources) ([]loader.Layer, []loader.Issue, error) {
	read := src.Read
	if read == nil {
		read = os.ReadFile
	}
	layers, err := BundleLayers(def)
	if err != nil {
		return nil, nil, err
	}
	var issues []loader.Issue
	file := func(label, p string, optional, writable bool) error {
		data, err := read(p)
		if err != nil && !(optional && errors.Is(err, fs.ErrNotExist)) {
			return herr.Wrap(ReasonBadLayer, err, "read layer "+p)
		}
		layer, found, err := ParseLayer(label, p, data)
		if err != nil {
			return herr.Wrap(ReasonBadLayer, err, "layer "+p)
		}
		layer.Path, layer.Writable = p, writable
		layers = append(layers, layer)
		issues = append(issues, found...)
		return nil
	}
	if err := file(ProfileLabel, LayerPath(src.Home, def.Name), true, true); err != nil {
		return nil, nil, err
	}
	if err := file(UserLabel, UserLayerPath(src.Home), true, false); err != nil {
		return nil, nil, err
	}
	for i, p := range src.Overlays {
		if err := file(fmt.Sprintf("%s%d", OverlayLabel, i+1), p, false, false); err != nil {
			return nil, nil, err
		}
	}
	launcher := loader.Layer{Label: LauncherLabel, Ops: append([]loader.Op{}, src.Launcher...)}
	return append(layers, launcher), issues, nil
}

// BundleLayers returns the embedded bundle layers of def, in its order.
func BundleLayers(def Definition) ([]loader.Layer, error) {
	layers := make([]loader.Layer, 0, len(def.Bundles))
	for _, name := range def.Bundles {
		layer, err := Bundle(name)
		if err != nil {
			return nil, err
		}
		layers = append(layers, layer)
	}
	return layers, nil
}

// Compose composes layers for def and applies the static Required rule (boot.md
// §6.2, DH-ADR-BOOT-6) with no evaluator: see ComposeWith.
func Compose(def Definition, layers []loader.Layer) ([]loader.EntryOptions, []loader.Issue, error) {
	return ComposeWith(def, layers, nil)
}

// ComposeWith composes layers for def and applies the static Required rule by
// running config.Check with CheckOptions.Required = def.Required, so a launch, a
// reload and this composition share one definition of the rule: a required id is
// written with loader.Separator from its enclosing groups, only a group's children
// are visited, and an entry is disabled by its literal flag or by a disabled
// expression that eval yields true for, fails, yields no boolean or cannot run (a
// nil eval). A failure is ReasonMissingRequired naming every id the tree lacks or
// disables, wrapping the check's issues for them; the tree and the other issues of
// the check are returned with it.
func ComposeWith(def Definition, layers []loader.Layer, eval loader.Evaluator) ([]loader.EntryOptions, []loader.Issue, error) {
	r, issues, err := check(def, layers, config.CheckOptions{Evaluator: eval})
	return r.Tree, issues, err
}

// Preview composes and checks layers for def without starting anything: the preview
// `dsh config show` prints (DH-D-BOOT-6, DH-D-BUNDLE-2). The report holds the tree,
// the ownership index naming the layer that supplied each entry and each value
// (Report.Entries, Report.Explain) and every issue, the Required ones included;
// opts.Required is def.Required. The error is ComposeWith's.
func Preview(def Definition, layers []loader.Layer, opts config.CheckOptions) (*config.Report, error) {
	r, _, err := check(def, layers, opts)
	return r, err
}

// check runs config.Preview with def's Required list and splits its issues: those
// for a required id the tree lacks or disables fail the composition; the others are
// returned.
func check(def Definition, layers []loader.Layer, opts config.CheckOptions) (*config.Report, []loader.Issue, error) {
	opts.Required = def.Required
	r := config.Preview(layers, opts)
	required := map[string]bool{}
	for _, id := range def.Required {
		required[id] = true
	}
	var issues, failed []loader.Issue
	var missing, off []string
	for _, issue := range r.Issues {
		if !strings.HasPrefix(issue.Message, requiredMessage) {
			issues = append(issues, issue)
			continue
		}
		switch id := strings.TrimSuffix(issue.Path, ".disabled"); {
		case required[issue.Path]:
			missing = append(missing, issue.Path)
		case id != issue.Path && required[id]:
			off = append(off, id)
		default:
			issues = append(issues, issue)
			continue
		}
		failed = append(failed, issue)
	}
	if len(failed) == 0 {
		return r, issues, nil
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "lacks "+strings.Join(missing, ", "))
	}
	if len(off) > 0 {
		parts = append(parts, "disables "+strings.Join(off, ", "))
	}
	return r, issues, herr.Wrap(ReasonMissingRequired, &loader.ParseError{Issues: failed},
		fmt.Sprintf("profile %s: the composed tree %s, which its Required list names", def.Name, strings.Join(parts, " and ")))
}

// requiredMessage begins the message of every issue config.Check reports for
// CheckOptions.Required.
const requiredMessage = "is a required entry"

// Insert is the layer operation that appends entries to the root (group "") or to
// the group with the tree id group. An entry whose id its siblings already hold is
// skipped and reported.
func Insert(group string, entries ...loader.EntryOptions) loader.Op {
	out := make([]loader.EntryOptions, len(entries))
	for i, e := range entries {
		out[i] = e.Clone()
	}
	return loader.Op{Kind: loader.OpInsert, ID: group, Entries: out}
}

// Merge is the layer operation that merges patch into the config of the entry with
// the tree id id, key by key as a JSON merge patch: a map merges into a map, null
// deletes a key and any other value replaces it. Each value it sets is owned by the
// layer that holds the operation.
func Merge(id string, patch map[string]any) (loader.Op, error) {
	raw, err := json.Marshal(patch)
	if err != nil {
		return loader.Op{}, fmt.Errorf("merge %q: %w", id, err)
	}
	if patch == nil {
		raw = []byte("{}")
	}
	return loader.Op{Kind: loader.OpMerge, ID: id, Patch: raw}, nil
}

// Remove is the layer operation that removes the entry with the tree id id, with its
// children, and the ownership records of both.
func Remove(id string) loader.Op { return loader.Op{Kind: loader.OpRemove, ID: id} }
