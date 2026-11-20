// Package loader provides the "loader" service, which materialises a declarative tree
// of entries into fibers and reconciles it on every change, and ConfigFile, which
// keeps that tree in sync with a JSON file (upstream's @cordisjs/loader and
// @cordisjs/include).
package loader

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
}

func newService(ctx *cordis.Context, options Options) (*Service, error) {
	resolver := options.Resolver
	if resolver == nil {
		resolver = Registered
	}
	s := &Service{
		ctx:        ctx,
		resolver:   resolver,
		evaluator:  options.Evaluator,
		entries:    map[string]*Entry{},
		byFiber:    map[*cordis.Fiber]*Entry{},
		realms:     map[string]*cordis.IsolateKey{},
		realmUsers: map[string]int{},
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
// has settled.
func (s *Service) Apply(desired []EntryOptions) error {
	s.apply.Lock()
	tree := cloneAll(desired)
	flattened := map[string]flatEntry{}
	var sequence []string
	s.flatten(tree, "", flattened, &sequence)

	// Publish the desired tree first, so a runtime commit made while the tree is being
	// reconciled is recorded in the tree that will be persisted.
	s.mu.Lock()
	s.current = tree
	s.committed = map[string]bool{}
	var removed []string
	for id := range s.entries {
		if _, ok := flattened[id]; !ok {
			removed = append(removed, id)
		}
	}
	s.mu.Unlock()
	for _, id := range removed {
		s.remove(id)
	}

	// 1. records: rebuild entries whose shape changed, so step 2 recreates them
	for position, id := range sequence {
		flat := flattened[id]
		s.mu.Lock()
		entry := s.entries[id]
		if entry == nil {
			entry = &Entry{id: id, parentID: flat.parentID}
			s.entries[id] = entry
		}
		rebuild := entry.fiber != nil && !entry.options.sameShape(flat.options)
		s.mu.Unlock()
		if rebuild {
			s.disposeFiber(entry)
		}
		s.mu.Lock()
		options := flat.options.withoutChildren()
		if s.committed[id] {
			options.Config = entry.options.Config
		}
		entry.options = options
		entry.order = position
		s.mu.Unlock()
	}

	// 2. materialise, parents first; children of unloaded groups wait for their group
	var updates sync.WaitGroup
	for _, id := range sequence {
		entry := s.Find(id)
		if entry == nil {
			continue
		}
		update, err := s.ensure(entry)
		if err != nil {
			s.ctx.Logger().Error(err)
		}
		if update != nil {
			updates.Add(1)
			go func() {
				defer updates.Done()
				update()
			}()
		}
	}
	updates.Wait()

	s.mu.Lock()
	s.committed = nil
	s.mu.Unlock()
	s.apply.Unlock()

	s.WaitAll()
	return nil
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
// persistence.
func (s *Service) SetDisabled(id string, disabled bool) error {
	entry := s.Find(id)
	if entry == nil {
		return fmt.Errorf("loader: unknown entry %q", id)
	}
	tree := s.Snapshot()
	options := s.Options(entry)
	options.Disabled = disabled
	options.DisabledExpr = ""
	patch(tree, id, options)
	if err := s.Apply(tree); err != nil {
		return err
	}
	s.changed.raise()
	return nil
}

type flatEntry struct {
	options  EntryOptions
	parentID string
}

// flatten indexes the tree by tree id, assigning ids to anonymous entries in place.
// Every entry is validated; an invalid entry is reported once with all of its issues
// and skipped with its subtree, so a live entry with that id is disposed, and its
// siblings still load.
func (s *Service) flatten(entries []EntryOptions, parentID string, sink map[string]flatEntry, sequence *[]string) {
	for i := range entries {
		options := &entries[i]
		if options.ID == "" {
			options.ID = newID()
		}
		id := compose(parentID, options.ID)
		_, dup := sink[id]
		if issues := s.check(*options, id, dup); len(issues) > 0 {
			s.ctx.Logger().Error(&EntryError{ID: id, Issues: issues})
			// the subtree is not loaded, but its own faults are still reported
			s.walkChecks(options.Children, id, map[string]bool{}, func(id string, issues []Issue) {
				s.ctx.Logger().Error(&EntryError{ID: id, Issues: issues})
			})
			continue
		}
		sink[id] = flatEntry{options: *options, parentID: parentID}
		*sequence = append(*sequence, id)
		if len(options.Children) > 0 {
			s.flatten(options.Children, id, sink, sequence)
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
func (s *Service) ensure(entry *Entry) (func(), error) {
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
		return func() {
			s.mu.Lock()
			stale := !jsonEqual(entry.applied, config) // a runtime commit came in since
			s.mu.Unlock()
			if !stale {
				s.updateFiber(fiber, config)
			}
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

func (s *Service) updateFiber(fiber *cordis.Fiber, config json.RawMessage) {
	var value any
	if len(config) > 0 {
		value = config
	}
	if err := fiber.Update(value, true); err != nil && fiber.State() != cordis.Failed {
		s.ctx.Logger().Error(err) // e.g. rejected by the schema before anything changed
	}
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
	options := entry.options.Clone()
	options.Config = raw
	entry.options = options
	entry.applied = raw
	patch(s.current, entry.id, options.Clone())
	if s.committed != nil {
		s.committed[entry.id] = true
	}
	s.mu.Unlock()
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
