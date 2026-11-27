package title

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// CodeUnavailable is a title operation on a session the service does not follow.
const CodeUnavailable herr.Reason = "title/unavailable"

// Model makes the one attempt of a title request: the buffered auxiliary call
// on the request's route, never retried and never on another route
// (DH-ADR-VIEW-6). The error is for a call that made no attempt; an attempt that
// failed is returned with its Finish.Failure. The adapter over llm.Runtime is
// llm/titlemodel, since nothing under internal/session imports the runtime
// (DH-D-LLM-1).
type Model interface {
	Attempt(ctx context.Context, req session.Request) (Attempt, error)
}

// Attempt is the record of a title request's one attempt.
type Attempt struct {
	RequestAsOf   event.Seq
	RequestDigest string
	// Message is the reply, nil when nothing was produced.
	Message  *vocab.Message
	Finish   vocab.Finish
	Usage    *vocab.Usage
	Duration time.Duration
	ServedBy string
}

// Option configures a Service.
type Option func(*Service)

// WithModel binds m from the start, as Use does; its binding lasts until Close.
func WithModel(m Model) Option { return func(s *Service) { s.initial = m } }

// WithConfig sets title.Config.
func WithConfig(c Config) Option { return func(s *Service) { s.cfg = c } }

// WithGenerator sets title.GeneratorConfig.
func WithGenerator(g GeneratorConfig) Option { return func(s *Service) { s.gen = g } }

// WithProvider registers a provider beside first-prompt.
func WithProvider(p Provider) Option { return func(s *Service) { s.extra = append(s.extra, p) } }

// WithWarn sets where a failed automatic generation is reported.
func WithWarn(f func(format string, args ...any)) Option { return func(s *Service) { s.warn = f } }

// Service is Titles (session-views.md sec. 6.1): it follows attached sessions,
// generates their titles after a turn ends, and renames, unpins and refreshes
// them on request.
type Service struct {
	cfg       Config
	gen       GeneratorConfig
	extra     []Provider
	reg       *views.Registry
	th        views.Handle[Title]
	ih        views.Handle[TitleInput]
	initial   Model
	providers *Providers
	warn      func(string, ...any)
	// life is the service's lifetime: every generation runs under it, and
	// Close cancels it.
	life context.Context
	stop context.CancelFunc

	mu       sync.Mutex
	sessions map[session.ID]*entry
	bound    *binding   // the model generations call; nil when none is bound
	closed   bool       // Close ran
	inflight int        // generations running, under mu
	idle     *sync.Cond // signalled whenever a generation ends
}

// binding is one Use of a model: its generations run under ctx and are counted
// in running (under Service.mu), so its release can cancel and join them.
type binding struct {
	m       Model
	ctx     context.Context
	cancel  context.CancelFunc
	running int
}

// entry is one attached session. mu serializes the appends and decisions of
// its title operations; the model call runs outside it.
type entry struct {
	mu      sync.Mutex
	s       session.Session
	ctx     context.Context
	cancel  context.CancelFunc
	running bool // an automatic generation is in flight
}

// New registers the title and titleInput folds in reg and returns the service.
// It generates only while a model is bound (WithModel, Use); without one, titles
// keep their fallback and still rename, unpin and fold.
func New(reg *views.Registry, opts ...Option) (*Service, error) {
	if reg == nil {
		return nil, errors.New("title: the service needs the view registry")
	}
	s := &Service{reg: reg, providers: NewProviders(), warn: func(string, ...any) {}, sessions: map[session.ID]*entry{}}
	s.idle = sync.NewCond(&s.mu)
	s.life, s.stop = context.WithCancel(context.Background())
	for _, o := range opts {
		o(s)
	}
	s.cfg, s.gen = s.cfg.withDefaults(), s.gen.withDefaults()
	for _, p := range append([]Provider{NewFirstPrompt(s.gen)}, s.extra...) {
		if err := s.providers.Register(p); err != nil {
			return nil, err
		}
	}
	if _, err := s.providers.Get(s.cfg.Provider); err != nil {
		return nil, err
	}
	var err error
	if s.th, err = views.Register(reg, TitleFold(s.cfg)); err != nil {
		return nil, err
	}
	if s.ih, err = views.Register(reg, InputFold(s.gen)); err != nil {
		return nil, err
	}
	if s.initial != nil {
		s.Use(s.initial)
	}
	return s, nil
}

// Use binds m as the model generations call, in place of any bound one, and
// returns its release: release cancels the generations m serves, waits until
// each has settled its attempt, and unbinds m. Component session.title.llm
// binds the runtime's model this way and releases it when it unloads.
func (s *Service) Use(m Model) (release func()) {
	b := &binding{m: m}
	b.ctx, b.cancel = context.WithCancel(s.life)
	s.mu.Lock()
	s.bound = b
	s.mu.Unlock()
	return func() {
		b.cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.bound == b {
			s.bound = nil
		}
		for b.running > 0 {
			s.idle.Wait()
		}
	}
}

