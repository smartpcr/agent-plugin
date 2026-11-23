package search

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGrepNoPhantomEOFLine: the empty read after a final terminator is not a line,
// and an empty file has none.
//
//dsh:test DH-T-RIPGREP-1
func TestGrepNoPhantomEOFLine(t *testing.T) {
	src := tree(t, map[string]string{"one": "a\n", "empty": "", "blank": "\n"})
	got := grepAll(t, GrepQuery{From: src, Pattern: "^$"})
	want := []Match{{Path: "blank", Line: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestGrepLongLine: a line over the scan cap is not searched as though complete; a
// needle past the cap and an end anchor on the kept prefix both make the result
// Incomplete instead of a silent miss or a false match.
//
//dsh:test DH-T-RIPGREP-1, DH-T-RIPGREP-3
func TestGrepLongLine(t *testing.T) {
	big := strings.Repeat("a", maxScanLine+10)
	src := tree(t, map[string]string{
		"beyond": big + "needle\nshort needle\n",
		"anchor": big + "b\n",
	})
	for _, tc := range []struct{ pattern, root string }{
		{"needle", "beyond"},
		{"a$", "anchor"},
	} {
		var got []Match
		sum, err := Grep(context.Background(), GrepQuery{From: src, Root: tc.root, Pattern: tc.pattern},
			func(m Match) bool { got = append(got, m); return true })
		if err != nil {
			t.Fatal(err)
		}
		if !sum.Incomplete || sum.Reason != CutLongLine {
			t.Errorf("%s: summary %+v, want incomplete %q", tc.pattern, sum, CutLongLine)
		}
		for _, m := range got {
			if m.Line == 1 {
				t.Errorf("%s: long line reported as a match: %+v", tc.pattern, m.Path)
			}
		}
		if tc.root == "beyond" && (len(got) != 1 || got[0].Line != 2) {
			t.Errorf("short line after the long one: got %+v", got)
		}
	}
}

// TestGrepBytesCountsConsumed: every byte read counts against MaxBytes, including
// the part of a long line that is not kept.
//
//dsh:test DH-T-RIPGREP-3
func TestGrepBytesCountsConsumed(t *testing.T) {
	src := tree(t, map[string]string{"big": strings.Repeat("x", 3<<20) + "\n"})
	sum, err := Grep(context.Background(), GrepQuery{From: src, Pattern: "y", Limits: Limits{MaxBytes: 2 << 20}},
		func(Match) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if sum.Reason != CutBytes {
		t.Fatalf("summary %+v, want %q", sum, CutBytes)
	}
}

// recording is a Source that records every directory it lists.
type recording struct {
	Source
	mu   sync.Mutex
	dirs []string
}

func (r *recording) ReadDir(name string) ([]fs.DirEntry, error) {
	r.mu.Lock()
	r.dirs = append(r.dirs, name)
	r.mu.Unlock()
	return r.Source.ReadDir(name)
}

func (r *recording) Open(name string) (io.ReadCloser, error) { return r.Source.Open(name) }

// TestPatternDirectedWalk: a walk for src/**/*.ts never lists directories no match
// can lie under, so unrelated files cannot exhaust MaxFiles first.
//
//dsh:test DH-T-FS-7
func TestPatternDirectedWalk(t *testing.T) {
	files := map[string]string{"src/a.ts": "x\n", "src/deep/b.ts": "x\n", "src/c.go": "x\n"}
	for i := range 20 {
		files[fmt.Sprintf("aaa/%02d/f.ts", i)] = "x\n"
	}
	rec := &recording{Source: tree(t, files)}
	got, sum, err := List(context.Background(), FilesQuery{From: rec, Pattern: "src/**/*.ts", Limits: Limits{MaxFiles: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"src/a.ts", "src/deep/b.ts"}; !reflect.DeepEqual(got, want) || sum.Incomplete {
		t.Fatalf("got %v %+v, want %v complete", got, sum, want)
	}
	for _, d := range rec.dirs {
		if d != "." && d != "src" && !strings.HasPrefix(d, "src/") {
			t.Errorf("listed unrelated directory %q", d)
		}
	}

	rec.dirs = nil
	var lines []Match
	if _, err := Grep(context.Background(), GrepQuery{From: rec, Pattern: "x", Include: "src/*.go"},
		func(m Match) bool { lines = append(lines, m); return true }); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Path != "src/c.go" {
		t.Fatalf("grep got %+v", lines)
	}
	if !slices.Equal(rec.dirs, []string{".", "src"}) {
		t.Errorf("grep listed %v", rec.dirs)
	}
}

func TestMayContain(t *testing.T) {
	for _, tc := range []struct {
		pattern, dir string
		want         bool
	}{
		{"src/**/*.ts", "src", true},
		{"src/**/*.ts", "src/a/b", true},
		{"src/**/*.ts", "docs", false},
		{"src/*.ts", "src/a", false},
		{"*/x/*.go", "q/x", true},
		{"*/x/*.go", "q/y", false},
		{"{a,b/c}/*.go", "z", true}, // a brace with a slash disables pruning
	} {
		g, err := newGlob("glob", tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		f := g.mayContain(".")
		if got := f == nil || f(tc.dir); got != tc.want {
			t.Errorf("%s under %s: %v, want %v", tc.pattern, tc.dir, got, tc.want)
		}
	}
}

// TestOffsetPages: concatenated pages equal the full ordered result, for listing
// and for each content-search output.
//
//dsh:test DH-T-FS-7
func TestOffsetPages(t *testing.T) {
	files := map[string]string{}
	for i := range 7 {
		files[fmt.Sprintf("d%d/f%d.txt", i%3, i)] = strings.Repeat("hit\n", i%2+1)
	}
	src := tree(t, files)
	ctx := context.Background()

	full, _, err := List(ctx, FilesQuery{From: src})
	if err != nil {
		t.Fatal(err)
	}
	for _, order := range []Order{ByPath, ByTime} {
		full, _, _ := List(ctx, FilesQuery{From: src, Order: order})
		var paged []string
		for off := 0; ; off += 3 {
			page, sum, err := List(ctx, FilesQuery{From: src, Order: order, Offset: off, Limits: Limits{MaxMatches: 3}})
			if err != nil {
				t.Fatal(err)
			}
			paged = append(paged, page...)
			if !sum.Incomplete {
				break
			}
		}
		if !reflect.DeepEqual(paged, full) {
			t.Errorf("order %d: pages %v, full %v", order, paged, full)
		}
	}
	if len(full) != 7 {
		t.Fatalf("full listing %v", full)
	}

	for _, out := range []Output{OutputLines, OutputFiles, OutputCounts} {
		all := grepAll(t, GrepQuery{From: src, Pattern: "hit", Output: out})
		var paged []Match
		for off := 0; ; off += 2 {
			var page []Match
			sum, err := Grep(ctx, GrepQuery{From: src, Pattern: "hit", Output: out, Offset: off, Limits: Limits{MaxMatches: 2}},
				func(m Match) bool { page = append(page, m); return true })
			if err != nil {
				t.Fatal(err)
			}
			paged = append(paged, page...)
			if !sum.Incomplete {
				break
			}
		}
		if !reflect.DeepEqual(paged, all) {
			t.Errorf("output %d: pages %+v\nfull %+v", out, paged, all)
		}
	}
}

// TestGrepOutputs: files and counts modes give one result per matching file in walk
// order, and MaxMatches bounds files, not lines.
//
//dsh:test DH-T-FS-7
func TestGrepOutputs(t *testing.T) {
	src := tree(t, map[string]string{"a": "hit\nhit\nhit\n", "b": "none\n", "c": "hit\n"})
	files := grepAll(t, GrepQuery{From: src, Pattern: "hit", Output: OutputFiles})
	if want := []Match{{Path: "a", Count: 1}, {Path: "c", Count: 1}}; !reflect.DeepEqual(files, want) {
		t.Errorf("files %+v", files)
	}
	counts := grepAll(t, GrepQuery{From: src, Pattern: "hit", Output: OutputCounts, Limits: Limits{MaxMatches: 2}})
	if want := []Match{{Path: "a", Count: 3}, {Path: "c", Count: 1}}; !reflect.DeepEqual(counts, want) {
		t.Errorf("counts %+v", counts)
	}
	var got []Match
	sum, _ := Grep(context.Background(), GrepQuery{From: src, Pattern: "hit", Output: OutputCounts, Limits: Limits{MaxMatches: 1}},
		func(m Match) bool { got = append(got, m); return true })
	if len(got) != 1 || got[0].Count != 3 || sum.Reason != CutMatches {
		t.Errorf("capped counts %+v %+v", got, sum)
	}
}

// TestSampleSpreads: over the cap, the sample is split across the children of each
// directory at every depth, so an early sibling cannot take it all.
//
//dsh:test DH-T-FS-7
func TestSampleSpreads(t *testing.T) {
	files := map[string]string{"z.txt": "x"}
	for i := range 30 {
		files[fmt.Sprintf("a/x/x%02d.txt", i)] = "x"
		files[fmt.Sprintf("a/y/y%02d.txt", i)] = "x"
		files[fmt.Sprintf("c/c%02d.txt", i)] = "x"
	}
	for i := range 2 {
		files[fmt.Sprintf("b/b%d.txt", i)] = "x"
	}
	src := tree(t, files)
	got, sum, err := List(context.Background(), FilesQuery{From: src, Sample: true, Limits: Limits{MaxMatches: 11}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 11 || !sum.Incomplete || sum.Reason != CutMatches {
		t.Fatalf("got %v %+v", got, sum)
	}
	count := func(prefix string) int {
		n := 0
		for _, p := range got {
			if strings.HasPrefix(p, prefix) {
				n++
			}
		}
		return n
	}
	// Top: a, b, c and z.txt share 11; b and z.txt need 3, so a and c get 4 each,
	// and inside a, x and y get 2 each.
	if count("b/") != 2 || count("z.txt") != 1 || count("c/") != 4 || count("a/x/") != 2 || count("a/y/") != 2 {
		t.Errorf("sample not spread: %v", got)
	}
	if !slices.IsSorted(got) {
		t.Errorf("sample not in walk order: %v", got)
	}
	all, _, _ := List(context.Background(), FilesQuery{From: src, Sample: true, Limits: Limits{MaxMatches: 100}})
	if len(all) != 93 {
		t.Errorf("under the cap the sample is everything: %d", len(all))
	}
}

// TestMatcher: the exported matcher answers like the listing, without walking.
func TestMatcher(t *testing.T) {
	m := NewMatcher(tree(t, fixture))
	for _, tc := range []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"build", true, true},
		{"build/sub/x.txt", false, true},
		{"src/build/gen.go", false, true},
		{"src/app.log", false, true},
		{"src/keep.log", false, false},
		{"docs/draft1.md", false, true},
		{"docs/guide.md", false, false},
		{"secret.txt", false, true},
		{".git/config", false, true},
		{`src\main.go`, false, false},
		{"README.md", false, false},
	} {
		if got := m.Ignored(tc.path, tc.isDir); got != tc.want {
			t.Errorf("Ignored(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// stalled is a Source whose file reads block until released, then fail as an
// interrupted read would.
type stalled struct {
	Source
	opened  chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *stalled) Open(name string) (io.ReadCloser, error) {
	f, err := s.Source.Open(name)
	if err != nil {
		return nil, err
	}
	s.once.Do(func() { close(s.opened) })
	return stalledFile{f, s.release}, nil
}

type stalledFile struct {
	io.ReadCloser
	release chan struct{}
}

func (f stalledFile) Read([]byte) (int, error) {
	<-f.release
	return 0, errors.New("read interrupted")
}

// TestGrepCancelAfterWalk: the walk has finished and only a scan is still running
// when the caller cancels. The search is aborted, not complete.
//
//dsh:test DH-T-RIPGREP-3
func TestGrepCancelAfterWalk(t *testing.T) {
	src := &stalled{Source: tree(t, map[string]string{"a.txt": "needle\n"}), opened: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	type out struct {
		sum Summary
		err error
	}
	done := make(chan out, 1)
	go func() {
		sum, err := Local{Workers: 1}.Grep(ctx, GrepQuery{From: src, Pattern: "needle"}, func(Match) bool { return true })
		done <- out{sum, err}
	}()
	t.Cleanup(func() { cancel(); close(src.release); <-done })
	<-src.opened
	// One file: once it is being scanned the walk has nothing left to list.
	<-time.After(50 * time.Millisecond)
	cancel()
	src.release <- struct{}{}
	got := <-done
	done <- got
	var e *Error
	if !errors.As(got.err, &e) || e.Reason() != ReasonAborted || !errors.Is(got.err, context.Canceled) {
		t.Errorf("error %v, want aborted", got.err)
	}
	if !got.sum.Incomplete || got.sum.Reason != CutAborted {
		t.Errorf("summary %+v, want aborted", got.sum)
	}
}

// lagging is a Source that holds the open of first until every other file has
// been opened (or a deadline passes), so later files are scanned before it.
type lagging struct {
	Source
	first  string
	others int
	mu     sync.Mutex
	opened int
	go_    chan struct{}
}

func (l *lagging) Open(name string) (io.ReadCloser, error) {
	if name == l.first {
		select {
		case <-l.go_:
		case <-time.After(2 * time.Second):
		}
	} else {
		l.mu.Lock()
		l.opened++
		if l.opened == l.others {
			close(l.go_)
		}
		l.mu.Unlock()
	}
	return l.Source.Open(name)
}

// TestGrepBytesDeterministic: the byte budget is spent in walk order, so a tight
// MaxBytes returns the same matches however the workers are scheduled, even when
// the first file is the last to be read.
//
//dsh:test DH-T-RIPGREP-3, DH-T-RIPGREP-5
func TestGrepBytesDeterministic(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		files[name] = strings.Repeat("hay\n", 15) + strings.Repeat("needle "+name+"\n", 5) // 110 bytes, needles in the last 50
	}
	fixed := tree(t, files)
	for _, output := range []Output{OutputLines, OutputCounts, OutputFiles} {
		q := GrepQuery{Pattern: "needle", Output: output, Limits: Limits{MaxBytes: 250}}
		q.From = fixed
		var want []Match
		wantSum, err := Local{Workers: 1}.Grep(context.Background(), q, func(m Match) bool { want = append(want, m); return true })
		if err != nil {
			t.Fatal(err)
		}
		if wantSum.Reason != CutBytes || len(want) == 0 || want[0].Path != "a" {
			t.Fatalf("output %v: sequential %+v %+v", output, wantSum, want)
		}
		for i := range 10 {
			q.From = &lagging{Source: fixed, first: "a", others: 4, go_: make(chan struct{})}
			var got []Match
			sum, err := Local{Workers: 4}.Grep(context.Background(), q, func(m Match) bool { got = append(got, m); return true })
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) || sum != wantSum {
				t.Fatalf("output %v run %d: got %+v %+v\nwant %+v %+v", output, i, sum, got, wantSum, want)
			}
		}
	}
}

// TestIgnoreFileFailures: an ignore file that cannot be read, or is over the
// size cap with a rule past the cap, never lets the paths it excludes through;
// the listing is Incomplete and the Matcher reports why.
//
//dsh:test DH-T-RIPGREP-7
func TestIgnoreFileFailures(t *testing.T) {
	files := map[string]string{
		"keep.txt":      "k",
		"build/out.txt": "o",
		"sub/a.txt":     "a",
	}
	big := maps.Clone(files)
	big["sub/.gitignore"] = strings.Repeat("# padding\n", maxIgnoreSize/10+1) + "a.txt\n"
	for _, tc := range []struct {
		name   string
		src    Source
		want   []string
		reason error
	}{
		{"unreadable root", failing{tree(t, withIgnore(files)), map[string]bool{".gitignore": true}}, nil, fs.ErrPermission},
		{"oversized nested", tree(t, withIgnore(big)), []string{"keep.txt"}, ErrIgnoreTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, sum, err := List(context.Background(), FilesQuery{From: tc.src})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("listed %q, want %q", got, tc.want)
			}
			if !sum.Incomplete || sum.Reason != CutIgnoreFile {
				t.Errorf("summary %+v, want incomplete %q", sum, CutIgnoreFile)
			}
			m := NewMatcher(tc.src)
			ignored, err := m.Check("sub/a.txt", false)
			if !ignored || !errors.Is(err, tc.reason) {
				t.Errorf("Check(sub/a.txt) = %v, %v; want ignored, %v", ignored, err, tc.reason)
			}
			if !m.Ignored("sub/a.txt", false) {
				t.Error("Ignored(sub/a.txt) = false under an unreadable ignore file")
			}
		})
	}
}

func withIgnore(files map[string]string) map[string]string {
	out := maps.Clone(files)
	out[".gitignore"] = "build/\n"
	return out
}
