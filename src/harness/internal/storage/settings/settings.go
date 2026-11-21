// Package settings is the live-settings form (DH-D-STORAGE-8): it lists what can be
// changed while the system runs, with where each value comes from, and applies an
// edit through boot's one edit path, config.Service (DH-D-BOOT-7), so a stale,
// invalid or shadowed edit changes nothing. It never opens a layer file for
// writing itself.
//
// This is the owner path only (NG-9): the routes and the page are DH-D-API-7 and
// DH-D-UISET-1, and reset, clear and the settled-reload announcement come with them.
package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Error codes of a refused edit.
const (
	// ReasonNotLive is an edit of a field that is not tagged dsh:"live".
	ReasonNotLive herr.Reason = "settings/not-live"
	// ReasonOverridden is an edit a higher layer shadows: it would not take effect.
	ReasonOverridden herr.Reason = "settings/overridden"
	// ReasonUnsaved is an edit that validated but could not be written.
	ReasonUnsaved herr.Reason = "settings/unsaved"
)

// Secret is the presence marker shown instead of a secret field's value.
const Secret = "<set>"

// Field is one live field of an entry: its path ("config.level"), its effective
// value, the layer that supplies it, and whether it is a secret (Value is then the
// presence marker, or null when unset).
type Field struct {
	Path   string          `json:"path"`
	Value  json.RawMessage `json:"value"`
	Layer  string          `json:"layer,omitempty"`
	Secret bool            `json:"secret,omitempty"`
}

// Entry is one entry of the tree with live fields.
type Entry struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}

// Form is the live settings: the effective values, those the running tree was
// built from, and the revision of the layers on disk an edit is made against. When
// the files on disk differ from the effective ones (a change saved but not yet
// reloaded, or rejected), Pending is set and Issues are what the disk's layers fail.
type Form struct {
	Revision string         `json:"revision"`
	Entries  []Entry        `json:"entries"`
	Pending  bool           `json:"pending,omitempty"`
	Issues   []loader.Issue `json:"issues,omitempty"`
}

// Status is the outcome of an edit.
type Status string

const (
	Applied    Status = "applied"
	Overridden Status = "overridden"
	Failed     Status = "failed"
	Unsaved    Status = "unsaved"
	Stale      Status = "stale"
)

// Result is the outcome of an edit: its status, the new revision once applied,
// the layer that shadows it when overridden, the positioned issues when it failed,
// and the coded error of every outcome but Applied.
type Result struct {
	Status   Status
	Revision string
	Layer    string
	Issues   []loader.Issue
	Err      error
}

// Service is the settings form over one config.Service.
type Service struct {
	config  *config.Service
	catalog config.Catalog
}

// New returns the form over cfg, reading config types from cat.
func New(cfg *config.Service, cat config.Catalog) *Service {
	return &Service{config: cfg, catalog: cat}
}

// Describe lists every entry with live fields at its effective value: the accepted
// tree, never a draft on disk that was rejected or not yet reloaded.
func (s *Service) Describe() (*Form, error) {
	eff := s.config.Current()
	form := &Form{Revision: eff.Revision, Entries: []Entry{}}
	switch draft, err := s.config.Draft(); {
	case err != nil:
		form.Pending = true
		form.Issues = []loader.Issue{{Message: err.Error()}}
		var pe *loader.ParseError
		if errors.As(err, &pe) {
			form.Issues = pe.Issues
		}
	case draft.Revision != eff.Revision:
		form.Revision, form.Pending, form.Issues = draft.Revision, true, draft.Report.Issues
	}
	snap := eff
	for _, pe := range snap.Report.Entries() {
		if pe.Group || pe.ID == "" {
			continue
		}
		fields := s.liveFields(pe.Name)
		if len(fields) == 0 {
			continue
		}
		entry := Entry{ID: pe.ID, Name: pe.Name}
		for _, f := range fields {
			field := Field{Path: f.path, Secret: f.secret, Value: json.RawMessage("null")}
			if ex, err := snap.Report.Explain(pe.ID, f.path); err == nil {
				field.Layer = ex.Layer
				if len(ex.Value) > 0 {
					field.Value = ex.Value
				}
			} else if label, ok := snap.Report.Index.Owner(pe.ID, f.path); ok {
				field.Layer = label
			}
			if f.secret && !bytes.Equal(field.Value, []byte("null")) {
				field.Value = json.RawMessage(`"` + Secret + `"`)
			}
			entry.Fields = append(entry.Fields, field)
		}
		form.Entries = append(form.Entries, entry)
	}
	return form, nil
}

