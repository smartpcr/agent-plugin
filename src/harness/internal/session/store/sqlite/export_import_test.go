package sqlite_test

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// exportFixture is the fixed session of the export golden: 50 events at fixed
// times, mostly tool calls (one payload with characters JSON escapes for HTML),
// and every fifth an ignorable record of a type this build does not know,
// carrying every envelope field and an aux part, which must come back as the
// same bytes.
func exportFixture(t *testing.T) (store.Header, []store.Record) {
	t.Helper()
	base := time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	h := store.Header{ID: "export-fixture", CreatedAt: time.Date(2025, 7, 1, 11, 59, 0, 0, time.UTC),
		Cwd: `C:\work\repo`, Preset: "default"}
	recs := make([]store.Record, 0, 50)
	for seq := store.Seq(0); seq < 50; seq++ {
		at := base + int64(seq)*1000
		if seq%5 == 4 {
			recs = append(recs, store.Record{Seq: seq, Time: at, Type: "future/thing", Version: 3, Ignorable: true,
				Op: event.OpReplace, Start: seq - 2, End: seq - 1, Sources: []store.Seq{seq - 3, seq - 1},
				Payload: fmt.Sprintf(`{"n": %d,  "kept":"as written"}`, seq), Cold: `{"raw":"stream <&>"}`})
			continue
		}
		args := `{"q":"x"}`
		if seq == 7 {
			args = `{"q":"a<b && c>d \u00e9"}`
		}
		d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("c%d", seq), Name: "grep", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		r, err := d.Stamp(seq, at)
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r)
	}
	return h, recs
}

func exportOf(t *testing.T, s *sqlite.Store, id string, o sqlite.ExportOptions) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := s.Export(bg, &buf, id, o); err != nil {
		t.Fatalf("Export(%s): %v", id, err)
	}
	return buf.Bytes()
}

