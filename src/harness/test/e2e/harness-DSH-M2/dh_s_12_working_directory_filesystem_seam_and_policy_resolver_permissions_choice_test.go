//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/permission"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
)

var pcxBg = context.Background()

var (
	pcxAgent = agent.Ref{Session: "s-permission-e2e", Incarnation: 1}
	pcxAlice = human.Actor{Channel: "ui", Name: "alice", Peer: "p1"}
)

// pcxTable is the TC-02a table: ask (workspace-write, ask) is the default and
// auto (danger-full-access, never) differs from it in both knobs.
func pcxTable() permission.Config {
	return permission.Config{DefaultPreset: "ask", Presets: []permission.Preset{
		{Name: "ask", Mode: sandbox.WorkspaceWrite, Approval: approval.PolicyAsk},
		{Name: "auto", Mode: sandbox.DangerFullAccess, Approval: approval.PolicyNever},
	}}
}

type pcxWorld struct {
	cleanups []func()

	root     *cordis.Context
	sessions session.Service
	resolver *policy.Resolver
	approval *approval.Service
	svc      *permission.Service
	handle   session.Handle
	cmds     *command.Registry
	target   command.Target
	from     int
	cmdOut   string

	cfg     permission.Config
	overlay string
	homeDir string
	status  int
	stderr  string
}

func (w *pcxWorld) cleanup(f func()) { w.cleanups = append(w.cleanups, f) }

func (w *pcxWorld) close() {
	for i := len(w.cleanups) - 1; i >= 0; i-- {
		w.cleanups[i]()
	}
	w.cleanups = nil
}

func pcxOwner(root *cordis.Context, name string) (*cordis.Context, error) {
	var octx *cordis.Context
	f, err := root.Apply(name, func(c *cordis.Context) error { octx = c; return nil })
	if err == nil {
		err = f.Wait()
	}
	return octx, err
}

func (w *pcxWorld) events() ([]string, error) {
	evs, err := w.handle.Read(pcxBg, 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Type())
	}
	return out, nil
}

func (w *pcxWorld) appended() ([]string, error) {
	all, err := w.events()
	if err != nil {
		return nil, err
	}
	return all[w.from:], nil
}

// ---- Scenario: Switch moves mode and approval together ----

func (w *pcxWorld) givenPinnedSession(pinned, a, b string) error {
	cfg := pcxTable()
	for _, n := range []string{pinned, a, b} {
		if !slices.ContainsFunc(cfg.Presets, func(p permission.Preset) bool { return p.Name == n }) {
			return fmt.Errorf("preset %q is not in the TC-02a table", n)
		}
	}
	w.root = cordis.New()
	w.cleanup(w.root.Dispose)
	w.sessions = session.NewService(w.root, memstore.New(), session.Config{})
	octx, err := pcxOwner(w.root, "world.sandbox.policy")
	if err != nil {
		return err
	}
	if w.resolver, err = policy.New(policy.Config{}, policy.Deps{Owner: octx, Sessions: w.sessions}); err != nil {
		return err
	}
	grants := approval.NewGrants()
	w.approval = approval.New(approval.Config{Registry: human.New(), Grants: grants, Sessions: w.sessions, Policy: approval.PolicyAsk})
	if _, err := w.approval.Bind(w.root, w.sessions); err != nil {
		return err
	}
	if w.svc, err = permission.New(cfg, permission.Deps{Sessions: w.sessions, Sandbox: w.resolver, Approval: w.approval, Grants: grants, Views: views.New()}); err != nil {
		return err
	}
	if w.handle, err = w.sessions.Create(pcxBg, nil, session.CreateOptions{Header: session.Header{ID: string(pcxAgent.Session)}}); err != nil {
		return err
	}
	h := w.handle
	w.cleanup(func() { _ = h.Close(pcxBg) })
	if err := w.svc.Pin(pcxBg, pcxAgent); err != nil {
		return fmt.Errorf("pin: %w", err)
	}
	if cur, err := w.svc.Current(pcxBg, pcxAgent.Session); err != nil || cur != pinned {
		return fmt.Errorf("pinned preset = %q, %v; want %q", cur, err, pinned)
	}
	all, err := w.events()
	if err != nil {
		return err
	}
	w.from = len(all)

	tab := scope.NewTable(w.root)
	w.cmds = command.NewRegistry(tab)
	if _, err := w.svc.RegisterCommand(w.root, w.cmds); err != nil {
		return err
	}
	sc, err := tab.New(w.root, "agent-a")
	if err != nil {
		return err
	}
	w.target = command.Target{Agent: pcxAgent, Scope: sc.Key(), Log: h, Owner: pcxAlice.Name}
	return nil
}

