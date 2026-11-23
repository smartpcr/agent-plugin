package subprocess

import (
	"strings"
	"testing"
)

// DH-T-EXEC-2: the Windows batch and PATHEXT rules are plain functions of the
// name, so they are checked on every system, not only where Windows applies
// them: Win32 drops a stream suffix and trailing dots and spaces before it looks
// at the extension, and PATHEXT entries are trimmed, lower-cased and tried after
// the name as given when it already has an extension.
//
//dsh:test DH-T-EXEC-2
func TestWindowsNameRules(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\tools\run.bat`:        true,
		`C:\tools\RUN.CMD`:        true,
		`C:\tools\run.cmd::$DATA`: true,
		`C:/tools/run.bat. . `:    true,
		`run.bat:stream`:          true,
		`C:\tools\run.exe`:        false,
		`C:\tools.bat\run`:        false,
		`C:\tools\run.bat.exe`:    false,
	} {
		if got := isBatch(path); got != want {
			t.Errorf("isBatch(%q) = %v; want %v", path, got, want)
		}
	}
	for _, c := range []struct {
		name string
		env  Env
		want string
	}{
		{"go", nil, ".com,.exe,.bat,.cmd"},
		{"go", Env{"PATHEXT=.EXE; ;.Cmd;"}, ".exe,.cmd"},
		{"go.exe", Env{"PATHEXT=.EXE"}, ",.exe"},
	} {
		if got := strings.Join(windowsExts(c.name, c.env), ","); got != c.want {
			t.Errorf("windowsExts(%q, %q) = %q; want %q", c.name, c.env, got, c.want)
		}
	}
}
