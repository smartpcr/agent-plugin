package clientroute

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// HelperLaunch is the launch plan of a signed-in client: the explicit
// unconfined helper plan of kind, from the policy r.ForHelper issues for it
// (DH-D-EXEC-4, M2-C-9). The client runs unconfined because it needs its own
// sign-in, home and network; that is a registered helper's reason, not a
// trusted start. A resolver that does not let kind run unconfined, or none, is
// llm/invalid-request before anything runs.
func HelperLaunch(ctx context.Context, r sandbox.Resolver, client, kind string) (sandbox.Launch, error) {
	if r == nil {
		return sandbox.Launch{}, Fail(vocab.CodeInvalidRequest, "%s: the route has no sandbox resolver to plan the client's launch", client)
	}
	p, err := r.ForHelper(ctx, sandbox.HelperRequest{Kind: kind, Unconfined: true, Network: true})
	if err != nil {
		if ctx.Err() != nil {
			return sandbox.Launch{}, Fail(vocab.CodeAborted, "%s: cancelled while planning the client's launch", client)
		}
		return sandbox.Launch{}, Fail(vocab.CodeInvalidRequest, "%s: the client may not run unconfined (%s)", client, reasonOf(err))
	}
	l, err := sandbox.Helper(p, kind)
	if err != nil {
		return sandbox.Launch{}, Fail(vocab.CodeInvalidRequest, "%s: the client's launch cannot be planned (%s)", client, reasonOf(err))
	}
	return l, nil
}
