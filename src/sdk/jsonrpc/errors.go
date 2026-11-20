package jsonrpc

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// JSON-RPC 2.0 error codes used by the table. A protocol fault uses its JSON-RPC
// code; every other failure uses CodeApplication and is told apart by its reason,
// never by message text.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeCancelled      = -32800
	CodeApplication    = -32000
)

// The reasons of the codec's own domain. Every reason is "<domain>/<reason>"; other
// packages declare their own domains and reach the wire through an error with a
// Reason() string method.
const (
	ReasonParseError      = "jsonrpc/parse-error"
	ReasonInvalidRequest  = "jsonrpc/invalid-request"
	ReasonMethodNotFound  = "jsonrpc/method-not-found"
	ReasonInvalidParams   = "jsonrpc/invalid-params"
	ReasonInternal        = "jsonrpc/internal"
	ReasonCancelled       = "jsonrpc/cancelled"
	ReasonBusy            = "jsonrpc/busy"
	ReasonLineTooLong     = "jsonrpc/line-too-long"
	ReasonTooManyPending  = "jsonrpc/too-many-pending"
	ReasonClosed          = "jsonrpc/closed"
	ReasonUnknownResponse = "jsonrpc/unknown-response"
)

// Entry is one row of the error table.
type Entry struct {
	Reason string // "<domain>/<reason>"
	Code   int    // the JSON-RPC code sent with it
	When   string // what the reason means
}

// Table is the one error table of the codec. It lists every reason the codec itself
// produces; the order is the documentation order.
var Table = []Entry{
	{ReasonParseError, CodeParseError, "a line is not one JSON object; the connection continues"},
	{ReasonInvalidRequest, CodeInvalidRequest, "a wrong envelope or a batch; the connection continues"},
	{ReasonMethodNotFound, CodeMethodNotFound, "the method is not in the allow-list"},
	{ReasonInvalidParams, CodeInvalidParams, "the parameters do not decode"},
	{ReasonInternal, CodeInternal, "an unclassified failure; no internal text crosses the wire"},
	{ReasonCancelled, CodeCancelled, "the request was cancelled by $/cancel"},
	{ReasonBusy, CodeApplication, "too many requests in flight"},
	{ReasonLineTooLong, CodeApplication, "a line over the line bound; the connection is then closed"},
	{ReasonTooManyPending, CodeApplication, "a call over the bound of pending outbound requests"},
	{ReasonClosed, CodeApplication, "the connection is closed; every pending call fails with it once"},
	{ReasonUnknownResponse, CodeApplication, "a response names no pending request"},
}

// Lookup returns the table row of reason.
func Lookup(reason string) (Entry, bool) {
	for _, e := range Table {
		if e.Reason == reason {
			return e, true
		}
	}
	return Entry{}, false
}

// reasonPattern is the shape of every reason: "<domain>/<reason>", lower case.
var reasonPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*/[a-z0-9][a-z0-9-]*$`)

// ValidReason reports whether s has the "<domain>/<reason>" form.
func ValidReason(s string) bool { return reasonPattern.MatchString(s) }

// Error is a JSON-RPC error with a reason from a "<domain>/<reason>" table. On the
// wire it is {"code", "message", "data": {"code": Reason, "details": Details}}.
type Error struct {
	Code    int
	Message string
	Reason  string
	Details json.RawMessage
}

// newError returns the codec error of reason with message msg. reason must be in
// Table.
func newError(reason, msg string) *Error {
	e, ok := Lookup(reason)
	if !ok {
		panic("jsonrpc: reason not in the table: " + reason)
	}
	return &Error{Code: e.Code, Message: msg, Reason: reason}
}

// Error implements error.
func (e *Error) Error() string {
	if e.Message == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Message
}

// Is matches another *Error with the same reason, so errors.Is(err, &Error{Reason: r})
// tests the reason.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Reason == e.Reason
}

// HasReason reports whether err is, or wraps, an *Error with reason.
func HasReason(err error, reason string) bool {
	var e *Error
	return errors.As(err, &e) && e.Reason == reason
}

type wireErrorData struct {
	Code    string          `json:"code"`
	Details json.RawMessage `json:"details,omitempty"`
}

type wireError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    *wireErrorData `json:"data,omitempty"`
}

// MarshalJSON writes the wire form.
func (e *Error) MarshalJSON() ([]byte, error) {
	w := wireError{Code: e.Code, Message: e.Message}
	if e.Reason != "" || len(e.Details) > 0 {
		w.Data = &wireErrorData{Code: e.Reason, Details: e.Details}
	}
	return json.Marshal(w)
}

// defaultReason is the reason given to a peer's error that carries no data.code.
func defaultReason(code int) string {
	for _, e := range Table {
		if e.Code == code && code != CodeApplication {
			return e.Reason
		}
	}
	return ReasonInternal
}

// reasoner is any error that names its own "<domain>/<reason>".
type reasoner interface{ Reason() string }

// toWire converts a handler's error into the error sent to the peer. An *Error is
// sent as it is; an error with a valid Reason() is sent with CodeApplication and its
// text; anything else becomes jsonrpc/internal with no internal text.
func toWire(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var r reasoner
	if errors.As(err, &r) {
		if reason := r.Reason(); ValidReason(reason) {
			msg := err.Error()
			if after, ok := strings.CutPrefix(msg, reason+": "); ok {
				msg = after
			}
			return &Error{Code: CodeApplication, Message: msg, Reason: reason}
		}
	}
	return newError(ReasonInternal, "internal error")
}
