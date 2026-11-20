package launch

import "github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"

// Scope returns the evaluator options the launch facts give internal/boot/expr:
// env.NAME reads env, os and cwd and home(...) read d, and launch.<key> reads the
// fields of d other than Args, Words, Overlays, User and Build. secret is the
// secret-name predicate of internal/human/secret (env refuses those names); nil
// refuses none. The catalogue's views are the caller's to add.
func Scope(d Data, env Env, secret func(name string) bool) expr.Options {
	var lookup func(string) (string, bool)
	if env != nil {
		lookup = func(name string) (string, bool) {
			v, _, ok := env.Lookup(name)
			return v, ok
		}
	}
	return expr.Options{
		Env:    lookup,
		Secret: secret,
		OS:     d.OS,
		Cwd:    d.Cwd,
		Home:   d.Home,
		Launch: expr.Launch{
			Profile: d.Profile,
			Home:    d.Home,
			Cwd:     d.Cwd,
			Version: d.Version,
			OS:      d.OS,
			TTY:     d.TTY,
			Safe:    d.Safe,
		},
	}
}
