package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Error codes of reload and the edit path (DH-D-BOOT-7).
const (
	// ReasonStale is an edit made against a revision of the layers that is no longer
	// the one on disk: another writer got there first. Nothing was written; read the
	// layers again and retry.
	ReasonStale herr.Reason = "config/stale"
	// ReasonBusy is an edit that could not take the layer lock in time.
	ReasonBusy herr.Reason = "config/busy"
	// ReasonRejected is a reload or an edit whose candidate tree did not validate;
	// the running tree and the files are unchanged.
	ReasonRejected herr.Reason = "config/rejected"
	// ReasonNotWritable is an edit with no writable layer to land in.
	ReasonNotWritable herr.Reason = "config/not-writable"
	// ReasonComputed is an edit that would replace a value an expression computes,
	// made without Edit.Overwrite (DH-ADR-BOOT-7). Nothing was written.
	ReasonComputed herr.Reason = "config/computed"
	// ReasonStopping is a reload or an edit asked once shutdown began (Close).
	ReasonStopping herr.Reason = "config/stopping"
	// ReasonShadowed is an edit of a field a layer above the writable one sets, so
	// it would not take effect (DH-D-BOOT-7). Nothing was written; the error holds a
	// *ShadowedError naming the layers.
	ReasonShadowed herr.Reason = "config/shadowed"
)

// Shadow is one field of an edit that a higher layer shadows: the field
// ("probe.config.level") and the label of the layer that sets it.
type Shadow struct {
	Field string
	Layer string
}

// ShadowedError lists the fields of a refused edit that higher layers shadow.
type ShadowedError struct{ Fields []Shadow }

func (e *ShadowedError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = fmt.Sprintf("%s is set by layer %s, which is above the one edited", f.Field, f.Layer)
	}
	return strings.Join(parts, "; ")
}

// DefaultLockTimeout bounds the wait for the layer lock of an edit.
const DefaultLockTimeout = 5 * time.Second

// ReadFile reads one layer file; a missing file satisfies errors.Is(err, fs.ErrNotExist).
type ReadFile func(path string) ([]byte, error)

// Stack reads and parses the layers of the launch through read: the boot stack
// (profile.Stack) with its Read set to read. It is called for every reload and
// edit, so the layers are always the bytes on disk.
type Stack func(read ReadFile) ([]loader.Layer, []loader.Issue, error)

// ServiceOptions configure a Service.
type ServiceOptions struct {
	// Stack reads the layers. Required.
	Stack Stack
	// Loader is the running tree's loader; nil validates and writes without applying
	// (a second process editing the same home).
	Loader *loader.Service
	// Context is the context the loader's Reloaded notice is heard on (the tree's
	// root); with it the service makes the applied snapshot Current before any
	// Reloaded listener registered on it is told. Without it only the loader's
	// lifecycle observers registered after NewService see the new Current.
	Context *cordis.Context
	// Catalog and Evaluator are the static check's (see CheckOptions).
	Catalog   Catalog
	Evaluator loader.Evaluator
	// Required are the profile's required entry ids (profile.Definition.Required):
	// a candidate that removes or disables one is rejected (CheckOptions.Required).
	Required []string
	// Guard serialises writes to a layer file across processes; nil takes the
	// operating-system lock <layer>.lock for at most LockTimeout (DH-D-LOADER-13).
	Guard loader.WriteGuard
	// LockTimeout bounds the default guard's wait; zero is DefaultLockTimeout.
	LockTimeout time.Duration
	// Read reads a layer file for every read after the first; nil is os.ReadFile.
	Read ReadFile
	// Baseline are the layer files as the running tree was built from them (path to
	// bytes, nil for a missing file). When set, the first snapshot is built from them
	// rather than read again, so a change saved between boot's read and NewService
	// is seen by the first Watch poll.
	Baseline map[string][]byte
}