func (w *pcxWorld) whenOwnerRuns(line string) error {
	res, err := w.cmds.Execute(pcxBg, w.target, pcxAlice, line)
	if err != nil {
		return fmt.Errorf("%s: %w", line, err)
	}
	if res.Outcome != command.OutcomeOK {
		return fmt.Errorf("%s: outcome %+v", line, res)
	}
	w.cmdOut = fmt.Sprintf("%+v", res)
	return nil
}

func (w *pcxWorld) thenKnobEventsInOrder(modeType, policyType string) error {
	if modeType != sandbox.ModeEventType || policyType != approval.PolicyType.Name() {
		return fmt.Errorf("event names %q, %q are not the knob owners' (%q, %q)", modeType, policyType, sandbox.ModeEventType, approval.PolicyType.Name())
	}
	got, err := w.appended()
	if err != nil {
		return err
	}
	var knobs []string
	for _, t := range got {
		if t == modeType || t == policyType {
			knobs = append(knobs, t)
		}
	}
	if !slices.Equal(knobs, []string{modeType, policyType}) {
		return fmt.Errorf("appended %v; want exactly one %s then one %s", got, modeType, policyType)
	}
	return nil
}

func (w *pcxWorld) thenOnePresetEvent(presetType, name string) error {
	if presetType != permission.PresetType.Name() {
		return fmt.Errorf("event name %q is not %q", presetType, permission.PresetType.Name())
	}
	evs, err := w.handle.Read(pcxBg, 0, 0)
	if err != nil {
		return err
	}
	var sel []permission.PresetSet
	for _, e := range evs[w.from:] {
		if e.Type() == presetType {
			p, err := permission.PresetType.As(e)
			if err != nil {
				return err
			}
			sel = append(sel, p)
		}
	}
	if len(sel) != 1 || sel[0].Preset != name || sel[0].Pinned || sel[0].By.Principal != pcxAlice.Name {
		return fmt.Errorf("appended selections %+v; want one unpinned %q by %s", sel, name, pcxAlice.Name)
	}
	return nil
}

func (w *pcxWorld) thenViewsReflect(name string) error {
	want, ok := w.svc.Lookup(name)
	if !ok {
		return fmt.Errorf("no preset %q", name)
	}
	reg := views.New()
	modes, err := policy.RegisterView(reg)
	if err != nil {
		return err
	}
	policies, err := approval.RegisterView(reg)
	if err != nil {
		return err
	}
	perms, err := permission.RegisterView(reg)
	if err != nil {
		return err
	}
	read := func(key string) (views.Snapshot, error) { return reg.Read(pcxBg, w.handle, key) }

	snap, err := read(policy.ModeKey)
	if err != nil {
		return err
	}
	if st, err := modes.Get(snap); err != nil || st.Mode != want.Mode || st.By.Principal != pcxAlice.Name {
		return fmt.Errorf("sandbox-mode view %+v, %v; want %s by %s", st, err, want.Mode, pcxAlice.Name)
	}
	if snap, err = read(approval.PolicyKey); err != nil {
		return err
	}
	if st, err := policies.Get(snap); err != nil || st.Policy != want.Approval || st.By.Principal != pcxAlice.Name {
		return fmt.Errorf("approval view %+v, %v; want %s by %s", st, err, want.Approval, pcxAlice.Name)
	}
	if snap, err = read(permission.ViewKey); err != nil {
		return err
	}
	if st, err := perms.Get(snap); err != nil || st.Preset != name || st.Mode != want.Mode || st.Approval != want.Approval || !st.Pinned {
		return fmt.Errorf("permissions view %+v, %v; want %s (%s, %s), pinned", st, err, name, want.Mode, want.Approval)
	}
	return nil
}

func (w *pcxWorld) thenKnobsInForce(name string) error {
	want, _ := w.svc.Lookup(name)
	m, err := w.resolver.Mode(pcxBg, pcxAgent.Session)
	if err != nil || m != want.Mode {
		return fmt.Errorf("mode in force %s, %v; want %s", m, err, want.Mode)
	}
	if p := w.approval.Policy(pcxAgent.Session); p != want.Approval {
		return fmt.Errorf("policy in force %s; want %s", p, want.Approval)
	}
	if cur, err := w.svc.Current(pcxBg, pcxAgent.Session); err != nil || cur != name {
		return fmt.Errorf("current preset %q, %v; want %q", cur, err, name)
	}
	return nil
}

// ---- Scenario: Unresolvable default refused ----

