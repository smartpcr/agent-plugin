package auth

// AfterRead runs f in admit between the session row's read and the peer's
// registration, so a test can revoke there. restore removes it.
func AfterRead(f func()) (restore func()) {
	afterRead = f
	return func() { afterRead = nil }
}