// Service is the validated reload and the one edit path of a launch's layers
// (DH-D-BOOT-7, DH-ADR-BOOT-8). A candidate, whether a file changed under the
// running tree or an edit, becomes effective only once its whole tree validates:
// the static check (Check, which resolves every literal config through its
// component's schema) and, for every running entry whose config changes, that
// fiber's own candidate config with the tree's hooks (DH-D-CORDIS-5). A rejected
// candidate changes nothing: no file, no fiber.
//
// Reloads and edits are read, checked and written one at a time (op). A candidate
// that validates is accepted (Desired) at once and becomes Current only once the
// running tree has applied it and settled. The tree is applied with no lock of the
// service held, so a loader listener may call any method, and every Reload and
// Apply returns only once its own tree, or a newer accepted one, has settled or
// failed (settle). The loader tells its listeners from inside ApplyLayers, on the
// goroutine that called it, once the tree has settled; the service hears that
// first (a lifecycle observer registered by NewService and a Reloaded listener
// prepended on Context) and makes the snapshot that goroutine is applying Current
// then, so a listener sees the tree it is told about (published). No application
// waits for another (the loader serialises reconciliations itself), and Current
// is resolved only from applications whose reconciliation is known to be over
// (resolveLocked), so it never names a tree still being applied. Listeners are
// told outside every lock.
type Service struct {
	opts ServiceOptions

	op sync.Mutex

	mu       sync.Mutex
	current  *Snapshot                 // applied by the running tree and settled
	desired  *Snapshot                 // accepted: validated and on disk
	seq      uint64                    // the accept order of snapshots
	apps     map[uint64][]*application // in flight, per goroutine, innermost last
	settled  []*application            // complete, not yet resolved into Current
	rejected string
	closed   bool

	lmu       sync.Mutex
	listeners map[int]func(error)
	next      int
}

// Snapshot is the layers as read at one revision, checked.
type Snapshot struct {
	// Revision is the digest of every layer file read; an edit names it.
	Revision string
	Layers   []loader.Layer
	Report   *Report

	files map[string][]byte
	docs  map[string]*loader.LayerDocument
	seq   uint64
}

// Writable returns the path of the writable layer, or "".
func (s *Snapshot) Writable() string {
	for _, l := range s.Layers {
		if l.Writable {
			return l.Path
		}
	}
	return ""
}

// NewService reads and checks the layers once; it does not apply them (boot
// applies the first tree itself).
func NewService(opts ServiceOptions) (*Service, error) {
	if opts.Stack == nil {
		return nil, errors.New("config: ServiceOptions.Stack is required")
	}
	s := &Service{opts: opts, listeners: map[int]func(error){}, apps: map[uint64][]*application{}}
	read := s.reader()
	if opts.Baseline != nil {
		read = baselineReader(opts.Baseline, read)
	}
	snap, err := s.readWith(read)
	if err != nil {
		return nil, err
	}
	s.current, s.desired = snap, snap
	if opts.Loader != nil {
		opts.Loader.OnLifecycle(func(loader.LifecycleEvent) { s.published() })
		if opts.Context != nil {
			if _, err := loader.Reloaded.Prepend(opts.Context, func(_ *cordis.Context, n loader.ReloadNotice) error {
				if n.Loader == opts.Loader {
					s.published()
				}
				return nil
			}); err != nil {
				return nil, fmt.Errorf("config: hear loader.Reloaded: %w", err)
			}
		}
	}
	return s, nil
}

// Close stops admitting reloads and edits: each is then ReasonStopping. One in
// flight finishes.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *Service) stopping() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return herr.New(ReasonStopping, "shutting down: reloads and edits are no longer admitted")
	}
	return nil
}

// Current is the effective snapshot: the last accepted one the running tree has
// applied and settled (without a running tree, the last accepted one).
func (s *Service) Current() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Desired is the last snapshot accepted: validated and on disk. It differs from
// Current while the running tree is applying it.
func (s *Service) Desired() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.desired
}

// Pending reports whether an accepted snapshot is not yet effective.
func (s *Service) Pending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.desired != s.current
}

// Revision is the accepted snapshot's revision: what the layers on disk were when
// the service last read them, and what an edit is made against.
func (s *Service) Revision() string { return s.Desired().Revision }

