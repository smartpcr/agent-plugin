package agent

// SetOwnHook runs f inside every owner effect's acquisition until the
// returned restore is called.
func SetOwnHook(f func(Ref)) (restore func()) {
	ownHook = f
	return func() { ownHook = nil }
}
