// Package loader provides the "loader" service, which materialises a declarative tree
// of entries into fibers and reconciles it on every change, and ConfigFile, which
// keeps that tree in sync with a JSON file (upstream's @cordisjs/loader and
// @cordisjs/include).
package loader

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Key is the typed key of the loader service.
var Key = cordis.NewServiceKey[*Service]("loader")

// Options configures the loader.
type Options struct {
	// Resolver maps entry names to components. Nil uses Registered.
	Resolver Resolver `json:"-"`
	// Evaluator computes the expressions in entries. Nil rejects every entry that
	// holds an expression; src/go ships none. Options is deep-copied like any
	// config, so an evaluator keeps shared state behind unexported pointer fields.
	Evaluator Evaluator `json:"-"`
}

// Component provides the loader service.
var Component = cordis.Construct("LoaderService", newService)

// Service reconciles the live tree with a desired list of entries. Every entry
// becomes a fiber under the loader's own fiber (a group's children under the group),
// so unloading the loader unloads the whole tree.
//
// Locking: mu guards the entry records and is never held across a call that can run
// cordis notifications; ensureMu serialises the check-and-create of each entry; apply
// serialises whole reconciliations.
type Service struct {
	ctx       *cordis.Context
	resolver  Resolver
	evaluator Evaluator
	changed   listenerList
	observers eventList[LifecycleEvent]
	// nested is set on a loader created by New: it has no writable layer, so its
	// mutation methods return ErrReadOnly
	nested bool

	apply    sync.Mutex
	ensureMu sync.Mutex
	// useGate is held while an entry's fiber is created and registered, so the config
	// hook of its first load finds it in byFiber
	useGate sync.RWMutex

	mu      sync.Mutex
	entries map[string]*Entry
	byFiber map[*cordis.Fiber]*Entry
	realms  map[string]*cordis.IsolateKey
	// live fibers holding each shared realm key; a key is dropped at zero
	realmUsers map[string]int
	current    []EntryOptions
	// entries committed at runtime while an Apply runs; their config wins over it
	committed map[string]bool
	// closed once the fiber that owns the loader has unloaded
	closed bool
	// the most recent lifecycle events, oldest first; seq numbers them
	lifecycle []LifecycleEvent
	seq       int
}

// lifecycleCap bounds the lifecycle log; older events are dropped.
const lifecycleCap = 512

var (
	// ErrReadOnly is returned by the mutation methods of a nested loader (New): it has
	// no writable layer, so it is changed by handing it a new layer list.
	ErrReadOnly = errors.New("loader: a nested loader is read-only; hand it a new layer list instead")
	// ErrNotFound is returned for a tree id that names no entry.
	ErrNotFound = errors.New("loader: no such entry")
	// ErrClosed is returned once the fiber that owns the loader has unloaded.
	ErrClosed = errors.New("loader: the loader has unloaded")
)

func newService(ctx *cordis.Context, options Options) (*Service, error) {
	return newServiceIn(ctx, options, false)
}

// New runs a loader of the calling component's own (a nested loader), for example an
// agent preset's tree. It is provided as an isolated "loader" service under ctx, so
// only ctx and what loads below it see it; its entries are children of ctx's fiber and
// unload with it, leaves first. It resolves names like the root loader (the
// package-level Register table unless options.Resolver is set) and evaluates the
// expressions of its own entries with options.Evaluator.
//
// A nested loader is given its layers as values: none may be writable or name a
// file, because it has no layer source, watches nothing and never writes back. Its
// mutation methods return ErrReadOnly; it is changed by ApplyLayers with a new layer
// list, which restarts only the entries that changed.
func New(ctx *cordis.Context, options Options, layers ...Layer) (*Service, error) {
	if err := checkNestedLayers(layers); err != nil {
		return nil, err
	}
	s, err := newServiceIn(ctx.Isolate(Key.Name()), options, true)
	if err != nil {
		return nil, err
	}
	if len(layers) > 0 {
		if err := s.ApplyLayers(layers); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func checkNestedLayers(layers []Layer) error {
	for _, layer := range layers {
		if layer.Writable {
			return fmt.Errorf("loader: layer %q of a nested loader cannot be writable", layer.Label)
		}
		if layer.Path != "" {
			return fmt.Errorf("loader: layer %q of a nested loader names a file; give its operations as values", layer.Label)
		}
	}
	return nil
}

func newServiceIn(ctx *cordis.Context, options Options, nested bool) (*Service, error) {
	resolver := options.Resolver
	if resolver == nil {
		resolver = Registered
	}
	s := &Service{
		ctx:        ctx,
		resolver:   resolver,
		evaluator:  options.Evaluator,
		nested:     nested,
		entries:    map[string]*Entry{},
		byFiber:    map[*cordis.Fiber]*Entry{},
		realms:     map[string]*cordis.IsolateKey{},
		realmUsers: map[string]int{},
	}
	// registered first, so it runs last: after every entry fiber (an effect of the
	// owner's fiber) has unloaded
	if _, err := ctx.Effect("loader/close", func() (cordis.Disposer, error) {
		return func() error {
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
			return nil
		}, nil
	}); err != nil {
		return nil, err
	}
	if _, err := Key.Provide(ctx, s); err != nil {
		return nil, err
	}
	if _, err := ctx.Effect("loader/commit", func() (cordis.Disposer, error) {
		remove := ctx.Internal().OnCommit(s.onCommit)
		return func() error { remove(); return nil }, nil
	}); err != nil {
		return nil, err
	}
	if _, err := ctx.OnConfig(s.configHook); err != nil {
		return nil, err
	}
	return s, nil
}

// OnChange subscribes to runtime changes of the tree that should be persisted (an
// Update of an entry's fiber, SetDisabled). It returns a function that removes the
// subscription.
func (s *Service) OnChange(fn func()) (remove func()) { return s.changed.add(fn) }

// Entries returns the entries in tree order.
func (s *Service) Entries() []*Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := make([]*Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].order < entries[j].order })
	return entries
}

