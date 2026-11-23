package copilot

import (
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// errorLine is the type of the structured error line. Inference: the line is
// the SDK's session.error event, its kind in data.errorType
// (github-copilot-cli.md sec. 5.4 names "a structured error line whose type
// names authentication" but not its shape); the signed-out fixture of
// DH-E-COPILOTCLI-1 (plan 16.5.9) pins it.
const errorLine = "session.error"

// classifyError is the finish of a structured error line of kind; afterModel
// is true when an assistant line came before it, so the vendor had been asked
// (a rejected sign-in rather than none). The message never carries the client's
// text (R9). ok is false for a kind the route cannot classify.
func classifyError(kind string, afterModel bool) (vocab.Finish, bool) {
	k := strings.ToLower(kind)
	switch {
	case strings.Contains(k, "auth"):
		if afterModel {
			return failed(vocab.CodeAuth, "the vendor rejected the client's sign-in; sign in again with `copilot login`"), true
		}
		return failed(vocab.CodeSignInRequired, "the client is not signed in (or its sign-in expired); run `copilot login`"), true
	case strings.Contains(k, "rate"):
		return failed(vocab.CodeRateLimit, "the account is rate limited"), true
	}
	return vocab.Finish{}, false
}

// exited is the finish of a client that ended with status code and no line the
// route can place (github-copilot-cli.md sec. 5.4): Permanent, never retried.
// Until DH-E-COPILOTCLI-1 pins the signed-out line this is how a signed-out run
// settles, so the check that it is llm/sign-in-required stays open.
func exited(code int) vocab.Finish {
	return failed(vocab.CodeClientContract,
		"the client exited with status %d and printed nothing this route can place; if it is not signed in, run `copilot login`", code)
}

func failed(code vocab.Code, format string, args ...any) vocab.Finish {
	return vocab.Finish{Reason: vocab.FinishError, Failure: clientroute.Fail(code, "copilot-cli: "+format, args...)}
}

// breach is a contract breach of the route (DH-ADR-LLM-10): Permanent, never
// retried, the tree stopped.
func breach(format string, args ...any) *vocab.Failure {
	return clientroute.Fail(vocab.CodeClientContract, "copilot-cli: "+format, args...)
}
