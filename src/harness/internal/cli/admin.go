package cli

// The admin tree (DH-D-CLI-9, C50, A24): a class admin command runs in the
// composition of the embedded admin profile, mounted through boot's own steps,
// never in a launchable one, so it builds no carrier, agent, model adapter or
// plugin process (INV-10, M2-C-14). The command is the tree's owner: it reaches
// the services the mounted entries provide through adminService. A command
// declared read-only gets a view that writes nothing: the home is not prepared,
// the process provider keeps no owner record and the entries that write into the
// home (readWriters) stay unloaded, and no configured file log or canary
// directory is written (readView).

import (
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/logging"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/diag"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
)

// access is a command's class: not admin, admin read-only, or admin writing.
type access int

const (
	notAdmin access = iota
	adminRead
	adminWrite
)

// TraceEnv, when set in the launch environment, makes an admin command print the
// components of the entries active in its tree as one line on standard error
// when it ends: the evidence that the admin tree acts on nothing (TC-13b).
const TraceEnv = "DSH_COMPONENT_TRACE"

// tracePrefix begins the trace line; "none" follows it when nothing is active.
const tracePrefix = "dsh: trace: components: "

// readWriters are the components whose entries write into the home when they
// load; a read-only view leaves them unloaded.
var readWriters = []string{"session.store", "human.secrets"}

// adminTree is the mounted admin tree an admin command runs against.
type adminTree struct {
	read  bool
	state *boot.State
	trace bool
}

type wallClock struct{}

