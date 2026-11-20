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
}

// Service is the validated reload and the one edit path of a launch's layers
// (DH-D-BOOT-7, DH-ADR-BOOT-8). A candidate, whether a file changed under the
// running tree or an edit, becomes effective only once its whole tree validates:
// the static check (Check) and, for every running entry whose config changes,
// the component's own schema through cordis candidate config (DH-D-CORDIS-5). A
// rejected candidate changes nothing: no file, no fiber.
type Service struct {
	opts ServiceOptions

	mu       sync.Mutex
	current  *Snapshot
	rejected string

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
	snap, err := s.read()
	if err != nil {
		return nil, err
	}
	s.current = snap
	return s, nil
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
func (s *Service) Draft() (*Snapshot, error) { return s.read() }

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
// applies them to the running tree. A candidate that does not validate is
// rejected whole with its positioned issues: the running tree is untouched, and
// the listeners are told once for that revision.
func (s *Service) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := s.read()
	if err != nil {
		return s.rejectLocked("", herr.Wrap(ReasonRejected, err, "reload rejected"))
	}
	if snap.Revision == s.current.Revision {
		return nil
	}
	if err := snap.Report.Err(); err != nil {
		return s.rejectLocked(snap.Revision, herr.Wrap(ReasonRejected, err, "reload rejected"))
	}
	return s.adoptLocked(snap)
}

func (s *Service) rejectLocked(revision string, err error) error {
	if revision == "" || revision != s.rejected {
		s.rejected = revision
		s.notify(err)
	}
	return err
}

func (s *Service) adoptLocked(snap *Snapshot) error {
	if s.opts.Loader != nil {
		if err := s.opts.Loader.ApplyLayers(snap.Layers); err != nil {
			return err
		}
	}
	s.current, s.rejected = snap, ""
	return nil
}

// Watch reloads whenever a layer file read changes, polling every interval, until
// ctx ends. A rejected change is reported through OnRejected.
func (s *Service) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := s.Current()
		if digest(readAll(cur.files)) != cur.Revision {
			_ = s.Reload()
		}
	}
}

// Edit is a change to the writable layer: operations appended to it, made against
// the revision the editor read.
type Edit struct {
	Revision string
	Ops      []loader.Op
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
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.current.Writable()
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
	if err := s.adoptLocked(adopted); err != nil {
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

// read reads and checks the layers, recording every file read for the revision.
func (s *Service) read() (*Snapshot, error) {
	files := map[string][]byte{}
	var fmu sync.Mutex
	read := func(path string) ([]byte, error) {
		data, err := os.ReadFile(path)
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
		return nil, err
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

// check is the static check, then the running components' own schemas for every
// entry whose config changes (DH-D-CORDIS-5).
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

// readAll reads again every path of files, a missing file as nil.
func readAll(files map[string][]byte) map[string][]byte {
	now := make(map[string][]byte, len(files))
	for p := range files {
		data, err := os.ReadFile(p)
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
