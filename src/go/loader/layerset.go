package loader

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// ErrStaleLayer is matched (errors.Is) by a write refused because the layer file
// changed since the layer set last read it.
var ErrStaleLayer = errors.New("loader: layer file changed since it was read")

// StaleLayerError refuses a write over a layer file that another writer changed
// since the layer set last read it. The file is left as the other writer made it and
// the runtime edits stay in memory; Reload merges the two, and a later Flush writes
// the result.
type StaleLayerError struct {
	Label string
	Path  string
}

func (e *StaleLayerError) Error() string {
	return fmt.Sprintf("loader: layer %q (%s) changed since it was read; reload it before writing", e.Label, e.Path)
}

// Is makes errors.Is(err, ErrStaleLayer) true.
func (e *StaleLayerError) Is(target error) bool { return target == ErrStaleLayer }

// ErrNoWritableLayer is returned by Flush when the tree holds runtime edits and no
// layer is writable.
var ErrNoWritableLayer = errors.New("loader: no layer is writable; runtime edits cannot be saved")

// WriteGuard brackets every write of the writable layer: it is handed the whole
// check-and-replace of the file as write, and decides when to call it, for example
// under a cross-process lock. It returns write's result, or an error of its own
// without calling write, which leaves the file untouched.
type WriteGuard func(path string, write func() error) error

// LayerSetOptions configures LayerSet.
type LayerSetOptions struct {
	// Layers in composition order, lowest first (bundle, profile, user, project). A
	// layer with a Path is read from that file; one without is given by its Ops. At
	// most one layer is Writable, and it must have a Path.
	Layers []Layer `json:"layers"`
	// Watch reloads the tree when a layer file changes on disk.
	Watch bool `json:"watch"`
	// PollInterval is how often watched files are checked; 100ms when zero.
	PollInterval time.Duration `json:"pollInterval,omitempty"`
	// Codec reads and writes every layer file. Nil picks the codec registered for each
	// file's extension (CodecFor). The writable layer's codec must be a LayerCodec.
	Codec Codec `json:"-"`
	// WriteGuard, when set, brackets every write of the writable layer.
	WriteGuard WriteGuard `json:"-"`
}

// Validate requires unique, non-empty labels and at most one writable layer, backed
// by a file.
func (o LayerSetOptions) Validate() error {
	var issues []cordis.ConfigIssue
	labels := map[string]bool{}
	writable := 0
	for i, layer := range o.Layers {
		path := fmt.Sprintf("layers[%d]", i)
		switch {
		case layer.Label == "":
			issues = append(issues, cordis.ConfigIssue{Path: path + ".label", Message: "is required"})
		case labels[layer.Label]:
			issues = append(issues, cordis.ConfigIssue{Path: path + ".label", Message: fmt.Sprintf("duplicate label %q", layer.Label)})
		}
		labels[layer.Label] = true
		if layer.Writable {
			writable++
			if layer.Path == "" {
				issues = append(issues, cordis.ConfigIssue{Path: path + ".path", Message: "is required for the writable layer"})
			}
			if writable > 1 {
				issues = append(issues, cordis.ConfigIssue{Path: path + ".writable", Message: "only one layer may be writable"})
			}
		}
	}
	if len(issues) > 0 {
		return &cordis.ConfigError{Issues: issues}
	}
	return nil
}

// LayerSetComponent composes the loader's tree from ordered layers and keeps the
// two in sync. It injects the loader.
var LayerSetComponent = cordis.Construct("LayerSet", newLayerSet, cordis.WithInject(Key.Name()))

// LayerSet composes the loader's tree from an ordered list of layers (Compose) and
// keeps it in step with the layer files: a read composes again and applies, merged
// three ways with runtime edits made since the last read; a write saves runtime edits
// to the one writable layer and nowhere else.
//
// A write diffs the runtime tree against the composition of the other layers, turns
// the difference into operations and renders them over the writable file's last
// read document through its LayerCodec, so the operations it does not change keep
// their text and comments. The file is replaced through a temporary file and a
// rename, and only while its content hash still equals the one last read; otherwise
// the write fails with a *StaleLayerError and the other writer's content is kept.
// The other layer files are only ever read. Every read and write runs under one lock,
// in order.
type LayerSet struct {
	ctx      *cordis.Context
	options  LayerSetOptions
	loader   *Service
	layers   []*layerState
	writable *layerState

	// serial orders every read and write: the layer set's serial loop
	serial sync.Mutex
	// guarded by serial
	base  []EntryOptions
	lower []EntryOptions
	index *Index

	mu          sync.Mutex
	disposed    bool
	readQueued  bool
	writeQueued bool
	tasks       sync.WaitGroup
	tempCounter int
}

