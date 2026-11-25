//go:build !testhook

package launch

import "testing"

// TestCanaryHookIsInertWithoutTheTag: a build without the testhook tag reports
// the canary it saw, whatever TestHookCanaryEnv says, so a shipped dsh cannot be
// told to lie about its confinement.
func TestCanaryHookIsInertWithoutTheTag(t *testing.T) {
	t.Setenv(TestHookCanaryEnv, string(CanaryWritten))
	for _, c := range []Canary{CanaryRefused, CanaryNotTried} {
		if got := canaryHook(c); got != c {
			t.Errorf("canaryHook(%s) = %s without the testhook tag", c, got)
		}
	}
	if r, err := prove(Plan{Kind: Unconfined}); err != nil || r.Canary != CanaryNotTried {
		t.Errorf("prove(unconfined) = %+v, %v; want canary not-tried", r, err)
	}
}
