package expr

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

type gitView struct {
	Branch string
	Dirty  bool
	Tags   []string
}

type gitService struct{ view any }

func (g *gitService) ExprView() any { return g.view }

func newEvaluator(t *testing.T) *Evaluator {
	t.Helper()
	env := map[string]string{"EDITOR": "vim", "API_TOKEN": "s3cret"}
	e, err := New(Options{
		Env:    func(name string) (string, bool) { v, ok := env[name]; return v, ok },
		Secret: func(name string) bool { return strings.HasSuffix(name, "_TOKEN") },
		OS:     "linux",
		Cwd:    "/work",
		Home:   filepath.FromSlash("/home/u/.dsh"),
		Launch: Launch{Profile: "dev", Home: "/home/u/.dsh", Cwd: "/work", Version: "1.2.3", TTY: true, Safe: false},
		Views:  map[string]reflect.Type{"git": reflect.TypeFor[gitView]()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// DH-T-LOADER-6: each name returns its value; an unknown name fails when checked.
func TestNamesResolve(t *testing.T) {
	e := newEvaluator(t)
	cases := []struct {
		source string
		want   any
	}{
		{`env.EDITOR`, "vim"},
		{`env.UNSET == nil`, true},
		{`env.UNSET ?? "nano"`, "nano"},
		{`os`, "linux"},
		{`cwd`, "/work"},
		{`launch.profile`, "dev"},
		{`launch.home`, "/home/u/.dsh"},
		{`launch.cwd`, "/work"},
		{`launch.version`, "1.2.3"},
		{`launch.tty`, true},
		{`launch.safe`, false},
		{`home("plugins", "x")`, filepath.Join(filepath.FromSlash("/home/u/.dsh"), "plugins", "x")},
		{`home()`, filepath.FromSlash("/home/u/.dsh")},
		{`upper(launch.profile) + "-" + string(len([1, 2]))`, "DEV-2"},
		{`os == "windows" ? 1 : 2`, 2},
	}
	for _, c := range cases {
		for _, kind := range []loader.ScopeKind{loader.DisabledScope, loader.ConfigScope} {
			if err := e.Check(c.source, kind); err != nil {
				t.Fatalf("Check(%q, %s): %v", c.source, kind, err)
			}
			got, err := e.Eval(c.source, loader.Scope{Kind: kind})
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Eval(%q, %s) = %#v, %v; want %#v", c.source, kind, got, err, c.want)
			}
		}
	}
}

func TestRejected(t *testing.T) {
	e := newEvaluator(t)
	cases := []struct {
		source string
		kind   loader.ScopeKind
		want   string
	}{
		{`undefinedName`, loader.ConfigScope, "undefinedName"},
		{`launch.words`, loader.ConfigScope, "words"},
		{`use.git.Branch`, loader.DisabledScope, "use is not available in disabled"},
		{`use`, loader.DisabledScope, "use is not available"},
		{`use.svn.Branch`, loader.ConfigScope, `service "svn" offers no view`},
		{`use["g" + "it"]`, loader.ConfigScope, "literal name"},
		{`env[launch.profile]`, loader.ConfigScope, "literal name"},
		{`env`, loader.ConfigScope, "literal name"},
		{`len(env)`, loader.ConfigScope, "literal name"},
		{`env.API_TOKEN`, loader.ConfigScope, "API_TOKEN is a secret"},
		{`now()`, loader.ConfigScope, "now"},
		{`date("2020-01-01")`, loader.ConfigScope, "date"},
		{`duration("1s")`, loader.ConfigScope, "duration"},
		{`timezone("UTC")`, loader.ConfigScope, "timezone"},
		{`readFile("/etc/passwd")`, loader.ConfigScope, "readFile"},
		{`$env.cwd`, loader.ConfigScope, "$env"},
		{`launch.profile.String()`, loader.ConfigScope, "only home"},
		{`(home)("a")`, loader.ConfigScope, ""},
		{`1 +`, loader.ConfigScope, "unexpected"},
		{`   `, loader.ConfigScope, "empty"},
		{`home("..", "etc")`, loader.ConfigScope, ""},
	}
	for _, c := range cases {
		err := e.Check(c.source, c.kind)
		if err == nil {
			_, err = e.Eval(c.source, loader.Scope{Kind: c.kind})
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%q in %s: got %v, want an error containing %q", c.source, c.kind, err, c.want)
		}
	}
}

func TestHome(t *testing.T) {
	e := newEvaluator(t)
	for _, source := range []string{`home("..")`, `home("a/../../b")`, `home("/etc")`, `home(1)`} {
		if _, err := e.Eval(source, loader.Scope{Kind: loader.ConfigScope}); err == nil || !strings.Contains(err.Error(), "home") {
			t.Fatalf("%s: got %v", source, err)
		}
	}
	got, err := e.Eval(`home("a/../b")`, loader.Scope{Kind: loader.ConfigScope})
	if err != nil || got != filepath.Join(filepath.FromSlash("/home/u/.dsh"), "b") {
		t.Fatalf("got %v, %v", got, err)
	}
}

// DH-T-LOADER-6: no builtin reaches the filesystem, network, processes or the clock.
func TestOnlyPureBuiltins(t *testing.T) {
	for _, name := range Builtins() {
		switch name {
		case "now", "date", "timezone", "duration":
			t.Fatalf("clock builtin %s is enabled", name)
		}
	}
	e := newEvaluator(t)
	for _, name := range []string{"now", "date", "timezone", "duration", "readFile", "exec", "getenv", "http"} {
		if err := e.Check(name+`("x")`, loader.ConfigScope); err == nil {
			t.Fatalf("%s is callable", name)
		}
	}
}

func TestUse(t *testing.T) {
	e := newEvaluator(t)
	ctx := cordis.New()
	view := &gitService{view: gitView{Branch: "main", Tags: []string{"v1"}}}
	if _, err := ctx.Provide("git", view); err != nil {
		t.Fatal(err)
	}
	scope := loader.Scope{Kind: loader.ConfigScope, EntryID: "x", Context: ctx}

	got, err := e.Eval(`use.git.Branch + ":" + string(use.git.Dirty) + ":" + use.git.Tags[0]`, scope)
	if err != nil || got != "main:false:v1" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := e.Eval(`use.git.Branch`, loader.Scope{Kind: loader.ConfigScope}); err == nil {
		t.Fatal("use without a context succeeded")
	}
	view.view = 42
	if _, err := e.Eval(`use.git.Branch`, scope); err == nil || !strings.Contains(err.Error(), "declared") {
		t.Fatalf("wrong view type: %v", err)
	}
	if err := ctx.Set("git", "not a viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Eval(`use.git.Branch`, scope); err == nil || !strings.Contains(err.Error(), "no view") {
		t.Fatalf("non-viewer: %v", err)
	}
}

func TestUseUnresolved(t *testing.T) {
	e := newEvaluator(t)
	_, err := e.Eval(`use.git.Branch`, loader.Scope{Kind: loader.ConfigScope, Context: cordis.New()})
	if err == nil || !strings.Contains(err.Error(), "use.git") {
		t.Fatalf("got %v", err)
	}
}

// tree and forest contain themselves; badTree does and has a pointer; secretTree has
// a secret only below the first level. loop and dict contain themselves without a
// struct in between; badDict does and holds a function.
type (
	loop    []loop
	dict    map[string]dict
	badDict map[string]struct {
		D badDict
		F func()
	}
	tree struct {
		Name     string
		Children []tree
	}
	forest struct {
		Trees map[string]forest
		Root  tree
	}
	badTree struct {
		Children []badTree
		P        *int
	}
	secretTree struct {
		Children []secretTree
		Auth     struct {
			Token string `dsh:"live,secret"`
		}
	}
)

func TestCheckView(t *testing.T) {
	type nested struct{ A map[string][]int }
	type creds struct {
		User  string
		Token string `json:"token" dsh:"required,secret"`
	}
	type unexported struct{ a int }
	good := []reflect.Type{reflect.TypeFor[gitView](), reflect.TypeFor[struct {
		N nested
		F [2]float64
		U uint8
	}](), reflect.TypeFor[tree](), reflect.TypeFor[forest](), reflect.TypeFor[struct {
		L  loop
		D  dict
		Ls []loop
	}](), reflect.TypeFor[struct {
		S string `dsh:"pattern=secret,x"`
		L string `dsh:"live"`
	}]()}
	for _, typ := range good {
		if err := CheckView(typ); err != nil {
			t.Fatalf("%v: %v", typ, err)
		}
	}
	bad := []reflect.Type{nil, reflect.TypeFor[int](), reflect.TypeFor[struct{ P *int }](),
		reflect.TypeFor[struct{ F func() }](), reflect.TypeFor[struct{ M map[int]string }](),
		reflect.TypeFor[struct{ I any }](), reflect.TypeFor[unexported](), reflect.TypeFor[badTree](),
		reflect.TypeFor[struct{ B badDict }](), reflect.TypeFor[struct {
			L []loop
			P *int
		}]()}
	for _, typ := range bad {
		if err := CheckView(typ); err == nil {
			t.Fatalf("%v accepted", typ)
		}
	}
	for typ, field := range map[reflect.Type]string{
		reflect.TypeFor[creds]():                          "creds.Token",
		reflect.TypeFor[struct{ C []map[string]creds }](): "C[][].Token",
		reflect.TypeFor[secretTree]():                     "secretTree.Auth.Token",
	} {
		if err := CheckView(typ); err == nil || !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), "secret") {
			t.Fatalf("CheckView(%v) = %v, want a secret refusal naming %s", typ, err, field)
		}
	}
	if _, err := New(Options{Views: map[string]reflect.Type{"x": reflect.TypeFor[struct{ P *int }]()}}); err == nil {
		t.Fatal("New accepted a non-plain view")
	}
}

func TestDefaultsAndCache(t *testing.T) {
	e, err := New(Options{MaxNodes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.Eval(`env.ANY`, loader.Scope{}); err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, _ := e.Eval(`os`, loader.Scope{}); got == "" {
		t.Fatal("os is empty")
	}
	if err := e.Check(`1 + 2 + 3 + 4 + 5`, loader.ConfigScope); err == nil {
		t.Fatal("MaxNodes not applied")
	}
	if e.compile(`os`, loader.DisabledScope) != e.compile(`os`, loader.ScopeKind(9)) {
		t.Fatal("programs are not cached by scope")
	}
}

// The loader's disabled expressions run through this evaluator.
func TestBehindLoaderSeam(t *testing.T) {
	var _ loader.Evaluator = newEvaluator(t)
	e := newEvaluator(t)
	got, err := e.Eval(`launch.safe || env.EDITOR == "vim"`, loader.Scope{Kind: loader.DisabledScope})
	if err != nil || got != true {
		t.Fatalf("got %v, %v", got, err)
	}
}
