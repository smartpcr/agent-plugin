//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// epoState is one scenario's world.
type epoState struct {
	srv   *httptest.Server
	conns atomic.Int64

	mu       sync.Mutex
	arrived  int
	agents   []string
	bodySeen []string

	svc    *egress.Service
	gate   *sharing.Gate
	permit *sharing.Permit
	client *http.Client
	err    error

	cfg     sharing.Config
	denials map[sharing.Kind]error
	audit   boot.Audit
}

func (s *epoState) close() {
	if s.client != nil && s.svc != nil {
		s.svc.Release(s.client)
	}
	if s.permit != nil {
		s.permit.Release()
	}
	if s.svc != nil {
		s.svc.CloseIdleConnections()
	}
	if s.srv != nil {
		s.srv.Close()
	}
}

func (s *epoState) loopbackServer() error {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.arrived++
		s.agents = append(s.agents, r.Header.Get("User-Agent"))
		s.bodySeen = append(s.bodySeen, string(b))
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv.Listener = l
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			s.conns.Add(1)
		}
	}
	srv.Start()
	s.srv = srv
	return nil
}

func (s *epoState) serviceAndGate(kind string) error {
	s.svc = egress.New(egress.Config{})
	s.gate = sharing.New(sharing.Config{Kinds: map[sharing.Kind]sharing.Setting{
		sharing.Kind(kind): {Enabled: true, Endpoint: s.srv.URL},
	}}, nil)
	return nil
}

