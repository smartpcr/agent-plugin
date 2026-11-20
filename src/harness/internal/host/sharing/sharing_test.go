package sharing_test

import (
	"context"
	"errors"
	"testing"

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
