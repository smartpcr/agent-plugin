// Package homedir decides where the harness home is (DH-D-UTIL-5). Every command
// and boot (A21, A24) call it, so they agree; the layout below the root belongs to
// the storage area (A07).
//
// The order is: an explicit path (the --home flag), then DSH_HOME, then ~/.dsh. A
// value that is empty or only white space counts as unset. Only "~" and "~/" (or
// "~\" on Windows) expand; "~alice/x" is left alone.
package homedir

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Env is the environment variable that names the home.
const Env = "DSH_HOME"

// Dir is the name of the default home below the user's home directory.
const Dir = ".dsh"

// ReasonNoUserHome is returned when the user's home directory cannot be found.
const ReasonNoUserHome herr.Reason = "homedir/no-user-home"

// userHome is os.UserHomeDir; a test replaces it.
var userHome = os.UserHomeDir

// Lookup reads one environment value; boot supplies it from launch.Env, a test from
// a map. A nil Lookup reads nothing.
type Lookup func(name string) (string, bool)

// Resolve returns the absolute home: explicit, else DSH_HOME from lookup, else
// Default.
func Resolve(explicit string, lookup Lookup) (string, error) {
	if v := strings.TrimSpace(explicit); v != "" {
		return absolute(v)
	}
	if lookup != nil {
		if v, ok := lookup(Env); ok && strings.TrimSpace(v) != "" {
			return absolute(strings.TrimSpace(v))
		}
	}
	return Default()
}

// Default returns ~/.dsh.
func Default() (string, error) {
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, Dir), nil
}

// ExpandTilde replaces a leading "~" or "~/" with the user's home directory and
// returns every other path unchanged.
func ExpandTilde(path string) (string, error) {
	if path != "~" && !hasTildeSlash(path) {
		return path, nil
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return h, nil
	}
	return filepath.Join(h, path[2:]), nil
}

// Display returns a form of path that never shows an absolute machine path: a path
// under the user's home as "~/<rest>", a relative path cleaned, and any other
// absolute path as ".../<last element>".
func Display(path string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	clean := filepath.Clean(path)
	if h, err := home(); err == nil {
		if rel, ok := within(h, clean); ok {
			if rel == "." {
				return "~"
			}
			return "~/" + filepath.ToSlash(rel)
		}
	}
	base := filepath.Base(clean)
	if base == string(filepath.Separator) || base == "." || filepath.VolumeName(clean)+`\` == clean {
		return "..."
	}
	return ".../" + base
}

func absolute(v string) (string, error) {
	p, err := ExpandTilde(v)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

func home() (string, error) {
	h, err := userHome()
	if err != nil || strings.TrimSpace(h) == "" {
		if err == nil {
			err = os.ErrNotExist
		}
		return "", herr.Wrap(ReasonNoUserHome, err, "the user's home directory is unknown; pass --home or set "+Env)
	}
	return filepath.Clean(h), nil
}

func hasTildeSlash(p string) bool {
	if strings.HasPrefix(p, "~/") {
		return true
	}
	return runtime.GOOS == "windows" && strings.HasPrefix(p, `~\`)
}

// within returns path relative to dir when path is dir or below it, comparing
// without regard to case on Windows.
func within(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.VolumeName(dir), filepath.VolumeName(path)) {
		return "", false
	}
	return rel, true
}
