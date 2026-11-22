// Package typeassert seeds the typeassert analyzer.
package typeassert

import (
	"fmt"
	"unsafe" // want `unsafe imported`
)

var size = unsafe.Sizeof(0)

func use(int) {}

func cases(x any) {
	v := x.(int) // want `unchecked type assertion to int`
	_ = v
	_ = x.(fmt.Stringer).String() // want `unchecked type assertion to fmt.Stringer`
	use(x.(int))                  // want `unchecked type assertion to int`
	var e = x.(error)             // want `unchecked type assertion to error`
	_ = e

	// Checked: the comma-ok forms and type switches.
	w, ok := x.(string)
	_, _ = w, ok
	w, ok = x.(string)
	var y, ok2 = x.(error)
	_, _ = y, ok2
	p, ok3 := (x.(*int))
	_, _ = p, ok3
	switch x.(type) {
	case int:
	}
	switch t := x.(type) {
	default:
		_ = t
	}

	//lint:ignore typeassert the fixture's reasoned exception
	z := x.(int)
	_ = z
}
