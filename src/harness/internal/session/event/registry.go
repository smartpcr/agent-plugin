package event

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
)

// Upgrade converts the JSON payload of one version (hot and cold parts merged) to
// the next version's. It is pure: it sees only the bytes.
type Upgrade func(raw json.RawMessage) (json.RawMessage, error)

// Spec declares one event type.
type Spec[T any] struct {
	// Name is "<segment>/<segment>[/...]", lower case. The "session/" namespace is
	// reserved to the session core, and "plugin:" to plugin records.
	Name string
	// Version is the current payload version, from 1.
	Version int
	// Upgrades holds, for every older version v from 1 to Version-1, the converter
	// from v to v+1. A chain with a gap or an extra entry is refused.
	Upgrades map[int]Upgrade
	// Ignorable says a reader that does not declare the type may skip it.
	Ignorable bool
	// Cold names the one top-level JSON field of T stored as the cold part; it
	// must be tagged omitempty. No fold may depend on it.
	Cold string
	// NoSources forbids citing earlier events.
	NoSources bool
	// Detached and Queueable are DH-ADR-SESSION-12's flags: a Detached log-only
	// type may be appended to a session the caller does not drive, and a
	// Queueable one may be handed to another process's holder.
	Detached, Queueable bool
	// Validate is the event-local validator, run on every draft and every decode.
	Validate func(T) error
}

// Info describes one declared type.
type Info struct {
	Name      string
	Version   int
	Ignorable bool
	Surface   bool
	Reserved  bool
	NoSources bool
	Detached  bool
	Queueable bool
	Cold      string
	// Schema is the payload's derived shape (DH-D-UTIL-1); its fingerprint is the
	// inventory's input (DH-D-STORE-5).
	Schema *schema.Type
}

// Fingerprint is the payload shape's fingerprint.
func (i Info) Fingerprint() string { return i.Schema.Fingerprint() }

type entry struct {
	info   Info
	decode func(Record) (any, error)
}

// Registry is the static vocabulary of one program. Declarations happen during
// initialisation; Seal ends them.
type Registry struct {
	mu     sync.RWMutex
	sealed bool
	types  map[string]*entry
}

// NewRegistry is an empty registry, for tests and tools; programs use Default.
func NewRegistry() *Registry { return &Registry{types: map[string]*entry{}} }

var defaultRegistry = NewRegistry()

// Default is the program's registry, holding the core vocabulary.
func Default() *Registry { return defaultRegistry }

// Seal ends declarations: a later Define returns ErrSealed and MustDefine panics.
func (r *Registry) Seal() {
	r.mu.Lock()
	r.sealed = true
	r.mu.Unlock()
}

// Sealed reports whether Seal was called.
func (r *Registry) Sealed() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sealed
}

// Lookup is the declaration of name.
func (r *Registry) Lookup(name string) (Info, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.types[name]
	if !ok {
		return Info{}, false
	}
	return e.info, true
}

