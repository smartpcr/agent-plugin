// Package search lists and searches the files of a workspace in-process: content
// search with a regular expression (the grep tool), file listing with a glob (the
// glob tool) and the file-name listing for the UI. It replaces upstream's ripgrep
// child process (docs/harness/libs/vscode-ripgrep.md §6).
//
// A search reads only through the caller's Source, so a confined view confines
// the search; no environment variable, configuration file or executable can change
// a result. Every walk is in name order, and content-search results are released in
// walk order, so the same search over the same files gives the same results.
//
// Ignore rules: .gitignore files at every level, .ignore files (which take
// precedence over .gitignore at the same level) and .git/info/exclude are honoured
// unless NoIgnore is set; names that start with a dot are skipped unless Hidden is
// set. The contents of version-control directories (.git, .svn, .hg, .bzr, .jj,
// .sl) are never listed or searched. The user's global Git ignore file is not read.
//
// Cut-short results: a limit, cancellation or an unreadable directory sets
// Summary.Incomplete and names the cause in Summary.Reason; a result is never
// returned as if complete when it is not.
package search

import (
	"errors"
	"fmt"
)

// Failure reasons carried by *Error.
const (
	ReasonInvalidPattern = "search/invalid-pattern"
	ReasonFailed         = "search/failed"
	ReasonAborted        = "search/aborted"
)

// Causes named in Summary.Reason when Incomplete is set.
const (
	CutMatches    = "limit: matches"
	CutFiles      = "limit: files"
	CutBytes      = "limit: bytes"
	CutUnreadable = "unreadable"
	CutStopped    = "stopped by caller"
	CutAborted    = "aborted"
)

// Error is a search that could not run (invalid pattern, unusable root) or was
// cancelled. Reason is one of the Reason constants.
type Error struct {
	reason string
	Msg    string
	Err    error
}

// Reason is the error's stable reason code.
func (e *Error) Reason() string { return e.reason }

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.reason, e.Msg, e.Err)
	}
	return e.reason + ": " + e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func invalidPattern(what, pattern string, err error) *Error {
	return &Error{reason: ReasonInvalidPattern, Msg: fmt.Sprintf("%s %q", what, pattern), Err: err}
}

// IsInvalidPattern reports whether err is the invalid-pattern error, the one a
// tool reports to the model as "fix your pattern" rather than "search failed".
func IsInvalidPattern(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.reason == ReasonInvalidPattern
}

// Limits bound a search. A zero field takes its default; a negative one means no
// bound.
type Limits struct {
	// MaxMatches bounds matching lines for Grep (default 250) and returned paths
	// for Files (default: no bound beyond MaxFiles).
	MaxMatches int
	// MaxLineBytes bounds the text of a reported line (default 2000); a longer
	// line is cut at a character boundary and marked Truncated.
	MaxLineBytes int
	// MaxFiles bounds the files visited (default 100,000).
	MaxFiles int
	// MaxBytes bounds the file content Grep reads in total (default 1 GiB).
	MaxBytes int64
}

// Default limits.
const (
	DefaultMaxMatches   = 250
	DefaultMaxLineBytes = 2000
	DefaultMaxFiles     = 100_000
	DefaultMaxBytes     = 1 << 30
)

func pick[T int | int64](v, def T) T {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0 // no bound
	}
	return v
}

// Summary says how a search ended.
type Summary struct {
	Files      int // files visited
	Matches    int // lines (Grep) or paths (Files) emitted
	Incomplete bool
	Reason     string // one of the Cut constants when Incomplete
}

func (s *Summary) cut(reason string) {
	if !s.Incomplete {
		s.Incomplete, s.Reason = true, reason
	}
}
