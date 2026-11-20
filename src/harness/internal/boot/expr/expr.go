// Package expr is the harness expression engine (DH-D-LOADER-5, DH-ADR-LOADER-3,
// DH-ADR-BOOT-5): the loader.Evaluator behind the loader's expression seam. An
// expression reads data and nothing else. The names are the ones of DH-ADR-LOADER-3:
//
//	env.NAME            launch environment variable, nil when unset; secret names refused
//	os                  "windows", "linux" or "darwin"
//	cwd                 working directory
//	home(part, ...)     path under the harness home (a pure join)
//	launch.<key>        profile, home, cwd, version, os, tty, safe
//	use.<service>.<f>   declared data view of an injected service; config only
//
// Expressions are compiled against a typed environment, so an unknown name fails
// when the expression is checked. Only pure builtins exist: none reads a file, the
// network, a process or the clock, and no method of any value can be called.
package expr

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"

	"github.com/smartpcr/agent-plugin/src/go/loader"
)

// Launch holds the launch facts an expression reads as launch.<key> (area A21,
// boot.md §6.2).
type Launch struct {
	Profile string `expr:"profile"`
	Home    string `expr:"home"`
	Cwd     string `expr:"cwd"`
	Version string `expr:"version"`
	OS      string `expr:"os"`
	TTY     bool   `expr:"tty"`
	Safe    bool   `expr:"safe"`
}

// Viewer is a service that offers a data view to expressions, as use.<service>.
type Viewer interface {
	ExprView() any
}

// Options are what the evaluator reads. Every field is a frozen launch fact.
type Options struct {
	// Env looks up a variable of the launch environment; nil means none is set.
	Env func(name string) (string, bool)
	// Secret reports a secret variable name, which env refuses; nil means none is.
	Secret func(name string) bool
	// OS is the platform; empty means runtime.GOOS.
	OS string
	// Cwd is the working directory.
	Cwd string
	// Home is the harness home, under which home(...) joins.
	Home string
	// Launch are the launch facts.
	Launch Launch
	// Views maps each service that offers a view to the view's declared type, a
	// struct of plain data (see CheckView).
	Views map[string]reflect.Type
	// MaxNodes caps the size of an expression; zero means DefaultMaxNodes.
	MaxNodes uint
}

// DefaultMaxNodes is the default cap on the node count of one expression.
const DefaultMaxNodes = 500

// pure lists the builtins an expression may call: none reads the clock or the
// outside world (now, date, timezone and duration are left out).
var pure = []string{
	"all", "none", "any", "one", "filter", "map", "find", "findIndex", "findLast", "findLastIndex",
	"count", "sum", "groupBy", "sortBy", "reduce", "len", "type", "abs", "ceil", "floor", "round",
	"int", "float", "string", "trim", "trimPrefix", "trimSuffix", "upper", "lower", "split",
	"splitAfter", "replace", "repeat", "join", "indexOf", "lastIndexOf", "hasPrefix", "hasSuffix",
	"max", "min", "mean", "median", "toJSON", "fromJSON", "toBase64", "fromBase64", "first", "last",
	"get", "take", "keys", "values", "toPairs", "fromPairs", "reverse", "uniq", "concat", "flatten",
	"sort", "bitnot", "bitand", "bitor", "bitxor", "bitshl", "bitshr", "bitushr",
}

// Builtins returns the names of the builtins an expression may call.
func Builtins() []string { return slices.Clone(pure) }

// Evaluator implements loader.Evaluator.
type Evaluator struct {
	opts     Options
	envType  [2]reflect.Type // by loader.ScopeKind
	useIndex map[string]int  // service -> field of the use struct
	compiled sync.Map        // key{kind, source} -> *program
}

var _ loader.Evaluator = (*Evaluator)(nil)

type key struct {
	kind   loader.ScopeKind
	source string
}

type program struct {
	vm   *vm.Program
	env  []string // env names read
	uses []string // services read
	err  error
}

// New returns an evaluator over opts. It fails when a view type is not plain data.
func New(opts Options) (*Evaluator, error) {
	if opts.OS == "" {
		opts.OS = runtime.GOOS
	}
	if opts.MaxNodes == 0 {
		opts.MaxNodes = DefaultMaxNodes
	}
	services := make([]string, 0, len(opts.Views))
	for service, view := range opts.Views {
		if err := CheckView(view); err != nil {
			return nil, fmt.Errorf("expr: view of %s: %w", service, err)
		}
		services = append(services, service)
	}
	slices.Sort(services)
	e := &Evaluator{opts: opts, useIndex: map[string]int{}}
	base := []reflect.StructField{
		{Name: "Env", Type: reflect.TypeFor[map[string]any](), Tag: `expr:"env"`},
		{Name: "OS", Type: reflect.TypeFor[string](), Tag: `expr:"os"`},
		{Name: "Cwd", Type: reflect.TypeFor[string](), Tag: `expr:"cwd"`},
		{Name: "Launch", Type: reflect.TypeFor[Launch](), Tag: `expr:"launch"`},
	}
	e.envType[loader.DisabledScope] = reflect.StructOf(base)
	use := make([]reflect.StructField, len(services))
	for i, service := range services {
		use[i] = reflect.StructField{Name: fmt.Sprintf("S%d", i), Type: opts.Views[service], Tag: reflect.StructTag(fmt.Sprintf("expr:%q", service))}
		e.useIndex[service] = i
	}
	e.envType[loader.ConfigScope] = reflect.StructOf(append(slices.Clone(base),
		reflect.StructField{Name: "Use", Type: reflect.StructOf(use), Tag: `expr:"use"`}))
	return e, nil
}

