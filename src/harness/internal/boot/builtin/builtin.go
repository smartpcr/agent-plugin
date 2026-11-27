// Package builtin lists the components compiled into dsh (DH-ADR-BOOT-3): one set
// per package family, joined by Sets into the build catalogue. Adding a component to
// the binary is a line here and a visible change of the catalogue golden.
package builtin

import (
	"reflect"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/logging"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/headless"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/access"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/assets"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/auth"
	hostlaunch "github.com/smartpcr/agent-plugin/src/harness/internal/host/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/stream"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/web"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/permission"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	secretfile "github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/file"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/titlemodel"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/title"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/items"
	"github.com/smartpcr/agent-plugin/src/harness/internal/ui/shell"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/stdio"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	sandboxlocal "github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
)

// Boot is the boot family's items.
func Boot() []catalog.Item {
	return []catalog.Item{
		{Name: logging.Name, Component: logging.Component, Config: reflect.TypeFor[logging.Config]()},
	}
}

// Util is the util family's items.
func Util() []catalog.Item {
	return []catalog.Item{
		{Name: egress.Name, Component: egress.Component, Config: reflect.TypeFor[egress.Config](), Provides: []string{egress.Key.Name()}},
		{Name: "util.watch", Component: watch.Component, Config: reflect.TypeFor[watch.Options](), Provides: []string{watch.Key.Name()}},
	}
}

// Host is the host family's items: the sharing gate, and the host plane the web
// profile mounts (the listener, admission, assets, the route table, the runtime
// record, access and the stream hub).
func Host() []catalog.Item {
	return []catalog.Item{
		{Name: sharing.Name, Component: sharing.Component, Config: reflect.TypeFor[sharing.Config]()},
		{Name: listen.Name, Component: listen.Component, Config: reflect.TypeFor[listen.Config](), Provides: []string{listen.Key.Name()}},
		{Name: auth.Name, Component: auth.Component, Config: reflect.TypeFor[auth.Config](), Provides: []string{auth.Key.Name()}},
		{Name: assets.Name, Component: assets.Component},
		{Name: web.Name, Component: web.Component, Config: reflect.TypeFor[web.Config](), Provides: []string{web.Key.Name()}},
		{Name: hostlaunch.Name, Component: hostlaunch.Component, Config: reflect.TypeFor[hostlaunch.Config](), Provides: []string{hostlaunch.Key.Name()}},
		{Name: access.Name, Component: access.Component, Provides: []string{access.Key.Name()}},
		{Name: stream.Name, Component: stream.Component, Provides: []string{stream.Key.Name()}},
	}
}

// Storage is the storage family's items: the home database handle, shared with
// session.store.
func Storage() []catalog.Item {
	return []catalog.Item{
		{Name: kv.Name, Component: kv.Component, Provides: []string{kv.DBKey.Name()}},
	}
}

// UI is the ui family's items: the page shell.
func UI() []catalog.Item {
	return []catalog.Item{
		{Name: shell.Name, Component: shell.Component, Provides: []string{shell.Key.Name()}},
	}
}

// Cycle C-3 (DH-S-12) is the working directory (C31), the filesystem seam (C32)
// and the policy resolver (C33): session.workdir validates through fs.local,
// sandbox.policy reads each session's directory from session.workdir and fs.ops
// fences the provider with sandbox.policy. The three families below are the one
// unit that lands them; each injects only what the others provide plus the
// process's session service (session.core) and fold registry (session.views).
//
// Not yet bootable: this unit is registered in the build catalogue only. No
// catalogue item and nothing in boot.Real.Tree provides session.views yet, so in
// the dsh binary session.workdir and sandbox.policy (and fs.ops through them)
// would wait on inject until Settle's StartupTimeout. cyclec3_test.go provides
// session.core and session.views by hand on a testkit root; it proves the three
// items compose from the catalogue, not that they boot through boot. The gap
// closes when the session.views item (DH-D-VIEW-1, DH-D-VIEW-3) joins Session().

