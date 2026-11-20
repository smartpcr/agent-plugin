//go:build !windows

package fsx

import "os"

// Open opens path for reading. On POSIX a rename over an open file always succeeds,
// so this is os.Open; on Windows it shares delete access.
func Open(path string) (*os.File, error) { return os.Open(path) }
