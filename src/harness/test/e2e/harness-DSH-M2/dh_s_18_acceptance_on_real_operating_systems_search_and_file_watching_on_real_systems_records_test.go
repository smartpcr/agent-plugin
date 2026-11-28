//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

// A committed record stands in for a variant only when it is a passing run of
// exactly these sources; anything else leaves the variant failing.
func TestS87RecordOnlyStandsInForAPassingRunOfTheseSources(t *testing.T) {
	s87OuterT = t
	sum, err := s87Sources("linux", runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"DH-E-CHOKIDAR-1", "DH-E-CHOKIDAR-2"}
	tests := []string{"TestWatchRealEditorSaves", "TestWatchRemoteFilesystemsPoll"}
	good := func() s87Record {
		return s87Record{Kind: s87RecordKind, IDs: ids, Tests: tests, OS: "linux", Arch: runtime.GOARCH, Sources: sum,
			Rows: map[string]map[string]any{ids[0]: {"result": "passed", "os": "linux"}, ids[1]: {"result": "passed", "os": "linux"}},
			Log:  "--- PASS: " + tests[0] + " (1s)\n--- PASS: " + tests[1] + " (1s)\n", Volumes: []string{"cifs"}, Remote: []string{"smb"}}
	}
	cause := errors.New("no root for mount")
	for _, c := range []struct {
		name, want string
		edit       func(r *s87Record)
	}{
		{"stale sources", "it is stale", func(r *s87Record) { r.Sources = strings.Repeat("0", 64) }},
		{"non-zero exit", "exited 1", func(r *s87Record) { r.Code = 1 }},
		{"failed row", "row is", func(r *s87Record) { r.Rows[ids[1]]["result"] = "failed" }},
		{"no pass in log", "no pass of " + tests[1], func(r *s87Record) { r.Log = "--- PASS: " + tests[0] + " (1s)\n" }},
		{"other scenarios", "not [DH-E-CHOKIDAR-1", func(r *s87Record) { r.IDs = []string{"DH-E-RIPGREP-1", "DH-E-RIPGREP-2"} }},
		{"other OS", "records windows", func(r *s87Record) { r.OS = "windows" }},
		{"missing", "DSH_S18_RECORD=1", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			s87RecordsDir = t.TempDir()
			t.Cleanup(func() { s87RecordsDir = "" })
			s := &s87State{ids: ids, tests: tests}
			if c.edit != nil {
				r := good()
				c.edit(&r)
				b, _ := json.Marshal(r)
				p, _ := s.recordPath("linux")
				if err := os.WriteFile(p, b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			l := &s87Leg{v: &s87Variant{goos: "linux"}, unreached: cause, ran: cause}
			err := s.fromRecord(l, cause)
			if err == nil || !errors.Is(err, cause) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("fromRecord = %v, want the cause and %q", err, c.want)
			}
			if l.recorded != nil || l.ran == nil {
				t.Fatalf("the leg was verified from a bad record: recorded %v, ran %v", l.recorded, l.ran)
			}
		})
	}
	t.Run("valid", func(t *testing.T) {
		s87RecordsDir = t.TempDir()
		t.Cleanup(func() { s87RecordsDir = "" })
		s := &s87State{ids: ids, tests: tests}
		b, _ := json.Marshal(good())
		p, _ := s.recordPath("linux")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		l := &s87Leg{v: &s87Variant{goos: "linux"}, unreached: cause, ran: cause}
		s.legs = []*s87Leg{l}
		if err := s.fromRecord(l, cause); err != nil {
			t.Fatal(err)
		}
		if l.recorded == nil || l.ran != nil || len(s.active()) != 1 || len(s.live()) != 0 {
			t.Fatalf("recorded %v, ran %v", l.recorded, l.ran)
		}
	})
	t.Run("live only", func(t *testing.T) {
		t.Setenv("DSH_S18_LIVE_ONLY", "1")
		s := &s87State{ids: ids, tests: tests}
		l := &s87Leg{v: &s87Variant{goos: "linux"}, unreached: cause, ran: cause}
		if err := s.fromRecord(l, cause); err != cause {
			t.Fatalf("fromRecord = %v, want only the cause", err)
		}
	})
}
