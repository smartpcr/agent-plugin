package tool_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/tool"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

const owner = "s-owner"

var bg = context.Background()

// parents is a fork lineage: child -> parent.
type parents map[string]string

func (p parents) Parent(_ context.Context, id string) (string, error) { return p[id], nil }

func newStore(t *testing.T) (*local.Store, home.Layout) {
	t.Helper()
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	s, err := local.New(l, local.DefaultConfig, local.WithLineage(parents{"s-fork": owner}))
	if err != nil {
		t.Fatal(err)
	}
	return s, l
}

func save(t *testing.T, s spill.Store, text string) string {
	t.Helper()
	head, ref, err := spill.Save(bg, s, owner, "big.txt", []byte(text), 0)
	if err != nil || ref == nil || len(head) != 0 {
		t.Fatalf("save: %v", err)
	}
	return ref.Path
}

// numbered is n bytes of numbered lines.
func numbered(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "line %07d\n", i)
	}
	return b.String()[:n]
}

// DH-T-STORAGE-13: a read by offset and limit is the exact slice, capped by the
// tool's most, its ends moved to rune boundaries; the owner and a fork read it,
// another session gets spill/not-owner.
//
//dsh:test DH-T-STORAGE-13
func TestReadByOffsetAndLimit(t *testing.T) {
	s, _ := newStore(t)
	full := numbered(200_000)
	id := save(t, s, full)
	for _, tc := range []struct {
		in   tool.In
		max  int
		want string
	}{
		{tool.In{Ref: id, Offset: 100_000, Limit: 4096}, 32 << 10, full[100_000:104_096]},
		{tool.In{Ref: id, Offset: 7}, 1000, full[7:1007]},
		{tool.In{Ref: id, Offset: 10, Limit: 1 << 20}, 0, full[10 : 10+tool.DefaultMaxBytes]},
		{tool.In{Ref: id, Offset: 199_990, Limit: 100}, 1000, full[199_990:]},
		{tool.In{Ref: id, Offset: 200_000}, 1000, ""},
		{tool.In{Ref: id, Limit: 1}, 1000, full[:1]},
		{tool.In{Ref: id}, 10, full[:tool.MinMaxBytes]},
	} {
		got, err := tool.Read(bg, s, owner, tc.in, tc.max)
		if err != nil || got != tc.want {
			t.Fatalf("%+v under %d: %d bytes, want %d: %v", tc.in, tc.max, len(got), len(tc.want), err)
		}
	}
	if got, err := tool.Read(bg, s, "s-fork", tool.In{Ref: id, Limit: 20}, 100); err != nil || got != full[:20] {
		t.Fatalf("a fork: %q, %v", got, err)
	}
	if _, err := tool.Read(bg, s, "s-stranger", tool.In{Ref: id}, 100); herr.Kind(err) != spill.ReasonNotOwner {
		t.Fatalf("a stranger: %v", err)
	}

	// Runes: a start inside a rune moves on, an end inside one moves back.
	runes := strings.Repeat("€", 100) // 3 bytes each
	rid := save(t, s, runes)
	got, err := tool.Read(bg, s, owner, tool.In{Ref: rid, Offset: 1, Limit: 300}, 300)
	if err != nil || got != runes[3:300] {
		t.Fatalf("an offset inside a rune: %d bytes, %v", len(got), err)
	}
	got, err = tool.Read(bg, s, owner, tool.In{Ref: rid, Limit: 299}, 300)
	if err != nil || got != runes[:297] {
		t.Fatalf("a limit inside a rune: %d bytes, %v", len(got), err)
	}
}

// DH-T-STORAGE-13: a search gives each matching line from the offset on with its
// byte offset, within the limit, and says where to go on when it ran out of room;
// a first line too long for the limit is cut, not skipped.
//
//dsh:test DH-T-STORAGE-13
func TestReadByPattern(t *testing.T) {
	s, _ := newStore(t)
	full := numbered(130_000) // 10,000 lines of 13 bytes
	id := save(t, s, full)
	got, err := tool.Read(bg, s, owner, tool.In{Ref: id, Offset: 13 * 5000, Pattern: `^line 000(49|50|51)0[0-2]$`}, 1000)
	want := ""
	for _, n := range []int{5000, 5001, 5002, 5100, 5101, 5102} {
		want += fmt.Sprintf("%d:line %07d\n", n*13, n)
	}
	if err != nil || got != want {
		t.Fatalf("got %q, want %q: %v", got, want, err)
	}
	// Out of room: each line is "<6 digits>:line 00ddddd\n" = 20 bytes.
	got, err = tool.Read(bg, s, owner, tool.In{Ref: id, Pattern: `5$`, Limit: 300}, 1000)
	if err != nil || len(got) > 300 || !strings.HasPrefix(got, "65:line 0000005\n") || !strings.Contains(got, "[more matches: search again from offset ") {
		t.Fatalf("out of room (%d bytes): %q, %v", len(got), got, err)
	}
	if got, _ := tool.Read(bg, s, owner, tool.In{Ref: id, Pattern: `nothing`}, 1000); !strings.Contains(got, "no line from offset 0 matches") {
		t.Fatalf("no match: %q", got)
	}

	// Long lines: a 100,000-byte line matches on its first part and is shown cut;
	// a line too long even for a small limit is cut to fit.
	long := "start " + strings.Repeat("é", 50_000) + "\nshort match\n"
	lid := save(t, s, long)
	got, err = tool.Read(bg, s, owner, tool.In{Ref: lid, Pattern: `start|match`}, 2000)
	if err != nil || !strings.HasPrefix(got, "0:start ") || !strings.Contains(got, "...\n") ||
		!strings.HasSuffix(got, fmt.Sprintf("%d:short match\n", len(long)-len("short match\n"))) || len(got) > 2000 {
		t.Fatalf("long lines: %q, %v", got, err)
	}
	got, err = tool.Read(bg, s, owner, tool.In{Ref: lid, Pattern: `start|match`}, tool.MinMaxBytes)
	if err != nil || len(got) > tool.MinMaxBytes || !strings.HasPrefix(got, "0:start ") || !strings.Contains(got, "[more matches: search again from offset ") {
		t.Fatalf("a small limit: %d bytes %q, %v", len(got), got, err)
	}
}

