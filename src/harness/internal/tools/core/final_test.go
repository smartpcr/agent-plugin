package core_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

// A final shaper runs after every ordinary shaper, whichever was registered first
// and in whichever layer, on the text they left (a Block's text included); it
// runs on a call refused at resolve too; one that does not answer fails the call
// and hides the text; and it needs an id and a function.
//
//dsh:test DH-T-TOOLS-4
func TestFinalShapersHaveTheLastWord(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{})
	final, ok := svc.(tools.FinalShaper)
	if !ok {
		t.Fatal("the registry is not a tools.FinalShaper")
	}
	e.register(t, e.root, echo("Echoes."))
	var order []string
	mask := func(_ tools.Call, o tools.Outcome) tools.PostDecision {
		order = append(order, "final")
		return tools.ReplaceContent(strings.ReplaceAll(o.Text, "hidden", "[masked]"))
	}
	if _, err := final.ShapeFinal(e.root, "mask", mask); err != nil {
		t.Fatal(err)
	}
	// Registered after the final shaper, it puts the word back from the value.
	if _, err := svc.Shape(e.root, "later", func(_ tools.Call, o tools.Outcome) tools.PostDecision {
		order = append(order, "ordinary")
		return tools.ReplaceContent("rebuilt " + o.Value.(echoOut).Text)
	}); err != nil {
		t.Fatal(err)
	}
	call := [2]string{"echo", `{"text":"hidden"}`}
	res := execute(t, svc, view(t, svc), openSession(t, memstore.New(), call), call)[0]
	if res.IsError || res.Text != "rebuilt [masked]" || !slices.Equal(order, []string{"ordinary", "final"}) {
		t.Fatalf("result %+v after %v; want the final shaper last", res, order)
	}

	if _, err := svc.Shape(e.root, "block", func(tools.Call, tools.Outcome) tools.PostDecision {
		return tools.Block("refused hidden")
	}); err != nil {
		t.Fatal(err)
	}
	res = execute(t, svc, view(t, svc), openSession(t, memstore.New(), call), call)[0]
	if res.Code != tools.CodeBlocked || res.Text != string(tools.CodeBlocked)+": refused [masked]" {
		t.Fatalf("blocked result %+v; want the block's text masked", res)
	}

	unknown := [2]string{"hidden", `{}`}
	res = execute(t, svc, view(t, svc), openSession(t, memstore.New(), unknown), unknown)[0]
	if res.Code != tools.CodeNotStarted || strings.Contains(res.Text, "hidden") {
		t.Fatalf("refused result %+v; want the final shaper to mask it too", res)
	}

	if _, err := final.ShapeFinal(e.root, "broken", func(tools.Call, tools.Outcome) tools.PostDecision {
		panic("cannot mask")
	}); err != nil {
		t.Fatal(err)
	}
	res = execute(t, svc, view(t, svc), openSession(t, memstore.New(), call), call)[0]
	if res.Code != tools.CodeFailed || res.By != "broken" || strings.Contains(res.Text, "hidden") || res.Value != nil {
		t.Fatalf("result with a failing final shaper %+v; want tools/failed by it, the text hidden", res)
	}

	for _, bad := range []struct {
		id string
		s  tools.Shaper
	}{{"", mask}, {"x", nil}} {
		if _, err := final.ShapeFinal(e.root, bad.id, bad.s); !errors.Is(err, tools.ErrInvalidDeclaration) {
			t.Errorf("ShapeFinal(%q) = %v; want an invalid declaration", bad.id, err)
		}
	}
}
