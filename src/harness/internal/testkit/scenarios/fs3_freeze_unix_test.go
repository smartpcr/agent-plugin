//go:build scenario && unix

package scenarios

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

// fs3Freeze stops process pid (SIGSTOP) and waits until it is stopped; resume
// continues it (SIGCONT).
func fs3Freeze(pid int) (resume func() error, err error) {
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		return nil, err
	}
	resume = func() error { return syscall.Kill(pid, syscall.SIGCONT) }
	stat := fmt.Sprintf("/proc/%d/stat", pid)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	if _, err := os.Stat(stat); err != nil {
		// No procfs: give the signal a few ticks to land.
		for range 20 {
			<-tick.C
		}
		return resume, nil
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); <-tick.C {
		data, err := os.ReadFile(stat)
		if err != nil {
			return resume, err
		}
		// The state follows the parenthesised command name: "pid (comm) S ...".
		if k := strings.LastIndexByte(string(data), ')'); k >= 0 && k+2 < len(data) && (data[k+2] == 'T' || data[k+2] == 't') {
			return resume, nil
		}
	}
	return resume, fmt.Errorf("process %d did not stop", pid)
}

// fs3LockHeld reports whether another process holds the flock of path, taking
// and releasing it at once when it is free (fsx.Lock on Unix is an exclusive
// flock of the whole file).
func fs3LockHeld(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
