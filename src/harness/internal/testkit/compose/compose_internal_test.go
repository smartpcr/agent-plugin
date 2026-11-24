package compose

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
)

// definition: "" and "core" are core alone; a built-in profile brings its own
// name, bundles and limits; a shipped bundle is stacked on core; anything else is
// refused, naming both lists.
func TestDefinitionResolvesBundlesAndProfiles(t *testing.T) {
	for _, tc := range []struct {
		bundle, name string
		bundles      []string
	}{
		{"", "compose", []string{"core"}},
		{"core", "compose", []string{"core"}},
		{profile.Headless, profile.Headless, []string{"core", "headless"}},
		{profile.SDKMinimal, profile.SDKMinimal, []string{"core", "minimal", "sdk"}},
		{"minimal", "compose", []string{"core", "minimal"}},
	} {
		def, err := definition(tc.bundle)
		if err != nil || def.Name != tc.name || !slices.Equal(def.Bundles, tc.bundles) {
			t.Errorf("definition(%q) = %s %v, %v; want %s %v", tc.bundle, def.Name, def.Bundles, err, tc.name, tc.bundles)
		}
		if def.Reload != 0 {
			t.Errorf("definition(%q) reloads every %s; a test tree watches no layer", tc.bundle, def.Reload)
		}
		if def.Limits.ShutdownTimeout == 0 {
			t.Errorf("definition(%q) has no shutdown bound", tc.bundle)
		}
	}
	if _, err := definition("no-such"); err == nil || !strings.Contains(err.Error(), `"no-such" is neither a shipped bundle`) {
		t.Fatalf("an unknown bundle: %v", err)
	}
}

// noUserManager refuses every scope, so a composed tree on Linux takes the
// process-group rung instead of asking the developer's systemd.
func TestNoUserManagerRefusesEveryScope(t *testing.T) {
	if err := (noUserManager{}).DelegateScope(context.Background(), "dsh-test.scope", 1); !errors.Is(err, errNoUserManager) {
		t.Fatalf("DelegateScope = %v, want errNoUserManager", err)
	}
}

// The leak rule's parser: a header gives the id and, inside a bubble, the
// bubble; a goroutine outside any bubble has none; a block that is not a
// goroutine is skipped.
func TestParseReadsGoroutineHeaders(t *testing.T) {
	g, ok := parse("goroutine 42 [chan receive (durable), synctest bubble 7]:\nmain.f()")
	if !ok || g.id != 42 || g.bubble != "7" || !strings.Contains(g.stack, "main.f()") {
		t.Fatalf("a bubbled goroutine parsed as %+v, %v", g, ok)
	}
	if g, ok := parse("goroutine 3 [select]:\nmain.g()"); !ok || g.id != 3 || g.bubble != "" {
		t.Fatalf("an unbubbled goroutine parsed as %+v, %v", g, ok)
	}
	if _, ok := parse("created by main.h in goroutine 1"); ok {
		t.Fatal("a block without a header parsed")
	}
}

// dumpFrom grows its buffer until the dump fits, so a dump larger than the
// first buffer is whole.
func TestDumpGrowsItsBuffer(t *testing.T) {
	small := dumpFrom(false, 16)
	if !strings.HasPrefix(small, "goroutine ") || !strings.Contains(small, "TestDumpGrowsItsBuffer") {
		t.Fatalf("a dump from a 16-byte buffer was cut: %q", small)
	}
}

// current and goroutines see the bubble: inside one, the caller's bubble is
// named and the dump lists the caller in it; outside, there is no bubble.
func TestCurrentNamesTheBubble(t *testing.T) {
	if _, b := current(); b != "" {
		t.Fatalf("outside a bubble current() named bubble %q", b)
	}
	synctest.Test(t, func(t *testing.T) {
		self, bubble := current()
		if bubble == "" {
			t.Fatal("inside a bubble current() named none")
		}
		found := false
		for _, g := range goroutines() {
			if g.id == self {
				found = g.bubble == bubble
			}
		}
		if !found {
			t.Fatalf("goroutines() does not list the caller %d in bubble %s", self, bubble)
		}
	})
}
