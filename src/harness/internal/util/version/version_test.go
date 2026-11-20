package version

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"testing"
)

// TestParse is DH-T-SEMVER-1.
func TestParse(t *testing.T) {
	for _, s := range []string{"1.2.3", "0.0.0", "1.2.3-rc.1+build.5", "10.20.30", "1.0.0-alpha.beta.1", "1.2.3+meta"} {
		v, err := Parse(s)
		if err != nil {
			t.Errorf("Parse(%q): %v", s, err)
			continue
		}
		if v.String() != s {
			t.Errorf("Parse(%q).String() = %q", s, v.String())
		}
	}
	for _, s := range []string{"", "v1.2.3", "V1.2.3", "1.2", "1", "01.2.3", "1.02.3", "=1.2.3", "1.2.3-", "1.2.3-01", " 1.2.3", "1.2.3 ", "latest"} {
		_, err := Parse(s)
		var e *Error
		if !errors.As(err, &e) || e.Text != s {
			t.Errorf("Parse(%q) = %v, want an *Error naming it", s, err)
		}
	}
	a, b := MustParse("1.2.3+a"), MustParse("1.2.3+b")
	if a.Compare(b) != 0 || a.Exact(b) || !a.Exact(MustParse("1.2.3+a")) {
		t.Fatal("build metadata must be ignored in order and kept in exact text")
	}
}

func TestText(t *testing.T) {
	var m struct {
		Version Version `json:"version"`
		Host    Range   `json:"host"`
	}
	if err := json.Unmarshal([]byte(`{"version":"1.2.3-rc.1","host":">=0.2.0 <1.0.0"}`), &m); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(m)
	if err != nil || string(out) != `{"version":"1.2.3-rc.1","host":"\u003e=0.2.0 \u003c1.0.0"}` {
		t.Fatalf("round trip = %s, %v", out, err)
	}
	var e *Error
	if err := json.Unmarshal([]byte(`{"version":"v1.2.3"}`), &m); !errors.As(err, &e) {
		t.Fatalf("a loose manifest version was accepted: %v", err)
	}
	if _, err := json.Marshal(struct{ V Version }{}); err == nil {
		t.Fatal("the zero version encoded")
	}
}

// TestOrder is DH-T-SEMVER-2, with the SemVer 2.0.0 specification's precedence
// example (section 11).
func TestOrder(t *testing.T) {
	oldestFirst := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "2.0.0", "2.1.0", "2.1.1"}
	vs := make([]Version, len(oldestFirst))
	for i, s := range oldestFirst {
		vs[i] = MustParse(s)
	}
	r := rand.New(rand.NewPCG(1, 2))
	r.Shuffle(len(vs), func(i, j int) { vs[i], vs[j] = vs[j], vs[i] })
	SortNewestFirst(vs)
	got := make([]string, len(vs))
	for i, v := range vs {
		got[i] = v.String()
	}
	want := slices.Clone(oldestFirst)
	slices.Reverse(want)
	if !slices.Equal(got, want) {
		t.Fatalf("sorted = %v\nwant     %v", got, want)
	}
	for _, s := range oldestFirst {
		if pre := MustParse(s).IsPrerelease(); pre != (s[len("1.0.0"):] != "" && s[5] == '-') {
			t.Errorf("IsPrerelease(%q) = %v", s, pre)
		}
	}
	for _, tc := range []struct {
		current, required string
		want              bool
	}{
		{"1.2.3", "1.2.3", true},
		{"1.3.0", "1.2.3", true},
		{"1.2.2", "1.2.3", false},
		{"1.2.3-rc.1", "1.2.3", false},
		{"1.2.3+b", "1.2.3", true},
	} {
		if got := AtLeast(MustParse(tc.current), MustParse(tc.required)); got != tc.want {
			t.Errorf("AtLeast(%s, %s) = %v", tc.current, tc.required, got)
		}
	}
	if AtLeast(Version{}, MustParse("0.0.0")) || AtLeast(MustParse("1.0.0"), Version{}) {
		t.Fatal("an unreadable requirement counted as satisfied")
	}
}

// TestRange is DH-T-SEMVER-3.
func TestRange(t *testing.T) {
	for _, tc := range []struct {
		rng, v string
		want   bool
	}{
		{">=0.2.0", "0.3.0-rc.1", true},
		{">=0.2.0", "0.1.9", false},
		{"^0.2.3", "0.2.9", true},
		{"^0.2.3", "0.3.0", false},
		{"^1.2.0", "1.9.9", true},
		{"^1.2.0", "2.0.0", false},
		{">=1.0.0 <2.0.0", "1.5.0-rc.1", true},
		{">=1.0.0 <2.0.0", "2.0.0", false},
		{"*", "3.0.0-beta.1", true},
		{"^1.2.0 || ^2.0.0", "2.4.0", true},
		{"^1.2.0 || ^2.0.0", "3.0.0", false},
		{"1.x", "1.9.0", true},
		{"1.x", "2.0.0", false},
		{"~1.2", "1.2.9", true},
		{"~1.2", "1.3.0", false},
		{"1.2 - 1.4.5", "1.4.5", true},
		{"1.2 - 1.4.5", "1.4.6", false},
		{"<=1.0.0", "1.0.0+build", true},
		{"=1.2.3", "1.2.3", true},
	} {
		r, err := ParseRange(tc.rng)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", tc.rng, err)
		}
		if got := r.Satisfies(MustParse(tc.v)); got != tc.want {
			t.Errorf("%q satisfied by %s = %v, want %v", tc.rng, tc.v, got, tc.want)
		}
	}
	for _, s := range []string{"", "   ", ">=1.0.0, <2.0.0", "!=1.0.0", ">>1", "^a.b", "1.2.3 ||| 2"} {
		var e *Error
		if _, err := ParseRange(s); !errors.As(err, &e) {
			t.Errorf("ParseRange(%q) = %v, want an *Error", s, err)
		}
	}
	if (Range{}).Satisfies(MustParse("1.0.0")) || MustRange(t, "*").Satisfies(Version{}) {
		t.Fatal("a zero range or version satisfied")
	}
}

func MustRange(t *testing.T, s string) Range {
	t.Helper()
	r, err := ParseRange(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"1.2.3", "1.2.3-rc.1+b", "v1", "^1.2 || 2.x", "1.2 - 3", "~0.0.1-0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if v, err := Parse(s); err == nil {
			if v.String() != s || v.Compare(v) != 0 || !v.Exact(v) {
				t.Fatalf("Parse(%q) does not round-trip", s)
			}
			back, err := Parse(v.String())
			if err != nil || !back.Exact(v) {
				t.Fatalf("re-parsing %q: %v", s, err)
			}
		}
		if r, err := ParseRange(s); err == nil {
			r.Satisfies(MustParse("1.2.3-rc.1"))
		}
	})
}
