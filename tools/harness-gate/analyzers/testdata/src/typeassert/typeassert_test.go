package typeassert

import (
	"testing"
	"unsafe" // want `unsafe imported`
)

// TestAssert: an unchecked assertion in a test file is not reported; an unsafe
// import is.
func TestAssert(t *testing.T) {
	var x any = 1
	_ = x.(int)
	_ = unsafe.Sizeof(x)
}
