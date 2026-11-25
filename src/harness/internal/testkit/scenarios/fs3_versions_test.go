//go:build scenario

package scenarios

import (
	"strings"
	"testing"
	"time"
)

// TestFS3VersionsTrustOnlyDshEdits checks FS-3's version oracle: versions are
// derived from the starting file and dsh's returned edit digests only, so an
// editor save cannot vouch for itself, and a lost edit is excused only by a save
// that wrote back a derived version without its marker.
func TestFS3VersionsTrustOnlyDshEdits(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(1_700_000_000, 0).Add(time.Duration(s) * time.Second) }
	file := func(markers ...string) string {
		return strings.Join(append(markers, fs3Anchor), "\n") + "\n"
	}
	start := file()
	v1, v2 := file("a0000"), file("a0000", "b0000")
	e1 := fs3Ack{what: "edit a0000", marker: "a0000", start: at(1), end: at(2), sum: fs3Sum(v1)}
	e2 := fs3Ack{what: "edit b0000", marker: "b0000", start: at(3), end: at(4), sum: fs3Sum(v2)}
	edits := []fs3Ack{e2, e1} // derivation must not depend on the given order
	invented := file("a0000", "zzz")

	cases := []struct {
		name    string
		edits   []fs3Ack
		saves   []fs3Save
		final   string
		wantErr string
		lost    int
	}{
		{"edits only", edits, nil, v2, "", 0},
		{"save writes back the current version", edits, []fs3Save{{at(5), at(6), fs3Sum(v2)}}, v2, "", 0},
		{"save overwrites an edit with an earlier version", edits, []fs3Save{{at(2), at(5), fs3Sum(v1)}}, v1, "", 1},
		{"invented save digest", edits, []fs3Save{{at(5), at(6), fs3Sum(invented)}}, v2, "no version dsh's edits produced", 0},
		{"invented save cannot excuse a lost edit", edits, []fs3Save{{at(3), at(6), fs3Sum(invented)}}, invented, "no version dsh's edits produced", 0},
		{"lost edit with no save", edits, nil, v1, "the edit was lost", 0},
		{"save ending before the edit began cannot excuse it", edits, []fs3Save{{at(0), at(2), fs3Sum(v1)}}, v1, "the edit was lost", 0},
		{"save of a version written after it ended", edits, []fs3Save{{at(1), at(2), fs3Sum(v2)}}, v2, "first written at", 0},
		{"edit digest no version yields", []fs3Ack{e1, {what: "edit b0000", marker: "b0000", start: at(3), end: at(4), sum: fs3Sum(invented)}}, nil, v1, "invented or torn write", 0},
		{"final file is an earlier version", edits, nil, start, "the edit was lost", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := fs3CheckVersions(start, c.edits, c.saves, []byte(c.final))
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("want accepted, got %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("want an error containing %q, got %v", c.wantErr, err)
			case c.wantErr == "" && v.lost != c.lost:
				t.Fatalf("lost %d, want %d", v.lost, c.lost)
			}
		})
	}
}
