package shellexec

import (
	"os"
	"path"
	"runtime"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Host is what program resolution reads: the system's name, the child's
// environment and a test for an existing file. Exists must judge a candidate by
// its attributes and never open it: a Windows Store execution alias is a reparse
// point that cannot be opened (execution.md sec. 6.1, rule 6). Tests pass a fake.
type Host struct {
	GOOS   string
	Env    subprocess.Env
	Exists func(path string) bool
}

// LocalHost is this machine with env as the child's environment.
func LocalHost(env subprocess.Env) Host {
	return Host{GOOS: runtime.GOOS, Env: env, Exists: regularFile}
}

// regularFile reports a path that names something other than a directory,
// without following or opening it.
func regularFile(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && !fi.IsDir()
}

// Windows reports a Windows host.
func (h Host) Windows() bool { return h.GOOS == "windows" }

// Get is the value of an environment variable, without regard to case on
// Windows.
func (h Host) Get(name string) string {
	for i := len(h.Env) - 1; i >= 0; i-- {
		n, v, ok := strings.Cut(h.Env[i], "=")
		if ok && (n == name || h.Windows() && strings.EqualFold(n, name)) {
			return v
		}
	}
	return ""
}

// IsAbs reports an absolute path on the host's system.
func (h Host) IsAbs(p string) bool {
	if !h.Windows() {
		return strings.HasPrefix(p, "/")
	}
	if strings.HasPrefix(p, `\\`) {
		return len(p) > 2
	}
	return len(p) >= 3 && isLetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// Join joins a directory and names with the host's separator.
func (h Host) Join(dir string, names ...string) string {
	if !h.Windows() {
		return path.Join(append([]string{dir}, names...)...)
	}
	out := strings.TrimRight(dir, `\/`)
	for _, n := range names {
		out += `\` + strings.Trim(n, `\/`)
	}
	return out
}

// Clean is a directory spelled for comparison: separators unified, the
// trailing one dropped, and on Windows upper case.
func (h Host) Clean(dir string) string {
	if !h.Windows() {
		return path.Clean(dir)
	}
	return strings.ToUpper(strings.TrimRight(strings.ReplaceAll(dir, "/", `\`), `\`))
}

// Configured checks an explicit program path: absolute and present.
func (h Host) Configured(p string) (string, error) {
	if !h.IsAbs(p) {
		return "", herr.Newf(ReasonBadConfig, "the configured shell path %q is not absolute", p)
	}
	if !h.Exists(p) {
		return "", herr.Newf(ReasonNotFound, "the configured shell %q does not exist", p)
	}
	return p, nil
}

// Search looks for name on the child's PATH, in order, skipping relative entries
// and the directories skip rejects. On Windows the name gets ".exe"; a batch file
// is never a shell.
func (h Host) Search(name string, skip func(dir string) bool) (string, bool) {
	sep := ":"
	if h.Windows() {
		sep = ";"
		if !strings.HasSuffix(strings.ToLower(name), ".exe") {
			name += ".exe"
		}
	}
	for _, dir := range strings.Split(h.Get("PATH"), sep) {
		dir = strings.Trim(dir, `"`)
		if dir == "" || !h.IsAbs(dir) || skip != nil && skip(dir) {
			continue
		}
		if p := h.Join(dir, name); h.Exists(p) {
			return p, true
		}
	}
	return "", false
}
