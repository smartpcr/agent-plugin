package boot

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
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
