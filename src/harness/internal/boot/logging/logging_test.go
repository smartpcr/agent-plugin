package logging

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
)

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
	root := testkit.Tree(t)
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

// DH-ADR-LOADER-9: a configured debug level reaches the destination through the
// ordinary exporter-free tree, and unloading puts the tree's threshold back.
func TestDebugLevelReachesTheDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.log")
	root := testkit.Tree(t)
	f, err := load(t, root, Config{Destination: File, Path: path, Level: "debug", Format: JSON})
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Loggers().Level(); got != cordis.LevelDebug {
		t.Fatalf("threshold while loaded = %v, want Debug", got)
	}
	root.Logger().Debugf("detail %d", 7)
	f.Dispose()
	if got := root.Loggers().Level(); got != cordis.LevelInfo {
		t.Fatalf("threshold after unload = %v, want Info", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil || record["msg"] != "detail 7" || record["level"] != "DEBUG" {
		t.Fatalf("file = %q (%v), want one DEBUG record", data, err)
	}

	// A threshold someone else set is neither lowered nor overwritten on unload.
	root.Loggers().SetLevel(cordis.LevelDebug)
	f, err = load(t, root, Config{Destination: File, Path: path, Level: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Loggers().Level(); got != cordis.LevelDebug {
		t.Fatalf("an error-level exporter lowered the threshold to %v", got)
	}
	f.Dispose()
	root.Loggers().SetLevel(cordis.LevelInfo)
	f, err = load(t, root, Config{Destination: File, Path: path, Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	root.Loggers().SetLevel(cordis.LevelWarn)
	f.Dispose()
	if got := root.Loggers().Level(); got != cordis.LevelWarn {
		t.Fatalf("unload overwrote a threshold changed since: %v", got)
	}
}

// Two loaded debug loggers share the threshold: unloading either first leaves the
// other receiving debug records, and unloading both restores the tree's threshold.
func TestOverlappingDebugLoggers(t *testing.T) {
	for _, firstOut := range []int{0, 1} {
		dir := t.TempDir()
		paths := []string{filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log")}
		root := testkit.Tree(t)
		var fibers []*cordis.Fiber
		for _, path := range paths {
			f, err := load(t, root, Config{Destination: File, Path: path, Level: "debug"})
			if err != nil {
				t.Fatal(err)
			}
			fibers = append(fibers, f)
		}
		// A warn-level logger in between neither lowers nor holds the threshold.
		warn, err := load(t, root, Config{Destination: File, Path: filepath.Join(dir, "w.log"), Level: "warn"})
		if err != nil {
			t.Fatal(err)
		}
		warn.Dispose()
		fibers[firstOut].Dispose()
		if got := root.Loggers().Level(); got != cordis.LevelDebug {
			t.Fatalf("order %d: threshold with one debug logger left = %v", firstOut, got)
		}
		root.Logger().Debugf("still here")
		fibers[1-firstOut].Dispose()
		if got := root.Loggers().Level(); got != cordis.LevelInfo {
			t.Fatalf("order %d: threshold after both unloaded = %v, want Info", firstOut, got)
		}
		data, err := os.ReadFile(paths[1-firstOut])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "still here") {
			t.Fatalf("order %d: remaining logger lost its debug record: %q", firstOut, data)
		}
		gone, err := os.ReadFile(paths[firstOut])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(gone), "still here") {
			t.Fatalf("order %d: unloaded logger still received records", firstOut)
		}
	}
	thresholds.Lock()
	defer thresholds.Unlock()
	if len(thresholds.services) != 0 {
		t.Fatalf("%d logger services still tracked", len(thresholds.services))
	}
}

func TestStderrDestination(t *testing.T) {
	buf := &testkit.Buffer{}
	saved := stderr
	stderr = buf
	t.Cleanup(func() { stderr = saved })

	root := testkit.Tree(t)
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
		root := testkit.Tree(t)
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
