package local

import (
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
func protect(backend sandbox.Backend, h Host, roots []sandbox.WriteRoot) error {
	overlay := keepsReadOnly(backend)
	exes := []string{h.Exe}
	if h.Exe != "" {
		if r, err := filepath.EvalSymlinks(h.Exe); err == nil && r != h.Exe {
			exes = append(exes, r)
		}
	}
	for _, r := range roots {
		if h.Home != "" && within(h.Home, r.Path) && r.Source != sandbox.SourceScratch && !pluginState(h.Home, r.Path) {
			return herr.Newf(sandbox.CodeUnavailable,
				"write root %s lies inside the harness home %s and is neither the session scratch nor a plugin's state directory; it was not started", r.Path, h.Home)
		}
		if overlay {
			continue
		}
		if h.Home != "" && within(r.Path, h.Home) && r.Path != h.Home {
			return herr.Newf(sandbox.CodeUnavailable,
				"write root %s holds the harness home %s, which %s cannot keep read-only inside a root; it was not started", r.Path, h.Home, backend)
		}
		for _, exe := range exes {
			if exe != "" && within(r.Path, exe) {
				return herr.Newf(sandbox.CodeUnavailable,
					"write root %s holds the harness binary %s, which %s cannot keep read-only inside a root; it was not started", r.Path, exe, backend)
			}
		}
	}
	return nil
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
