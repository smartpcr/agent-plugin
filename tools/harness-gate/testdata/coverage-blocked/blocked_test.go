package blocked

import "testing"

// TestBlocked never returns: nothing closes release.
func TestBlocked(t *testing.T) {
	Wait(make(chan struct{}))
}