// Check parses and type-checks source for kind.
func (e *Evaluator) Check(source string, kind loader.ScopeKind) error {
	return e.compile(source, kind).err
}

// Eval computes source in scope. It reads the launch facts and, in config, the views
// of the services the expression names, and has no other effect.
func (e *Evaluator) Eval(source string, scope loader.Scope) (any, error) {
	p := e.compile(source, scope.Kind)
	if p.err != nil {
		return nil, p.err
	}
	env := reflect.New(e.envType[e.kindOf(scope.Kind)]).Elem()
	vars := make(map[string]any, len(p.env))
	for _, name := range p.env {
		vars[name] = nil
		if e.opts.Env != nil {
			if value, ok := e.opts.Env(name); ok {
				vars[name] = value
			}
		}
	}
	env.Field(0).Set(reflect.ValueOf(vars))
	env.Field(1).SetString(e.opts.OS)
	env.Field(2).SetString(e.opts.Cwd)
	env.Field(3).Set(reflect.ValueOf(e.opts.Launch))
	if len(p.uses) > 0 {
		if scope.Context == nil {
			return nil, errors.New("use needs the entry's context")
		}
		use := env.Field(4)
		for _, service := range p.uses {
			value, err := scope.Context.Get(service)
			if err != nil {
				return nil, fmt.Errorf("use.%s: %w", service, err)
			}
			viewer, ok := value.(Viewer)
			if !ok {
				return nil, fmt.Errorf("use.%s: the service offers no view", service)
			}
			view := reflect.ValueOf(viewer.ExprView())
			field := use.Field(e.useIndex[service])
			if !view.IsValid() || view.Type() != field.Type() {
				return nil, fmt.Errorf("use.%s: the view is %T, declared %s", service, viewer.ExprView(), field.Type())
			}
			field.Set(view)
		}
	}
	return expr.Run(p.vm, env.Interface())
}

func (e *Evaluator) kindOf(kind loader.ScopeKind) loader.ScopeKind {
	if kind == loader.ConfigScope {
		return loader.ConfigScope
	}
	return loader.DisabledScope
}

func (e *Evaluator) compile(source string, kind loader.ScopeKind) *program {
	kind = e.kindOf(kind)
	k := key{kind: kind, source: source}
	if cached, ok := e.compiled.Load(k); ok {
		return cached.(*program)
	}
	p := e.build(source, kind)
	cached, _ := e.compiled.LoadOrStore(k, p)
	return cached.(*program)
}

func (e *Evaluator) build(source string, kind loader.ScopeKind) *program {
	if strings.TrimSpace(source) == "" {
		return &program{err: errors.New("expression is empty")}
	}
	tree, err := parser.Parse(source)
	if err != nil {
		return &program{err: err}
	}
	p := &program{}
	if p.env, p.uses, err = e.inspect(tree.Node, kind); err != nil {
		return &program{err: err}
	}
	options := []expr.Option{
		expr.Env(reflect.New(e.envType[kind]).Elem().Interface()),
		expr.DisableAllBuiltins(),
		expr.MaxNodes(e.opts.MaxNodes),
		expr.Function("home", e.home, new(func(...string) string)),
	}
	for _, name := range pure {
		options = append(options, expr.EnableBuiltin(name))
	}
	if p.vm, err = expr.Compile(source, options...); err != nil {
		return &program{err: err}
	}
	return p
}

