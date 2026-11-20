package event

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// Draft is an encoded event not yet given a position: what a writer hands the
// session. Its fields are unexported, so the only drafts are those a Type,
// SurfaceType or PluginDraft built; the payload was encoded and validated then.
type Draft struct {
	typ       string
	version   int
	ignorable bool
	reserved  bool
	surface   bool
	op        Op
	sources   []Seq
	payload   string
	cold      string
}

// Type is the draft's type name.
func (d Draft) Type() string { return d.typ }

// Version is the payload version.
func (d Draft) Version() int { return d.version }

// Ignorable reports whether the type is ignorable.
func (d Draft) Ignorable() bool { return d.ignorable }

// Reserved reports whether only the session core may append it.
func (d Draft) Reserved() bool { return d.reserved }

// Op is the surface operation.
func (d Draft) Op() Op { return d.op }

// Sources is a copy of the cited events.
func (d Draft) Sources() []Seq { return slices.Clone(d.sources) }

// Raw is the encoded hot part.
func (d Draft) Raw() string { return d.payload }

// ColdRaw is the encoded cold part, empty when there is none.
func (d Draft) ColdRaw() string { return d.cold }

// IsZero reports whether d was never built.
func (d Draft) IsZero() bool { return d.typ == "" }

// Check is the session's admission check of a draft from a writer: a reserved
// type is refused unless byCore, and a zero draft is refused.
func (d Draft) Check(byCore bool) error {
	if d.IsZero() {
		return errorf(CodeInvalidEvent, "the draft was not built by a declared type")
	}
	if d.reserved && !byCore {
		return errorf(CodeReservedType, "type %q is reserved to the session core", d.typ)
	}
	return nil
}

// Stamp gives d its position and time, checking what only the position decides:
// a replaced range ends before seq and every source is earlier.
func (d Draft) Stamp(seq Seq, time int64) (Record, error) {
	if d.IsZero() {
		return Record{}, errorf(CodeInvalidEvent, "the draft was not built by a declared type")
	}
	if d.op.Kind == OpReplace && d.op.End >= seq {
		return Record{}, errorf(CodeInvalidSurface, "type %q at seq %d: replaced range %d..%d is not an earlier range", d.typ, seq, d.op.Start, d.op.End)
	}
	if err := checkSources(d.typ, seq, d.sources); err != nil {
		return Record{}, err
	}
	return Record{
		Seq: seq, Time: time, Type: d.typ, Version: d.version, Ignorable: d.ignorable,
		Op: d.op.Kind, Start: d.op.Start, End: d.op.End, Sources: slices.Clone(d.sources),
		Payload: d.payload, Cold: d.cold,
	}, nil
}

// pluginPrefix begins every plugin record type.
const pluginPrefix = "plugin:"

var pluginSegment = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// PluginType is the type name of a plugin record: "plugin:<owner>/<record>".
func PluginType(owner, record string) (string, error) {
	if !pluginSegment.MatchString(owner) || !pluginSegment.MatchString(record) {
		return "", errorf(CodeInvalidEvent, "plugin record %q/%q: owner and record are lower-case [a-z0-9._-], 1 to 64 long", owner, record)
	}
	return pluginPrefix + owner + "/" + record, nil
}

// ParsePluginType splits a plugin record type; ok is false for anything else,
// including a "plugin:" name outside the grammar.
func ParsePluginType(typ string) (owner, record string, ok bool) {
	rest, found := strings.CutPrefix(typ, pluginPrefix)
	if !found {
		return "", "", false
	}
	owner, record, found = strings.Cut(rest, "/")
	if !found || !pluginSegment.MatchString(owner) || !pluginSegment.MatchString(record) {
		return "", "", false
	}
	return owner, record, true
}

// PluginDraft is a plugin's own record (DH-ADR-SESSION-10): version 0, always
// ignorable, log-only, its data any JSON document. Its owner is the extension's
// bound id; core folds never read it.
func PluginDraft(owner, record string, data json.RawMessage, sources ...Seq) (Draft, error) {
	typ, err := PluginType(owner, record)
	if err != nil {
		return Draft{}, err
	}
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	b, err := vocab.Canonical(data)
	if err != nil {
		return Draft{}, errorf(CodeInvalidEvent, "plugin record %q: data is not JSON: %v", typ, err)
	}
	if hasDuplicate(sources) {
		return Draft{}, errorf(CodeInvalidEvent, "plugin record %q: a source is cited twice", typ)
	}
	return Draft{typ: typ, ignorable: true, sources: slices.Clone(sources), payload: string(b)}, nil
}
