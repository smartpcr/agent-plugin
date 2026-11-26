package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/yamlx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Error codes of the coverage check and the egress set (DH-D-BUNDLE-6).
const (
	// ReasonUncovered: a catalogue component is neither mounted by a shipped layer
	// nor on the optional list, is on both, or the optional list names a component
	// the catalogue does not have.
	ReasonUncovered herr.Reason = "profile/uncovered"
	// ReasonBadOptional: the optional list does not parse, or a row lacks its name,
	// owner or reason, or repeats a name; a build defect the tests keep out of a
	// release.
	ReasonBadOptional herr.Reason = "profile/bad-optional"
	// ReasonBadSharing: a composed host.sharing entry's config does not decode.
	ReasonBadSharing herr.Reason = "profile/bad-sharing"
)

// OptionalName is the embedded optional list, bundles/optional.yml: catalogue
// components no shipped layer mounts, each with an owner and a reason
// (bundle-preset.md §6.2, DH-ADR-BUNDLE-10). It is not a bundle.
const OptionalName = "optional"

// presetName is the catalogue name of an agent preset declaration entry
// (agentpreset.DeclarationName), whose config's children are preset children.
const presetName = "agent.preset"

// sharingName is the catalogue name of the sharing gate's entry (sharing.Name).
const sharingName = sharing.Name

// OptionalItem is one row of the optional list.
type OptionalItem struct {
	// Name is the catalogue name of the component.
	Name string `json:"name"`
	// Owner is the area that owns it (an area key of docs/harness/design.md).
	Owner string `json:"owner"`
	// Reason is why it is compiled in but mounted by no shipped layer.
	Reason string `json:"reason"`
}

// Optional returns the embedded optional list, in file order.
func Optional() ([]OptionalItem, error) {
	file := "bundles/" + OptionalName + ".yml"
	data, err := bundles.ReadFile(file)
	if err != nil {
		return nil, herr.Wrap(ReasonBadOptional, err, "optional list")
	}
	return ParseOptional(file, data)
}

