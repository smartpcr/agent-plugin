package policy

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// Name is the component's catalogue name.
const Name = "sandbox.policy"

// Component is sandbox.policy (C33, cycle C-3): the resolver over its Config,
// reading and appending sandbox/mode through the session service (session.Key),
// taking each session's directory and project root from the working directory
// (workdir.Key, the contract-only edge of the cycle) and re-checking configured
// roots and grants through the seam (fs.Key). It is provided under
// sandbox.ResolverKey; fs/ops is its one caller. No session has a scratch
// directory and every escalation is rejected until the scratch and approval
// deliverables wire theirs.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	sessions, err := session.Key.Get(ctx)
	if err != nil {
		return err
	}
	wd, err := workdir.Key.Get(ctx)
	if err != nil {
		return err
	}
	seam, err := fs.Key.Get(ctx)
	if err != nil {
		return err
	}
	r, err := New(cfg, Deps{Owner: ctx, Sessions: sessions, Workdir: wd, FS: seam})
	if err != nil {
		return err
	}
	_, err = sandbox.ResolverKey.Provide(ctx, r)
	return err
}, cordis.WithInject(session.Key.Name(), workdir.Key.Name(), fs.Key.Name()))
