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

// Handler returns the slog handler config describes writing to w.
func (c Config) Handler(w io.Writer) (slog.Handler, error) {
	var level slog.Level
	switch strings.ToLower(c.Level) {
	case "", "info":
		level = slog.LevelInfo
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("logging: level %q is not debug, info, warn or error", c.Level)
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
var Component = cordis.Define(Name, func(ctx *cordis.Context, config Config) error {
	_, err := ctx.Effect("log exporter", func() (cordis.Disposer, error) {
		w, closeFn, err := config.open()
		if err != nil {
			return nil, err
		}
		handler, err := config.Handler(w)
		if err != nil {
			_ = closeFn()
			return nil, err
		}
		remove := ctx.Loggers().AddExporter(cordis.SlogExporter{Handler: handler})
		return func() error {
			remove()
			return closeFn()
		}, nil
	})
	return err
})
