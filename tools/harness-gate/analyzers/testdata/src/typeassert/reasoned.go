package typeassert

import (
	//lint:ignore typeassert the fixture's reason: a Windows API needs the pointer
	"unsafe"
)

var ptr = unsafe.Pointer(nil)
