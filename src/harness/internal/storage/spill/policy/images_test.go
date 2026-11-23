package policy_test

import (
	"context"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/meter"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/policy"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

var (
	imgA = vocab.Image{MediaType: "image/png", Ref: "image:aaaa"}
	imgB = vocab.Image{MediaType: "image/jpeg", Ref: "image:bbbb"}
)

// cost is a shaped result's size in budget bytes, priced as the policy prices
// it: its text bytes plus each kept image's tokens times meter.CharsPerToken, and
// its estimated tokens.
func cost(parts []vocab.Part, price func(vocab.Image) int) (bytes, tokens int) {
	text := policy.TextOf(parts)
	bytes, tokens = len(text), meter.EstimateText(text)
	for _, p := range parts {
		if p.Image != nil {
			bytes += price(*p.Image) * meter.CharsPerToken
			tokens += price(*p.Image)
		}
	}
	return bytes, tokens
}

// imagesOf is the images of parts, in order.
func imagesOf(parts []vocab.Part) []vocab.Image {
	var out []vocab.Image
	for _, p := range parts {
		if p.Image != nil {
			out = append(out, *p.Image)
		}
	}
	return out
}

func readSaved(t *testing.T, s spill.Store, id string) string {
	t.Helper()
	rc, err := s.Open(context.Background(), string(me.Session), id)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Scenario: An image never splits the budget (plan 16.3, M1-G-PIPE-2). Given a
// result of text plus two images priced over the budget, When it is shaped with
// no route calculator, Then it is within budget, each image is present or
// absent whole, and the saved file has an image line at each position. Also
// DH-T-STORAGE-11: with the calculator missing an image is priced at the
// fallback, and the notice says how many images were omitted.
//
//dsh:test DH-T-STORAGE-11
func TestImagesNeverSplitTheBudget(t *testing.T) {
	s := store(t)
	// 2,000 tokens is 8,000 budget bytes; each image at the 1,500-token
	// fallback costs 6,000, so both together are over the budget.
	p, err := policy.New(s, policy.Config{MaxInlineTokens: 2000}, 0)
	if err != nil {
		t.Fatal(err)
	}
	fallback := func(vocab.Image) int { return policy.DefaultImageTokenFallback }
	for name, texts := range map[string][3]string{
		"short texts":   {"before the images", "between them", "after them\n"},
		"a long middle": {"head text\n", strings.Repeat("middle €\n", 2000), "tail text"},
		"a long start":  {strings.Repeat("a", 20_000), "b", strings.Repeat("c", 30)},
	} {
		t.Run(name, func(t *testing.T) {
			parts := []vocab.Part{vocab.TextPart(texts[0]), {Image: &imgA}, vocab.TextPart(texts[1]), {Image: &imgB}, vocab.TextPart(texts[2])}
			r := p.ShapeParts(context.Background(), call(me), parts)
			if !r.Replace || r.Ref == nil {
				t.Fatalf("not replaced and saved: %+v", r)
			}
			if b, tok := cost(r.Parts, fallback); b > p.Budget() || tok > 2000 {
				t.Fatalf("over budget: %d budget bytes (of %d), %d tokens", b, p.Budget(), tok)
			}
			if !utf8.ValidString(policy.TextOf(r.Parts)) {
				t.Fatal("the preview splits a rune")
			}
			// Each image is there whole, in order, or not at all.
			kept := imagesOf(r.Parts)
			if len(kept)+r.ImagesOmitted != 2 || r.ImagesOmitted == 0 {
				t.Fatalf("kept %v, omitted %d", kept, r.ImagesOmitted)
			}
			for _, k := range kept {
				if k != imgA && k != imgB {
					t.Fatalf("an image was altered: %+v", k)
				}
			}
			if len(kept) == 2 && kept[0] != imgA {
				t.Fatal("the images lost their order")
			}
			text := policy.TextOf(r.Parts)
			if want := " and " + string(rune('0'+r.ImagesOmitted)) + " of 2 images."; !strings.Contains(text, want) {
				t.Fatalf("the notice does not say %q: %q", want, text[max(0, len(text)-300):])
			}
			// The saved file is the whole result with an image line at each
			// image's position, on a line of its own.
			saved := readSaved(t, s, r.Ref.Path)
			line := func(img vocab.Image, before string) string {
				if before != "" && !strings.HasSuffix(before, "\n") {
					return "\n" + policy.ImageLine(img)
				}
				return policy.ImageLine(img)
			}
			want := texts[0] + line(imgA, texts[0]) + texts[1] + line(imgB, texts[1]) + texts[2]
			if saved != want || r.Ref.Bytes != int64(len(want)) {
				t.Fatalf("saved %d bytes (ref %d), want %d:\n%q", len(saved), r.Ref.Bytes, len(want), saved[:min(len(saved), 200)])
			}
			if !strings.Contains(saved, "[image image/png image:aaaa]\n") || !strings.Contains(saved, "[image image/jpeg image:bbbb]\n") {
				t.Fatalf("the image lines are missing: %q", saved[:min(len(saved), 200)])
			}
		})
	}
}

// DH-T-STORAGE-11: a route calculator prices the images; one that gives no
// price, a negative one or panics falls back. Images the budget can hold are
// kept; within the budget nothing is replaced.
//
//dsh:test DH-T-STORAGE-11
func TestImagesPricedByTheRoute(t *testing.T) {
	s := store(t)
	text := strings.Repeat("z", 9000)
	parts := []vocab.Part{vocab.TextPart(text[:10]), {Image: &imgA}, vocab.TextPart(text), {Image: &imgB}}
	for name, tc := range map[string]struct {
		price   policy.ImagePricer
		tokens  int
		omitted int
	}{
		"cheap route": {func(context.Context, tools.Call, vocab.Image) (int, bool) { return 10, true }, 10, 0},
		"no price": {func(context.Context, tools.Call, vocab.Image) (int, bool) { return 0, false },
			policy.DefaultImageTokenFallback, 2},
		"negative": {func(context.Context, tools.Call, vocab.Image) (int, bool) { return -5, true },
			policy.DefaultImageTokenFallback, 2},
		"panics": {func(context.Context, tools.Call, vocab.Image) (int, bool) { panic("route gone") },
			policy.DefaultImageTokenFallback, 2},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := policy.New(s, policy.Config{MaxInlineTokens: policy.MinInlineTokens * 3, PriceImage: tc.price}, 0)
			if err != nil {
				t.Fatal(err)
			}
			r := p.ShapeParts(context.Background(), call(me), parts)
			if !r.Replace || r.ImagesOmitted != tc.omitted || len(imagesOf(r.Parts)) != 2-tc.omitted {
				t.Fatalf("replace %v, omitted %d, kept %v", r.Replace, r.ImagesOmitted, imagesOf(r.Parts))
			}
			if b, _ := cost(r.Parts, func(vocab.Image) int { return tc.tokens }); b > p.Budget() {
				t.Fatalf("%d budget bytes over %d", b, p.Budget())
			}
		})
	}
	// Within the budget, images included, nothing changes.
	p, err := policy.New(s, policy.Config{ImageTokenFallback: 100}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r := p.ShapeParts(context.Background(), call(me), []vocab.Part{vocab.TextPart("hi"), {Image: &imgA}}); r.Replace {
		t.Fatalf("a result within the budget was replaced: %+v", r)
	}
	if r := p.ShapeParts(context.Background(), call(me), nil); r.Replace {
		t.Fatal("an empty result was replaced")
	}
}

// DH-T-STORAGE-11: with a failing store and images, the result is still within
// budget, has no reference, says it was not saved and how many images were
// omitted, and no file is left in the spill directory.
//
//dsh:test DH-T-STORAGE-11
func TestImagesWithAFailingStore(t *testing.T) {
	base := store(t)
	p, err := policy.New(failing{base}, policy.Config{MaxInlineTokens: 2000}, 0)
	if err != nil {
		t.Fatal(err)
	}
	parts := []vocab.Part{{Image: &imgA}, vocab.TextPart(strings.Repeat("q", 10_000)), {Image: &imgB}}
	r := p.ShapeParts(context.Background(), call(me), parts)
	text := policy.TextOf(r.Parts)
	if !r.Replace || r.Ref != nil || !strings.Contains(text, "was not saved") || !strings.Contains(text, "of 2 images") {
		t.Fatalf("%+v: %q", r, text[max(0, len(text)-300):])
	}
	if b, _ := cost(r.Parts, func(vocab.Image) int { return policy.DefaultImageTokenFallback }); b > p.Budget() {
		t.Fatalf("%d budget bytes over %d", b, p.Budget())
	}
	host, ok := base.HostPath(spill.NewID(string(me.Session), "x"))
	if !ok {
		t.Fatal("no host path")
	}
	if entries, err := os.ReadDir(filepath.Dir(host)); err == nil && len(entries) > 0 {
		t.Fatalf("a file was orphaned: %v", entries)
	}
}

// DH-T-STORAGE-12: a negative image fallback is refused at load.
//
//dsh:test DH-T-STORAGE-12
func TestImageFallbackRefused(t *testing.T) {
	if _, err := policy.New(store(t), policy.Config{ImageTokenFallback: -1}, 0); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("a negative fallback: %v", err)
	}
}