// layerState is one layer and what was last read of its file. Guarded by serial.
type layerState struct {
	Layer
	codec  Codec
	loaded bool
	exists bool
	data   []byte
	sum    [sha256.Size]byte
	doc    *LayerDocument
	// rejected is the hash of content that failed to parse, reported once
	rejected *[sha256.Size]byte
}

func newLayerSet(ctx *cordis.Context, options LayerSetOptions) (*LayerSet, error) {
	loader, err := Key.Get(ctx)
	if err != nil {
		return nil, err
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 100 * time.Millisecond
	}
	s := &LayerSet{ctx: ctx, options: options, loader: loader}
	for _, layer := range options.Layers {
		state := &layerState{Layer: layer}
		state.Ops = slices.Clone(layer.Ops)
		if layer.Path != "" {
			if state.Path, err = filepath.Abs(layer.Path); err != nil {
				return nil, err
			}
			state.codec = options.Codec
			if state.codec == nil {
				state.codec = CodecFor(state.Path)
			}
			state.Ops = nil
		}
		if layer.Writable {
			if _, ok := state.codec.(LayerCodec); !ok {
				return nil, fmt.Errorf("loader: the codec of writable layer %q cannot write layers", layer.Label)
			}
			s.writable = state
		}
		s.layers = append(s.layers, state)
	}
	return s, nil
}

// Layers returns the layers as last read, in composition order.
func (s *LayerSet) Layers() []Layer {
	s.serial.Lock()
	defer s.serial.Unlock()
	return s.snapshotLayers(false)
}

// Index returns the ownership index of the composition last read.
func (s *LayerSet) Index() *Index {
	s.serial.Lock()
	defer s.serial.Unlock()
	return s.index
}

// Init reads every layer, composes them and applies the tree, then starts watching.
func (s *LayerSet) Init() error {
	if _, err := s.ctx.Effect("loader/layers-changed", func() (cordis.Disposer, error) {
		remove := s.loader.OnChange(s.queueWrite)
		return func() error { remove(); return nil }, nil
	}); err != nil {
		return err
	}
	s.serial.Lock()
	s.readFiles()
	tree, index, issues := Compose(s.snapshotLayers(false))
	s.report(issues)
	assignStableIDs(tree, "", nil)
	s.lower = s.composeLower(tree)
	err := s.loader.Apply(tree)
	if err == nil {
		s.base, s.index = cloneAll(tree), index
	}
	s.serial.Unlock()
	if err != nil {
		return err
	}
	if s.options.Watch {
		return s.watch()
	}
	return nil
}

// Reload reads the layer files now and, if one changed, composes again and applies
// the tree merged with the runtime edits made since the last read (a file edit wins a
// conflict). Runtime edits that survive are written back. It returns the write's
// error, if any.
func (s *LayerSet) Reload() error {
	s.serial.Lock()
	defer s.serial.Unlock()
	if !s.readLocked() {
		return nil
	}
	return s.writeLocked()
}

// Flush writes the runtime edits to the writable layer now. It returns nil when
// there is nothing to write, and otherwise the error of the write until a later one
// succeeds: the edits stay in memory, so whether the layer holds them is never left
// unknown.
func (s *LayerSet) Flush() error {
	s.serial.Lock()
	defer s.serial.Unlock()
	return s.writeLocked()
}

// Close flushes pending runtime edits. It runs when the fiber unloads.
func (s *LayerSet) Close() error {
	s.mu.Lock()
	s.disposed = true
	s.mu.Unlock()
	s.tasks.Wait()
	if err := s.Flush(); err != nil {
		s.ctx.Logger().Warnf("%v; the runtime edits were not saved", err)
	}
	return nil
}

// ------------------------------------------------------------------ queue

