package boot

import (
	"fmt"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
)

// EgressKinds are the kinds of outbound traffic a host may be permitted to send,
// in the order the audit lists them: the sharing gate's table (DH-D-HOST-4). Every
// kind is off until configured.
var EgressKinds = func() []string {
	kinds := sharing.Kinds()
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return out
}()

// Permit is one outbound permit decision: a kind of traffic, where it may go, and
// its state as the gate sees it.
type Permit struct {
	Kind        string `json:"kind"`
	Destination string `json:"destination,omitempty"`
	Allowed     bool   `json:"allowed"`
	// State is off, on or unavailable (sharing.State).
	State string `json:"state"`
	// Reason says why the kind is not on: sharing/off (stopped by the switch),
	// sharing/disabled or sharing/no-endpoint.
	Reason string `json:"reason,omitempty"`
}

// Permits returns one permit per kind of EgressKinds: allowed with its destination
// when configured names the kind, denied otherwise. Nil configures none.
func Permits(configured map[string]string) []Permit {
	cfg := sharing.Config{Kinds: map[sharing.Kind]sharing.Setting{}}
	for kind, dest := range configured {
		cfg.Kinds[sharing.Kind(kind)] = sharing.Setting{Enabled: true, Endpoint: dest}
	}
	return PermitsFrom(sharing.New(cfg, nil))
}

// PermitsFrom returns the gate's decision for every kind of EgressKinds.
func PermitsFrom(g *sharing.Gate) []Permit {
	senders := g.Senders()
	out := make([]Permit, len(senders))
	for i, st := range senders {
		out[i] = Permit{
			Kind:        string(st.Kind),
			Destination: st.Endpoint,
			Allowed:     st.State == sharing.StateOn,
			State:       string(st.State),
			Reason:      string(st.Reason),
		}
	}
	return out
}

// EntryState is the audit's view of one entry that is not active.
type EntryState struct {
	ID        string `json:"id"`
	Component string `json:"component,omitempty"`
	// Position is file:line:column of the entry in its layer, "" when unknown.
	Position string `json:"position,omitempty"`
	// State is absent, disabled, pending, loading, failed or not loaded.
	State    string `json:"state"`
	Cause    string `json:"cause,omitempty"`
	Required bool   `json:"required,omitempty"`
}

func (e EntryState) String() string {
	var b strings.Builder
	b.WriteString(e.ID)
	if e.Component != "" {
		b.WriteString(" (" + e.Component + ")")
	}
	if e.Position != "" {
		b.WriteString(" at " + e.Position)
	}
	b.WriteString(": " + e.State)
	if e.Cause != "" {
		b.WriteString(": " + strings.ReplaceAll(e.Cause, "\n", " "))
	}
	return b.String()
}

// Audit is the boot audit: the outbound permits, and the entries that are not
// active, required ones marked.
type Audit struct {
	Permits []Permit `json:"permits"`
	// Off is set when the sharing off-switch denies every kind; OffLocked when the
	// environment (DSH_SHARING_OFF) set it.
	Off       bool         `json:"off,omitempty"`
	OffLocked bool         `json:"offLocked,omitempty"`
	Inactive  []EntryState `json:"inactive,omitempty"`
}

// FromGate fills a's permits and off-switch from the gate.
func (a *Audit) FromGate(g *sharing.Gate) {
	a.Permits = PermitsFrom(g)
	a.Off, a.OffLocked = g.Off()
}

// KindsLine is the audit line listing every outbound kind with its state, so each
// kind's setting is visible at every launch.
func (a Audit) KindsLine() string {
	parts := make([]string, len(a.Permits))
	for i, p := range a.Permits {
		state := p.State
		if state == "" {
			state = string(sharing.StateOff)
			if p.Allowed {
				state = string(sharing.StateOn)
			}
		}
		parts[i] = p.Kind + "=" + state
	}
	head := "dsh: audit: outbound kinds"
	switch {
	case a.OffLocked:
		head += " (off-switch set by " + sharing.EnvOff + ")"
	case a.Off:
		head += " (off-switch set)"
	}
	return head + ": " + strings.Join(parts, ", ")
}

// Allowed returns the permits that are allowed.
func (a Audit) Allowed() []Permit {
	var out []Permit
	for _, p := range a.Permits {
		if p.Allowed {
			out = append(out, p)
		}
	}
	return out
}

