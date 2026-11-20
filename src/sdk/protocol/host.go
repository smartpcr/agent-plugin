package protocol

import "encoding/json"

// Scope names the session a host notification or request concerns, its root and,
// for a child session, its parent.
type Scope struct {
	SessionID       string `json:"sessionId"`
	RootSessionID   string `json:"rootSessionId"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
}

// ApprovalRequest asks the client to allow one tool call (capability approvals).
type ApprovalRequest struct {
	InteractionID string `json:"interactionId"`
	SessionID     string `json:"sessionId"`
	RootSessionID string `json:"rootSessionId"`
	ToolName      string `json:"toolName"`
	CallID        string `json:"callId,omitempty"`
	Origin        string `json:"origin"`
	Reason        string `json:"reason,omitempty"`
}

// ApprovalResult answers one call only; it never creates a session grant.
type ApprovalResult struct {
	Outcome string `json:"outcome" dsh:"enum=allowed-once|rejected|cancelled"`
}

// QuestionOption is one choice of a question.
type QuestionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Question is one question of question/ask.
type Question struct {
	ID          string           `json:"id"`
	Text        string           `json:"text"`
	Options     []QuestionOption `json:"options,omitempty"`
	Multiple    bool             `json:"multiple,omitempty"`
	AllowCustom bool             `json:"allowCustom,omitempty"`
}

// QuestionRequest asks the user questions (capability questions).
type QuestionRequest struct {
	InteractionID string     `json:"interactionId"`
	SessionID     string     `json:"sessionId"`
	RootSessionID string     `json:"rootSessionId"`
	Questions     []Question `json:"questions"`
}

// Answer answers one question.
type Answer struct {
	ID       string   `json:"id"`
	Selected []string `json:"selected"`
	Custom   string   `json:"custom,omitempty"`
}

// QuestionResult names every question id exactly once, or is a dismissal; a
// dismissal is never an approval.
type QuestionResult struct {
	Answers   []Answer `json:"answers,omitempty"`
	Dismissed bool     `json:"dismissed,omitempty"`
}

// ToolCallParams call a tool the client declared.
type ToolCallParams struct {
	CallID        string          `json:"callId"`
	SessionID     string          `json:"sessionId"`
	RootSessionID string          `json:"rootSessionId"`
	Name          string          `json:"name"`
	Arguments     json.RawMessage `json:"arguments"`
}

// ToolCallResult is the client tool's answer.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// SessionEventNotification carries one committed log event.
type SessionEventNotification struct {
	Scope
	Event Event `json:"event"`
}

// SessionStatusNotification is a whole-agent transition.
type SessionStatusNotification struct {
	Scope
	Status string `json:"status" dsh:"enum=idle|running"`
}

// RunRef names the prompt or command a run result belongs to.
type RunRef struct {
	Kind      string `json:"kind" dsh:"enum=prompt|command"`
	MessageID string `json:"messageId,omitempty"`
	CommandID string `json:"commandId,omitempty"`
}

// RunCompletedNotification is the result of one run (DH-ADR-SDK-5).
type RunCompletedNotification struct {
	Scope
	Run           RunRef `json:"run"`
	Turn          int64  `json:"turn,omitempty"`
	FinishReason  string `json:"finishReason"`
	FinalResponse string `json:"finalResponse"`
	FirstSeq      int64  `json:"firstSeq"`
	LastSeq       int64  `json:"lastSeq"`
}

// StreamNotification is opt-in live text of the attempt in flight; transient.
type StreamNotification struct {
	Scope
	Turn    int64  `json:"turn"`
	Step    int64  `json:"step"`
	Attempt int64  `json:"attempt"`
	Kind    string `json:"kind" dsh:"enum=start|chunk|end"`
	Index   int    `json:"index"`
	Text    string `json:"text,omitempty"`
}

// SessionClosedNotification reports an owned session closed for a reason other than
// this client's session/close.
type SessionClosedNotification struct {
	Scope
	Cause string `json:"cause"`
}

// SubagentStartedNotification is a new edge of the session tree; Scope names the
// child and its parent.
type SubagentStartedNotification struct {
	Scope
}

// SubagentFinishedNotification is a child's result.
type SubagentFinishedNotification struct {
	Scope
	StopReason           string `json:"stopReason"`
	LastAssistantMessage string `json:"lastAssistantMessage,omitempty"`
}