func (s *LayerSet) queueWrite() { s.queue(&s.writeQueued, s.Flush) }

func (s *LayerSet) queueRead() { s.queue(&s.readQueued, s.Reload) }

// queue runs step on its own goroutine, coalescing requests made before it starts.
func (s *LayerSet) queue(queued *bool, step func() error) {
	s.mu.Lock()
	if s.disposed || *queued {
		s.mu.Unlock()
		return
	}
	*queued = true
	s.tasks.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.tasks.Done()
		s.mu.Lock()
		*queued = false
		s.mu.Unlock()
		if err := step(); err != nil {
			s.ctx.Logger().Warnf("%v; the runtime edits are kept in memory", err)
		}
	}()
}

// ------------------------------------------------------------------ reads

// readFiles reads every layer file whose content changed since it was last read and
// reports whether one did. A file that cannot be read or parsed keeps the operations
// last read from it; a missing file is an empty layer.
func (s *LayerSet) readFiles() bool {
	changed := false
	for _, layer := range s.layers {
		if layer.Path == "" {
			continue
		}
		data, err := os.ReadFile(layer.Path)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.ctx.Logger().Warnf("cannot read layer %q (%s): %v; keeping its last operations", layer.Label, layer.Path, err)
			continue
		}
		sum := sha256.Sum256(data)
		if layer.loaded && layer.exists == exists && layer.sum == sum {
			continue
		}
		if !exists {
			if !layer.Writable {
				s.ctx.Logger().Warnf("layer %q (%s) is missing; it is composed as empty", layer.Label, layer.Path)
			}
			layer.loaded, layer.exists, layer.data, layer.sum, layer.doc, layer.Ops = true, false, nil, sum, nil, nil
			changed = true
			continue
		}
		doc, err := ParseLayer(layer.codec, layer.Path, data)
		if err != nil {
			if layer.rejected == nil || *layer.rejected != sum {
				layer.rejected = &sum
				s.ctx.Logger().Warnf("layer %q (%s) is not an array of layer operations (%v); keeping its last operations", layer.Label, layer.Path, err)
			}
			continue
		}
		layer.loaded, layer.exists, layer.data, layer.sum, layer.doc, layer.Ops = true, true, data, sum, doc, doc.Ops
		layer.rejected = nil
		changed = true
	}
	return changed
}

// readLocked reads the files and, if one changed, applies the new composition merged
// with the runtime edits. It reports whether runtime edits survived the merge and so
// need writing.
func (s *LayerSet) readLocked() bool {
	if !s.readFiles() {
		return false
	}
	theirs, index, issues := Compose(s.snapshotLayers(false))
	s.report(issues)
	assignStableIDs(theirs, "", s.base)
	merged, conflicts := merge(s.base, s.loader.Snapshot(), theirs)
	for _, id := range conflicts {
		s.ctx.Logger().Errorf("entry %q was changed both in a layer file and at runtime; the file wins", id)
	}
	if err := s.loader.Apply(merged); err != nil {
		s.ctx.Logger().Error(err)
		return false
	}
	s.base, s.index = cloneAll(theirs), index
	s.lower = s.composeLower(theirs)
	return !sameTree(merged, theirs)
}

// composeLower composes every layer but the writable one, with the ids of anonymous
// entries taken from tree, the full composition.
func (s *LayerSet) composeLower(tree []EntryOptions) []EntryOptions {
	lower, _, _ := Compose(s.snapshotLayers(true))
	assignStableIDs(lower, "", tree)
	return lower
}

func (s *LayerSet) snapshotLayers(withoutWritable bool) []Layer {
	layers := make([]Layer, 0, len(s.layers))
	for _, layer := range s.layers {
		if withoutWritable && layer.Writable {
			continue
		}
		layers = append(layers, layer.Layer)
	}
	return layers
}

func (s *LayerSet) report(issues []Issue) {
	for _, issue := range issues {
		s.ctx.Logger().Warnf("%s", issue.Error())
	}
}

func sameTree(a, b []EntryOptions) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

// ----------------------------------------------------------------- writes

