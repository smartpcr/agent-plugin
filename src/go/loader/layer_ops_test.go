package loader_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

// A faulty operation is not a parse error: it is kept (so a write-back keeps it),
// reported with its path and reason, written back exactly as it was read, and
// skipped by Compose with the same reasons.
func TestParseLayerReportsEachFaultyOperation(t *testing.T) {
	for name, tc := range map[string]struct{ op, want string }{
		"not an object":       {`42`, "[0]: must be an object, got number"},
		"duplicate key":       {`{"id":"a","remove":true,"id":"b"}`, "[0].id: duplicate key"},
		"id not a string":     {`{"id":1,"remove":true}`, "[0].id: must be a string, got number"},
		"name not a string":   {`{"id":"a","name":false,"remove":true}`, "[0].name: must be a string, got boolean"},
		"insert not an array": {`{"insert":{}}`, "[0].insert: must be an array of entries, got object"},
		"merge not an object": {`{"id":"a","merge":[1]}`, "[0].merge: must be an object, got array"},
		"remove not true":     {`{"id":"a","remove":false}`, "[0].remove: must be true"},
		"unknown key":         {`{"id":"a","remove":true,"rename":"b"}`, "[0].rename: unknown key"},
		"no operation":        {`{"id":"a"}`, "names no operation"},
		"two operations":      {`{"id":"a","remove":true,"merge":{}}`, "combines operations (remove, merge)"},
		"missing id":          {`{"disabled":true}`, "[0].id: is required"},
		"name on an insert":   {`{"name":"Values","insert":[]}`, "[0].name: is not allowed on an insert"},
	} {
		doc, err := loader.ParseLayer(nil, "user.json", []byte("["+tc.op+"]"))
		NoError(t, err)
		Equal(t, 1, len(doc.Ops), name)
		True(t, !doc.Ops[0].Valid(), name+": the faulty operation is valid")
		True(t, strings.Contains(issueTexts(doc.Issues), tc.want), name+": issues "+issueTexts(doc.Issues))

		written, err := json.Marshal(doc.Ops[0])
		NoError(t, err)
		Equal(t, string(compactOf(t, tc.op)), string(written), name+": written back")

		_, _, issues := loader.Compose([]loader.Layer{{Label: "user", Ops: doc.Ops}})
		Equal(t, 1, len(issues), name)
		True(t, strings.Contains(issues[0].Message, "operation is invalid and was skipped: ") &&
			strings.Contains(issues[0].Message, strings.TrimPrefix(tc.want, "[0]")) && issues[0].Path == "layer user",
			name+": compose issue "+issues[0].Error())
	}
}

// compactOf is text as the JSON codec writes a decoded node back.
func compactOf(t *testing.T, text string) []byte {
	t.Helper()
	node, err := loader.JSON.Decode("", []byte(text))
	NoError(t, err)
	return node.JSON()
}

// A layer file must be well-formed and an array; anything else is a *ParseError
// that says what it got.
func TestParseLayerRefusesANonArrayDocument(t *testing.T) {
	_, err := loader.ParseLayer(loader.JSON, "user.json", []byte(`{"insert":[]}`))
	var parse *loader.ParseError
	True(t, errors.As(err, &parse) && strings.Contains(err.Error(), "must be an array of layer operations, got object"), errText(err))
	Equal(t, 0, parse.Issues[0].Pos.Offset)

	_, err = loader.ParseLayer(loader.JSON, "user.json", []byte(`[{"id":`))
	True(t, errors.As(err, &parse), "malformed bytes: "+errText(err))
}

