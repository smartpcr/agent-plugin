/* want `//lint:file-ignore is not allowed` */ //lint:file-ignore errdiscard a whole file is never accepted

// Package lintignore seeds the directive rules of the lintignore analyzer.
package lintignore

func f() error { return nil }

func cases() {
	/* want `//lint:ignore errdiscard has no reason` */ //lint:ignore errdiscard
	_ = f()
	/* want `names unknown check "nosuch"` */ //lint:ignore nosuch some reason
	_ = f()
	/* want `names no check` */ //lint:ignore
	_ = f()
	//lint:ignore errdiscard,SA4006 a reason, so it is accepted
	_ = f()
	_ = f() //lint:ignore U1000 a trailing directive with a reason is accepted too
	//lint:ignored is another word, not a directive
	_ = f()
}
