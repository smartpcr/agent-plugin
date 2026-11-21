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
	"sort"
	"strings"
	"sync"
	"time"

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
)

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
	// Catalog and Evaluator are the static check's (see CheckOptions).
	Catalog   Catalog
	Evaluator loader.Evaluator
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
// Reloads and edits run one at a time (op); the effective state has its own lock
// (mu), held only to read or swap it, and listeners are told after both are
// released, so a listener may call any method.
type Service struct {
	opts ServiceOptions

	op sync.Mutex

	mu       sync.Mutex
	current  *Snapshot
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
	s := &Service{opts: opts, listeners: map[int]func(error){}}
	read := s.reader()
	if opts.Baseline != nil {
		read = baselineReader(opts.Baseline, read)
	}
	snap, err := s.readWith(read)
	if err != nil {
		return nil, err
	}
	s.current = snap
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

// Current is the effective snapshot: the last one read that validated.
func (s *Service) Current() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Revision is the effective snapshot's revision.
func (s *Service) Revision() string { return s.Current().Revision }

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
	publish, err := s.reload()
	s.op.Unlock()
	publish()
	return err
}

func (s *Service) reload() (func(), error) {
	none := func() {}
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
	return none, s.adopt(snap)
}

// reject records revision as rejected and returns the notification to publish once
// the locks are released: none when that revision was rejected already.
func (s *Service) reject(revision string, err error) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision == s.rejected {
		return func() {}
	}
	s.rejected = revision
	return func() { s.notify(err) }
}

// adopt applies snap to the running tree, outside the state lock, then makes it
// effective. The caller holds op.
func (s *Service) adopt(snap *Snapshot) error {
	if s.opts.Loader != nil {
		if err := s.opts.Loader.ApplyLayers(snap.Layers); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.current, s.rejected = snap, ""
	s.mu.Unlock()
	return nil
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
		cur := s.Current()
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
	found := false
	for i := range layers {
		if layers[i].Writable {
			layers[i].Ops = append(append([]loader.Op{}, layers[i].Ops...), edit.Ops...)
			found = true
			break
		}
	}
	if !found {
		return nil, herr.New(ReasonNotWritable, "no writable layer")
	}
	next := &Snapshot{Revision: snap.Revision, Layers: layers, files: snap.files, docs: snap.docs}
	next.Report = s.check(layers, snap.docs)
	return next, nil
}

// Apply is the one edit path (DH-D-BOOT-7): under the layer lock it reads the
// layers, refuses a stale revision, checks the candidate, writes the writable layer
// (keeping its comments) and applies the new tree. A rejected edit writes nothing.
// It returns the new revision.
func (s *Service) Apply(ctx context.Context, edit Edit) (string, error) {
	s.op.Lock()
	defer s.op.Unlock()
	if err := s.stopping(); err != nil {
		return "", err
	}
	path := s.Current().Writable()
	if path == "" {
		return "", herr.New(ReasonNotWritable, "no writable layer")
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
		return "", err
	}
	if err := s.adopt(adopted); err != nil {
		return adopted.Revision, err
	}
	return adopted.Revision, nil
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
	r := Check(layers, CheckOptions{Catalog: s.opts.Catalog, Evaluator: s.opts.Evaluator, Docs: docs})
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