func (s *epoState) post(c *http.Client) error {
	resp, err := c.Post(s.srv.URL+"/v1/chat", "application/json", strings.NewReader(`{"probe":true}`))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (s *epoState) postWithoutPermit() error {
	// The no-permit path: Client refuses a nil permit before any transport exists.
	c, err := s.svc.Client(string(sharing.ModelRequest), nil, egress.Options{Label: "e2e"})
	if err == nil {
		s.err = s.post(c)
		s.svc.Release(c)
	} else {
		s.err = err
	}
	// A nil *sharing.Permit (what a refused gate leaves behind) is refused too, and
	// so is a permit for a different kind.
	var none *sharing.Permit
	if _, err := s.svc.Client(string(sharing.ModelRequest), none, egress.Options{}); !egress.IsNotPermitted(err) {
		return fmt.Errorf("nil *sharing.Permit: want egress/not-permitted, got %v", err)
	}
	if _, err := s.gate.Permit(sharing.Telemetry); err == nil {
		return fmt.Errorf("telemetry permit given though its kind is off")
	}
	return nil
}

func (s *epoState) requestFails(reason string) error {
	if s.err == nil {
		return fmt.Errorf("request succeeded; want %s", reason)
	}
	if !herr.Has(s.err, herr.Reason(reason)) || !egress.IsNotPermitted(s.err) {
		return fmt.Errorf("want %s, got %v (kind %s)", reason, s.err, herr.Kind(s.err))
	}
	return nil
}

func (s *epoState) connCount(n int) error {
	if got := s.conns.Load(); got != int64(n) {
		return fmt.Errorf("connection count %d, want %d", got, n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == 0 && s.arrived != 0 {
		return fmt.Errorf("%d requests arrived, want 0", s.arrived)
	}
	return nil
}

func (s *epoState) connAtLeast(n int) error {
	if got := s.conns.Load(); got < int64(n) {
		return fmt.Errorf("connection count %d, want at least %d", got, n)
	}
	return nil
}

func (s *epoState) postWithPermit(kind string) error {
	p, err := s.gate.Permit(sharing.Kind(kind))
	if err != nil {
		return fmt.Errorf("gate refused %s: %w", kind, err)
	}
	s.permit = p
	c, err := s.svc.Client(kind, p, egress.Options{Label: "e2e"})
	if err != nil {
		return fmt.Errorf("client for %s: %w", kind, err)
	}
	s.client = c
	s.err = s.post(c)
	return nil
}

func (s *epoState) arrivedWithAgent(ua string) error {
	if s.err != nil {
		return fmt.Errorf("request failed: %v", s.err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.arrived != 1 {
		return fmt.Errorf("%d requests arrived, want 1", s.arrived)
	}
	if s.agents[0] != ua {
		return fmt.Errorf("user agent %q, want %q", s.agents[0], ua)
	}
	if s.bodySeen[0] != `{"probe":true}` {
		return fmt.Errorf("body %q", s.bodySeen[0])
	}
	return nil
}

func (s *epoState) everyKindEnabled() error {
	s.cfg = sharing.Config{Kinds: map[sharing.Kind]sharing.Setting{}}
	for _, k := range sharing.Kinds() {
		s.cfg.Kinds[k] = sharing.Setting{Enabled: true, Endpoint: "https://" + string(k) + ".example.invalid"}
	}
	// Sanity: without the switch, every kind is on.
	for _, st := range sharing.New(s.cfg, nil).Senders() {
		if st.State != sharing.StateOn {
			return fmt.Errorf("%s is %s without the switch", st.Kind, st.State)
		}
	}
	return nil
}

func (s *epoState) offSwitchOn() error {
	s.cfg.Off = true
	s.gate = sharing.New(s.cfg, nil)
	if off, _ := s.gate.Off(); !off {
		return fmt.Errorf("gate does not report the off-switch")
	}
	return nil
}

func (s *epoState) evaluate() error {
	s.denials = map[sharing.Kind]error{}
	for _, k := range sharing.Kinds() {
		p, err := s.gate.Permit(k)
		if err == nil {
			p.Release()
		}
		s.denials[k] = err
	}
	s.audit.FromGate(s.gate)
	return nil
}

func (s *epoState) everyKindDenied(reason string) error {
	if len(s.denials) != len(sharing.Kinds()) {
		return fmt.Errorf("evaluated %d kinds, want %d", len(s.denials), len(sharing.Kinds()))
	}
	for _, k := range sharing.Kinds() {
		err := s.denials[k]
		if err == nil {
			return fmt.Errorf("%s was permitted under the off-switch", k)
		}
		if herr.Kind(err) != herr.Reason(reason) {
			return fmt.Errorf("%s denied with %s, want %s", k, herr.Kind(err), reason)
		}
	}
	if n := s.gate.Live(); n != 0 {
		return fmt.Errorf("%d live permits, want 0", n)
	}
	return nil
}

func (s *epoState) auditListsEach(state string) error {
	if len(s.audit.Permits) != len(boot.EgressKinds) {
		return fmt.Errorf("audit lists %d kinds, want %d", len(s.audit.Permits), len(boot.EgressKinds))
	}
	line := s.audit.KindsLine()
	for i, k := range boot.EgressKinds {
		p := s.audit.Permits[i]
		if p.Kind != k || p.State != state || p.Allowed || p.Reason != string(sharing.ReasonOff) {
			return fmt.Errorf("audit row %d = %+v, want %s %s denied by %s", i, p, k, state, sharing.ReasonOff)
		}
		if !strings.Contains(line, k+"="+state) {
			return fmt.Errorf("kinds line %q lacks %s=%s", line, k, state)
		}
	}
	return nil
}

func (s *epoState) auditOffNoPermits() error {
	if !s.audit.Off || s.audit.OffLocked {
		return fmt.Errorf("audit off=%v locked=%v, want off and not locked", s.audit.Off, s.audit.OffLocked)
	}
	if line := s.audit.KindsLine(); !strings.Contains(line, "(off-switch set)") {
		return fmt.Errorf("kinds line %q does not report the switch", line)
	}
	if got := s.audit.PermitLine(); got != "dsh: audit: outbound permits: none" {
		return fmt.Errorf("permit line %q", got)
	}
	// The environment switch locks it, even "0".
	g := sharing.New(s.cfg, func(n string) (string, bool) {
		if n == sharing.EnvOff {
			return "0", true
		}
		return "", false
	})
	var a boot.Audit
	a.FromGate(g)
	if !a.OffLocked || !strings.Contains(a.KindsLine(), "set by "+sharing.EnvOff) {
		return fmt.Errorf("env off-switch not reported locked: %q", a.KindsLine())
	}
	return nil
}

// InitializeScenario_boot_launch_egress_secrets_and_test_kit_egress_policy_and_telemetry_off_switch registers the steps.
func InitializeScenario_boot_launch_egress_secrets_and_test_kit_egress_policy_and_telemetry_off_switch(ctx *godog.ScenarioContext) {
	s := &epoState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = epoState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return c, nil
	})

	ctx.Step(`^a loopback HTTP server on 127\.0\.0\.1:0 counting connections$`, s.loopbackServer)
	ctx.Step(`^an egress service and a sharing gate whose "([^"]+)" kind is enabled for the server$`, s.serviceAndGate)
	ctx.Step(`^the egress client is asked to POST to the server without a permit$`, s.postWithoutPermit)
	ctx.Step(`^the request fails with "([^"]+)"$`, s.requestFails)
	ctx.Step(`^the server's connection count is (\d+)$`, s.connCount)
	ctx.Step(`^the server's connection count is at least (\d+)$`, s.connAtLeast)
	ctx.Step(`^the egress client is asked to POST to the server with a "([^"]+)" permit$`, s.postWithPermit)
	ctx.Step(`^the request arrives at the server with user agent "([^"]+)"$`, s.arrivedWithAgent)

	ctx.Step(`^a sharing gate with every kind enabled with an endpoint$`, s.everyKindEnabled)
	ctx.Step(`^the off-switch is on$`, s.offSwitchOn)
	ctx.Step(`^permit decisions are evaluated in memory$`, s.evaluate)
	ctx.Step(`^every kind is denied with "([^"]+)"$`, s.everyKindDenied)
	ctx.Step(`^the boot audit lists each kind as "([^"]+)"$`, s.auditListsEach)
	ctx.Step(`^the boot audit reports the off-switch set and no outbound permits$`, s.auditOffNoPermits)
}

func TestE2E_boot_launch_egress_secrets_and_test_kit_egress_policy_and_telemetry_off_switch(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "boot_launch_egress_secrets_and_test_kit_egress_policy_and_telemetry_off_switch",
		ScenarioInitializer: InitializeScenario_boot_launch_egress_secrets_and_test_kit_egress_policy_and_telemetry_off_switch,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"boot_launch_egress_secrets_and_test_kit_egress_policy_and_telemetry_off_switch.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
