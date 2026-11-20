package protocol

import "encoding/json"

// Implementation names a client or a host.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ModelRoute selects a model by exact id; there is no fallback.
type ModelRoute struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
}

// ToolDeclaration is a tool the client implements (DH-D-SDK-7); the host calls it
// with tool/call.
type ToolDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ClientCapabilities are what the client answers; the host sends a host request
// only when its capability is advertised.
type ClientCapabilities struct {
	Approvals bool              `json:"approvals"`
	Questions bool              `json:"questions"`
	Stream    bool              `json:"stream"`
	Tools     []ToolDeclaration `json:"tools,omitempty"`
}

// Defaults apply to every session the connection creates.
type Defaults struct {
	Cwd            string      `json:"cwd,omitempty"`
	Model          *ModelRoute `json:"model,omitempty"`
	AgentPreset    string      `json:"agentPreset,omitempty"`
	PermissionMode string      `json:"permissionMode,omitempty"`
}

// InitializeParams open a connection; initialize is sent once, first.
type InitializeParams struct {
	ProtocolVersions []int              `json:"protocolVersions"`
	ClientInfo       Implementation     `json:"clientInfo"`
	TimeZone         string             `json:"timeZone,omitempty" jsonschema:"the user's IANA zone name for this connection's sessions"`
	Capabilities     ClientCapabilities `json:"capabilities"`
	Defaults         *Defaults          `json:"defaults,omitempty"`
}

// HostCapabilities are what the host offers.
type HostCapabilities struct {
	Images      bool     `json:"images"`
	Commands    []string `json:"commands"`
	ClientTools bool     `json:"clientTools"`
}

// Limits are the host's frame bounds.
type Limits struct {
	MaxFrameBytes  int64 `json:"maxFrameBytes"`
	MaxInlineBytes int64 `json:"maxInlineBytes"`
}

// InitializeResult answers initialize once boot reports the tree ready.
type InitializeResult struct {
	ProtocolVersion int              `json:"protocolVersion"`
	ServerInfo      Implementation   `json:"serverInfo"`
	Capabilities    HostCapabilities `json:"capabilities"`
	Limits          Limits           `json:"limits"`
}

// SessionCreateParams create a session; session/create is the only method that does.
type SessionCreateParams struct {
	SessionID      string      `json:"sessionId,omitempty"`
	Cwd            string      `json:"cwd,omitempty"`
	Model          *ModelRoute `json:"model,omitempty"`
	AgentPreset    string      `json:"agentPreset,omitempty"`
	PermissionMode string      `json:"permissionMode,omitempty"`
	TimeZone       string      `json:"timeZone,omitempty"`
}

// SessionCreateResult names the new session and the watermark its events start after.
type SessionCreateResult struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	LastSeq   int64  `json:"lastSeq"`
}

// SessionResumeParams take the write lease of a stored session.
type SessionResumeParams struct {
	SessionID      string      `json:"sessionId"`
	PermissionMode string      `json:"permissionMode,omitempty"`
	Model          *ModelRoute `json:"model,omitempty"`
}

// SessionResumeResult answers session/resume.
type SessionResumeResult struct {
	SessionID             string `json:"sessionId"`
	Cwd                   string `json:"cwd"`
	LastSeq               int64  `json:"lastSeq"`
	ClosedInterruptedTurn bool   `json:"closedInterruptedTurn"`
}

// SessionListParams page through stored root sessions, newest first.
type SessionListParams struct {
	Cwd    string `json:"cwd,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// SessionSummary is one stored root session.
type SessionSummary struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt" jsonschema:"RFC 3339 time of the last event"`
}

