package permission

import (
	"fmt"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// Name is the component's catalogue name.
const Name = "permission.presets"

// Component is permission.presets (C34): the service over its Config,
// registering the permissions fold in the process's fold registry (views.Key)
// and writing permission/preset through the session service (session.Key), the
// mode through
// the resolver (sandbox.ResolverKey) and the policy through the approval service
// (approval.Key), whose grant table a switch revokes. It pins each fresh session
// at agent creation, registers /permission in the command registry
// (command.Key) and provides the service under Key. The config resolves onto
// DefaultConfig and must validate, so a default preset that names no entry
// fails the load and the config check (config/rejected, status 78). An update
// is accepted in place: the table changes and no session's knobs do.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	sessions, err := session.Key.Get(ctx)
	if err != nil {
		return err
	}
	resolver, err := sandbox.ResolverKey.Get(ctx)
	if err != nil {
		return err
	}
	sb, ok := resolver.(Sandbox)
	if !ok {
		return fmt.Errorf("permission: the sandbox resolver %T does not read a session's mode", resolver)
	}
	apr, err := approval.Key.Get(ctx)
	if err != nil {
		return err
	}
	cmds, err := command.Key.Get(ctx)
	if err != nil {
		return err
	}
	reg, err := views.Key.Get(ctx)
	if err != nil {
		return err
	}
	svc, err := New(cfg, Deps{Sessions: sessions, Sandbox: sb, Approval: apr, Grants: apr.Grants(), Views: reg})
	if err != nil {
		return err
	}
	if _, err := cordis.AcceptUpdate(ctx, svc.reconfigure); err != nil {
		return err
	}
	if _, err := svc.Bind(ctx); err != nil {
		return err
	}
	if _, err := svc.RegisterCommand(ctx, cmds); err != nil {
		return err
	}
	_, err = Key.Provide(ctx, svc)
	return err
}, cordis.WithInject(session.Key.Name(), views.Key.Name(), sandbox.ResolverKey.Name(), approval.Key.Name(), command.Key.Name()),
	cordis.WithDefaults(DefaultConfig))
