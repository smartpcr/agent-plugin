//go:build !windows && !linux

package local

// Platform is the backend chain of this platform, in selection order. Only Linux
// has backends (bubblewrap, then Landlock; DH-D-SANDBOX-4): elsewhere the chain
// is empty and a confined policy is refused with sandbox/unavailable, never run
// unconfined.
func Platform() []Backend { return nil }

// runtimeDir is empty: only the Linux bubblewrap backend hides it.
func runtimeDir() string { return "" }
