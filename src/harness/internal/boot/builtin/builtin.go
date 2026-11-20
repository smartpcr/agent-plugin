// Package builtin lists the components compiled into dsh (DH-ADR-BOOT-3): one set
// per package family, joined by Sets into the build catalogue. Adding a component to
// the binary is a line here and a visible change of the catalogue golden.
package builtin

import (
	"reflect"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/logging"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
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
		{Name: "util.watch", Component: watch.Component, Config: reflect.TypeFor[watch.Options](), Provides: []string{watch.Key.Name()}},
	}
}

// Sets is every family's items.
func Sets() [][]catalog.Item { return [][]catalog.Item{Boot(), Util()} }

// Catalog builds the catalogue of this binary.
func Catalog() (*catalog.Catalog, error) { return catalog.Build(Sets()...) }
