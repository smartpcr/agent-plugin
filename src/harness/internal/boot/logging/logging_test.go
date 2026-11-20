package logging

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func load(t *testing.T, root *cordis.Context, config Config) (*cordis.Fiber, error) {
	t.Helper()
	f, err := root.Use(Component, config)
	if err != nil {
		t.Fatal(err)
	}
	return f, f.Wait()
}

// DH-T-LOADER-13: with the component loaded and a pipe as standard output, an info
// message reaches the configured destination and the pipe stays empty; after unload
// the exporter is gone and the file is closed.
func TestFileDestinationKeepsStdoutClean(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	path := filepath.Join(t.TempDir(), "logs", "dsh.log")
	root := cordis.New(cordis.WithExporters())
	f, err := load(t, root, Config{Destination: File, Path: path, Format: JSON})
	if err != nil {
		t.Fatal(err)
	}
	root.Logger().Infof("hello %s", "world")
	root.Logger().Debugf("not at info")
	f.Dispose()
	root.Logger().Infof("after unload")

	os.Stdout = stdout
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	piped, _ := io.ReadAll(r)
	if len(piped) != 0 {
		t.Fatalf("standard output received %q", piped)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("file has %d lines: %q", len(lines), data)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatal(err)
	}
	if record["msg"] != "hello world" || record["level"] != "INFO" {
		t.Fatalf("record %v", record)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator == '/' && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	// The file is closed: on every platform it can now be removed.
	if err := os.Remove(path); err != nil {
		t.Fatalf("file still open: %v", err)
	}
}

func TestStderrDestination(t *testing.T) {
	buf := &syncBuffer{}
	saved := stderr
	stderr = buf
	t.Cleanup(func() { stderr = saved })

	root := cordis.New(cordis.WithExporters())
	f, err := load(t, root, Config{Level: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	root.Logger().Infof("quiet")
	root.Logger().Warnf("loud")
	f.Dispose()
	root.Logger().Warnf("gone")
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "msg=loud") || strings.Contains(out, "quiet") || strings.Contains(out, "gone") {
		t.Fatalf("stderr got %q", out)
	}
}

func TestBadConfigFailsLoad(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		config Config
		want   string
	}{
		{Config{Destination: "syslog"}, "destination"},
		{Config{Destination: File}, "needs a path"},
		{Config{Path: "x.log"}, "needs destination"},
		{Config{Level: "trace"}, "level"},
		{Config{Format: "xml"}, "format"},
		{Config{Destination: File, Path: filepath.Join(dir, "x.log"), Format: "xml"}, "format"},
		{Config{Destination: File, Path: filepath.Join(blocker, "x.log")}, "logging"},
		{Config{Destination: File, Path: dir}, "logging"},
	}
	for _, c := range cases {
		root := cordis.New(cordis.WithExporters())
		f, err := load(t, root, c.config)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%+v: got %v, want %q", c.config, err, c.want)
		}
		f.Dispose()
	}
	// The failed file load left no file open: the directory can be removed.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
}

func TestHandlerLevels(t *testing.T) {
	for _, level := range []string{"debug", "INFO", "warn", "error", ""} {
		if _, err := (Config{Level: level}).Handler(io.Discard); err != nil {
			t.Fatalf("%s: %v", level, err)
		}
	}
}