func (s *LayerSet) writeLocked() error {
	runtime := s.loader.Snapshot()
	layer := s.writable
	if layer == nil {
		if s.base != nil && !sameTree(runtime, s.base) {
			return ErrNoWritableLayer
		}
		return nil
	}
	ops := diffOps(s.lower, runtime, layer.Ops)
	text, err := layer.codec.(LayerCodec).EncodeLayer(layer.doc, ops)
	if err != nil {
		return fmt.Errorf("loader: cannot encode layer %q (%s): %w", layer.Label, layer.Path, err)
	}
	if layer.exists && bytes.Equal(text, layer.data) {
		s.base = runtime
		return nil
	}
	if !layer.exists && len(ops) == 0 {
		return nil // nothing to save: a missing writable layer stays missing
	}

	wrote := false
	write := func() error {
		current, err := os.ReadFile(layer.Path)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if exists != layer.exists || sha256.Sum256(current) != layer.sum {
			return &StaleLayerError{Label: layer.Label, Path: layer.Path}
		}
		if exists {
			if info, err := os.Stat(layer.Path); err == nil && info.Mode().Perm()&0o200 == 0 {
				return fmt.Errorf("loader: layer %q (%s) is read-only", layer.Label, layer.Path)
			}
		}
		if err := s.replace(layer.Path, text); err != nil {
			return err
		}
		wrote = true
		return nil
	}
	err = s.guarded(layer.Path, write, &wrote)
	if wrote {
		s.adopt(layer, text)
		s.base = runtime
	}
	return err
}

// guarded runs write under the write guard, if there is one. A guard that panics, or
// that returns nil without writing, is a failed write.
func (s *LayerSet) guarded(path string, write func() error, wrote *bool) (err error) {
	guard := s.options.WriteGuard
	if guard == nil {
		return write()
	}
	called := false
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("loader: the write guard for %s panicked: %v", path, r)
		}
	}()
	err = guard(path, func() error {
		called = true
		return write()
	})
	if err == nil && !called {
		err = fmt.Errorf("loader: the write guard for %s returned without writing", path)
	}
	if err == nil && !*wrote {
		err = fmt.Errorf("loader: the write guard for %s did not return the write's error", path)
	}
	return err
}

// adopt makes text, just written, what the layer was last read as. It is parsed
// again, so the next write renders over the document it wrote.
func (s *LayerSet) adopt(layer *layerState, text []byte) {
	layer.loaded, layer.exists, layer.data, layer.sum = true, true, text, sha256.Sum256(text)
	if doc, err := ParseLayer(layer.codec, layer.Path, text); err == nil {
		layer.doc, layer.Ops = doc, doc.Ops
	} else {
		s.ctx.Logger().Errorf("layer %q (%s) cannot be read back after writing it: %v", layer.Label, layer.Path, err)
	}
}

// replace writes text through a temporary file renamed over path, retried while the
// file is locked (on Windows, by a reader).
func (s *LayerSet) replace(path string, text []byte) error {
	s.mu.Lock()
	s.tempCounter++
	counter := s.tempCounter
	s.mu.Unlock()
	temp := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(path), os.Getpid(), counter))
	defer os.Remove(temp)
	if err := os.WriteFile(temp, text, 0o644); err != nil {
		return fmt.Errorf("loader: cannot write layer file %s: %w", path, err)
	}
	var err error
	for attempt := 1; attempt <= writeAttempts; attempt++ {
		if err = os.Rename(temp, path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(20*attempt) * time.Millisecond)
	}
	return fmt.Errorf("loader: cannot replace layer file %s: %w", path, err)
}