// Find returns the entry with the tree id, or nil.
func (s *Service) Find(id string) *Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[id]
}

// Options returns a copy of the entry's current options (without children).
func (s *Service) Options(entry *Entry) EntryOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return entry.options.Clone()
}

// Fiber returns the entry's fiber, or nil while it is not loaded (disabled, or held
// back by its group).
func (s *Service) Fiber(entry *Entry) *cordis.Fiber {
	s.mu.Lock()
	defer s.mu.Unlock()
	return entry.fiber
}

// Active reports whether the entry's fiber is Active.
func (s *Service) Active(entry *Entry) bool {
	fiber := s.Fiber(entry)
	return fiber != nil && fiber.State() == cordis.Active
}

// Snapshot returns a copy of the desired tree, including runtime edits.
func (s *Service) Snapshot() []EntryOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := cloneAll(s.current)
	if snapshot == nil {
		snapshot = []EntryOptions{}
	}
	return snapshot
}

// Apply reconciles the live tree with desired: entries that disappeared are disposed,
// entries whose shape changed are rebuilt, entries whose config changed are updated,
// and new entries are created, parents first. An invalid, unresolvable or failing
// entry is logged on its own; its siblings still load. Apply returns once every fiber
// has settled, after recording the lifecycle events of the changed entries and
// emitting one Reloaded notice.
func (s *Service) Apply(desired []EntryOptions) error {
	s.apply.Lock()
	return s.reconcileLocked(cloneAll(desired), nil, nil)
}

// ApplyLayers composes layers (Compose) and applies the tree. An anonymous entry
// keeps the id it had in the running tree, so only the entries that changed restart.
// It is how a nested loader (New) is changed; a loader driven by a layer source
// should be changed through that source instead.
func (s *Service) ApplyLayers(layers []Layer) error {
	if s.nested {
		if err := checkNestedLayers(layers); err != nil {
			return err
		}
	}
	tree, _, issues := Compose(layers)
	for _, issue := range issues {
		s.ctx.Logger().Warnf("%s", issue.Error())
	}
	s.apply.Lock()
	assignStableIDs(tree, "", s.Snapshot())
	return s.reconcileLocked(tree, nil, issues)
}

// applyDeferred is Apply for a layer source. issues are the source's own faults
// (composition issues), reported in the notice. Instead of publishing the lifecycle
// events and the Reloaded notice, it returns the publication, so the source can
// commit its state and release its lock first: a listener may then call back into
// the source without deadlocking or seeing the state from before the reload.
func (s *Service) applyDeferred(desired []EntryOptions, issues []Issue) (publish func(), err error) {
	s.apply.Lock()
	return s.reconcileHeld(cloneAll(desired), nil, issues)
}

// reconcileLocked is reconcileHeld followed by its publication.
func (s *Service) reconcileLocked(tree []EntryOptions, extra []LifecycleEvent, issues []Issue) error {
	publish, err := s.reconcileHeld(tree, extra, issues)
	if err != nil {
		return err
	}
	publish()
	return nil
}

