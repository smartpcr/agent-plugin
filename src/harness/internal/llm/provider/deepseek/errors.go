package deepseek

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/transport"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/wire/anthropic"
)

// vendorBody is DeepSeek's own error envelope, {"error":{"message","type","code"}},
// beside the Messages envelope the anthropic driver reads.
type vendorBody struct {
	Error struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
	} `json:"error"`
}

// StatusCodes maps the statuses DeepSeek documents onto the closed table.
var StatusCodes = map[int]vocab.Code{
	http.StatusBadRequest:          vocab.CodeInvalidRequest,
	http.StatusUnauthorized:        vocab.CodeAuth,
	http.StatusPaymentRequired:     vocab.CodeAccountQuota, // insufficient balance
	http.StatusUnprocessableEntity: vocab.CodeInvalidRequest,
	http.StatusTooManyRequests:     vocab.CodeRateLimit,
	http.StatusInternalServerError: vocab.CodeServer,
	http.StatusServiceUnavailable:  vocab.CodeServer,
}

// BusinessCodes maps DeepSeek error type or code strings onto the closed table.
// The vendor publishes no list (UP-DEBT-marker protocol.ts:245, carried to
// DH-S-35), so it holds only the codes this route has seen; anything else stays
// unknown: classified by its status and reported with its raw code and body.
var BusinessCodes = map[string]vocab.Code{
	"invalid_api_key":            vocab.CodeAuth,
	"authentication_error":       vocab.CodeAuth,
	"insufficient_balance":       vocab.CodeAccountQuota,
	"insufficient_quota":         vocab.CodeAccountQuota,
	"rate_limit_exceeded":        vocab.CodeRateLimit,
	"invalid_request_error":      vocab.CodeInvalidRequest,
	"context_length_exceeded":    vocab.CodeContextWindowExceeded,
	"server_overloaded":          vocab.CodeServer,
	"model_not_found":            vocab.CodeUnknownModel,
	"unsupported_parameter":      vocab.CodeUnsupportedOption,
	"invalid_parameter":          vocab.CodeInvalidRequest,
	"invalid_request_parameters": vocab.CodeInvalidRequest,
}

// Classify maps a DeepSeek error, from a non-2xx response or an in-stream error
// event (status 0), onto the closed failure table, masking every secret value
// mask knows. Order: a Messages error type the anthropic driver knows, a
// business code in BusinessCodes, a documented status, then the generic class of
// the status (transport.StatusFailure; llm/server in-stream) with the raw code
// and body kept in the message, never a guessed specific class.
func Classify(mask func(string) string) transport.Classifier {
	if mask == nil {
		mask = func(s string) string { return s }
	}
	return func(status int, h http.Header, body []byte) *vocab.Failure {
		f, _ := classify(status, h, body)
		f.Message = mask(f.Message)
		return f
	}
}

func classify(status int, h http.Header, body []byte) (*vocab.Failure, bool) {
	if e, ok := anthropic.ParseError(body); ok && e.Type == "error" {
		if code, known := anthropic.ErrorCodes[e.Error.Type]; known {
			return transport.Fail(code, "deepseek: %s: %s", e.Error.Type, clip(e.Error.Message)), true
		}
		if code, known := BusinessCodes[e.Error.Type]; known {
			return transport.Fail(code, "deepseek: %s: %s", e.Error.Type, clip(e.Error.Message)), true
		}
		if c, known := StatusCodes[status]; known {
			return transport.Fail(c, "deepseek: status %d: %s: %s", status, strconvQuote(e.Error.Type), clip(e.Error.Message)), true
		}
		return unknown(status, h, strconvQuote(e.Error.Type), body), false
	}
	var v vendorBody
	if json.Unmarshal(body, &v) == nil && (v.Error.Type != "" || len(v.Error.Code) > 0) {
		code := codeString(v.Error.Code)
		for _, k := range []string{code, v.Error.Type} {
			if c, known := BusinessCodes[k]; known && k != "" {
				return transport.Fail(c, "deepseek: %s: %s", k, clip(v.Error.Message)), true
			}
		}
		if c, known := StatusCodes[status]; known && code == "" {
			return transport.Fail(c, "deepseek: status %d: %s", status, clip(v.Error.Message)), true
		}
		raw := code
		if raw == "" {
			raw = v.Error.Type
		}
		return unknown(status, h, strconvQuote(raw), body), false
	}
	if c, known := StatusCodes[status]; known {
		return transport.Fail(c, "deepseek: status %d %s", status, http.StatusText(status)), true
	}
	return unknown(status, h, "", body), false
}

// unknown is the generic class of status with the raw vendor code and body kept.
func unknown(status int, h http.Header, code string, body []byte) *vocab.Failure {
	var f *vocab.Failure
	if status == 0 {
		f = transport.Fail(vocab.CodeServer, "")
	} else {
		f = transport.StatusFailure(status, h, body)
	}
	what := "unknown vendor error"
	if code != "" {
		what += " " + code
	}
	f.Message = fmt.Sprintf("deepseek: %s (status %d): %s", what, status, clip(string(body)))
	return f
}

func codeString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func strconvQuote(s string) string { return fmt.Sprintf("%q", clip(s)) }

func clip(s string) string {
	const max = 512
	s = strings.ToValidUTF8(strings.TrimSpace(s), "\uFFFD")
	if len(s) > max {
		s = strings.ToValidUTF8(s[:max], "") + "…"
	}
	return s
}