// Effective is the config the entry's component runs with, as JSON: what its
// running fiber resolved (component defaults applied, expressions evaluated).
// Without a running fiber of it (a second process, or an entry not loaded) it is
// the effective snapshot's literal config resolved through the component's schema
// on a scratch fiber; ok is false when neither is known (a config holding an
// expression that only a load evaluates, or no catalogue).
func (s *Service) Effective(id string) (value json.RawMessage, ok bool) {
	if ldr := s.opts.Loader; ldr != nil {
		if entry := ldr.Find(id); entry != nil {
			if fiber := ldr.Fiber(entry); fiber != nil {
				if cfg, has := fiber.EffectiveConfig(); has {
					if raw, err := json.Marshal(cfg); err == nil {
						return raw, true
					}
				}
			}
		}
	}
	if s.opts.Catalog == nil {
		return nil, false
	}
	var e *loader.EntryOptions
	r := s.Current().Report
	r.walk(r.Tree, "", func(eid string, opt loader.EntryOptions) {
		if eid == id && e == nil && !opt.Group {
			e = &opt
		}
	})
	if e == nil || holdsExpr(e.Config) {
		return nil, false
	}
	item, err := s.opts.Catalog.Lookup(e.Name)
	if err != nil || item.Component == nil {
		return nil, false
	}
	var pre preflight
	defer pre.close()
	var raw json.RawMessage
	if len(bytes.TrimSpace(e.Config)) > 0 {
		raw = e.Config
	}
	cfg, err := pre.resolved(item.Component, raw)
	if err != nil {
		return nil, false
	}
	out, err := json.Marshal(cfg)
	return out, err == nil
}

// Draft reads the layers on disk now, checked, without making them effective: the
// read an edit starts from.
func (s *Service) Draft() (*Snapshot, error) {
	snap, err := s.read()
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// OnRejected registers fn to be told of each rejected candidate, once per
// revision rejected.
func (s *Service) OnRejected(fn func(error)) (remove func()) {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	id := s.next
	s.next++
	s.listeners[id] = fn
	return func() {
		s.lmu.Lock()
		defer s.lmu.Unlock()
		delete(s.listeners, id)
	}
}

func (s *Service) notify(err error) {
	s.lmu.Lock()
	fns := make([]func(error), 0, len(s.listeners))
	ids := make([]int, 0, len(s.listeners))
	for id := range s.listeners {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		fns = append(fns, s.listeners[id])
	}
	s.lmu.Unlock()
	for _, fn := range fns {
		fn(err)
	}
}

// Reload reads the layers again and, if they changed and the candidate validates,
// applies them to the running tree. A candidate that does not validate, a layer
// that does not parse included, is rejected whole with its positioned issues: the
// running tree is untouched, and the listeners are told once for the content
// rejected (the digest of the bytes read), after every lock is released.
func (s *Service) Reload() error {
	s.op.Lock()
	then, err := s.reload()
	s.op.Unlock()
	if terr := then(); err == nil {
		err = terr
	}
	return err
}

func (s *Service) reload() (func() error, error) {
	none := func() error { return nil }
	if err := s.stopping(); err != nil {
		return none, err
	}
	snap, err := s.read()
	if err != nil {
		err = herr.Wrap(ReasonRejected, err, "reload rejected")
		return s.reject(snap.Revision, err), err
	}
	if snap.Revision == s.Revision() {
		s.mu.Lock()
		s.rejected = ""
		s.mu.Unlock()
		return none, nil
	}
	if err := snap.Report.Err(); err != nil {
		err = herr.Wrap(ReasonRejected, err, "reload rejected")
		return s.reject(snap.Revision, err), err
	}
	return s.adopt(snap), nil
}

// reject records revision as rejected and returns the notification to publish once
// the locks are released: none when that revision was rejected already.
func (s *Service) reject(revision string, err error) func() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision == s.rejected {
		return func() error { return nil }
	}
	s.rejected = revision
	return func() error {
		s.notify(err)
		return nil
	}
}

// adopt accepts the validated snap (Desired); the caller holds op, so snapshots
// are accepted in the order they were read. It returns the application, which the
// caller runs once op is released (settle). Without a running tree snap is
// effective at once.
func (s *Service) adopt(snap *Snapshot) func() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	snap.seq = s.seq
	s.desired, s.rejected = snap, ""
	if s.opts.Loader == nil {
		s.current = snap
		return func() error { return nil }
	}
	return s.settle
}