// reconcileHeld is Apply over a tree the caller owns, with s.apply held; it
// releases s.apply before waiting for the fibers to settle. extra are lifecycle
// events of the edit that the diff cannot see (a move among siblings), and issues
// are faults found before the tree was built; both are in the returned publication,
// which records the events and emits the one Reloaded notice.
func (s *Service) reconcileHeld(tree []EntryOptions, extra []LifecycleEvent, issues []Issue) (func(), error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		s.apply.Unlock()
		return nil, ErrClosed
	}
	issues = slices.Clone(issues)
	flattened := map[string]flatEntry{}
	var sequence []string
	s.flatten(tree, "", flattened, &sequence, &issues)

	// Publish the desired tree first, so a runtime commit made while the tree is being
	// reconciled is recorded in the tree that will be persisted.
	s.mu.Lock()
	s.current = tree
	s.committed = map[string]bool{}
	var removed []*Entry
	for id, entry := range s.entries {
		if _, ok := flattened[id]; !ok {
			removed = append(removed, entry)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].order < removed[j].order })
	var events []LifecycleEvent
	var removedIDs []string
	for _, entry := range removed {
		before := entry.options.Clone()
		events = append(events, LifecycleEvent{Kind: EntryRemoved, ID: entry.id, Before: &before})
		removedIDs = append(removedIDs, entry.id)
	}
	s.mu.Unlock()
	for _, entry := range removed {
		s.remove(entry.id)
	}

	// 1. records: rebuild entries whose shape changed, so step 2 recreates them
	var created, changed []string
	rebuilt := map[string]bool{}
	for position, id := range sequence {
		flat := flattened[id]
		s.mu.Lock()
		entry := s.entries[id]
		added := entry == nil
		if added {
			entry = &Entry{id: id, parentID: flat.parentID}
			s.entries[id] = entry
		}
		before := entry.options.Clone()
		rebuild := entry.fiber != nil && !entry.options.sameShape(flat.options)
		s.mu.Unlock()
		if rebuild {
			s.disposeFiber(entry)
			rebuilt[id] = true
		}
		s.mu.Lock()
		options := flat.options.withoutChildren()
		if s.committed[id] {
			options.Config = entry.options.Config
		}
		entry.options = options
		entry.order = position
		after := options.Clone()
		s.mu.Unlock()
		switch {
		case added:
			events = append(events, LifecycleEvent{Kind: EntryAdded, ID: id, After: &after})
			created = append(created, id)
		case !sameOptions(before, after):
			events = append(events, LifecycleEvent{Kind: EntryUpdated, ID: id, Before: &before, After: &after})
			changed = append(changed, id)
		}
	}

	// 2. materialise, parents first; children of unloaded groups wait for their group
	var updates sync.WaitGroup
	var rejectedMu sync.Mutex
	rejected := map[string]error{}
	for _, id := range sequence {
		entry := s.Find(id)
		if entry == nil {
			continue
		}
		update, err := s.ensure(entry)
		if err != nil {
			s.ctx.Logger().Error(err)
			issues = append(issues, issuesOf(id, err)...)
		}
		if update != nil {
			updates.Add(1)
			go func() {
				defer updates.Done()
				if err := update(); err != nil {
					rejectedMu.Lock()
					rejected[id] = err
					rejectedMu.Unlock()
				}
			}()
		}
	}
	updates.Wait()

	s.mu.Lock()
	s.committed = nil
	s.mu.Unlock()
	s.apply.Unlock()

	s.WaitAll()

	notice := ReloadNotice{Created: created, Removed: removedIDs, Issues: issues}
	for _, id := range sequence {
		// a refused update (e.g. by the schema) leaves the old config running; it is
		// reported whether this edit changed the entry or retried a refused config
		if err, ok := rejected[id]; ok {
			notice.Issues = append(notice.Issues, issuesOf(id, err)...)
		}
	}
	for _, id := range changed {
		if _, ok := rejected[id]; ok {
			continue
		}
		if entry := s.Find(id); rebuilt[id] && entry != nil && s.Fiber(entry) != nil {
			notice.Restarted = append(notice.Restarted, id)
		} else {
			notice.Updated = append(notice.Updated, id)
		}
	}
	for _, id := range append(slices.Clone(created), changed...) {
		if _, ok := rejected[id]; ok {
			continue
		}
		if entry := s.Find(id); entry != nil {
			if fiber := s.Fiber(entry); fiber != nil {
				if err := fiber.Wait(); err != nil {
					notice.Issues = append(notice.Issues, Issue{Path: id, Message: err.Error()})
				}
			}
		}
	}
	events = append(events, extra...)
	return func() { s.publish(events, notice) }, nil
}

// sameOptions reports whether two entry records hold the same options (children
// aside).
func sameOptions(a, b EntryOptions) bool {
	return a.ID == b.ID && a.sameShape(b) && jsonEqual(a.Config, b.Config)
}

// issuesOf returns the issues of an entry's failure.
func issuesOf(id string, err error) []Issue {
	var entryErr *EntryError
	if errors.As(err, &entryErr) {
		return slices.Clone(entryErr.Issues)
	}
	return []Issue{{Path: id, Message: err.Error()}}
}

// WaitAll waits for every entry's fiber to settle.
func (s *Service) WaitAll() {
	for _, entry := range s.Entries() {
		if fiber := s.Fiber(entry); fiber != nil {
			_ = fiber.Wait() // a failed load is reported by the fiber
		}
	}
}

// SetDisabled disables or re-enables an entry, and reports the change for
// persistence. A nested loader returns ErrReadOnly.
func (s *Service) SetDisabled(id string, disabled bool) error {
	return s.edit(func(tree []EntryOptions) ([]EntryOptions, []LifecycleEvent, error) {
		target := locate(&tree, id)
		if target == nil || s.Find(id) == nil {
			return nil, nil, fmt.Errorf("loader: unknown entry %q", id)
		}
		target.Disabled = disabled
		target.DisabledExpr = ""
		return tree, nil, nil
	})
}

// ------------------------------------------------------------ mutation API

// Lookup returns a copy of the options of the entry at the tree id path in the
// desired tree, children included, and whether there is one.
func (s *Service) Lookup(path string) (EntryOptions, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := locate(&s.current, path)
	if found == nil {
		return EntryOptions{}, false
	}
	return found.Clone(), true
}