// Each kind of operation writes back in its file form, fields in a fixed order, and
// an operation of no known kind does not encode.
func TestOpMarshalJSONWritesTheFileForm(t *testing.T) {
	for want, op := range map[string]loader.Op{
		`{"insert":[{"id":"a","name":"Values"}]}`: {Kind: loader.OpInsert, Entries: []loader.EntryOptions{{ID: "a", Name: "Values"}}},
		`{"id":"g","insert":[]}`:                  {Kind: loader.OpInsert, ID: "g"},
		`{"id":"a","name":"Values","config":{"x":1},"disabled":true}`: {Kind: loader.OpOverride, ID: "a", Name: "Values",
			Set: map[string]json.RawMessage{"disabled": raw("true"), "config": raw(`{ "x" : 1 }`)}},
		`{"id":"a","merge":{}}`:          {Kind: loader.OpMerge, ID: "a"},
		`{"id":"a","merge":{"y":[1,2]}}`: {Kind: loader.OpMerge, ID: "a", Patch: raw(`{"y": [1, 2]}`)},
		`{"id":"a","remove":true}`:       {Kind: loader.OpRemove, ID: "a"},
	} {
		got, err := json.Marshal(op)
		NoError(t, err)
		Equal(t, want, string(got))
	}
	_, err := json.Marshal(loader.Op{Kind: "rename", ID: "a"})
	True(t, err != nil && strings.Contains(err.Error(), `unknown layer operation "rename"`), errText(err))
}

// An operation that matches nothing, targets the wrong component, inserts into a
// non-group or duplicates an entry is reported with its layer and skipped; the
// rest of the layer still applies.
func TestComposeReportsOperationsThatCannotApply(t *testing.T) {
	base := loader.Layer{Label: "base", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: []loader.EntryOptions{
		values("svc", `{"a":1}`), group("g", values("child", `"scalar"`)),
	}}}}
	user := loader.Layer{Label: "user", Ops: []loader.Op{
		{Kind: loader.OpInsert, ID: "missing", Entries: []loader.EntryOptions{values("x", `{}`)}},
		{Kind: loader.OpInsert, ID: "svc", Entries: []loader.EntryOptions{values("x", `{}`)}},
		{Kind: loader.OpInsert, Entries: []loader.EntryOptions{values("svc", `{}`), values("new", `{}`)}},
		{Kind: loader.OpRemove, ID: "svc", Name: "Other"},
		{Kind: loader.OpOverride, ID: "svc", Set: map[string]json.RawMessage{"disabled": raw(`"yes"`), "inject": raw(`["a", 1]`)}},
		{Kind: loader.OpOverride, ID: "svc", Set: map[string]json.RawMessage{"config": raw(`{"a":`)}},
		{Kind: loader.OpMerge, ID: "svc", Patch: raw(`{"a"`)},
		{Kind: "rename", ID: "svc"},
		{Kind: loader.OpMerge, ID: "g:child", Patch: raw(`{"k":{"deep":true,"gone":null}}`)},
		{Kind: loader.OpOverride, ID: "g:child", Set: map[string]json.RawMessage{"config": raw("null")}},
		{Kind: loader.OpMerge, ID: "g:child", Patch: raw(`{"after":1}`)},
		{Kind: loader.OpRemove, ID: "g:child"},
	}}
	tree, index, issues := loader.Compose([]loader.Layer{base, user})
	wants := []string{
		`insert into "missing" matches no entry`,
		`insert into "svc": the entry is not a group`,
		`insert: entry "svc" already exists`,
		`remove "svc" expects name "Other", found "Values"`,
		`override "svc": `,
		`override "svc": config: `,
		`merge "svc": `,
		`unknown operation "rename"`,
	}
	Equal(t, len(wants), len(issues), issueTexts(issues))
	for i, want := range wants {
		True(t, strings.Contains(issues[i].Error(), want) && strings.Contains(issues[i].Error(), "layer user"), issues[i].Error())
	}
	True(t, strings.Contains(issues[4].Message, "a boolean or an expression") && strings.Contains(issues[4].Message, "a service name"),
		"override reasons: "+issues[4].Message)

	Equal(t, 3, len(tree), "the inserted entry next to the duplicate")
	Equal(t, "new", tree[2].ID)
	Equal(t, "user", owner(t, index, "new", ""))
	Equal(t, `{"a":1}`, config(t, tree, "svc"))
	True(t, !tree[0].Disabled && tree[0].Inject == nil, "a refused override changed the entry")
	Equal(t, 0, len(tree[1].Children), "the child was not removed")
	_, ok := index.Owner("g:child", "config.after")
	True(t, !ok, "a removed child kept its field owners")
}

