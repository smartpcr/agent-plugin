// Package shellenv is shellexec/env (shellexec.env, DH-D-EXEC-8, DH-ADR-EXEC-8):
// the registry of managed variables and the builder of every shell command's
// child environment.
//
// Contributors declare their keys with a one-line description; the built-ins
// are the first contributor, so List is complete. The builder starts from the
// host environment through subprocess.BuildEnv and drops every name the secret
// store (human/secret) holds, from the host, from the caller's entries and from
// the managed snapshot alike, so a secret name never reaches the child (M2-C-11,
// INV-11).
package shellenv

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// The reasons this package fails with.
const (
	// ReasonBadKey is a declaration refused: a malformed name, no description,
	// a duplicate, or a reserved key.
	ReasonBadKey herr.Reason = "shellenv/bad-key"
	// ReasonUndeclared is a contributor's value for a key it did not declare.
	ReasonUndeclared herr.Reason = "shellenv/undeclared"
	// ReasonContributor is a contributor whose values failed.
	ReasonContributor herr.Reason = "shellenv/contributor-failed"
	// ReasonSecrets is a secret store that could not say which names it holds;
	// the build fails closed.
	ReasonSecrets herr.Reason = "shellenv/secrets-unknown"
)

// Reasons lists every reason this package declares.
var Reasons = []herr.Reason{ReasonBadKey, ReasonUndeclared, ReasonContributor, ReasonSecrets}

// Key is the managed environment registry service (shellenv.Key).
var Key = cordis.NewServiceKey[*Registry]("shellenv")

// The built-in managed variables.
const (
	// Home is the harness home, a boot fact.
	Home = "DSH_HOME"
	// Executable is the harness binary (upstream's DSH_NODE_EXECUTABLE).
	Executable = "DSH_EXECUTABLE"
)

// Reserved are the keys no contributor may declare: they come only from the
// script environment a hosting tool's body passes (DH-ADR-TOOLS-6).
var Reserved = []string{"DSH_TOOL_ENDPOINT", "DSH_TOOL_TOKEN"}

// BuiltinName is the contributor of the built-ins.
const BuiltinName = "builtin"

// Decl is one declared key.
type Decl struct {
	Name        string
	Description string
}

// Var is one key of the complete list: its declaration and who declared it.
type Var struct {
	Decl
	Contributor string
}

// Contributor declares keys and supplies their values.
type Contributor struct {
	// Name says who contributes, for the list and for errors.
	Name string
	// Decls are the keys it declares.
	Decls []Decl
	// Values returns its "NAME=value" entries at build time; every name must be
	// one of Decls. Nil supplies nothing.
	Values func(ctx context.Context) ([]string, error)
}

// Builtins are the built-in values; an empty one is not set.
type Builtins struct {
	Home       string
	Executable string
}

// Registry holds the contributors in order, the built-ins first.
type Registry struct {
	mu   sync.Mutex
	seq  int
	list []entry
}

type entry struct {
	id int
	c  Contributor
}

// NewRegistry returns a registry whose first contributor is the built-ins.
func NewRegistry(b Builtins) *Registry {
	r := &Registry{}
	builtin := Contributor{
		Name: BuiltinName,
		Decls: []Decl{
			{Home, "the harness home directory"},
			{Executable, "the harness binary, to run dsh from a command"},
		},
		Values: func(context.Context) ([]string, error) {
			var out []string
			if b.Home != "" {
				out = append(out, Home+"="+b.Home)
			}
			if b.Executable != "" {
				out = append(out, Executable+"="+b.Executable)
			}
			return out, nil
		},
	}
	if _, err := r.Contribute(builtin); err != nil {
		panic(err) // the built-ins are well formed
	}
	return r
}

