package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
)

// A YAML layer that indents a line with a tab is rejected at that line, and its
// diagnostic shows the line, a caret under the column and the message, with no
// escape byte.
//
//dsh:test DH-T-CODEFRAME-1
func TestDiagnoseFramesAYAMLSyntaxError(t *testing.T) {
	data := []byte("- id: probe\n  name: x.probe\n\tconfig: {}\n")
	_, err := config.YAML.Decode("layer.yaml", data)
	var pe *loader.ParseError
	if !errors.As(err, &pe) || len(pe.Issues) != 1 || pe.Issues[0].Pos.Line != 3 {
		t.Fatalf("Decode of a tab-indented line = %v; want one issue on line 3", err)
	}
	got := config.Diagnose(data, err)
	if len(got) != 1 {
		t.Fatalf("Diagnose = %q; want one diagnostic", got)
	}
	lines := strings.Split(got[0], "\n")
	issue := pe.Issues[0]
	if lines[0] != issue.Error() {
		t.Errorf("first line %q; want the issue %q", lines[0], issue.Error())
	}
	if !strings.Contains(got[0], "> 3 | \tconfig: {}") || !strings.Contains(got[0], "^ "+issue.Message) {
		t.Errorf("diagnostic lacks the marked line or the caret and message:\n%s", got[0])
	}
	if strings.ContainsAny(got[0], "\x1b\r") {
		t.Errorf("diagnostic holds an escape character: %q", got[0])
	}

	if got := config.Diagnose(data, errors.New("plain failure")); len(got) != 1 || got[0] != "plain failure" {
		t.Errorf("Diagnose of a plain error = %q", got)
	}
	if got := config.Diagnose(data, nil); got != nil {
		t.Errorf("Diagnose(nil) = %q", got)
	}
}

// A checked report frames each issue in the text of the layer that wrote it; an
// issue with no position, or in a file the report has no text of, is its one
// line.
//
//dsh:test DH-T-CODEFRAME-1
func TestReportDiagnosticsFrameEachIssue(t *testing.T) {
	p := newProbes(t)
	bundle, bdoc := layer(t, "bundle:core", "core.yml", bundleText)
	userText := "- id: probe\n  merge:\n    colour: red\n"
	user, udoc := layer(t, "user", "user.yml", userText)
	r := config.Check([]loader.Layer{bundle, user}, config.CheckOptions{Catalog: p.cat,
		Docs: map[string]*loader.LayerDocument{"bundle:core": bdoc, "user": udoc}, Required: []string{"absent"}})
	diags := r.Diagnostics()
	if len(diags) != len(r.Issues) || len(diags) < 2 {
		t.Fatalf("diagnostics %q for issues %v", diags, r.Issues)
	}
	framed, plain := 0, 0
	for i, d := range diags {
		issue := r.Issues[i]
		switch {
		case issue.Pos.File == "user.yml":
			framed++
			if !strings.HasPrefix(d, issue.Error()+"\n") || !strings.Contains(d, "> 3 |     colour: red") || !strings.Contains(d, "^ "+issue.Message) {
				t.Errorf("issue %v framed as:\n%s", issue, d)
			}
		case !issue.Pos.IsValid():
			plain++
			if d != issue.Error() {
				t.Errorf("issue without a position = %q; want %q", d, issue.Error())
			}
		}
	}
	if framed == 0 || plain == 0 {
		t.Fatalf("framed %d, plain %d of %q; want both kinds", framed, plain, diags)
	}

	// A file the report holds no text of stays one line.
	issue := loader.Issue{Pos: loader.Position{File: "elsewhere.yml", Line: 2, Column: 1}, Message: "m"}
	if got := config.FrameIssue(nil, issue); got != issue.Error() {
		t.Errorf("FrameIssue without text = %q", got)
	}
	var none *config.Report
	if none.Diagnostics() != nil {
		t.Error("a nil report has diagnostics")
	}
}
