package agentpreset_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
)

func issues(t *testing.T, cfg agentpreset.Config, decls ...agentpreset.Declaration) string {
	t.Helper()
	is := agentpreset.Validate(cfg, decls, newFixture(t).cat)
	s := make([]string, len(is))
	for i, x := range is {
		s[i] = x.String()
	}
	return strings.Join(s, "\n")
}

// Extends: the parent must exist, ids are unique, children and the default are
// checked against the catalogue.
func TestValidateIssues(t *testing.T) {
	cfg := agentpreset.Config{Default: "a"}
	for name, c := range map[string]struct {
		cfg   agentpreset.Config
		decls []agentpreset.Declaration
		want  string
	}{
		"unknown parent": {cfg, []agentpreset.Declaration{{ID: "a", Extends: "ghost"}}, "ghost"},
		"duplicate":      {cfg, []agentpreset.Declaration{{ID: "a"}, {ID: "a"}}, "duplicate"},
		"unknown child": {cfg, []agentpreset.Declaration{{ID: "a", Children: map[string]*agentpreset.Child{
			"tool.nope": {}}}}, "tool.nope"},
		"bad config": {cfg, []agentpreset.Declaration{{ID: "a", Children: map[string]*agentpreset.Child{
			"tool.read": {Config: json.RawMessage(`{"rot":"x"}`)}}}}, "rot"},
		"unknown default": {agentpreset.Config{Default: "zz"}, []agentpreset.Declaration{{ID: "a"}}, "zz"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := issues(t, c.cfg, c.decls...); !strings.Contains(got, c.want) {
				t.Fatalf("issues %q do not mention %q", got, c.want)
			}
		})
	}
	if got := issues(t, cfg, agentpreset.Declaration{ID: "a"}); got != "" {
		t.Fatalf("a valid set has issues: %s", got)
	}
}

// A child inherited through Extends is replaced by the child's own entry of the
// same key and removed by a null; the base's revision is not affected.
func TestExtendsMerge(t *testing.T) {
	e := newEnv(t, memstore.New())
	base := agentpreset.Declaration{ID: "base", Children: map[string]*agentpreset.Child{
		"tool.read":       {Config: json.RawMessage(`{"root":"base"}`)},
		"tool.note@extra": {Config: json.RawMessage(`{"root":"extra"}`)},
	}}
	kid := agentpreset.Declaration{ID: "kid", Extends: "base", Children: map[string]*agentpreset.Child{
		"tool.read":       {Config: json.RawMessage(`{"root":"kid"}`)},
		"tool.note@extra": nil,
	}}
	if err := e.reg.Load(agentpreset.Config{Default: "base"}, []agentpreset.Declaration{base, kid}); err != nil {
		t.Fatal(err)
	}
	if n := e.fx.live.Load(); n != 3 {
		t.Fatalf("%d tool.read instances live, want base's two and kid's one", n)
	}
	_, sc, tg := e.agent(t, "s1")
	if _, err := e.reg.Bind(t.Context(), tg, "kid", agentpreset.CauseCreate); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.reg.Lookup(sc.Key(), "tool.read"); v != "kid" {
		t.Fatalf("kid's tool.read = %v, want kid", v)
	}
	// A preset rebuilds only when its resolved children change: kid removed the
	// child the edit touches.
	base.Children["tool.note@extra"] = &agentpreset.Child{Config: json.RawMessage(`{"root":"extra2"}`)}
	if err := e.reg.Load(agentpreset.Config{Default: "base"}, []agentpreset.Declaration{base, kid}); err != nil {
		t.Fatal(err)
	}
	if info, _ := e.reg.Resolve("kid"); info.Revision != 1 {
		t.Fatalf("kid rebuilt to rev %d though its resolved children did not change", info.Revision)
	}
	if info, _ := e.reg.Resolve("base"); info.Revision != 2 {
		t.Fatalf("base is at rev %d after its edit, want 2", info.Revision)
	}
}