// Apply sets the entry's live field to value (JSON; null clears it in the writable
// layer), made against revision. It first checks the candidate, so a field error
// names the field and a shadowed edit is refused, then applies it through
// config.Service.Apply with the same operation and revision.
func (s *Service) Apply(ctx context.Context, revision, id, path string, value json.RawMessage) Result {
	snap, err := s.config.Draft()
	if err != nil {
		return Result{Status: Unsaved, Err: herr.Wrap(ReasonUnsaved, err, "read layers")}
	}
	var name string
	for _, pe := range snap.Report.Entries() {
		if pe.ID == id {
			name = pe.Name
		}
	}
	if name == "" {
		return failed(herr.Newf(ReasonNotLive, "%s: no such entry", id), loader.Issue{Path: id, Message: "no such entry"})
	}
	if !slices.ContainsFunc(s.liveFields(name), func(f liveField) bool { return f.path == path }) {
		return failed(herr.Newf(ReasonNotLive, "%s.%s is not a live setting", id, path), loader.Issue{Path: id + "." + path, Message: "not a live setting"})
	}
	op, err := mergeOp(id, path, value)
	if err != nil {
		return failed(err, loader.Issue{Path: id + "." + path, Message: err.Error()})
	}
	edit := config.Edit{Revision: revision, Ops: []loader.Op{op}}
	cand, err := s.config.Candidate(edit)
	if err != nil {
		return classify(err)
	}
	if !cand.Report.OK() {
		return Result{Status: Failed, Issues: cand.Report.Issues, Err: herr.Wrap(config.ReasonRejected, cand.Report.Err(), "edit rejected")}
	}
	writable := writableLabel(cand)
	if owner, ok := cand.Report.Index.Owner(id, path); ok && owner != writable {
		return Result{Status: Overridden, Layer: owner, Err: herr.Newf(ReasonOverridden, "%s.%s is set by layer %s, which is above the one edited", id, path, owner)}
	}
	rev, err := s.config.Apply(ctx, edit)
	if err != nil {
		return classify(err)
	}
	return Result{Status: Applied, Revision: rev}
}

func failed(err error, issue loader.Issue) Result {
	return Result{Status: Failed, Issues: []loader.Issue{issue}, Err: err}
}

// classify maps an error of config.Service to an outcome.
func classify(err error) Result {
	switch {
	case herr.Has(err, config.ReasonStale):
		return Result{Status: Stale, Err: err}
	case herr.Has(err, config.ReasonComputed):
		return Result{Status: Failed, Err: err}
	case herr.Has(err, config.ReasonRejected):
		var pe *loader.ParseError
		r := Result{Status: Failed, Err: err}
		if errors.As(err, &pe) {
			r.Issues = pe.Issues
		}
		return r
	}
	return Result{Status: Unsaved, Err: herr.Wrap(ReasonUnsaved, err, "edit not saved")}
}

func writableLabel(snap *config.Snapshot) string {
	for _, l := range snap.Layers {
		if l.Writable {
			return l.Label
		}
	}
	return ""
}

// mergeOp is the merge operation setting the config field at path to value.
func mergeOp(id, path string, value json.RawMessage) (loader.Op, error) {
	steps := strings.Split(path, ".")
	if len(steps) < 2 || steps[0] != "config" {
		return loader.Op{}, herr.Newf(ReasonNotLive, "%s: only config fields are settings", path)
	}
	if !json.Valid(value) {
		return loader.Op{}, fmt.Errorf("%s: value is not JSON", path)
	}
	patch := json.RawMessage(bytes.Clone(value))
	for i := len(steps) - 1; i >= 1; i-- {
		wrapped, err := json.Marshal(map[string]json.RawMessage{steps[i]: patch})
		if err != nil {
			return loader.Op{}, err
		}
		patch = wrapped
	}
	return loader.Op{Kind: loader.OpMerge, ID: id, Patch: patch}, nil
}

type liveField struct {
	path   string
	secret bool
}

// liveFields lists the fields of name's config type tagged dsh:"live", nested
// structs included.
func (s *Service) liveFields(name string) []liveField {
	if s.catalog == nil {
		return nil
	}
	item, err := s.catalog.Lookup(name)
	if err != nil || item.Config == nil {
		return nil
	}
	var out []liveField
	collect(item.Config, "config", &out, 0)
	return out
}

func collect(t reflect.Type, prefix string, out *[]liveField, depth int) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || depth > 8 {
		return
	}
	for _, f := range reflect.VisibleFields(t) {
		if f.Anonymous || !f.IsExported() {
			continue
		}
		name, ok := jsonName(f)
		if !ok {
			continue
		}
		words := strings.Split(f.Tag.Get("dsh"), ",")
		path := prefix + "." + name
		if slices.Contains(words, "live") {
			*out = append(*out, liveField{path: path, secret: slices.Contains(words, "secret")})
			continue
		}
		collect(f.Type, path, out, depth+1)
	}
}

func jsonName(f reflect.StructField) (string, bool) {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name, true
	}
	name, _, _ := strings.Cut(tag, ",")
	switch name {
	case "-":
		return "", false
	case "":
		return f.Name, true
	}
	return name, true
}