// eventRows is every events row of session id, read by a fresh connection.
func eventRows(t *testing.T, path, id string) []string {
	t.Helper()
	rs, err := fresh(t, path).Query(`SELECT e.seq, e.time, e.type, e.pv, e.ignorable, e.surface, e.env, e.data, e.cold
		FROM events e JOIN sessions s ON s.uid = e.session WHERE s.id = ? ORDER BY e.seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var (
			seq, tm, pv, ign, surf int64
			typ, data              string
			env, cold              sql.NullString
		)
		if err := rs.Scan(&seq, &tm, &typ, &pv, &ign, &surf, &env, &data, &cold); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d|%d|%s|%d|%d|%d|%v|%s|%v", seq, tm, typ, pv, ign, surf, env, data, cold))
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// DH-T-STORE-13 / TC-07: a session of 50 events exported from home H1 and
// imported into an empty home H2 has the same event rows there, the export file
// equals the committed golden, and exporting it again from H2 gives the same
// bytes, plain and gzip.
func TestExportImportRoundTrip(t *testing.T) {
	h1, h2 := dbPath(t), dbPath(t)
	s1, s2 := open(t, h1), open(t, h2)
	h, recs := exportFixture(t)
	if err := s1.Seed(bg, h, recs); err != nil {
		t.Fatal(err)
	}
	file := exportOf(t, s1, h.ID, sqlite.ExportOptions{})
	golden.Assert(t, "export", file)

	got, err := s2.Import(bg, bytes.NewReader(file), sqlite.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got.ID != h.ID || !got.CreatedAt.Equal(h.CreatedAt) || got.Cwd != h.Cwd || got.Preset != h.Preset {
		t.Fatalf("imported header %+v; want %+v", got, h)
	}
	want, have := eventRows(t, h1, h.ID), eventRows(t, h2, h.ID)
	if len(want) != 50 || strings.Join(want, "\n") != strings.Join(have, "\n") {
		t.Fatalf("H2 rows differ from H1's:\nH1 (%d):\n%s\nH2 (%d):\n%s", len(want), strings.Join(want, "\n"),
			len(have), strings.Join(have, "\n"))
	}
	r, err := s2.OpenRead(bg, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	read, err := r.Read(bg, 0, 0, store.ReadOptions{WithAux: true})
	if err != nil || len(read) != len(recs) {
		t.Fatalf("Read in H2: %d records, %v", len(read), err)
	}
	for i := range recs {
		if !read[i].Equal(recs[i]) {
			t.Fatalf("record %d in H2 = %+v; want %+v", i, read[i], recs[i])
		}
	}
	sum, err := s2.Stat(bg, h.ID)
	if err != nil || sum.NextSeq != 50 || sum.Holder != nil {
		t.Fatalf("Stat in H2 = %+v, %v; want 50 records, unheld", sum, err)
	}
	var from string
	if err := fresh(t, h2).QueryRow(`SELECT imported_from FROM sessions WHERE id = ?`, h.ID).Scan(&from); err != nil || from != h.ID {
		t.Fatalf("imported_from = %q, %v", from, err)
	}
	if again := exportOf(t, s2, h.ID, sqlite.ExportOptions{}); !bytes.Equal(again, file) {
		t.Fatal("export, import, export: the two files differ")
	}

	gz1 := exportOf(t, s1, h.ID, sqlite.ExportOptions{Gzip: true})
	if _, err := s2.Import(bg, bytes.NewReader(gz1), sqlite.ImportOptions{NewID: "copy"}); err != nil {
		t.Fatalf("Import of the gzip file under a new id: %v", err)
	}
	if gz2 := exportOf(t, s2, "copy", sqlite.ExportOptions{Gzip: true}); bytes.Equal(gz2, gz1) {
		t.Fatal("the copy exported the original's id")
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz1))
	if err != nil {
		t.Fatal(err)
	}
	var plain bytes.Buffer
	if _, err := plain.ReadFrom(zr); err != nil || !bytes.Equal(plain.Bytes(), file) {
		t.Fatalf("the gzip export does not decompress to the plain one: %v", err)
	}
	if rows := eventRows(t, h2, "copy"); strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatal("the copy's rows differ from the original's")
	}
}

// DH-T-STORE-13: a truncated or altered file is refused and nothing is written;
// an id the home holds is refused with the home unchanged.
func TestImportRefusals(t *testing.T) {
	h1, h2 := dbPath(t), dbPath(t)
	s1, s2 := open(t, h1), open(t, h2)
	h, recs := exportFixture(t)
	if err := s1.Seed(bg, h, recs); err != nil {
		t.Fatal(err)
	}
	file := exportOf(t, s1, h.ID, sqlite.ExportOptions{})
	lines := strings.SplitAfter(string(file), "\n")
	lines = lines[:len(lines)-1] // the empty string after the last "\n"
	replaced := func(i int, old, new string) string {
		l := append([]string(nil), lines...)
		if !strings.Contains(l[i], old) {
			t.Fatalf("line %d has no %q: %s", i, old, l[i])
		}
		l[i] = strings.Replace(l[i], old, new, 1)
		return strings.Join(l, "")
	}
	dropped := func(i int) string {
		l := append(append([]string(nil), lines[:i]...), lines[i+1:]...)
		return strings.Join(l, "")
	}
	cases := []struct {
		name, file string
		code       store.Code
	}{
		{"empty", "", sqlite.CodeInvalidExport},
		{"truncated mid-line", string(file[:len(file)/2]), sqlite.CodeInvalidExport},
		{"no trailer", strings.Join(lines[:len(lines)-1], ""), sqlite.CodeInvalidExport},
		{"an altered payload byte", replaced(3, `grep`, `grap`), sqlite.CodeInvalidExport},
		{"an event dropped", dropped(10), sqlite.CodeInvalidExport},
		{"bytes after the trailer", string(file) + "{}\n", sqlite.CodeInvalidExport},
		{"another format", replaced(0, `"dsh_export":1`, `"dsh_export":2`), sqlite.CodeInvalidExport},
		{"not JSON", "hello\n", sqlite.CodeInvalidExport},
	}
	for _, c := range cases {
		_, err := s2.Import(bg, strings.NewReader(c.file), sqlite.ImportOptions{})
		if codeOf(err) != c.code {
			t.Errorf("%s: Import = %v; want %s", c.name, err, c.code)
		}
	}
	if n := countWhere(t, fresh(t, h2), `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("refused imports wrote %d sessions", n)
	}
	if n := countWhere(t, fresh(t, h2), `SELECT count(*) FROM events`); n != 0 {
		t.Fatalf("refused imports wrote %d events", n)
	}

	// A file whose digest is right but whose record the registry refuses (a
	// known type with a payload that does not decode) is refused too.
	bad := []store.Record{recs[0]}
	bad[0].Payload = `{"call_id":5}`
	if err := s1.Seed(bg, store.Header{ID: "bad", CreatedAt: h.CreatedAt}, bad); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Import(bg, bytes.NewReader(exportOf(t, s1, "bad", sqlite.ExportOptions{})), sqlite.ImportOptions{}); err == nil {
		t.Fatal("Import of an undecodable record succeeded")
	}

	// An existing id.
	if _, err := s2.Import(bg, bytes.NewReader(file), sqlite.ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	before := eventRows(t, h2, h.ID)
	_, err := s2.Import(bg, bytes.NewReader(file), sqlite.ImportOptions{})
	if codeOf(err) != store.CodeExists || !strings.Contains(err.Error(), h.ID) {
		t.Fatalf("Import of an existing id = %v; want %s naming %q", err, store.CodeExists, h.ID)
	}
	if after := eventRows(t, h2, h.ID); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatal("the refused import changed the existing session")
	}
	if n := countWhere(t, fresh(t, h2), `SELECT count(*) FROM sessions`); n != 1 {
		t.Fatalf("%d sessions after the refusal; want 1", n)
	}
	if _, err := s1.Import(bg, bytes.NewReader(file), sqlite.ImportOptions{}); codeOf(err) != store.CodeExists {
		t.Fatalf("Import into the home it came from = %v; want %s", err, store.CodeExists)
	}
}

