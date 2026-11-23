package main

import (
	"fmt"
	"strings"
	"testing"
)

// profile builds an in-memory text profile of package p: n one-statement blocks in
// p/a.go, of which the blocks in covered have count 1.
func profile(n int, covered func(i int) bool) string {
	var b strings.Builder
	b.WriteString("mode: set\n")
	for i := 0; i < n; i++ {
		count := 0
		if covered(i) {
			count = 1
		}
		fmt.Fprintf(&b, "example.com/m/p/a.go:%d.1,%d.10 1 %d\n", i+1, i+1, count)
	}
	return b.String()
}

func mustParse(t *testing.T, s string) *Profile {
	t.Helper()
	p, err := ParseProfile(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCoverageFloorOverMergedProfiles is the TC-01a merge scenario: the test process
// covers 70% of p, a re-executed child covers more, and only the merge reaches the
// floor.
//
//dsh:test DH-T-TOOLCHAIN-7
func TestCoverageFloorOverMergedProfiles(t *testing.T) {
	parent := mustParse(t, profile(50, func(i int) bool { return i < 35 }))           // 35/50 = 70%
	child := mustParse(t, profile(50, func(i int) bool { return i >= 30 && i < 44 })) // adds 35..43

	merged := Coverage(parent, child)
	if len(merged) != 1 || merged[0].Package != "example.com/m/p" || merged[0].Covered != 44 || merged[0].Total != 50 {
		t.Fatalf("merged coverage = %+v, want p at 44/50 (88%%)", merged)
	}
	if err := CheckFloor(merged, CoverageFloor); err != nil {
		t.Errorf("merged profile fails the floor: %v", err)
	}

	alone := Coverage(parent)
	err := CheckFloor(alone, CoverageFloor)
	if err == nil {
		t.Fatal("parent profile alone passes the floor, want 70% < 85%")
	}
	if !strings.Contains(err.Error(), "example.com/m/p: 70% < 85%") {
		t.Errorf("error = %q, want it to name p as 70%% < 85%%", err)
	}
}

func TestCheckFloorEdges(t *testing.T) {
	tests := []struct {
		name string
		cov  PackageCoverage
		fail string
	}{
		{"exactly the floor", PackageCoverage{"a", 85, 100}, ""},
		{"just under is not rounded up", PackageCoverage{"b", 8499, 10000}, "b: 84.9% < 85%"},
		{"no statements", PackageCoverage{"c", 0, 0}, ""},
		{"untested", PackageCoverage{"d", 0, 3}, "d: 0% < 85%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckFloor([]PackageCoverage{tt.cov}, CoverageFloor)
			switch {
			case tt.fail == "" && err != nil:
				t.Errorf("unexpected failure: %v", err)
			case tt.fail != "" && (err == nil || !strings.Contains(err.Error(), tt.fail)):
				t.Errorf("error = %v, want %q", err, tt.fail)
			}
		})
	}
}

func TestCoverageDeduplicatesAcrossPackagesAndKeepsEachPackage(t *testing.T) {
	// With -coverpkg=./..., the tests of q also profile p: the same block appears in
	// both test processes' sections of the profile.
	src := `mode: set
example.com/m/p/a.go:1.1,1.10 2 0
example.com/m/p/a.go:2.1,2.10 2 1
example.com/m/q/b.go:1.1,1.10 1 1
example.com/m/p/a.go:1.1,1.10 2 1
example.com/m/p/a.go:2.1,2.10 2 0
`
	got := Coverage(mustParse(t, src))
	want := []PackageCoverage{{"example.com/m/p", 4, 4}, {"example.com/m/q", 1, 1}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("coverage = %v, want %v", got, want)
	}
}

func TestParseProfileRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"no colon here", "f.go:1.1,2.2 1", "f.go:1.1-2.2 1 1", "f.go:1.1,2.2 x 1"} {
		if _, err := ParseProfile(strings.NewReader("mode: set\n" + bad + "\n")); err == nil {
			t.Errorf("ParseProfile(%q) succeeded, want an error", bad)
		}
	}
}
