//go:build e2e && !windows

package e2e

import (
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// sftSpawnRecorder is the subprocess fake on Unix: every child the test process
// starts, by any path, sends SIGCHLD when it exits; one still running is listed
// in /proc/self/task/*/children. Signals may coalesce, so a count is a lower
// bound on spawns -- zero means none.
type sftSpawnRecorder struct {
	ch    chan os.Signal
	total int
}

func sftNewSpawnRecorder() (*sftSpawnRecorder, error) {
	r := &sftSpawnRecorder{ch: make(chan os.Signal, 1024)}
	signal.Notify(r.ch, syscall.SIGCHLD)
	return r, nil
}

func (r *sftSpawnRecorder) Count() (int, error) {
	// Let the runtime deliver a signal raised by a child that just exited.
	time.Sleep(250 * time.Millisecond)
	for {
		select {
		case <-r.ch:
			r.total++
			continue
		default:
		}
		break
	}
	live := 0
	tasks, _ := filepath.Glob("/proc/self/task/*/children")
	for _, t := range tasks {
		if b, err := os.ReadFile(t); err == nil {
			live += len(strings.Fields(string(b)))
		}
	}
	return r.total + live, nil
}

func (r *sftSpawnRecorder) Close() { signal.Stop(r.ch) }