// PermitLine is the audit line listing every outbound permit.
func (a Audit) PermitLine() string {
	allowed := a.Allowed()
	if len(allowed) == 0 {
		return "dsh: audit: outbound permits: none"
	}
	parts := make([]string, len(allowed))
	for i, p := range allowed {
		parts[i] = p.Kind
		if p.Destination != "" {
			parts[i] += " -> " + p.Destination
		}
	}
	return fmt.Sprintf("dsh: audit: outbound permits: %d: %s", len(allowed), strings.Join(parts, ", "))
}

// WarningLine is the one warning for the entries that are not active, "" for none.
func (a Audit) WarningLine() string {
	if len(a.Inactive) == 0 {
		return ""
	}
	parts := make([]string, len(a.Inactive))
	for i, e := range a.Inactive {
		parts[i] = e.String()
	}
	return fmt.Sprintf("dsh: warning: %d entries are not active: %s", len(a.Inactive), strings.Join(parts, "; "))
}

// RequiredError is the start-up report of a launch whose required entries did not
// all load: the failed ones, the ones waiting for a service, and the others.
type RequiredError struct {
	Required []EntryState
	Others   []EntryState
}

func (e *RequiredError) Reason() string { return string(ReasonRequired) }

func (e *RequiredError) Error() string {
	ids := make([]string, len(e.Required))
	for i, r := range e.Required {
		ids[i] = r.ID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "required entry not active: %s", strings.Join(ids, ", "))
	group := func(title string, pick func(EntryState) bool) {
		var lines []string
		for _, list := range [][]EntryState{e.Required, e.Others} {
			for _, s := range list {
				if pick(s) {
					mark := ""
					if s.Required {
						mark = " [required]"
					}
					lines = append(lines, "  "+s.String()+mark)
				}
			}
		}
		if len(lines) > 0 {
			b.WriteString("\n" + title + ":\n" + strings.Join(lines, "\n"))
		}
	}
	group("failed", func(s EntryState) bool { return s.State == "failed" })
	group("waiting for a service", func(s EntryState) bool { return s.State == "pending" })
	group("not active", func(s EntryState) bool { return s.State != "failed" && s.State != "pending" })
	return b.String()
}

var _ interface{ Reason() string } = (*RequiredError)(nil)

// audit checks svc against the required ids: each must be present, enabled and
// active. It returns the inactive entries, and a *RequiredError when a required one
// is among them.
//
// An enabled entry of a policy component (one of policies, by component name) is
// required wherever it sits: a launch whose outbound policy did not initialize
// stops rather than run with it failed. A disabled or absent one is not, as
// nothing can then leave the machine.
func audit(svc *loader.Service, required []string, policies ...string) ([]EntryState, error) {
	var inactive []EntryState
	seen := map[string]bool{}
	for _, entry := range svc.Entries() {
		seen[entry.ID()] = true
		if st, ok := entryState(svc, entry); !ok {
			st.Required = slices.Contains(required, entry.ID()) ||
				(st.State != "disabled" && slices.Contains(policies, st.Component))
			inactive = append(inactive, st)
		}
	}
	for _, id := range required {
		if !seen[id] {
			inactive = append(inactive, EntryState{ID: id, State: "absent", Required: true})
		}
	}
	var req, others []EntryState
	for _, st := range inactive {
		if st.Required {
			req = append(req, st)
		} else {
			others = append(others, st)
		}
	}
	if len(req) > 0 {
		return inactive, &RequiredError{Required: req, Others: others}
	}
	return inactive, nil
}

// entryState describes entry; ok is true when it is active or is a group.
func entryState(svc *loader.Service, entry *loader.Entry) (EntryState, bool) {
	opts := svc.Options(entry)
	st := EntryState{ID: entry.ID(), Component: opts.Name}
	if pos := opts.Position(); pos.IsValid() {
		st.Position = pos.String()
	}
	if opts.Group && !opts.Disabled {
		return st, true
	}
	fiber := svc.Fiber(entry)
	switch {
	case opts.Disabled:
		st.State = "disabled"
	case fiber == nil:
		st.State = "not loaded"
	default:
		switch fiber.State() {
		case cordis.Active:
			return st, true
		case cordis.Pending:
			st.State = "pending"
		case cordis.Loading:
			st.State = "loading"
		case cordis.Failed:
			st.State = "failed"
		default:
			st.State = "not loaded"
		}
		if err := fiber.Err(); err != nil {
			st.Cause = err.Error()
		}
	}
	return st, false
}
