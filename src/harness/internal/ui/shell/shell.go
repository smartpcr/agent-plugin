// Package shell is area C1's page shell (docs/harness/areas/ui-core.md §6.1,
// DH-ADR-UICORE-2). This stage holds the region registry and the composition
// epoch the web profile mounts: the owner of a place declares its region, any
// component contributes to one, and both are effects of their fiber, so unloading
// a component removes what it placed and bumps the epoch. The layout, the island
// table and the pages arrive with the UI that renders them.
package shell

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/access"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/web"
)

// Name is the catalogue name of Component.
const Name = "ui.shell"

// Key is the shell.
var Key = cordis.NewServiceKey[Shell]("ui.shell")

// Kind is how a region shows its contributions.
type Kind int

const (
	// One shows a single contribution.
	One Kind = iota
	// List shows every contribution in order.
	List
	// Keyed shows one contribution per key.
	Keyed
	// Wrap nests each contribution around the next.
	Wrap
)

// Region is one place a page shows contributions.
type Region struct {
	Name string
	Kind Kind
	Doc  string
}

// Contribution is one thing a component places in a region.
type Contribution struct {
	Region, ID string
	Order      int
}

// Shell is the region registry and the composition epoch.
type Shell interface {
	// Declare names a region as an effect of owner; a name is declared once.
	Declare(owner *cordis.Context, r Region) error
	// Contribute places c in a declared region as an effect of owner.
	Contribute(owner *cordis.Context, c Contribution) error
	// Epoch increases whenever the set of regions or contributions changes.
	Epoch() uint64
}

type shell struct {
	mu      sync.Mutex
	regions map[string]Region
	placed  map[string]*placed // region name -> the declared instance's contributions
	epoch   atomic.Uint64
}

// placed is one declared instance of a region: redeclaring a name makes a new one.
type placed struct{ items map[string]Contribution }

// New returns an empty shell.
func New() Shell {
	return &shell{regions: map[string]Region{}, placed: map[string]*placed{}}
}

func (s *shell) Declare(owner *cordis.Context, r Region) error {
	if r.Name == "" {
		return errors.New("shell: a region needs a name")
	}
	_, err := owner.Effect("region "+r.Name, func() (cordis.Disposer, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.regions[r.Name]; ok {
			return nil, fmt.Errorf("shell: region %q is already declared", r.Name)
		}
		s.regions[r.Name], s.placed[r.Name] = r, &placed{items: map[string]Contribution{}}
		s.epoch.Add(1)
		return func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.regions, r.Name)
			delete(s.placed, r.Name)
			s.epoch.Add(1)
			return nil
		}, nil
	})
	return err
}

func (s *shell) Contribute(owner *cordis.Context, c Contribution) error {
	if c.ID == "" {
		return errors.New("shell: a contribution needs an id")
	}
	_, err := owner.Effect("contribution "+c.Region+"/"+c.ID, func() (cordis.Disposer, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		in, ok := s.placed[c.Region]
		if !ok {
			return nil, fmt.Errorf("shell: region %q is not declared", c.Region)
		}
		if _, ok := in.items[c.ID]; ok {
			return nil, fmt.Errorf("shell: region %q already holds %q", c.Region, c.ID)
		}
		in.items[c.ID] = c
		s.epoch.Add(1)
		// The disposer is bound to the region instance this contribution joined
		// (the instance captured as in), never to whatever region holds the name at
		// disposal: a region unloaded and redeclared is a new instance whose
		// contributions this one must not touch.
		return func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(in.items, c.ID)
			if s.placed[c.Region] == in {
				s.epoch.Add(1)
			}
			return nil
		}, nil
	})
	return err
}

func (s *shell) Epoch() uint64 { return s.epoch.Load() }

// Component is ui.shell: it provides the shell under Key, beside the route table
// its pages join and the access service that decides what a page shows.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	if _, err := web.Key.Get(ctx); err != nil {
		return err
	}
	if _, err := access.Key.Get(ctx); err != nil {
		return err
	}
	_, err := Key.Provide(ctx, New())
	return err
}, cordis.WithInject(web.Key.Name(), access.Key.Name()))
