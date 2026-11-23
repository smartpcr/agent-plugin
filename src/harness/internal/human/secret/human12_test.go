package secret_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/redact"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

func TestMain(m *testing.M) { subprocesstest.Main(m) }

// reference is one named secret of the test store and the dummy value its
// variable holds.
type reference struct {
	ref   secret.Ref
	value string
}

// threeReferences are the store of DH-T-HUMAN-12: two names shaped like
// credentials and secret://github/pat, whose GITHUB_PAT no deny pattern and no
// credential suffix catches. The pat holds characters JSON escapes, so a value
// rendered as JSON is masked in that form too.
var threeReferences = []reference{
	{secret.MustParse("secret://deepseek/api-key"), "sk-h12-dummy-never-leaks"},
	{secret.MustParse("secret://acme/service-token"), "tok-h12-dummy-never-leaks"},
	{secret.MustParse("secret://github/pat"), "ghp_h12<dummy>&never-leaks"},
}

// storeOfThree sets each reference's variable in this process's environment,
// names the three in a store reading the launch environment, and resolves each
// once, so the store has released all three values.
func storeOfThree(t *testing.T) *secret.Store {
	t.Helper()
	store := secret.NewStore(secret.EnvSource(os.LookupEnv))
	for _, r := range threeReferences {
		t.Setenv(r.ref.EnvName(), r.value)
		store.Name(r.ref)
	}
	for _, r := range threeReferences {
		v, origin, err := store.Resolve(context.Background(), r.ref)
		if err != nil || v.Reveal() != r.value || origin != "env" {
			t.Fatalf("Resolve(%s) = %v from %q: %v", r.ref, v, origin, err)
		}
	}
	if secret.SecretShaped(threeReferences[2].ref.EnvName()) {
		t.Fatalf("%s is credential-shaped; the test needs one name only the store knows", threeReferences[2].ref.EnvName())
	}
	return store
}

// DH-T-HUMAN-12 (SP-11), the child and log halves. A store holds three
// references, one of them (secret://github/pat) not credential-shaped. A child
// started from the host environment through BuildEnv with the store prints its
// whole environment, which holds none of the three names or values while a plain
// variable still arrives. A test tool that returns the resolved values, as text
// and as JSON, reaches the session log through the human.secret.redact shaper as
// "[redacted]", and no event of the log holds a value.
//
// The other clauses of DH-T-HUMAN-12 wait for gap-closure-plan step 19.5.4: the
// process environment unchanged after Set, no .env entry in the child, and the
// lease's signal when a reference is unset (no store write, .env source or lease
// exists yet).
//
//dsh:test DH-T-HUMAN-12
func TestNamedSecretsReachNoChildAndNoLog(t *testing.T) {
	store := storeOfThree(t)
	t.Setenv("HUMAN12_PLAIN", "kept")

	t.Run("child environment", func(t *testing.T) {
		spec := subprocesstest.Spec(t, subprocesstest.RoleEnviron)
		spec.Env = subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{Secrets: store, Entries: spec.Env})
		out := strings.ReplaceAll(subprocesstest.Output(t, spec), "\r\n", "\n")
		if !strings.Contains(out, "\nHUMAN12_PLAIN=kept\n") && !strings.HasPrefix(out, "HUMAN12_PLAIN=kept\n") {
			t.Fatalf("the child lacks the plain variable, so it did not print the built environment:\n%s", out)
		}
		upper := strings.ToUpper(out)
		for _, r := range threeReferences {
			name := r.ref.EnvName()
			if strings.HasPrefix(upper, name+"=") || strings.Contains(upper, "\n"+name+"=") {
				t.Errorf("the child inherited %s", name)
			}
			if strings.Contains(out, r.value) {
				t.Errorf("the child's environment holds the value of %s", r.ref)
			}
		}
	})

	t.Run("tool result", func(t *testing.T) {
		h, res := runRevealTool(t, store)
		if res.IsError || res.Text != revealedText() {
			t.Fatalf("result %+v; want success with the masked text %q", res, revealedText())
		}
		evs, err := h.Read(context.Background(), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		results := 0
		for _, e := range evs {
			raw := e.Raw() + e.ColdRaw()
			// Every value ends in this marker, which no encoding escapes, so a value
			// in any nesting of JSON strings is found.
			if strings.Contains(raw, "never-leaks") {
				t.Errorf("event %d (%s) holds a secret value: %s", e.Seq(), e.Type(), raw)
			}
			for _, r := range threeReferences {
				if strings.Contains(raw, r.value) || strings.Contains(raw, jsonInner(r.value)) {
					t.Errorf("event %d (%s) holds the value of %s: %s", e.Seq(), e.Type(), r.ref, raw)
				}
			}
			if e.Type() == "tool/result" {
				results++
				if strings.Count(raw, secret.Redacted) != 2*len(threeReferences) {
					t.Errorf("tool/result %s; want each value as %s, in the text and in the JSON", raw, secret.Redacted)
				}
			}
		}
		if results != 1 {
			t.Fatalf("%d tool/result events; want one", results)
		}
	})
}