// Session is the session family's items: the store (over the home boot
// provides), the scope table and the session service of the spine,
// session.workdir of cycle C-3, and the session titles (C56): session.title
// and session.title.llm, the model call the sdk profile switches off. Like
// cycle C-3, session.title injects session.views, so the titles wait on inject
// in the dsh binary until the session.views item lands; their bundle entries
// (base.yml) land with it, and until then both sit on the optional list.
// titles_test.go composes both from the catalogue, with and without the model.
func Session() []catalog.Item {
	return []catalog.Item{
		{Name: sqlite.Name, Component: sqlite.Component, Provides: []string{session.StoreKey.Name()}},
		{Name: "session.scope", Component: scope.Component, Provides: []string{scope.TableKey.Name()}},
		{Name: session.Name, Component: session.Component, Provides: []string{session.Key.Name()}},
		{Name: workdir.Name, Component: workdir.Component, Provides: []string{workdir.Key.Name(), workdir.ServiceKey.Name()}},
		{Name: title.Name, Component: title.Component, Config: reflect.TypeFor[title.ComponentConfig](), Provides: []string{title.Key.Name()}},
		{Name: titlemodel.Name, Component: titlemodel.Component},
	}
}

// Agent is the agent family's items: the registry, the driver that fills its
// factory seat, and the preset registry and its declarations, which read the
// catalogue and evaluator boot provides (agentpreset.HostKey).
func Agent() []catalog.Item {
	return append([]catalog.Item{
		{Name: agent.Name, Component: agent.Component, Provides: []string{agent.Agents.Name()}},
		driver.Item(),
	}, agentpreset.Items(nil)...)
}

// LLM is the llm family's items: the runtime, with no route.
func LLM() []catalog.Item {
	return []catalog.Item{
		{Name: llm.Name, Component: llm.Component, Provides: []string{llm.RuntimeKey.Name()}},
	}
}

// Tools is the tools family's items: the tool registry and the prompt assembly.
func Tools() []catalog.Item { return items.Items() }

// FS is the fs family's items.
func FS() []catalog.Item {
	return []catalog.Item{
		{Name: local.Name, Component: local.Component, Config: reflect.TypeFor[local.Options](), Provides: []string{fs.Key.Name()}},
		ops.Item(),
	}
}

// Sandbox is the sandbox family's items: the resolver, whose item comes from ops,
// the one package allowed to import it, and the local seam (C37) the admin tree's
// `dsh sandbox status` reads.
func Sandbox() []catalog.Item {
	return []catalog.Item{
		ops.PolicyItem(),
		{Name: sandboxlocal.Name, Component: sandboxlocal.Component, Config: reflect.TypeFor[sandboxlocal.Config](), Provides: []string{sandbox.ServiceKey.Name()}},
	}
}

// Human is the human family's items: the secret store over the home's file
// backend, which `dsh secret set` writes through.
func Human() []catalog.Item {
	return []catalog.Item{
		{Name: secretfile.Name, Component: secretfile.Component, Config: reflect.TypeFor[secretfile.Config](), Provides: []string{secret.Key.Name()}},
	}
}

// Permission is the permission family's items: the permission preset table
// (C34). Registered in the build catalogue so a layer's table is checked at
// config load (a default that names no preset is status 78); like cycle C-3 it
// waits on inject in the dsh binary until session.core, human.approval and
// human.command have catalogue items.
func Permission() []catalog.Item {
	return []catalog.Item{
		{Name: permission.Name, Component: permission.Component, Config: reflect.TypeFor[permission.Config](), Provides: []string{permission.Key.Name()}},
	}
}

// Wire is the wire family's items: the stdio stream owner and the SDK server
// over it, the carriers of the sdk profile.
func Wire() []catalog.Item {
	return []catalog.Item{
		{Name: stdio.Name, Component: stdio.Component, Config: reflect.TypeFor[stdio.Settings](), Provides: []string{stdio.Key.Name()}},
		{Name: sdkserver.Name, Component: sdkserver.Component},
	}
}

// CLI is the cli family's items: the carrier of the headless profile.
func CLI() []catalog.Item {
	return []catalog.Item{
		{Name: headless.Name, Component: headless.Component, Config: reflect.TypeFor[headless.Config](), Provides: []string{human.Key.Name(), approval.Key.Name(), command.Key.Name()}},
	}
}

// extra is the items a testhook build adds (testhook.go); empty otherwise.
var extra []catalog.Item

// Sets is every family's items.
func Sets() [][]catalog.Item {
	sets := [][]catalog.Item{Boot(), Host(), Util(), Session(), Agent(), LLM(), Tools(), FS(), Sandbox(), Human(), Permission(), Wire(), CLI(), Storage(), UI()}
	if len(extra) > 0 {
		sets = append(sets, extra)
	}
	return sets
}

// Catalog builds the catalogue of this binary.
func Catalog() (*catalog.Catalog, error) { return catalog.Build(Sets()...) }
