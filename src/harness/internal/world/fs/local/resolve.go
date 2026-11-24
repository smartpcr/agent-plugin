package local

import (
	"errors"
	iofs "io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// maxHops bounds the links followed resolving one path, as MemFS does.
const maxHops = 40

// abs is the lexical half of resolution (fs.Ref): a relative Path is joined with
// Dir and the result cleaned before any link is followed.
func abs(r fs.Ref) (string, error) {
	p := r.Path
	if p == "" {
		return "", herr.New(fs.CodeInvalidPath, "empty path")
	}
	if strings.ContainsRune(p, 0) {
		return "", herr.Newf(fs.CodeInvalidPath, "path %q contains a NUL byte", p)
	}
	if !filepath.IsAbs(p) {
		if r.Dir == "" || !filepath.IsAbs(r.Dir) {
			return "", herr.Newf(fs.CodeInvalidPath, "relative path %q with no absolute working directory", p)
		}
		p = filepath.Join(r.Dir, p)
	}
	return filepath.Clean(p), nil
}

// linkTarget reports whether fi, the Lstat of p, is a link and what it points
// at. On Windows a directory junction (a mount point, which Lstat reports as
// irregular since Go 1.23) counts as a link: os.Root treats it as one, so the
// fence must too.
func linkTarget(p string, fi iofs.FileInfo) (string, bool) {
	m := fi.Mode()
	if m&iofs.ModeSymlink == 0 && !(runtime.GOOS == "windows" && m&iofs.ModeIrregular != 0) {
		return "", false
	}
	t, err := os.Readlink(p)
	if err != nil {
		return "", false
	}
	return t, true
}

// resolve returns the canonical path of the absolute, clean path p: every link
// followed component by component (the final one too, unless noFollowFinal),
// the spelling of every other component kept as given. The part that does not
// exist yet is appended unresolved, so a path about to be created resolves too.
//
// filepath.EvalSymlinks is not used: on Windows it neither follows junctions nor
// keeps 8.3 short names, so its answer is not the path os.Root will walk.
func resolve(p string, noFollowFinal bool) (string, error) {
	sep := string(filepath.Separator)
	for hops := 0; ; {
		vol := filepath.VolumeName(p)
		rest := strings.Trim(p[len(vol):], sep)
		var parts []string
		if rest != "" {
			parts = strings.Split(rest, sep)
		}
		cur := vol + sep
		restarted := false
		for i, part := range parts {
			next := filepath.Join(cur, part)
			fi, err := os.Lstat(next)
			if errors.Is(err, iofs.ErrNotExist) {
				return filepath.Join(append([]string{next}, parts[i+1:]...)...), nil
			}
			if err != nil {
				e := mapErr(err, next)
				if busy(err) {
					return "", changed{e}
				}
				return "", e
			}
			last := i == len(parts)-1
			if t, ok := linkTarget(next, fi); ok && !(last && noFollowFinal) {
				if hops++; hops > maxHops {
					return "", herr.Newf(fs.CodeIO, "too many links resolving %s", p)
				}
				if !filepath.IsAbs(t) {
					t = filepath.Join(cur, t)
				}
				p = filepath.Clean(filepath.Join(append([]string{t}, parts[i+1:]...)...))
				restarted = true
				break
			}
			if !last && !fi.IsDir() {
				nd := herr.Newf(fs.CodeNotDirectory, "%s is not a directory", next)
				if fi.Mode()&iofs.ModeIrregular != 0 {
					// A reparse point being made or removed: the tree is changing.
					return "", changed{nd}
				}
				return "", nd
			}
			cur = next
		}
		if !restarted {
			return cur, nil
		}
	}
}

// canon resolves a write root the way resolve resolves a target, for
// fs.FenceCanon.
func canon(p string) (string, error) { return resolve(filepath.Clean(p), false) }

// key is the TargetKey of a canonical path: case-folded where the filesystem
// folds case.
func key(p string) fs.TargetKey {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return fs.TargetKey(strings.ToLower(p))
	}
	return fs.TargetKey(p)
}

func target(p string) fs.Target { return fs.Target{Key: key(p), Display: p} }