// EntryOf returns the entry whose fiber is fiber or encloses it (a fiber loaded by
// the entry's component, at any depth), or nil.
func (s *Service) EntryOf(fiber *cordis.Fiber) *Entry {
	for fiber != nil {
		s.mu.Lock()
		entry := s.byFiber[fiber]
		s.mu.Unlock()
		if entry != nil {
			return entry
		}
		parent := fiber.Parent()
		if parent == nil || parent.Fiber() == fiber {
			return nil
		}
		fiber = parent.Fiber()
	}
	return nil
}

// Create validates options and adds them as a child of the group parentID ("" for
// the top level) at index (appended when index is negative or past the end), then
// applies the tree. An entry without an id is given one. It returns the new entry's
// tree id; an invalid entry changes nothing.
func (s *Service) Create(parentID string, index int, options EntryOptions) (string, error) {
	var id string
	err := s.edit(func(tree []EntryOptions) ([]EntryOptions, []LifecycleEvent, error) {
		var err error
		id, err = s.createIn(&tree, parentID, index, options)
		return tree, nil, err
	})
	return id, err
}

// Update validates options and replaces those of the entry id with them, keeping its
// children (and its id when options has none), then applies the tree. An invalid
// entry changes nothing.
func (s *Service) Update(id string, options EntryOptions) error {
	return s.edit(func(tree []EntryOptions) ([]EntryOptions, []LifecycleEvent, error) {
		return tree, nil, s.updateIn(&tree, id, options)
	})
}

// Set creates the entry at the tree id path, appended to its group, or updates it
// when it exists; the check and the change are one edit, so concurrent Set, Create
// and Remove calls cannot make it fail halfway. An id in options must match the
// path's last segment.
func (s *Service) Set(path string, options EntryOptions) error {
	if path == "" {
		return fmt.Errorf("loader: Set needs a tree id")
	}
	parentID, own := splitID(path)
	if options.ID != "" && options.ID != own {
		return fmt.Errorf("loader: entry id %q does not match path %q", options.ID, path)
	}
	return s.edit(func(tree []EntryOptions) ([]EntryOptions, []LifecycleEvent, error) {
		if locate(&tree, path) != nil {
			return tree, nil, s.updateIn(&tree, path, options)
		}
		options := options
		options.ID = own
		_, err := s.createIn(&tree, parentID, -1, options)
		return tree, nil, err
	})
}

// createIn is Create's change to tree.
func (s *Service) createIn(tree *[]EntryOptions, parentID string, index int, options EntryOptions) (string, error) {
	options = options.Clone()
	if options.ID == "" {
		options.ID = newID()
	}
	siblings, err := s.siblings(tree, parentID)
	if err != nil {
		return "", err
	}
	if err := s.validateAt(parentID, options, *siblings, ""); err != nil {
		return "", err
	}
	if index < 0 || index > len(*siblings) {
		index = len(*siblings)
	}
	*siblings = slices.Insert(*siblings, index, options)
	return compose(parentID, options.ID), nil
}

