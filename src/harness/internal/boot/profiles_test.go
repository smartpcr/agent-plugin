package boot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// DH-D-BUNDLE-3 (C42), TC-08g: the kernel profiles boot through the real steps
// on a temp home with the built catalogue: admin opens the session store, core
// loads the whole spine; each reaches ready, stops with the status it is given,
// and writes no runtime record, since neither binds a listener (M2-C-13).
func TestKernelProfilesBootAndStopCleanly(t *testing.T) {
	for _, def := range profile.Definitions() {
		name := def.Name
		if name != profile.Admin && name != profile.Core {
			continue // the mode carriers are not registered yet (DH-S-16 16.1)
		}
		t.Run(name, func(t *testing.T) {
			homeDir := temphome.Dir(t)
			root := filepath.Dir(homeDir)
			app := launch.NewApp()
			var stderr testkit.Buffer
			opts := Options{
				Request: launch.Request{Args: []string{"dsh", name}, Cwd: root, Home: homeDir, Profile: name},
				Profile: def,
				App:     app,
				Stderr:  &stderr,
			}
			done := runJoined(t, app, func() int { return Run(context.Background(), opts) })
			select {
			case <-app.Ready():
			case status := <-done:
				t.Fatalf("%s ended with %d before ready:\n%s", name, status, stderr.String())
			}
			if _, err := os.Stat(home.New(homeDir).DB()); err != nil {
				t.Errorf("%s is ready without the session store: %v", name, err)
			}
			app.Stop(launch.Completed, output.OK)
			if status := <-done; status != output.OK {
				t.Fatalf("%s stopped with %d, want %d:\n%s", name, status, output.OK, stderr.String())
			}
			if strings.Contains(stderr.String(), "dsh: fatal:") {
				t.Errorf("%s printed a fatal message:\n%s", name, stderr.String())
			}
			noRuntimeRecord(t, homeDir)
		})
	}
}

// noRuntimeRecord fails t when <home>/run holds a runtime record (web-<hash>.json,
// host/launch PathsFor): only a listener profile writes one.
func noRuntimeRecord(t *testing.T, homeDir string) {
	t.Helper()
	recs, _ := filepath.Glob(filepath.Join(home.New(homeDir).Run(), "web-*.json"))
	if len(recs) > 0 {
		t.Errorf("%s/run holds runtime records %v: no listener was bound", homeDir, recs)
	}
}