// Close ends the service with its component: it stops following sessions,
// cancels every generation and waits until each has settled its attempt. An
// operation after Close is title/unavailable.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.sessions = map[session.ID]*entry{}
	s.mu.Unlock()
	s.stop()
	s.Wait()
}

// begin counts a generation on the bound model, or refuses it when none is
// bound or the service is closed.
func (s *Service) begin() (*binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.closed:
		return nil, herr.New(CodeUnavailable, "title: the service is closed")
	case s.bound == nil:
		return nil, herr.New(CodeUnavailable, "title: no title model is bound (session.title.llm is off)")
	}
	s.bound.running++
	s.inflight++
	return s.bound, nil
}

func (s *Service) end(b *binding) {
	s.mu.Lock()
	b.running--
	s.inflight--
	s.idle.Broadcast()
	s.mu.Unlock()
}

// Attach follows sess. Every request no attempt settled (the process stopped
// mid-generation) is settled interrupted, so the next turn may try again.
func (s *Service) Attach(ctx context.Context, sess session.Session) error {
	if sess == nil {
		return errors.New("title: Attach needs the session")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return herr.New(CodeUnavailable, "title: the service is closed")
	}
	e, ok := s.sessions[sess.ID()]
	if !ok {
		e = &entry{s: sess}
		e.ctx, e.cancel = context.WithCancel(s.life)
		s.sessions[sess.ID()] = e
	}
	s.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ok {
		e.s = sess
		return nil
	}
	t, err := s.title(ctx, e)
	if err != nil || len(t.Pending) == 0 {
		return err
	}
	as := make([]AttemptPayload, len(t.Pending))
	for i, seq := range t.Pending {
		as[i] = AttemptPayload{RequestSeq: seq, Outcome: OutcomeInterrupted}
	}
	return s.settleLocked(ctx, e, nil, as...)
}

// Detach stops following session id and cancels its automatic generation.
func (s *Service) Detach(id session.ID) {
	s.mu.Lock()
	e, ok := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if ok {
		e.cancel()
	}
}

// Wait blocks until every generation started has ended.
func (s *Service) Wait() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.inflight > 0 {
		s.idle.Wait()
	}
}

func (s *Service) entry(id session.ID) (*entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.sessions[id]; ok {
		return e, nil
	}
	return nil, herr.Newf(CodeUnavailable, "title: session %s is not attached", id)
}

func (s *Service) title(ctx context.Context, e *entry) (Title, error) {
	snap, err := s.reg.Read(ctx, e.s, FoldKey)
	if err != nil {
		return Title{}, err
	}
	return s.th.Get(snap)
}

// Get is session id's title fold.
func (s *Service) Get(ctx context.Context, id session.ID) (Title, error) {
	e, err := s.entry(id)
	if err != nil {
		return Title{}, err
	}
	return s.title(ctx, e)
}

// Rename gives session id the user's title, pinned: no generation replaces it
// until Unpin. A title that normalizes to nothing is title/invalid.
func (s *Service) Rename(ctx context.Context, id session.ID, text string) (Title, error) {
	t := Normalize(text, s.cfg.MaxTitleBytes)
	if t == "" {
		return Title{}, herr.New(CodeInvalid, "title: the new title is empty once normalized")
	}
	return s.record(ctx, id, func(Title) (TitlePayload, bool) {
		return TitlePayload{Title: t, Source: SourceUser, Pinned: true}, true
	})
}

// Unpin lets generation replace session id's title again; the text stays. An
// unpinned title is left as it is.
func (s *Service) Unpin(ctx context.Context, id session.ID) (Title, error) {
	return s.record(ctx, id, func(cur Title) (TitlePayload, bool) {
		if !cur.Pinned {
			return TitlePayload{}, false
		}
		return TitlePayload{Title: cur.Text, Source: SourceUser, MessageSeqs: cur.MessageSeqs}, true
	})
}

