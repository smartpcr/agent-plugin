package subprocess

import (
	"os"
	"path"
	"runtime"
	"slices"
	"strings"
)

// Env is a whole child environment, "NAME=value" entries; a later entry for a name
// wins. Nil is the empty environment.
type Env []string

// DefaultDeny are the name patterns BuildEnv always removes (DH-ADR-EXEC-8):
// upstream's four plus PASSPHRASE and CREDENTIAL. Patterns use path.Match syntax and
// match without regard to case.
var DefaultDeny = []string{"*KEY*", "*TOKEN*", "*SECRET*", "*PASSWORD*", "*PASSPHRASE*", "*CREDENTIAL*"}

// HarnessPrefix names the harness's own variables; BuildEnv removes all of them
// from the host environment before caller entries are added.
const HarnessPrefix = "DSH_"

// SecretNames says which environment variable names hold a secret. The secret
// store (*secret.Store in internal/human/secret) is one: it names the variable of
// every reference configuration named, such as GITHUB_PAT for secret://github/pat,
// and every credential-shaped name. It is an interface so that this package
// imports nothing of internal/human.
type SecretNames interface {
	IsSecretName(name string) bool
}

// EnvOptions says how BuildEnv derives a child environment.
type EnvOptions struct {
	// Deny adds name patterns to DefaultDeny.
	Deny []string
	// Allow keeps names (patterns) that a deny pattern would remove. It does not
	// keep a credential name, a name Secrets matches or a DSH_* name.
	Allow []string
	// Credentials are the exact names configuration uses as credential
	// references; each is always removed.
	Credentials []string
	// Secrets removes every host name it reports as a secret (SP-11), whatever
	// the deny and allow patterns say. A child that needs a secret gets it as an
	// explicit entry, never by inheritance.
	Secrets SecretNames
	// Entries are the caller's "NAME=value" additions.
	Entries []string
	// Managed is the managed-variable snapshot, applied last.
	Managed []string
}

// BuildEnv derives a child environment from host (usually os.Environ()): every
// DSH_* name, every credential name and every name Secrets reports are removed,
// names matching a deny pattern are removed unless allowed, then the caller's
// entries and finally the managed snapshot are added. The result is merged by
// name, which on Windows ignores case.
//
// The filter fails closed: a host entry that is not NAME=value is dropped, the
// allow list cannot keep a name the earlier rules remove, and a Secrets check that
// panics removes the name. Entries and Managed are the caller's explicit choices
// and are not filtered.
func BuildEnv(host []string, o EnvOptions) Env {
	deny := append(slices.Clone(DefaultDeny), o.Deny...)
	out := make(Env, 0, len(host)+len(o.Entries)+len(o.Managed))
	for _, kv := range host {
		name, ok := envName(kv)
		if !ok {
			continue
		}
		switch {
		case hasPrefixFold(name, HarnessPrefix):
		case containsName(o.Credentials, name):
		case isSecret(o.Secrets, name):
		case matchAny(deny, name) && !matchAny(o.Allow, name):
		default:
			out = append(out, kv)
		}
	}
	out = append(out, o.Entries...)
	out = append(out, o.Managed...)
	return out.merged()
}

// Get returns the value of name; the last entry wins, and on Windows case is
// ignored.
func (e Env) Get(name string) string {
	for i := len(e) - 1; i >= 0; i-- {
		if n, ok := envName(e[i]); ok && sameName(n, name) {
			return e[i][len(n)+1:]
		}
	}
	return ""
}

// Has reports whether name is set.
func (e Env) Has(name string) bool {
	for _, kv := range e {
		if n, ok := envName(kv); ok && sameName(n, name) {
			return true
		}
	}
	return false
}

// With returns a copy of e with name set to value.
func (e Env) With(name, value string) Env {
	return append(slices.Clone(e), name+"="+value).merged()
}

// merged keeps the last entry per name, in first-appearance order; on Windows it
// is sorted without regard to case, as CreateProcess expects.
func (e Env) merged() Env {
	idx := map[string]int{}
	out := make(Env, 0, len(e))
	for _, kv := range e {
		n, _ := envName(kv)
		k := key(n)
		if i, ok := idx[k]; ok {
			out[i] = kv
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	if runtime.GOOS == "windows" {
		slices.SortStableFunc(out, func(a, b string) int {
			an, _ := envName(a)
			bn, _ := envName(b)
			return strings.Compare(strings.ToUpper(an), strings.ToUpper(bn))
		})
	}
	return out
}

// block is the environment handed to the system: never nil, so os/exec cannot
// fall back to the host's. On Windows it always holds SystemRoot.
func (e Env) block() []string {
	out := e.merged()
	if runtime.GOOS == "windows" && !out.Has("SystemRoot") {
		if v, ok := os.LookupEnv("SystemRoot"); ok {
			out = append(out, "SystemRoot="+v).merged()
		}
	}
	if out == nil {
		out = Env{}
	}
	return out
}

// envName returns the name of a "NAME=value" entry. A Windows per-drive entry
// such as "=C:=C:\x" is named "=C:".
func envName(kv string) (string, bool) {
	if strings.HasPrefix(kv, "=") {
		i := strings.IndexByte(kv[1:], '=')
		if i < 0 {
			return "", false
		}
		return kv[:i+1], true
	}
	n, _, ok := strings.Cut(kv, "=")
	return n, ok && n != ""
}

func key(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func sameName(a, b string) bool { return key(a) == key(b) }

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// isSecret asks names about name. A check that panics counts as a secret, so a
// faulty store removes the variable instead of passing it on.
func isSecret(names SecretNames, name string) (secret bool) {
	if names == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			secret = true
		}
	}()
	return names.IsSecretName(name)
}

func containsName(names []string, name string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return sameName(n, name) })
}

func matchAny(patterns []string, name string) bool {
	up := strings.ToUpper(name)
	for _, p := range patterns {
		if ok, err := path.Match(strings.ToUpper(p), up); err == nil && ok {
			return true
		}
	}
	return false
}
