package policy_test

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/policy"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

var me = agent.Ref{Session: session.ID("s-policy"), Incarnation: 1}

func store(t *testing.T) *local.Store {
	t.Helper()
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	s, err := local.New(l, local.DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func call(ag agent.Ref) tools.Call {
	return tools.NewCall("c1", "shell", []byte(`{}`), tools.OriginModel, "", ag, nil, nil)
}

// failing is a spill store whose every save fails.
type failing struct{ spill.Store }

func (failing) Create(context.Context, spill.Ref, int64) (spill.Writer, error) {
	return nil, errors.New("disk full\nand more")
}

// unmapped is a store whose files have no host path, and whose writer fails at
// write or close as asked.
type unmapped struct {
	spill.Store
	failWrite, failClose bool
}

func (u unmapped) HostPath(string) (string, bool) { return "", false }

func (u unmapped) Create(ctx context.Context, ref spill.Ref, limit int64) (spill.Writer, error) {
	w, err := u.Store.Create(ctx, ref, limit)
	if err != nil {
		return nil, err
	}
	return badWriter{w, u}, nil
}

type badWriter struct {
	spill.Writer
	u unmapped
}

func (b badWriter) Write(p []byte) (int, error) {
	if b.u.failWrite {
		return 0, errors.New(strings.Repeat("write failed ", 40))
	}
	return b.Writer.Write(p)
}

func (b badWriter) Close() error {
	if b.u.failClose {
		_ = b.Writer.Abort()
		return errors.New("close failed")
	}
	return b.Writer.Close()
}

// seat is a tools.Service that records the spill seat it is given.
type seat struct {
	tools.Service
	limit int
	got   tools.SpillShaper
	err   error
}

func (s *seat) Limits() tools.Limits { return tools.Limits{MaxResultBytes: s.limit} }

func (s *seat) SetSpill(_ *cordis.Context, id string, f tools.SpillShaper) (*cordis.EffectHandle, error) {
	if id != policy.SeatID {
		return nil, errors.New("wrong id " + id)
	}
	s.got = f
	return nil, s.err
}

// Mount files the policy in the registry's seat under its ceiling, and passes
// the registry's refusal on.
func TestPolicyMount(t *testing.T) {
	s := store(t)
	svc := &seat{limit: 256 << 10}
	p, _, err := policy.Mount(nil, svc, s, policy.Config{})
	if err != nil || p == nil || svc.got == nil {
		t.Fatalf("Mount: %v", err)
	}
	if _, _, err := policy.Mount(nil, &seat{limit: 1 << 10}, s, policy.Config{}); err == nil {
		t.Fatal("a budget above the ceiling was mounted")
	}
	if _, _, err := policy.Mount(nil, &seat{limit: 256 << 10, err: errors.New("taken")}, s, policy.Config{}); err == nil {
		t.Fatal("the seat's refusal was lost")
	}
}

// DH-T-STORAGE-11: an unmappable path is cited by id, and a save that fails at
// write or close leaves no file and says so with its reason clipped.
func TestPolicyUnmappedAndBrokenWriter(t *testing.T) {
	text := strings.Repeat("y", 100_000)
	base := store(t)
	p, err := policy.New(unmapped{Store: base}, policy.Config{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r := p.Shape(context.Background(), call(me), tools.Outcome{Text: text})
	if r.Ref == nil || !strings.Contains(r.Text, r.Ref.Path) || len(r.Text) > p.Budget() {
		t.Fatalf("unmapped: %+v", r.Ref)
	}
	for _, u := range []unmapped{{Store: base, failWrite: true}, {Store: base, failClose: true}} {
		p, err := policy.New(u, policy.Config{}, 0)
		if err != nil {
			t.Fatal(err)
		}
		r := p.Shape(context.Background(), call(me), tools.Outcome{Text: text})
		if r.Ref != nil || !strings.Contains(r.Text, "was not saved") || len(r.Text) > p.Budget() {
			t.Fatalf("%+v: %+v", u, r)
		}
		if u.failWrite && !strings.Contains(r.Text, "...") {
			t.Fatalf("a long reason was not clipped: %q", r.Text[len(r.Text)-300:])
		}
	}
}

// DH-T-STORAGE-12: a budget below the minimum, or one the registry's ceiling
// would cut, is refused at load.
func TestPolicyConfigRefused(t *testing.T) {
	s := store(t)
	for _, tc := range []struct {
		cfg     policy.Config
		ceiling int
		want    string
	}{
		{policy.Config{MaxInlineTokens: policy.MinInlineTokens - 1}, 0, "below the minimum"},
		{policy.Config{Keep: -time.Hour}, 0, "negative"},
		{policy.Config{}, 1 << 10, "exceeds the registry's maxResultBytes"},
	} {
		if _, err := policy.New(s, tc.cfg, tc.ceiling); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v under %d: %v, want %q", tc.cfg, tc.ceiling, err, tc.want)
		}
	}
	if _, err := policy.New(nil, policy.Config{}, 0); err == nil {
		t.Error("a policy without a store was accepted")
	}
	p, err := policy.New(s, policy.Config{MaxInlineTokens: policy.MinInlineTokens}, 0)
	if err != nil || p.Budget() != policy.MinInlineTokens*4 {
		t.Fatalf("the minimum: %v, budget %d", err, p.Budget())
	}
}

// DH-T-STORAGE-11: a failed save still bounds the result, gives no reference
// and says the full result was not saved; so does a call with no session.
func TestPolicyFailedSaveStillBounds(t *testing.T) {
	text := strings.Repeat("x", 100_000)
	for name, tc := range map[string]struct {
		s  spill.Store
		ag agent.Ref
	}{
		"failing store": {failing{store(t)}, me},
		"no session":    {store(t), agent.Ref{}},
	} {
		p, err := policy.New(tc.s, policy.Config{}, 0)
		if err != nil {
			t.Fatal(err)
		}
		r := p.Shape(context.Background(), call(tc.ag), tools.Outcome{Text: text})
		if !r.Replace || r.Ref != nil || len(r.Text) > p.Budget() || !strings.Contains(r.Text, "was not saved") {
			t.Fatalf("%s: replace %v, ref %+v, %d bytes: %q", name, r.Replace, r.Ref, len(r.Text), r.Text[len(r.Text)-200:])
		}
	}
}

// DH-T-STORAGE-12 (property): no accepted budget yields an over-budget result,
// whatever the text's size and runes, and a saved file holds the whole text.
func TestPolicyNeverOverBudget(t *testing.T) {
	s := store(t)
	rng := rand.New(rand.NewPCG(1, 2))
	runes := []string{"a", "é", "€", "𝄞", "\n"}
	for i := 0; i < 40; i++ {
		tokens := policy.MinInlineTokens + rng.IntN(4000)
		p, err := policy.New(s, policy.Config{MaxInlineTokens: tokens}, 0)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for n := rng.IntN(4 * p.Budget()); b.Len() < n; {
			b.WriteString(runes[rng.IntN(len(runes))])
		}
		text := b.String()
		r := p.Shape(context.Background(), call(me), tools.Outcome{Text: text})
		got := text
		if r.Replace {
			got = r.Text
		}
		if len(got) > p.Budget() || !utf8.ValidString(got) {
			t.Fatalf("budget %d, text %d bytes: result %d bytes (valid %v)", p.Budget(), len(text), len(got), utf8.ValidString(got))
		}
		if len(text) <= p.Budget() {
			if r.Replace {
				t.Fatalf("a text within the budget was replaced")
			}
			continue
		}
		if r.Ref == nil || r.Ref.Bytes != int64(len(text)) {
			t.Fatalf("no reference for %d bytes: %+v", len(text), r.Ref)
		}
		rc, err := s.Open(context.Background(), string(me.Session), r.Ref.Path)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || string(saved) != text {
			t.Fatalf("the saved file differs from the text: %v", err)
		}
	}
}
