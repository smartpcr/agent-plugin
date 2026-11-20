package session

// SetFingerprintWindow sets the ring size of sessions created after the call and
// returns the restore.
func SetFingerprintWindow(n int) func() {
	old := fpWindow
	fpWindow = n
	return func() { fpWindow = old }
}

// HeldFingerprints is how many fingerprints s holds for its deliverer, and the
// ring's size.
func HeldFingerprints(s Session) (held, size int) {
	l := s.(*live)
	l.dmu.Lock()
	defer l.dmu.Unlock()
	return int(l.target - l.fpFrom), len(l.fps)
}
