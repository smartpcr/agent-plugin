package core_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/meter"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/policy"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

// A result with images (tools.Content) is carried as its parts from the body
// through the shapers and the spill seat to the tool/result message; the spill
// policy prices each image and keeps or omits it whole (stage 16.3 deviation,
// closed in 16.2).

func contentTool(name string, parts ...vocab.Part) *tools.Declaration {
	return tools.Define(tools.Spec[noIn, tools.Content]{
		Name: name, Description: "Shows a picture.",
		Run: func(context.Context, noIn, tools.Run) (tools.Content, error) { return tools.Content{Parts: parts}, nil },
	})
}

// loggedParts is the content parts of the tool/result of call id in h.
func loggedParts(t *testing.T, h session.Handle, id string) []vocab.Part {
	t.Helper()
	logged, _ := resultsOf(t, h)
	p, ok := logged[id]
	if !ok {
		t.Fatalf("no tool/result for %s", id)
	}
	bs := p.Message.Blocks()
	if len(bs) != 1 || bs[0].ToolResult == nil {
		t.Fatalf("the logged message of %s has blocks %+v", id, bs)
	}
	return bs[0].ToolResult.Parts
}

func imageRefs(parts []vocab.Part) []string {
	var out []string
	for _, p := range parts {
		if p.Image != nil {
			out = append(out, p.Image.Ref)
		}
	}
	return out
}

func textBytes(parts []vocab.Part) int {
	n := 0
	for _, p := range parts {
		if p.Text != nil {
			n += len(p.Text.Text)
		}
	}
	return n
}

// Through the real pipeline with the spill policy mounted, a result of text and
// two images priced over the budget is bounded with each image kept or omitted
// whole: the small one is kept, the large one omitted, the notice counts the
// omitted image, the logged tool/result holds exactly the kept parts in order,
// and the saved file has an image line at each image's position.
//
//dsh:test DH-T-STORAGE-11
func TestImagesReachTheSpillSeatAndAreKeptOrOmittedWhole(t *testing.T) {
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	sp, err := local.New(l, local.DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{Checkpoint: recCheckpoint{&trace{}}})
	prices := map[string]int{"att-small": 25, "att-big": 5000}
	pol, _, err := policy.Mount(e.root, svc, sp, policy.Config{
		MaxInlineTokens: policy.MinInlineTokens, ReadTool: "spill_read", Now: func() time.Time { return epoch },
		PriceImage: func(_ context.Context, _ tools.Call, img vocab.Image) (int, bool) {
			n, ok := prices[img.Ref]
			return n, ok
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	small, big := vocab.ImagePart("image/png", "att-small"), vocab.ImagePart("image/jpeg", "att-big")
	body := numbered(10000)
	e.register(t, e.root, contentTool("shot", vocab.TextPart("caption\n"), small, vocab.TextPart(body), big, vocab.TextPart("end\n")))
	var shaped []vocab.Part
	if _, err := svc.Shape(e.root, "look", func(_ tools.Call, o tools.Outcome) tools.PostDecision {
		shaped = o.Parts
		return tools.Accept()
	}); err != nil {
		t.Fatal(err)
	}
	call := [2]string{"shot", `{}`}
	h := openSession(t, memstore.New(), call)
	r := execute(t, svc, view(t, svc), h, call)[0]

	if got := imageRefs(shaped); len(got) != 2 {
		t.Fatalf("the shaper saw images %v, want both", got)
	}
	if r.IsError || r.Spill == nil || r.Truncated {
		t.Fatalf("result %+v, want a spilled success", r)
	}
	if got := imageRefs(r.Parts); len(got) != 1 || got[0] != "att-small" {
		t.Fatalf("kept images %v, want only att-small", got)
	}
	if cost := textBytes(r.Parts) + prices["att-small"]*meter.CharsPerToken; cost > pol.Budget() {
		t.Fatalf("the kept result costs %d budget bytes, over %d", cost, pol.Budget())
	}
	if !strings.HasPrefix(r.Text, "caption\n") || !strings.Contains(r.Text, "and 1 of 2 images") || r.Text != textOfParts(r.Parts) {
		t.Fatalf("text %q", r.Text)
	}
	logged := loggedParts(t, h, "c1")
	if got := imageRefs(logged); len(got) != 1 || got[0] != "att-small" || textOfParts(logged) != r.Text {
		t.Fatalf("logged parts %+v, want the kept parts", logged)
	}
	if logged[0].Text == nil || logged[1].Image == nil {
		t.Fatalf("the logged parts lost their order: %+v", logged)
	}
	host, ok := sp.HostPath(r.Spill.Path)
	if !ok {
		t.Fatalf("no host path for %s", r.Spill.Path)
	}
	saved, err := os.ReadFile(host)
	if err != nil {
		t.Fatal(err)
	}
	want := "caption\n" + policy.ImageLine(*small.Image) + body + "\n" + policy.ImageLine(*big.Image) + "end\n"
	if string(saved) != want {
		t.Fatalf("the saved file (%d bytes) is not the parts with an image line at each image", len(saved))
	}
}

func textOfParts(parts []vocab.Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Text != nil {
			b.WriteString(p.Text.Text)
		}
	}
	return b.String()
}

// A result with images within every bound is logged with its parts unchanged;
// a Content of text alone is a text-only result, logged as one text part as a
// string result is.
func TestImageResultWithinBudgetKeepsItsParts(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{})
	img := vocab.ImagePart("image/webp", "att-1")
	e.register(t, e.root, contentTool("shot", vocab.TextPart("a "), img, vocab.TextPart("b")))
	e.register(t, e.root, contentTool("plain", vocab.TextPart("only "), vocab.TextPart("text")))
	e.register(t, e.root, textTool("str", "only text", false))
	calls := [][2]string{{"shot", `{}`}, {"plain", `{}`}, {"str", `{}`}}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, view(t, svc), h, calls...)
	if r := res[0]; r.IsError || r.Text != "a b" || len(r.Parts) != 3 || r.Parts[1].Image == nil || *r.Parts[1].Image != *img.Image {
		t.Fatalf("image result %+v", r)
	}
	if got := loggedParts(t, h, "c1"); len(got) != 3 || got[0].Text.Text != "a " || got[1].Image.Ref != "att-1" || got[2].Text.Text != "b" {
		t.Fatalf("logged image parts %+v", got)
	}
	if r := res[1]; r.IsError || r.Parts != nil || r.Text != "only text" {
		t.Fatalf("text-only content %+v", r)
	}
	plain, str := loggedParts(t, h, "c2"), loggedParts(t, h, "c3")
	if len(plain) != 1 || len(str) != 1 || plain[0].Text.Text != str[0].Text.Text {
		t.Fatalf("a text-only content is logged as %+v, a string as %+v", plain, str)
	}
}

