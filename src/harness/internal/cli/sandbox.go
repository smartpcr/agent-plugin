package cli

// dsh sandbox status (C37, DH-D-SANDBOX-3): the confinement a workspace-write
// command gets on this machine now (Full, Partial or Unconfined), the backend,
// the promised effects it misses and the exposures, then each backend's probe.

import (
	"fmt"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/cmdline"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
)

// sandboxStatusDoc is the JSON form of `dsh sandbox status --json`; its fields
// are part of the contract.
type sandboxStatusDoc struct {
	State    sandbox.State       `json:"state"`
	Backend  sandbox.Backend     `json:"backend"`
	Missing  []sandbox.Effect    `json:"missing"`
	Exposed  []sandbox.Effect    `json:"exposed"`
	Backends []sandboxBackendDoc `json:"backends"`
}

type sandboxBackendDoc struct {
	Backend   sandbox.Backend  `json:"backend"`
	Custom    bool             `json:"custom,omitempty"`
	Available bool             `json:"available"`
	Reason    string           `json:"reason,omitempty"`
	Canary    string           `json:"canary,omitempty"`
	Governed  []sandbox.Effect `json:"governed"`
	Exposed   []sandbox.Effect `json:"exposed"`
}

func runSandboxStatus(st *state, _ *cmdline.Values) error {
	t, err := st.openAdmin()
	if err != nil {
		return err
	}
	defer t.close(st.env.Stderr)
	svc, err := adminService(t, local.Name, sandbox.ServiceKey)
	if err != nil {
		return err
	}
	statuses := svc.Describe(st.ctx)
	c := sandbox.Expect(statuses)
	doc := sandboxStatusDoc{State: c.State, Backend: c.Backend, Missing: orEmpty(c.Missing), Exposed: orEmpty(c.Exposed),
		Backends: []sandboxBackendDoc{}}
	for _, s := range statuses {
		doc.Backends = append(doc.Backends, sandboxBackendDoc{Backend: s.Backend, Custom: s.Custom, Available: s.Available,
			Reason: s.Reason, Canary: s.Canary, Governed: orEmpty(s.Governed), Exposed: orEmpty(s.Exposed)})
	}
	if st.res.JSON() {
		return st.res.Value(doc)
	}
	return st.res.Text(sandboxReport(doc))
}

// sandboxReport is the text form: the state first, as Full, Partial or
// Unconfined, then one line per backend of the chain.
func sandboxReport(d sandboxStatusDoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "state:   %s\n", titleState(d.State))
	fmt.Fprintf(&b, "backend: %s\n", d.Backend)
	fmt.Fprintf(&b, "missing: %s\n", effectList(d.Missing))
	fmt.Fprintf(&b, "exposed: %s\n", effectList(d.Exposed))
	b.WriteString("backends:\n")
	if len(d.Backends) == 0 {
		b.WriteString("  none on this platform: commands run unconfined, under danger-full-access only\n")
	}
	for _, s := range d.Backends {
		if s.Available {
			fmt.Fprintf(&b, "  %s: available, canary %s, governs %s\n", s.Backend, s.Canary, effectList(s.Governed))
			continue
		}
		fmt.Fprintf(&b, "  %s: unavailable: %s\n", s.Backend, s.Reason)
	}
	return b.String()
}

// titleState is the report's name of s: Full, Partial or Unconfined.
func titleState(s sandbox.State) string {
	if s == "" {
		return "Unknown"
	}
	return strings.ToUpper(string(s[:1])) + string(s[1:])
}

func effectList(es []sandbox.Effect) string {
	if len(es) == 0 {
		return "none"
	}
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = string(e)
	}
	return strings.Join(out, ", ")
}

func orEmpty(es []sandbox.Effect) []sandbox.Effect {
	if es == nil {
		return []sandbox.Effect{}
	}
	return es
}
