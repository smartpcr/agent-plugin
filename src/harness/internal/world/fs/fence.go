package fs

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// The fs error codes (filesystem.md sec. 6 "Errors"). The seam owns them; ops and
// the tools return them unchanged.
const (
	CodeNotFound       herr.Reason = "fs/not-found"
	CodeNotDirectory   herr.Reason = "fs/not-directory"
	CodeNotText        herr.Reason = "fs/not-text"
	CodeNotRegularFile herr.Reason = "fs/not-regular-file"
	CodeTooLarge       herr.Reason = "fs/too-large"
	CodePermission     herr.Reason = "fs/permission-denied"
	CodeSandboxDenied  herr.Reason = "fs/sandbox-denied"
	CodeIO             herr.Reason = "fs/io"
	CodeStaleVersion   herr.Reason = "fs/stale-version"
	CodeNotObserved    herr.Reason = "fs/not-observed"
	CodeAmbiguousEdit  herr.Reason = "fs/ambiguous-edit"
	CodeEditNotFound   herr.Reason = "fs/edit-not-found"
	CodeAborted        herr.Reason = "fs/aborted"
	CodePatchMismatch  herr.Reason = "fs/patch-mismatch"
	CodeSameTarget     herr.Reason = "fs/same-target"
	CodeAlreadyExists  herr.Reason = "fs/already-exists"
	CodeTimeout        herr.Reason = "fs/timeout"
	CodeInvalidPath    herr.Reason = "fs/invalid-path"
)

// Codes lists every fs code.
var Codes = []herr.Reason{
	CodeNotFound, CodeNotDirectory, CodeNotText, CodeNotRegularFile, CodeTooLarge,
	CodePermission, CodeSandboxDenied, CodeIO, CodeStaleVersion, CodeNotObserved,
	CodeAmbiguousEdit, CodeEditNotFound, CodeAborted, CodePatchMismatch,
	CodeSameTarget, CodeAlreadyExists, CodeTimeout, CodeInvalidPath,
}

func errorf(r herr.Reason, format string, args ...any) error { return herr.Newf(r, format, args...) }

// Op is what a call does to one target; the fence classifies by it.
type Op string

// The operations.
const (
	OpRead            Op = "read"
	OpWrite           Op = "write"
	OpEdit            Op = "edit"
	OpPatch           Op = "patch"
	OpDelete          Op = "delete"
	OpMoveSource      Op = "move-source"
	OpMoveDestination Op = "move-destination"
)

// Mutates reports whether op changes the filesystem.
func (op Op) Mutates() bool {
	switch op {
	case OpWrite, OpEdit, OpPatch, OpDelete, OpMoveSource, OpMoveDestination:
		return true
	}
	return false
}

// Fence decides whether op may touch the resolved, canonical path under p
// (filesystem.md sec. 6 "The fence"): a zero p is sandbox/unavailable; a read is
// allowed; under read-only every mutation is fs/sandbox-denied; under
// workspace-write a mutation is allowed only at or below a write root; under
// danger-full-access it is allowed. Providers fence the path after following
// symlinks, so a link inside a root to a file outside is refused.
func Fence(p sandbox.Policy, op Op, path string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !op.Mutates() {
		return nil
	}
	switch p.Mode() {
	case sandbox.DangerFullAccess:
		return nil
	case sandbox.WorkspaceWrite:
		for _, r := range p.WriteRoots() {
			if Within(r.Path, path) {
				return nil
			}
		}
		return errorf(CodeSandboxDenied, "%s %s is outside the writable roots of workspace-write", op, path)
	default:
		return errorf(CodeSandboxDenied, "%s %s is refused in %s mode", op, path, p.Mode())
	}
}

// Within reports whether path is root or below it, lexically, both absolute and
// clean; case-insensitively on Windows.
func Within(root, path string) bool {
	if runtime.GOOS == "windows" {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
