// Package patch is the model-facing patch format (DH-D-FS-2): the "*** Begin Patch"
// envelope of add, delete and update hunks that the apply_patch tool takes, its
// line-fed parser, the matcher that applies an update hunk to a file's text, and
// Make, which writes a patch from two texts.
//
// The grammar and the matching rules are ported rule for rule from OpenAI Codex's
// apply-patch crate (codex-rs/apply-patch, tag rust-v0.158.0, Apache-2.0; see NOTICE):
//
//	*** Begin Patch
//	*** Add File: <path>        then "+" lines: the new file's content
//	*** Delete File: <path>
//	*** Update File: <path>     then an optional "*** Move to: <path>", then chunks:
//	@@ [context line]           opens a chunk; only the first may omit it
//	 context / -removed / +added lines; an empty line is an empty context line
//	*** End of File             optional: the chunk matches at the end of the file
//	*** End Patch
//
// Marker lines are compared with surrounding white space trimmed; a heredoc wrapper
// (<<EOF, <<'EOF' or <<"EOF" first, a last line ending in EOF) is tolerated; a
// trailing carriage return is dropped from every patch line.
//
// Apply normalises the text's line endings to LF, finds each chunk's old lines with
// four passes of decreasing strictness (exact, trailing white space ignored, white
// space at both ends ignored, then typographic dashes, quotes and spaces folded to
// ASCII), replaces them, and restores the text's dominant line ending. The result
// always ends with a line ending, as upstream's does; a failure returns a structured
// *Error and no text.
//
// Limit of the format: it cannot say "no newline at end of file", so a text whose
// last line lacks one gains it when a patch is applied.
//
//dsh:untrusted-parser patch text written by the model
package patch

import "fmt"

// Error reasons, in the harness's <domain>/<reason> table.
const (
	ReasonSyntax   = "patch/syntax"
	ReasonMismatch = "fs/patch-mismatch"
)

// Stages at which Apply can fail to match.
const (
	StageContext = "context" // the "@@ <context>" line of a chunk was not found
	StageLines   = "lines"   // the chunk's old lines were not found
)

// Error is a parse or apply failure.
type Error struct {
	reason string
	// Line is the 1-based line of the patch text the failure is about: the
	// offending line for a syntax error, the chunk's "@@" line (or first line) for
	// a mismatch.
	Line int
	// Path is the file of the hunk, for a mismatch.
	Path string
	// Stage is StageContext or StageLines, for a mismatch.
	Stage string
	// Msg is upstream's message.
	Msg string
}

// Reason is ReasonSyntax or ReasonMismatch.
func (e *Error) Reason() string { return e.reason }

func (e *Error) Error() string {
	if e.reason == ReasonMismatch {
		return fmt.Sprintf("%s: %s: chunk at patch line %d (%s): %s", e.reason, e.Path, e.Line, e.Stage, e.Msg)
	}
	if e.Line > 0 {
		return fmt.Sprintf("%s: line %d: %s", e.reason, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.reason, e.Msg)
}

func syntaxError(line int, format string, args ...any) *Error {
	return &Error{reason: ReasonSyntax, Line: line, Msg: fmt.Sprintf(format, args...)}
}
