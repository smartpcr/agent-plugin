package loader

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

const writeAttempts = 10

// ConfigFileOptions configures ConfigFile.
type ConfigFileOptions struct {
	// Path of the file holding the array of entries. Required.
	Path string `json:"path"`
	// Watch reloads the tree when the file changes on disk.
	Watch bool `json:"watch"`
	// Initial seeds a missing file at startup.
	Initial []EntryOptions `json:"initial,omitempty"`
	// PollInterval is how often a watched file is checked; 100ms when zero.
	PollInterval time.Duration `json:"pollInterval,omitempty"`
	// Codec reads and writes the file. Nil picks the codec registered for the file's
	// extension (CodecFor), which is JSON unless another one is registered.
	Codec Codec `json:"-"`
}

// Validate requires a path.
func (o ConfigFileOptions) Validate() error {
	if o.Path == "" {
		return &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Message: "is required", Path: "path"}}}
	}
	return nil
}

// ConfigFileComponent loads the loader's tree from a JSON file and keeps the two in
// sync. It injects the loader.
var ConfigFileComponent = cordis.Construct("ConfigFile", newConfigFile, cordis.WithInject(Key.Name()))

// ConfigFile keeps the loader's tree and a JSON file in sync.
//
// All reads and writes run on one serial loop. File edits and runtime edits are
// merged per entry against the content last synced with the file; if both sides
// changed the same entry differently, the file wins and the conflict is logged.
// Writes go through a temporary file renamed over the config, retried while it is
// locked, and never replace a file that changed since it was last read. A missing
// file is seeded from Initial only at startup; a file that later disappears or stops
// being an array of entries is logged and left alone, and the live tree is kept. The
// file is read through a Codec, so every entry keeps the file, line and column it was
// read from, and an invalid entry is reported at that position.
type ConfigFile struct {
	ctx     *cordis.Context
	options ConfigFileOptions
	loader  *Service
	path    string
	codec   Codec

	mu          sync.Mutex
	drain       chan struct{}
	running     bool
	dirtyRead   bool
	dirtyWrite  bool
	disposed    bool
	tempCounter int

	// accessed by Init, then only by the drain loop
	known       *string
	unmergeable *string
	base        []EntryOptions
	document    *Document
}

func newConfigFile(ctx *cordis.Context, options ConfigFileOptions) (*ConfigFile, error) {
	loader, err := Key.Get(ctx)
	if err != nil {
		return nil, err
	}
	path, err := filepath.Abs(options.Path)
	if err != nil {
		return nil, err
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 100 * time.Millisecond
	}
	codec := options.Codec
	if codec == nil {
		codec = CodecFor(path)
	}
	return &ConfigFile{ctx: ctx, options: options, loader: loader, path: path, codec: codec}, nil
}

// Path returns the absolute path of the file.
func (f *ConfigFile) Path() string { return f.path }

// Init reads (or seeds) the file and applies it, then starts watching.
func (f *ConfigFile) Init() error {
	if _, err := f.ctx.Effect("loader/config-changed", func() (cordis.Disposer, error) {
		remove := f.loader.OnChange(func() { f.schedule(false, true) })
		return func() error { remove(); return nil }, nil
	}); err != nil {
		return err
	}

	text, exists, err := f.tryRead()
	if err != nil {
		return err
	}
	var entries []EntryOptions
	var document *Document
	if exists {
		if entries, document, err = parse(f.codec, f.path, text); err != nil {
			return fmt.Errorf("loader: config file %q must contain an array of entries: %w", f.path, err)
		}
	} else {
		entries = cloneAll(f.options.Initial)
		if text, err = f.render(entries); err != nil {
			return err
		}
		if f.writeAtomic(text, nil) {
			// read the seeded file back, so its entries carry their place in it
			if seeded, seededDocument, err := parse(f.codec, f.path, text); err == nil {
				entries, document = seeded, seededDocument
			}
		}
	}

	assignStableIDs(entries, "", nil)
	publish, err := f.loader.applyDeferred(entries, nil)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.known = &text
	f.base = cloneAll(entries)
	f.document = document
	f.mu.Unlock()
	publish() // after the state is committed, so a listener sees it

	if f.options.Watch {
		return f.watch()
	}
	return nil
}

