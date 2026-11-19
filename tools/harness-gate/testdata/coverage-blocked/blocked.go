// Package blocked seeds a test that never returns, for the coverage gate's timeout.
package blocked

// Wait blocks until release is closed.
func Wait(release <-chan struct{}) { <-release }
