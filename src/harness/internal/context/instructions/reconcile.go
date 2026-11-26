package instructions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
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

// Reconciler holds the instruction chain of one session. It keeps no record of
// what the model saw: each step passes that in, read from the current surface,
// so a step that was never admitted, a compaction and a resume all leave it
// right.
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
}

// NewReconciler returns a reconciler whose watches live as long as ctx.
func NewReconciler(ctx context.Context, o Options) (*Reconciler, error) {
	if o.FS == nil {
		return nil, errors.New("instructions: the filesystem seam is a required injection")
	}
	if _, ok := o.FS.(fs.BoundedOpener); !ok {
		return nil, errors.New("instructions: the filesystem seam cannot read a bounded prefix")
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
// discovered at the next step. The same directory again changes nothing.
func (r *Reconciler) SetDirectory(dir, root string) {
	dir, root = filepath.Clean(dir), filepath.Clean(root)
	r.mu.Lock()
	same := r.dir == dir && r.root == root
	if r.root != root {
		// Last-good text is the text of a file of this project only.
		r.lastGood = map[string]Entry{}
	}
	r.dir, r.root = dir, root
	r.mu.Unlock()
	if !same {
		r.dirty.Store(true)
	}
}

// Touch is a file-tool touch (fs.Observed): the chain is marked when path is
// one of its candidates or a directory of it. path may be as given or as the
// seam resolved it.
func (r *Reconciler) Touch(path string) { r.touch(path, false) }

// TouchShell is a shell call's working directory: a shell may have rewritten
// any file under it, so the chain is marked when dir is under the project
// root.
func (r *Reconciler) TouchShell(dir string) { r.touch(dir, true) }

func (r *Reconciler) touch(path string, shell bool) {
	if !filepath.IsAbs(path) {
		return
	}
	r.mu.Lock()
	dir, root := r.dir, r.root
	var canonRoot string
	if r.obs != nil {
		canonRoot = r.obs.canonRoot
	}
	r.mu.Unlock()
	if dir == "" {
		return
	}
	path = filepath.Clean(path)
	if r.touches(path, dir, root, shell) {
		r.dirty.Store(true)
		return
	}
	if canonRoot != "" && canonRoot != root {
		rel, err := filepath.Rel(root, dir)
		if err == nil && r.touches(path, filepath.Join(canonRoot, rel), canonRoot, shell) {
			r.dirty.Store(true)
		}
	}
}

func (r *Reconciler) touches(path, dir, root string, shell bool) bool {
	if shell {
		return fs.Within(root, path)
	}
	if fs.Within(root, path) && fs.Within(path, dir) {
		return true
	}
	parent := filepath.Dir(path)
	if !fs.Within(root, parent) || !fs.Within(parent, dir) {
		return false
	}
	for _, n := range append(append([]string(nil), r.o.Config.Candidates...), r.o.Config.LocalCandidates...) {
		if strings.EqualFold(n, filepath.Base(path)) {
			return true
		}
	}
	return false
}

// Pending reports whether the next Next discovers the chain again.
func (r *Reconciler) Pending() bool { return r.dirty.Load() }

// Next reconciles a marked chain and renders the section. seen is the record
// of the section the model last saw (Seen of the current surface), nil when
// none is visible. changed is true when the section differs from seen: a
// baseline when nothing is visible and there is a file, a refusal or an
// unwatched candidate to record, an update otherwise.
func (r *Reconciler) Next(ctx context.Context, seen *Record) (Section, bool, error) {
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
		obs.Unwatched = r.syncWatches(obs.Root, obs.Candidates)
		r.obs = &obs
		if len(obs.Unwatched) > 0 {
			// Without a watch the chain is polled: read again at every step,
			// and the watch is tried again.
			r.dirty.Store(true)
		}
	}
	sec := Render(*r.obs, r.o.Config.MaxBytes)
	switch {
	case seen == nil && len(sec.Record.Files) == 0 && len(sec.Record.Refused) == 0 && len(sec.Record.Unwatched) == 0:
		return sec, false, nil
	case seen == nil:
		sec.Record.Form = FormBaseline
	case seen.Digest == sec.Record.Digest:
		return sec, false, nil
	default:
		sec.Record.Form = FormUpdate
	}
	return sec, true, nil
}

// Seen is the record of the latest instructions message on h, the surface the
// model sees: nil when none is (none was admitted, or a compaction shadowed
// it).
func Seen(h surface.History) (*Record, error) {
	msgs, err := h.Messages()
	if err != nil {
		return nil, err
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		src, ok := msgs[i].Source()
		if !ok || src.Kind != request.KindInstructions || len(src.Data) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(src.Data, &rec); err != nil {
			return nil, fmt.Errorf("instructions: the visible record: %w", err)
		}
		return &rec, nil
	}
	return nil, nil
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
// that left the chain. It returns the candidates whose watch failed, relative
// to root. A watch callback only marks the chain, so a Close here never waits
// on r.mu.
func (r *Reconciler) syncWatches(root string, paths []string) []Unwatched {
	if r.o.Watch == nil {
		return nil
	}
	var failed []Unwatched
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
		if err != nil {
			rel := p
			if q, rerr := filepath.Rel(root, p); rerr == nil {
				rel = filepath.ToSlash(q)
			}
			failed = append(failed, Unwatched{Path: rel, Error: err.Error()})
			continue
		}
		r.watches[p] = w
	}
	for p, w := range r.watches {
		if !want[p] {
			_ = w.Close()
			delete(r.watches, p)
		}
	}
	return failed
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
// step it returns the section when it differs from the one on the current
// surface. What the model saw is read from that surface, so a step the
// pipeline rejected is offered again, and a baseline a compaction shadowed is
// entered again.
type Contributor struct {
	// For gives the reconciler of an agent's session, its directory current.
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
	if err != nil || r == nil {
		return request.Output{}, err
	}
	seen, err := Seen(in.Visible)
	if err != nil {
		return request.Output{}, err
	}
	sec, changed, err := r.Next(ctx, seen)
	if err != nil || !changed {
		return request.Output{}, err
	}
	m, err := request.Message(request.KindInstructions, Producer, sec.Text, sec.Record)
	if err != nil {
		return request.Output{}, err
	}
	return request.Output{Messages: []vocab.Message{m}}, nil
}