// settle applies the latest accepted snapshot to the running tree and returns once
// it has settled, so a caller's success means its tree (or a newer accepted one,
// which holds every earlier write) is what runs. No lock of the service is held
// while the loader applies, and an application never waits for another one: the
// loader serialises the reconciliations itself, and ApplyLayers waits for every
// fiber, so an edit made while another application is stuck returns only once that
// one has settled too. A listener may therefore reload or edit, on its own
// goroutine (nested) or by handing the call to another one and waiting for it.
// When a newer snapshot was accepted meanwhile it is applied before returning.
// Current follows from the applications that completed (resolve). A snapshot the
// loader refused stops being Desired, so the next Watch poll tries the files again.
func (s *Service) settle() error {
	g := goid()
	s.mu.Lock()
	app := s.startLocked(g)
	for {
		s.mu.Unlock()
		err := s.opts.Loader.ApplyLayers(app.snap.Layers)
		s.mu.Lock()
		if stack := s.apps[g][:len(s.apps[g])-1]; len(stack) > 0 {
			s.apps[g] = stack
		} else {
			delete(s.apps, g)
		}
		if err == nil {
			s.completeLocked(app)
		} else {
			s.resolveLocked()
		}
		target := app.snap
		switch {
		case s.desired.seq > target.seq && s.desired != s.current:
			// started before unlocking, so no resolve sees the tree it is about to
			// replace as the last one reconciled
			app = s.startLocked(g)
			continue
		case err == nil || s.current.seq >= target.seq:
			s.mu.Unlock()
			return nil
		}
		s.desired = s.current
		s.mu.Unlock()
		return herr.Wrap(ReasonRejected, err, "reload not applied")
	}
}

// application is one settle's ApplyLayers of snap; complete once the loader has
// told of its settled reconciliation or ApplyLayers returned.
type application struct {
	snap     *Snapshot
	complete bool
}

// startLocked registers an application of the desired snapshot on goroutine g;
// s.mu is held.
func (s *Service) startLocked(g uint64) *application {
	app := &application{snap: s.desired}
	s.apps[g] = append(s.apps[g], app)
	return app
}

// published runs when the loader tells of a settled reconciliation, before the
// listeners registered after the service. The loader tells on the goroutine whose
// ApplyLayers reconciled, so the reconciliation is that goroutine's innermost
// application, which is complete. A notice on a goroutine applying nothing (a
// restart, a reconciliation not made by the service) changes nothing.
func (s *Service) published() {
	g := goid()
	s.mu.Lock()
	defer s.mu.Unlock()
	if stack := s.apps[g]; len(stack) > 0 {
		s.completeLocked(stack[len(stack)-1])
	}
}

// completeLocked records that app's reconciliation has settled; s.mu is held.
func (s *Service) completeLocked(app *application) {
	if app.complete {
		return
	}
	app.complete = true
	s.settled = append(s.settled, app)
	s.resolveLocked()
}

// resolveLocked makes Current the snapshot the running tree last settled, from the
// completed applications not yet resolved (settled); s.mu is held. The loader
// reconciles one at a time, but in the order the applications reached it, which
// need not be the order they were accepted, and it may tell of them in any order.
//   - No application in flight: every reconciliation is over, so the loader's tree
//     is the last one reconciled; the newest settled snapshot composing it is
//     Current (the newest settled one when none does, the tree having been edited
//     at runtime).
//   - Some still in flight: one settled snapshot with none older in flight is
//     Current, as no older tree can be reconciled after it. Otherwise Current waits
//     for the rest, staying Pending rather than guessing; an application's caller
//     may thus return, its tree settled, before Current names it.
//
// Current never moves to a snapshot older than itself.
func (s *Service) resolveLocked() {
	if len(s.settled) == 0 {
		return
	}
	var inflight []*application
	for _, stack := range s.apps {
		for _, app := range stack {
			if !app.complete {
				inflight = append(inflight, app)
			}
		}
	}
	var pick *Snapshot
	if len(inflight) > 0 {
		if len(s.settled) > 1 {
			return
		}
		pick = s.settled[0].snap
		for _, app := range inflight {
			if app.snap.seq <= pick.seq {
				return
			}
		}
	} else {
		running := s.opts.Loader.Snapshot()
		for _, app := range s.settled {
			if pick == nil || app.snap.seq > pick.seq {
				pick = app.snap
			}
		}
		var match *Snapshot
		for _, app := range s.settled {
			if composes(running, app.snap.Report.Tree) && (match == nil || app.snap.seq > match.seq) {
				match = app.snap
			}
		}
		if match != nil {
			pick = match
		}
	}
	s.settled = nil
	if pick.seq > s.current.seq {
		s.current = pick
	}
}

