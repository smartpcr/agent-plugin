package protocol

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// InitializeMethod is the request that opens a connection (DH-ADR-SDK-3).
const InitializeMethod = "initialize"

// sdkError returns the error of reason, which must be a row of Errors.
func sdkError(reason, msg string) *jsonrpc.Error {
	for _, e := range Errors {
		if e.Reason == reason {
			return &jsonrpc.Error{Code: e.Code, Message: msg, Reason: reason}
		}
	}
	panic("protocol: reason not in the error table: " + reason)
}

// Negotiate picks the protocol version of a connection: the highest version that
// the client offered and the host serves. With none in common it fails with
// sdk/version-unsupported, whose details are the host's versions as a JSON array.
func Negotiate(offered, served []int) (int, error) {
	best, found := 0, false
	for _, v := range offered {
		if slices.Contains(served, v) && (!found || v > best) {
			best, found = v, true
		}
	}
	if found {
		return best, nil
	}
	hosts := slices.Compact(slices.Sorted(slices.Values(served)))
	if hosts == nil {
		hosts = []int{}
	}
	details, _ := json.Marshal(hosts)
	e := sdkError(ReasonVersionUnsupported, fmt.Sprintf("no offered version %v is served; the host serves %v", offered, hosts))
	e.Details = details
	return 0, e
}

// Gate refuses requests until initialize has succeeded, and refuses a second
// initialize, for one connection: make one Gate per connection. Requests other
// than initialize fail with sdk/not-initialized while initialize has not yet
// succeeded (also while it runs); initialize fails with sdk/already-initialized once
// one is running or has succeeded. An initialize whose handler fails leaves the
// connection uninitialized, so the client may try again.
type Gate struct {
	methods map[string]jsonrpc.Handler

	mu    sync.Mutex
	state gateState
}

type gateState uint8

const (
	gateClosed gateState = iota
	gateOpening
	gateOpen
)

// NewGate gates methods, which should hold the initialize handler: without one no
// request can ever pass.
func NewGate(methods map[string]jsonrpc.Handler) *Gate {
	return &Gate{methods: methods}
}

// Methods returns the gated handler map, for jsonrpc.Options.Methods. It holds the
// same names as the map given to NewGate.
func (g *Gate) Methods() map[string]jsonrpc.Handler {
	out := make(map[string]jsonrpc.Handler, len(g.methods))
	for name, h := range g.methods {
		if name == InitializeMethod {
			out[name] = g.initialize(h)
		} else {
			out[name] = g.after(name, h)
		}
	}
	return out
}

// Initialized reports whether initialize has succeeded on this connection.
func (g *Gate) Initialized() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state == gateOpen
}

func (g *Gate) initialize(h jsonrpc.Handler) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		g.mu.Lock()
		if g.state != gateClosed {
			g.mu.Unlock()
			return nil, sdkError(ReasonAlreadyInitialized, "initialize was already sent on this connection")
		}
		g.state = gateOpening
		g.mu.Unlock()
		result, err := h(ctx, params)
		g.mu.Lock()
		if err != nil {
			g.state = gateClosed
		} else {
			g.state = gateOpen
		}
		g.mu.Unlock()
		return result, err
	}
}

func (g *Gate) after(name string, h jsonrpc.Handler) jsonrpc.Handler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		if !g.Initialized() {
			return nil, sdkError(ReasonNotInitialized, name+" came before initialize succeeded")
		}
		return h(ctx, params)
	}
}

// Issue is one entry of the details of jsonrpc/invalid-params: the path of the
// offending value ("$" is the params object, "$.a.b[2]" a nested value) and what is
// wrong with it.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// InvalidParamsDetails is the details object of jsonrpc/invalid-params.
type InvalidParamsDetails struct {
	Issues []Issue `json:"issues"`
}