// diffOps returns the operations that turn lower, the composition of the layers
// below the writable one, into runtime. previous are the writable layer's current
// operations: an operation keeps its place among them, an insert keeps its entries
// in the operation that inserted them, a config written by an override stays an
// override, and an operation that names an entry found nowhere (or that could not
// be decoded) is kept as it is.
func diffOps(lower, runtime []EntryOptions, previous []Op) []Op {
	l, lowerOrder := flattenTree(lower)
	r, runtimeOrder := flattenTree(runtime)

	// pinned are the fields the writable layer already overrides: they stay in its
	// override, with the runtime value, even when that now equals the lower value
	pinned := map[string]map[string]bool{}
	insertedBy := map[string]int{}
	lastInsert := map[string]int{}
	for j, op := range previous {
		if !op.Valid() {
			continue
		}
		switch op.Kind {
		case OpOverride:
			if pinned[op.ID] == nil {
				pinned[op.ID] = map[string]bool{}
			}
			for key := range op.Set {
				pinned[op.ID][key] = true
			}
		case OpInsert:
			lastInsert[op.ID] = j
			for _, entry := range op.Entries {
				if entry.ID != "" {
					insertedBy[compose(op.ID, entry.ID)] = j
				}
			}
		}
	}

	replaced := func(id string) bool {
		lo, inLower := l[id]
		ro, inRuntime := r[id]
		return inLower && inRuntime && (lo.options.Name != ro.options.Name || lo.options.Group != ro.options.Group)
	}
	gone := func(id string) bool { _, ok := r[id]; return !ok || replaced(id) }
	fresh := func(id string) bool { _, ok := l[id]; return !ok || replaced(id) }

	type generated struct {
		op   Op
		slot int // index in previous it takes the place of, or -1
	}
	var ops []generated

	// removals, top-most only
	for _, id := range lowerOrder {
		if !gone(id) {
			continue
		}
		if parent := l[id].parentID; parent != "" && gone(parent) {
			continue
		}
		ops = append(ops, generated{op: Op{Kind: OpRemove, ID: id}, slot: -1})
	}

	// inserts, top-most only, grouped by the operation that inserted them before
	type group struct {
		parent string
		slot   int
	}
	var groups []group
	inserted := map[group][]EntryOptions{}
	for _, id := range runtimeOrder {
		if !fresh(id) {
			continue
		}
		parent := r[id].parentID
		if parent != "" && fresh(parent) {
			continue
		}
		slot, ok := insertedBy[id]
		if !ok {
			if slot, ok = lastInsert[parent]; !ok {
				slot = -1
			}
		}
		key := group{parent: parent, slot: slot}
		if _, seen := inserted[key]; !seen {
			groups = append(groups, key)
		}
		inserted[key] = append(inserted[key], r[id].options.Clone())
	}
	for _, key := range groups {
		ops = append(ops, generated{op: Op{Kind: OpInsert, ID: key.parent, Entries: inserted[key]}, slot: key.slot})
	}

	// changed fields of entries in both
	for _, id := range runtimeOrder {
		lo, ok := l[id]
		if !ok || replaced(id) {
			continue
		}
		from, to := lo.options, r[id].options
		set := map[string]json.RawMessage{}
		if pinned[id]["disabled"] || from.Disabled != to.Disabled || from.DisabledExpr != to.DisabledExpr {
			switch {
			case to.DisabledExpr != "":
				set["disabled"], _ = json.Marshal(map[string]string{exprKey: to.DisabledExpr})
			default:
				set["disabled"], _ = json.Marshal(to.Disabled)
			}
		}
		if pinned[id]["inject"] || !slices.Equal(from.Inject, to.Inject) {
			inject := to.Inject
			if inject == nil {
				inject = []string{}
			}
			set["inject"], _ = json.Marshal(inject)
		}
		if pinned[id]["isolate"] || !maps.Equal(from.Isolate, to.Isolate) {
			isolate := map[string]any{}
			for name, realm := range to.Isolate {
				if realm == "true" {
					isolate[name] = true
				} else {
					isolate[name] = realm
				}
			}
			set["isolate"], _ = json.Marshal(isolate)
		}
		if pinned[id]["intercept"] || !maps.EqualFunc(from.Intercept, to.Intercept, func(a, b json.RawMessage) bool { return jsonEqual(a, b) }) {
			intercept := to.Intercept
			if intercept == nil {
				intercept = map[string]json.RawMessage{}
			}
			set["intercept"], _ = json.Marshal(intercept)
		}
		if pinned[id]["config"] || !jsonEqual(from.Config, to.Config) {
			patch, ok := mergePatchBetween(from.Config, to.Config)
			if pinned[id]["config"] || !ok {
				config := to.Config
				if len(config) == 0 {
					config = json.RawMessage("null")
				}
				set["config"] = config
			} else if string(patch) != "{}" {
				ops = append(ops, generated{op: Op{Kind: OpMerge, ID: id, Patch: patch}, slot: -1})
			}
		}
		if len(set) > 0 {
			ops = append(ops, generated{op: Op{Kind: OpOverride, ID: id, Set: set}, slot: -1})
		}
	}

	// place each operation where the one it rewrites stands
	slots := make([]*Op, len(previous))
	for j, op := range previous {
		if !op.Valid() || (op.Kind != OpInsert && !inEither(l, r, op.ID)) || (op.Kind == OpInsert && op.ID != "" && !inEither(l, r, op.ID)) {
			kept := op
			slots[j] = &kept
		}
	}
	var extra []Op
	for _, g := range ops {
		slot := g.slot
		if slot < 0 && g.op.Kind != OpInsert {
			slot = slices.IndexFunc(previous, func(op Op) bool { return op.Valid() && op.key() == g.op.key() })
		}
		if slot >= 0 && slots[slot] == nil {
			op := g.op
			slots[slot] = &op
			continue
		}
		extra = append(extra, g.op)
	}
	result := []Op{}
	for _, op := range slots {
		if op != nil {
			result = append(result, *op)
		}
	}
	return append(result, extra...)
}

