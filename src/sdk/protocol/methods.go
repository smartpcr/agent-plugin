package protocol

import (
	"reflect"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// Version is the SDK protocol version this package describes.
const Version = 1

// Direction says who sends a method.
type Direction string

// The directions of a method.
const (
	FromClient Direction = "client-to-host"
	FromHost   Direction = "host-to-client"
	Both       Direction = "both"
)

// Kind says whether a method is answered.
type Kind string

// The kinds of a method.
const (
	Request      Kind = "request"
	Notification Kind = "notification"
)

// Method is one row of the method table. Requests use "/", notifications ".".
type Method struct {
	Name       string
	Direction  Direction
	Kind       Kind
	Params     reflect.Type
	Result     reflect.Type // nil for a notification
	Capability string       // the client capability a host request needs, or ""
	Since      int          // the first protocol version with the method
}

func req[P, R any](name string, dir Direction, capability string) Method {
	return Method{Name: name, Direction: dir, Kind: Request, Params: reflect.TypeFor[P](), Result: reflect.TypeFor[R](), Capability: capability, Since: 1}
}

func note[P any](name string, dir Direction, capability string) Method {
	return Method{Name: name, Direction: dir, Kind: Notification, Params: reflect.TypeFor[P](), Capability: capability, Since: 1}
}

// Methods is the method table of protocol version 1 (sdk-acp-typert.md §6.2), in
// documentation order.
var Methods = []Method{
	req[InitializeParams, InitializeResult]("initialize", FromClient, ""),
	req[SessionCreateParams, SessionCreateResult]("session/create", FromClient, ""),
	req[SessionResumeParams, SessionResumeResult]("session/resume", FromClient, ""),
	req[SessionListParams, SessionListResult]("session/list", FromClient, ""),
	req[SessionPromptParams, SessionPromptResult]("session/prompt", FromClient, ""),
	req[SessionQueryParams, SessionQueryResult]("session/query", FromClient, ""),
	req[SessionRef, LastSeqResult]("session/wait", FromClient, ""),
	req[SessionCancelParams, SessionCancelResult]("session/cancel", FromClient, ""),
	req[SessionRef, LastSeqResult]("session/close", FromClient, ""),
	req[SessionHistoryParams, SessionHistoryResult]("session/history", FromClient, ""),
	req[ModelSetParams, ModelSetResult]("session/model/set", FromClient, ""),
	req[SessionRef, WorkingDirectoryResult]("session/working-directory/get", FromClient, ""),
	req[WorkingDirectorySetParams, WorkingDirectoryResult]("session/working-directory/set", FromClient, ""),
	req[Empty, Empty]("shutdown", FromClient, ""),
	note[CancelParams](jsonrpc.CancelMethod, Both, ""),

	req[ApprovalRequest, ApprovalResult]("approval/request", FromHost, "approvals"),
	req[QuestionRequest, QuestionResult]("question/ask", FromHost, "questions"),
	req[ToolCallParams, ToolCallResult]("tool/call", FromHost, "tools"),

	note[SessionEventNotification]("session.event", FromHost, ""),
	note[SessionStatusNotification]("session.status", FromHost, ""),
	note[RunCompletedNotification]("session.run.completed", FromHost, ""),
	note[StreamNotification]("session.stream", FromHost, "stream"),
	note[SessionClosedNotification]("session.closed", FromHost, ""),
	note[SubagentStartedNotification]("subagent.started", FromHost, ""),
	note[SubagentFinishedNotification]("subagent.finished", FromHost, ""),
}

// Lookup returns the row of method name.
func Lookup(name string) (Method, bool) {
	for _, m := range Methods {
		if m.Name == name {
			return m, true
		}
	}
	return Method{}, false
}

// The reasons of the SDK area, all sent with jsonrpc.CodeApplication.
const (
	ReasonNotInitialized     = "sdk/not-initialized"
	ReasonAlreadyInitialized = "sdk/already-initialized"
	ReasonVersionUnsupported = "sdk/version-unsupported"
	ReasonSessionNotOwned    = "sdk/session-not-owned"
	ReasonSessionLive        = "sdk/session-live"
	ReasonSubagentSession    = "sdk/subagent-session"
	ReasonInvalidContent     = "sdk/invalid-content"
	ReasonShuttingDown       = "sdk/shutting-down"
)

// Errors is the error table of the protocol: the codec's table followed by the SDK
// area's reasons. Other owners' reasons (session/not-found, store/busy...) pass
// through unchanged with jsonrpc.CodeApplication.
var Errors = append(append([]jsonrpc.Entry(nil), jsonrpc.Table...),
	jsonrpc.Entry{Reason: ReasonNotInitialized, Code: jsonrpc.CodeApplication, When: "a request other than initialize came first"},
	jsonrpc.Entry{Reason: ReasonAlreadyInitialized, Code: jsonrpc.CodeApplication, When: "initialize was sent twice"},
	jsonrpc.Entry{Reason: ReasonVersionUnsupported, Code: jsonrpc.CodeApplication, When: "no offered protocol version is supported; details list the host's"},
	jsonrpc.Entry{Reason: ReasonSessionNotOwned, Code: jsonrpc.CodeApplication, When: "the session is not owned by this connection"},
	jsonrpc.Entry{Reason: ReasonSessionLive, Code: jsonrpc.CodeApplication, When: "the session is live in this process"},
	jsonrpc.Entry{Reason: ReasonSubagentSession, Code: jsonrpc.CodeApplication, When: "the session is a subagent's and cannot be resumed"},
	jsonrpc.Entry{Reason: ReasonInvalidContent, Code: jsonrpc.CodeApplication, When: "a content block is malformed or not admitted"},
	jsonrpc.Entry{Reason: ReasonShuttingDown, Code: jsonrpc.CodeApplication, When: "the host is shutting down"},
)