// inspect walks the parsed expression: env and use may only be read with a literal
// name, use only in config, a secret name never; the only callable function is home
// and the only builtins the pure ones. It returns the env names and services read.
func (e *Evaluator) inspect(root ast.Node, kind loader.ScopeKind) (env, uses []string, err error) {
	v := &inspector{allowed: map[ast.Node]bool{}}
	ast.Walk(&root, v)
	for _, n := range v.nodes {
		at := func(format string, args ...any) error {
			return fmt.Errorf(format+" (column %d)", append(args, n.Location().From+1)...)
		}
		switch n := n.(type) {
		case *ast.MemberNode:
			id, ok := n.Node.(*ast.IdentifierNode)
			if !ok || (id.Value != "env" && id.Value != "use") {
				continue
			}
			name, literal := n.Property.(*ast.StringNode)
			if !literal {
				return nil, nil, at("%s must be read with a literal name", id.Value)
			}
			v.allowed[id] = true
			switch {
			case id.Value == "use" && kind != loader.ConfigScope:
				return nil, nil, at("use is not available in %s", kind)
			case id.Value == "use":
				if _, ok := e.useIndex[name.Value]; !ok {
					return nil, nil, at("use.%s: service %q offers no view", name.Value, name.Value)
				}
				if !slices.Contains(uses, name.Value) {
					uses = append(uses, name.Value)
				}
			case e.opts.Secret != nil && e.opts.Secret(name.Value):
				return nil, nil, at("env.%s is a secret and cannot be read by an expression", name.Value)
			case !slices.Contains(env, name.Value):
				env = append(env, name.Value)
			}
		case *ast.CallNode:
			id, ok := n.Callee.(*ast.IdentifierNode)
			if !ok {
				return nil, nil, at("only home(...) can be called")
			}
			if id.Value != "home" {
				return nil, nil, at("function %s is not available; only home(...) can be called", id.Value)
			}
		case *ast.BuiltinNode:
			if !slices.Contains(pure, n.Name) {
				return nil, nil, at("function %s is not available", n.Name)
			}
		}
	}
	for _, n := range v.nodes {
		id, ok := n.(*ast.IdentifierNode)
		switch {
		case !ok:
		case id.Value == "$env":
			return nil, nil, fmt.Errorf("$env is not available (column %d)", id.Location().From+1)
		case (id.Value == "env" || id.Value == "use") && !v.allowed[id]:
			if id.Value == "use" && kind != loader.ConfigScope {
				return nil, nil, fmt.Errorf("use is not available in %s (column %d)", kind, id.Location().From+1)
			}
			return nil, nil, fmt.Errorf("%s must be read with a literal name, as %s.NAME (column %d)", id.Value, id.Value, id.Location().From+1)
		}
	}
	return env, uses, nil
}

type inspector struct {
	nodes   []ast.Node
	allowed map[ast.Node]bool
}

func (v *inspector) Visit(n *ast.Node) { v.nodes = append(v.nodes, *n) }

// home joins parts under the harness home. It touches no file.
func (e *Evaluator) home(params ...any) (any, error) {
	parts := []string{e.opts.Home}
	for _, param := range params {
		part, ok := param.(string)
		if !ok {
			return nil, fmt.Errorf("home: want strings, got %T", param)
		}
		if filepath.IsAbs(part) || strings.HasPrefix(part, "/") || strings.HasPrefix(part, `\`) {
			return nil, fmt.Errorf("home: %q is absolute", part)
		}
		parts = append(parts, part)
	}
	joined := filepath.Join(parts...)
	rel, err := filepath.Rel(filepath.Clean(e.opts.Home), joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("home: %q leaves the home", strings.Join(parts[1:], "/"))
	}
	return joined, nil
}

// CheckView reports whether t is a view type an expression may read: a struct whose
// fields are plain data (booleans, numbers, strings, and slices, arrays, maps with
// string keys and structs of those), never a pointer, function, interface or channel,
// and never a field marked secret (`dsh:"secret"`), at any depth (DH-ADR-BOOT-5). A
// type that contains itself (through a struct, slice, array or map) is checked once
// and accepted if what it holds is plain.
func CheckView(t reflect.Type) error {
	if t == nil || t.Kind() != reflect.Struct {
		return fmt.Errorf("a view must be a struct, got %v", t)
	}
	return plain(t, t.String(), map[reflect.Type]bool{})
}

func plain(t reflect.Type, path string, seen map[reflect.Type]bool) error {
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		// Every composite type is checked once: a named type can contain itself
		// through any of them (type Loop []Loop, type Dict map[string]Dict).
		if seen[t] {
			return nil // being checked further up, or already checked
		}
		seen[t] = true
	}
	switch t.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return nil
	case reflect.Slice, reflect.Array:
		return plain(t.Elem(), path+"[]", seen)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("%s: map keys must be strings", path)
		}
		return plain(t.Elem(), path+"[]", seen)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				return fmt.Errorf("%s.%s: unexported field", path, f.Name)
			}
			if secretTag(f.Tag) {
				return fmt.Errorf("%s.%s: a secret field may not be in a view", path, f.Name)
			}
			if err := plain(f.Type, path+"."+f.Name, seen); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%s: %s is not plain data", path, t.Kind())
	}
}

// secretTag reports whether a field's dsh tag carries the word secret, as the schema
// package reads it (internal/schema: comma-separated words; pattern= takes the rest).
func secretTag(tag reflect.StructTag) bool {
	words, ok := tag.Lookup("dsh")
	for ok && words != "" && !strings.HasPrefix(words, "pattern=") {
		var word string
		word, words, _ = strings.Cut(words, ",")
		if strings.TrimSpace(word) == "secret" {
			return true
		}
	}
	return false
}
