//go:build unix && !linux

package subprocesstest

// zombie is false where no /proc tells a zombie apart: Exited is then !Alive,
// which waits for the reaping.
func zombie(int) bool { return false }
