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
	"strings"

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

// syncDir makes a new directory entry durable; tests replace it.
var syncDir = fsx.SyncDir

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
			if err = mkdirParents(filepath.Dir(dir)); err == nil {
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
	// Every layout directory is the home or a child of it, so syncing the home's
	// parent and the home makes all their entries durable, adopted ones included
	// (an earlier Ensure may have failed before its sync).
	for _, d := range []string{filepath.Dir(l.root), l.root} {
		if err := syncDir(d); err != nil {
			return herr.Wrap(ReasonIO, err, "home: sync the entries of the layout")
		}
	}
	return Check(l)
}

// mkdirParents creates the missing directories of the path dir one level at a
// time, each 0700, and syncs the parent of each so that its entry survives a
// crash (MkdirAll syncs nothing). A level whose parent cannot be synced is removed
// again, so a retry creates and syncs it instead of adopting an entry that may not
// be durable.
func mkdirParents(dir string) error {
	var missing []string
	for d := dir; ; {
		_, err := os.Lstat(d)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		missing = append(missing, d)
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	for i := len(missing) - 1; i >= 0; i-- {
		d := missing[i]
		err := os.Mkdir(d, 0o700)
		created := err == nil
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := syncDir(filepath.Dir(d)); err != nil {
			if created {
				_ = os.Remove(d)
			}
			return err
		}
	}
	return nil
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

// EnsurePrivateDir creates dir below the layout (its parent must exist) private
// to the current user, and checks it: an existing dir is adopted only when it is a
// real directory (not a link) that is already private; it is never loosened or
// repaired. The stores under spill/ and blobs/ create their subdirectories with it.
func EnsurePrivateDir(dir string) error {
	err := os.Mkdir(dir, 0o700)
	switch {
	case err == nil:
		if err := makePrivate(dir); err != nil {
			return herr.Wrap(ReasonIO, err, "home: make "+filepath.Base(dir)+" private")
		}
	case !errors.Is(err, fs.ErrExist):
		return herr.Wrap(ReasonIO, err, "home: create "+filepath.Base(dir))
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return herr.Wrap(ReasonIO, err, "home: "+filepath.Base(dir))
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return herr.Newf(ReasonNotDirectory, "home: %s is not a directory", filepath.Base(dir))
	}
	if err := CheckPrivate(dir); err != nil {
		return err
	}
	// An entry survives a crash only once its parent is synced. An adopted
	// directory is synced too: its creator may have failed, or not yet reached,
	// that sync, and nothing may be published below an entry that is not durable.
	if err := syncDir(filepath.Dir(dir)); err != nil {
		return herr.Wrap(ReasonIO, err, "home: sync the entry of "+filepath.Base(dir))
	}
	return nil
}

// CreatePrivateFile creates the file at path exclusively and private to the
// current user from its first byte: mode 0600 on POSIX; on Windows a protected
// access list naming only the current user, SYSTEM and Administrators, given at
// creation. Anything already at path, a symbolic link included, fails the create
// with an error matching fs.ErrExist; a link is never followed.
func CreatePrivateFile(path string) (*os.File, error) { return createPrivate(path) }

// SafeSegment makes a suggested file name one safe path segment of at most max
// bytes: only ASCII letters, digits, '.', '_' and '-' are kept (any other byte, a
// separator or NUL among them, becomes '_'), leading and trailing dots are
// dropped (so "." and ".." are never a name), and a Windows device name (CON,
// NUL, COM1 ...) gets a leading '_'. A result of nothing but '_' is fallback.
func SafeSegment(name string, max int, fallback string) string {
	var b strings.Builder
	for i := 0; i < len(name) && b.Len() < max; i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	s := strings.Trim(b.String(), ".")
	if strings.Trim(s, "_") == "" {
		return fallback
	}
	if isDeviceName(s) {
		s = "_" + s
		if len(s) > max {
			s = strings.TrimRight(s[:max], ".")
		}
	}
	return s
}

// isDeviceName reports a Windows reserved device name, with or without an
// extension.
func isDeviceName(s string) bool {
	base := strings.ToUpper(s)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) &&
		base[3] >= '0' && base[3] <= '9'
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

// CheckPrivateFile is CheckPrivate for an open file: it checks the object f
// refers to, so the answer holds for the bytes read from f even if the name is
// swapped afterwards. The error names f's base only.
func CheckPrivateFile(f *os.File) error {
	base := filepath.Base(f.Name())
	info, err := f.Stat()
	if err != nil {
		return herr.Wrap(ReasonIO, err, "home: "+base)
	}
	if problem := checkPrivateFile(f, info); problem != "" {
		return herr.Newf(ReasonNotPrivate, "home: %s is not private to its owner: %s", base, problem)
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
