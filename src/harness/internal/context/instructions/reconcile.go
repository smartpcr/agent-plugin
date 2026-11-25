package instructions

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// FoldKey is the key of the fold registered with session/views.
const FoldKey = "instructions.visible"

// Producer is the producer named on every instructions message.
const Producer = "context.instructions"

// FoldView is the instructions.visible fold: the source record of the latest
// instructions message. It reads only the record, never a file.
func FoldView() views.Definition[Record] {
	return views.Definition[Record]{
		Key: FoldKey, Version: 1, Stored: true,
		Types: []string{event.UserMessage.Name()},
		Init:  func(session.Header) Record { return Record{} },
		Apply: func(s Record, ev event.Event) (Record, bool) {
			p, err := event.UserMessage.As(ev)
			if err != nil {
				return s, false
			}
			src, ok := p.Message.Source()
			if !ok || src.Kind != request.KindInstructions || len(src.Data) == 0 {
				return s, false
			}
			var r Record
			if json.Unmarshal(src.Data, &r) != nil {
				return s, false
			}
			return r, true
		},
	}
}

// Options are a Reconciler's collaborators.
type Options struct {
	// FS is the filesystem seam; required.
	FS     fs.FS
	Config Config
	// Watch, when set, watches every candidate path of the chain.
	Watch watch.Engine
	// Notify, when set, runs after a watch notice marked the chain; it must not
	// block.
	Notify func()
}

// Reconciler holds the instruction chain of one session.
type Reconciler struct {
	ctx   context.Context
	o     Options
	dirty atomic.Bool

	mu       sync.Mutex
	dir      string
	root     string
	obs      *Observation
	lastGood map[string]Entry
	watches  map[string]watch.Watch
	shown    string
}

// NewReconciler returns a reconciler whose watches live as long as ctx.
func NewReconciler(ctx context.Context, o Options) (*Reconciler, error) {
	if o.FS == nil {
		return nil, errors.New("instructions: the filesystem seam is a required injection")
	}
	if err := o.Config.Validate(); err != nil {
		return nil, err
	}
	o.Config = o.Config.withDefaults()
	r := &Reconciler{ctx: ctx, o: o, lastGood: map[string]Entry{}, watches: map[string]watch.Watch{}}
	r.dirty.Store(true)
	return r, nil
}

// SetDirectory is a working-directory change: the chain of dir under root is
// discovered at the next step. The root is held until the next change.
func (r *Reconciler) SetDirectory(dir, root string) {
	r.mu.Lock()
	r.dir, r.root = filepath.Clean(dir), filepath.Clean(root)
	r.mu.Unlock()
	r.dirty.Store(true)
}

// Resume restores what the model last saw from the fold: the next step
// reconciles, and reports a change only if the section differs from rec.
func (r *Reconciler) Resume(rec Record) {
	r.mu.Lock()
	r.shown = rec.Digest
	r.mu.Unlock()
	r.dirty.Store(true)
}

// Touch is a file-tool touch (fs.Observed) or a shell call's working directory:
// the chain is marked when path is one of its candidates or a directory of it.
func (r *Reconciler) Touch(path string) {
	r.mu.Lock()
	dir, root := r.dir, r.root
	r.mu.Unlock()
	if dir == "" || !filepath.IsAbs(path) {
		return
	}
	path = filepath.Clean(path)
	if fs.Within(root, path) && fs.Within(path, dir) {
		r.dirty.Store(true)
		return
	}
	parent := filepath.Dir(path)
	if !fs.Within(root, parent) || !fs.Within(parent, dir) {
		return
	}
	for _, n := range append(append([]string(nil), r.o.Config.Candidates...), r.o.Config.LocalCandidates...) {
		if strings.EqualFold(n, filepath.Base(path)) {
			r.dirty.Store(true)
			return
		}
	}
}

// Pending reports whether the next Next discovers the chain again.
func (r *Reconciler) Pending() bool { return r.dirty.Load() }

// Next reconciles a marked chain and renders the section. changed is true when
// the section differs from the one the model last saw, which it then is; an
// empty first section is not a change.
func (r *Reconciler) Next(ctx context.Context) (Section, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dir == "" {
		return Section{}, false, errors.New("instructions: the session has no working directory")
	}
	if r.dirty.Swap(false) || r.obs == nil {
		obs, err := Discover(ctx, r.o.FS, r.dir, r.root, r.o.Config)
		if err != nil {
			r.dirty.Store(true)
			return Section{}, false, err
		}
		r.keepLastGood(&obs)
		r.obs = &obs
		r.syncWatches(obs.Candidates)
	}
	sec := Render(*r.obs, r.o.Config.MaxBytes)
	if sec.Record.Digest == r.shown || (r.shown == "" && !hasFiles(sec.Record)) {
		return sec, false, nil
	}
	sec.Record.Form = FormUpdate
	if r.shown == "" {
		sec.Record.Form = FormBaseline
	}
	r.shown = sec.Record.Digest
	return sec, true, nil
}

func hasFiles(rec Record) bool {
	for _, f := range rec.Files {
		if f.State == Visible || f.State == Omitted {
			return true
		}
	}
	return false
}

// keepLastGood gives an unreadable file its own last-good text.
func (r *Reconciler) keepLastGood(obs *Observation) {
	good := map[string]Entry{}
	for i, e := range obs.Entries {
		switch e.File.State {
		case Visible:
			good[e.File.Path] = e
		case Unreadable:
			if g, ok := r.lastGood[e.File.Path]; ok {
				obs.Entries[i] = g
				good[e.File.Path] = g
			}
		}
	}
	r.lastGood = good
}

// syncWatches watches each candidate path once and closes the watches of paths
// that left the chain. A watch callback only marks the chain, so a Close here
// never waits on r.mu.
func (r *Reconciler) syncWatches(paths []string) {
	if r.o.Watch == nil {
		return
	}
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
		if _, ok := r.watches[p]; ok {
			continue
		}
		w, err := r.o.Watch.Watch(r.ctx, watch.Target{Path: p}, func(error) {
			r.dirty.Store(true)
			if r.o.Notify != nil {
				r.o.Notify()
			}
		})
		if err == nil {
			r.watches[p] = w
		}
	}
	for p, w := range r.watches {
		if !want[p] {
			_ = w.Close()
			delete(r.watches, p)
		}
	}
}

// Close stops every watch.
func (r *Reconciler) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for p, w := range r.watches {
		errs = append(errs, w.Close())
		delete(r.watches, p)
	}
	return errors.Join(errs...)
}

// Contributor is the instructions contributor of the request pipeline: at each
// step it returns the section when it changed since the model last saw it.
type Contributor struct {
	// For gives the reconciler of an agent's session.
	For func(agent.Ref) (*Reconciler, error)
}

var _ request.Contributor = Contributor{}

// Name is the source kind of its messages.
func (Contributor) Name() string { return request.KindInstructions }

// Required is false: a failure is skipped and recorded as context/skipped.
func (Contributor) Required() bool { return false }

// Contribute implements request.Contributor.
func (c Contributor) Contribute(ctx context.Context, in request.Input) (request.Output, error) {
	if c.For == nil {
		return request.Output{}, errors.New("instructions: no reconciler lookup")
	}
	r, err := c.For(in.Agent)
	if err != nil {
		return request.Output{}, err
	}
	sec, changed, err := r.Next(ctx)
	if err != nil || !changed {
		return request.Output{}, err
	}
	m, err := request.Message(request.KindInstructions, Producer, sec.Text, sec.Record)
	if err != nil {
		return request.Output{}, err
	}
	return request.Output{Messages: []vocab.Message{m}}, nil
}