// composes reports whether the loader's tree running is the composed tree, an
// anonymous composed entry matching whatever id the loader gave it.
func composes(running, composed []loader.EntryOptions) bool {
	if len(running) != len(composed) {
		return false
	}
	for i, r := range running {
		c := composed[i]
		if c.DisabledExpr != r.DisabledExpr || !composes(r.Children, c.Children) {
			return false
		}
		if c.ID == "" {
			c.ID = r.ID
		}
		r.Children, c.Children = nil, nil
		rj, rerr := json.Marshal(r)
		cj, cerr := json.Marshal(c)
		if rerr != nil || cerr != nil || !bytes.Equal(rj, cj) {
			return false
		}
	}
	return true
}

// goid is the calling goroutine's id, read from its stack header ("goroutine N
// [..."). Go has no other way to name the goroutine a synchronous callback runs on,
// which is what ties a loader publication to the ApplyLayers that made it.
func goid() uint64 {
	var buf [64]byte
	b := bytes.TrimPrefix(buf[:runtime.Stack(buf[:], false)], []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i >= 0 {
		b = b[:i]
	}
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}

// Watch reloads whenever a layer file read changes, polling every interval, until
// ctx ends or the service is closed. A rejected change is reported through
// OnRejected, once per content.
func (s *Service) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if s.stopping() != nil {
			return
		}
		cur := s.Desired()
		if digest(readAll(cur.files, s.reader())) != cur.Revision {
			_ = s.Reload()
		}
	}
}

// Edit is a change to the writable layer: operations appended to it, made against
// the revision the editor read. An operation that would replace a value an
// expression computes is refused unless Overwrite says to replace it.
type Edit struct {
	Revision  string
	Ops       []loader.Op
	Overwrite bool
}

// Candidate checks the tree the edit would give, against the layers on disk now,
// without writing anything. A stale revision is ReasonStale.
func (s *Service) Candidate(edit Edit) (*Snapshot, error) {
	snap, err := s.read()
	if err != nil {
		return nil, err
	}
	return s.candidate(snap, edit)
}

func (s *Service) candidate(snap *Snapshot, edit Edit) (*Snapshot, error) {
	if edit.Revision != snap.Revision {
		return nil, herr.Newf(ReasonStale, "layers changed since revision %s (now %s): read again and retry", short(edit.Revision), short(snap.Revision))
	}
	if !edit.Overwrite {
		if computed := snap.Report.computed(edit.Ops); len(computed) > 0 {
			return nil, herr.Newf(ReasonComputed, "computed by an expression, so the edit must say to overwrite it: %s", strings.Join(computed, ", "))
		}
	}
	layers := make([]loader.Layer, len(snap.Layers))
	copy(layers, snap.Layers)
	writable := -1
	for i := range layers {
		if layers[i].Writable {
			layers[i].Ops = append(append([]loader.Op{}, layers[i].Ops...), edit.Ops...)
			writable = i
			break
		}
	}
	if writable < 0 {
		return nil, herr.New(ReasonNotWritable, "no writable layer")
	}
	next := &Snapshot{Revision: snap.Revision, Layers: layers, files: snap.files, docs: snap.docs}
	next.Report = s.check(layers, snap.docs)
	if shadowed := shadows(next.Report.Index, layers, writable, edit.Ops); len(shadowed) > 0 {
		return nil, herr.Wrap(ReasonShadowed, &ShadowedError{Fields: shadowed}, "edit shadowed")
	}
	return next, nil
}

// shadows lists the fields ops write that, in the composed tree, a layer above the
// writable one (layers[writable]) supplies: an edit of them would not take effect.
// A layer above shadows a written field when it supplies that field, a field
// enclosing it (Index.Owner) or a field inside it: an override of the whole config
// does not take effect where a higher layer merges config.level.
func shadows(index *loader.Index, layers []loader.Layer, writable int, ops []loader.Op) []Shadow {
	above := map[string]bool{}
	for _, l := range layers[writable+1:] {
		above[l.Label] = true
	}
	inner := map[string][]loader.Ownership{}
	for _, o := range index.Owners() {
		if o.Field != "" && above[o.Label] {
			inner[o.ID] = append(inner[o.ID], o)
		}
	}
	var found []Shadow
	seen := map[string]bool{}
	add := func(id, field, label string) {
		key := joinField(id, field)
		if !seen[key] {
			seen[key] = true
			found = append(found, Shadow{Field: key, Layer: label})
		}
	}
	for _, op := range ops {
		for _, field := range opFields(op) {
			if owner, ok := index.Owner(op.ID, field); ok && above[owner] {
				add(op.ID, field, owner)
				continue
			}
			for _, o := range inner[op.ID] {
				if within(o.Field, field) {
					add(op.ID, o.Field, o.Label)
				}
			}
		}
	}
	return found
}

