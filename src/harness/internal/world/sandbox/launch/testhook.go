//go:build testhook

package launch

import "os"

// A testhook build lets TestHookCanaryEnv force the reported canary result to
// written (TC-04b). No release build carries the tag.
func init() {
	canaryHook = func(c Canary) Canary {
		if os.Getenv(TestHookCanaryEnv) == string(CanaryWritten) {
			return CanaryWritten
		}
		return c
	}
}