// DecodeParams decodes request params strictly into a T: an unknown field, a value
// of the wrong type, malformed JSON or trailing data fail with
// jsonrpc/invalid-params, whose details list every unknown field's path (or the
// one wrong value's path) as InvalidParamsDetails. Absent params decode as the zero T.
func DecodeParams[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(bytes.TrimSpace(raw)) == 0 {
		return v, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	if err == nil {
		if _, more := dec.Token(); !errors.Is(more, io.EOF) {
			err = errors.New("trailing data after the params value")
		}
	}
	if err != nil {
		var zero T
		return zero, invalidParams(issuesOf(raw, reflect.TypeFor[T](), err))
	}
	return v, nil
}

func invalidParams(issues []Issue) *jsonrpc.Error {
	details, _ := json.Marshal(InvalidParamsDetails{Issues: issues})
	parts := make([]string, len(issues))
	for i, is := range issues {
		parts[i] = is.Path + ": " + is.Message
	}
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeInvalidParams,
		Message: "the parameters do not decode: " + strings.Join(parts, "; "),
		Reason:  jsonrpc.ReasonInvalidParams,
		Details: details,
	}
}

// issuesOf turns a decode error into issues with paths.
func issuesOf(raw json.RawMessage, t reflect.Type, err error) []Issue {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		path := "$"
		if typeErr.Field != "" {
			path += "." + typeErr.Field
		}
		return []Issue{{Path: path, Message: "want " + typeErr.Type.String() + ", got " + typeErr.Value}}
	}
	if name, ok := strings.CutPrefix(err.Error(), `json: unknown field "`); ok {
		var doc any
		var paths []string
		if json.Unmarshal(raw, &doc) == nil {
			unknownFields(doc, t, "$", &paths)
		}
		if len(paths) == 0 {
			paths = []string{"$." + strings.TrimSuffix(name, `"`)}
		}
		issues := make([]Issue, len(paths))
		for i, p := range paths {
			issues[i] = Issue{Path: p, Message: "unknown field"}
		}
		return issues
	}
	return []Issue{{Path: "$", Message: err.Error()}}
}

var (
	rawMessageType  = reflect.TypeFor[json.RawMessage]()
	unmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	textType        = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// unknownFields appends the path of every object key in doc that t has no field
// for, matching names as encoding/json does (exactly, else case-insensitively).
// A value with its own decoding (json.RawMessage, an interface, an Unmarshaler) is
// not looked into.
func unknownFields(doc any, t reflect.Type, path string, out *[]string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == rawMessageType || t.Kind() == reflect.Interface ||
		reflect.PointerTo(t).Implements(unmarshalerType) || reflect.PointerTo(t).Implements(textType) {
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := doc.(map[string]any)
		if !ok {
			return
		}
		fields := map[string]reflect.Type{}
		jsonFields(t, fields)
		for _, key := range slices.Sorted(maps.Keys(obj)) {
			ft, ok := fields[key]
			if !ok {
				for name, typ := range fields {
					if strings.EqualFold(name, key) {
						ft, ok = typ, true
						break
					}
				}
			}
			if !ok {
				*out = append(*out, path+"."+key)
				continue
			}
			unknownFields(obj[key], ft, path+"."+key, out)
		}
	case reflect.Slice, reflect.Array:
		arr, _ := doc.([]any)
		for i, e := range arr {
			unknownFields(e, t.Elem(), fmt.Sprintf("%s[%d]", path, i), out)
		}
	case reflect.Map:
		obj, _ := doc.(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(obj)) {
			unknownFields(obj[key], t.Elem(), path+"."+key, out)
		}
	}
}

// jsonFields collects the wire names of t's fields, flattening embedded structs as
// encoding/json does.
func jsonFields(t reflect.Type, into map[string]reflect.Type) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, hasTag := f.Tag.Lookup("json")
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				jsonFields(et, into)
				continue
			}
		}
		if !f.IsExported() || name == "-" {
			continue
		}
		if !hasTag || name == "" {
			name = f.Name
		}
		into[name] = f.Type
	}
}
