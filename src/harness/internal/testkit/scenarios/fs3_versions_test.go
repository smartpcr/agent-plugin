//go:build scenario

package scenarios

import (
	"strings"
	"testing"
	"time"
)

// TestFS3VersionsTrustOnlyDshEdits checks FS-3's version oracle: one
// chronological history of the logged edits and editor saves must end in the
// final file, so an editor save cannot vouch for itself, an edit cannot build on
// a version no longer on disk, and a lost edit is excused only by a save that
// wrote back an earlier version after it.
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
	// Evaluator's stale-base history: S; a0000 during [10,20] gives A; b0000 during
	// [30,40] gives B from S; saves during [0,15] and [30,35] write back S; a0001
	// during [50,60] claims A+a0001. After the second edit and both saves only S or
	// B can be on disk, so nothing restores A for the third edit.
	stale := []fs3Ack{
		{what: "edit a0000", marker: "a0000", start: at(10), end: at(20), sum: fs3Sum(v1)},
		{what: "edit b0000", marker: "b0000", start: at(30), end: at(40), sum: fs3Sum(file("b0000"))},
		{what: "edit a0001", marker: "a0001", start: at(50), end: at(60), sum: fs3Sum(file("a0000", "a0001"))},
	}
	staleSaves := []fs3Save{{at(0), at(15), fs3Sum(start)}, {at(30), at(35), fs3Sum(start)}}

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
		{"invented save digest", edits, []fs3Save{{at(5), at(6), fs3Sum(invented)}}, v2, "no chronological history", 0},
		{"invented save cannot excuse a lost edit", edits, []fs3Save{{at(3), at(6), fs3Sum(invented)}}, invented, "no chronological history", 0},
		{"lost edit with no save", edits, nil, v1, "no chronological history", 0},
		{"save ending before the edit began cannot excuse it", edits, []fs3Save{{at(0), at(2), fs3Sum(v1)}}, v1, "no chronological history", 0},
		{"save of a version written after it ended", edits, []fs3Save{{at(1), at(2), fs3Sum(v2)}}, v2, "no chronological history", 0},
		{"edit digest no version yields", []fs3Ack{e1, {what: "edit b0000", marker: "b0000", start: at(3), end: at(4), sum: fs3Sum(invented)}}, nil, v1, "no chronological history", 0},
		{"final file is an earlier version", edits, nil, start, "no chronological history", 0},
		{"save replaced by the next dsh publish", []fs3Ack{e1, {what: "edit b0000", marker: "b0000", start: at(8), end: at(12), sum: fs3Sum(v2)}}, []fs3Save{{at(0), at(10), fs3Sum(start)}}, v2, "", 0},
		{"saves writing back the current version change nothing", []fs3Ack{e1}, []fs3Save{{at(1), at(3), fs3Sum(v1)}, {at(4), at(5), fs3Sum(v1)}}, v1, "", 0},
		{"edit cannot build on a version no longer on disk", stale, staleSaves, file("a0000", "a0001"), "no chronological history", 0},
		{"the same history ending in the reachable version", stale[:2], staleSaves, file("b0000"), "", 1},
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