// Reload re-reads the file now, and returns once it has been merged.
func (f *ConfigFile) Reload() { <-f.schedule(true, false) }

// Save writes the runtime tree now, and returns once it has been written (or merged
// with a concurrent file edit first).
func (f *ConfigFile) Save() { <-f.schedule(false, true) }

// Close flushes pending runtime edits to the file. It runs when the fiber unloads.
func (f *ConfigFile) Close() error {
	f.mu.Lock()
	f.disposed = true
	f.mu.Unlock()
	<-f.schedule(false, true)
	return nil
}

// ------------------------------------------------------------- drain loop

func (f *ConfigFile) schedule(read, write bool) <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.disposed && !write {
		if f.drain == nil {
			return closed
		}
		return f.drain
	}
	f.dirtyRead = f.dirtyRead || read
	f.dirtyWrite = f.dirtyWrite || write
	if !f.running {
		f.running = true
		f.drain = make(chan struct{})
		go f.drainLoop(f.drain)
	}
	return f.drain
}

var closed = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()

func (f *ConfigFile) drainLoop(done chan struct{}) {
	for {
		f.mu.Lock()
		if !f.dirtyRead && !f.dirtyWrite {
			f.running = false
			f.mu.Unlock()
			close(done)
			return
		}
		read := f.dirtyRead
		f.dirtyRead = false
		if !read {
			f.dirtyWrite = false
		}
		f.mu.Unlock()

		var err error
		if read {
			err = f.readStep()
		} else {
			err = f.writeStep()
		}
		if err != nil {
			f.ctx.Logger().Error(err)
		}
	}
}

func (f *ConfigFile) markDirty() {
	f.mu.Lock()
	f.dirtyRead = true
	f.dirtyWrite = true
	f.mu.Unlock()
}

func (f *ConfigFile) state() (known, unmergeable *string, base []EntryOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.known, f.unmergeable, f.base
}

func same(text *string, other string) bool { return text != nil && *text == other }

func (f *ConfigFile) readStep() error {
	text, exists, err := f.tryRead()
	if err != nil {
		return err
	}
	known, unmergeable, base := f.state()
	if !exists {
		f.ctx.Logger().Warnf("config file %q is missing; keeping the current tree", f.path)
		return nil
	}
	if same(known, text) {
		return nil
	}
	theirs, document, err := parse(f.codec, f.path, text)
	if err != nil {
		if same(unmergeable, text) {
			return nil
		}
		f.mu.Lock()
		f.unmergeable = &text
		f.mu.Unlock()
		f.ctx.Logger().Warnf("config file %q is not an array of entries (%v); keeping the current tree", f.path, err)
		return nil
	}

	assignStableIDs(theirs, "", base)
	merged, conflicts := merge(base, f.loader.Snapshot(), theirs)
	for _, id := range conflicts {
		f.ctx.Logger().Errorf("entry %q was changed both in %q and at runtime; the file wins", id, f.path)
	}
	publish, err := f.loader.applyDeferred(merged, nil)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.known = &text
	f.base = cloneAll(theirs)
	f.document = document
	// runtime edits survived the merge: write them out (a write step also reports an
	// encoding failure)
	mergedText, mergedErr := f.renderLocked(merged)
	theirsText, theirsErr := f.renderLocked(theirs)
	if mergedErr != nil || theirsErr != nil || mergedText != theirsText {
		f.dirtyWrite = true
	}
	f.mu.Unlock()
	publish()
	return nil
}

// render encodes entries with the file's codec, against the document last read. An
// encoding failure is returned, never replaced by another format.
func (f *ConfigFile) render(entries []EntryOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renderLocked(entries)
}

func (f *ConfigFile) renderLocked(entries []EntryOptions) (string, error) {
	encoded, err := f.codec.Encode(f.document, entries)
	if err != nil {
		return "", fmt.Errorf("loader: cannot encode config file %q: %w", f.path, err)
	}
	return string(encoded), nil
}

