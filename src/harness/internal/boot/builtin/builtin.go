// Package builtin lists the components compiled into dsh (DH-ADR-BOOT-3): one set
// per package family, joined by Sets into the build catalogue. Adding a component to
// the binary is a line here and a visible change of the catalogue golden.
package builtin

import (
	"reflect"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/logging"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/permission"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
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

// Host is the host family's items.
func Host() []catalog.Item {
	return []catalog.Item{
		{Name: sharing.Name, Component: sharing.Component, Config: reflect.TypeFor[sharing.Config]()},
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
// catalogue item and nothing in boot.Real.Tree provides session.core or
// session.views yet, so in the dsh binary session.workdir and sandbox.policy
// (and fs.ops through them) wait on inject until Settle's StartupTimeout.
// cyclec3_test.go provides both keys by hand on a testkit root; it proves the
// three items compose from the catalogue, not that they boot through boot.
// The gap closes when the session.core item (core.Component, DH-D-SESSION-2)
// and the session.views item (DH-D-VIEW-1, DH-D-VIEW-3) join Session().

// Session is the session family's items.
func Session() []catalog.Item {
	return []catalog.Item{
		{Name: workdir.Name, Component: workdir.Component, Provides: []string{workdir.Key.Name(), workdir.ServiceKey.Name()}},
	}
}

// FS is the fs family's items.
func FS() []catalog.Item {
	return []catalog.Item{
		{Name: local.Name, Component: local.Component, Config: reflect.TypeFor[local.Options](), Provides: []string{fs.Key.Name()}},
		ops.Item(),
	}
}

// Sandbox is the sandbox family's items. The resolver's item comes from ops,
// the one package allowed to import it.
func Sandbox() []catalog.Item { return []catalog.Item{ops.PolicyItem()} }

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

// Sets is every family's items.
func Sets() [][]catalog.Item {
	return [][]catalog.Item{Boot(), Host(), Util(), Session(), FS(), Sandbox(), Permission()}
}

// Catalog builds the catalogue of this binary.
func Catalog() (*catalog.Catalog, error) { return catalog.Build(Sets()...) }
