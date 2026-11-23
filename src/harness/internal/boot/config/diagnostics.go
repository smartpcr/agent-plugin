package config

import (
	"errors"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// FrameIssue is issue as a diagnostic a person reads (DH-O-2, DH-LIB-CODEFRAME):
// its one line ("file:line:column: path: message") and, when it names a line and
// src is the text of its file, the code frame of herr.Frame under it: the offending
// line, a caret at the column and the message. Without a line or a text it is the
// one line alone. The frame is plain text, safe for a log or a model.
func FrameIssue(src []byte, issue loader.Issue) string {
	line := issue.Error()
	if !issue.Pos.IsValid() || src == nil {
		return line
	}
	return line + "\n" + herr.Frame(string(src), issue.Pos.Line, issue.Pos.Column, issue.Message)
}

// Diagnostics is every issue of the report as FrameIssue gives it, in order: an
// issue in a layer file the report was given the text of (CheckOptions.Docs) is
// framed in that text, any other is its one line.
func (r *Report) Diagnostics() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.Issues))
	for i, issue := range r.Issues {
		out[i] = FrameIssue(r.source(issue.Pos.File), issue)
	}
	return out
}

// source is the text of the layer file named file, or nil when the report holds
// none.
func (r *Report) source(file string) []byte {
	if file == "" {
		return nil
	}
	for _, d := range r.docs {
		if d != nil && d.File == file {
			return d.Data
		}
	}
	return nil
}

// Diagnose is the diagnostics of a failed decode of data: each issue of the
// *loader.ParseError in err's chain (the YAML codec places a syntax error at its
// line and column, see parseError) framed in data; any other error is its one
// line, and a nil error none.
func Diagnose(data []byte, err error) []string {
	if err == nil {
		return nil
	}
	var pe *loader.ParseError
	if !errors.As(err, &pe) {
		return []string{err.Error()}
	}
	out := make([]string, len(pe.Issues))
	for i, issue := range pe.Issues {
		out[i] = FrameIssue(data, issue)
	}
	return out
}
