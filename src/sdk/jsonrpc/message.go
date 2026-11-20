package jsonrpc

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Version is the only JSON-RPC version the codec speaks.
const Version = "2.0"

type idKind uint8

const (
	idNull idKind = iota
	idNumber
	idString
)

// ID is a request id: an integer, a string, or null (the zero value; only a response
// to a frame whose id could not be read carries it).
type ID struct {
	kind idKind
	num  int64
	str  string
}

// Int64ID returns the integer id n.
func Int64ID(n int64) ID { return ID{kind: idNumber, num: n} }

// StringID returns the string id s.
func StringID(s string) ID { return ID{kind: idString, str: s} }

// IsNull reports whether id is the null id.
func (id ID) IsNull() bool { return id.kind == idNull }

// String returns the id as it appears on the wire.
func (id ID) String() string {
	b, _ := id.MarshalJSON()
	return string(b)
}

// MarshalJSON writes the id.
func (id ID) MarshalJSON() ([]byte, error) {
	switch id.kind {
	case idNumber:
		return strconv.AppendInt(nil, id.num, 10), nil
	case idString:
		return json.Marshal(id.str)
	}
	return []byte("null"), nil
}

// parseID reads an id: an integer that fits int64, a string, or null.
func parseID(raw json.RawMessage) (ID, bool) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0:
		return ID{}, false
	case string(raw) == "null":
		return ID{}, true
	case raw[0] == '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ID{}, false
		}
		return StringID(s), true
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return ID{}, false
	}
	return Int64ID(n), true
}

// Frame is one decoded line: *Request, *Notification or *Response.
type Frame interface{ isFrame() }

// Request asks the peer to run Method and answer with a Response of the same ID.
type Request struct {
	ID     ID
	Method string
	Params json.RawMessage // an object or an array; nil when absent
}

// Notification asks the peer to run Method and expects no answer.
type Notification struct {
	Method string
	Params json.RawMessage
}

// Response settles the request with the same ID: exactly one of Result and Error.
type Response struct {
	ID     ID
	Result json.RawMessage
	Error  *Error
}

func (*Request) isFrame()      {}
func (*Notification) isFrame() {}
func (*Response) isFrame()     {}

// envelope holds the members of one frame object, each nil when absent.
type envelope struct {
	jsonrpc, id, method, params, result, error json.RawMessage
}

func (e *envelope) slot(key string) *json.RawMessage {
	switch key {
	case "jsonrpc":
		return &e.jsonrpc
	case "id":
		return &e.id
	case "method":
		return &e.method
	case "params":
		return &e.params
	case "result":
		return &e.result
	case "error":
		return &e.error
	}
	return nil
}

func invalid(msg string) error { return newError(ReasonInvalidRequest, msg) }

// Decode parses one line (without its newline) into a frame. The checks are strict:
// the line is exactly one JSON object with the members of JSON-RPC 2.0 and no other,
// none repeated. A frame with both "id" and "method" is always a request, so it can
// never settle a pending call. Every failure is an *Error of Table:
// jsonrpc/parse-error when the line is not one JSON object, jsonrpc/invalid-request
// for a batch or a wrong envelope.
func Decode(line []byte) (Frame, error) {
	line = bytes.TrimSpace(line)
	if !json.Valid(line) {
		return nil, newError(ReasonParseError, "the line is not JSON")
	}
	switch line[0] {
	case '[':
		return nil, invalid("batches are not accepted")
	case '{':
	default:
		return nil, newError(ReasonParseError, "the line is not a JSON object")
	}
	env, err := readEnvelope(line)
	if err != nil {
		return nil, err
	}
	var version string
	if env.jsonrpc == nil || json.Unmarshal(env.jsonrpc, &version) != nil || version != Version {
		return nil, invalid(`"jsonrpc" must be "2.0"`)
	}
	var id ID
	if env.id != nil {
		var ok bool
		if id, ok = parseID(env.id); !ok {
			return nil, invalid(`"id" must be an integer, a string or null`)
		}
	}
	if env.method != nil {
		return decodeCall(env, id)
	}
	return decodeResponse(env, id)
}

// readEnvelope walks the members of the object in line, which json.Valid accepted.
func readEnvelope(line []byte) (*envelope, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	if _, err := dec.Token(); err != nil { // '{'
		return nil, newError(ReasonParseError, "the line is not a JSON object")
	}
	env := &envelope{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, newError(ReasonParseError, "unreadable member name")
		}
		key, _ := tok.(string)
		slot := env.slot(key)
		if slot == nil {
			return nil, invalid("unknown member " + strconv.Quote(key))
		}
		if *slot != nil {
			return nil, invalid("repeated member " + strconv.Quote(key))
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, newError(ReasonParseError, "unreadable member "+strconv.Quote(key))
		}
		*slot = raw
	}
	return env, nil
}

func isStructured(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && (raw[0] == '{' || raw[0] == '[')
}

