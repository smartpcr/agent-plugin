package launch

import (
	"io"
	"os"
)

// Channels exposes the channel decision.
func Channels(cfg Config, terminal io.Writer, getenv func(string) string, goos string) (bool, bool) {
	return channels(cfg, terminal, getenv, goos)
}

// BrowserEnv exposes the browser opener's environment filter.
var BrowserEnv = browserEnv

// BrowserArgv exposes the launcher argv for a platform.
var BrowserArgv = browserArgv

// OpenBrowser exposes the system browser opener.
var OpenBrowser = openBrowser

// Terminal exposes the print-channel check of the host's stderr.
func Terminal(f *os.File) io.Writer { return terminal(f) }

// UseOptions makes the component pass its options through f until the test ends.
func UseOptions(f func(*Options)) (restore func()) {
	optionsHook = f
	return func() { optionsHook = nil }
}
