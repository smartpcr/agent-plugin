package meter

import (
	"context"
	"errors"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// Key is the tokenMeter service's key (llm.md section 6.2).
const Key = "tokenMeter"

// Config is meter.Config (UP-CFG-dsh-token-meter).
type Config struct {
	// Calibrate scales the heuristic by the session's calibration factor.
	Calibrate bool `json:"calibrate"`
}

// DefaultConfig is the default: calibration on.
func DefaultConfig() Config { return Config{Calibrate: true} }

// ErrMoved is returned when the session kept changing its surface across every
// attempt to read the folds and the history at one cut.
var ErrMoved = errors.New("meter: the session's surface moved while it was measured")

// Meter is the tokenMeter service over the three folds it registers.
type Meter struct {
	reg       *views.Registry
	cfg       Config
	usage     views.Handle[Usage]
	breakdown views.Handle[Breakdown]
	pressure  views.Handle[Pressure]
}

// New registers the three folds in r and returns the service over them.
func New(r *views.Registry, c Config) (*Meter, error) {
	u, err := views.Register(r, UsageView())
	if err != nil {
		return nil, err
	}
	b, err := views.Register(r, BreakdownView())
	if err != nil {
		return nil, err
	}
	p, err := views.Register(r, PressureView())
	if err != nil {
		return nil, err
	}
	return &Meter{reg: r, cfg: c, usage: u, breakdown: b, pressure: p}, nil
}

// Usage is the session's tokenUsage fold.
func (m *Meter) Usage(ctx context.Context, s session.ReadSession) (Usage, error) {
	snap, err := m.reg.Read(ctx, s, UsageKey)
	if err != nil {
		return Usage{}, err
	}
	return m.usage.Get(snap)
}

// Breakdown is the session's contextBreakdown fold.
func (m *Meter) Breakdown(ctx context.Context, s session.ReadSession) (Breakdown, error) {
	snap, err := m.reg.Read(ctx, s, BreakdownKey)
	if err != nil {
		return Breakdown{}, err
	}
	return m.breakdown.Get(snap)
}

// Measurement is the heuristic's price of a session's current surface under a
// request header, and that price calibrated.
type Measurement struct {
	// AsOf is the cut measured: the next seq.
	AsOf event.Seq
	// Estimate is the heuristic's tokens of the surface and the header's tools.
	Estimate int
	// Factor is the calibration factor applied (1 when none applies).
	Factor float64
	// Calibrated reports that Factor came from a provider sample.
	Calibrated bool
	// Tokens is Estimate scaled by Factor.
	Tokens int
}

// Measure prices the current surface of s, read from the session, its one
// owner, under the request header h. The calibration factor is the last
// sample's provider-reported prompt over the heuristic's price of that same
// request envelope: the nodes up to the sample's requestAsOf, under the header
// then in force, priced from the surface while that envelope is still its
// prefix. Once a replacement or a header change has rewritten it, the factor
// is the last such ratio the contextPressure fold took on an intact envelope,
// so the new envelope is scaled by it rather than measured raw (llm.md,
// token-meter.4). Before any sample, or with Calibrate off, the factor is 1.
func (m *Meter) Measure(ctx context.Context, s session.ReadSession, h surface.RequestHeader) (Measurement, error) {
	for range 3 {
		snap, err := m.reg.Read(ctx, s, PressureKey)
		if err != nil {
			return Measurement{}, err
		}
		p, err := m.pressure.Get(snap)
		if err != nil {
			return Measurement{}, err
		}
		hist := s.History()
		cur, sameHeader := s.RequestHeader()
		total, request, moved := 0, 0, false
		for i := range hist.Len() {
			n := hist.At(i)
			if n.Seq() >= snap.AsOf {
				moved = true
				break
			}
			msg, err := n.Message()
			if err != nil {
				return Measurement{}, err
			}
			e := EstimateMessage(msg)
			total += e
			if p.Sample != nil && n.Seq() <= p.Sample.RequestAsOf {
				request += e
			}
		}
		if moved {
			continue
		}
		out := Measurement{AsOf: snap.AsOf, Estimate: total + EstimateHeader(h), Factor: 1}
		switch {
		case !m.cfg.Calibrate:
		case p.Sample != nil && !p.Rewritten && sameHeader && cur.Seq <= p.Sample.RequestAsOf && p.Sample.Prompt > 0:
			// The sample's envelope is still the surface's prefix: price it now.
			if est := request + EstimateHeader(cur); est > 0 {
				out.Factor, out.Calibrated = Calibration(p.Sample.Prompt, est), true
			}
		case p.Factor > 0:
			// Rewritten since: the last ratio taken on an intact envelope.
			out.Factor, out.Calibrated = p.Factor, true
		}
		out.Tokens = Calibrated(out.Estimate, out.Factor)
		return out, nil
	}
	return Measurement{}, ErrMoved
}

// Reading is a session's context pressure: the tokens its next request is
// projected to occupy.
type Reading struct {
	AsOf   event.Seq
	Tokens int
	// Measured reports that the surface was measured rather than projected
	// from the sample: a compaction since the sample made the projection an
	// upper bound.
	Measured bool
}

// Fraction is Tokens over a context window of window tokens; 0 for a window of
// 0 or less.
func (r Reading) Fraction(window int) float64 { return fraction(r.Tokens, window) }

// Pressure is the session's context pressure: the contextPressure fold's
// projection, or, after a compaction since the last sample, the measurement of
// the current surface under the header in force.
func (m *Meter) Pressure(ctx context.Context, s session.ReadSession) (Reading, error) {
	snap, err := m.reg.Read(ctx, s, PressureKey)
	if err != nil {
		return Reading{}, err
	}
	p, err := m.pressure.Get(snap)
	if err != nil {
		return Reading{}, err
	}
	if !p.Compacted {
		return Reading{AsOf: snap.AsOf, Tokens: p.Projected()}, nil
	}
	h, _ := s.RequestHeader()
	got, err := m.Measure(ctx, s, h)
	if err != nil {
		return Reading{}, err
	}
	return Reading{AsOf: got.AsOf, Tokens: got.Tokens, Measured: true}, nil
}

// EstimateMessage is the heuristic's tokens of msg.
func (m *Meter) EstimateMessage(msg vocab.Message) int { return EstimateMessage(msg) }

// EstimateHeader is the heuristic's tokens of a request header's tool schemas:
// each tool's name, description and parameter document, plus a block's framing.
func EstimateHeader(h surface.RequestHeader) int { return EstimateTools(h.Tools) }

// EstimateTools is the heuristic's tokens of tool schemas: each tool's name,
// description and parameter document, plus a block's framing.
func EstimateTools(tools []vocab.ToolSchema) int {
	n := 0
	for _, t := range tools {
		n += BlockTokens + EstimateText(t.Name) + EstimateText(t.Description) + EstimateBytes(len(t.Parameters))
	}
	return n
}
