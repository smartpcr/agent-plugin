package launch_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/host/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// warnings collects what a host reports through Options.Warn.
type warnings struct {
	mu   sync.Mutex
	msgs []string
}

func (w *warnings) warn(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, fmt.Sprintf(format, args...))
}

func (w *warnings) all() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.msgs)
}

// Start refuses a missing run directory, a run directory that is a file and a
// stale control socket it cannot remove; the last releases the record's lock, so
// a later Start on a clean directory succeeds.
func TestStartRefusals(t *testing.T) {
	const origin = "http://127.0.0.1:1"
	if _, err := launch.Start(bg, launch.Options{Origin: origin}); herr.Kind(err) != launch.ReasonConfig {
		t.Fatalf("Start without a run directory = %v, want %s", err, launch.ReasonConfig)
	}

	file := filepath.Join(t.TempDir(), "run")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if h, err := launch.Start(bg, launch.Options{Config: launch.Config{RunDir: file}, Origin: origin}); err == nil {
		_ = h.Close()
		t.Fatal("Start on a run directory that is a file succeeded")
	}

	run := temphome.New(t).Run()
	sock := launch.PathsFor(run, origin).Socket
	if err := os.MkdirAll(sock, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sock, "held"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if h, err := launch.Start(bg, launch.Options{Config: launch.Config{RunDir: run}, Origin: origin}); herr.Kind(err) != launch.ReasonIO {
		if h != nil {
			_ = h.Close()
		}
		t.Fatalf("Start over an unremovable stale socket = %v, want %s", err, launch.ReasonIO)
	}
	if err := os.RemoveAll(sock); err != nil {
		t.Fatal(err)
	}
	start(t, run, launch.Options{Origin: origin})
}

// A link that cannot be minted is warned about at announcement; nothing is
// printed or opened.
func TestAnnounceMintFails(t *testing.T) {
	w := &warnings{}
	term := &syncBuf{}
	op := &opener{}
	start(t, temphome.New(t).Run(), launch.Options{Terminal: term, Opener: op.open, Warn: w.warn,
		Getenv: func(k string) string { return map[string]string{"DISPLAY": ":0"}[k] },
		Mint:   func() (string, error) { return "", errors.New("no key") }})
	eventually(t, "the mint warning", func() bool { return len(w.all()) == 1 })
	if msg := w.all()[0]; !strings.Contains(msg, "mint") {
		t.Fatalf("warning = %q, want the mint failure", msg)
	}
	if term.String() != "" || op.count() != 0 {
		t.Fatalf("a link was announced: terminal %q, opened %d", term.String(), op.count())
	}
}

// launch.open refuses once the host is closed, passes a mint failure through,
// and with no terminal fails when the browser cannot be opened.
func TestOpenRefusals(t *testing.T) {
	closed := start(t, temphome.New(t).Run(), launch.Options{})
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Open(false); herr.Kind(err) != launch.ReasonNotReady {
		t.Fatalf("Open after Close = %v, want %s", err, launch.ReasonNotReady)
	}

	display := func(k string) string { return map[string]string{"DISPLAY": ":0"}[k] }
	noKey := errors.New("no key")
	unminted := start(t, temphome.New(t).Run(), launch.Options{Terminal: &syncBuf{}, Getenv: display,
		Mint: func() (string, error) { return "", noKey }})
	if how, err := unminted.Open(false); !errors.Is(err, noKey) || how != "" {
		t.Fatalf("Open with a failing mint = %q %v, want %v", how, err, noKey)
	}

	op := &opener{err: herr.New("open/failed", "no browser")}
	blind := start(t, temphome.New(t).Run(), launch.Options{Getenv: display, Opener: op.open})
	eventually(t, "the announcement's open", func() bool { return op.count() == 1 })
	if how, err := blind.Open(false); herr.Kind(err) != launch.ReasonNoChannel || how != "" {
		t.Fatalf("Open with a failing browser and no terminal = %q %v, want %s", how, err, launch.ReasonNoChannel)
	}
	if op.count() != 2 {
		t.Fatalf("browser opened %d times, want 2", op.count())
	}
}

// The control methods refuse parameters that do not decode, and launch.open
// carries the host's refusal back to the caller.
func TestMethodsRefuse(t *testing.T) {
	h := start(t, temphome.New(t).Run(), launch.Options{Ready: make(chan struct{})})
	sock := h.Paths().Socket
	for _, c := range []struct{ method, params, want string }{
		{launch.MethodStop, `{"ask":"yes"}`, "invalid"},
		{launch.MethodOpen, `{"print":"yes"}`, "invalid"},
		{launch.MethodOpen, `{"print":true}`, string(launch.ReasonNotReady)},
	} {
		reply := call(t, sock, c.method, c.params)
		if _, ok := reply["result"]; ok || !strings.Contains(string(reply["error"]), c.want) {
			t.Errorf("%s %s = %v, want an error with %q", c.method, c.params, reply, c.want)
		}
	}
}

// The launcher argv is the platform's own launcher with the URL as its one
// argument; elsewhere it is xdg-open from PATH, and without one there is no argv.
func TestBrowserArgv(t *testing.T) {
	const url = "http://127.0.0.1:1/#t=x"
	root := filepath.Join(string(filepath.Separator)+"win", "dir")
	got, err := launch.BrowserArgv("windows", url, subprocess.Env{"SYSTEMROOT=" + root})
	if want := []string{filepath.Join(root, "System32", "rundll32.exe"), "url.dll,FileProtocolHandler", url}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("windows argv = %q %v, want %q", got, err, want)
	}
	if got, err := launch.BrowserArgv("darwin", url, nil); err != nil || !slices.Equal(got, []string{"/usr/bin/open", url}) {
		t.Fatalf("darwin argv = %q %v", got, err)
	}
	empty := t.TempDir()
	if got, err := launch.BrowserArgv("linux", url, subprocess.Env{"PATH=" + empty}); err == nil {
		t.Fatalf("linux argv without xdg-open = %q, want an error", got)
	}
	if runtime.GOOS == "windows" {
		return // an extensionless xdg-open is not a program there
	}
	xdg := filepath.Join(empty, "xdg-open")
	if err := os.WriteFile(xdg, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := launch.BrowserArgv("linux", url, subprocess.Env{"PATH=" + empty}); err != nil || !slices.Equal(got, []string{xdg, url}) {
		t.Fatalf("linux argv = %q %v, want %q", got, err, []string{xdg, url})
	}
}

// Off Windows and macOS the system opener needs xdg-open; without one on PATH it
// fails before starting anything.
func TestOpenBrowserWithoutLauncher(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("the system launcher there is always present and would open a real browser")
	}
	t.Setenv("PATH", t.TempDir())
	if err := launch.OpenBrowser(context.Background(), "http://127.0.0.1:1/#t=x"); err == nil {
		t.Fatal("OpenBrowser without xdg-open succeeded")
	}
}

// Only a character device is a print channel: no file and a regular file are not.
func TestTerminal(t *testing.T) {
	if launch.Terminal(nil) != nil {
		t.Fatal("a nil file is a terminal")
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if launch.Terminal(f) != nil {
		t.Fatal("a regular file is a terminal")
	}
}