// within reports whether the field path lies inside parent ("config.level" in
// "config", "config.list[0]" in "config.list").
func within(path, parent string) bool {
	return strings.HasPrefix(path, parent+".") || strings.HasPrefix(path, parent+"[")
}

// opFields lists the entry fields a merge or override writes: each leaf of a merge
// patch under "config", each key an override sets.
func opFields(op loader.Op) []string {
	switch op.Kind {
	case loader.OpOverride:
		fields := make([]string, 0, len(op.Set))
		for k := range op.Set {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		return fields
	case loader.OpMerge:
		var v any
		if err := json.Unmarshal(op.Patch, &v); err != nil {
			return []string{"config"}
		}
		var fields []string
		var walk func(v any, path string)
		walk = func(v any, path string) {
			m, ok := v.(map[string]any)
			if !ok || len(m) == 0 {
				fields = append(fields, path)
				return
			}
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(m[k], path+"."+k)
			}
		}
		walk(v, "config")
		return fields
	}
	return nil
}

// Apply is the one edit path (DH-D-BOOT-7): under the layer lock it reads the
// layers, refuses a stale revision, an edit a higher layer shadows (ReasonShadowed)
// and a computed field without Overwrite, checks the candidate, writes the writable
// layer (keeping its comments) and applies the new tree. A refused or rejected edit
// writes nothing. It returns the new revision once the running tree has applied
// it, or a newer accepted tree, and settled; the tree is applied after every lock
// of the service is released (see settle).
func (s *Service) Apply(ctx context.Context, edit Edit) (string, error) {
	s.op.Lock()
	then, rev, err := s.apply(ctx, edit)
	s.op.Unlock()
	if terr := then(); err == nil {
		err = terr
	}
	return rev, err
}

func (s *Service) apply(ctx context.Context, edit Edit) (func() error, string, error) {
	none := func() error { return nil }
	if err := s.stopping(); err != nil {
		return none, "", err
	}
	path := s.Current().Writable()
	if path == "" {
		return none, "", herr.New(ReasonNotWritable, "no writable layer")
	}
	guard := s.opts.Guard
	if guard == nil {
		guard = LockGuard(ctx, s.opts.LockTimeout)
	}
	var adopted *Snapshot
	err := guard(path, func() error {
		snap, err := s.read()
		if err != nil {
			return err
		}
		cand, err := s.candidate(snap, edit)
		if err != nil {
			return err
		}
		if err := cand.Report.Err(); err != nil {
			return herr.Wrap(ReasonRejected, err, "edit rejected")
		}
		if err := writeLayer(snap, cand); err != nil {
			return err
		}
		adopted, err = s.read()
		return err
	})
	if err != nil {
		return none, "", err
	}
	return s.adopt(adopted), adopted.Revision, nil
}

// LockGuard is the default write guard: it takes the operating-system lock on
// <path>.lock, waiting at most timeout (zero is DefaultLockTimeout) or until ctx
// ends; a lock not taken in time is ReasonBusy.
func LockGuard(ctx context.Context, timeout time.Duration) loader.WriteGuard {
	if timeout <= 0 {
		timeout = DefaultLockTimeout
	}
	return func(path string, write func() error) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		lctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		lock, err := fsx.Lock(lctx, path+".lock")
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return herr.Wrap(ReasonBusy, err, "layer "+path+" is locked by another writer")
			}
			return err
		}
		defer func() { _ = lock.Unlock() }()
		return write()
	}
}

// writeLayer renders the candidate's writable layer over the document last read
// and replaces the file atomically.
func writeLayer(prev, cand *Snapshot) error {
	var layer loader.Layer
	for _, l := range cand.Layers {
		if l.Writable {
			layer = l
		}
	}
	Register()
	codec, ok := loader.CodecFor(layer.Path).(loader.LayerCodec)
	if !ok {
		return fmt.Errorf("layer %s: its codec cannot write layers", layer.Path)
	}
	data, err := codec.EncodeLayer(prev.docs[layer.Label], layer.Ops)
	if err != nil {
		return fmt.Errorf("layer %s: %w", layer.Path, err)
	}
	if err := os.MkdirAll(filepath.Dir(layer.Path), 0o700); err != nil {
		return err
	}
	return fsx.WriteFile(layer.Path, data, fsx.Options{Perm: 0o600, Durable: true})
}

