// Package sharing is the one place that says whether data may leave the machine
// (DH-D-HOST-4, docs/harness/areas/host-telemetry.md §6.1 "The sharing gate").
//
// Every kind of outbound data is a row of a fixed table, each with its own visible
// setting (enabled and endpoint). Every kind is off until its own setting is turned
// on, and one off-switch -- the live config field Off, or the environment variable
// DSH_SHARING_OFF set to any non-empty value, even "0" -- denies every kind. The
// variable wins, and Off reports the switch as locked.
//
// A Permit is the gate's "yes, for now" for one kind and one destination. Its
// context ends the moment the decision changes (the switch is set, the kind's
// setting is turned off, or its endpoint changes), so a sender that derives its
// requests from it stops without a last flush. The egress client
// (internal/util/egress) hands out an HTTP client only to a permit holder.
package sharing

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Kind names one kind of data that may leave the machine.
type Kind string

// The kinds, in the order the boot audit and the settings page list them.
const (
	ModelRequest           Kind = "model-request"
	SessionUpload          Kind = "session-upload"
	SessionLogContribution Kind = "session-log-contribution"
	ModelRequestExtras     Kind = "model-request-extras"
	Feedback               Kind = "feedback"
	Analytics              Kind = "analytics"
	Telemetry              Kind = "telemetry"
	UpdateCheck            Kind = "update-check"
	AccountNotices         Kind = "account-notices"
	Translation            Kind = "translation"
	SpeechAudio            Kind = "speech-audio"
)

// EnvOff is the environment variable that sets the off-switch when non-empty.
const EnvOff = "DSH_SHARING_OFF"

// Reasons a Permit is refused, and the cause a withdrawn permit's context ends with.
const (
	ReasonOff          herr.Reason = "sharing/off"
	ReasonDisabled     herr.Reason = "sharing/disabled"
	ReasonNoEndpoint   herr.Reason = "sharing/no-endpoint"
	ReasonUnregistered herr.Reason = "sharing/unregistered"
	ReasonWithdrawn    herr.Reason = "sharing/withdrawn"
	// ReasonReleased is the cause a permit's context ends with when its holder
	// releases it (or the holder's owner ends).
	ReasonReleased herr.Reason = "sharing/released"
	// ReasonClaimed refuses a second host.sharing entry: one entry configures the
	// launch's gate.
	ReasonClaimed herr.Reason = "sharing/claimed"
)

// Info describes one kind: what is sent, and by whom.
type Info struct {
	Kind  Kind   `json:"kind"`
	About string `json:"about"`
}

// table is the complete list of kinds (host-telemetry.md, the sender table). A new
// sender is a new row.
var table = []Info{
	{ModelRequest, "A model request to the provider route the user configured (A04)"},
	{SessionUpload, "Session events on a confirmed feedback submission (A06)"},
	{SessionLogContribution, "Session-log lines attached to a vendor's model request (A05)"},
	{ModelRequestExtras, "Installation id, session id and purpose, inventory, client name and version (A04)"},
	{Feedback, "Feedback text and rating (A17)"},
	{Analytics, "Product usage events (A18)"},
	{Telemetry, "Operational events (A18)"},
	{UpdateCheck, "The automatic version query (A24)"},
	{AccountNotices, "Polling the vendor account for notices, and the notice reports (A17)"},
	{Translation, "One paragraph of model reasoning and two language codes to a translation endpoint (X6)"},
	{SpeechAudio, "One recording to the remote speech provider the user configured (X4)"},
}

// Kinds returns every kind, in table order.
func Kinds() []Kind {
	out := make([]Kind, len(table))
	for i, row := range table {
		out[i] = row.Kind
	}
	return out
}

// Describe returns the table row of k.
func Describe(k Kind) (Info, bool) {
	i := slices.IndexFunc(table, func(row Info) bool { return row.Kind == k })
	if i < 0 {
		return Info{}, false
	}
	return table[i], true
}

// Setting is one kind's visible setting. The zero value is off.
type Setting struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint,omitempty"`
}

// Config is the gate's configuration (`sharing.Config`): the off-switch and one
// setting per kind. A kind absent from Kinds is off.
type Config struct {
	Off   bool             `json:"off"`
	Kinds map[Kind]Setting `json:"kinds,omitempty"`
}

