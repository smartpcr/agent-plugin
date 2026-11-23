package driver_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// threeStepPersona is the persona prefix the test configures between steps 2
// and 3, which changes the assembly and so rewrites node 0.
const threeStepPersona = "You check every echo before you answer."

// The per-run values the three-step golden cannot hold, as f2Serialize masks
// them (internal/agent/f2_test.go): generated ids and the digests over them,
// numbered in order of first appearance, and an attempt's wall-clock timing.
var (
	threeStepVolatile = regexp.MustCompile(`\b[a-z]+[_-][0-9a-f]{16,}\b|sha256:[0-9a-f]{64}`)
	threeStepTiming   = regexp.MustCompile(`"(start|first_token|end)":\d+`)
)

// threeStepSerialize is the log as golden text: one line per event with its
// seq, type, operation (with its range for a replace), sources and payload, ids
// and digests numbered. The session here runs on the wall clock, so event times
// are left out.
func threeStepSerialize(evs []event.Event) string {
	names := map[string]string{}
	norm := func(s string) string {
		return threeStepVolatile.ReplaceAllStringFunc(s, func(v string) string {
			if n, ok := names[v]; ok {
				return n
			}
			kind := strings.FieldsFunc(v, func(r rune) bool { return r == '_' || r == '-' || r == ':' })[0]
			n := fmt.Sprintf("<%s-%d>", kind, len(names)+1)
			names[v] = n
			return n
		})
	}
	var b strings.Builder
	for _, e := range evs {
		op := e.Op()
		fmt.Fprintf(&b, "%d %s op=%s", e.Seq(), e.Type(), op.Kind)
		if op.Kind == event.OpReplace {
			fmt.Fprintf(&b, "[%d,%d]", op.Start, op.End)
		}
		if src := e.Sources(); len(src) > 0 {
			fmt.Fprintf(&b, " sources=%v", src)
		}
		fmt.Fprintf(&b, " %s\n", threeStepTiming.ReplaceAllString(norm(e.Raw()), `"$1":"<time>"`))
	}
	return b.String()
}

// Plan 17.1 scenario "Three-step turn golden" (17.1.11): a script of one tool
// call, then a parallel pair of calls, then an answer, with the persona edited
// through prompt.Service.Configure between steps 2 and 3, runs on memstore
// under the tools.prompt component. The log equals testdata/three_step.golden;
// it holds exactly one node-0 replace (step 3's system/message replacing step
// 1's, followed by a new header), and turn/end is completed.
//
//dsh:test DH-T-AGENT-7
func TestThreeStepTurnLogMatchesGolden(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	must(0, must(root.Use(prompt.Component, prompt.Defaults())).Wait())
	var ps *prompt.Service
	g := must(root.Inject([]string{prompt.Key.Name()}, func(c *cordis.Context) (err error) {
		ps, err = prompt.Key.Get(c)
		return err
	}))
	must(0, g.Wait())

	hooks := driver.Hooks{ContinueOrStop: func(_, step int) {
		if step == 2 {
			cfg := ps.Config()
			cfg.PersonaPrefix = threeStepPersona
			if err := ps.Configure(cfg); err != nil {
				t.Errorf("configure the persona: %v", err)
			}
		}
	}}
	r := newRig(t, rigOptions{root: root, svc: svc, hooks: hooks, id: "s-three-step",
		options: func(o *driver.Options) { o.Prompt = ps },
		turns: []fakemodel.Turn{
			fakemodel.Calls(echo("c1")),
			fakemodel.Calls(echo("c2"), fakemodel.Tool("c3", "echo", `{"text":"y"}`)),
			fakemodel.Reply("done"),
		}})
	r.send(agent.ListNextTurn, "echo three times")
	r.idle()
	evs := r.events()

	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v, want one completed", got)
	}
	if se := stepEnds(t, evs); len(se) != 3 {
		t.Fatalf("step/end %+v, want 3 steps", se)
	}
	if n := len(ofType(evs, "tool/result")); n != 3 {
		t.Fatalf("%d tool results, want 3", n)
	}
	var replaces []event.Event
	for _, e := range evs {
		if e.Op().Kind == event.OpReplace {
			replaces = append(replaces, e)
		}
	}
	sys := ofType(evs, "system/message")
	if len(replaces) != 1 || len(sys) != 2 || replaces[0].Seq() != sys[1].Seq() ||
		replaces[0].Op().Start != sys[0].Seq() || replaces[0].Op().End != sys[0].Seq() {
		t.Fatalf("replaces %v, system messages %v: want exactly one, step 3's prompt replacing node 0", replaces, sys)
	}
	if got := must(r.sent.requests()[2].Messages())[0]; !strings.Contains(got.Blocks()[0].Text.Text, threeStepPersona) {
		t.Fatalf("step 3's request leads with %q, want the edited persona", got.Blocks()[0].Text.Text)
	}
	checkClosed(t, r.sess)
	golden.Assert(t, "three_step", threeStepSerialize(evs))
}