// updateIn is Update's change to tree.
func (s *Service) updateIn(tree *[]EntryOptions, id string, options EntryOptions) error {
	parentID, own := splitID(id)
	target := locate(tree, id)
	if target == nil {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	options = options.Clone()
	if options.ID == "" {
		options.ID = own
	} else if options.ID != own {
		return fmt.Errorf("loader: Update cannot change the id of %q to %q; use Move", id, options.ID)
	}
	options.Children = cloneAll(target.Children)
	siblings, err := s.siblings(tree, parentID)
	if err != nil {
		return err
	}
	if err := s.validateAt(parentID, options, *siblings, own); err != nil {
		return err
	}
	patch(*tree, id, options)
	return nil
}

// Move moves the entry id, with its children, to the group parentID ("" for the top
// level) at index (appended when negative or past the end), and returns its new tree
// id. Moving to another group changes the tree id, so the entry restarts under its
// new parent; a move among siblings keeps the fiber.
func (s *Service) Move(id, parentID string, index int) (string, error) {
	var moved string
	err := s.edit(func(tree []EntryOptions) ([]EntryOptions, []LifecycleEvent, error) {
		oldParent, own := splitID(id)
		if parentID == id || strings.HasPrefix(parentID, id+Separator) {
			return nil, nil, fmt.Errorf("loader: cannot move %q into itself", id)
		}
		from, err := s.siblings(&tree, oldParent)
		if err != nil {
			return nil, nil, err
		}
		at := slices.IndexFunc(*from, func(o EntryOptions) bool { return o.ID == own })
		if at < 0 {
			return nil, nil, fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		options := (*from)[at].Clone()
		*from = slices.Delete(*from, at, at+1)
		to, err := s.siblings(&tree, parentID)
		if err != nil {
			return nil, nil, err
		}
		if parentID != oldParent {
			if err := s.validateAt(parentID, options, *to, ""); err != nil {
				return nil, nil, err
			}
		}
		if index < 0 || index > len(*to) {
			index = len(*to)
		}
		*to = slices.Insert(*to, index, options)
		moved = compose(parentID, own)
		if parentID != oldParent {
			return tree, nil, nil // the diff records a remove and an add
		}
		after := options.withoutChildren()
		return tree, []LifecycleEvent{{Kind: EntryMoved, ID: id, After: &after}}, nil
	})
	return moved, err
}

// Remove removes the entry id and its children, then applies the tree.
func (s *Service) Remove(id string) error {
	return s.edit(func(tree []EntryOptions) ([]EntryOptions, []LifecycleEvent, error) {
		parentID, own := splitID(id)
		siblings, err := s.siblings(&tree, parentID)
		if err != nil {
			return nil, nil, err
		}
		at := slices.IndexFunc(*siblings, func(o EntryOptions) bool { return o.ID == own })
		if at < 0 {
			return nil, nil, fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		*siblings = slices.Delete(*siblings, at, at+1)
		return tree, nil, nil
	})
}

// edit runs one mutation: fn changes a copy of the desired tree, which is then
// applied, all under the apply lock so no other reconciliation interleaves. A change
// is reported for persistence (OnChange).
func (s *Service) edit(fn func(tree []EntryOptions) ([]EntryOptions, []LifecycleEvent, error)) error {
	if s.nested {
		return ErrReadOnly
	}
	s.apply.Lock()
	s.mu.Lock()
	tree := cloneAll(s.current)
	s.mu.Unlock()
	tree, extra, err := fn(tree)
	if err != nil {
		s.apply.Unlock()
		return err
	}
	if err := s.reconcileLocked(tree, extra, nil); err != nil {
		return err
	}
	s.changed.raise()
	return nil
}

// siblings returns the list holding the children of the group parentID in tree.
func (s *Service) siblings(tree *[]EntryOptions, parentID string) (*[]EntryOptions, error) {
	if parentID == "" {
		return tree, nil
	}
	parent := locate(tree, parentID)
	if parent == nil {
		return nil, fmt.Errorf("%w: group %q", ErrNotFound, parentID)
	}
	if !parent.Group {
		return nil, fmt.Errorf("loader: entry %q is not a group", parentID)
	}
	return &parent.Children, nil
}

// validateAt checks options, and its children, as an entry of the group parentID
// whose other children are siblings (self, when set, is the entry being replaced).
func (s *Service) validateAt(parentID string, options EntryOptions, siblings []EntryOptions, self string) error {
	seen := map[string]bool{}
	for _, sibling := range siblings {
		if sibling.ID != "" && sibling.ID != self {
			seen[compose(parentID, sibling.ID)] = true
		}
	}
	var errs []error
	s.walkChecks([]EntryOptions{options}, parentID, seen, func(id string, issues []Issue) {
		errs = append(errs, &EntryError{ID: id, Issues: issues})
	})
	return errors.Join(errs...)
}

// ------------------------------------------------------------------ restart

// Restart disposes the entry's fiber and loads it again with the same options; the
// tree and every file are left alone, and no sibling is touched. A group restarts
// with its children. Once the new fiber has settled, one Reloaded notice names the
// entry. It returns the new load's error, which the fiber also reports; a failed entry
// stays failed until it is restarted or changed.
func (s *Service) Restart(id string) error {
	s.apply.Lock()
	s.mu.Lock()
	closed := s.closed
	entry := s.entries[id]
	loaded := entry != nil && entry.fiber != nil
	s.mu.Unlock()
	switch {
	case closed:
		s.apply.Unlock()
		return ErrClosed
	case entry == nil:
		s.apply.Unlock()
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	case !loaded:
		s.apply.Unlock()
		return fmt.Errorf("loader: entry %q is not loaded (disabled, or held back by its group)", id)
	}
	s.disposeFiber(entry)
	_, err := s.ensure(entry)
	s.apply.Unlock()
	if err == nil {
		if fiber := s.Fiber(entry); fiber != nil {
			err = fiber.Wait()
		}
		for _, child := range s.Entries() {
			if strings.HasPrefix(child.id, id+Separator) {
				if fiber := s.Fiber(child); fiber != nil {
					_ = fiber.Wait() // a child's failure is reported by its fiber
				}
			}
		}
	} else {
		s.ctx.Logger().Error(err)
	}
	notice := ReloadNotice{Restarted: []string{id}}
	if err != nil {
		notice.Issues = issuesOf(id, err)
	}
	s.publish([]LifecycleEvent{{Kind: EntryRestarted, ID: id}}, notice)
	return err
}

// ------------------------------------------------------- lifecycle and notices

// LifecycleKind names what happened to an entry.
type LifecycleKind string

const (
	EntryAdded     LifecycleKind = "add"
	EntryUpdated   LifecycleKind = "update"
	EntryMoved     LifecycleKind = "move"
	EntryRemoved   LifecycleKind = "remove"
	EntryRestarted LifecycleKind = "restart"
)

// LifecycleEvent is one change of one entry of the tree.
type LifecycleEvent struct {
	// Seq numbers the events of one loader in the order they were recorded.
	Seq  int
	Kind LifecycleKind
	// ID is the entry's tree id.
	ID string
	// Before and After are the entry's options (without children) before and after
	// the change, when they apply: an add has no Before, a remove no After, a
	// restart neither.
	Before, After *EntryOptions
}

// Lifecycle returns the most recent lifecycle events, oldest first.
func (s *Service) Lifecycle() []LifecycleEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.lifecycle)
}

// OnLifecycle subscribes to lifecycle events as they are recorded: an Apply's after
// its fibers settle, a runtime Update of an entry's fiber when it is committed. It
// returns a function that removes the subscription.
func (s *Service) OnLifecycle(fn func(LifecycleEvent)) (remove func()) { return s.observers.add(fn) }

// ReloadNotice is the payload of Reloaded: the tree ids an Apply, or a Restart,
// created, updated in place, restarted and removed, and the issues it found. Updated
// entries had their config passed through fiber.Update; Restarted ones were rebuilt.
type ReloadNotice struct {
	Loader    *Service
	Created   []string
	Updated   []string
	Restarted []string
	Removed   []string
	Issues    []Issue
}

// Reloaded is emitted once per Apply and per Restart, after every fiber they touched
// has settled. A listener cannot veto it (it is a Notify). A nested loader's notice
// reaches only listeners registered at or below its owner.
var Reloaded = cordis.NewEvent[ReloadNotice]("loader/reloaded")

// publish records events in the lifecycle log, tells the observers and emits notice.
func (s *Service) publish(events []LifecycleEvent, notice ReloadNotice) {
	s.record(events)
	notice.Loader = s
	if s.nested {
		Reloaded.NotifyScoped(s.ctx, s.ctx.Fiber(), notice)
	} else {
		Reloaded.Notify(s.ctx, notice)
	}
}

func (s *Service) record(events []LifecycleEvent) {
	if len(events) == 0 {
		return
	}
	s.mu.Lock()
	for i := range events {
		s.seq++
		events[i].Seq = s.seq
	}
	s.lifecycle = append(s.lifecycle, events...)
	if extra := len(s.lifecycle) - lifecycleCap; extra > 0 {
		s.lifecycle = slices.Delete(s.lifecycle, 0, extra)
	}
	s.mu.Unlock()
	for _, event := range events {
		s.observers.raise(event)
	}
}

type flatEntry struct {
	options  EntryOptions
	parentID string
}

// flatten indexes the tree by tree id, assigning ids to anonymous entries in place.
// Every entry is validated; an invalid entry is reported once with all of its issues
// and skipped with its subtree, so a live entry with that id is disposed, and its
// siblings still load.
func (s *Service) flatten(entries []EntryOptions, parentID string, sink map[string]flatEntry, sequence *[]string, found *[]Issue) {
	for i := range entries {
		options := &entries[i]
		if options.ID == "" {
			options.ID = newID()
		}
		id := compose(parentID, options.ID)
		_, dup := sink[id]
		if issues := s.check(*options, id, dup); len(issues) > 0 {
			s.ctx.Logger().Error(&EntryError{ID: id, Issues: issues})
			*found = append(*found, issues...)
			// the subtree is not loaded, but its own faults are still reported
			s.walkChecks(options.Children, id, map[string]bool{}, func(id string, issues []Issue) {
				s.ctx.Logger().Error(&EntryError{ID: id, Issues: issues})
				*found = append(*found, issues...)
			})
			continue
		}
		sink[id] = flatEntry{options: *options, parentID: parentID}
		*sequence = append(*sequence, id)
		if len(options.Children) > 0 {
			s.flatten(options.Children, id, sink, sequence, found)
		}
	}
}

// Validate checks every entry of the tree, children included, without applying it,
// and returns all issues in document order: decoding faults, ids, names, duplicates,
// unresolvable components and rejected expressions. An issue of an entry decoded by
// Parse carries the file, line and column of the value at fault.
func (s *Service) Validate(entries []EntryOptions) []Issue {
	var issues []Issue
	s.walkChecks(entries, "", map[string]bool{}, func(_ string, found []Issue) {
		issues = append(issues, found...)
	})
	return issues
}

// walkChecks checks level and its descendants in document order without changing
// them, and calls report with the issues of each faulty entry. An anonymous entry gets
// a traversal-only identity from its index, so the children of two anonymous groups
// never collide; seen holds the tree ids met so far, for duplicates.
func (s *Service) walkChecks(level []EntryOptions, parentID string, seen map[string]bool, report func(id string, issues []Issue)) {
	for i, options := range level {
		var id string
		duplicate := false
		if options.ID == "" {
			// Separator cannot occur in a valid id, so this cannot match one
			id = compose(parentID, fmt.Sprintf("%s#%d", Separator, i))
		} else {
			id = compose(parentID, options.ID)
			duplicate = seen[id]
			seen[id] = true
		}
		if issues := s.check(options, id, duplicate); len(issues) > 0 {
			report(id, issues)
		}
		s.walkChecks(options.Children, id, seen, report)
	}
}

// check returns every issue of one entry (not of its children).
func (s *Service) check(options EntryOptions, id string, duplicate bool) []Issue {
	issues := slices.Clone(options.issues)
	faulty := func(relative string) bool {
		_, path := options.position(relative)
		return slices.ContainsFunc(issues, func(issue Issue) bool { return path != "" && issue.Path == path })
	}
	add := func(relative, message string) {
		pos, path := options.position(relative)
		issues = append(issues, Issue{Pos: pos, Path: path, Message: message})
	}
	if strings.Contains(options.ID, Separator) {
		add("id", fmt.Sprintf("entry id %q must not contain %q", options.ID, Separator))
	} else if duplicate {
		add("id", fmt.Sprintf("duplicate entry id %q", id))
	}
	if !options.Group && !faulty("name") {
		if strings.TrimSpace(options.Name) == "" {
			add("name", fmt.Sprintf("entry %q requires a name", options.ID))
		} else if _, ok := s.resolver.Resolve(options.Name); !ok {
			add("name", fmt.Sprintf("cannot resolve component %q", options.Name))
		}
	}
	return append(issues, s.checkExprs(options)...)
}

// ensure creates the entry's fiber if it can load now, or returns an update to run
// for a live fiber whose config changed.
func (s *Service) ensure(entry *Entry) (func() error, error) {
	// a disabled expression is evaluated before taking any lock
	s.mu.Lock()
	options := entry.options.Clone()
	scope, held := s.scopeLocked(entry)
	s.mu.Unlock()
	if held {
		return nil, nil
	}
	disabled, err := s.evalDisabled(options, entry.id, scope)
	if disabled || err != nil {
		if s.Fiber(entry) != nil {
			s.disposeFiber(entry)
		}
		return nil, err
	}

	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()

	s.mu.Lock()
	options = entry.options.Clone()
	fiber := entry.fiber
	applied := entry.applied
	scope, held = s.scopeLocked(entry)
	s.mu.Unlock()

	if held || (options.DisabledExpr == "" && options.Disabled) {
		return nil, nil
	}

	if fiber != nil && fiber.State() != cordis.Disposed {
		if jsonEqual(applied, options.Config) {
			return nil, nil
		}
		s.mu.Lock()
		entry.applied = options.Config
		s.mu.Unlock()
		if options.Group {
			return nil, nil
		}
		config := options.Config
		return func() error {
			s.mu.Lock()
			stale := !jsonEqual(entry.applied, config) // a runtime commit came in since
			s.mu.Unlock()
			if stale {
				return nil
			}
			err := s.updateFiber(fiber, config)
			if err != nil {
				// refused: the old config still runs, so the same config is tried (and
				// reported) again by the next Apply instead of being taken as applied
				s.mu.Lock()
				if jsonEqual(entry.applied, config) {
					entry.applied = applied
				}
				s.mu.Unlock()
			}
			return err
		}, nil
	}

	names := slices.Sorted(maps.Keys(options.Isolate))
	var labels []string
	for _, name := range names {
		label, key := s.acquireRealm(name, options.Isolate[name], entry.id)
		labels = append(labels, label)
		scope = scope.IsolateWith(name, key)
	}
	for _, name := range slices.Sorted(maps.Keys(options.Intercept)) {
		scope = scope.Intercept(name, slices.Clone(options.Intercept[name]))
	}

	var created *cordis.Fiber
	if options.Group {
		s.useGate.Lock()
		created, err = scope.Apply("group:"+options.ID, func(groupCtx *cordis.Context) error {
			return s.loadGroup(entry, groupCtx)
		}, options.Inject...)
	} else {
		component, ok := s.resolver.Resolve(options.Name)
		if !ok {
			s.releaseRealms(labels)
			pos, path := options.position("name")
			return nil, &EntryError{ID: entry.id, Issues: []Issue{{Pos: pos, Path: path, Message: fmt.Sprintf("cannot resolve component %q", options.Name)}}}
		}
		var config any
		if len(options.Config) > 0 {
			config = options.Config
		}
		s.useGate.Lock()
		created, err = scope.Use(component, config, options.Inject...)
	}
	if err != nil {
		s.useGate.Unlock()
		s.releaseRealms(labels)
		return nil, err
	}

	s.mu.Lock()
	entry.fiber = created
	entry.applied = options.Config
	stale := entry.realms
	entry.realms = labels
	s.byFiber[created] = entry
	s.mu.Unlock()
	s.useGate.Unlock()
	s.releaseRealms(stale)
	return nil, nil
}

// scopeLocked returns the context the entry loads in; held is true while its
// enclosing group is disabled, pending or reloading.
func (s *Service) scopeLocked(entry *Entry) (scope *cordis.Context, held bool) {
	if entry.parentID == "" {
		return s.ctx, false
	}
	parent := s.entries[entry.parentID]
	if parent == nil || parent.scope == nil {
		return nil, true
	}
	return parent.scope, false
}

// loadGroup is the body of a group entry: it records the group's scope and creates
// the children that can load.
func (s *Service) loadGroup(group *Entry, groupCtx *cordis.Context) error {
	// registered first, so it runs last: after the children (effects of this fiber) are gone
	if _, err := groupCtx.Effect("loader/group", func() (cordis.Disposer, error) {
		return func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			group.scope = nil
			for _, child := range s.entries {
				if child.parentID != group.id || child.fiber == nil || child.fiber.State() != cordis.Disposed {
					continue
				}
				delete(s.byFiber, child.fiber)
				child.fiber = nil
				s.releaseRealmsLocked(child.realms)
				child.realms = nil
			}
			return nil
		}, nil
	}); err != nil {
		return err
	}

	s.mu.Lock()
	group.scope = groupCtx
	var children []*Entry
	for _, entry := range s.entries {
		if entry.parentID == group.id {
			children = append(children, entry)
		}
	}
	s.mu.Unlock()
	sort.Slice(children, func(i, j int) bool { return children[i].order < children[j].order })

	for _, child := range children {
		if _, err := s.ensure(child); err != nil {
			s.ctx.Logger().Error(err)
		}
	}
	return nil
}

