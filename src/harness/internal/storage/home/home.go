// Package home is the layout of the harness home and its permission check
// (DH-D-STORAGE-2, DH-ADR-STORAGE-8, architecture.md §4.1). Every path below the
// home comes from a Layout, so no package builds a home path by hand.
//
// Ensure creates the directories private to their owner (0700 on POSIX; on Windows
// a protected access list naming only the current user, SYSTEM and Administrators)
// and then checks them. Check refuses a home whose directories are reachable by
// anybody else, so a world-readable home fails boot. A home on a network
// filesystem is refused.
package home

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Names below the home.
const (
	DBName     = "dsh.db"
	RunDir     = "run"
	SpillDir   = "spill"
	BlobsDir   = "blobs"
	ConfigDir  = "config"
	ReportsDir = "reports"
	SecretsDir = "secrets"
)

// Reasons of the errors Ensure and Check return.
const (
	ReasonNotPrivate   herr.Reason = "home/not-private"
	ReasonNotDirectory herr.Reason = "home/not-directory"
	ReasonRemote       herr.Reason = "home/remote"
	ReasonIO           herr.Reason = "home/io"
)

// Layout derives every path from the home boot resolved once.
type Layout struct{ root string }

// New returns the layout of the home at root.
func New(root string) Layout { return Layout{root: filepath.Clean(root)} }

// Root is the home itself; it holds dsh.db.
func (l Layout) Root() string { return l.root }

// DB is the one SQLite database (WAL files beside it).
func (l Layout) DB() string { return filepath.Join(l.root, DBName) }

// Run holds per-process liveness lock files and other runtime files.
func (l Layout) Run() string { return filepath.Join(l.root, RunDir) }

// Spill holds the private full text of oversized tool results.
func (l Layout) Spill() string { return filepath.Join(l.root, SpillDir) }

// Blobs holds content-addressed attachments.
func (l Layout) Blobs() string { return filepath.Join(l.root, BlobsDir) }

// Config holds the home and profile layers and the home's .env.
func (l Layout) Config() string { return filepath.Join(l.root, ConfigDir) }

// Reports holds the fatal-path report.
func (l Layout) Reports() string { return filepath.Join(l.root, ReportsDir) }

// Secrets holds the owner-only secret files.
func (l Layout) Secrets() string { return filepath.Join(l.root, SecretsDir) }

// Dirs returns every directory of the layout, the home first.
func (l Layout) Dirs() []string {
	return []string{l.root, l.Run(), l.Spill(), l.Blobs(), l.Config(), l.Reports(), l.Secrets()}
}

// Ensure creates the missing directories private to the current user and then
// checks the whole layout. An existing empty home is adopted as if created (it
// holds nothing yet); any other existing directory is never loosened or repaired: a
// wider one fails the check.
func Ensure(l Layout) error {
	for _, dir := range l.Dirs() {
		err := os.Mkdir(dir, 0o700)
		switch {
		case errors.Is(err, fs.ErrNotExist) && dir == l.root:
			if err = os.MkdirAll(filepath.Dir(dir), 0o700); err == nil {
				err = os.Mkdir(dir, 0o700)
			}
		case errors.Is(err, fs.ErrExist) && dir == l.root && isEmptyDir(dir):
			err = nil
		case errors.Is(err, fs.ErrExist):
			continue
		}
		if err != nil {
			return herr.Wrap(ReasonIO, err, "home: create "+l.name(dir))
		}
		if err := makePrivate(dir); err != nil {
			return herr.Wrap(ReasonIO, err, "home: make "+l.name(dir)+" private")
		}
	}
	return Check(l)
}

// Check verifies that the home is local, that every directory of the layout
// exists, is a directory (not a link) and is private to its owner, and that
// dsh.db, when present, is private too. The error names the first offending path
// relative to the home ("spill/").
func Check(l Layout) error {
	if remote, err := fsx.IsRemote(l.root); err == nil && remote {
		return herr.New(ReasonRemote, "home: the home is on a network filesystem; choose a local --home")
	}
	for _, dir := range l.Dirs() {
		info, err := os.Lstat(dir)
		if err != nil {
			return herr.Wrap(ReasonIO, err, "home: "+l.name(dir))
		}
		if !info.IsDir() {
			return herr.Newf(ReasonNotDirectory, "home: %s is not a directory", l.name(dir))
		}
		if problem := privateProblem(dir, info); problem != "" {
			return herr.Newf(ReasonNotPrivate, "home: %s is not private to its owner: %s", l.name(dir), problem)
		}
	}
	if info, err := os.Lstat(l.DB()); err == nil {
		if problem := privateProblem(l.DB(), info); problem != "" {
			return herr.Newf(ReasonNotPrivate, "home: %s is not private to its owner: %s", DBName, problem)
		}
	}
	return nil
}

// CheckPrivate reports whether the file or directory at path is private to its
// owner, by the same rule Check applies. The error names path's base only.
func CheckPrivate(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return herr.Wrap(ReasonIO, err, "home: "+filepath.Base(path))
	}
	if problem := privateProblem(path, info); problem != "" {
		return herr.Newf(ReasonNotPrivate, "home: %s is not private to its owner: %s", filepath.Base(path), problem)
	}
	return nil
}

// name is dir relative to the home, with a trailing slash: "spill/", "./".
func (l Layout) name(dir string) string {
	rel, err := filepath.Rel(l.root, dir)
	if err != nil || rel == "." {
		return "the home"
	}
	return filepath.ToSlash(rel) + "/"
}

// isEmptyDir reports a real directory (not a link) with no entries.
func isEmptyDir(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 0
}

func privateProblem(path string, info fs.FileInfo) string {
	if info.Mode()&fs.ModeSymlink != 0 {
		return "it is a symbolic link"
	}
	return checkPrivate(path, info)
}
