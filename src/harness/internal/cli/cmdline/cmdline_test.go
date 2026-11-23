package cmdline

import (
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

var app = Spec{
	Name: "dsh headless",
	Options: []Option{
		{Name: "json", Kind: Bool, Help: "JSON output"},
		{Name: "layer", Kind: Strings, Value: "file", Help: "Add a layer"},
		{Name: "format", Kind: Choice, Choices: []string{"md", "json"}, Default: "md", Help: "Format"},
		{Name: "port", Kind: Int, Default: "80", Help: "Port"},
		{Name: "a", Kind: String}, {Name: "b", Kind: String}, {Name: "c", Kind: String}, {Name: "d", Kind: String},
	},
	Rules: []Rule{{Kind: OneOf, Names: []string{"a", "b"}}, {Kind: Together, Names: []string{"c", "d"}}},
	Args:  Args{Name: "task", Max: -1},
}

func usage(t *testing.T, err error, contains string) {
	t.Helper()
	if herr.Kind(err) != ReasonUsage || !strings.Contains(err.Error(), contains) {
		t.Errorf("err = %v, want a usage error containing %q", err, contains)
	}
}

// DH-LIB-COMMANDER-R3: options after free words are options; -- ends them.
//
//dsh:test DH-T-COMMANDER-2
func TestParseOptionsAfterFreeWords(t *testing.T) {
	v, err := Parse(app, []string{"run the tests", "--json", "--layer", "a", "--layer=b", "--port", "8"})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Bool("json") || !slices.Equal(v.Strings("layer"), []string{"a", "b"}) || v.Int("port") != 8 ||
		!slices.Equal(v.Args(), []string{"run the tests"}) || v.String("format") != "md" || v.Set("format") {
		t.Errorf("values = %+v", v)
	}
	v, err = Parse(app, []string{"--", "--json"})
	if err != nil || v.Bool("json") || !slices.Equal(v.Args(), []string{"--json"}) {
		t.Errorf("-- did not end options: %+v %v", v, err)
	}
}

//dsh:test DH-T-COMMANDER-1
func TestParseStopAtArgs(t *testing.T) {
	launcher := Spec{Name: "dsh", StopAtArgs: true, Options: []Option{{Name: "layer", Kind: Strings}}, Args: Args{Max: -1}}
	v, err := Parse(launcher, []string{"--layer", "a", "web", "--layer", "inner", "--help"})
	if err != nil || v.Help() {
		t.Fatal(err)
	}
	if !slices.Equal(v.Strings("layer"), []string{"a"}) || !slices.Equal(v.Args(), []string{"web", "--layer", "inner", "--help"}) {
		t.Errorf("values = %v %v", v.Strings("layer"), v.Args())
	}
}

//dsh:test DH-T-COMMANDER-2
func TestParseEnds(t *testing.T) {
	spec := Spec{Name: "dsh", Options: []Option{
		{Name: "profile", Short: "p", Kind: String, Ends: true}, {Name: "safe", Short: "s", Kind: Bool},
	}, Args: Args{Max: -1}}
	for _, words := range [][]string{
		{"-s", "--profile", "sdk", "--safe", "-h", "--", "x"},
		{"--safe", "--profile=sdk", "--safe", "-h", "--", "x"},
		{"-sp", "sdk", "--safe", "-h", "--", "x"},
		{"-spsdk", "--safe", "-h", "--", "x"},
	} {
		v, err := Parse(spec, words)
		if err != nil || v.Help() || !v.Bool("safe") || v.String("profile") != "sdk" ||
			!slices.Equal(v.Args(), []string{"--safe", "-h", "--", "x"}) {
			t.Errorf("Parse(%q) = %+v, %v", words, v, err)
		}
	}
}

//dsh:test DH-T-COMMANDER-2
func TestGiven(t *testing.T) {
	for words, want := range map[string]bool{
		"--json --nope":         true,
		"--nope --json=true":    true,
		"--json=false --nope":   false,
		"--json=bogus":          false,
		"--layer --json":        false,
		"-- --json":             false,
		"task --nope --json":    true,
		"--port 8 --json extra": true,
	} {
		if got := Given(app, strings.Fields(words), "json"); got != want {
			t.Errorf("Given(%q) = %v, want %v", words, got, want)
		}
	}
	launcher := Spec{Name: "dsh", StopAtArgs: true, Options: []Option{{Name: "safe", Kind: Bool}}, Args: Args{Max: -1}}
	if Given(launcher, []string{"web", "--safe"}, "safe") {
		t.Error("Given read past the first free word of a StopAtArgs spec")
	}
}

//dsh:test DH-T-COMMANDER-2
func TestParseUsageErrors(t *testing.T) {
	_, err := Parse(app, []string{"--nope"})
	usage(t, err, "unknown flag: --nope")
	if ue, _ := err.(*UsageError); ue == nil || ue.Option != "nope" {
		t.Errorf("option = %+v", err)
	}
	_, err = Parse(app, []string{"--format", "xml"})
	usage(t, err, "must be one of md, json")
	_, err = Parse(app, []string{"--a", "1", "--b", "2"})
	usage(t, err, "--a and --b cannot be used together")
	_, err = Parse(app, []string{"--c", "1"})
	usage(t, err, "--c, --d must be given together")
	_, err = Parse(Spec{Name: "x"}, []string{"extra"})
	usage(t, err, `unexpected argument "extra"`)
	_, err = Parse(Spec{Name: "x", Args: Args{Name: "shell", Min: 1, Max: 1}}, nil)
	usage(t, err, "missing <shell>")
}

// DH-LIB-COMMANDER-R4: help is a result, not a print.
//
//dsh:test DH-T-COMMANDER-3
func TestParseHelp(t *testing.T) {
	v, err := Parse(app, []string{"-h"})
	if err != nil || !v.Help() {
		t.Fatalf("help = %+v %v", v, err)
	}
}

//dsh:test DH-T-COMMANDER-3
func TestHelp(t *testing.T) {
	var b strings.Builder
	spec := Spec{Name: "dsh completion", Summary: "Print a completion script",
		Options:  []Option{{Name: "home", Kind: String, Value: "dir", Help: "Home"}},
		Args:     Args{Name: "shell", Help: "bash or zsh", Min: 1, Max: 1},
		Examples: []Example{{Command: "dsh completion bash", Help: "Bash"}}}
	if err := Help(&b, spec); err != nil {
		t.Fatal(err)
	}
	want := `Usage: dsh completion [options] <shell>

Print a completion script

Arguments:
  shell  bash or zsh

Options:
      --home <dir>  Home
  -h, --help        Show this help

Examples:
  dsh completion bash
      Bash
`
	if b.String() != want {
		t.Errorf("help =\n%s\nwant\n%s", b.String(), want)
	}
}
