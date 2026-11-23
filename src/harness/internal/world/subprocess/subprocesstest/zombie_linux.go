//go:build linux

package subprocesstest

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strconv"
)

// zombie reports whether the process with pid has exited and waits to be
// reaped: its state in /proc/<pid>/stat is Z (zombie) or X (dead). A pid whose
// entry is already gone was reaped meanwhile and has exited too.
func zombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	// The state follows the command name, which is in parentheses and may itself
	// hold spaces and parentheses.
	i := bytes.LastIndexByte(b, ')')
	return i >= 0 && i+2 < len(b) && (b[i+2] == 'Z' || b[i+2] == 'X')
}