// Without a spill seat, the ceiling cuts a result with images at the text that
// overflows: earlier images are kept, later ones are left out whole, and the
// marker says how much text was dropped.
func TestCeilingCutsAnImageResultBetweenParts(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{MaxResultBytes: 200})
	e.register(t, e.root, contentTool("shot", vocab.TextPart("head "), vocab.ImagePart("image/png", "att-1"),
		vocab.TextPart(strings.Repeat("é", 300)), vocab.ImagePart("image/png", "att-2"), vocab.TextPart("tail")))
	call := [2]string{"shot", `{}`}
	h := openSession(t, memstore.New(), call)
	r := execute(t, svc, view(t, svc), h, call)[0]
	if r.IsError || !r.Truncated || len(r.Text) > 200 || !strings.Contains(r.Text, "[truncated:") {
		t.Fatalf("result (%d bytes) %+v", len(r.Text), r)
	}
	if got := imageRefs(r.Parts); len(got) != 1 || got[0] != "att-1" {
		t.Fatalf("images after the ceiling %v, want att-1 only", got)
	}
	if got := imageRefs(loggedParts(t, h, "c1")); len(got) != 1 {
		t.Fatalf("logged images %v", got)
	}
}

// A part that is not valid (an image with no reference, a part that is neither
// text nor image) is invalid output, so the call still settles; a shaper that
// rewrites an image result's text replaces it whole, and one that returns the
// same text keeps the images.
func TestImagePartsInvalidOrRewritten(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{})
	e.register(t, e.root, contentTool("noref", vocab.ImagePart("image/png", "")))
	e.register(t, e.root, contentTool("empty", vocab.Part{}))
	e.register(t, e.root, contentTool("keep", vocab.TextPart("same"), vocab.ImagePart("image/gif", "att-k")))
	e.register(t, e.root, contentTool("redo", vocab.TextPart("secret"), vocab.ImagePart("image/gif", "att-r")))
	if _, err := svc.Shape(e.root, "mask", func(_ tools.Call, o tools.Outcome) tools.PostDecision {
		return tools.ReplaceContent(strings.ReplaceAll(o.Text, "secret", "[masked]"))
	}); err != nil {
		t.Fatal(err)
	}
	calls := [][2]string{{"noref", `{}`}, {"empty", `{}`}, {"keep", `{}`}, {"redo", `{}`}}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, view(t, svc), h, calls...)
	for i := range 2 {
		if r := res[i]; r.Code != tools.CodeInvalidOutput || r.Parts != nil || !strings.Contains(r.Text, "part 0") {
			t.Fatalf("call %d: %+v, want invalid output naming the part", i, r)
		}
	}
	if r := res[2]; r.IsError || len(imageRefs(r.Parts)) != 1 {
		t.Fatalf("unchanged text %+v, want the image kept", r)
	}
	if r := res[3]; r.IsError || r.Parts != nil || r.Text != "[masked]" || len(imageRefs(loggedParts(t, h, "c4"))) != 0 {
		t.Fatalf("rewritten text %+v, want the content replaced whole", r)
	}
}

