package launch

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// browserVars are the only variables the browser opener inherits: what a desktop
// session needs to find and start the default browser (DH-LIB-OPEN-R2). No DSH_*
// variable and no credential is among them.
var browserVars = []string{
	"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "BROWSER",
	"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "XDG_CURRENT_DESKTOP", "XDG_DATA_DIRS",
	"XDG_CONFIG_DIRS", "DBUS_SESSION_BUS_ADDRESS",
	"SYSTEMROOT", "WINDIR", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "TMPDIR",
}

// browserEnv keeps the browserVars of host, compared without case.
func browserEnv(host []string) subprocess.Env {
	var env subprocess.Env
	for _, kv := range host {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		for _, v := range browserVars {
			if strings.EqualFold(name, v) {
				env = append(env, kv)
				break
			}
		}
	}
	return env
}

// browserArgv is the launcher of goos with url as its one argument, never through
// a shell (DH-LIB-OPEN-R3, libs/open.md §5).
func browserArgv(goos, url string, env subprocess.Env) ([]string, error) {
	switch goos {
	case "windows":
		return []string{filepath.Join(env.Get("SYSTEMROOT"), "System32", "rundll32.exe"), "url.dll,FileProtocolHandler", url}, nil
	case "darwin":
		return []string{"/usr/bin/open", url}, nil
	}
	path, err := subprocess.Lookup("xdg-open", env)
	if err != nil {
		return nil, err
	}
	return []string{path, url}, nil
}

// openBrowser starts the system launcher detached and trusted, with a scrubbed
// environment; the host never waits for it.
func openBrowser(ctx context.Context, url string) error {
	env := browserEnv(os.Environ())
	argv, err := browserArgv(runtime.GOOS, url, env)
	if err != nil {
		return err
	}
	_, err = subprocess.Start(ctx, subprocess.Spec{Argv: argv, Env: env, Detach: true,
		Launch: sandbox.Trusted("the browser opener is given only the launch URL and reads nothing the model writes")})
	return err
}
