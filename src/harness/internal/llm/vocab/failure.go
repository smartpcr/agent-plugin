package vocab

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Code names a failure: a closed table plus llm/http-<status>.
type Code string

// Class is what may act on a failure; it is a property of the code, so policy
// never reads message text.
type Class string

// The classes.
const (
	Cancelled  Class = "cancelled"
	Transient  Class = "transient"
	Repairable Class = "repairable"
	Permanent  Class = "permanent"
)

// The failure codes (docs/harness/areas/llm.md section 6.1).
const (
	CodeAborted                    Code = "llm/aborted"
	CodeRateLimit                  Code = "llm/rate-limit"
	CodeServer                     Code = "llm/server"
	CodeTimeout                    Code = "llm/timeout"
	CodeTransport                  Code = "llm/transport"
	CodeStreamClosed               Code = "llm/stream-closed"
	CodeMalformedResponse          Code = "llm/malformed-response"
	CodeEmptyResponse              Code = "llm/empty-response"
	CodeContextWindowExceeded      Code = "llm/context-window-exceeded"
	CodeImageOffloadRequired       Code = "llm/image-offload-required"
	CodeAttachmentUnreadable       Code = "llm/attachment-unreadable"
	CodeAuth                       Code = "llm/auth"
	CodeQuota                      Code = "llm/quota"
	CodeAccountQuota               Code = "llm/account-quota"
	CodeInvalidRequest             Code = "llm/invalid-request"
	CodeContentRefused             Code = "llm/content-refused"
	CodeMissingCredential          Code = "llm/missing-credential"
	CodeInvalidCredential          Code = "llm/invalid-credential"
	CodeSignInRequired             Code = "llm/sign-in-required"
	CodeTokenInvalid               Code = "llm/token-invalid"
	CodeNoRoute                    Code = "llm/no-route"
	CodeUnknownModel               Code = "llm/unknown-model"
	CodeModelUnsupported           Code = "llm/model-unsupported"
	CodeClientUpgradeRequired      Code = "llm/client-upgrade-required"
	CodeUnsupportedOption          Code = "llm/unsupported-option"
	CodeUnsupportedContent         Code = "llm/unsupported-content"
	CodeUnsupportedReasoningEffort Code = "llm/unsupported-reasoning-effort"
	CodeRequestExtension           Code = "llm/request-extension"
	CodeRequestHeaders             Code = "llm/request-headers"
)

var classes = map[Code]Class{
	CodeAborted:                    Cancelled,
	CodeRateLimit:                  Transient,
	CodeServer:                     Transient,
	CodeTimeout:                    Transient,
	CodeTransport:                  Transient,
	CodeStreamClosed:               Transient,
	CodeMalformedResponse:          Transient,
	CodeEmptyResponse:              Transient,
	CodeContextWindowExceeded:      Repairable,
	CodeImageOffloadRequired:       Repairable,
	CodeAttachmentUnreadable:       Repairable,
	CodeAuth:                       Permanent,
	CodeQuota:                      Permanent,
	CodeAccountQuota:               Permanent,
	CodeInvalidRequest:             Permanent,
	CodeContentRefused:             Permanent,
	CodeMissingCredential:          Permanent,
	CodeInvalidCredential:          Permanent,
	CodeSignInRequired:             Permanent,
	CodeTokenInvalid:               Permanent,
	CodeNoRoute:                    Permanent,
	CodeUnknownModel:               Permanent,
	CodeModelUnsupported:           Permanent,
	CodeClientUpgradeRequired:      Permanent,
	CodeUnsupportedOption:          Permanent,
	CodeUnsupportedContent:         Permanent,
	CodeUnsupportedReasoningEffort: Permanent,
	CodeRequestExtension:           Permanent,
	CodeRequestHeaders:             Permanent,
}

const httpPrefix = "llm/http-"

// HTTPCode is the code of an HTTP status the closed table does not name.
func HTTPCode(status int) Code { return Code(httpPrefix + strconv.Itoa(status)) }

// ClassOf is the class of a code, false when the code is not in the set. An
// llm/http-<status> code is Permanent for 4xx and Transient for 5xx.
func ClassOf(c Code) (Class, bool) {
	if cl, ok := classes[c]; ok {
		return cl, true
	}
	if digits, ok := strings.CutPrefix(string(c), httpPrefix); ok && len(digits) == 3 && digits[0] != '0' {
		status, err := strconv.Atoi(digits)
		switch {
		case err != nil:
		case status >= 400 && status < 500:
			return Permanent, true
		case status >= 500 && status < 600:
			return Transient, true
		}
	}
	return "", false
}

// Codes lists the closed table, without the HTTP family.
func Codes() []Code {
	out := make([]Code, 0, len(classes))
	for c := range classes {
		out = append(out, c)
	}
	return out
}

// Failure is a classified model-call failure.
type Failure struct {
	Code       Code          `json:"code"`
	Class      Class         `json:"class" dsh:"enum=cancelled|transient|repairable|permanent"`
	Message    string        `json:"message"`
	Status     int           `json:"status,omitempty"`      // HTTP status, 0 when none
	RetryAfter time.Duration `json:"retry_after,omitempty"` // the provider's request, validated; not a decision
	RequestID  string        `json:"request_id,omitempty"`
}

// NewFailure is a failure of code, classed from the code.
func NewFailure(code Code, message string) (*Failure, error) {
	cl, ok := ClassOf(code)
	if !ok {
		return nil, fmt.Errorf("vocab: failure code %q is not in the closed table", code)
	}
	status, _ := httpStatus(code)
	return &Failure{Code: code, Class: cl, Message: message, Status: status}, nil
}

// httpStatus is the status an llm/http-<status> code carries.
func httpStatus(c Code) (int, bool) {
	digits, ok := strings.CutPrefix(string(c), httpPrefix)
	if !ok {
		return 0, false
	}
	status, err := strconv.Atoi(digits)
	return status, err == nil
}

func (f *Failure) Error() string {
	if f.Message == "" {
		return string(f.Code)
	}
	return string(f.Code) + ": " + f.Message
}

// Validate checks that the code is known, its class is the code's, and an
// llm/http-<status> code records that status. Status is 0 (none) or 100..599.
func (f Failure) Validate() error {
	cl, ok := ClassOf(f.Code)
	switch {
	case !ok:
		return fmt.Errorf("vocab: failure code %q is not in the closed table", f.Code)
	case cl != f.Class:
		return fmt.Errorf("vocab: failure %s has class %q; the code's class is %q", f.Code, f.Class, cl)
	case (f.Status != 0 && f.Status < 100) || f.Status > 599 || f.RetryAfter < 0:
		return fmt.Errorf("vocab: failure %s has status %d or retry-after %v out of range", f.Code, f.Status, f.RetryAfter)
	}
	if status, ok := httpStatus(f.Code); ok && f.Status != status {
		return fmt.Errorf("vocab: failure %s has status %d; the code's status is %d", f.Code, f.Status, status)
	}
	return nil
}