func (w *pcxWorld) givenBadDefaultLayer(name string) error {
	base, err := os.MkdirTemp("", "pcx-boot-")
	if err != nil {
		return err
	}
	w.cleanup(func() { _ = os.RemoveAll(base) })
	w.homeDir = filepath.Join(base, "home")
	w.overlay = filepath.Join(base, "overlay.yml")
	layer := fmt.Sprintf("- insert:\n    - id: permissions\n      name: permission.presets\n      config:\n        defaultPreset: %s\n", name)
	if err := os.WriteFile(w.overlay, []byte(layer), 0o600); err != nil {
		return err
	}
	w.cfg = permission.DefaultConfig()
	w.cfg.DefaultPreset = name
	return nil
}

func (w *pcxWorld) whenConfigLoads() error {
	cat, err := builtin.Catalog()
	if err != nil {
		return err
	}
	def := profile.Definition{Name: "probe", Bundles: []string{profile.Admin, profile.Core}}
	var stderr testkit.Buffer
	w.status = boot.Run(pcxBg, boot.Options{
		Request: launch.Request{Args: []string{"dsh", "probe"}, Cwd: filepath.Dir(w.homeDir), Home: w.homeDir, Profile: def.Name, Overlays: []string{w.overlay}},
		Profile: def,
		Catalog: cat,
		Stderr:  &stderr,
	})
	w.stderr = stderr.String()
	return nil
}

func (w *pcxWorld) thenConfigStatus() error {
	if w.status != output.Config {
		return fmt.Errorf("status = %d, want %d (config); stderr:\n%s", w.status, output.Config, w.stderr)
	}
	if n := strings.Count(w.stderr, "dsh: fatal:"); n != 1 {
		return fmt.Errorf("%d fatal messages, want 1:\n%s", n, w.stderr)
	}
	return nil
}

func (w *pcxWorld) thenMessageNames(name, field string) error {
	want := fmt.Sprintf("default permission preset %q is not a preset of the table", name)
	if !strings.Contains(w.stderr, field) || !strings.Contains(w.stderr, want) {
		return fmt.Errorf("the message does not name %q at %s:\n%s", name, field, w.stderr)
	}
	return nil
}

func (w *pcxWorld) thenValidateRefuses(name string) error {
	err := w.cfg.Validate()
	var ce *cordis.ConfigError
	if !errors.As(err, &ce) {
		return fmt.Errorf("Validate = %v, want a *cordis.ConfigError", err)
	}
	if !slices.ContainsFunc(ce.Issues, func(i cordis.ConfigIssue) bool {
		return i.Path == "defaultPreset" && strings.Contains(i.Message, fmt.Sprintf("%q", name))
	}) {
		return fmt.Errorf("issues %+v do not name %q at defaultPreset", ce.Issues, name)
	}
	if _, err := permission.New(w.cfg, permission.Deps{}); !errors.As(err, &ce) {
		return fmt.Errorf("permission.New over the config = %v, want a configuration error", err)
	}
	return nil
}

func InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_permissions_choice(ctx *godog.ScenarioContext) {
	w := &pcxWorld{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.close()
		*w = pcxWorld{}
		return c, err
	})
	ctx.Step(`^an in-memory live session pinned to preset "([^"]*)" of a table holding "([^"]*)" and "([^"]*)"$`, w.givenPinnedSession)
	ctx.Step(`^the owner runs "([^"]*)"$`, w.whenOwnerRuns)
	ctx.Step(`^exactly one "([^"]*)" and one "([^"]*)" event are appended in that order$`, w.thenKnobEventsInOrder)
	ctx.Step(`^exactly one "([^"]*)" event naming "([^"]*)" is appended$`, w.thenOnePresetEvent)
	ctx.Step(`^the sandbox-mode, approval and permissions views all reflect "([^"]*)"$`, w.thenViewsReflect)
	ctx.Step(`^the knobs in force and the current preset are "([^"]*)"$`, w.thenKnobsInForce)

	ctx.Step(`^a config layer whose permission\.presets default preset is "([^"]*)"$`, w.givenBadDefaultLayer)
	ctx.Step(`^the configuration loads through boot$`, w.whenConfigLoads)
	ctx.Step(`^the load fails with the configuration status and exactly one fatal message$`, w.thenConfigStatus)
	ctx.Step(`^the message names the preset "([^"]*)" at "([^"]*)"$`, w.thenMessageNames)
	ctx.Step(`^validating the same config directly is a configuration error naming "([^"]*)"$`, w.thenValidateRefuses)
}

func TestE2E_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_permissions_choice(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_permissions_choice,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_12_working_directory_filesystem_seam_and_policy_resolver_permissions_choice.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("permissions choice e2e scenarios failed")
	}
}