// A merge into a config that is not an object replaces it with the patch minus its
// nulls, and an override to null clears the config.
func TestComposeMergeReplacesANonObjectConfig(t *testing.T) {
	tree, index, issues := loader.Compose([]loader.Layer{
		{Label: "base", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: []loader.EntryOptions{values("svc", `[1]`), values("off", `{"a":1}`)}}}},
		{Label: "user", Ops: []loader.Op{
			{Kind: loader.OpMerge, ID: "svc", Patch: raw(`{"k":{"deep":true,"gone":null},"drop":null}`)},
			{Kind: loader.OpOverride, ID: "off", Set: map[string]json.RawMessage{"config": raw("null")}},
		}},
	})
	Equal(t, 0, len(issues), issueTexts(issues))
	Equal(t, `{"k":{"deep":true}}`, config(t, tree, "svc"))
	Equal(t, "user", owner(t, index, "svc", "config"))
	True(t, tree[1].Config == nil, "an override to null kept the config: "+string(tree[1].Config))
}

// Owners lists every ownership record, sorted by id then field; a nil index owns
// nothing.
func TestIndexOwnersListsEveryRecordSorted(t *testing.T) {
	_, index, _ := loader.Compose([]loader.Layer{
		{Label: "base", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: []loader.EntryOptions{values("b", `{}`), values("a", `{"x":1}`)}}}},
		{Label: "user", Ops: []loader.Op{
			{Kind: loader.OpMerge, ID: "a", Patch: raw(`{"y":2,"x":3}`)},
			{Kind: loader.OpOverride, ID: "b", Set: map[string]json.RawMessage{"disabled": raw("true")}},
		}},
	})
	want := []loader.Ownership{
		{ID: "a", Label: "base"},
		{ID: "a", Field: "config.x", Label: "user"},
		{ID: "a", Field: "config.y", Label: "user"},
		{ID: "b", Label: "base"},
		{ID: "b", Field: "disabled", Label: "user"},
	}
	got := index.Owners()
	Equal(t, len(want), len(got))
	for i := range want {
		Equal(t, want[i], got[i])
	}
	var none *loader.Index
	True(t, none.Owners() == nil, "a nil index listed owners")
	_, ok := none.Owner("a", "")
	True(t, !ok, "a nil index named an owner")
}

// An entry read from JSON reports every fault at once, each with its path, so one
// edit fixes them all.
func TestEntryUnmarshalReportsEveryFault(t *testing.T) {
	const bad = `{
		"id": 7,
		"name": "Values",
		"name": "Twice",
		"group": "yes",
		"disabled": {"$expr": "  "},
		"inject": ["svc", 3],
		"isolate": {"a": 1, "b": true, "b": "x", "c": "realm"},
		"intercept": {"s": {}, "s": {}},
		"children": [5, {"id": "c", "colour": "red"}],
		"extra": 1
	}`
	var o loader.EntryOptions
	err := json.Unmarshal([]byte(bad), &o)
	var parse *loader.ParseError
	True(t, errors.As(err, &parse), errText(err))
	text := issueTexts(parse.Issues)
	for _, want := range []string{
		"id: must be a string, got number",
		"name: duplicate key",
		"group: must be a boolean, got string",
		"disabled: expression must not be empty",
		"inject[1]: must be a service name, got number",
		"isolate.a: must be a realm label or true, got number",
		"isolate.b: duplicate key",
		"intercept.s: duplicate key",
		"children[0]: must be an object, got number",
		"children[1].colour: unknown key",
		"extra: unknown key",
	} {
		True(t, strings.Contains(text, want), "missing "+want+" in:\n"+text)
	}

	for field, want := range map[string]string{
		`"disabled": 1`:           "disabled: must be a boolean or an expression, got number",
		`"inject": "svc"`:         "inject: must be an array of service names, got string",
		`"isolate": []`:           "isolate: must be an object of realm labels, got array",
		`"intercept": 1`:          "intercept: must be an object of service configs, got number",
		`"children": {}`:          "children: must be an array of entries, got object",
		`"isolate": {"r": false}`: "isolate.r: must be a realm label or true, got boolean",
	} {
		err := json.Unmarshal([]byte(`{"id": "x", `+field+`}`), &o)
		True(t, err != nil && strings.Contains(err.Error(), want), field+": "+errText(err))
	}

	NoError(t, json.Unmarshal([]byte(`{"id":"ok","name":"Values","disabled":{"$expr":"env.OFF"},"isolate":{"a":true,"b":"r"},"config":null}`), &o))
	Equal(t, "env.OFF", o.DisabledExpr)
	Equal(t, "true", o.Isolate["a"])
	Equal(t, "r", o.Isolate["b"])
	True(t, o.Config == nil, "a null config was kept")
	Equal(t, loader.Position{}, o.Position(), "an unmarshalled entry kept a source position")
}