// Types is every declaration, sorted by name.
func (r *Registry) Types() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Info, 0, len(r.types))
	for _, e := range r.types {
		out = append(out, e.info)
	}
	slices.SortFunc(out, func(a, b Info) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Classify judges whether a stored record can be read by this build. It returns nil
// for a declared type at or below the current version, for a plugin record and for
// an undeclared or newer record marked ignorable (kept as its raw strings). It
// returns *UnknownTypeError for an undeclared required type and *NewerPayloadError
// for a newer required version. It does not decode the payload.
func (r *Registry) Classify(rec Record) error {
	if _, _, ok := ParsePluginType(rec.Type); ok {
		return nil
	}
	info, ok := r.Lookup(rec.Type)
	switch {
	case !ok && rec.Ignorable:
		return nil
	case !ok:
		return &UnknownTypeError{Type: rec.Type, Seq: rec.Seq}
	case rec.Version < 1:
		return errorf(CodeInvalidEvent, "type %q at seq %d has payload version %d", rec.Type, rec.Seq, rec.Version)
	case rec.Version > info.Version && rec.Ignorable:
		return nil
	case rec.Version > info.Version:
		return &NewerPayloadError{Type: rec.Type, Version: rec.Version, Known: info.Version, Seq: rec.Seq}
	}
	return nil
}

// DecodeAny decodes rec into its type's current payload struct, returned as a
// value of that struct. It refuses as Classify does, and also an ignorable record
// it cannot interpret: a caller that wants raw strings reads the Record.
func (r *Registry) DecodeAny(rec Record) (any, error) {
	r.mu.RLock()
	e, ok := r.types[rec.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, &UnknownTypeError{Type: rec.Type, Seq: rec.Seq}
	}
	return e.decode(rec)
}

// Validate checks the envelope of a record against the declarations
// (DH-T-SESSION-2): a surface type has an operation and a log-only one has none; a
// replaced range lies before the event; sources, when present, are unique and
// earlier, and absent where the type forbids them; a plugin record has version 0
// and is ignorable; the parts are JSON.
func (r *Registry) Validate(rec Record) error {
	if !json.Valid([]byte(rec.Payload)) {
		return errorf(CodeInvalidEvent, "type %q at seq %d: payload is not JSON", rec.Type, rec.Seq)
	}
	if rec.Cold != "" && !json.Valid([]byte(rec.Cold)) {
		return errorf(CodeInvalidEvent, "type %q at seq %d: cold part is not JSON", rec.Type, rec.Seq)
	}
	if err := checkSources(rec.Type, rec.Seq, rec.Sources); err != nil {
		return err
	}
	if _, _, ok := ParsePluginType(rec.Type); ok {
		if rec.Version != 0 || !rec.Ignorable || rec.Op != OpNone || rec.Start != 0 || rec.End != 0 || rec.Cold != "" {
			return errorf(CodeInvalidEvent, "plugin record %q at seq %d: version 0, ignorable, log-only and hot only", rec.Type, rec.Seq)
		}
		return nil
	}
	if strings.HasPrefix(rec.Type, pluginPrefix) {
		return errorf(CodeInvalidEvent, "plugin record type %q at seq %d is outside the grammar", rec.Type, rec.Seq)
	}
	info, ok := r.Lookup(rec.Type)
	if !ok {
		return nil // judged by Classify; its envelope rules are its own build's
	}
	if info.NoSources && len(rec.Sources) > 0 {
		return errorf(CodeInvalidEvent, "type %q at seq %d cites sources, which it forbids", rec.Type, rec.Seq)
	}
	return checkOp(info, rec.Seq, Op{Kind: rec.Op, Start: rec.Start, End: rec.End})
}

func checkOp(info Info, at Seq, op Op) error {
	if !info.Surface {
		if op != (Op{}) {
			return errorf(CodeInvalidSurface, "log-only type %q at seq %d carries a surface operation %s", info.Name, at, op.Kind)
		}
		return nil
	}
	switch op.Kind {
	case OpAppend:
		if op.Start != 0 || op.End != 0 {
			return errorf(CodeInvalidSurface, "type %q at seq %d: an append names no range", info.Name, at)
		}
	case OpReplace:
		if op.Start > op.End || op.End >= at {
			return errorf(CodeInvalidSurface, "type %q at seq %d: replaced range %d..%d is not an earlier range", info.Name, at, op.Start, op.End)
		}
	default:
		return errorf(CodeInvalidSurface, "surface type %q at seq %d has no operation", info.Name, at)
	}
	return nil
}

func checkSources(typ string, at Seq, sources []Seq) error {
	seen := make(map[Seq]bool, len(sources))
	for _, s := range sources {
		if s >= at {
			return errorf(CodeInvalidEvent, "type %q at seq %d cites seq %d, which is not earlier", typ, at, s)
		}
		if seen[s] {
			return errorf(CodeInvalidEvent, "type %q at seq %d cites seq %d twice", typ, at, s)
		}
		seen[s] = true
	}
	return nil
}

// decl is what Type and SurfaceType share: the declaration and its codec.
type decl[T any] struct {
	info     Info
	spec     Spec[T]
	coldPath []int
}

// Name is the type's name.
func (d *decl[T]) Name() string { return d.info.Name }

// Version is the current payload version.
func (d *decl[T]) Version() int { return d.info.Version }

// Info is the declaration.
func (d *decl[T]) Info() Info { return d.info }

// Decode reads rec into a fresh T: an older version is converted through the
// upgrade chain, a newer one is refused with *NewerPayloadError and nothing is
// fabricated. Unknown fields are ignored, because the raw string, never the
// decoded struct, is what fork, export and storage carry. The cold part is merged
// in when the record holds it.
func (d *decl[T]) Decode(rec Record) (T, error) {
	var zero T
	if rec.Type != d.info.Name {
		return zero, errorf(CodeInvalidEvent, "record at seq %d is %q, not %q", rec.Seq, rec.Type, d.info.Name)
	}
	if rec.Version > d.info.Version {
		return zero, &NewerPayloadError{Type: rec.Type, Version: rec.Version, Known: d.info.Version, Seq: rec.Seq}
	}
	if rec.Version < 1 {
		return zero, errorf(CodeInvalidEvent, "type %q at seq %d has payload version %d", rec.Type, rec.Seq, rec.Version)
	}
	if !isObject(rec.Payload) {
		return zero, errorf(CodeInvalidEvent, "type %q at seq %d: the payload is not a JSON object", rec.Type, rec.Seq)
	}
	raw, err := d.merge(rec)
	if err != nil {
		return zero, err
	}
	for v := rec.Version; v < d.info.Version; v++ {
		if raw, err = d.spec.Upgrades[v](raw); err != nil {
			return zero, errorf(CodeInvalidEvent, "type %q at seq %d: upgrading version %d: %v", rec.Type, rec.Seq, v, err)
		}
	}
	if !isObject(string(raw)) {
		return zero, errorf(CodeInvalidEvent, "type %q at seq %d: the upgraded payload is not a JSON object", rec.Type, rec.Seq)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, errorf(CodeInvalidEvent, "type %q at seq %d version %d: %v", rec.Type, rec.Seq, rec.Version, err)
	}
	if d.spec.Validate != nil {
		if err := d.spec.Validate(out); err != nil {
			return zero, errorf(CodeInvalidEvent, "type %q at seq %d: %v", rec.Type, rec.Seq, err)
		}
	}
	return out, nil
}

// As decodes an event; see Decode.
func (d *decl[T]) As(e Event) (T, error) { return d.Decode(e.r) }

func (d *decl[T]) merge(rec Record) (json.RawMessage, error) {
	if rec.Cold == "" || d.info.Cold == "" {
		return json.RawMessage(rec.Payload), nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rec.Payload), &m); err != nil {
		return nil, errorf(CodeInvalidEvent, "type %q at seq %d: payload with a cold part is not an object: %v", rec.Type, rec.Seq, err)
	}
	if m == nil {
		return nil, errorf(CodeInvalidEvent, "type %q at seq %d: payload with a cold part is not an object", rec.Type, rec.Seq)
	}
	if !json.Valid([]byte(rec.Cold)) {
		return nil, errorf(CodeInvalidEvent, "type %q at seq %d: the cold part is not JSON", rec.Type, rec.Seq)
	}
	m[d.info.Cold] = json.RawMessage(rec.Cold)
	return json.Marshal(m)
}

