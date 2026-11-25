package local

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// keepsReadOnly reports a backend that can keep a path read-only inside a write
// root: bubblewrap mounts the harness binary and the harness home read-only over
// the roots (BwrapArgv). Landlock only adds access beneath a root, so under it a
// root is writable all the way down.
func keepsReadOnly(b sandbox.Backend) bool { return b == sandbox.BackendBwrap }

// protect is Prepare's refusal of DH-ADR-SANDBOX-5 (a): a confined policy under
// which the running harness binary or any part of the harness home would be
// writable is refused, unless backend keeps it read-only inside the root. The
// session scratch and a plugin's state directory are the only parts of the home
// that are ever a write root, under every backend; a root that holds the whole
// home, or the binary, is refused only where the backend cannot overlay it.
//
// Every path is compared both as written and as resolved (canonical): a root
// reached through a symbolic link in any ancestor names the directory the
// backend will actually open, so a root /alias/sub with /alias pointing into
// the home is the home. A path that cannot be resolved refuses the start.
func protect(backend sandbox.Backend, h Host, roots []sandbox.WriteRoot) error {
	overlay := keepsReadOnly(backend)
	homes, err := forms(h.Home)
	if err != nil {
		return err
	}
	exes, err := forms(h.Exe)
	if err != nil {
		return err
	}
	for _, r := range roots {
		paths, err := forms(r.Path)
		if err != nil {
			return err
		}
		for _, root := range paths {
			for _, home := range homes {
				if within(home, root) && r.Source != sandbox.SourceScratch && !pluginState(home, root) {
					return herr.Newf(sandbox.CodeUnavailable,
						"write root %s lies inside the harness home %s and is neither the session scratch nor a plugin's state directory; it was not started", r.Path, h.Home)
				}
			}
		}
		if overlay {
			continue
		}
		for _, root := range paths {
			for _, home := range homes {
				if within(root, home) && root != home {
					return herr.Newf(sandbox.CodeUnavailable,
						"write root %s holds the harness home %s, which %s cannot keep read-only inside a root; it was not started", r.Path, h.Home, backend)
				}
			}
			for _, exe := range exes {
				if within(root, exe) {
					return herr.Newf(sandbox.CodeUnavailable,
						"write root %s holds the harness binary %s, which %s cannot keep read-only inside a root; it was not started", r.Path, exe, backend)
				}
			}
		}
	}
	return nil
}

// forms is p as written and, when it differs, p resolved; none for an empty p.
func forms(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	r, err := resolve(p)
	if err != nil {
		return nil, herr.Wrap(sandbox.CodeUnavailable, err,
			"cannot resolve "+p+" to tell whether it would make the harness writable; it was not started")
	}
	if r == filepath.Clean(p) {
		return []string{p}, nil
	}
	return []string{p, r}, nil
}

// resolve is p with every symbolic link resolved. A part of p that does not
// exist yet cannot be a link, so the nearest existing ancestor is resolved and
// the rest appended; any other failure (a loop, a denied search) is an error.
func resolve(p string) (string, error) {
	p = filepath.Clean(p)
	r, err := filepath.EvalSymlinks(p)
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(p)
	if parent == p {
		return "", err
	}
	rp, err := resolve(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(rp, filepath.Base(p)), nil
}

// pluginState reports that path is a plugin's state directory, or lies below
// one: <home>/plugins/<id>/state, or <home>/plugins/attached/<session>/<id>/state
// for an attached plugin (extensions.md, "Persisted data").
func pluginState(home, path string) bool {
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 || parts[0] != "plugins" {
		return false
	}
	if parts[1] == "attached" {
		return len(parts) >= 5 && parts[2] != ".." && parts[3] != ".." && parts[4] == "state"
	}
	return parts[1] != ".." && parts[2] == "state"
}
