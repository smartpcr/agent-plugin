package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// ResumeOptions resume a stored session; empty fields keep its stored values.
type ResumeOptions struct {
	PermissionMode string
	Model          *protocol.ModelRoute
}

// ListOptions page through the stored root sessions, newest first.
type ListOptions = protocol.SessionListParams

// Page is one page of sessions; NextCursor is empty on the last one.
type Page = protocol.SessionListResult

// CancelOptions cancel the running turn or step. Scope is "turn" (the default)
// or "step"; Inbox is "keep" (the default) or "discard" for queued messages.
type CancelOptions struct {
	Scope string
	Inbox string
}

// ErrSlowConsumer ends a subscription whose buffer was full when an event came.
var ErrSlowConsumer = errors.New("sdk: the subscriber fell behind; the subscription ended")

// ErrSubscriptionClosed ends a subscription its owner closed.
var ErrSubscriptionClosed = errors.New("sdk: the subscription was closed")

// Resume takes the write lease of a stored session.
func (h *Harness) Resume(ctx context.Context, id string, o ResumeOptions) (*Session, error) {
	var r protocol.SessionResumeResult
	p := protocol.SessionResumeParams{SessionID: id, PermissionMode: o.PermissionMode, Model: o.Model}
	if err := h.conn.Call(ctx, "session/resume", p, &r); err != nil {
		return nil, fmt.Errorf("sdk: session/resume: %w", err)
	}
	return &Session{h: h, ID: r.SessionID, Cwd: r.Cwd, LastSeq: r.LastSeq}, nil
}

// List returns one page of stored sessions.
func (h *Harness) List(ctx context.Context, o ListOptions) (Page, error) {
	var r Page
	if err := h.conn.Call(ctx, "session/list", o, &r); err != nil {
		return Page{}, fmt.Errorf("sdk: session/list: %w", err)
	}
	return r, nil
}

// Wait returns once the session is idle: no turn running and its inbox empty.
func (s *Session) Wait(ctx context.Context) error {
	return s.call(ctx, "session/wait", protocol.SessionRef{SessionID: s.ID}, &protocol.LastSeqResult{})
}

// Cancel cancels the running turn or step and returns after its end.
func (s *Session) Cancel(ctx context.Context, o CancelOptions) error {
	if o.Inbox == "" {
		o.Inbox = "keep"
	}
	return s.call(ctx, "session/cancel", protocol.SessionCancelParams{SessionID: s.ID, Scope: o.Scope, Inbox: o.Inbox}, &protocol.SessionCancelResult{})
}

// History returns up to limit stored events after afterSeq; limit 0 takes the
// host's default page.
func (s *Session) History(ctx context.Context, afterSeq int64, limit int) ([]protocol.Event, error) {
	var r protocol.SessionHistoryResult
	if err := s.call(ctx, "session/history", protocol.SessionHistoryParams{SessionID: s.ID, AfterSeq: afterSeq, Limit: limit}, &r); err != nil {
		return nil, err
	}
	return r.Events, nil
}

// Subscription receives the session.event notifications of one session. The
// codec delivers notifications on one goroutine, so a subscriber never blocks
// it: when the buffer is full the subscription ends with ErrSlowConsumer.
type Subscription struct {
	h       *Harness
	session string
	c       chan protocol.Event
	err     error // guarded by h.mu; set once, when c is closed
}

// Events subscribes to the session's live events with a buffer of at least 1.
// A subscription made after the connection ended is ended already.
func (s *Session) Events(buffer int) *Subscription {
	sub := &Subscription{h: s.h, session: s.ID, c: make(chan protocol.Event, max(buffer, 1))}
	s.h.mu.Lock()
	defer s.h.mu.Unlock()
	if s.h.subsErr != nil {
		sub.end(s.h.subsErr)
	} else {
		s.h.subs[s.ID] = append(s.h.subs[s.ID], sub)
	}
	return sub
}

// C yields the events in order and is closed when the subscription ends.
func (sub *Subscription) C() <-chan protocol.Event { return sub.c }

// Err is why the subscription ended, or nil while it runs.
func (sub *Subscription) Err() error {
	sub.h.mu.Lock()
	defer sub.h.mu.Unlock()
	return sub.err
}

// Close ends the subscription with ErrSubscriptionClosed.
func (sub *Subscription) Close() {
	sub.h.mu.Lock()
	defer sub.h.mu.Unlock()
	sub.h.unsubscribe(sub)
	sub.end(ErrSubscriptionClosed)
}

// end closes c once; the caller holds h.mu, under which every send happens too.
func (sub *Subscription) end(err error) {
	if sub.err == nil {
		sub.err = err
		close(sub.c)
	}
}

func (h *Harness) unsubscribe(sub *Subscription) {
	list := h.subs[sub.session]
	for i, s := range list {
		if s == sub {
			list = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(h.subs, sub.session)
	} else {
		h.subs[sub.session] = list
	}
}

// event hands a session.event to the session's subscribers without blocking.
func (h *Harness) event(raw json.RawMessage) {
	var n protocol.SessionEventNotification
	if json.Unmarshal(raw, &n) != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs[n.SessionID] {
		select {
		case sub.c <- n.Event:
		default:
			h.unsubscribe(sub)
			sub.end(ErrSlowConsumer)
		}
	}
}

// endSubscriptions ends every subscription, and any made later, with err.
func (h *Harness) endSubscriptions(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subsErr = err
	for _, list := range h.subs {
		for _, sub := range list {
			sub.end(err)
		}
	}
	clear(h.subs)
}