// A spill seat that grows an image result, or answers with a part that is not
// valid, is a spill failure: the result keeps its parts and ends with the
// not-saved notice, within the ceiling.
func TestSpillSeatCannotGrowAnImageResult(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []vocab.Part
		want  string
	}{
		{"more images", []vocab.Part{vocab.TextPart("x"), vocab.ImagePart("image/png", "a"), vocab.ImagePart("image/png", "b")}, "grew the result"},
		{"invalid part", []vocab.Part{{}}, "invalid part 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			svc := e.pipeline(t, core.Config{MaxResultBytes: 400})
			if _, err := svc.SetSpill(e.root, "bad-seat", func(context.Context, tools.Call, tools.Outcome) tools.SpillResult {
				return tools.SpillResult{Replace: true, Text: "x", Parts: tc.parts}
			}); err != nil {
				t.Fatal(err)
			}
			e.register(t, e.root, contentTool("shot", vocab.TextPart(strings.Repeat("y", 600)), vocab.ImagePart("image/png", "a")))
			call := [2]string{"shot", `{}`}
			h := openSession(t, memstore.New(), call)
			r := execute(t, svc, view(t, svc), h, call)[0]
			if r.SpillFailure == "" || !strings.Contains(r.SpillFailure, tc.want) || len(r.Text) > 400 ||
				!strings.Contains(r.Text, "not saved") || !r.Truncated {
				t.Fatalf("result %+v", r)
			}
			if got := loggedParts(t, h, "c1"); textOfParts(got) != r.Text {
				t.Fatalf("logged %+v", got)
			}
		})
	}
}

// The parts a Settled observer reads are copies: changing them reaches neither
// a second read, nor the caller's result, nor the log.
func TestSettledPartsAreCopies(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{})
	e.register(t, e.root, contentTool("shot", vocab.TextPart("a"), vocab.ImagePart("image/png", "att-1")))
	var again tools.Result
	settledObserver(t, e.root, "settled.copies", func(_ *cordis.Context, s tools.Settlement) error {
		s.Result().Parts[1].Image.Ref = "changed"
		again = s.Result()
		return nil
	})
	call := [2]string{"shot", `{}`}
	h := openSession(t, memstore.New(), call)
	r := execute(t, svc, view(t, svc), h, call)[0]
	if again.Parts[1].Image.Ref != "att-1" || r.Parts[1].Image.Ref != "att-1" {
		t.Fatalf("an observer changed the parts: %+v, %+v", again.Parts, r.Parts)
	}
	r.Parts[1].Image.Ref = "changed"
	if got := loggedParts(t, h, "c1"); got[1].Image.Ref != "att-1" {
		t.Fatal("the caller's parts share the logged message")
	}
}
