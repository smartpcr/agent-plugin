package boot

import (
	"bytes"
	"context"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/permission"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
)

// Preview is one previewed launch of a profile (DH-D-BOOT-6): the report of the
// static check over its composed tree, the labels of the layers it composed, in
// order, the launch environment it was read under, and the layer issues a valid
// configuration carries (a built-in bundle's operation that matches no entry,
// see Tolerated), which the report's issues leave out. Every other layer issue is
// in the report.
type Preview struct {
	Report *config.Report
	Layers []string
	Env    *launch.Snapshot
	Notes  []loader.Issue
}

// PreviewLaunch runs the launch's facts and layers and the static check a launch
// and a reload hold the tree to (the catalogue, the expressions, the Required
// list, the agent presets as one set and each permission preset table) without
// the home, the guards or the tree: it reads the layer files and writes nothing,
// and no component is constructed. Without defaults the stack is the launch's;
// with defaults it leaves the profile layer out (`dsh config show --defaults`).
// The error is a layer that cannot be read or parsed (profile/bad-layer) or the
// Required failure of profile.Preview, which returns its report with it.
func PreviewLaunch(ctx context.Context, o Options, defaults bool) (*Preview, error) {
	s := &State{Options: o}
	steps := Real{}
	if err := steps.Facts(ctx, s); err != nil {
		return nil, err
	}
	if err := steps.Layers(ctx, s); err != nil {
		return nil, err
	}
	layers := s.Layers
	if defaults {
		layers = slices.DeleteFunc(slices.Clone(layers), func(l loader.Layer) bool { return l.Label == profile.ProfileLabel })
	}
	cat := o.Catalog
	if cat == nil {
		var err error
		if cat, err = builtin.Catalog(); err != nil {
			return nil, err
		}
	}
	scope := launch.Scope(s.Data, s.Env, secret.SecretShaped)
	scope.Views = cat.Views()
	ev, err := expr.New(scope)
	if err != nil {
		return nil, err
	}
	docs := map[string]*loader.LayerDocument{}
	for _, l := range layers {
		data := s.layerFiles[l.Path]
		if l.Path == "" || len(bytes.TrimSpace(data)) == 0 {
			continue
		}
		if doc, err := loader.ParseLayer(nil, l.Path, data); err == nil {
			docs[l.Label] = doc
		}
	}
	tree := map[loader.Issue]bool{}
	checks := []config.TreeCheck{agentpreset.CheckTree(cat, ev), permission.CheckTree()}
	for k, check := range checks {
		checks[k] = func(t []loader.EntryOptions) []loader.Issue {
			issues := check(t)
			for _, i := range issues {
				tree[i] = true
			}
			return issues
		}
	}
	r, err := profile.Preview(o.Profile, layers, config.CheckOptions{
		Catalog: cat, Evaluator: ev, Docs: docs, TreeChecks: checks,
	})
	p := &Preview{Report: r, Env: s.Env}
	if r != nil {
		// Check reports the composition issues first. A built-in bundle's
		// operation on an entry the profile does not carry is a note; every other
		// layer fault (an invalid or duplicate operation, a layer file issue) is
		// an issue, so validate rejects it.
		_, _, composed := loader.Compose(layers)
		n := min(len(composed), len(r.Issues))
		issues := append(slices.Clone(s.Issues), r.Issues[:n]...)
		var faults []loader.Issue
		for _, i := range issues {
			if Tolerated(i) {
				p.Notes = append(p.Notes, i)
			} else {
				faults = append(faults, i)
			}
		}
		r.Issues = append(faults, r.Issues[n:]...)
		r.Issues = dedupe(r.Issues, tree)
	}
	for _, l := range layers {
		p.Layers = append(p.Layers, l.Label)
	}
	return p, err
}

// dedupe drops the second report of one fault: an issue identical to an earlier
// one, position included, or an issue of a tree check whose path and message an
// issue outside the tree checks carries, since the entry's own schema places that
// fault at its value and the tree check at the entry. Two faults with the same
// path and message at different positions (the same bad operation on two lines,
// or in two layers) both stay.
func dedupe(issues []loader.Issue, tree map[loader.Issue]bool) []loader.Issue {
	type text struct{ path, message string }
	named := map[text]bool{}
	for _, i := range issues {
		if !tree[i] {
			named[text{i.Path, i.Message}] = true
		}
	}
	seen := map[loader.Issue]bool{}
	return slices.DeleteFunc(issues, func(i loader.Issue) bool {
		if tree[i] && named[text{i.Path, i.Message}] {
			return true
		}
		dup := seen[i]
		seen[i] = true
		return dup
	})
}

// Tolerated reports whether a layer issue is one a valid configuration carries: a
// built-in bundle's operation on an entry the profile leaves out, which matches no
// entry (headless drops human.secrets). Any other layer issue is a fault.
func Tolerated(i loader.Issue) bool {
	return strings.HasPrefix(i.Path, "layer bundle:") && strings.HasSuffix(i.Message, "matches no entry")
}
