package subprocess

import (
	"runtime"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// DH-LIB-NODEPROC-R4: the checked batch run's rules are plain functions, so they
// are checked on every system, not only where Windows applies them. Each of ", %,
// !, CR, LF and NUL is refused with its position and never its value; the batch
// file's own path is checked too; every argument is wrapped in quotes with only
// its trailing backslashes doubled, behind cmd.exe's /d /s /c.
//
//dsh:test DH-T-NODEPROC-2
func TestBatchRules(t *testing.T) {
	for _, c := range []struct {
		arg, want string
	}{
		{`say "hi"`, "a double quote"},
		{"100%", "a percent sign"},
		{"wow!", "an exclamation mark"},
		{"a\rb", "a carriage return"},
		{"a\nb", "a line feed"},
		{"a\x00b", "a NUL byte"},
	} {
		err := checkBatchArgs([]string{`C:\t\tool.cmd`, "ok", "sk-secret-value" + c.arg})
		if herr.Kind(err) != ReasonBadArgument || !strings.Contains(err.Error(), "argument 2 holds "+c.want) {
			t.Errorf("checkBatchArgs(%q) = %v; want %s naming argument 2 and %s", c.arg, err, ReasonBadArgument, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "sk-secret-value") {
			t.Errorf("the refusal %q quotes the argument's value", err)
		}
	}
	if err := checkBatchArgs([]string{`C:\100%\tool.cmd`}); err == nil || !strings.Contains(err.Error(), "the batch file's path holds a percent sign") {
		t.Errorf("checkBatchArgs(path with %%) = %v; want the path refused", err)
	}
	if err := checkBatchArgs([]string{`C:\t\tool.cmd`, "", "a&b|c^d", `x\`}); err != nil {
		t.Errorf("checkBatchArgs(plain) = %v; want nil", err)
	}

	got := batchCommandLine(`C:\Windows\System32\cmd.exe`, []string{`C:\Program Files\t\tool.cmd`, "", "a b", `back\slash\\`, `mid\dle`})
	want := `"C:\Windows\System32\cmd.exe" /d /s /c ""C:\Program Files\t\tool.cmd" "" "a b" "back\slash\\\\" "mid\dle""`
	if got != want {
		t.Errorf("batchCommandLine =\n  %s\nwant\n  %s", got, want)
	}
}

// Off Windows the batch policy changes nothing: there are no batch files, so a
// .cmd path is an ordinary program and no command line is built.
//
//dsh:test DH-T-NODEPROC-2
func TestBatchPolicyOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows behaviour is TestBatchToolByBareNameWithCheckedArguments")
	}
	for _, policy := range []BatchPolicy{Refuse, AllowChecked} {
		l, err := check([]string{"/opt/t/tool.cmd", "100%"}, policy)
		if err != nil || l.path != "/opt/t/tool.cmd" || l.cmdLine != "" || len(l.args) != 2 {
			t.Errorf("check(policy %d) = %+v, %v; want the program as given and no command line", policy, l, err)
		}
	}
	if p := classify("/opt/t/tool.cmd"); p.Batch {
		t.Errorf("classify = %+v; want Batch false off Windows", p)
	}
	if _, err := systemShell(); err == nil {
		t.Error("systemShell off Windows = nil error; want one")
	}
	if _, err := batchLaunch([]string{"/opt/t/tool.cmd"}); herr.Kind(err) != ReasonStartFailed {
		t.Errorf("batchLaunch off Windows = %v; want %s", err, ReasonStartFailed)
	}
}
