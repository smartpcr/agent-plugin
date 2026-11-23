package claudecode

import (
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// The assistant error kinds the client reports (SDKAssistantMessageError,
// claude-code-cli.md sec. 5.4).
const (
	errAuthFailed       = "authentication_failed"
	errOrgNotAllowed    = "oauth_org_not_allowed"
	errRateLimit        = "rate_limit"
	errOverloaded       = "overloaded"
	errServer           = "server_error"
	errBilling          = "billing_error"
	errAccountOnHold    = "account_on_hold"
	errInvalidRequest   = "invalid_request"
	errModelNotFound    = "model_not_found"
	errMaxOutputTokens  = "max_output_tokens"
	errCloudCredentials = "cloud_credential_error"
)

// classify is the finish of a client failure, from structured fields only: the
// assistant line's error kind and the result's API status (0 when no request
// reached the API). The message never carries the client's text (R9).
func classify(kind string, status int) vocab.Finish {
	fail := func(code vocab.Code, format string, args ...any) vocab.Finish {
		f := clientroute.Fail(code, "claude-code: "+format, args...)
		if f.Status == 0 && status >= 100 && status <= 599 {
			f.Status = status
		}
		return vocab.Finish{Reason: vocab.FinishError, Failure: f}
	}
	switch kind {
	case errAuthFailed, errOrgNotAllowed:
		if status == 0 {
			return fail(vocab.CodeSignInRequired, "the client is not signed in (or its sign-in expired); run `claude auth login`")
		}
		return fail(vocab.CodeAuth, "the vendor rejected the client's sign-in (HTTP %d); sign in again with `claude auth login`", status)
	case errCloudCredentials:
		return fail(vocab.CodeAuth, "the client used cloud credentials, which this route removes")
	case errRateLimit:
		return fail(vocab.CodeRateLimit, "the account is rate limited")
	case errOverloaded, errServer:
		return fail(vocab.CodeServer, "the vendor reported %s", kind)
	case errBilling:
		return fail(vocab.CodeQuota, "the account has no usage left (billing)")
	case errAccountOnHold:
		return fail(vocab.CodeAccountQuota, "the account is on hold")
	case errInvalidRequest:
		return fail(vocab.CodeInvalidRequest, "the vendor refused the request as invalid")
	case errModelNotFound:
		return fail(vocab.CodeUnknownModel, "the vendor does not offer the requested model")
	case errMaxOutputTokens:
		return vocab.Finish{Reason: vocab.FinishMaxTokens}
	}
	if status >= 400 && status <= 599 {
		return vocab.Finish{Reason: vocab.FinishError, Failure: clientroute.Fail(vocab.HTTPCode(status), "claude-code: the vendor answered HTTP %d", status)}
	}
	return fail(vocab.CodeServer, "the client reported a failure it did not classify")
}

// breach is a contract breach of the route (DH-ADR-LLM-10): Permanent, never
// retried, the tree stopped.
func breach(format string, args ...any) *vocab.Failure {
	return clientroute.Fail(vocab.CodeClientContract, "claude-code: "+format, args...)
}
