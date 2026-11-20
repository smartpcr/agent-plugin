//go:build replay

package x

import "testing"

func TestReplayAlwaysFails(t *testing.T) {
	t.Fatal("a replay test ran in the default gate")
}