// isObject reports whether s is a syntactically valid JSON object.
func isObject(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	return strings.HasPrefix(t, "{") && json.Valid([]byte(s))
}

// encode validates v and returns its canonical hot and cold parts.
func (d *decl[T]) encode(v T) (hot, cold string, err error) {
	if d.spec.Validate != nil {
		if err := d.spec.Validate(v); err != nil {
			return "", "", errorf(CodeInvalidEvent, "type %q: %v", d.info.Name, err)
		}
	}
	if d.coldPath != nil {
		f := reflect.ValueOf(&v).Elem().FieldByIndex(d.coldPath)
		if !f.IsZero() {
			b, err := vocab.Encode(f.Interface())
			if err != nil {
				return "", "", errorf(CodeInvalidEvent, "type %q: encoding the cold part: %v", d.info.Name, err)
			}
			cold = string(b)
			f.SetZero()
		}
	}
	b, err := vocab.Encode(v)
	if err != nil {
		return "", "", errorf(CodeInvalidEvent, "type %q: encoding: %v", d.info.Name, err)
	}
	return string(b), cold, nil
}

func (d *decl[T]) draft(op Op, v T, sources []Seq) (Draft, error) {
	if d.info.NoSources && len(sources) > 0 {
		return Draft{}, errorf(CodeInvalidEvent, "type %q forbids sources", d.info.Name)
	}
	if hasDuplicate(sources) {
		return Draft{}, errorf(CodeInvalidEvent, "type %q: a source is cited twice", d.info.Name)
	}
	hot, cold, err := d.encode(v)
	if err != nil {
		return Draft{}, err
	}
	return Draft{
		typ: d.info.Name, version: d.info.Version, ignorable: d.info.Ignorable, reserved: d.info.Reserved,
		surface: d.info.Surface, op: op, sources: slices.Clone(sources), payload: hot, cold: cold,
	}, nil
}

