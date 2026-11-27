// Package stream is area A19's event stream hub (docs/harness/areas/api.md §6.1,
// DH-ADR-API-4). This stage holds the hub's source table, which the web profile's
// host plane mounts: a component registers a topic as an effect of its own fiber,
// so unloading it removes the topic. Connections, the recovery classes, cursor
// resume and tabs arrive with the routes that open a stream.
package stream

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/access"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/web"
)

// Name is the catalogue name of Component.
const Name = "host.stream"

// Key is the hub.
var Key = cordis.NewServiceKey[Hub]("host.stream")

// Class is how a topic recovers after a gap.
type Class int

const (
	// State is replaced whole.
	State Class = iota
	// Notice may be dropped.
	Notice
)

// Source is one topic a component offers.
type Source struct {
	Topic string
	Class Class
}

// Hub is the stream hub.
type Hub interface {
	// Register adds s as an effect of owner; a topic is registered once.
	Register(owner *cordis.Context, s Source) error
	// Topics are the registered topics, sorted.
	Topics() []string
}

// hub is the source table.
type hub struct {
	mu      sync.Mutex
	sources map[string]Source
}

// New returns an empty hub.
func New() Hub { return &hub{sources: map[string]Source{}} }

func (h *hub) Register(owner *cordis.Context, s Source) error {
	if s.Topic == "" {
		return errors.New("stream: a source needs a topic")
	}
	_, err := owner.Effect("stream source "+s.Topic, func() (cordis.Disposer, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.sources[s.Topic]; ok {
			return nil, fmt.Errorf("stream: topic %q is already registered", s.Topic)
		}
		h.sources[s.Topic] = s
		return func() error {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(h.sources, s.Topic)
			return nil
		}, nil
	})
	return err
}

func (h *hub) Topics() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.sources))
	for t := range h.sources {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// Component is host.stream: it provides the hub under Key, beside the route table
// its stream routes join and the access service that checks every subscription.
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
