//go:build testhook

package launch

import (
	"context"
	"fmt"
	"os"
)

// TestOpened is the line a testhook build's opener writes on standard error in
// place of starting a browser, so a built-binary test counts the opens. The link
// is never written.
const TestOpened = "testkit-opener: opened"

func init() {
	optionsHook = func(o *Options) {
		o.Opener = func(context.Context, string) error {
			_, err := fmt.Fprintln(os.Stderr, TestOpened)
			return err
		}
	}
}
