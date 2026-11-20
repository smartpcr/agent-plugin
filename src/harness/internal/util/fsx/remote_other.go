//go:build !windows && !linux && !darwin

package fsx

// IsRemote cannot tell on this platform and answers false.
func IsRemote(string) (bool, error) { return false, nil }
