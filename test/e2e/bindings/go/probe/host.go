package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/go/timer"
)

// RootID is the id that names the root context in actions and trace markers.
const RootID = "root"

// Session holds the current host of a run. The runtime gives an action no case
// identity, and cases run one at a time, so there is one current host.
type Session struct {
	mu      sync.Mutex
	current *Host
}

// Start disposes the current host, if any, and starts a new one.
func (s *Session) Start(ctx context.Context, withLoader, withTimer bool) (*Host, error) {
	s.mu.Lock()
	previous := s.current
	s.current = nil
	s.mu.Unlock()
	if previous != nil {
		if err := previous.Dispose(ctx); err != nil {
			return nil, err
		}
	}
	host, err := newHost(ctx, withLoader, withTimer)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.current = host
	s.mu.Unlock()
	return host, nil
}

// Current returns the current host, or nil before host.start.
func (s *Session) Current() *Host {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Host is one cordis tree and everything the probes record about it.
type Host struct {
	Root   *cordis.Context
	Logs   *cordis.MemoryExporter
	Loader *loader.Service

	done        chan struct{} // closed on dispose: releases every gate wait
	disposeOnce sync.Once

	mu         sync.Mutex
	trace      []string
	loads      map[string]int64
	fibers     map[string]*cordis.Fiber
	known      map[*cordis.Fiber]struct{}
	components map[string]*cordis.Component
	realms     map[string]*cordis.IsolateKey
	events     map[string]any
	gates      map[string]*gate
	services   map[string]*cordis.EffectHandle
	configFile *loader.ConfigFile
	configPath string
	tempDirs   []string
}

type gate struct {
	once sync.Once
	ch   chan struct{}
}

func newHost(ctx context.Context, withLoader, withTimer bool) (*Host, error) {
	root := cordis.New()
	logs := &cordis.MemoryExporter{}
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(logs)
	h := &Host{
		Root:       root,
		Logs:       logs,
		done:       make(chan struct{}),
		loads:      map[string]int64{},
		fibers:     map[string]*cordis.Fiber{},
		known:      map[*cordis.Fiber]struct{}{},
		components: map[string]*cordis.Component{},
		realms:     map[string]*cordis.IsolateKey{},
		events:     map[string]any{},
		gates:      map[string]*gate{},
		services:   map[string]*cordis.EffectHandle{},
	}
	root.Internal().OnPlugin(func(fiber *cordis.Fiber) {
		h.mu.Lock()
		h.known[fiber] = struct{}{}
		h.mu.Unlock()
	})

	if withTimer {
		// The timer component runs in a private realm; its instance is then provided on
		// the root itself, so every fiber reaches it without inject. The service is
		// Scoped, so each caller gets a copy whose timers that caller's fiber owns.
		fiber, err := root.Isolate(timer.Key.Name()).Use(timer.Component, nil)
		if err != nil {
			return nil, err
		}
		if err := WaitFiber(ctx, fiber); err != nil {
			return nil, err
		}
		service, ok := fiber.Instance().(*timer.Service)
		if !ok {
			return nil, fmt.Errorf("timer service did not start: %v", fiber.Err())
		}
		if _, err := timer.Key.Provide(root, service); err != nil {
			return nil, err
		}
	}
	if withLoader {
		fiber, err := root.Use(loader.Component, loader.Options{Resolver: resolver{h}})
		if err != nil {
			return nil, err
		}
		if err := WaitFiber(ctx, fiber); err != nil {
			return nil, err
		}
		service, ok := fiber.Instance().(*loader.Service)
		if !ok {
			return nil, fmt.Errorf("loader service did not start: %v", fiber.Err())
		}
		h.Loader = service
	}
	return h, nil
}

// resolver resolves the name "probe", and any implementation already in use, to
// the probe component.
type resolver struct{ h *Host }

func (r resolver) Resolve(name string) (*cordis.Component, bool) {
	if name == "probe" {
		return r.h.component(name, nil), true
	}
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	c, ok := r.h.components[name]
	return c, ok
}

// Dispose disposes the root, which unloads the whole tree from the leaves up, waits
// for it, and deletes the temporary folders. A second call does nothing.
func (h *Host) Dispose(ctx context.Context) error {
	var err error
	h.disposeOnce.Do(func() {
		close(h.done)
		err = run(ctx, h.Root.Dispose)
		h.mu.Lock()
		dirs := h.tempDirs
		h.tempDirs = nil
		h.mu.Unlock()
		for _, dir := range dirs {
			_ = os.RemoveAll(dir)
		}
	})
	return err
}

// --------------------------------------------------------------------- trace

// Record appends a marker to the trace.
func (h *Host) Record(marker string) {
	h.mu.Lock()
	h.trace = append(h.trace, marker)
	h.mu.Unlock()
}

// Trace returns the markers that start with prefix and contain text (either may be
// empty).
func (h *Host) Trace(prefix, contains string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	events := []string{}
	for _, marker := range h.trace {
		if strings.HasPrefix(marker, prefix) && strings.Contains(marker, contains) {
			events = append(events, marker)
		}
	}
	return events
}

// WaitFor polls the trace until a marker contains text.
func (h *Host) WaitFor(ctx context.Context, text string) error {
	for {
		if len(h.Trace("", text)) > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("no trace marker contains %q: %w", text, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (h *Host) addLoad(id string) {
	h.mu.Lock()
	h.loads[id]++
	h.mu.Unlock()
}

// Loads returns how many times the body of the probe id has run.
func (h *Host) Loads(id string) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.loads[id]
}

// --------------------------------------------------------------------- gates

func (h *Host) gate(name string) *gate {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.gates[name]
	if !ok {
		g = &gate{ch: make(chan struct{})}
		h.gates[name] = g
	}
	return g
}

// OpenGate opens a gate; a gate opened before anyone waits stays open.
func (h *Host) OpenGate(name string) {
	g := h.gate(name)
	g.once.Do(func() { close(g.ch) })
}

// waitGate waits for a gate to open; false when the host was disposed first.
func (h *Host) waitGate(name string) bool {
	select {
	case <-h.gate(name).ch:
		return true
	case <-h.done:
		return false
	}
}

// -------------------------------------------------------------------- fibers

func (h *Host) register(id string, fiber *cordis.Fiber) {
	if fiber == nil {
		return
	}
	h.mu.Lock()
	h.fibers[id] = fiber
	h.known[fiber] = struct{}{}
	h.mu.Unlock()
}

// Fiber returns the fiber registered under id, or the fiber of the loader entry
// whose composite id it is; nil when there is none.
func (h *Host) Fiber(id string) *cordis.Fiber {
	h.mu.Lock()
	fiber := h.fibers[id]
	h.mu.Unlock()
	if fiber != nil {
		return fiber
	}
	if h.Loader != nil {
		if entry := h.Loader.Find(id); entry != nil {
			return h.Loader.Fiber(entry)
		}
	}
	return nil
}

// Context returns the context of the fiber id, or the root for "" and "root".
func (h *Host) Context(id string) (*cordis.Context, error) {
	if id == "" || id == RootID {
		return h.Root, nil
	}
	fiber := h.Fiber(id)
	if fiber == nil {
		return nil, fmt.Errorf("no fiber %q", id)
	}
	return fiber.Ctx(), nil
}

// idOf names a fiber the probe did not create with an id: a loader entry's, by its
// composite id. The loader records the fiber right after Use returns, which may be
// after the body started, so look until it appears.
func (h *Host) idOf(fiber *cordis.Fiber) string {
	if h.Loader == nil {
		return fiber.Name()
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, entry := range h.Loader.Entries() {
			if h.Loader.Fiber(entry) == fiber {
				return entry.ID()
			}
		}
		if time.Now().After(deadline) {
			return fiber.Name()
		}
		time.Sleep(time.Millisecond)
	}
}

// allFibers is every fiber the host knows of: those it registered or saw created,
// and those the registry holds now.
func (h *Host) allFibers() []*cordis.Fiber {
	seen := map[*cordis.Fiber]struct{}{}
	for _, runtime := range h.Root.Registry().Runtimes() {
		for _, fiber := range runtime.Fibers() {
			seen[fiber] = struct{}{}
		}
	}
	h.mu.Lock()
	for fiber := range h.known {
		seen[fiber] = struct{}{}
	}
	h.mu.Unlock()
	if h.Loader != nil {
		for _, entry := range h.Loader.Entries() {
			if fiber := h.Loader.Fiber(entry); fiber != nil {
				seen[fiber] = struct{}{}
			}
		}
	}
	fibers := make([]*cordis.Fiber, 0, len(seen))
	for fiber := range seen {
		fibers = append(fibers, fiber)
	}
	return fibers
}

// Settle waits until two consecutive passes over every known fiber find none
// Loading or Unloading.
func (h *Host) Settle(ctx context.Context) error {
	quiet := 0
	for quiet < 2 {
		busy := false
		for _, fiber := range h.allFibers() {
			if state := fiber.State(); state == cordis.Loading || state == cordis.Unloading {
				busy = true
				if err := WaitFiber(ctx, fiber); err != nil {
					return err
				}
			}
		}
		if busy {
			quiet = 0
		} else {
			quiet++
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the tree did not settle: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

// WaitFiber waits for a fiber to settle, ignoring a load error, within ctx.
func WaitFiber(ctx context.Context, fiber *cordis.Fiber) error {
	return run(ctx, func() { _ = fiber.Wait() })
}

// run runs fn, giving up (and leaving it running) when ctx ends first.
func run(ctx context.Context, fn func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------------------------------------------------------------- components

func (h *Host) realm(service, label string) *cordis.IsolateKey {
	h.mu.Lock()
	defer h.mu.Unlock()
	name := service + "@" + label
	key, ok := h.realms[name]
	if !ok {
		key = cordis.NewIsolateKey(name)
		h.realms[name] = key
	}
	return key
}

// isolated chains one isolated scope per service, with one realm key per (service,
// label) for the whole host.
func (h *Host) isolated(scope *cordis.Context, isolate map[string]string) *cordis.Context {
	names := make([]string, 0, len(isolate))
	for name := range isolate {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		scope = scope.IsolateWith(name, h.realm(name, isolate[name]))
	}
	return scope
}

// Use creates a probe under the root or the parent fiber, in the isolated realms
// given, and registers it under spec.ID. It returns at once.
func (h *Host) Use(spec Spec, parent, implementation string, isolate map[string]string) (*cordis.Fiber, error) {
	scope, err := h.Context(parent)
	if err != nil {
		return nil, err
	}
	scope = h.isolated(scope, isolate)
	fiber, err := scope.Use(h.component(implementation, nil), spec, spec.Inject...)
	if err != nil {
		return nil, err
	}
	h.register(spec.ID, fiber)
	return fiber, nil
}

// Replace swaps the implementation from for to (created with inject if new) behind
// every live fiber, and returns how many fibers moved.
func (h *Host) Replace(from, to string, inject []string) int {
	return len(h.Root.Registry().Replace(h.component(from, nil), h.component(to, inject)))
}

// ------------------------------------------------------------------ services

// Provide provides a plain value on the root, or in the root's realm label.
func (h *Host) Provide(name, value, realm string) error {
	scope := h.Root
	if realm != "" {
		scope = scope.IsolateWith(name, h.realm(name, realm))
	}
	handle, err := scope.Provide(name, value)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.services[name] = handle
	h.mu.Unlock()
	return nil
}

// Withdraw disposes a service made by Provide; with wait, it returns once every
// dependent has let go.
func (h *Host) Withdraw(ctx context.Context, name string, wait bool) error {
	h.mu.Lock()
	handle := h.services[name]
	delete(h.services, name)
	h.mu.Unlock()
	if handle == nil {
		return fmt.Errorf("no service %q was provided by service.provide", name)
	}
	if !wait {
		go handle.Dispose()
		return nil
	}
	return run(ctx, handle.Dispose)
}

// Lookup resolves name from the context of the fiber from. err is "not-injected",
// "unavailable" or "" (another failure is returned as an error).
func (h *Host) Lookup(from, name string, optional bool) (value any, found bool, failure string, err error) {
	scope, err := h.Context(from)
	if err != nil {
		return nil, false, "", err
	}
	// An optional access is the port's unchecked lookup (upstream's ctx.get): no inject
	// check, and nothing found is not a failure.
	if optional {
		value, found = scope.Lookup(name)
		return value, found, "", nil
	}
	value, err = scope.Get(name)
	switch {
	case err == nil:
		return value, true, "", nil
	case errors.Is(err, cordis.ErrNotInjected):
		return nil, false, "not-injected", nil
	case errors.Is(err, cordis.ErrUnavailable):
		return nil, false, "unavailable", nil
	}
	return nil, false, "", err
}

// ---------------------------------------------------------------- events

// eventKey returns the one key of an event in a mode: an EventKey[string] for emit
// and parallel, a BailKey[string, string] for bail, a WaterfallKey[string] for
// waterfall.
func (h *Host) eventKey(name, mode string) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cacheKey := mode + "\x00" + name
	if key, ok := h.events[cacheKey]; ok {
		return key, nil
	}
	var key any
	switch mode {
	case "emit", "parallel":
		key = cordis.NewEvent[string](name)
	case "bail":
		key = cordis.NewBail[string, string](name)
	case "waterfall":
		key = cordis.NewWaterfall[string](name)
	default:
		return nil, fmt.Errorf("unknown event mode %q", mode)
	}
	h.events[cacheKey] = key
	return key, nil
}

// Listener describes a probe listener.
type Listener struct {
	Owner, Event, Mode, Label, Behaviour, Answer string
	Delay                                        time.Duration
	Prepend                                      bool
}

// Listen registers a probe listener on the owner fiber's context.
func (h *Host) Listen(l Listener) error {
	scope, err := h.Context(l.Owner)
	if err != nil {
		return err
	}
	key, err := h.eventKey(l.Event, l.Mode)
	if err != nil {
		return err
	}
	owner := l.Owner
	if owner == "" {
		owner = RootID
	}
	ran := func() {
		if l.Behaviour == "delay" {
			time.Sleep(l.Delay)
		}
		h.Record(owner + ":listener " + l.Label)
	}
	failure := func() error { return fmt.Errorf("listener %s of %s failed", l.Label, l.Event) }

	switch key := key.(type) {
	case *cordis.EventKey[string]:
		fn := func(*cordis.Context, string) error {
			ran()
			if l.Behaviour == "throw" {
				return failure()
			}
			return nil
		}
		if l.Prepend {
			_, err = key.Prepend(scope, fn)
		} else {
			_, err = key.On(scope, fn)
		}
	case *cordis.BailKey[string, string]:
		fn := func(*cordis.Context, string) (string, bool, error) {
			ran()
			switch l.Behaviour {
			case "throw":
				return "", false, failure()
			case "answer", "veto":
				return l.Answer, true, nil
			}
			return "", false, nil
		}
		if l.Prepend {
			_, err = key.Prepend(scope, fn)
		} else {
			_, err = key.On(scope, fn)
		}
	case *cordis.WaterfallKey[string]:
		fn := func(_ *cordis.Context, value string, next func(string) (string, error)) (string, error) {
			ran()
			switch l.Behaviour {
			case "throw":
				return "", failure()
			case "answer":
				return next(l.Answer)
			case "veto":
				return l.Answer, nil
			case "callNextTwice":
				if _, err := next(value); err != nil {
					return "", err
				}
				return next(value)
			}
			return next(value)
		}
		if l.Prepend {
			_, err = key.Prepend(scope, fn)
		} else {
			_, err = key.On(scope, fn)
		}
	}
	return err
}

// Dispatch dispatches an event from the fiber from and returns the bail answer or
// the waterfall result (nil when none), the failure (nil when none) and how many
// listener failures it carries.
func (h *Host) Dispatch(event, mode, from, payload string) (result any, failure error, errorCount int64, err error) {
	scope, err := h.Context(from)
	if err != nil {
		return nil, nil, 0, err
	}
	key, err := h.eventKey(event, mode)
	if err != nil {
		return nil, nil, 0, err
	}
	if from == "" {
		from = RootID
	}
	switch mode {
	case "emit":
		failure = key.(*cordis.EventKey[string]).Emit(scope, payload)
	case "parallel":
		failure = key.(*cordis.EventKey[string]).Parallel(scope, payload)
	case "bail":
		answer, ok, bailErr := key.(*cordis.BailKey[string, string]).Bail(scope, payload)
		failure = bailErr
		if ok && bailErr == nil {
			result = answer
		}
	case "waterfall":
		value, waterfallErr := key.(*cordis.WaterfallKey[string]).Waterfall(scope, payload, func(value string) (string, error) {
			h.Record(from + ":terminal")
			return value, nil
		})
		failure = waterfallErr
		if waterfallErr == nil {
			result = value
		}
	}
	if failure != nil {
		var aggregate *cordis.AggregateError
		if errors.As(failure, &aggregate) {
			errorCount = int64(len(aggregate.Errors))
		} else {
			errorCount = 1
		}
	}
	return result, failure, errorCount, nil
}

// -------------------------------------------------------------------- timers

// Schedule schedules a timer through the timer service, as the owner fiber.
func (h *Host) Schedule(owner, label, kind string, delay time.Duration, calls int) error {
	scope, err := h.Context(owner)
	if err != nil {
		return err
	}
	timers, err := timer.Key.Get(scope)
	if err != nil {
		return err
	}
	if owner == "" {
		owner = RootID
	}
	fire := func() { h.Record(owner + ":timer " + label) }
	switch kind {
	case "timeout":
		_, err = timers.Timeout(fire, delay)
	case "interval":
		_, err = timers.Interval(fire, delay)
	case "debounce", "throttle":
		var call func()
		if kind == "debounce" {
			call, err = timers.Debounce(fire, delay)
		} else {
			call, err = timers.Throttle(fire, delay, false)
		}
		if err == nil {
			for range calls {
				call()
			}
		}
	default:
		err = fmt.Errorf("unknown timer kind %q", kind)
	}
	return err
}

// ------------------------------------------------------------------- loader

func (h *Host) requireLoader() error {
	if h.Loader == nil {
		return errors.New("the host was started without the loader")
	}
	return nil
}

// Apply reconciles the loader with entries, then settles the tree.
func (h *Host) Apply(ctx context.Context, entries []loader.EntryOptions) error {
	if err := h.requireLoader(); err != nil {
		return err
	}
	var applyErr error
	if err := run(ctx, func() { applyErr = h.Loader.Apply(entries) }); err != nil {
		return err
	}
	if applyErr != nil {
		return applyErr
	}
	return h.Settle(ctx)
}

// SetDisabled enables or disables a loader entry, then settles the tree.
func (h *Host) SetDisabled(ctx context.Context, id string, disabled bool) error {
	if err := h.requireLoader(); err != nil {
		return err
	}
	var setErr error
	if err := run(ctx, func() { setErr = h.Loader.SetDisabled(id, disabled) }); err != nil {
		return err
	}
	if setErr != nil {
		return setErr
	}
	return h.Settle(ctx)
}

// OpenConfigFile writes entries to a config file in a fresh temporary folder,
// backs the loader with it, waits for the first reconcile and settles the tree.
func (h *Host) OpenConfigFile(ctx context.Context, entries []loader.EntryOptions, watch bool) (string, error) {
	if err := h.requireLoader(); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "cordis-e2e-")
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	h.tempDirs = append(h.tempDirs, dir)
	h.mu.Unlock()
	path := filepath.Join(dir, "cordis.json")
	text, err := EntriesText(entries)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	fiber, err := h.Root.Use(loader.ConfigFileComponent, loader.ConfigFileOptions{
		Path:         path,
		Watch:        watch,
		PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		return "", err
	}
	if err := WaitFiber(ctx, fiber); err != nil {
		return "", err
	}
	file, ok := fiber.Instance().(*loader.ConfigFile)
	if !ok {
		return "", fmt.Errorf("the config file component did not load: %v", fiber.Err())
	}
	h.mu.Lock()
	h.configFile = file
	h.configPath = path
	h.mu.Unlock()
	return path, h.Settle(ctx)
}

func (h *Host) config() (*loader.ConfigFile, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.configFile == nil {
		return nil, "", errors.New("no config file is open; run configFile.open first")
	}
	return h.configFile, h.configPath, nil
}

// WriteConfigFile writes the config file as another program would: in place, or
// through a temporary file renamed over it.
func (h *Host) WriteConfigFile(ctx context.Context, text string, byRename bool) error {
	_, path, err := h.config()
	if err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		err = writeFile(path, text, byRename)
		if err == nil {
			return nil
		}
		// the watcher may hold the file open for a moment (Windows)
		if attempt >= 20 {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(10*attempt) * time.Millisecond):
		}
	}
}

func writeFile(path, text string, byRename bool) error {
	if !byRename {
		return os.WriteFile(path, []byte(text), 0o644)
	}
	temp := path + ".save.tmp"
	if err := os.WriteFile(temp, []byte(text), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// SyncConfigFile makes the config file re-read the file now, then settles the tree.
func (h *Host) SyncConfigFile(ctx context.Context) error {
	file, _, err := h.config()
	if err != nil {
		return err
	}
	if err := run(ctx, file.Reload); err != nil {
		return err
	}
	return h.Settle(ctx)
}

// ReadConfigFile returns the config file's text as it is on disk.
func (h *Host) ReadConfigFile() (string, error) {
	_, path, err := h.config()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

// EntriesText is entries as a config file holds them: an indented JSON array.
func EntriesText(entries []loader.EntryOptions) (string, error) {
	if entries == nil {
		entries = []loader.EntryOptions{}
	}
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}