// Dropping an operation that another follows removes its line, its comma and the
// comment trailing it; the comment above it and the rest of the file stay as they
// were written.
func TestEncodeLayerDropsAnOperationWithItsTrailingComment(t *testing.T) {
	doc, err := loader.ParseLayer(loader.JSONC, "user.jsonc", []byte(userLayer))
	NoError(t, err)
	out, err := loader.JSONC.(loader.LayerCodec).EncodeLayer(doc, doc.Ops[1:])
	NoError(t, err)
	Equal(t, "// user layer: local edits\n[\n  // raise b\n  { \"id\": \"other\", \"disabled\": false }\n]\n", string(out))
	again, err := loader.ParseLayer(loader.JSONC, "user.jsonc", out)
	NoError(t, err)
	Equal(t, 1, len(again.Ops))
	Equal(t, "other", again.Ops[0].ID)
}

// registrations gives each registration of this test binary its own name, so the
// tests also pass when run more than once in one process.
var registrations atomic.Int64

func uniqueName(prefix string) string { return fmt.Sprintf("%s%d", prefix, registrations.Add(1)) }

func panicText(fn func()) (text string) {
	defer func() { text = fmt.Sprint(recover()) }()
	fn()
	return "no panic"
}

// Register makes a component resolvable through Registered, once per name; a nil
// component or a second registration of a name panics, as a database/sql driver
// registration does.
func TestRegisterMakesAComponentResolvable(t *testing.T) {
	name := uniqueName("RegisteredEcho")
	_, ok := loader.Registered.Resolve(name)
	True(t, !ok, "an unregistered name resolved")
	component := Echo(&Log{})
	loader.Register(name, component)
	got, ok := loader.Registered.Resolve(name)
	True(t, ok && got == component, "the registered component did not resolve")
	True(t, strings.Contains(panicText(func() { loader.Register(name, Echo(&Log{})) }), "Register called twice for \""+name+"\""), "duplicate registration")
	True(t, strings.Contains(panicText(func() { loader.Register(uniqueName("Nil"), nil) }), "component is nil"), "nil registration")
	got, _ = loader.Registered.Resolve(name)
	True(t, got == component, "a refused registration replaced the component")
}

// RegisterCodec selects a codec by file extension, case-insensitively; an
// unregistered extension falls back to JSON, and a nil codec or a second
// registration of an extension panics.
func TestRegisterCodecSelectsByExtension(t *testing.T) {
	ext := "." + uniqueName("probe")
	True(t, loader.CodecFor("layer"+ext) == loader.JSON, "an unregistered extension did not fall back to JSON")
	loader.RegisterCodec(strings.ToUpper(ext), loader.JSONC)
	True(t, loader.CodecFor("dir/layer"+ext) == loader.JSONC, "the registered codec was not chosen")
	True(t, loader.CodecFor("LAYER"+strings.ToUpper(ext)) == loader.JSONC, "the extension match is case-sensitive")
	True(t, strings.Contains(panicText(func() { loader.RegisterCodec(ext, loader.JSON) }), "RegisterCodec called twice"), "duplicate codec")
	True(t, strings.Contains(panicText(func() { loader.RegisterCodec(".other", nil) }), "codec is nil"), "nil codec")
	True(t, loader.CodecFor("x.jsonc") == loader.JSONC && loader.CodecFor("x.json") == loader.JSON, "the built-in codecs changed")

	commented := []byte("[ // a comment the JSON codec refuses\n]")
	_, err := loader.ParseLayer(loader.JSON, "user.json", commented)
	True(t, err != nil, "the JSON codec accepted a comment")
	doc, err := loader.ParseLayer(nil, "user"+ext, commented)
	NoError(t, err)
	Equal(t, 0, len(doc.Ops))
}