// Contribute adds c and returns the release that removes it. A key that is
// malformed, has no description, is reserved, or is already declared (by c or
// another contributor) is refused, and nothing is added.
func (r *Registry) Contribute(c Contributor) (release func(), err error) {
	if blank(c.Name) {
		return nil, herr.New(ReasonBadKey, "a contributor has a name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]bool{}
	for _, d := range c.Decls {
		switch {
		case !validName(d.Name):
			return nil, herr.Newf(ReasonBadKey, "%s: %q is not a variable name", c.Name, d.Name)
		case blank(d.Description):
			return nil, herr.Newf(ReasonBadKey, "%s: %s has no description", c.Name, d.Name)
		case slices.ContainsFunc(Reserved, func(k string) bool { return strings.EqualFold(k, d.Name) }):
			return nil, herr.Newf(ReasonBadKey, "%s: %s is reserved for a hosting tool's script environment", c.Name, d.Name)
		case seen[key(d.Name)]:
			return nil, herr.Newf(ReasonBadKey, "%s: %s is declared twice", c.Name, d.Name)
		}
		if owner, ok := r.owner(d.Name); ok {
			return nil, herr.Newf(ReasonBadKey, "%s: %s is already declared by %s", c.Name, d.Name, owner)
		}
		seen[key(d.Name)] = true
	}
	r.seq++
	id := r.seq
	c.Decls = slices.Clone(c.Decls)
	r.list = append(r.list, entry{id: id, c: c})
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.list = slices.DeleteFunc(r.list, func(e entry) bool { return e.id == id })
	}, nil
}

// Mount contributes c for the life of ctx's fiber: its keys and values leave
// when the fiber unloads or the handle is disposed.
func Mount(ctx *cordis.Context, r *Registry, c Contributor) (*cordis.EffectHandle, error) {
	if r == nil {
		return nil, herr.New(ReasonContributor, "no managed environment registry to contribute to")
	}
	return ctx.Effect("shellenv "+c.Name, func() (cordis.Disposer, error) {
		release, err := r.Contribute(c)
		if err != nil {
			return nil, err
		}
		return func() error { release(); return nil }, nil
	})
}

// owner names the contributor that declared name. r.mu is held.
func (r *Registry) owner(name string) (string, bool) {
	for _, e := range r.list {
		for _, d := range e.c.Decls {
			if key(d.Name) == key(name) {
				return e.c.Name, true
			}
		}
	}
	return "", false
}

// List is every declared key in contributor order, the built-ins first.
func (r *Registry) List() []Var {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Var
	for _, e := range r.list {
		for _, d := range e.c.Decls {
			out = append(out, Var{Decl: d, Contributor: e.c.Name})
		}
	}
	return out
}

// Snapshot collects every contributor's values, in order. A value for a key
// its contributor did not declare, or a malformed entry, fails the snapshot.
func (r *Registry) Snapshot(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	list := slices.Clone(r.list)
	r.mu.Unlock()
	var out []string
	for _, e := range list {
		if e.c.Values == nil {
			continue
		}
		vals, err := e.c.Values(ctx)
		if err != nil {
			return nil, herr.Wrap(ReasonContributor, err, e.c.Name+": collect the managed variables")
		}
		for _, kv := range vals {
			name, _, ok := strings.Cut(kv, "=")
			if !ok || !validName(name) {
				return nil, herr.Newf(ReasonUndeclared, "%s: a managed entry is not NAME=value", e.c.Name)
			}
			if !slices.ContainsFunc(e.c.Decls, func(d Decl) bool { return key(d.Name) == key(name) }) {
				return nil, herr.Newf(ReasonUndeclared, "%s: %s is not declared", e.c.Name, name)
			}
			out = append(out, kv)
		}
	}
	return out, nil
}

// Builder builds a command's child environment; it is a shellexec.Environment.
type Builder struct {
	// Registry supplies the managed snapshot, added last; nil adds none.
	Registry *Registry
	// Secrets is the secret store; every name it holds, named by configuration
	// or stored in its managed store, is dropped. Nil knows only the
	// credential-shaped names and DefaultDeny.
	Secrets *secret.Store
	// Host is the host environment; nil is os.Environ.
	Host func() []string
	// Deny adds name patterns to subprocess.DefaultDeny; Allow keeps names a
	// deny pattern would remove (subprocess/local.Config ScrubDeny, ScrubAllow).
	Deny, Allow []string
}

// Environ builds the environment: the host's, scrubbed by subprocess.BuildEnv
// (DSH_* names, credential names, secret names and deny patterns removed), then
// entries, then the managed snapshot. Every entry and managed value whose name
// the secret store holds is dropped too, so a secret name never passes to the
// child. When the store cannot list what it holds, the build fails closed.
func (b *Builder) Environ(ctx context.Context, entries []string) (subprocess.Env, error) {
	var credentials []string
	if b.Secrets != nil {
		if err := b.Secrets.NameStored(ctx); err != nil {
			return nil, herr.Wrap(ReasonSecrets, err, "list the names the secret store holds")
		}
		for _, r := range b.Secrets.References() {
			credentials = append(credentials, r.EnvName())
		}
	}
	var managed []string
	if b.Registry != nil {
		snap, err := b.Registry.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		managed = snap
	}
	held := func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(credentials, func(c string) bool { return strings.EqualFold(c, name) })
	}
	host := os.Environ
	if b.Host != nil {
		host = b.Host
	}
	o := subprocess.EnvOptions{
		Deny:        b.Deny,
		Allow:       b.Allow,
		Credentials: credentials,
		Entries:     slices.DeleteFunc(slices.Clone(entries), held),
		Managed:     slices.DeleteFunc(managed, held),
	}
	if b.Secrets != nil {
		o.Secrets = b.Secrets
	}
	return subprocess.BuildEnv(host(), o), nil
}

// validName is an environment variable name: a letter or underscore, then
// letters, digits and underscores.
func validName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// key is a name compared without regard to case: two keys that differ only in
// case would be one variable on Windows.
func key(name string) string { return strings.ToUpper(name) }

func blank(s string) bool { return strings.TrimSpace(s) == "" }
