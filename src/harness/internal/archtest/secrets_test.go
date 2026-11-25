package archtest

import (
	"os"
	"testing"
	"testing/fstest"
)

// TestRevealOnlyFromListedCallers is the Reveal() source check over the real
// harness module: no non-test file outside RevealCallers takes a value out of a
// secret (DH-D-HUMAN-7).
//
//dsh:test DH-T-HUMAN-10
func TestRevealOnlyFromListedCallers(t *testing.T) {
	found, err := Reveals(os.DirFS(moduleDirs[HarnessModule]))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

// TestNoSetenvOutsideTheStarter is the os.Setenv source check over the real
// harness module: no non-test file outside the starter writes the process
// environment, so no stored value or .env entry reaches a child by inheritance.
//
//dsh:test DH-T-HUMAN-12
func TestNoSetenvOutsideTheStarter(t *testing.T) {
	found, err := EnvWrites(os.DirFS(moduleDirs[HarnessModule]))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

// TestSecretChecksCatchViolations proves both predicates find what they look for
// and honour their exemptions.
//
//dsh:test DH-T-HUMAN-10, DH-T-HUMAN-12
func TestSecretChecksCatchViolations(t *testing.T) {
	file := func(src string) *fstest.MapFile { return &fstest.MapFile{Data: []byte("package p\n" + src)} }
	for _, tc := range []struct {
		name            string
		files           fstest.MapFS
		reveals, writes int
	}{
		{"clean", fstest.MapFS{"a/a.go": file(`import "os"; var _ = os.Getenv("X")`)}, 0, 0},
		{"a Reveal call", fstest.MapFS{"a/a.go": file(`type v struct{}; func (v) Reveal() string { return "" }; var _ = v{}.Reveal()`)}, 1, 0},
		{"Reveal with arguments is another method", fstest.MapFS{"a/a.go": file(`type v struct{}; func (v) Reveal(int) string { return "" }; var _ = v{}.Reveal(1)`)}, 0, 0},
		{"a listed caller", fstest.MapFS{"a/a.go": file(""), "internal/human/secret/file/f.go": file(`type v struct{}; func (v) Reveal() string { return "" }; var _ = v{}.Reveal()`), "internal/llm/provider/deepseek/d.go": file(`type v struct{}; func (v) Reveal() string { return "" }; var _ = v{}.Reveal()`)}, 0, 0},
		{"a lookalike of a listed caller", fstest.MapFS{"internal/human/secretx/f.go": file(`type v struct{}; func (v) Reveal() string { return "" }; var _ = v{}.Reveal()`)}, 1, 0},
		{"os.Setenv", fstest.MapFS{"a/a.go": file(`import "os"; func f() { os.Setenv("A", "b") }`)}, 0, 1},
		{"renamed syscall and a value", fstest.MapFS{"a/a.go": file(`import sc "syscall"; var set = sc.Setenv`)}, 0, 1},
		{"dot import", fstest.MapFS{"a/a.go": file(`import . "os"; var _ = Getpid`)}, 0, 1},
		{"a local named os", fstest.MapFS{"a/a.go": file(`type t struct{}; func (t) Setenv() {}; func f() { var os t; os.Setenv() }`)}, 0, 0},
		{"the starter is exempt", fstest.MapFS{"a/a.go": file(""), EnvWriter + "/s.go": file(`import "os"; func f() { os.Setenv("A", "b") }`)}, 0, 0},
		{"tests and testdata are exempt", fstest.MapFS{"a/a.go": file(""), "a/a_test.go": file(`import "os"; func f() { os.Setenv("A", "b"); x.Reveal() }`), "a/testdata/x.go": file(`import "os"; func f() { os.Setenv("A", "b") }`)}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reveals, err := Reveals(tc.files)
			if err != nil || len(reveals) != tc.reveals {
				t.Fatalf("Reveals = %q, %v; want %d", reveals, err, tc.reveals)
			}
			writes, err := EnvWrites(tc.files)
			if err != nil || len(writes) != tc.writes {
				t.Fatalf("EnvWrites = %q, %v; want %d", writes, err, tc.writes)
			}
		})
	}
	if _, err := Reveals(fstest.MapFS{}); err == nil {
		t.Fatal("an empty tree passed")
	}
}