// revealIn is the reveal tool's input: nothing.
type revealIn struct{}

// revealOut is the reveal tool's value: each resolved value as plain text and as
// a JSON field, so the rendered result holds both forms.
type revealOut struct {
	Text   string   `json:"text"`
	Values []string `json:"values"`
}

// revealedText is the reveal tool's rendered result once masked.
func revealedText() string {
	var plain, values []string
	for range threeReferences {
		plain = append(plain, secret.Redacted)
		values = append(values, secret.Redacted)
	}
	b, err := json.Marshal(revealOut{Text: strings.Join(plain, " "), Values: values})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// jsonInner is v as JSON writes it inside a string.
func jsonInner(v string) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b[1 : len(b)-1])
}

// runRevealTool runs one call of a tool that resolves and returns the three
// values through a tool registry with human.secret.redact loaded, on a memstore
// session, and returns the session and the call's result.
func runRevealTool(t *testing.T, store *secret.Store) (session.Handle, tools.Result) {
	t.Helper()
	ctx := context.Background()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	if _, err := secret.Key.Provide(root, store); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*cordis.Component{scope.Component, core.Component, redact.Component} {
		f, err := root.Use(c, cordis.None{})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Wait(); err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
	}
	var svc tools.Service
	g, err := root.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		svc, err = tools.Key.Get(c)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	reveal := tools.Define(tools.Spec[revealIn, revealOut]{
		Name: "reveal", Description: "Returns the resolved test secrets.",
		Run: func(ctx context.Context, _ revealIn, _ tools.Run) (revealOut, error) {
			var out revealOut
			var plain []string
			for _, r := range threeReferences {
				v, _, err := store.Resolve(ctx, r.ref)
				if err != nil {
					return out, err
				}
				plain = append(plain, v.Reveal())
				out.Values = append(out.Values, v.Reveal())
			}
			out.Text = strings.Join(plain, " ")
			return out, nil
		},
	})
	if _, err := svc.Register(root, reveal); err != nil {
		t.Fatal(err)
	}

	id := session.ID("s-human-12")
	h, err := session.NewService(root, memstore.New(), session.Config{}).Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
	if err != nil {
		t.Fatal(err)
	}
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("reveal"))
	if err != nil {
		t.Fatal(err)
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock("c1", "reveal", json.RawMessage(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
	} {
		d, err := mk()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Append(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	view, err := svc.Resolve(ctx, tools.Target{Agent: agent.Ref{Session: id, Incarnation: 1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.Release)
	batch, err := svc.ExecuteBatch(ctx, tools.Batch{View: view, Log: h, Calls: []tools.CallRequest{{ID: "c1", Name: "reveal", Arguments: `{}`}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Results) != 1 {
		t.Fatalf("%d results for one call", len(batch.Results))
	}
	return h, batch.Results[0]
}

// A released value is masked in every form a text carries it: as it is, Go
// quoted, and JSON encoded with and without HTML escaping; a named reference's
// variable is a secret name in any case, and a nil store still knows the
// credential shapes.
//
//dsh:test DH-T-HUMAN-12
func TestMaskAndSecretNamesFailClosed(t *testing.T) {
	store := storeOfThree(t)
	pat := threeReferences[2].value
	var plainJSON strings.Builder
	enc := json.NewEncoder(&plainJSON)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]string{"v": pat + "\t"}); err != nil {
		t.Fatal(err)
	}
	htmlJSON, err := json.Marshal(map[string]string{"v": pat})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{pat, string(htmlJSON), plainJSON.String(), strconv.Quote(pat + "\n")} {
		if masked := store.MaskString(text); strings.Contains(masked, "never-leaks") || !strings.Contains(masked, secret.Redacted) {
			t.Errorf("MaskString(%q) = %q", text, masked)
		}
	}
	for _, name := range []string{"GITHUB_PAT", "github_pat", "Github_Pat", "DEEPSEEK_API_KEY", "ACME_SERVICE_TOKEN"} {
		if !store.IsSecretName(name) {
			t.Errorf("IsSecretName(%s) = false", name)
		}
	}
	var none *secret.Store
	if none.IsSecretName("GITHUB_PAT") || !none.IsSecretName("SOME_API_KEY") {
		t.Error("a nil store must check the shape only")
	}
}