func (wallClock) Now() time.Time                         { return time.Now() }
func (wallClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// adminDefinition is the admin composition's definition.
func adminDefinition() profile.Definition {
	i := slices.IndexFunc(profile.Definitions(), func(d profile.Definition) bool { return d.Name == profile.Admin })
	return profile.Definitions()[i]
}

// openAdmin mounts the admin profile under --home: the launch facts, the home
// (prepared only for a writing command), the layers, the composition, which it
// refuses if anything in it acts, then the tree, settled. A command that is not
// of class admin has no tree. The caller closes the tree.
func (st *state) openAdmin() (*adminTree, error) {
	a := st.access
	if a == notAdmin {
		return nil, herr.Newf(output.ReasonUnavailable, "%s is not an admin command", st.command)
	}
	if st.env.probeErr != nil {
		return nil, st.env.probeErr
	}
	req := st.env.Request
	req.Args = slices.Clone(req.Args)
	req.Home, req.Profile, req.Words, req.Overlays, req.Safe = st.home, profile.Admin, nil, nil, false
	s := &boot.State{
		Options: boot.Options{Request: req, Profile: adminDefinition(), Stderr: st.env.Stderr},
		App:     launch.NewApp(), Clock: wallClock{}, Stderr: st.env.Stderr, Ring: diag.NewRing(diag.RingSize),
	}
	if s.Stderr == nil {
		s.Stderr = io.Discard
	}
	steps := boot.Real{}
	if err := steps.Facts(st.ctx, s); err != nil {
		return nil, err
	}
	t := &adminTree{read: a == adminRead, state: s}
	if _, _, ok := s.Env.Lookup(TraceEnv); ok {
		t.trace = true
	}
	root := home.New(s.Data.Home)
	if t.read {
		// No home on the tree: the process provider reads and writes no owner
		// record. The sandbox seam still names the home it keeps read-only.
		s.Home = home.Layout{}
	} else {
		s.Home = root
		if err := home.Ensure(root); err != nil {
			return nil, err
		}
	}
	s.Diag = diag.New(diag.Config{Stderr: s.Stderr, Ring: s.Ring, Facts: diag.Facts{Version: s.Data.Version, OS: s.Data.OS, Profile: s.Data.Profile}})
	if err := steps.Layers(st.ctx, s); err != nil {
		return nil, err
	}
	if err := steps.Compose(st.ctx, s); err != nil {
		return nil, err
	}
	if id, name, ok := acting(s.Composed); ok {
		return nil, herr.Newf(output.ReasonUnavailable, "the admin tree holds %s (%s): an admin command builds no carrier, agent, model adapter or plugin process", id, name)
	}
	if t.read {
		if err := readView(s.Composed, root.Root()); err != nil {
			return nil, err
		}
	}
	if err := steps.Tree(st.ctx, s); err != nil {
		t.unload()
		return nil, err
	}
	if err := steps.Settle(st.ctx, s); err != nil {
		t.unload()
		return nil, err
	}
	if s.ApplyErr != nil {
		t.unload()
		return nil, s.ApplyErr
	}
	return t, nil
}

// readView turns composed into the read-only view in place: the entries of
// readWriters are disabled, and the configs of boot.logging and sandbox.local
// are rebuilt from the fields of readFields only, which name no path, with a
// fixed destination standard error and the home. However a configured log path,
// log destination or canary directory is written, as a literal, a ${{ }} string
// or a {"$expr"} object, the field itself or the whole config, it is not in the
// view's config, so no configured file or directory is created or written.
func readView(composed []loader.EntryOptions, root string) error {
	for i := range composed {
		e := &composed[i]
		var err error
		switch {
		case slices.Contains(readWriters, e.Name):
			e.Disabled = true
		case e.Name == logging.Name:
			err = readConfig(e, map[string]any{"destination": logging.Stderr})
		case e.Name == local.Name:
			err = readConfig(e, map[string]any{"home": root})
		}
		if err != nil {
			return err
		}
		if err := readView(e.Children, root); err != nil {
			return err
		}
	}
	return nil
}

// readFields are the config fields a read-only view keeps, per component: none
// of them names a path, so none, whatever its value, makes the entry write.
var readFields = map[string][]string{
	logging.Name: {"level", "format"},
	local.Name:   {"probeTimeout", "launchTimeout", "backends"},
}

// readConfig replaces the config of e with fixed plus the readFields of its
// component taken from the configured object. A config that is an expression
// as a whole keeps nothing of it.
func readConfig(e *loader.EntryOptions, fixed map[string]any) error {
	cfg := map[string]any{}
	if len(e.Config) > 0 {
		if err := json.Unmarshal(e.Config, &cfg); err != nil {
			return err
		}
	}
	if _, whole := cfg["$expr"]; whole {
		cfg = nil
	}
	out := maps.Clone(fixed)
	for _, k := range readFields[e.Name] {
		if v, ok := cfg[k]; ok {
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	e.Config = b
	return nil
}

// acting finds the first entry of entries, nested ones included, that acts.
func acting(entries []loader.EntryOptions) (id, name string, ok bool) {
	for _, e := range entries {
		if profile.Acting(e.ID, e.Name) {
			return e.ID, e.Name, true
		}
		if id, name, ok := acting(e.Children); ok {
			return id, name, true
		}
	}
	return "", "", false
}

// active lists the components of the tree's active entries, in tree order.
func (t *adminTree) active() []string {
	var names []string
	svc := t.state.Service
	if svc == nil {
		return nil
	}
	for _, e := range svc.Entries() {
		if f := svc.Fiber(e); f != nil && f.State() == cordis.Active {
			names = append(names, svc.Options(e).Name)
		}
	}
	return names
}

// adminService is the owner's typed access to a service the tree's entry of
// component provides: an error names the entry that is not active or the
// service it does not provide.
func adminService[T any](t *adminTree, component string, key cordis.ServiceKey[T]) (T, error) {
	var zero T
	svc := t.state.Service
	for _, e := range svc.Entries() {
		if svc.Options(e).Name != component {
			continue
		}
		f := svc.Fiber(e)
		if f == nil || f.State() != cordis.Active {
			if f != nil && f.Err() != nil {
				return zero, herr.Wrap(output.ReasonUnavailable, f.Err(), "the admin tree's "+component+" entry is not active")
			}
			return zero, herr.Newf(output.ReasonUnavailable, "the admin tree's %s entry is not active", component)
		}
		// The owner is a fiber beside the entry that injects the one service, so
		// the access is declared like any component's and ends with the tree.
		var v T
		owner := cordis.Define("cli.admin.owner", func(ctx *cordis.Context, _ cordis.None) error {
			var err error
			v, err = key.Get(ctx)
			return err
		}, cordis.WithInject(key.Name()))
		fiber, err := f.Parent().Use(owner, nil)
		if err == nil {
			err = fiber.Wait()
		}
		if err != nil {
			return zero, herr.Wrap(output.ReasonUnavailable, err, component+" provides no "+key.Name())
		}
		return v, nil
	}
	return zero, herr.Newf(output.ReasonUnavailable, "the admin tree has no %s entry", component)
}

// close prints the component trace when TraceEnv is set, then unloads the tree.
func (t *adminTree) close(stderr io.Writer) {
	if t == nil {
		return
	}
	if t.trace {
		list := "none"
		if names := t.active(); len(names) > 0 {
			list = strings.Join(names, ",")
		}
		_, _ = io.WriteString(stderr, tracePrefix+list+"\n")
	}
	t.unload()
}

func (t *adminTree) unload() {
	if t.state.Host != nil {
		t.state.Host.Unload()
		t.state.Host = nil
	}
}
