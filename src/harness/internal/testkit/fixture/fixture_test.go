package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DH-T-UTIL-16: equal identities get equal tokens and a normalised fixture is a
// fixed point.
//
//dsh:test DH-T-UTIL-16
func TestNormalizeReplacesTheTableWithTypedTokens(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	n := &Normalizer{Paths: map[string]string{home: "home"}}
	in := `{"id":"ses_0123456789ab","msg":"msg_0123456789abcdef","in":"in_0123456789abcdef01","call":"call_ABCDEFGH",` +
		`"created_at":"2026-01-02T03:04:05.123Z","time":1795772337790,"wait_ms":12,` +
		`"data":"{\"time\":1795772337791,\"request_digest\":\"sha256:` + strings.Repeat("a", 64) + `\",\"cwd\":` + escapeJSON(escapeJSON(home)) + `}",` +
		`"cwd":"` + escapeJSON(home) + `","again":"msg_0123456789abcdef","other":"msg_fedcba9876543210",` +
		`"sha256":"` + strings.Repeat("b", 64) + `","end":5}`
	got := string(n.Normalize([]byte(in)))
	want := `{"id":"<session:1>","msg":"<msg:1>","in":"<in:1>","call":"<call:1>",` +
		`"created_at":"<time>","time":"<time>","wait_ms":"<ms>",` +
		`"data":"{\"time\":\"<time>\",\"request_digest\":\"<digest:1>\",\"cwd\":<home>}",` +
		`"cwd":"<home>","again":"<msg:1>","other":"<msg:2>",` +
		`"sha256":"<sha256:1>","end":5}`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if err := Guard([]byte(got)); err != nil {
		t.Errorf("a normalised line is not a fixed point: %v", err)
	}
	// Numbering continues across calls on one normaliser.
	if got := string(n.Normalize([]byte(`"msg_fedcba9876543210" "msg_1111111111111111"`))); got != `"<msg:2>" "<msg:3>"` {
		t.Errorf("second call: %s", got)
	}
}

// escapeJSON is s inside a JSON string, without the quotes.
func escapeJSON(s string) string { return escape(s) }

// DH-T-UTIL-16: a fixture with an unscrubbed path or identity fails its guard.
//
//dsh:test DH-T-UTIL-16
func TestGuardRejectsAnUnscrubbedFixture(t *testing.T) {
	for _, in := range []string{`{"id":"ses_0123456789ab"}`, `{"time":1795772337790}`} {
		if err := Guard([]byte(in)); err == nil || !strings.Contains(err.Error(), "not normalised") {
			t.Errorf("%s: %v", in, err)
		}
	}
	if err := Guard([]byte(`{"cwd":"/tmp/x/y"}`), "/tmp/x"); err == nil {
		t.Error("a literal path passed the guard")
	}
	if err := Guard([]byte(`{"cwd":"<home>"}`), "/tmp/x"); err != nil {
		t.Error(err)
	}
}

func TestDiffNamesTheFirstDifferingLine(t *testing.T) {
	if d := Diff("f", []byte("a\nb"), []byte("a\nb")); d != "" {
		t.Errorf("equal: %q", d)
	}
	if d := Diff("f", []byte("a\nb"), []byte("a\nc")); !strings.Contains(d, "f: line 2 differs") || !strings.Contains(d, "got: b") {
		t.Errorf("differ: %q", d)
	}
	if d := Diff("f", []byte("a\n"), []byte("a\n\n")); !strings.Contains(d, "line 3") {
		t.Errorf("longer: %q", d)
	}
}

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCopyAndCompareTrees(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, src, map[string]string{"a.txt": "a\n", "sub/b.txt": "b\n"})
	if err := CopyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := CompareTrees(dst, src); err != nil {
		t.Fatalf("a copy differs: %v", err)
	}
	write(t, dst, map[string]string{"sub/b.txt": "B\n", "extra.txt": "x"})
	if err := os.Remove(filepath.Join(dst, "a.txt")); err != nil {
		t.Fatal(err)
	}
	err := CompareTrees(dst, src)
	for _, want := range []string{"a.txt: missing", "sub/b.txt: line 1 differs", "extra.txt: not expected"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
	if err := CompareTrees(filepath.Join(src, "none"), src); err == nil {
		t.Error("a missing tree compared")
	}
	if err := CompareTrees(src, filepath.Join(src, "none")); err == nil {
		t.Error("a missing expected tree compared")
	}
	if err := CopyTree(filepath.Join(src, "none"), dst); err == nil {
		t.Error("a missing tree copied")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	write(t, filepath.Dir(blocked), map[string]string{"file": "x"})
	if err := CopyTree(src, blocked); err == nil {
		t.Error("copied under a file")
	}
}

func TestReadManifest(t *testing.T) {
	for name, tc := range map[string]struct{ text, err string }{
		"ok":        {`{"profile":"headless","input":"hi","recording":"authored","route":"r"}`, ""},
		"profile":   {`{"profile":"tui","input":"hi","recording":"authored","route":"r"}`, "only headless"},
		"input":     {`{"profile":"headless","recording":"authored","route":"r"}`, "needs input and route"},
		"recording": {`{"profile":"headless","input":"hi","recording":"x","route":"r"}`, "neither authored nor live"},
		"unknown":   {`{"profile":"headless","input":"hi","recording":"live","route":"r","extra":1}`, "unknown field"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, map[string]string{"scenario.json": tc.text})
			m, err := ReadManifest(dir)
			if tc.err == "" {
				if err != nil || m.Input != "hi" || m.Route != "r" {
					t.Fatalf("%+v %v", m, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("want %q, got %v", tc.err, err)
			}
		})
	}
	if _, err := ReadManifest(t.TempDir()); err == nil {
		t.Error("a missing manifest read")
	}
}