// DH-T-STORE-13: a small input that expands hugely is stopped at the limit, and
// writes nothing.
func TestImportStopsAGzipBomb(t *testing.T) {
	s := open(t, dbPath(t))
	var bomb bytes.Buffer
	zw, err := gzip.NewWriterLevel(&bomb, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	zero := make([]byte, 1<<20)
	for range 64 { // 64 MiB of zeros, a line without end
		if _, err := zw.Write(zero); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if bomb.Len() > 1<<20 {
		t.Fatalf("the bomb is %d bytes compressed; want a small input", bomb.Len())
	}
	_, err = s.Import(bg, bytes.NewReader(bomb.Bytes()), sqlite.ImportOptions{Limit: 4 << 20})
	if codeOf(err) != sqlite.CodeExportTooLarge {
		t.Fatalf("Import of the bomb = %v; want %s", err, sqlite.CodeExportTooLarge)
	}
	if n := countWhere(t, fresh(t, s.Path()), `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("the bomb wrote %d sessions", n)
	}

	// The limit bounds a plain file too, and a file under it passes.
	h, recs := exportFixture(t)
	if err := s.Seed(bg, h, recs); err != nil {
		t.Fatal(err)
	}
	file := exportOf(t, s, h.ID, sqlite.ExportOptions{})
	if _, err := s.Import(bg, bytes.NewReader(file), sqlite.ImportOptions{NewID: "small", Limit: int64(len(file)) - 1}); codeOf(err) != sqlite.CodeExportTooLarge {
		t.Fatalf("Import one byte over the limit = %v; want %s", err, sqlite.CodeExportTooLarge)
	}
	if _, err := s.Import(bg, bytes.NewReader(file), sqlite.ImportOptions{NewID: "small", Limit: int64(len(file))}); err != nil {
		t.Fatalf("Import at the limit: %v", err)
	}
}

// Export of a missing session is session/not-found and writes nothing.
func TestExportMissing(t *testing.T) {
	s := open(t, dbPath(t))
	var buf bytes.Buffer
	if err := s.Export(bg, &buf, "nope", sqlite.ExportOptions{}); codeOf(err) != store.CodeNotFound || buf.Len() != 0 {
		t.Fatalf("Export(nope) = %v with %d bytes; want %s and nothing", err, buf.Len(), store.CodeNotFound)
	}
}