// SessionListResult is one page of sessions.
type SessionListResult struct {
	Sessions   []SessionSummary `json:"sessions"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// ContentBlock is one block of prompt or tool content: text, an inline raster image,
// or a reference to a stored attachment.
type ContentBlock struct {
	Type         string `json:"type" dsh:"enum=text|image|reference"`
	Text         string `json:"text,omitempty"`
	Data         string `json:"data,omitempty" jsonschema:"base64 image bytes"`
	MimeType     string `json:"mimeType,omitempty"`
	Name         string `json:"name,omitempty"`
	AttachmentID string `json:"attachmentId,omitempty"`
}

// SessionPromptParams queue a message.
type SessionPromptParams struct {
	SessionID string         `json:"sessionId"`
	Content   []ContentBlock `json:"content"`
	Delivery  string         `json:"delivery,omitempty" dsh:"enum=follow-up|steer"`
	MessageID string         `json:"messageId,omitempty" jsonschema:"unique over the session's history; a repeat appends nothing"`
}

// SessionPromptResult is the receipt of a queued message.
type SessionPromptResult struct {
	MessageID string `json:"messageId"`
	Seq       int64  `json:"seq"`
}

// SessionQueryParams queue a message or run a recognised command.
type SessionQueryParams struct {
	SessionID string         `json:"sessionId"`
	Content   []ContentBlock `json:"content"`
}

// SessionQueryResult is a prompt receipt or a command's result.
type SessionQueryResult struct {
	Kind      string          `json:"kind" dsh:"enum=prompt|command"`
	MessageID string          `json:"messageId,omitempty"`
	Seq       int64           `json:"seq,omitempty"`
	CommandID string          `json:"commandId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

// SessionRef names one session.
type SessionRef struct {
	SessionID string `json:"sessionId"`
}

// LastSeqResult carries the session's watermark.
type LastSeqResult struct {
	LastSeq int64 `json:"lastSeq"`
}

// SessionCancelParams cancel the running turn or step.
type SessionCancelParams struct {
	SessionID string `json:"sessionId"`
	Scope     string `json:"scope,omitempty" dsh:"enum=turn|step"`
	Inbox     string `json:"inbox" dsh:"enum=keep|discard"`
}

// SessionCancelResult answers after the cancelled turn's turn/end.
type SessionCancelResult struct {
	Cancelled bool  `json:"cancelled"`
	LastSeq   int64 `json:"lastSeq"`
}

// SessionHistoryParams page through the stored events of any session.
type SessionHistoryParams struct {
	SessionID string   `json:"sessionId"`
	AfterSeq  int64    `json:"afterSeq,omitempty"`
	Limit     int      `json:"limit,omitempty" dsh:"min=1,max=5000"`
	Types     []string `json:"types,omitempty"`
}

// Event is one committed log event; Data is the committed payload, byte for byte.
type Event struct {
	Seq       int64           `json:"seq"`
	Time      string          `json:"time"`
	Type      string          `json:"type"`
	Version   int             `json:"version"`
	Ignorable bool            `json:"ignorable"`
	Sources   []string        `json:"sources,omitempty"`
	Data      json.RawMessage `json:"data"`
}

// SessionHistoryResult is one page of events.
type SessionHistoryResult struct {
	Events  []Event `json:"events"`
	LastSeq int64   `json:"lastSeq"`
	More    bool    `json:"more"`
}

// ModelSetParams select the model from the next step.
type ModelSetParams struct {
	SessionID string     `json:"sessionId"`
	Model     ModelRoute `json:"model"`
}

// ModelSetResult names the selected model.
type ModelSetResult struct {
	Model ModelRoute `json:"model"`
}

// WorkingDirectorySetParams change a session's working directory.
type WorkingDirectorySetParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
}

// WorkingDirectoryResult names a session's working directory.
type WorkingDirectoryResult struct {
	Cwd string `json:"cwd"`
}

// Empty is the parameters or result of a method that carries none.
type Empty struct{}

// CancelParams name the request $/cancel cancels.
type CancelParams struct {
	ID json.RawMessage `json:"id" jsonschema:"the id of the request to cancel, a number or a string"`
}
