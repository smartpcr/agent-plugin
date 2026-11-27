package cli

// The admin tree (DH-D-CLI-9, C50, A24): a class admin command runs in the
// composition of the embedded admin profile, never in a launchable one, so it
// builds no carrier, agent, model adapter or plugin process (INV-10, M2-C-14). A
// command declared read-only gets a view that writes nothing: the home is not
// prepared and nothing is created in it.

import (
	"io"
	"os"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// access is a command's class: not admin, admin read-only, or admin writing.
type access int

const (
	notAdmin access = iota
	adminRead
	adminWrite
)

// TraceEnv, when set in the launch environment, makes an admin command print the
// components it built as one line on standard error when it ends: the evidence
// that the admin tree acts on nothing (TC-13b).
const TraceEnv = "DSH_COMPONENT_TRACE"

// tracePrefix begins the trace line; "none" follows it when nothing was built.
const tracePrefix = "dsh: trace: components: "

// adminTree is what an admin command runs against: the launch facts and
// environment of the admin profile, its home and the components it built.
type adminTree struct {
	read  bool
	data  launch.Data
	env   *launch.Snapshot
	home  home.Layout
	built []string
}

// adminDefinition is the admin composition's definition.
func adminDefinition() profile.Definition {
	i := slices.IndexFunc(profile.Definitions(), func(d profile.Definition) bool { return d.Name == profile.Admin })
	return profile.Definitions()[i]
}

// openAdmin resolves the launch facts of the admin profile under --home, composes
// the admin tree and refuses it if it holds anything that acts. The running
// command's class decides the view: a writing command gets a prepared home, a
// read-only one reads only. A command that is not of class admin has no tree.
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
	data, env, err := launch.Build(req)
	if err != nil {
		return nil, err
	}
	def := adminDefinition()
	read := profile.ReadFile(os.ReadFile)
	if req.ReadFile != nil {
		read = profile.ReadFile(req.ReadFile)
	}
	layers, _, err := profile.Stack(def, profile.Sources{Home: data.Home, Read: read})
	if err != nil {
		return nil, err
	}
	entries, _, err := profile.Compose(def, layers)
	if err != nil {
		return nil, err
	}
	if id, name, ok := acting(entries); ok {
		return nil, herr.Newf(output.ReasonUnavailable, "the admin tree holds %s (%s): an admin command builds no carrier, agent, model adapter or plugin process", id, name)
	}
	t := &adminTree{read: a == adminRead, data: data, env: env, home: home.New(data.Home)}
	if !t.read {
		if err := home.Ensure(t.home); err != nil {
			return nil, err
		}
	}
	return t, nil
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

// build records that the command built the component name.
func (t *adminTree) build(name string) { t.built = append(t.built, name) }

// close prints the component trace when TraceEnv is set.
func (t *adminTree) close(stderr io.Writer) {
	if t == nil || t.env == nil {
		return
	}
	if _, _, ok := t.env.Lookup(TraceEnv); !ok {
		return
	}
	list := "none"
	if len(t.built) > 0 {
		list = strings.Join(t.built, ",")
	}
	_, _ = io.WriteString(stderr, tracePrefix+list+"\n")
}