// reader is the configured file read.
func (s *Service) reader() ReadFile {
	if s.opts.Read != nil {
		return s.opts.Read
	}
	return os.ReadFile
}

// baselineReader serves the files of baseline, a nil entry as missing, and reads
// any other path with read.
func baselineReader(baseline map[string][]byte, read ReadFile) ReadFile {
	return func(path string) ([]byte, error) {
		data, ok := baseline[path]
		switch {
		case !ok:
			return read(path)
		case data == nil:
			return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		}
		return bytes.Clone(data), nil
	}
}

// read reads and checks the layers now.
func (s *Service) read() (*Snapshot, error) { return s.readWith(s.reader()) }

// readWith reads and checks the layers through readFile, recording every file read
// for the revision. When the layers cannot be read or parsed it returns the error
// with a snapshot holding only the revision of the bytes read, so the same broken
// content is recognised again.
func (s *Service) readWith(readFile ReadFile) (*Snapshot, error) {
	files := map[string][]byte{}
	var fmu sync.Mutex
	read := func(path string) ([]byte, error) {
		data, err := readFile(path)
		fmu.Lock()
		defer fmu.Unlock()
		switch {
		case err == nil:
			files[path] = data
		case errors.Is(err, fs.ErrNotExist):
			files[path] = nil
		}
		return data, err
	}
	layers, issues, err := s.opts.Stack(read)
	if err != nil {
		fmu.Lock()
		defer fmu.Unlock()
		return &Snapshot{Revision: digest(files), files: files}, err
	}
	Register()
	docs := map[string]*loader.LayerDocument{}
	for _, l := range layers {
		data := files[l.Path]
		if l.Path == "" || len(bytes.TrimSpace(data)) == 0 {
			continue
		}
		if doc, err := loader.ParseLayer(nil, l.Path, data); err == nil {
			docs[l.Label] = doc
		}
	}
	snap := &Snapshot{Revision: digest(files), Layers: layers, files: files, docs: docs}
	snap.Report = s.check(layers, docs)
	snap.Report.Issues = append(issues, snap.Report.Issues...)
	return snap, nil
}

// check is the static check (which resolves every literal config through its
// component's schema on a scratch fiber), then, for every running entry whose
// config changes, that fiber's own candidate config with the running tree's hooks
// (DH-D-CORDIS-5). Without a catalogue the loader's own name check stands in.
func (s *Service) check(layers []loader.Layer, docs map[string]*loader.LayerDocument) *Report {
	r := Check(layers, CheckOptions{Catalog: s.opts.Catalog, Evaluator: s.opts.Evaluator, Docs: docs, Required: s.opts.Required})
	svc := s.opts.Loader
	if svc == nil {
		return r
	}
	if s.opts.Catalog == nil {
		r.Issues = append(r.Issues, svc.Validate(r.Tree)...)
	}
	if len(r.Issues) > 0 {
		return r
	}
	r.walk(r.Tree, "", func(id string, e loader.EntryOptions) {
		if id == "" || e.Group {
			return
		}
		entry := svc.Find(id)
		if entry == nil {
			return
		}
		cur := svc.Options(entry)
		if cur.Name != e.Name || bytes.Equal(compact(cur.Config), compact(e.Config)) {
			return
		}
		fiber := svc.Fiber(entry)
		if fiber == nil {
			return
		}
		var candidate any
		if len(e.Config) > 0 {
			candidate = json.RawMessage(bytes.Clone(e.Config))
		}
		if _, err := fiber.ResolveConfig(candidate); err != nil {
			r.Issues = append(r.Issues, r.FromConfigError(id, e, err)...)
		}
	})
	return r
}

// readAll reads again every path of files with read, a missing file as nil.
func readAll(files map[string][]byte, read ReadFile) map[string][]byte {
	now := make(map[string][]byte, len(files))
	for p := range files {
		data, err := read(p)
		if err != nil {
			data = nil
		}
		now[p] = data
	}
	return now
}

// digest is the revision of a set of files: sha256 over each path and its bytes,
// in path order, a missing file distinct from an empty one.
func digest(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		data := files[p]
		if data == nil {
			fmt.Fprintf(h, "%s\x00-\x00", p)
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
