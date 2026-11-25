//go:build linux

package local_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

type runningKernel struct{}

func (runningKernel) LandlockABI() (int, error) { return launch.LandlockABI() }

// TestLandlockStageConfinesForReal: on a kernel with Landlock, the real probe
// stage restricts itself and refuses its canary; a confined start then writes
// beneath its write root and cannot write outside it, reported Partial with the
// network missing (Landlock does not govern it at this reference set).
//
//dsh:test DH-T-SANDBOX-4
func TestLandlockStageConfinesForReal(t *testing.T) {
	abi, err := launch.LandlockABI()
	if err != nil || abi < 1 {
		t.Skipf("the kernel has no Landlock (version %d, %v)", abi, err)
	}
	env := []string{reexecEnv + "=1"}
	if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
		env = append(env, subprocesstest.CoverDirEnv+"="+dir)
	}
	local.SetProbeEnv(t, env...)
	s := newService(t, local.Landlock(runningKernel{}))
	st := s.Describe(context.Background())
	if len(st) != 1 || !st[0].Available || st[0].Canary != string(launch.CanaryRefused) {
		t.Fatalf("Describe = %+v; want landlock available with its canary refused", st)
	}
	root := t.TempDir()
	policy := sandboxtest.Policy(sandbox.WorkspaceWrite, root)
	exe := command(t)[0]
	for _, row := range []struct {
		name   string
		marker string
		ran    bool
	}{
		{"beneath the root", filepath.Join(root, "marker"), true},
		{"outside the root", filepath.Join(t.TempDir(), "marker"), false},
	} {
		argv := []string{exe, markerRole, row.marker}
		l, err := s.Prepare(context.Background(), policy, argv)
		if err != nil {
			t.Fatal(err)
		}
		p, err := start(t, l, argv)
		if err != nil {
			t.Fatalf("%s: Start: %v", row.name, err)
		}
		if err := p.Wait(); err != nil && row.ran {
			t.Fatalf("%s: Wait: %v", row.name, err)
		}
		if exists(t, row.marker) != row.ran || (p.ExitCode() == 0) != row.ran {
			t.Fatalf("%s: marker exists %v, exit %d; want ran=%v", row.name, exists(t, row.marker), p.ExitCode(), row.ran)
		}
		c := p.Confinement()
		if c.Backend != sandbox.BackendLandlock || c.State != sandbox.StatePartial || !slices.Contains(c.Missing, sandbox.EffectNetwork) {
			t.Fatalf("%s: Confinement = %+v; want Partial under landlock, network missing", row.name, c)
		}
	}
}