func inEither(l, r map[string]node, id string) bool {
	_, inLower := l[id]
	_, inRuntime := r[id]
	return inLower || inRuntime
}

// mergePatchBetween returns the JSON merge patch that turns the config from into to,
// or false when a merge patch cannot express it (either is not an object, or to holds
// a null, which a merge patch reads as a deletion).
func mergePatchBetween(from, to json.RawMessage) (json.RawMessage, bool) {
	if len(from) == 0 || len(to) == 0 {
		return nil, false
	}
	a, errA := JSON.Decode("", from)
	b, errB := JSON.Decode("", to)
	if errA != nil || errB != nil || a.Kind != ObjectNode || b.Kind != ObjectNode || holdsNull(b) {
		return nil, false
	}
	var out bytes.Buffer
	writePatch(&out, a, b)
	return out.Bytes(), true
}

func writePatch(b *bytes.Buffer, from, to *Node) {
	before := map[string]*Node{}
	for _, field := range from.Fields {
		before[field.Key] = field.Value
	}
	after := map[string]bool{}
	b.WriteByte('{')
	first := true
	member := func(key string) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		encoded, _ := json.Marshal(key)
		b.Write(encoded)
		b.WriteByte(':')
	}
	for _, field := range to.Fields {
		after[field.Key] = true
		old, ok := before[field.Key]
		switch {
		case ok && sameJSON(old, field.Value):
		case ok && old.Kind == ObjectNode && field.Value.Kind == ObjectNode:
			member(field.Key)
			writePatch(b, old, field.Value)
		default:
			member(field.Key)
			b.Write(field.Value.JSON())
		}
	}
	for _, field := range from.Fields {
		if !after[field.Key] {
			after[field.Key] = true
			member(field.Key)
			b.WriteString("null")
		}
	}
	b.WriteByte('}')
}

func holdsNull(node *Node) bool {
	switch node.Kind {
	case NullNode:
		return true
	case ObjectNode:
		return slices.ContainsFunc(node.Fields, func(field *Field) bool { return holdsNull(field.Value) })
	case ArrayNode:
		return slices.ContainsFunc(node.Items, holdsNull)
	}
	return false
}

// --------------------------------------------------------------- watching

// watch polls the layer files and queues a reload whenever one changes.
func (s *LayerSet) watch() error {
	_, err := s.ctx.Effect("loader/watch-layers", func() (cordis.Disposer, error) {
		stop := make(chan struct{})
		done := make(chan struct{})
		last := map[string][]byte{}
		for _, layer := range s.layers {
			if layer.Path != "" {
				last[layer.Path], _ = os.ReadFile(layer.Path)
			}
		}
		go func() {
			defer close(done)
			ticker := time.NewTicker(s.options.PollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					changed := false
					for path, previous := range last {
						current, err := os.ReadFile(path)
						if err != nil && !errors.Is(err, fs.ErrNotExist) {
							continue // e.g. locked mid-rename on Windows; look again next tick
						}
						if !bytes.Equal(current, previous) {
							last[path] = current
							changed = true
						}
					}
					if changed {
						s.queueRead()
					}
				}
			}
		}()
		return func() error {
			close(stop)
			<-done
			return nil
		}, nil
	})
	return err
}
