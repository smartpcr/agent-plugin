package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestStaticcheckGateNamesTheCheck is the "a staticcheck finding names the gate"
// scenario of plan stage 13.5 at the gate's own level: on a scratch module with
// "if x == x", the staticcheck gate fails naming SA4000, and it ran the pinned
// staticcheck with both tag sets (M1-G-TOOL-1; DH-T-TOOLCHAIN-9).
// TestStaticcheckFindingStopsTheGate runs the whole list.
func TestStaticcheckGateNamesTheCheck(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "staticcheck-seeded"))
	var out bytes.Buffer
	err := cfg.staticcheckGate(context.Background(), &out)
	if err == nil {
		t.Fatalf("staticcheck gate passed x == x:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "(SA4000)") {
		t.Errorf("output does not name SA4000:\n%s", out.String())
	}
	for _, tags := range []string{noTags, layerTags} {
		if !strings.Contains(out.String(), "staticcheck "+tags+" ./...") {
			t.Errorf("staticcheck did not run with %s:\n%s", tags, out.String())
		}
	}

	clean := t.TempDir()
	writeFiles(t, clean, map[string]string{
		"go.mod": "module example.com/scclean\n\ngo 1.27.4\n",
		"x.go":   "// Package x is clean under staticcheck.\npackage x\n\n// Same reports whether a equals b.\nfunc Same(a, b int) bool { return a == b }\n",
	})
	cfg = newTestConfig(t, clean)
	out.Reset()
	if err := cfg.staticcheckGate(context.Background(), &out); err != nil {
		t.Errorf("clean module: staticcheck gate = %v\n%s", err, out.String())
	}
}

// TestVulncheckGateUsesTheDatabase is the "known-vulnerable fixture fails vulncheck"
// scenario of plan stage 13.5 at the gate's own level: with the file database of
// testdata/vulndb, which lists strings.ToUpper as vulnerable before go1.99.0, the
// vulncheck gate fails on a module that calls it and names the advisory; a database
// it cannot read fails the gate rather than skipping it; the same database passes a
// module that does not call the symbol (M1-G-TOOL-4). No network is used.
func TestVulncheckGateUsesTheDatabase(t *testing.T) {
	db, err := filepath.Abs(filepath.Join("testdata", "vulndb"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := newTestConfig(t, scratch(t, "vuln-stdlib"))
	cfg.VulnDB = fileURL(db)
	var out bytes.Buffer
	err = cfg.vulncheckGate(context.Background(), &out)
	if err == nil || !strings.Contains(out.String(), "GO-2099-0001") {
		t.Errorf("vulnerable module: vulncheck gate = %v, want a failure naming GO-2099-0001\n%s", err, out.String())
	}

	cfg.VulnDB = fileURL(filepath.Join(t.TempDir(), "no-such-db"))
	out.Reset()
	if err := cfg.vulncheckGate(context.Background(), &out); err == nil {
		t.Errorf("unreadable database: vulncheck gate passed\n%s", out.String())
	}

	clean := t.TempDir()
	writeFiles(t, clean, map[string]string{
		"go.mod": "module example.com/vulnclean\n\ngo 1.27.4\n",
		"p.go":   "// Package p calls nothing listed.\npackage p\n\n// Two is 2.\nfunc Two() int { return 2 }\n",
	})
	cfg = newTestConfig(t, clean)
	cfg.VulnDB = fileURL(db)
	out.Reset()
	if err := cfg.vulncheckGate(context.Background(), &out); err != nil {
		t.Errorf("clean module: vulncheck gate = %v\n%s", err, out.String())
	}
}

// TestVetGatePrintsExceptions: the vet gate prints the total of accepted
// //lint:ignore directives, own and staticcheck alike, over every Go file of the
// module, also files of another platform; a reasonless directive fails vet through
// the lintignore analyzer and is not counted (M1-G-TOOL-7; DH-T-TOOLCHAIN-8).
func TestVetGatePrintsExceptions(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod": "module example.com/exceptions\n\ngo 1.27.4\n",
		"x.go": "// Package x has reasoned exceptions.\npackage x\n\nfunc f() error { return nil }\n\n" +
			"// G calls f.\nfunc G() {\n\t//lint:ignore errdiscard the fixture's reason\n\t_ = f()\n}\n",
		"y_plan9.go": "package x\n\n//lint:ignore U1000 kept for the plan9 port\nfunc unused() {}\n",
	})
	cfg := newTestConfig(t, root)
	cfg.VetTool = buildGate(t)
	var out bytes.Buffer
	if err := cfg.vetGate(context.Background(), &out); err != nil {
		t.Fatalf("vet gate = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "exceptions: 2 (U1000 1, errdiscard 1)\n") {
		t.Errorf("output lacks the exception total:\n%s", out.String())
	}

	if err := os.WriteFile(filepath.Join(root, "z.go"), []byte("package x\n\nfunc h() {\n\t//lint:ignore errdiscard\n\t_ = f()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := cfg.vetGate(context.Background(), &out)
	if err == nil || !strings.Contains(out.String(), "errdiscard has no reason") {
		t.Errorf("reasonless directive: vet gate = %v, want the lintignore diagnostic\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "exceptions: 2 ") {
		t.Errorf("the reasonless directive was counted:\n%s", out.String())
	}
}

func TestFileURL(t *testing.T) {
	in, want := "/srv/db", "file:///srv/db"
	if runtime.GOOS == "windows" {
		in, want = `C:\db\v1`, "file:///C:/db/v1"
	}
	if got := fileURL(in); got != want {
		t.Errorf("fileURL(%q) = %q, want %q", in, got, want)
	}
}

// TestStaticcheckFindingStopsTheGate is the "a staticcheck finding names the gate"
// scenario of plan stage 13.5: the gate run on a scratch module with "if x == x"
// stops at staticcheck naming SA4000, exits 1 and starts no later gate (M1-G-TOOL-1;
// DH-T-TOOLCHAIN-9).
func TestStaticcheckFindingStopsTheGate(t *testing.T) {
	out, code := runGate(t, scratch(t, "staticcheck-seeded"))
	assertStoppedAt(t, out, code, "staticcheck")
	if code != 1 || !strings.Contains(out, "(SA4000)") {
		t.Errorf("exit %d, want 1 with SA4000 named:\n%s", code, out)
	}
}

// TestBrokenE2EFileFailsVet is the "a broken e2e file fails vet" scenario of plan
// stage 13.5: a scratch module whose //go:build e2e file does not compile passes the
// untagged and layer builds, yet the gate stops at vet (M1-D-GATE-25 A,
// M1-G-TOOL-17; DH-T-TOOLCHAIN-4).
func TestBrokenE2EFileFailsVet(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":      "module example.com/e2ebroken\n\ngo 1.27.4\n",
		"x.go":        "// Package x is seeded for the e2e vet run.\npackage x\n",
		"e2e_test.go": "//go:build e2e\n\npackage x\n\nimport \"testing\"\n\nfunc TestE2E(t *testing.T) { undefinedCall() }\n",
	})
	out, code := runGate(t, root)
	assertStoppedAt(t, out, code, "vet")
	if !strings.Contains(out, "FAIL .: go vet -tags=e2e ./...") || !strings.Contains(out, "undefinedCall") {
		t.Errorf("vet did not fail on the e2e file:\n%s", out)
	}
	if strings.Contains(out, "FAIL .: go vet -tags= ./...") {
		t.Errorf("the untagged vet failed; only the e2e build is broken:\n%s", out)
	}
}
