//go:build scenario

package scenarios

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exec10Rehearsal writes a rehearsal directory holding files (name to content)
// and a manifest recording sums (name to SHA-256; "" is the file's own).
func exec10Rehearsal(t *testing.T, files, sums map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	var manifest strings.Builder
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, sum := range sums {
		if sum == "" {
			h := sha256.Sum256([]byte(files[name]))
			sum = hex.EncodeToString(h[:])
		}
		manifest.WriteString(sum + "  " + name + "\n")
	}
	if sums != nil {
		if err := os.WriteFile(filepath.Join(dir, rehearsalManifest), []byte(manifest.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The DH-S-18 hand-off names the runner's binary; EXEC-10 copies the rehearsal's
// binary for the target's architecture, verified, and never a build of its own.
func TestExec10PickRehearsalBinary(t *testing.T) {
	files := map[string]string{"dsh-linux-amd64": "amd64 binary", "dsh-linux-arm64": "arm64 binary"}

	t.Run("same platform: the hand-off binary itself", func(t *testing.T) {
		dir := exec10Rehearsal(t, files, map[string]string{"dsh-linux-amd64": "", "dsh-linux-arm64": ""})
		bin := filepath.Join(dir, "dsh-linux-amd64")
		path, sum, same, err := exec10Pick(bin, "linux", "amd64", "amd64")
		h := sha256.Sum256([]byte(files["dsh-linux-amd64"]))
		if err != nil || !same || path != bin || sum != hex.EncodeToString(h[:]) {
			t.Fatalf("exec10Pick = %q, %q, %v, %v; want the hand-off binary with its manifest SHA-256", path, sum, same, err)
		}
	})

	t.Run("other architecture: its rehearsed sibling", func(t *testing.T) {
		dir := exec10Rehearsal(t, files, map[string]string{"dsh-linux-amd64": "", "dsh-linux-arm64": ""})
		path, _, same, err := exec10Pick(filepath.Join(dir, "dsh-linux-amd64"), "linux", "amd64", "arm64")
		if err != nil || same || path != filepath.Join(dir, "dsh-linux-arm64") {
			t.Fatalf("exec10Pick = %q, %v, %v; want the rehearsed dsh-linux-arm64", path, same, err)
		}
	})

	t.Run("a Windows runner hands its own binary: the Linux sibling is picked", func(t *testing.T) {
		all := map[string]string{"dsh-windows-amd64.exe": "exe", "dsh-linux-amd64": "amd64 binary"}
		dir := exec10Rehearsal(t, all, map[string]string{"dsh-windows-amd64.exe": "", "dsh-linux-amd64": ""})
		path, _, same, err := exec10Pick(filepath.Join(dir, "dsh-windows-amd64.exe"), "windows", "amd64", "amd64")
		if err != nil || same || path != filepath.Join(dir, "dsh-linux-amd64") {
			t.Fatalf("exec10Pick = %q, %v, %v; want the rehearsed dsh-linux-amd64", path, same, err)
		}
	})

	refused := []struct {
		name, want string
		sums       map[string]string
		os, arch   string
	}{
		{"a target the rehearsal did not build", "built no dsh-linux-arm64", map[string]string{"dsh-linux-amd64": ""}, "linux", "amd64"},
		{"a binary that is not the manifest's", "SHA-256", map[string]string{"dsh-linux-amd64": "", "dsh-linux-arm64": strings.Repeat("0", 64)}, "linux", "amd64"},
		{"no rehearsal and another platform", "no " + rehearsalManifest, nil, "windows", "amd64"},
	}
	for _, c := range refused {
		t.Run("refused: "+c.name, func(t *testing.T) {
			dir := exec10Rehearsal(t, files, c.sums)
			path, _, _, err := exec10Pick(filepath.Join(dir, "dsh-linux-amd64"), c.os, c.arch, "arm64")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("exec10Pick = %q, %v; want an error naming %q", path, err, c.want)
			}
		})
	}

	t.Run("refused: a manifest that cannot be read, even on the same platform", func(t *testing.T) {
		dir := exec10Rehearsal(t, files, nil)
		if err := os.Mkdir(filepath.Join(dir, rehearsalManifest), 0o700); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, "dsh-linux-amd64")
		if path, _, same, err := exec10Pick(bin, "linux", "amd64", "amd64"); err == nil || !strings.Contains(err.Error(), "reading the rehearsal manifest") {
			t.Fatalf("exec10Pick = %q, %v, %v; want the unreadable manifest refused, not DSH_BIN used unchecked", path, same, err)
		}
	})

	t.Run("no rehearsal, same platform: DSH_BIN as given", func(t *testing.T) {
		dir := exec10Rehearsal(t, files, nil)
		bin := filepath.Join(dir, "dsh-linux-amd64")
		if path, _, same, err := exec10Pick(bin, "linux", "amd64", "amd64"); err != nil || !same || path != bin {
			t.Fatalf("exec10Pick = %q, %v, %v; want DSH_BIN itself", path, same, err)
		}
	})
}

// A cleanup's SSH command gets a live, bounded context: testing cancels t.Context
// just before the cleanups run, which would end every cleanup command at once
// (stopping the host, checking the scope, removing the scratch directory).
func TestExec10ContextLiveInCleanup(t *testing.T) {
	var live, bounded bool
	t.Run("cleanup", func(t *testing.T) {
		t.Cleanup(func() {
			ctx, cancel := exec10Context(t)
			defer cancel()
			_, bounded = ctx.Deadline()
			live = ctx.Err() == nil
		})
	})
	if !live || !bounded {
		t.Fatalf("in a cleanup exec10Context is live=%v bounded=%v, want both", live, bounded)
	}
}
