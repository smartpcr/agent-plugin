package cordis

import (
	"bytes"
	"runtime"
	"strconv"
)

// goroutineID returns the calling goroutine's id, which attributes a lifecycle wait to
// the notification delivery running on that goroutine. Go exposes the id only in the
// header of a goroutine's stack trace, "goroutine N [status]:", so it is read from
// there. ok is false when the header does not have that form, so a malformed header
// never yields an identity, least of all one shared by several goroutines. It is a
// variable so tests can make attribution fail.
var goroutineID = func() (id uint64, ok bool) {
	var buf [64]byte
	return parseGoroutineID(buf[:runtime.Stack(buf[:], false)])
}

// parseGoroutineID reads N from a stack trace header "goroutine N [...". N must be a
// positive decimal integer: the runtime never assigns id 0 to a user goroutine.
func parseGoroutineID(header []byte) (uint64, bool) {
	rest, ok := bytes.CutPrefix(header, []byte("goroutine "))
	if !ok {
		return 0, false
	}
	digits, _, ok := bytes.Cut(rest, []byte(" ["))
	if !ok || len(digits) == 0 || digits[0] < '0' || digits[0] > '9' {
		return 0, false
	}
	id, err := strconv.ParseUint(string(digits), 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}