// DH-T-STORAGE-13 (the notice side): with the read tool named, the notice cites
// the file's id, not a host path, and names the tool.
//
//dsh:test DH-T-STORAGE-13
func TestNoticeNamesTheReadTool(t *testing.T) {
	s := store(t)
	p, err := policy.New(s, policy.Config{ReadTool: "spill_read"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r := p.Shape(context.Background(), call(me), tools.Outcome{Text: strings.Repeat("w", 100_000)})
	if r.Ref == nil || len(r.Text) > p.Budget() || !strings.Contains(r.Text, "saved to "+r.Ref.Path+",") || !strings.Contains(r.Text, "Use spill_read with this ref") {
		t.Fatalf("%q", r.Text[len(r.Text)-300:])
	}
	if host, _ := s.HostPath(r.Ref.Path); strings.Contains(r.Text, host) {
		t.Fatal("the notice cites the host path")
	}
}

// DH-T-STORAGE-12 (property, images): no mix of texts and images, priced by any
// route, yields a result over the budget, and the saved file always holds every
// text and an image line for every image.
//
//dsh:test DH-T-STORAGE-12
func TestImagesNeverOverBudget(t *testing.T) {
	s := store(t)
	rng := rand.New(rand.NewPCG(3, 4))
	runes := []string{"a", "é", "€", "𝄞", "\n"}
	for i := 0; i < 60; i++ {
		tokens := policy.MinInlineTokens + rng.IntN(3000)
		prices := map[string]int{}
		price := func(_ context.Context, _ tools.Call, img vocab.Image) (int, bool) {
			n, ok := prices[img.Ref]
			return n, ok
		}
		p, err := policy.New(s, policy.Config{MaxInlineTokens: tokens, PriceImage: price}, 0)
		if err != nil {
			t.Fatal(err)
		}
		var parts []vocab.Part
		for k := rng.IntN(8); k >= 0; k-- {
			if rng.IntN(3) == 0 {
				img := vocab.Image{MediaType: "image/webp", Ref: "image:" + string(rune('a'+len(prices)))}
				if rng.IntN(4) > 0 {
					prices[img.Ref] = rng.IntN(tokens)
				}
				parts = append(parts, vocab.Part{Image: &img})
				continue
			}
			var b strings.Builder
			for n := rng.IntN(p.Budget()); b.Len() < n; {
				b.WriteString(runes[rng.IntN(len(runes))])
			}
			parts = append(parts, vocab.TextPart(b.String()))
		}
		priceOf := func(img vocab.Image) int {
			if n, ok := prices[img.Ref]; ok {
				return n
			}
			return policy.DefaultImageTokenFallback
		}
		r := p.ShapeParts(context.Background(), call(me), parts)
		got := parts
		if r.Replace {
			got = r.Parts
		}
		if b, tok := cost(got, priceOf); b > p.Budget() || tok > tokens || !utf8.ValidString(policy.TextOf(got)) {
			t.Fatalf("case %d: %d budget bytes of %d, %d tokens of %d", i, b, p.Budget(), tok, tokens)
		}
		if !r.Replace {
			continue
		}
		saved := readSaved(t, s, r.Ref.Path)
		rest := saved
		for _, pt := range parts {
			want := ""
			if pt.Image != nil {
				want = policy.ImageLine(*pt.Image)
			} else {
				want = pt.Text.Text
			}
			k := strings.Index(rest, want)
			if k < 0 || (pt.Text != nil && k != 0) || k > 1 {
				t.Fatalf("case %d: the saved text does not hold %q in order", i, want[:min(len(want), 40)])
			}
			rest = rest[k+len(want):]
		}
		if rest != "" {
			t.Fatalf("case %d: %d unexpected bytes after the last part", i, len(rest))
		}
	}
}