// updateFiber passes config to the running fiber. It returns the error of an update
// that was refused before anything changed (e.g. by the schema), so the old config
// still runs; a reload that failed is reported by the fiber itself.
func (s *Service) updateFiber(fiber *cordis.Fiber, config json.RawMessage) error {
	var value any
	if len(config) > 0 {
		value = config
	}
	if err := fiber.Update(value, true); err != nil && fiber.State() != cordis.Failed {
		s.ctx.Logger().Error(err)
		return err
	}
	return nil
}

func (s *Service) disposeFiber(entry *Entry) {
	s.mu.Lock()
	fiber := entry.fiber
	entry.fiber = nil
	entry.scope = nil
	if fiber != nil {
		delete(s.byFiber, fiber)
	}
	realms := entry.realms
	entry.realms = nil
	s.mu.Unlock()
	if fiber != nil {
		fiber.Dispose()
	}
	s.releaseRealms(realms)
}

func (s *Service) remove(id string) {
	s.mu.Lock()
	entry := s.entries[id]
	delete(s.entries, id)
	s.mu.Unlock()
	if entry != nil {
		s.disposeFiber(entry)
	}
}

// acquireRealm returns the shared realm key of the label, counting the entry as a user.
func (s *Service) acquireRealm(service, realm, entryID string) (string, *cordis.IsolateKey) {
	label := service + "@" + realm
	if realm == "true" {
		label = service + "#" + entryID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.realms[label]
	if !ok {
		key = cordis.NewIsolateKey(label)
		s.realms[label] = key
	}
	s.realmUsers[label]++
	return label, key
}

// releaseRealms drops one use of each label; a key with no users left is deleted.
func (s *Service) releaseRealms(labels []string) {
	if len(labels) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseRealmsLocked(labels)
}

func (s *Service) releaseRealmsLocked(labels []string) {
	for _, label := range labels {
		if s.realmUsers[label]--; s.realmUsers[label] <= 0 {
			delete(s.realmUsers, label)
			delete(s.realms, label)
		}
	}
}

// onCommit records a runtime Update of an entry's fiber in the desired tree.
func (s *Service) onCommit(fiber *cordis.Fiber, config any) {
	var raw json.RawMessage
	if config != nil {
		encoded, err := json.Marshal(config)
		if err != nil {
			s.ctx.Logger().Error(fmt.Errorf("loader: cannot persist config of %s: %w", fiber.Name(), err))
			return
		}
		raw = encoded
	}
	s.mu.Lock()
	entry := s.byFiber[fiber]
	if entry == nil {
		s.mu.Unlock()
		return
	}
	before := entry.options.Clone()
	options := entry.options.Clone()
	options.Config = raw
	entry.options = options
	entry.applied = raw
	patch(s.current, entry.id, options.Clone())
	if s.committed != nil {
		s.committed[entry.id] = true
	}
	id := entry.id
	s.mu.Unlock()
	after := options.Clone()
	s.record([]LifecycleEvent{{Kind: EntryUpdated, ID: id, Before: &before, After: &after}})
	s.changed.raise()
}

func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// listenerList is a small thread-safe list of callbacks.
type listenerList struct {
	mu    sync.Mutex
	seq   int
	items map[int]func()
}

func (l *listenerList) add(fn func()) func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.items == nil {
		l.items = map[int]func(){}
	}
	l.seq++
	id := l.seq
	l.items[id] = fn
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.items, id)
	}
}

func (l *listenerList) raise() {
	l.mu.Lock()
	fns := make([]func(), 0, len(l.items))
	for _, fn := range l.items {
		fns = append(fns, fn)
	}
	l.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// eventList is a thread-safe list of callbacks taking an argument, called in
// subscription order.
type eventList[T any] struct {
	mu    sync.Mutex
	seq   int
	items []eventItem[T]
}

type eventItem[T any] struct {
	id int
	fn func(T)
}

func (l *eventList[T]) add(fn func(T)) func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	id := l.seq
	l.items = append(l.items, eventItem[T]{id, fn})
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.items = slices.DeleteFunc(l.items, func(item eventItem[T]) bool { return item.id == id })
	}
}

func (l *eventList[T]) raise(value T) {
	l.mu.Lock()
	items := slices.Clone(l.items)
	l.mu.Unlock()
	for _, item := range items {
		item.fn(value)
	}
}
