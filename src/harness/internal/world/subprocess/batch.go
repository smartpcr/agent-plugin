package subprocess

import (
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// BatchPolicy says whether Start may run a Windows batch file (.bat, .cmd) through
// cmd.exe (DH-ADR-NODEPROC-1, DH-LIB-NODEPROC-R4). It matters only on Windows;
// other systems have no batch files and ignore it.
type BatchPolicy int

const (
	// Refuse is the default: a batch file is refused before anything starts,
	// because cmd.exe parses its command line by rules no argument quoting can
	// make safe in general.
	Refuse BatchPolicy = iota
	// AllowChecked runs a batch file as `cmd.exe /d /s /c "<line>"`, with cmd.exe
	// taken from the system directory, every argument wrapped in double quotes and
	// its trailing backslashes doubled. An argument (the batch file's path
	// included) holding a double quote, a percent sign, an exclamation mark, CR,
	// LF or NUL is refused with its position, never passed in altered form: none
	// of them reaches the batch file unchanged. The npm shims of vendor command
	// line tools on Windows are such files (gap-closure 16.5.1).
	AllowChecked
)

// maxBatchLine is the longest command line cmd.exe takes, in UTF-16 units
// ("Command prompt (Cmd.exe) command-line string limitation", Microsoft Learn).
const maxBatchLine = 8191

// Program is a resolved program: its absolute path, and whether Windows would run
// it through cmd.exe, so that the caller decides whether to opt in.
type Program struct {
	Path  string
	Batch bool
}

// batchUnsafe names each character that cannot pass through cmd.exe unchanged:
// cmd.exe ends its quoted region at a double quote, substitutes %NAME% even inside
// quotes, expands !NAME! when delayed expansion is on, and ends the command at a
// line break; NUL cannot be in a command line at all.
var batchUnsafe = []struct {
	c    byte
	name string
}{
	{'"', "a double quote"},
	{'%', "a percent sign"},
	{'!', "an exclamation mark"},
	{'\r', "a carriage return"},
	{'\n', "a line feed"},
	{0, "a NUL byte"},
}

// checkBatchArgs refuses the first argument cmd.exe cannot pass unchanged. The
// error names the position and the character, never the value, which may be a
// secret.
func checkBatchArgs(argv []string) error {
	for i, a := range argv {
		for _, u := range batchUnsafe {
			if strings.IndexByte(a, u.c) < 0 {
				continue
			}
			what := "argument " + strconv.Itoa(i)
			if i == 0 {
				what = "the batch file's path"
			}
			return herr.Newf(ReasonBadArgument, "%s holds %s, which cmd.exe cannot pass to a batch file unchanged; start the program behind the batch file instead", what, u.name)
		}
	}
	return nil
}

// batchCommandLine is the whole command line of a checked batch run: shell (the
// absolute path of cmd.exe) with /d (no AutoRun commands), /s (strip exactly the
// outer quotes of what follows /c) and /c, then every element of argv wrapped in
// double quotes with its trailing backslashes doubled, so that a C-runtime program
// the batch file forwards %* to reads each argument as given. argv must have
// passed checkBatchArgs.
func batchCommandLine(shell string, argv []string) string {
	var b strings.Builder
	b.WriteString(`"` + shell + `" /d /s /c "`)
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(quoteBatchArg(a))
	}
	b.WriteByte('"')
	return b.String()
}

func quoteBatchArg(a string) string {
	trimmed := strings.TrimRight(a, `\`)
	return `"` + trimmed + strings.Repeat(`\`, 2*(len(a)-len(trimmed))) + `"`
}

// batchLaunch checks argv for a checked batch run and returns the program to
// create (cmd.exe), its nominal arguments and the command line CreateProcess gets.
func batchLaunch(argv []string) (launch, error) {
	if err := checkBatchArgs(argv); err != nil {
		return launch{}, err
	}
	shell, err := systemShell()
	if err != nil {
		return launch{}, herr.Wrap(ReasonStartFailed, err, "find cmd.exe in the system directory")
	}
	line := batchCommandLine(shell, argv)
	if n := len(utf16.Encode([]rune(line))); n > maxBatchLine {
		return launch{}, herr.Newf(ReasonLineTooLong, "the batch command line is %d UTF-16 units, over cmd.exe's %d; pass a script file instead", n, maxBatchLine)
	}
	return launch{path: shell, args: []string{shell, "/d", "/s", "/c", line}, cmdLine: line}, nil
}

// launch is what Start creates: the program, its arguments, and on Windows the
// literal command line when the default quoting must not be used.
type launch struct {
	path    string
	args    []string
	cmdLine string
}
