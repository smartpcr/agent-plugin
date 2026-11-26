//go:build e2e

package listen

import "net"

// E2ESpoofSource makes every connection the listeners started after it accept
// report addr as its remote address, so an e2e scenario on 127.0.0.1 can present
// a source from another machine without binding a non-loopback address. restore
// puts the listener back. Only the e2e build (-tags e2e) has it; the package's own
// tests use SpoofSource (export_test.go).
func E2ESpoofSource(addr *net.TCPAddr) (restore func()) { return spoofSource(addr) }