// ParseOptional decodes an optional list: a YAML sequence of rows, each with a
// non-empty name, owner and reason and no other key, no name twice. Empty or blank
// data is an empty list.
func ParseOptional(file string, data []byte) ([]OptionalItem, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	doc, err := yamlx.Parse(file, data)
	if err != nil {
		return nil, herr.Wrap(ReasonBadOptional, err, "optional list")
	}
	raw, err := json.Marshal(doc.Value().Plain())
	if err != nil {
		return nil, herr.Wrap(ReasonBadOptional, err, "optional list "+file)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var items []OptionalItem
	if err := dec.Decode(&items); err != nil {
		return nil, herr.Wrap(ReasonBadOptional, err, "optional list "+file)
	}
	seen := map[string]bool{}
	for i, it := range items {
		switch {
		case strings.TrimSpace(it.Name) == "":
			return nil, herr.Newf(ReasonBadOptional, "optional list %s: row %d has no name", file, i+1)
		case strings.TrimSpace(it.Owner) == "":
			return nil, herr.Newf(ReasonBadOptional, "optional list %s: %s has no owner", file, it.Name)
		case strings.TrimSpace(it.Reason) == "":
			return nil, herr.Newf(ReasonBadOptional, "optional list %s: %s has no reason", file, it.Name)
		case seen[it.Name]:
			return nil, herr.Newf(ReasonBadOptional, "optional list %s: %s is listed twice", file, it.Name)
		}
		seen[it.Name] = true
	}
	return items, nil
}

// Mounted maps each component name the definitions mount to where: "<profile>" for
// an entry of the profile's composed bundle layers, enabled or disabled, at any
// depth, and "<profile> preset <id>" for a child of an agent.preset declaration
// entry. Only the embedded bundles are composed (no home, overlay or launcher
// layer), with the loader's pure composition and no Required rule. The places of
// a name are sorted and once each.
func Mounted(defs []Definition) (map[string][]string, error) {
	out := map[string][]string{}
	for _, def := range defs {
		layers, err := BundleLayers(def)
		if err != nil {
			return nil, err
		}
		tree, _, _ := loader.Compose(layers)
		if err := mounts(out, def.Name, tree); err != nil {
			return nil, herr.Wrap(ReasonBadBundle, err, "coverage")
		}
	}
	for name := range out {
		slices.Sort(out[name])
	}
	return out, nil
}

// mounts adds to out the components tree, composed for profile, mounts.
func mounts(out map[string][]string, profile string, tree []loader.EntryOptions) error {
	add := func(name, where string) {
		if name != "" && !slices.Contains(out[name], where) {
			out[name] = append(out[name], where)
		}
	}
	for _, e := range tree {
		if !e.Group {
			add(e.Name, profile)
		}
		if e.Name == presetName {
			id, children, err := presetChildren(e)
			if err != nil {
				return fmt.Errorf("profile %s: entry %s: %w", profile, e.ID, err)
			}
			for _, c := range children {
				add(c, profile+" preset "+id)
			}
		}
		if err := mounts(out, profile, e.Children); err != nil {
			return err
		}
	}
	return nil
}

// presetChildren returns the declaration id of an agent.preset entry and the
// component names of its own children: a child's component, or its map key
// without an "@<instance>" suffix (agentpreset.Child). A null child is a removal
// and names nothing.
func presetChildren(e loader.EntryOptions) (string, []string, error) {
	if len(e.Config) == 0 {
		return e.ID, nil, nil
	}
	var decl struct {
		ID       string `json:"id"`
		Children map[string]*struct {
			Component string `json:"component"`
		} `json:"children"`
	}
	if err := json.Unmarshal(e.Config, &decl); err != nil {
		return "", nil, fmt.Errorf("preset declaration: %w", err)
	}
	id := decl.ID
	if id == "" {
		id = e.ID
	}
	var names []string
	for key, c := range decl.Children {
		if c == nil {
			continue
		}
		name := c.Component
		if name == "" {
			name, _, _ = strings.Cut(key, "@")
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return id, names, nil
}

// CheckCoverage is the coverage rule of DH-ADR-BUNDLE-10 over catalogue, the
// names of the build catalogue: every name is mounted (a key of mounted) or on
// optional, and not both; optional names only catalogue components. A failure is
// ReasonUncovered naming every offending component.
func CheckCoverage(catalogue []string, mounted map[string][]string, optional []OptionalItem) error {
	inCatalogue := map[string]bool{}
	for _, name := range catalogue {
		inCatalogue[name] = true
	}
	listed := map[string]bool{}
	for _, it := range optional {
		listed[it.Name] = true
	}
	var problems []string
	for _, name := range slices.Sorted(maps.Keys(inCatalogue)) {
		where, isMounted := mounted[name]
		switch {
		case !isMounted && !listed[name]:
			problems = append(problems, name+" is mounted by no shipped layer and is not on the optional list")
		case isMounted && listed[name]:
			problems = append(problems, fmt.Sprintf("%s is on the optional list and mounted by %s", name, strings.Join(where, ", ")))
		}
	}
	for _, it := range optional {
		if !inCatalogue[it.Name] {
			problems = append(problems, "the optional list names "+it.Name+", which is not in the catalogue")
		}
	}
	if len(problems) > 0 {
		return herr.Newf(ReasonUncovered, "coverage: %s", strings.Join(problems, "; "))
	}
	return nil
}

// Coverage runs CheckCoverage for the shipped build: catalogue is the names of
// the build catalogue; the mounts are those of every definition, the five of
// DH-D-BUNDLE-1 and the other launchable profiles; the list is Optional.
func Coverage(catalogue []string) error {
	defs := Definitions()
	for _, d := range Builtins() {
		if !slices.ContainsFunc(defs, func(o Definition) bool { return o.Name == d.Name }) {
			defs = append(defs, d)
		}
	}
	mounted, err := Mounted(defs)
	if err != nil {
		return err
	}
	optional, err := Optional()
	if err != nil {
		return err
	}
	return CheckCoverage(catalogue, mounted, optional)
}

// Egress is the egress set of a composed tree (NG-7, DH-ADR-BUNDLE-10): the kinds
// of outbound data the sharing gate configured by the tree's host.sharing entries
// would permit, with their destinations. The egress client hands a client only to
// a permit holder, so a kind not in the set cannot leave the machine. A disabled
// entry, or one under a disabled group, configures nothing; a tree with no enabled
// host.sharing entry leaves the gate at its default, every kind off. The
// environment is not read: DSH_SHARING_OFF only shrinks the set. A config that
// does not decode strictly (an expression included) is ReasonBadSharing.
func Egress(tree []loader.EntryOptions) ([]sharing.Status, error) {
	var out []sharing.Status
	var walk func([]loader.EntryOptions) error
	walk = func(entries []loader.EntryOptions) error {
		for _, e := range entries {
			if e.Disabled {
				continue
			}
			if !e.Group && e.Name == sharingName {
				var cfg sharing.Config
				if len(e.Config) > 0 {
					dec := json.NewDecoder(bytes.NewReader(e.Config))
					dec.DisallowUnknownFields()
					if err := dec.Decode(&cfg); err != nil {
						return herr.Wrap(ReasonBadSharing, err, "entry "+e.ID)
					}
				}
				for _, st := range sharing.New(cfg, nil).Senders() {
					if st.State == sharing.StateOn {
						out = append(out, st)
					}
				}
			}
			if err := walk(e.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(tree); err != nil {
		return nil, err
	}
	return out, nil
}
