//go:build windows

package local

// Platform is the backend chain of this platform. Windows has no backend in M2
// (M2-C-8, R-05; restricted tokens are S27): every confined policy is refused with
// sandbox/unavailable and only danger-full-access runs, reported Unconfined.
func Platform() []Backend { return nil }

// runtimeDir is empty: Windows has no backend that hides it.
func runtimeDir() string { return "" }
