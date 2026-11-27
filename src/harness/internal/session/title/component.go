package title

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// Name is the component's catalogue name.
const Name = "session.title"

// ComponentConfig is the component's configuration: title.Config and
// title.GeneratorConfig.
type ComponentConfig struct {
	Title     Config          `json:"title,omitempty"`
	Generator GeneratorConfig `json:"generator,omitempty"`
}

// Component is session.title (C56): it registers the title and titleInput folds
// in the process's fold registry (views.Key), generates through the Model under
// ModelKey (llm/titlemodel's adapter over the runtime), provides the *Service
// under Key, and follows every session the session service creates (Listen).
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg ComponentConfig) error {
	reg, err := views.Key.Get(ctx)
	if err != nil {
		return err
	}
	m, err := ModelKey.Get(ctx)
	if err != nil {
		return err
	}
	s, err := New(reg, m, WithConfig(cfg.Title), WithGenerator(cfg.Generator), WithWarn(ctx.Logger().Warnf))
	if err != nil {
		return err
	}
	if err := s.Listen(ctx); err != nil {
		return err
	}
	_, err = Key.Provide(ctx, s)
	return err
}, cordis.WithInject(views.Key.Name(), ModelKey.Name()))

// Listen follows the sessions created under ctx: each is attached when created,
// due an automatic title after each turn/end, and detached when closed.
func (s *Service) Listen(ctx *cordis.Context) error {
	if _, err := session.Created.On(ctx, func(c *cordis.Context, a session.CreatedArgs) error {
		if err := s.Attach(context.Background(), a.Session); err != nil {
			s.warn("title: attach session %s: %v", a.Session.ID(), err)
		}
		return nil
	}); err != nil {
		return err
	}
	if _, err := session.Committed.On(ctx, func(_ *cordis.Context, a session.CommittedArgs) error {
		if a.Event.Type() == event.TurnEndType.Name() {
			s.Auto(a.Session.ID())
		}
		return nil
	}); err != nil {
		return err
	}
	_, err := session.Closed.On(ctx, func(_ *cordis.Context, a session.ClosedArgs) error {
		s.Detach(a.ID)
		return nil
	})
	return err
}