func hasDuplicate(s []Seq) bool {
	seen := make(map[Seq]bool, len(s))
	for _, x := range s {
		if seen[x] {
			return true
		}
		seen[x] = true
	}
	return false
}

// Type is the handle of a log-only event type: its drafts carry no operation.
type Type[T any] struct{ *decl[T] }

// Draft encodes v as an event of this type, citing sources. The payload is encoded
// once, here; the caller may change v afterwards.
func (t *Type[T]) Draft(v T, sources ...Seq) (Draft, error) { return t.draft(Op{}, v, sources) }

// SurfaceType is the handle of a type that changes model history: its drafts
// always carry an operation. Only this package declares surface types.
type SurfaceType[T any] struct{ *decl[T] }

// Draft encodes v as an event performing op, citing sources.
func (t *SurfaceType[T]) Draft(op Op, v T, sources ...Seq) (Draft, error) {
	if op.Kind != OpAppend && op.Kind != OpReplace {
		return Draft{}, errorf(CodeInvalidSurface, "surface type %q needs an append or replace operation", t.info.Name)
	}
	if op.Kind == OpAppend && (op.Start != 0 || op.End != 0) {
		return Draft{}, errorf(CodeInvalidSurface, "type %q: an append names no range", t.info.Name)
	}
	if op.Kind == OpReplace && op.Start > op.End {
		return Draft{}, errorf(CodeInvalidSurface, "type %q: replaced range %d..%d is reversed", t.info.Name, op.Start, op.End)
	}
	return t.draft(op, v, sources)
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(/[a-z][a-z0-9-]*)+$`)

// reservedPrefix is the namespace of the session core's own types.
const reservedPrefix = "session/"

type declOpts struct{ surface, core bool }

// Define declares a log-only event type in r.
func Define[T any](r *Registry, s Spec[T]) (*Type[T], error) {
	d, err := define(r, s, declOpts{})
	if err != nil {
		return nil, err
	}
	return &Type[T]{d}, nil
}

// MustDefine is Define for package-level declarations: it panics on every error,
// including a declaration after Seal (DH-ADR-SESSION-4).
func MustDefine[T any](r *Registry, s Spec[T]) *Type[T] {
	t, err := Define(r, s)
	if err != nil {
		panic(err)
	}
	return t
}

func mustCore[T any](s Spec[T]) *Type[T] {
	d, err := define(defaultRegistry, s, declOpts{core: true})
	if err != nil {
		panic(err)
	}
	return &Type[T]{d}
}

func mustCoreSurface[T any](s Spec[T]) *SurfaceType[T] {
	d, err := define(defaultRegistry, s, declOpts{surface: true, core: true})
	if err != nil {
		panic(err)
	}
	return &SurfaceType[T]{d}
}

func define[T any](r *Registry, s Spec[T], o declOpts) (*decl[T], error) {
	bad := func(format string, args ...any) error {
		return errorf(CodeInvalidDeclaration, "type %q: "+format, append([]any{s.Name}, args...)...)
	}
	// The registry owns its codec: later changes to the caller's map must not reach it.
	s.Upgrades = maps.Clone(s.Upgrades)
	switch {
	case !namePattern.MatchString(s.Name):
		return nil, bad("the name is not lower-case segments separated by '/'")
	case strings.HasPrefix(s.Name, reservedPrefix) && !o.core:
		return nil, errorf(CodeReservedType, "type %q: the %q namespace is reserved to the session core", s.Name, reservedPrefix)
	case s.Version < 1:
		return nil, bad("version %d is below 1", s.Version)
	case s.Queueable && !s.Detached:
		return nil, bad("a Queueable type must be Detached")
	case (s.Detached) && o.surface:
		return nil, bad("a surface type cannot be Detached")
	}
	for v := 1; v < s.Version; v++ {
		if s.Upgrades[v] == nil {
			return nil, bad("no upgrade from version %d to %d", v, v+1)
		}
	}
	for v := range s.Upgrades {
		if v < 1 || v >= s.Version {
			return nil, bad("an upgrade from version %d is outside 1..%d", v, s.Version-1)
		}
	}
	rt := reflect.TypeFor[T]()
	if rt.Kind() != reflect.Struct {
		return nil, bad("the payload %s is not a struct", rt)
	}
	sch, err := schema.ForType(rt)
	if err != nil {
		return nil, bad("%v", err)
	}
	d := &decl[T]{spec: s, info: Info{
		Name: s.Name, Version: s.Version, Ignorable: s.Ignorable, Surface: o.surface,
		Reserved: strings.HasPrefix(s.Name, reservedPrefix), NoSources: s.NoSources,
		Detached: s.Detached, Queueable: s.Queueable, Cold: s.Cold, Schema: sch,
	}}
	if s.Cold != "" {
		path, err := coldField(rt, s.Cold)
		if err != nil {
			return nil, bad("%v", err)
		}
		d.coldPath = path
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return nil, fmt.Errorf("%w: %w", bad("declared after the registry was sealed"), ErrSealed)
	}
	if _, dup := r.types[s.Name]; dup {
		return nil, fmt.Errorf("%w: %w", bad("declared twice"), ErrDuplicate)
	}
	r.types[s.Name] = &entry{info: d.info, decode: func(rec Record) (any, error) {
		v, err := d.Decode(rec)
		if err != nil {
			return nil, err
		}
		return v, nil
	}}
	return d, nil
}

// coldField finds the top-level field of t encoded under name, tagged omitempty.
func coldField(t reflect.Type, name string) ([]int, error) {
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() || len(f.Index) != 1 {
			continue
		}
		tag := f.Tag.Get("json")
		jn, opts, _ := strings.Cut(tag, ",")
		if jn != name {
			continue
		}
		if !slices.Contains(strings.Split(opts, ","), "omitempty") {
			return nil, fmt.Errorf("the cold field %q must be tagged omitempty", name)
		}
		return f.Index, nil
	}
	return nil, fmt.Errorf("the cold field %q is not a top-level field", name)
}

// Canonical re-encodes a payload in the canonical form drafts use; decoding and
// re-encoding a declared type gives the same bytes (DH-T-SESSION-1).
func Canonical(raw string) (string, error) {
	b, err := vocab.Canonical([]byte(raw))
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(b)), nil
}
