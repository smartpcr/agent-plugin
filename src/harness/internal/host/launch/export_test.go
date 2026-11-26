package launch

import "io"

// Channels exposes the channel decision.
func Channels(cfg Config, terminal io.Writer, getenv func(string) string, goos string) (bool, bool) {
	return channels(cfg, terminal, getenv, goos)
}

// BrowserEnv exposes the browser opener's environment filter.
var BrowserEnv = browserEnv

// UseOptions makes the component pass its options through f until the test ends.
func UseOptions(f func(*Options)) (restore func()) {
	optionsHook = f
	return func() { optionsHook = nil }
}