func (f *ConfigFile) writeStep() error {
	entries := f.loader.Snapshot()
	text, err := f.render(entries)
	if err != nil {
		// the file and the synchronized baseline stay as they are; a later save retries
		return err
	}
	known, unmergeable, _ := f.state()
	if same(known, text) {
		return nil
	}
	onDisk, exists, err := f.tryRead()
	if err != nil {
		return err
	}
	if exists && !same(known, onDisk) {
		// a file we cannot parse is left alone; the changes stay in memory until it is fixed
		if same(unmergeable, onDisk) {
			return nil
		}
		// someone else edited the file: merge their edit first, then write
		f.markDirty()
		return nil
	}
	var expected *string
	if exists {
		expected = &onDisk
	}
	if !f.writeAtomic(text, expected) {
		return nil
	}
	return f.adoptWritten(text, entries)
}

// adoptWritten makes text, just written from entries, the synchronized baseline. The
// write may have moved entries in the file, so the text is parsed again: the parsed
// document is what the codec encodes against next, and its positions replace the
// stale ones on the baseline and on every live entry not edited since the snapshot,
// so later issues still name the entry's place in the file.
func (f *ConfigFile) adoptWritten(text string, entries []EntryOptions) error {
	written, document, err := parse(f.codec, f.path, text)
	if err != nil {
		// cannot happen for a codec that reads what it writes; keep the old positions
		f.mu.Lock()
		f.known = &text
		f.base = cloneAll(entries)
		f.mu.Unlock()
		return fmt.Errorf("loader: config file %q cannot be read back after writing it: %w", f.path, err)
	}
	assignStableIDs(written, "", entries)
	// unchanged entries take the re-parsed version; runtime edits made since the
	// snapshot are kept and written by the write step they scheduled
	merged, _ := merge(entries, f.loader.Snapshot(), written)
	f.mu.Lock()
	f.known = &text
	f.base = cloneAll(written)
	f.document = document
	disposed := f.disposed
	f.mu.Unlock()
	if disposed {
		return nil // the final flush: the tree is being unloaded, positions no longer matter
	}
	return f.loader.Apply(merged)
}

// ------------------------------------------------------------ file access

func (f *ConfigFile) tryRead() (string, bool, error) {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// writeAtomic writes text through a temporary file renamed over the config, provided
// the file still holds expected (nil: still missing). It reports whether it wrote.
func (f *ConfigFile) writeAtomic(text string, expected *string) bool {
	f.mu.Lock()
	f.tempCounter++
	counter := f.tempCounter
	f.mu.Unlock()
	temp := filepath.Join(filepath.Dir(f.path), fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(f.path), os.Getpid(), counter))
	defer os.Remove(temp)
	if err := os.WriteFile(temp, []byte(text), 0o644); err != nil {
		f.ctx.Logger().Warnf("cannot write config file %q: %v; changes are kept in memory", f.path, err)
		return false
	}
	for attempt := 1; ; attempt++ {
		current, exists, err := f.tryRead()
		if err == nil && (exists != (expected != nil) || (exists && current != *expected)) {
			f.markDirty()
			return false
		}
		if err == nil {
			if err = os.Rename(temp, f.path); err == nil {
				return true
			}
		}
		if attempt >= writeAttempts {
			f.ctx.Logger().Warnf("cannot write config file %q: %v; changes are kept in memory", f.path, err)
			return false
		}
		time.Sleep(time.Duration(20*attempt) * time.Millisecond)
	}
}

// --------------------------------------------------------------- watching

// watch polls the file and schedules a reload whenever its content changes; polling
// sees editor-style saves (write a temporary file, rename it over the config) and the
// component's own writes alike.
func (f *ConfigFile) watch() error {
	_, err := f.ctx.Effect("loader/watch", func() (cordis.Disposer, error) {
		stop := make(chan struct{})
		done := make(chan struct{})
		last, _ := os.ReadFile(f.path)
		go func() {
			defer close(done)
			ticker := time.NewTicker(f.options.PollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					current, err := os.ReadFile(f.path)
					if err != nil && !errors.Is(err, fs.ErrNotExist) {
						continue // e.g. locked mid-rename on Windows; look again next tick
					}
					if !bytes.Equal(current, last) {
						last = current
						f.schedule(true, false)
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
