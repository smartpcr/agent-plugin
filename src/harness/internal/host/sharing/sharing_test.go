package sharing_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

func everyKindOn() sharing.Config {
	cfg := sharing.Config{Kinds: map[sharing.Kind]sharing.Setting{}}
	for _, k := range sharing.Kinds() {
		cfg.Kinds[k] = sharing.Setting{Enabled: true, Endpoint: "https://collector.example/" + string(k)}
	}
	return cfg
}

func env(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

// Nothing leaves by default: with no configuration every kind is off.
func TestEveryKindOffByDefault(t *testing.T) {
	g := sharing.New(sharing.Config{}, nil)
	for _, st := range g.Senders() {
		if st.State != sharing.StateOff || st.Reason != sharing.ReasonDisabled || st.Setting == "" || st.About == "" {
			t.Errorf("default %+v, want off/disabled with a setting and a description", st)
		}
		if _, err := g.Permit(st.Kind); herr.Kind(err) != sharing.ReasonDisabled {
			t.Errorf("Permit(%s) err = %v, want %s", st.Kind, err, sharing.ReasonDisabled)
		}
	}
}

// TC-06a: with permits configured for every kind and the off-switch on, every kind
// is denied and listed off, stopped by the switch; the environment variable, even
// "0", locks the switch and clearing the config field changes nothing.
func TestOffSwitchOverridesPermits(t *testing.T) {
	cfg := everyKindOn()
	cfg.Off = true
	for name, g := range map[string]*sharing.Gate{
		"config":      sharing.New(cfg, nil),
		"environment": sharing.New(everyKindOn(), env(map[string]string{sharing.EnvOff: "0"})),
	} {
		t.Run(name, func(t *testing.T) {
			for _, k := range sharing.Kinds() {
				if p, err := g.Permit(k); p != nil || herr.Kind(err) != sharing.ReasonOff {
					t.Errorf("Permit(%s) = %v, %v; want refused with %s", k, p, err, sharing.ReasonOff)
				}
			}
			senders := g.Senders()
			if len(senders) != len(sharing.Kinds()) {
				t.Fatalf("Senders() lists %d kinds, want %d", len(senders), len(sharing.Kinds()))
			}
			for _, st := range senders {
				if st.State != sharing.StateOff || !st.StoppedBySwitch() {
					t.Errorf("%s: state %s reason %s, want off, stopped by the switch", st.Kind, st.State, st.Reason)
				}
			}
			off, locked := g.Off()
			if !off || locked != (name == "environment") {
				t.Errorf("Off() = %v, %v", off, locked)
			}
		})
	}

	g := sharing.New(everyKindOn(), env(map[string]string{sharing.EnvOff: "0"}))
	g.SetOff(false)
	if _, err := g.Permit(sharing.ModelRequest); herr.Kind(err) != sharing.ReasonOff {
		t.Fatalf("clearing the field under %s gave err %v, want %s", sharing.EnvOff, err, sharing.ReasonOff)
	}
	if off := sharing.New(everyKindOn(), env(map[string]string{sharing.EnvOff: ""})); func() bool { o, _ := off.Off(); return o }() {
		t.Fatal("an empty DSH_SHARING_OFF set the switch")
	}
}

// Setting the switch withdraws a live permit: its context ends and the egress
// client refuses the next request before dialling.
func TestSwitchWithdrawsLivePermit(t *testing.T) {
	g := sharing.New(everyKindOn(), nil)
	p, err := g.Permit(sharing.Telemetry)
	if err != nil {
		t.Fatal(err)
	}
	svc := egress.New(egress.Config{})
	if _, err := svc.Client(string(sharing.Telemetry), p, egress.Options{}); err != nil {
		t.Fatalf("a live permit was refused: %v", err)
	}
	g.SetOff(true)
	if p.Context().Err() == nil {
		t.Fatal("the permit outlived the off-switch")
	}
	if herr.Kind(context.Cause(p.Context())) != sharing.ReasonWithdrawn {
		t.Fatalf("cause = %v, want %s", context.Cause(p.Context()), sharing.ReasonWithdrawn)
	}
	if _, err := svc.Client(string(sharing.Telemetry), p, egress.Options{}); !egress.IsNotPermitted(err) {
		t.Fatalf("a withdrawn permit got a client: %v", err)
	}
	var none *sharing.Permit
	if _, err := svc.Client(string(sharing.ModelRequest), none, egress.Options{}); !errors.Is(err, egress.ErrNotPermitted) {
		t.Fatalf("a nil *sharing.Permit got a client: %v", err)
	}
}

// Turning one kind off or moving its endpoint withdraws that kind's permit only.
func TestConfigureWithdrawsChangedKindsOnly(t *testing.T) {
	g := sharing.New(everyKindOn(), nil)
	model, _ := g.Permit(sharing.ModelRequest)
	feedback, _ := g.Permit(sharing.Feedback)
	update, _ := g.Permit(sharing.UpdateCheck)
	cfg := everyKindOn()
	cfg.Kinds[sharing.Feedback] = sharing.Setting{}
	cfg.Kinds[sharing.UpdateCheck] = sharing.Setting{Enabled: true, Endpoint: "https://other.example"}
	g.Configure(cfg)
	if model.Context().Err() != nil || feedback.Context().Err() == nil || update.Context().Err() == nil {
		t.Fatalf("withdrawn: model=%v feedback=%v update=%v; want only feedback and update",
			model.Context().Err(), feedback.Context().Err(), update.Context().Err())
	}
	cfg.Kinds[sharing.Feedback] = sharing.Setting{Enabled: true}
	g.Configure(cfg)
	if _, err := g.Permit(sharing.Feedback); herr.Kind(err) != sharing.ReasonNoEndpoint {
		t.Fatalf("enabled without endpoint: err %v, want %s", err, sharing.ReasonNoEndpoint)
	}
	if _, err := g.Permit("carrier-pigeon"); herr.Kind(err) != sharing.ReasonUnregistered {
		t.Fatalf("unknown kind: err %v, want %s", err, sharing.ReasonUnregistered)
	}
}

// A real gate's permit withdrawn while its request is in flight: the request is
// cancelled and the error keeps egress/not-permitted as its kind (not the
// permit's sharing/withdrawn cause), so the exit status is the egress one.
func TestWithdrawnInFlightKeepsEgressReason(t *testing.T) {
	entered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	cfg := everyKindOn()
	cfg.Kinds[sharing.Telemetry] = sharing.Setting{Enabled: true, Endpoint: srv.URL}
	g := sharing.New(cfg, nil)
	p, err := g.Permit(sharing.Telemetry)
	if err != nil {
		t.Fatal(err)
	}
	svc := egress.New(egress.Config{})
	t.Cleanup(svc.CloseIdleConnections)
	c, err := svc.Client(string(sharing.Telemetry), p, egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		resp, err := c.Post(srv.URL, "text/plain", strings.NewReader("batch"))
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("the request ended before arriving: %v", err)
	}
	g.SetOff(true)
	select {
	case err := <-done:
		if herr.Kind(err) != egress.ReasonNotPermitted || output.Status(err) != output.Denied {
			t.Fatalf("err = %v: kind %q status %d, want %s (status %d)", err, herr.Kind(err), output.Status(err), egress.ReasonNotPermitted, output.Denied)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the request in flight was not cancelled")
	}
}

// Permits belong to their holder: a released permit, or one whose owner ended,
// leaves the gate without consent changing, so asking repeatedly while the kind
// stays on accumulates nothing.
func TestPermitsAreReleasedByHolderAndOwner(t *testing.T) {
	g := sharing.New(everyKindOn(), nil)
	for range 100 {
		p, err := g.Permit(sharing.Analytics)
		if err != nil {
			t.Fatal(err)
		}
		p.Release()
		p.Release()
		if herr.Kind(context.Cause(p.Context())) != sharing.ReasonReleased {
			t.Fatalf("cause = %v, want %s", context.Cause(p.Context()), sharing.ReasonReleased)
		}
	}
	owner, unload := context.WithCancel(context.Background())
	held := make([]*sharing.Permit, 0, 10)
	for range 10 {
		p, err := g.PermitFor(owner, sharing.Analytics)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, p)
	}
	if n := g.Live(); n != 10 {
		t.Fatalf("Live() = %d with ten owned permits, want 10", n)
	}
	unload()
	for _, p := range held {
		<-p.Context().Done()
	}
	if n := g.Live(); n != 0 {
		t.Fatalf("Live() = %d after the owner ended, want 0", n)
	}
	if _, err := g.PermitFor(owner, sharing.Analytics); herr.Kind(err) != sharing.ReasonReleased {
		t.Fatalf("an ended owner got a permit: %v", err)
	}
	if p, err := g.Permit(sharing.Analytics); err != nil || p.Context().Err() != nil {
		t.Fatalf("consent is unchanged, yet Permit = %v, %v", p, err)
	}
}

// DH-T-HOST-19, the two gate rows: a permit for an unregistered kind is refused,
// even when the configuration turns that kind on; and every sharing-purpose client
// egress hands out belongs to a listed sender, because egress hands a client only
// for its permit's own kind and the gate issues permits only for table rows. The
// table holds each kind once, so no kind has a second sender row.
//
//dsh:test DH-T-HOST-19
func TestEverySharingClientBelongsToAListedSender(t *testing.T) {
	cfg := everyKindOn()
	for _, k := range []sharing.Kind{"carrier-pigeon", "", "Telemetry", "telemetry "} {
		cfg.Kinds[k] = sharing.Setting{Enabled: true, Endpoint: "https://collector.example/x"}
	}
	g := sharing.New(cfg, nil)
	for _, k := range []sharing.Kind{"carrier-pigeon", "", "Telemetry", "telemetry "} {
		if p, err := g.Permit(k); p != nil || herr.Kind(err) != sharing.ReasonUnregistered {
			t.Errorf("Permit(%q) = %v, %v; want refused with %s", k, p, err, sharing.ReasonUnregistered)
		}
		if _, ok := sharing.Describe(k); ok {
			t.Errorf("Describe(%q) found a row", k)
		}
	}
	if n := len(g.Senders()); n != len(sharing.Kinds()) {
		t.Errorf("Senders() lists %d kinds with unregistered kinds configured, want %d", n, len(sharing.Kinds()))
	}

	listed := map[string]bool{}
	for _, k := range sharing.Kinds() {
		if listed[string(k)] {
			t.Errorf("kind %s has two rows", k)
		}
		listed[string(k)] = true
	}
	svc := egress.New(egress.Config{})
	t.Cleanup(svc.CloseIdleConnections)
	for _, k := range sharing.Kinds() {
		p, err := g.Permit(k)
		if err != nil {
			t.Fatalf("Permit(%s): %v", k, err)
		}
		if _, err := svc.Client("carrier-pigeon", p, egress.Options{}); !egress.IsNotPermitted(err) {
			t.Errorf("a %s permit got a client for an unlisted kind: %v", k, err)
		}
		if _, err := svc.Client(string(k), p, egress.Options{Label: string(k)}); err != nil {
			t.Fatalf("Client(%s): %v", k, err)
		}
	}
	handed := svc.Handed()
	if len(handed) != len(sharing.Kinds()) {
		t.Fatalf("Handed() = %d clients, want one per kind (%d)", len(handed), len(sharing.Kinds()))
	}
	for _, c := range handed {
		if !listed[c.Kind] {
			t.Errorf("client %+v belongs to no listed sender", c)
		}
		if info, _ := sharing.Describe(sharing.Kind(c.Kind)); c.Destination != cfg.Kinds[info.Kind].Endpoint {
			t.Errorf("client %+v does not go to its sender's endpoint %q", c, cfg.Kinds[info.Kind].Endpoint)
		}
	}
}
