package clientroute

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// EnvSpec is one route's environment rules on top of subprocess.BuildEnv,
// whose DefaultDeny already removes every name holding KEY, TOKEN, SECRET,
// PASSWORD, PASSWD, PASSPHRASE or CREDENTIAL, and every DSH_* name.
type EnvSpec struct {
	// Remove are exact names always removed: credentials and the variables that
	// move a request to another endpoint, provider, account or model.
	Remove []string
	// Patterns are removed names in path.Match syntax ("ANTHROPIC_*_BASE_URL"),
	// matched without regard to case.
	Patterns []string
	// Set are the route's own NAME=value settings (no client retries, no
	// auto-update), added after the host's variables.
	Set []string
	// Extra are the caller's NAME=value additions (a configuration home for a
	// signed-out check, a test's stand-in selector). Check refuses one whose
	// name a rule above or a default rule would remove, or one that overrides a
	// Set name, so no addition can bring a credential back.
	Extra []string
	// Secrets removes every host name the secret store reports (SP-11).
	Secrets subprocess.SecretNames
}

// Check refuses an Extra entry that is not NAME=value, whose name any removal
// rule matches, or that overrides one of the route's Set names.
func (s EnvSpec) Check() error {
	for _, kv := range s.Extra {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return fmt.Errorf("clientroute: the extra entry %q is not NAME=value", kv)
		}
		if s.removes(name) {
			return fmt.Errorf("clientroute: the extra entry %s names a variable the route removes", name)
		}
		for _, set := range s.Set {
			if n, _, _ := strings.Cut(set, "="); strings.EqualFold(n, name) {
				return fmt.Errorf("clientroute: the extra entry %s overrides the route's own setting", name)
			}
		}
	}
	return nil
}

// removes reports whether any rule of the spec or of BuildEnv removes name.
func (s EnvSpec) removes(name string) bool {
	up := strings.ToUpper(name)
	if strings.HasPrefix(up, subprocess.HarnessPrefix) {
		return true
	}
	if slices.ContainsFunc(s.Remove, func(n string) bool { return strings.EqualFold(n, name) }) {
		return true
	}
	for _, p := range append(slices.Clone(subprocess.DefaultDeny), s.Patterns...) {
		if ok, err := path.Match(strings.ToUpper(p), up); err == nil && ok {
			return true
		}
	}
	return s.Secrets != nil && s.Secrets.IsSecretName(name)
}

// Build is the child environment: host (usually os.Environ()) through
// subprocess.BuildEnv with the removal rules, then Set, then Extra. It fails
// only when Check does.
func (s EnvSpec) Build(host []string) (subprocess.Env, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	entries := append(slices.Clone(s.Set), s.Extra...)
	return subprocess.BuildEnv(host, subprocess.EnvOptions{
		Deny:        s.Patterns,
		Credentials: s.Remove,
		Secrets:     s.Secrets,
		Entries:     entries,
	}), nil
}

// Names are the variable names of env, sorted and without values: what the
// invocation record logs.
func Names(env subprocess.Env) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && name != "" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