// State is a kind's state as the audit and the settings page show it.
type State string

const (
	// StateOff is a kind that may not send: its setting is off, or the switch is set.
	StateOff State = "off"
	// StateOn is a kind that is given a permit on request.
	StateOn State = "on"
	// StateUnavailable is a kind turned on with no endpoint to send to.
	StateUnavailable State = "unavailable"
)

// Status is one kind as Senders lists it.
type Status struct {
	Kind  Kind   `json:"kind"`
	About string `json:"about"`
	// Setting and EndpointSetting are the settings addresses of the kind's switch
	// and destination, so the settings page keeps no table of its own.
	Setting         string `json:"setting"`
	EndpointSetting string `json:"endpointSetting"`
	Endpoint        string `json:"endpoint,omitempty"`
	State           State  `json:"state"`
	// Reason says why the kind is not on: sharing/off (stopped by the switch),
	// sharing/disabled, sharing/no-endpoint or sharing/unregistered; "" when on.
	Reason herr.Reason `json:"reason,omitempty"`
}

// StoppedBySwitch reports whether the kind is off because of the off-switch.
func (s Status) StoppedBySwitch() bool { return s.Reason == ReasonOff }

// Permit is the gate's "yes, for now" for one kind and one destination. Its
// context ends, with a sharing/withdrawn cause, when consent is withdrawn, and
// with sharing/released when its holder releases it.
type Permit struct {
	gate   *Gate
	kind   Kind
	dest   string
	ctx    context.Context
	cancel context.CancelCauseFunc
	owner  context.Context
	stop   func() bool
}

// Release gives the permit back: the gate forgets it and its context ends with a
// sharing/released cause. Releasing twice, or a nil permit, does nothing.
func (p *Permit) Release() {
	if p == nil || p.gate == nil {
		return
	}
	p.gate.forget(p, herr.Newf(ReasonReleased, "permit for %s released", p.kind))
}

// Kind returns the permitted kind as a string, the form the egress client reads.
// A nil permit has no kind, so egress refuses it.
func (p *Permit) Kind() string {
	if p == nil {
		return ""
	}
	return string(p.kind)
}

// DataKind returns the permitted kind.
func (p *Permit) DataKind() Kind {
	if p == nil {
		return ""
	}
	return p.kind
}

// Destination is the endpoint the permit allows sending to.
func (p *Permit) Destination() string {
	if p == nil {
		return ""
	}
	return p.dest
}

// Context ends when consent is withdrawn; a nil permit's has already ended.
func (p *Permit) Context() context.Context {
	if p == nil {
		return withdrawn
	}
	return p.ctx
}

var withdrawn = func() context.Context {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(herr.New(ReasonWithdrawn, "no permit"))
	return ctx
}()

// Gate decides, per kind, whether data may leave. It is safe for concurrent use.
type Gate struct {
	mu      sync.Mutex
	cfg     Config
	envOff  bool
	permits map[*Permit]struct{}
	claimed bool
}

// New returns a gate over cfg. lookup reads the launch environment for EnvOff; nil
// reads nothing. The gate never reads the process environment itself.
func New(cfg Config, lookup func(name string) (string, bool)) *Gate {
	g := &Gate{cfg: clone(cfg), permits: map[*Permit]struct{}{}}
	if lookup != nil {
		if v, ok := lookup(EnvOff); ok && v != "" {
			g.envOff = true
		}
	}
	return g
}

// Permit returns a permit for k, or an error carrying sharing/off,
// sharing/disabled, sharing/no-endpoint or sharing/unregistered. The gate holds
// it until it is withdrawn or released; prefer PermitFor, which releases it with
// its owner.
func (g *Gate) Permit(k Kind) (*Permit, error) { return g.PermitFor(nil, k) }

