//go:build !linux && !windows

package subprocess

import (
	"errors"
	"net"
)

// connPeerPid cannot name a peer here without native code; PeerTree reports
// that it cannot answer (execution.md sec. 11).
func connPeerPid(net.Conn) (int, error) {
	return 0, errors.New("the process behind a connection cannot be named on this system")
}
