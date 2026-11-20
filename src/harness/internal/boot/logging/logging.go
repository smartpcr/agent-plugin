// Package logging is the harness logging component, boot.logging (DH-D-LOADER-12,
// DH-ADR-LOADER-9): it sends the cordis logger's messages to a log/slog handler on
// standard error or a file, at a configured level and format. The tree is created
// with no console exporter (DH-D-CORDIS-9), so standard output, which carries
// protocol output, never receives a log line (R5).
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Name is the component's catalog name.
const Name = "boot.logging"

// Destination values.
const (
	Stderr = "stderr"
	File   = "file"
)

// Format values.
const (
	Text = "text"
	JSON = "json"
)

// Config is the component's config. The zero value logs info and above as text to
// standard error.
type Config struct {
	// Destination is "stderr" (the default) or "file".
	Destination string `json:"destination,omitempty"`
	// Path is the log file, required with destination "file". It is opened for
	// appending and created with mode 0600, its directory with 0700.
	Path string `json:"path,omitempty"`
	// Level is "debug", "info" (the default), "warn" or "error".
	Level string `json:"level,omitempty"`
	// Format is "text" (the default) or "json".
	Format string `json:"format,omitempty"`
}

// stderr is standard error; tests replace it.
var stderr io.Writer = os.Stderr

// levels returns config's level as a slog level and as the cordis threshold that
// lets its messages through.
func (c Config) levels() (slog.Level, cordis.LogLevel, error) {
	switch strings.ToLower(c.Level) {
	case "", "info":
		return slog.LevelInfo, cordis.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, cordis.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, cordis.LevelWarn, nil
	case "error":
		return slog.LevelError, cordis.LevelError, nil
	default:
		return 0, 0, fmt.Errorf("logging: level %q is not debug, info, warn or error", c.Level)
	}
}

// Handler returns the slog handler config describes writing to w.
func (c Config) Handler(w io.Writer) (slog.Handler, error) {
	level, _, err := c.levels()
	if err != nil {
		return nil, err
	}
	options := &slog.HandlerOptions{Level: level}
	switch strings.ToLower(c.Format) {
	case "", Text:
		return slog.NewTextHandler(w, options), nil
	case JSON:
		return slog.NewJSONHandler(w, options), nil
	default:
		return nil, fmt.Errorf("logging: format %q is not text or json", c.Format)
	}
}

// open returns the writer of the destination and the function that closes it.
func (c Config) open() (io.Writer, func() error, error) {
	switch strings.ToLower(c.Destination) {
	case "", Stderr:
		if c.Path != "" {
			return nil, nil, fmt.Errorf("logging: path %q needs destination %q", c.Path, File)
		}
		return stderr, func() error { return nil }, nil
	case File:
		if c.Path == "" {
			return nil, nil, fmt.Errorf("logging: destination %q needs a path", File)
		}
		if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
			return nil, nil, fmt.Errorf("logging: %w", err)
		}
		f, err := os.OpenFile(c.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("logging: %w", err)
		}
		return f, f.Close, nil
	default:
		return nil, nil, fmt.Errorf("logging: destination %q is not stderr or file", c.Destination)
	}
}

// Component is boot.logging. On load it opens the destination and adds one
// SlogExporter to the tree's logger service, both as an effect of its fiber; on
// unload the effect removes the exporter and closes the file. It neither clears nor
// restores other exporters. A destination that cannot be opened fails the load.
//
// The tree's default threshold drops a message before any exporter sees it, so a
// level more verbose than the current threshold (debug, under the default info)
// raises the threshold to it while the component is loaded. It never lowers the
// threshold, which would silence another exporter. The loaded components of one
// tree share the threshold: it is the most verbose any of them needs, so unloading
// one keeps the level another still needs, and once the last is unloaded the
// threshold goes back to what it was, unless someone has changed it since, which
// is kept. A context's own logger
// intercept (cordis.LoggerConfig) still decides for the loggers below it.
var Component = cordis.Define(Name, func(ctx *cordis.Context, config Config) error {
	_, err := ctx.Effect("log exporter", func() (cordis.Disposer, error) {
		_, threshold, err := config.levels()
		if err != nil {
			return nil, err
		}
		w, closeFn, err := config.open()
		if err != nil {
			return nil, err
		}
		handler, err := config.Handler(w)
		if err != nil {
			_ = closeFn()
			return nil, err
		}
		loggers := ctx.Loggers()
		remove := loggers.AddExporter(cordis.SlogExporter{Handler: handler})
		release := require(loggers, threshold)
		return func() error {
			release()
			remove()
			return closeFn()
		}, nil
	})
	return err
})

// thresholds coordinates the threshold of each logger service among the loaded
// logging components, so that unloading one never silences another that is still
// loaded, whatever the order.
var thresholds = struct {
	sync.Mutex
	services map[*cordis.LoggerService]*demand
}{services: map[*cordis.LoggerService]*demand{}}

// demand is what the loaded components of one logger service need. base is the
// threshold the service would have without them: the one found when the first was
// loaded, or the one someone else has set since (set records the last threshold
// the components set, so a different current threshold is someone else's).
type demand struct {
	base, set cordis.LogLevel
	wants     map[*int]cordis.LogLevel
}

// require adds a component's need for threshold on loggers and returns the
// function that withdraws it. Each change sets the threshold to the most verbose of
// base and the remaining needs.
func require(loggers *cordis.LoggerService, threshold cordis.LogLevel) func() {
	thresholds.Lock()
	defer thresholds.Unlock()
	d := thresholds.services[loggers]
	if d == nil {
		d = &demand{base: loggers.Level(), set: loggers.Level(), wants: map[*int]cordis.LogLevel{}}
		thresholds.services[loggers] = d
	}
	key := new(int)
	d.wants[key] = threshold
	d.apply(loggers)
	return func() {
		thresholds.Lock()
		defer thresholds.Unlock()
		delete(d.wants, key)
		d.apply(loggers)
		if len(d.wants) == 0 {
			delete(thresholds.services, loggers)
		}
	}
}

func (d *demand) apply(loggers *cordis.LoggerService) {
	if current := loggers.Level(); current != d.set {
		d.base = current
	}
	target := d.base
	for _, want := range d.wants {
		target = max(target, want)
	}
	if target != loggers.Level() {
		loggers.SetLevel(target)
	}
	d.set = target
}