// PermitFor is Permit owned by owner (the asking fiber's Lifetime): the permit is
// released when owner ends, so a fiber that unloads leaves nothing registered. A
// nil owner leaves the release to Permit.Release or a withdrawal.
func (g *Gate) PermitFor(owner context.Context, k Kind) (*Permit, error) {
	if owner != nil && owner.Err() != nil {
		return nil, herr.Newf(ReasonReleased, "the asker of a %s permit has ended", k)
	}
	g.mu.Lock()
	st := g.decide(k)
	if st.State != StateOn {
		g.mu.Unlock()
		return nil, herr.Newf(st.Reason, "no permit for %s", k)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	p := &Permit{gate: g, kind: k, dest: st.Endpoint, ctx: ctx, cancel: cancel, owner: owner}
	g.permits[p] = struct{}{}
	g.mu.Unlock()
	if owner != nil {
		stop := context.AfterFunc(owner, p.Release)
		g.mu.Lock()
		_, live := g.permits[p]
		if live {
			p.stop = stop
		}
		g.mu.Unlock()
		if !live {
			stop()
		}
	}
	return p, nil
}

// Live is the number of permits the gate holds: given, and not yet withdrawn or
// released.
func (g *Gate) Live() int {
	g.mu.Lock()
	var ended []*Permit
	for p := range g.permits {
		if p.owner != nil && p.owner.Err() != nil {
			ended = append(ended, p)
		}
	}
	g.mu.Unlock()
	for _, p := range ended {
		p.Release()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.permits)
}

// forget drops p and ends its context with cause.
func (g *Gate) forget(p *Permit, cause error) {
	g.mu.Lock()
	_, ok := g.permits[p]
	delete(g.permits, p)
	stop := p.stop
	p.stop = nil
	g.mu.Unlock()
	if !ok {
		return
	}
	p.cancel(cause)
	if stop != nil {
		stop()
	}
}

// Off reports whether the off-switch is set, and whether the environment set it
// (so the settings page shows it locked).
func (g *Gate) Off() (off, lockedByEnvironment bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.envOff || g.cfg.Off, g.envOff
}

// Configure applies a new configuration (the live reload of `sharing.Config`).
// Every live permit whose kind is no longer on, or whose endpoint changed, is
// withdrawn: its context ends.
func (g *Gate) Configure(cfg Config) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.configureLocked(clone(cfg))
}

// configureLocked installs cfg (already owned by the gate) and withdraws every
// live permit it no longer allows. The caller holds g.mu.
func (g *Gate) configureLocked(cfg Config) {
	g.cfg = cfg
	for p := range g.permits {
		st := g.decide(p.kind)
		if st.State != StateOn || st.Endpoint != p.dest {
			p.cancel(herr.Newf(ReasonWithdrawn, "permit for %s withdrawn: %s", p.kind, st.Reason))
			delete(g.permits, p)
			if p.stop != nil {
				p.stop()
				p.stop = nil
			}
		}
	}
}

// SetOff sets or clears the live off-switch field. Clearing it while EnvOff is set
// changes nothing.
func (g *Gate) SetOff(off bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := clone(g.cfg)
	cfg.Off = off
	g.configureLocked(cfg)
}

// claim marks the gate configured by one entry; a second claim fails.
func (g *Gate) claim() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.claimed {
		return herr.New(ReasonClaimed, "another host.sharing entry configures the gate")
	}
	g.claimed = true
	return nil
}

func (g *Gate) unclaim() {
	g.mu.Lock()
	g.claimed = false
	g.mu.Unlock()
}

// Senders lists every kind with its setting addresses and state, in table order.
func (g *Gate) Senders() []Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Status, len(table))
	for i, row := range table {
		out[i] = g.decide(row.Kind)
	}
	return out
}

// decide is the one decision rule; g.mu is held.
func (g *Gate) decide(k Kind) Status {
	info, ok := Describe(k)
	st := Status{
		Kind:            k,
		About:           info.About,
		Setting:         fmt.Sprintf("sharing.kinds.%s.enabled", k),
		EndpointSetting: fmt.Sprintf("sharing.kinds.%s.endpoint", k),
		State:           StateOff,
	}
	if !ok {
		st.Reason = ReasonUnregistered
		return st
	}
	set := g.cfg.Kinds[k]
	st.Endpoint = set.Endpoint
	switch {
	case g.envOff || g.cfg.Off:
		st.Reason = ReasonOff
	case !set.Enabled:
		st.Reason = ReasonDisabled
	case set.Endpoint == "":
		st.State, st.Reason = StateUnavailable, ReasonNoEndpoint
	default:
		st.State = StateOn
	}
	return st
}

func clone(c Config) Config {
	out := Config{Off: c.Off, Kinds: make(map[Kind]Setting, len(c.Kinds))}
	for k, v := range c.Kinds {
		out.Kinds[k] = v
	}
	return out
}
