package boot

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
)

// TC-06a: with a permit configured for every kind and the off-switch on, the boot
// audit denies every kind and lists each one as off.
func TestAuditOffSwitchListsEveryKindOff(t *testing.T) {
	cfg := sharing.Config{Off: true, Kinds: map[sharing.Kind]sharing.Setting{}}
	for _, k := range sharing.Kinds() {
		cfg.Kinds[k] = sharing.Setting{Enabled: true, Endpoint: "https://collector.example"}
	}
	var a Audit
	a.FromGate(sharing.New(cfg, nil))
	if len(a.Permits) != len(EgressKinds) {
		t.Fatalf("audit lists %d kinds, want %d", len(a.Permits), len(EgressKinds))
	}
	for _, p := range a.Permits {
		if p.Allowed || p.State != string(sharing.StateOff) || p.Reason != string(sharing.ReasonOff) {
			t.Errorf("%+v, want denied, off, stopped by the switch", p)
		}
	}
	if got := a.PermitLine(); got != "dsh: audit: outbound permits: none" {
		t.Errorf("PermitLine() = %q", got)
	}
	line := a.KindsLine()
	if !strings.HasPrefix(line, "dsh: audit: outbound kinds (off-switch set): ") {
		t.Errorf("KindsLine() = %q, want the switch named", line)
	}
	for _, k := range EgressKinds {
		if !strings.Contains(line, k+"=off") {
			t.Errorf("KindsLine() = %q, missing %s=off", line, k)
		}
	}
}

// The real audit builds the gate from the launch environment: DSH_SHARING_OFF set
// to "0" locks the switch, and with nothing configured every kind is off.
func TestRealAuditReadsOffSwitchFromLaunchEnv(t *testing.T) {
	s := &State{Env: launch.NewSnapshot(runtime.GOOS, []string{sharing.EnvOff + "=0"})}
	_ = Real{}.Audit(context.Background(), s) // no loader: the permits are filled first
	if s.Sharing == nil || !s.Audit.Off || !s.Audit.OffLocked {
		t.Fatalf("audit off=%v locked=%v gate=%v, want the switch locked by the environment", s.Audit.Off, s.Audit.OffLocked, s.Sharing)
	}
	if n := len(s.Audit.Allowed()); n != 0 {
		t.Fatalf("%d kinds allowed under the off-switch", n)
	}
	if line := s.Audit.KindsLine(); !strings.Contains(line, "(off-switch set by "+sharing.EnvOff+")") {
		t.Fatalf("KindsLine() = %q", line)
	}
}

// Run prints the kinds line once after the permit line.
func TestRunPrintsKindsLine(t *testing.T) {
	fakes := &fakeSteps{}
	app := launch.NewApp()
	var stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- Run(context.Background(), Options{Stderr: &stderr, App: app, Steps: fakes}) }()
	<-app.Ready()
	app.Stop(launch.Completed, 0)
	<-done
	got := stderr.String()
	want := "dsh: audit: outbound permits: none\ndsh: audit: outbound kinds: model-request=off, "
	if !strings.Contains(got, want) || strings.Count(got, "outbound kinds") != 1 {
		t.Fatalf("stderr = %q, want %q once", got, want)
	}
}

// bootKinds runs the real boot over the core bundle plus layer, with environ as
// the process environment, and returns the kinds line it printed.
func bootKinds(t *testing.T, layer string, environ ...string) string {
	t.Helper()
	opts, _ := fixture(t, layer)
	opts.Request.Environ = environ
	app := launch.NewApp()
	var stderr syncBuffer
	opts.App, opts.Stderr = app, &stderr
	done := make(chan int, 1)
	go func() { done <- Run(context.Background(), opts) }()
	select {
	case <-app.Ready():
	case status := <-done:
		t.Fatalf("boot ended with %d before ready:\n%s", status, stderr.String())
	}
	app.Stop(launch.Completed, 0)
	<-done
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(line, "dsh: audit: outbound kinds") {
			return line
		}
	}
	t.Fatalf("no kinds line:\n%s", stderr.String())
	return ""
}

// The sharing settings are composed configuration: the core bundle's host.sharing
// entry is every kind off; a layer that turns feedback on is listed on through the
// real boot path, and the off-switch -- the layer's sharing.off or DSH_SHARING_OFF
// in the launch environment -- overrides it.
func TestComposedSharingSettingsReachTheAudit(t *testing.T) {
	const optIn = "- id: sharing\n  merge:\n    kinds:\n      feedback:\n        enabled: true\n        endpoint: https://feedback.example\n"
	if line := bootKinds(t, "[]\n"); strings.Contains(line, "=on") || strings.Contains(line, "off-switch") {
		t.Fatalf("default boot: %q, want every kind off", line)
	}
	line := bootKinds(t, optIn)
	if !strings.Contains(line, "feedback=on") || !strings.Contains(line, "model-request=off") || strings.Count(line, "=on") != 1 {
		t.Fatalf("opt-in: %q, want feedback on and every other kind off", line)
	}
	line = bootKinds(t, optIn+"- id: sharing\n  merge:\n    off: true\n")
	if !strings.Contains(line, "(off-switch set): ") || strings.Contains(line, "=on") {
		t.Fatalf("sharing.off: %q, want every kind off by the switch", line)
	}
	line = bootKinds(t, optIn, sharing.EnvOff+"=0")
	if !strings.Contains(line, "(off-switch set by "+sharing.EnvOff+")") || strings.Contains(line, "=on") {
		t.Fatalf("%s: %q, want every kind off, locked by the environment", sharing.EnvOff, line)
	}
}

// The outbound policy is required in every profile: a configured certificate file
// that is missing, or holds no certificate, fails the util.egress entry and the
// boot ends with a configuration status, one fatal message naming the entry and
// the cause, and no readiness.
func TestUnreadableCertificateStopsTheLaunch(t *testing.T) {
	junk := filepath.Join(t.TempDir(), "junk.pem")
	if err := os.WriteFile(junk, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "absent.pem"),
		"invalid": junk,
	} {
		t.Run(name, func(t *testing.T) {
			opts, _ := fixture(t, "- id: egress\n  merge:\n    extraCACerts: ['"+file+"']\n")
			app := launch.NewApp()
			var stderr syncBuffer
			opts.App, opts.Stderr = app, &stderr
			if status := Run(context.Background(), opts); status != output.Config {
				t.Errorf("status = %d, want %d", status, output.Config)
			}
			select {
			case <-app.Ready():
				t.Fatal("the launch announced readiness with the outbound policy failed")
			default:
			}
			got := stderr.String()
			if n := strings.Count(got, "dsh: fatal:"); n != 1 {
				t.Fatalf("%d fatal messages, want 1:\n%s", n, got)
			}
			if !strings.Contains(got, "required entry not active: egress") {
				t.Errorf("the message does not name the egress entry:\n%s", got)
			}
			if !strings.Contains(got, "certificate file "+file) {
				t.Errorf("the message does not give the certificate cause:\n%s", got)
			}
		})
	}
}
