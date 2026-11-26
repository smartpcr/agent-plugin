package listen

import "net"

// SpoofSource makes every connection the listeners started after it accept report
// addr as its remote address, so a test on loopback can be a source from another
// machine. restore puts the listener back.
func SpoofSource(addr *net.TCPAddr) (restore func()) { return spoofSource(addr) }

// UseTrees makes every listener Component starts after it ask t for the tree of a
// local peer. restore puts the boot tree's registry back.
func UseTrees(t Trees) (restore func()) {
	treesHook = t
	return func() { treesHook = nil }
}

// UseOwnerExact pins whether this machine's tree owner is exact for the listeners
// started after it, so a test can run as on a job-object or control-group host
// (exact) or a process-group fallback host (best effort). restore unpins it.
func UseOwnerExact(exact bool) (restore func()) {
	prev := ownerExact
	ownerExact = func() bool { return exact }
	return func() { ownerExact = prev }
}