func (s *Service) record(ctx context.Context, id session.ID, next func(Title) (TitlePayload, bool)) (Title, error) {
	e, err := s.entry(id)
	if err != nil {
		return Title{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, err := s.title(ctx, e)
	if err != nil {
		return Title{}, err
	}
	p, ok := next(cur)
	if !ok {
		return cur, nil
	}
	d, err := TitleType.Draft(p)
	if err != nil {
		return Title{}, err
	}
	if _, err := e.s.Append(ctx, d); err != nil {
		return Title{}, err
	}
	if _, err := e.s.Flush(ctx); err != nil {
		return Title{}, err
	}
	return s.title(ctx, e)
}

// Refresh generates session id's title now, from all its own prompts. A pinned
// title is title/pinned and unchanged; a reply that is not a title is
// title/rejected; a model failure is returned as itself; with no model bound it
// is title/unavailable.
func (s *Service) Refresh(ctx context.Context, id session.ID) (Title, error) {
	e, err := s.entry(id)
	if err != nil {
		return Title{}, err
	}
	b, err := s.begin()
	if err != nil {
		return Title{}, err
	}
	defer s.end(b)
	return s.generate(ctx, e, b, true)
}

// generate is one title generation on b, counted by the caller: the request is
// appended and flushed, the request rebuilt from it is sent, and exactly one
// attempt settles it, with the title in the same group when the reply is
// accepted and nothing moved meanwhile. Releasing b or closing the service
// cancels it; the attempt still settles, as cancelled.
func (s *Service) generate(ctx context.Context, e *entry, b *binding, refresh bool) (Title, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(b.ctx, cancel)()
	e.mu.Lock()
	seq, rev, seqs, err := s.requestLocked(ctx, e, refresh)
	sess := e.s
	e.mu.Unlock()
	if err != nil {
		return Title{}, err
	}
	var rec Attempt
	start := time.Now()
	req, err := sess.RequestFrom(seq)
	if err == nil {
		cctx, cancel := context.WithTimeout(ctx, s.gen.Timeout)
		rec, err = b.m.Attempt(cctx, req)
		cancel()
	}
	a := AttemptPayload{RequestSeq: seq, DurationMs: time.Since(start).Milliseconds()}
	var reply string
	if err == nil {
		asOf := rec.RequestAsOf
		a.RequestAsOf, a.RequestDigest, a.Finish, a.Usage, a.ServedBy = &asOf, rec.RequestDigest, rec.Finish.Reason, rec.Usage, rec.ServedBy
		if rec.Duration > 0 {
			a.DurationMs = rec.Duration.Milliseconds()
		}
		if rec.Message != nil {
			reply = messageText(*rec.Message)
		}
		if f := rec.Finish.Failure; f != nil {
			err = f
		} else if rec.Message == nil {
			err = errors.New("title: the attempt produced no reply")
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, rerr := s.title(ctx, e)
	if rerr != nil {
		return Title{}, rerr
	}
	var out error
	var title *TitlePayload
	text, ok := accept(reply, s.cfg.MaxTitleBytes)
	switch {
	case ctx.Err() != nil:
		a.Outcome, out = OutcomeCancelled, ctx.Err()
	case err != nil:
		a.Outcome, out = OutcomeFailed, err
		var f *vocab.Failure
		if errors.As(err, &f) {
			a.ErrorCode, a.ErrorClass = string(f.Code), string(f.Class)
		}
	case a.Finish != vocab.FinishStop:
		// A reply cut at max_tokens or one that calls a tool is not a title,
		// whatever its text (session-views.md sec. 6.2, Titles).
		a.Outcome, a.Text = OutcomeRejected, clip(reply, s.cfg.MaxTitleBytes)
		out = herr.Newf(CodeRejected, "title: the reply finished %q, not stop", a.Finish)
	case !ok:
		a.Outcome, a.Text = OutcomeRejected, clip(reply, s.cfg.MaxTitleBytes)
		out = herr.Newf(CodeRejected, "title: the reply %q is not a title", a.Text)
	case cur.Revision != rev || cur.Pinned:
		a.Outcome, a.Text = OutcomeSuperseded, text
		out = herr.New(CodeSuperseded, "title: the title changed while it was generated")
	default:
		a.Outcome, a.Text = OutcomeAccepted, text
		title = &TitlePayload{Title: text, Source: SourceGenerated, MessageSeqs: seqs, RequestSeq: &seq}
	}
	if err := s.settleLocked(context.WithoutCancel(ctx), e, title, a); err != nil {
		return Title{}, errors.Join(out, err)
	}
	t, rerr := s.title(context.WithoutCancel(ctx), e)
	if rerr != nil {
		return Title{}, rerr
	}
	return t, out
}

// requestLocked appends and flushes the next title request; the caller holds
// e.mu. It returns the request's seq, the revision it made and the prompts' seqs.
func (s *Service) requestLocked(ctx context.Context, e *entry, refresh bool) (event.Seq, int64, []event.Seq, error) {
	t, err := s.title(ctx, e)
	if err != nil {
		return 0, 0, nil, err
	}
	if refresh && t.Pinned {
		return 0, 0, nil, herr.New(CodePinned, "title: the title is pinned; Unpin it first")
	}
	p, err := s.providers.Get(s.cfg.Provider)
	if err != nil {
		return 0, 0, nil, err
	}
	prompts, err := s.prompts(ctx, e, t.Own)
	if err != nil {
		return 0, 0, nil, err
	}
	pr, err := p.Prompt(Input{Prompts: prompts, Current: t, Refresh: refresh})
	if err != nil {
		return 0, 0, nil, err
	}
	msgs := reduce(pr.Messages, s.gen.MaxInputBytes)
	if len(msgs) == 0 {
		return 0, 0, nil, herr.New(CodeNoInput, "title: the provider sent no prompt")
	}
	route := vocab.RouteID(s.gen.Route)
	if route == "" {
		if h, ok := e.s.RequestHeader(); ok {
			route = h.Route
		}
	}
	if route == "" {
		return 0, 0, nil, herr.New(CodeNoRoute, "title: no title route is configured and the session logged none")
	}
	rp := RequestPayload{Provider: p.ID(), Route: route, System: pr.System, MaxOutputTokens: s.gen.MaxOutputTokens,
		Effort: s.gen.effort(), Purpose: Purpose}
	for _, m := range msgs {
		rp.Messages, rp.MessageSeqs = append(rp.Messages, m.Text), append(rp.MessageSeqs, m.Seq)
	}
	d, err := RequestType.Draft(rp)
	if err != nil {
		return 0, 0, nil, err
	}
	r, err := e.s.Append(ctx, d)
	if err != nil {
		return 0, 0, nil, err
	}
	if _, err := e.s.Flush(ctx); err != nil {
		return 0, 0, nil, err
	}
	t, err = s.title(ctx, e)
	return r.Seq, t.Revision, rp.MessageSeqs, err
}

// settleLocked appends the attempts, and the title when one is accepted, as one
// group and flushes it; the caller holds e.mu.
func (s *Service) settleLocked(ctx context.Context, e *entry, t *TitlePayload, as ...AttemptPayload) error {
	var ds []event.Draft
	for _, a := range as {
		d, err := AttemptType.Draft(a)
		if err != nil {
			return err
		}
		ds = append(ds, d)
	}
	if t != nil {
		td, err := TitleType.Draft(*t)
		if err != nil {
			return err
		}
		ds = append(ds, td)
	}
	if _, err := e.s.AppendGroup(ctx, ds); err != nil {
		return err
	}
	_, err := e.s.Flush(ctx)
	return err
}

// prompts is every eligible own prompt of e's session, each clipped to
// MaxInputBytes.
func (s *Service) prompts(ctx context.Context, e *entry, from event.Seq) ([]Message, error) {
	evs, err := e.s.Read(ctx, from, int(e.s.Len()-from), session.WithCold())
	if err != nil {
		return nil, fmt.Errorf("title: read the prompts: %w", err)
	}
	var out []Message
	for _, ev := range evs {
		if text, ok := promptText(ev); ok {
			out = append(out, Message{Seq: ev.Seq(), Text: clip(text, s.gen.MaxInputBytes)})
		}
	}
	return out, nil
}

func messageText(m vocab.Message) string {
	var parts []string
	for _, b := range m.Blocks() {
		if b.Text != nil {
			parts = append(parts, b.Text.Text)
		}
	}
	return strings.Join(parts, "")
}

// due reports whether e's session is due an automatic title after a turn:
// never for a subagent or a pinned title or while a request is pending; under
// first-prompt once its first own prompt exists, under every-prompt whenever an
// own prompt is newer than the last request covered.
func (s *Service) due(ctx context.Context, e *entry) bool {
	if e.s.Header().Origin == "subagent" {
		return false
	}
	snap, err := s.reg.Read(ctx, e.s, FoldKey, InputKey)
	if err != nil {
		return false
	}
	t, err1 := s.th.Get(snap)
	in, err2 := s.ih.Get(snap)
	p, err3 := s.providers.Get(s.cfg.Provider)
	if err1 != nil || err2 != nil || err3 != nil || t.Pinned || len(t.Pending) != 0 || in.Count == 0 {
		return false
	}
	if p.Cadence() == EveryPrompt {
		return in.Latest.Seq > t.Covered
	}
	return t.Requests == 0
}

// Auto starts an automatic generation of session id's title when it is due and
// a model is bound, in the background (Wait waits for it). A failure is
// reported to WithWarn and leaves the fallback in place.
func (s *Service) Auto(id session.ID) {
	e, err := s.entry(id)
	if err != nil {
		return
	}
	e.mu.Lock()
	if e.running || !s.due(e.ctx, e) {
		e.mu.Unlock()
		return
	}
	b, err := s.begin()
	if err != nil {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.mu.Unlock()
	go func() {
		defer s.end(b)
		_, err := s.generate(e.ctx, e, b, false)
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
		if err != nil {
			s.warn("title: session %s: %v", id, err)
		}
	}()
}
