//go:build !linux && !darwin && !windows

package cli

// hideEcho reports no terminal: on this platform standard input is read as a pipe.
func hideEcho(uintptr) (restore func() error, ok bool, err error) { return nil, false, nil }
