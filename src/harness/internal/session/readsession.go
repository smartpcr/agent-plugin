package session

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// readSession is a ReadSession over folds that do not change: an OpenRead, whose
// in-memory closers are served past the stored end, or the frozen view a Folder
// receives.
type readSession struct {
	id      ID
	header  Header
	key     scope.Key
	r       store.Reader
	st      *surface.State
	stored  event.Seq      // records below this are read from r
	extra   []event.Record // closers folded in memory, from stored on
	durable event.Seq
}

func (s *readSession) ID() ID                                       { return s.id }
func (s *readSession) Header() Header                               { return s.header }
func (s *readSession) Len() Offset                                  { return s.st.Len() }
func (s *readSession) Durable() Offset                              { return s.durable }
func (s *readSession) History() surface.History                     { return s.st.History() }
func (s *readSession) RequestHeader() (surface.RequestHeader, bool) { return s.st.RequestHeader() }
func (s *readSession) ToolHistory() surface.ToolHistory             { return s.st.ToolHistory() }
func (s *readSession) Open() surface.Brackets                       { return s.st.Open() }
func (s *readSession) Scope() scope.Key                             { return s.key }

func (s *readSession) Checkpoints(ctx context.Context) ([]store.Checkpoint, error) {
	return s.r.Checkpoints(ctx)
}

func (s *readSession) Read(ctx context.Context, from Offset, limit int, opts ...ReadOption) ([]event.Event, error) {
	out, err := readRange(ctx, s.r, from, limit, s.stored, opts)
	if err != nil {
		return nil, err
	}
	for _, rec := range s.extra {
		if limit > 0 && len(out) >= limit {
			break
		}
		if rec.Seq >= from {
			out = append(out, event.Of(rec))
		}
	}
	return out, nil
}
