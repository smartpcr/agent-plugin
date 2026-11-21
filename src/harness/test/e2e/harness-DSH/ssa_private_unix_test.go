//go:build e2e && !windows

package e2e

import "errors"

// ssaAssertOwnerOnly is the Windows DACL check; Unix asserts mode bits instead.
func ssaAssertOwnerOnly(path string) error {
	return errors.New(path + ": ssaAssertOwnerOnly is Windows-only")
}