func decodeCall(env *envelope, id ID) (Frame, error) {
	var method string
	if json.Unmarshal(env.method, &method) != nil || method == "" {
		return nil, invalid(`"method" must be a non-empty string`)
	}
	if env.result != nil || env.error != nil {
		return nil, invalid(`a request or notification carries no "result" or "error"`)
	}
	if env.params != nil && !isStructured(env.params) {
		return nil, invalid(`"params" must be an object or an array`)
	}
	if env.id == nil {
		return &Notification{Method: method, Params: env.params}, nil
	}
	if id.IsNull() {
		return nil, invalid(`a request id is never null`)
	}
	return &Request{ID: id, Method: method, Params: env.params}, nil
}

func decodeResponse(env *envelope, id ID) (Frame, error) {
	if env.id == nil {
		return nil, invalid(`a frame without "method" is a response and needs "id"`)
	}
	if env.params != nil {
		return nil, invalid(`a response carries no "params"`)
	}
	if (env.result == nil) == (env.error == nil) {
		return nil, invalid(`a response carries exactly one of "result" and "error"`)
	}
	if env.result != nil {
		if id.IsNull() {
			return nil, invalid(`a result is never sent to the null id`)
		}
		return &Response{ID: id, Result: env.result}, nil
	}
	e, err := decodeError(env.error)
	if err != nil {
		return nil, err
	}
	return &Response{ID: id, Error: e}, nil
}

// decodeError reads an error object strictly: an integer code, a string message and
// an optional data object whose code is a "<domain>/<reason>".
func decodeError(raw json.RawMessage) (*Error, error) {
	var w struct {
		Code    *int            `json:"code"`
		Message *string         `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&w) != nil || w.Code == nil || w.Message == nil {
		return nil, invalid(`"error" must be an object with an integer "code" and a string "message"`)
	}
	e := &Error{Code: *w.Code, Message: *w.Message}
	if w.Data == nil || string(bytes.TrimSpace(w.Data)) == "null" {
		e.Reason = defaultReason(e.Code)
		return e, nil
	}
	var d struct {
		Code    *string         `json:"code"`
		Details json.RawMessage `json:"details"`
	}
	dec = json.NewDecoder(bytes.NewReader(w.Data))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil || d.Code == nil || !ValidReason(*d.Code) {
		return nil, invalid(`"error.data" must be an object whose "code" is "<domain>/<reason>"`)
	}
	e.Reason, e.Details = *d.Code, d.Details
	return e, nil
}

// Encode returns the wire form of f, without the newline. It refuses a frame Decode
// would refuse, so the codec never writes a line its peer cannot read.
func Encode(f Frame) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"jsonrpc":"2.0"`)
	switch f := f.(type) {
	case *Request:
		if f.ID.IsNull() {
			return nil, invalid("a request id is never null")
		}
		b.WriteString(`,"id":`)
		b.WriteString(f.ID.String())
		if err := writeCall(&b, f.Method, f.Params); err != nil {
			return nil, err
		}
	case *Notification:
		if err := writeCall(&b, f.Method, f.Params); err != nil {
			return nil, err
		}
	case *Response:
		b.WriteString(`,"id":`)
		b.WriteString(f.ID.String())
		if f.Error != nil {
			if f.Result != nil {
				return nil, invalid(`a response carries exactly one of "result" and "error"`)
			}
			// Mirror decodeError: a data object, once present, must name a valid reason.
			if f.Error.Reason == "" && len(f.Error.Details) > 0 {
				return nil, invalid(`an error with "details" must carry a "<domain>/<reason>" reason`)
			}
			if f.Error.Reason != "" && !ValidReason(f.Error.Reason) {
				return nil, invalid(`an error reason must be "<domain>/<reason>"`)
			}
			raw, err := json.Marshal(f.Error)
			if err != nil {
				return nil, err
			}
			b.WriteString(`,"error":`)
			b.Write(raw)
		} else {
			if f.ID.IsNull() {
				return nil, invalid("a result is never sent to the null id")
			}
			result := f.Result
			if result == nil {
				result = json.RawMessage("null")
			}
			if err := writeCompact(&b, `,"result":`, result); err != nil {
				return nil, err
			}
		}
	default:
		return nil, invalid("not a frame")
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func writeCall(b *bytes.Buffer, method string, params json.RawMessage) error {
	if method == "" {
		return invalid(`"method" must be a non-empty string`)
	}
	m, _ := json.Marshal(method)
	b.WriteString(`,"method":`)
	b.Write(m)
	if params == nil {
		return nil
	}
	if !isStructured(params) {
		return invalid(`"params" must be an object or an array`)
	}
	return writeCompact(b, `,"params":`, params)
}

// writeCompact writes prefix and raw with insignificant whitespace (and so every
// newline) removed: one frame is one line.
func writeCompact(b *bytes.Buffer, prefix string, raw json.RawMessage) error {
	b.WriteString(prefix)
	if err := json.Compact(b, raw); err != nil {
		return newError(ReasonInvalidParams, "the value is not JSON")
	}
	return nil
}
