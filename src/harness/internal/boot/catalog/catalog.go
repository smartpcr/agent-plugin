// Package catalog is the build catalogue (DH-D-BOOT-1, DH-ADR-BOOT-3,
// DH-ADR-BOOT-13): every component compiled into a build, declared in one list,
// with its config type, the services it may provide, the data view it offers to
// expressions and its platforms. A Catalog is the loader's Resolver. Nothing
// registers from init: two catalogues can coexist in one process.
package catalog

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
)

// Item declares one compiled-in component.
type Item struct {
	Name      string            // "storage.kv": the name configuration uses
	Component *cordis.Component // its Name must equal Name
	Config    reflect.Type      // nil when the component takes no config
	Provides  []string          // services it may provide
	View      reflect.Type      // data view offered to expressions as use.<service>; nil for none
	Platforms []string          // empty means every platform
}

// Platforms are the platform values an item may name.
var Platforms = []string{"darwin", "linux", "windows"}

// ErrUnknown is returned by Lookup for a name the build does not contain.
var ErrUnknown = errors.New("unknown component")

// ErrUnavailable is returned by Lookup for a component not built for this platform.
var ErrUnavailable = errors.New("component not available on this platform")

// segment is one lower-case segment, with single hyphens between words.
const segment = `[a-z0-9]+(?:-[a-z0-9]+)*`

var nameRE = regexp.MustCompile(`^` + segment + `(?:\.` + segment + `){1,2}$`)

// CheckName reports whether name follows the naming rule of DH-ADR-BOOT-13: two or
// three dotted lower-case segments, the first a registered family.
func CheckName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("name %q is not <family>.<name> or <family>.<name>.<part> in lower case", name)
	}
	first, _, _ := strings.Cut(name, ".")
	if _, ok := family(first); !ok {
		return fmt.Errorf("name %q: family %q is not registered", name, first)
	}
	return nil
}

// Catalog is an immutable, validated set of items.
type Catalog struct {
	goos  string
	items []Item // sorted by name
	index map[string]int
}

var _ loader.Resolver = (*Catalog)(nil)

// Build validates and freezes the items for the running platform.
func Build(sets ...[]Item) (*Catalog, error) { return BuildFor(runtime.GOOS, sets...) }

// BuildFor is Build for platform goos.
func BuildFor(goos string, sets ...[]Item) (*Catalog, error) {
	if !slices.Contains(Platforms, goos) {
		return nil, fmt.Errorf("catalog: platform %q is not one of %s", goos, strings.Join(Platforms, ", "))
	}
	c := &Catalog{goos: goos, index: map[string]int{}}
	var errs []error
	views := map[string][]Item{}
	for _, set := range sets {
		for _, item := range set {
			if err := check(item); err != nil {
				errs = append(errs, err)
				continue
			}
			if _, dup := c.index[item.Name]; dup {
				errs = append(errs, fmt.Errorf("catalog: %s is declared twice", item.Name))
				continue
			}
			if item.View != nil {
				// Checked across every platform, not only goos, so a catalogue
				// valid here is valid everywhere it is built.
				for _, service := range item.Provides {
					for _, other := range views[service] {
						if overlap(other.Platforms, item.Platforms) {
							errs = append(errs, fmt.Errorf("catalog: %s and %s both declare a view of %s on a shared platform", other.Name, item.Name, service))
						}
					}
					views[service] = append(views[service], item)
				}
			}
			c.index[item.Name] = -1
			c.items = append(c.items, freeze(item))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	slices.SortFunc(c.items, func(a, b Item) int { return strings.Compare(a.Name, b.Name) })
	for i, item := range c.items {
		c.index[item.Name] = i
	}
	return c, nil
}

func check(item Item) error {
	if err := CheckName(item.Name); err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	if item.Component == nil {
		return fmt.Errorf("catalog: %s has no component", item.Name)
	}
	if item.Component.Name() != item.Name {
		return fmt.Errorf("catalog: %s declares component %q", item.Name, item.Component.Name())
	}
	for i, service := range item.Provides {
		if service == "" || slices.Contains(item.Provides[:i], service) {
			return fmt.Errorf("catalog: %s: provided service %q is empty or repeated", item.Name, service)
		}
	}
	if item.View != nil {
		if len(item.Provides) == 0 {
			return fmt.Errorf("catalog: %s declares a view but provides no service", item.Name)
		}
		if err := expr.CheckView(item.View); err != nil {
			return fmt.Errorf("catalog: %s: view: %w", item.Name, err)
		}
	}
	for i, p := range item.Platforms {
		if !slices.Contains(Platforms, p) || slices.Contains(item.Platforms[:i], p) {
			return fmt.Errorf("catalog: %s: platform %q is unknown or repeated", item.Name, p)
		}
	}
	return nil
}

// overlap reports whether two platform sets share a platform; an empty set
// means every platform.
func overlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, p := range a {
		if slices.Contains(b, p) {
			return true
		}
	}
	return false
}

func freeze(item Item) Item {
	item.Provides = slices.Clone(item.Provides)
	item.Platforms = slices.Clone(item.Platforms)
	slices.Sort(item.Platforms)
	return item
}

// Lookup returns the item called name. The error wraps ErrUnknown when the build
// has no such component, and ErrUnavailable when it is not built for this platform.
func (c *Catalog) Lookup(name string) (Item, error) {
	i, ok := c.index[name]
	if !ok {
		return Item{}, fmt.Errorf("%w: %q", ErrUnknown, name)
	}
	item := c.items[i]
	if len(item.Platforms) > 0 && !slices.Contains(item.Platforms, c.goos) {
		return Item{}, fmt.Errorf("%w: %q is built for %s, not %s", ErrUnavailable, name, strings.Join(item.Platforms, ", "), c.goos)
	}
	return freeze(item), nil
}

// Resolve implements loader.Resolver: the component called name, when it exists on
// this platform.
func (c *Catalog) Resolve(name string) (*cordis.Component, bool) {
	item, err := c.Lookup(name)
	return item.Component, err == nil
}

// Items returns every item, sorted by name, including those of other platforms.
func (c *Catalog) Items() []Item {
	items := make([]Item, len(c.items))
	for i, item := range c.items {
		items[i] = freeze(item)
	}
	return items
}

// Views maps each service with a declared view to the view type, for the
// expression evaluator's use.<service> names. Items of other platforms are left out.
func (c *Catalog) Views() map[string]reflect.Type {
	views := map[string]reflect.Type{}
	for _, item := range c.items {
		if item.View == nil {
			continue
		}
		if _, err := c.Lookup(item.Name); err != nil {
			continue
		}
		for _, service := range item.Provides {
			views[service] = item.View
		}
	}
	return views
}

// Print writes the catalogue as a table, one component per line, sorted by name.
// The output does not depend on the running platform.
func (c *Catalog) Print(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tAREA\tCONFIG\tPROVIDES\tVIEW\tPLATFORMS")
	for _, item := range c.items {
		first, _, _ := strings.Cut(item.Name, ".")
		f, _ := family(first) // Build admitted only registered families
		platforms := "all"
		if len(item.Platforms) > 0 {
			platforms = strings.Join(item.Platforms, ",")
		}
		provides := "-"
		if len(item.Provides) > 0 {
			provides = strings.Join(item.Provides, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", item.Name, f.Area, typeName(item.Config), provides, typeName(item.View), platforms)
	}
	return tw.Flush()
}

func typeName(t reflect.Type) string {
	if t == nil {
		return "-"
	}
	return t.String()
}