// DH-T-STORAGE-13: bad input is refused before the file is read.
//
//dsh:test DH-T-STORAGE-13
func TestReadRefused(t *testing.T) {
	s, _ := newStore(t)
	id := save(t, s, numbered(1000))
	cancelled, cancel := context.WithCancel(bg)
	cancel()
	for name, tc := range map[string]struct {
		ctx    context.Context
		caller string
		in     tool.In
		want   string
	}{
		"negative offset": {bg, owner, tool.In{Ref: id, Offset: -1}, "negative"},
		"past the end":    {bg, owner, tool.In{Ref: id, Offset: 1001}, "past the end"},
		"bad pattern":     {bg, owner, tool.In{Ref: id, Pattern: "("}, "the pattern"},
		"long pattern":    {bg, owner, tool.In{Ref: id, Pattern: strings.Repeat("a", tool.MaxPatternBytes+1)}, "longer than"},
		"no session":      {bg, "", tool.In{Ref: id}, "no session"},
		"not a spill id":  {bg, owner, tool.In{Ref: "../../etc/passwd"}, "not a spill id"},
		"missing":         {bg, owner, tool.In{Ref: spill.NewID(owner, "gone")}, "no saved file"},
		"cancelled grep":  {cancelled, owner, tool.In{Ref: id, Pattern: "x"}, "canceled"},
	} {
		if _, err := tool.Read(tc.ctx, s, tc.caller, tc.in, 100); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

// registry is a tools.Service that records what is registered.
type registry struct {
	tools.Service
	got []*tools.Declaration
	err error
}

func (r *registry) Register(_ *cordis.Context, d *tools.Declaration) (*cordis.EffectHandle, error) {
	if r.err != nil {
		return nil, r.err
	}
	r.got = append(r.got, d)
	return nil, nil
}

// mapper is a fake filesystem provider.
type mapper struct {
	can   bool
	asked string
}

func (m *mapper) CanMap(dir string) bool { m.asked = dir; return m.can }

// unmapped is a store with no host paths.
type unmapped struct{ spill.Store }

func (unmapped) HostPath(string) (string, bool) { return "", false }

// DH-T-STORAGE-13: the tool is mounted with no provider, or one that cannot map
// the spill directory, or a store with no host paths; it is not mounted when the
// provider can map the directory. It bounds its own output, so the spill policy
// skips it.
//
//dsh:test DH-T-STORAGE-13
func TestMountedOnlyWhereTheSpillDirCannotBeMapped(t *testing.T) {
	s, l := newStore(t)
	if dir, ok := tool.Dir(s); !ok || dir != l.Spill() {
		t.Fatalf("Dir = %q, %v; want %q", dir, ok, l.Spill())
	}
	for name, tc := range map[string]struct {
		store spill.Store
		m     tool.Mapper
		want  bool
	}{
		"no provider":        {s, nil, true},
		"cannot map":         {s, &mapper{can: false}, true},
		"can map":            {s, &mapper{can: true}, false},
		"store has no paths": {unmapped{s}, &mapper{can: true}, true},
	} {
		reg := &registry{}
		_, mounted, err := tool.Mount(nil, reg, tc.store, tc.m, tool.Config{})
		if err != nil || mounted != tc.want || len(reg.got) != map[bool]int{true: 1, false: 0}[tc.want] {
			t.Fatalf("%s: mounted %v, %d registered, %v", name, mounted, len(reg.got), err)
		}
		if m, ok := tc.m.(*mapper); ok && tc.store == spill.Store(s) && m.asked != l.Spill() {
			t.Fatalf("%s: the provider was asked about %q, want %q", name, m.asked, l.Spill())
		}
		if tc.want {
			d := reg.got[0]
			if d.Err() != nil || d.Name() != tool.Name || !d.BoundsOwnOutput() || !d.Classify([]byte(`{"ref":"x"}`)).IsFree() {
				t.Fatalf("%s: the declaration: %v", name, d.Err())
			}
		}
	}
	if _, _, err := tool.Mount(nil, &registry{err: errors.New("taken")}, s, nil, tool.Config{}); err == nil {
		t.Fatal("the registry's refusal was lost")
	}
	if _, _, err := tool.Mount(nil, &registry{}, nil, nil, tool.Config{}); err == nil {
		t.Fatal("a tool without a store was mounted")
	}
	if _, err := tool.Declare(s, tool.Config{MaxBytes: tool.MinMaxBytes - 1}); err == nil {
		t.Fatal("a MaxBytes below the minimum was accepted")
	}
}
