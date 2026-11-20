package search

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"regexp/syntax"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// GrepQuery is a content search.
type GrepQuery struct {
	From     Source
	Root     string // directory or file in From; "" is the whole view
	Pattern  string // Go RE2 syntax, matched against each line
	Include  string // optional doublestar glob, matched against the path from Root (the base name when it has no slash)
	Hidden   bool   // include names starting with a dot
	NoIgnore bool   // do not apply ignore files
	Output   Output // matching lines (default), matching files or per-file counts
	// Offset skips the first Offset results (lines, files or counts, as Output
	// says). Results come in a stable order, so successive offsets page through
	// one result; MaxMatches bounds each page.
	Offset int
	Limits Limits
}

// Output is what a content search reports.
type Output int

const (
	OutputLines  Output = iota // one Match per matching line
	OutputFiles                // one Match per file with a match; Line and Text are empty, Count is 1
	OutputCounts               // one Match per file with a match; Count is its number of matching lines
)

// Match is one result of a content search.
type Match struct {
	Path      string // from the Source's root; opens with Source.Open
	Line      int    // 1-based; 0 unless Output is OutputLines
	Text      string // the line without its terminator; invalid UTF-8 replaced by U+FFFD
	Truncated bool   // Text was cut at MaxLineBytes
	Count     int    // OutputFiles: 1; OutputCounts: the file's matching lines; OutputLines: 0
}

const (
	sniffBytes  = 8000    // the prefix Git inspects for a NUL byte
	maxScanLine = 1 << 20 // a longer line is matched on its first MiB only
	ctxEvery    = 4096    // lines between cancellation checks
)

// Grep emits each matching line of the files q selects.
func Grep(ctx context.Context, q GrepQuery, emit func(Match) bool) (Summary, error) {
	return Local{}.Grep(ctx, q, emit)
}

// Grep emits each matching line, in walk order then line order. Binary files
// (a NUL byte in the first 8,000) and unreadable files are skipped; an unreadable
// file sets Incomplete.
func (l Local) Grep(ctx context.Context, q GrepQuery, emit func(Match) bool) (Summary, error) {
	var sum Summary
	re, err := regexp.Compile(q.Pattern)
	if err != nil {
		return sum, invalidPattern("regular expression", q.Pattern, err)
	}
	include, err := newGlob("include glob", q.Include)
	if err != nil {
		return sum, err
	}
	lim := grepLimits{
		matches:   pick(q.Limits.MaxMatches, DefaultMaxMatches),
		lineBytes: pick(q.Limits.MaxLineBytes, DefaultMaxLineBytes),
		bytes:     pick(q.Limits.MaxBytes, int64(DefaultMaxBytes)),
	}
	workers := l.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s := &scanner{re: re, literal: requiredLiteral(q.Pattern), lim: lim, src: q.From, ctx: ctx, output: q.Output}
	if q.Output != OutputLines {
		// The per-file line cap is for line output only.
		s.lim.matches = 0
	} else if s.lim.matches > 0 {
		s.lim.matches += max(q.Offset, 0)
	}
	jobs := make(chan *job)
	ordered := make(chan *job, 2*workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for j := range jobs {
				j.res <- s.scan(j.path)
			}
		})
	}

	root := clean(q.Root)
	var walkErr error
	var walkSum Summary
	w := &walker{ctx: ctx, src: q.From, sum: &walkSum, opt: walkOptions{
		hidden: q.Hidden, noIgnore: q.NoIgnore,
		maxFiles: pick(q.Limits.MaxFiles, DefaultMaxFiles),
		enter:    include.mayContain(root),
	}}
	walked := make(chan struct{})
	go func() {
		defer close(walked)
		defer close(ordered)
		defer close(jobs)
		walkErr = w.walk(root, func(f file) bool {
			if !include.match(relTo(root, f.path)) {
				return true
			}
			j := &job{path: f.path, res: make(chan result, 1)}
			select {
			case ordered <- j:
			case <-ctx.Done():
				return false
			}
			select {
			case jobs <- j:
				return true
			case <-ctx.Done():
				j.res <- result{}
				return false
			}
		})
	}()

	stopped := ""
	skip := max(q.Offset, 0)
	long := false
	for j := range ordered {
		r := <-j.res
		if stopped != "" {
			continue
		}
		if r.unreadable {
			sum.cut(CutUnreadable)
		}
		long = long || r.longLine
		if r.overBytes {
			stopped = CutBytes
		}
		for _, m := range r.matches {
			if skip > 0 {
				skip--
				continue
			}
			if sum.Matches >= lim.matches && lim.matches > 0 {
				stopped = CutMatches
				break
			}
			sum.Matches++
			if !emit(m) {
				stopped = CutStopped
				break
			}
		}
		if stopped == "" && r.more && lim.matches > 0 && sum.Matches >= lim.matches {
			stopped = CutMatches
		}
		if stopped != "" {
			cancel()
		}
	}
	<-walked
	wg.Wait()

	sum.Files = walkSum.Files
	if walkSum.Incomplete {
		sum.cut(walkSum.Reason)
	}
	if long {
		sum.cut(CutLongLine)
	}
	if stopped != "" {
		sum.Incomplete, sum.Reason = true, stopped
		if parent.Err() == nil {
			// The walk ended because a limit cancelled it, not the caller.
			walkErr = nil
		}
	}
	return sum, walkErr
}

type grepLimits struct {
	matches, lineBytes int
	bytes              int64
}

type job struct {
	path string
	res  chan result
}

type result struct {
	matches    []Match
	more       bool // the file had matches beyond the per-file cap
	unreadable bool
	overBytes  bool
	longLine   bool // a line over maxScanLine was not searched
}

// scanner holds what every worker shares.
type scanner struct {
	re      *regexp.Regexp
	literal []byte
	lim     grepLimits
	src     Source
	ctx     context.Context
	output  Output
	read    atomic.Int64
}

func (s *scanner) scan(path string) result {
	var r result
	if s.ctx.Err() != nil {
		return r
	}
	f, err := s.src.Open(path)
	if err != nil {
		r.unreadable = true
		return r
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64<<10)
	head, err := br.Peek(sniffBytes)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		r.unreadable = true
		return r
	}
	if fsx.IsBinarySniff(head) {
		return r
	}
	var line []byte
	for n := 1; ; n++ {
		if n%ctxEvery == 0 && s.ctx.Err() != nil {
			return r
		}
		ln, err := s.readLine(br, line[:0])
		line = ln.text
		switch {
		case err != nil && s.ctx.Err() != nil:
			return r
		case err != nil:
			r.unreadable = true
			return r
		case ln.overBytes:
			r.overBytes = true
			return r
		case ln.eof && ln.consumed == 0:
			// Nothing follows the last terminator: there is no line here.
			return r
		}
		if ln.long {
			// Only a prefix was kept; matching it could miss a needle past the cap
			// or match an end anchor falsely, so the line is not searched.
			r.longLine = true
		} else if s.wanted(bytes.TrimSuffix(line, []byte("\r"))) {
			if r.add(s, path, n, bytes.TrimSuffix(line, []byte("\r"))) {
				return r
			}
		}
		if ln.eof {
			return r
		}
	}
}

func (s *scanner) wanted(line []byte) bool {
	return (s.literal == nil || bytes.Contains(line, s.literal)) && s.re.Match(line)
}

// add records a matching line in the shape the output mode asks for; it reports
// whether the file needs no further reading.
func (r *result) add(s *scanner, path string, n int, line []byte) bool {
	switch s.output {
	case OutputFiles:
		r.matches = append(r.matches, Match{Path: path, Count: 1})
		return true
	case OutputCounts:
		if len(r.matches) == 0 {
			r.matches = append(r.matches, Match{Path: path})
		}
		r.matches[0].Count++
		return false
	}
	if s.lim.matches > 0 && len(r.matches) >= s.lim.matches {
		r.more = true
		return true
	}
	r.matches = append(r.matches, s.match(path, n, line))
	return false
}

// lineRead is one line as readLine saw it.
type lineRead struct {
	text      []byte // at most maxScanLine bytes of the line, without "\n"
	consumed  int64  // bytes read from the file for this line, terminator included
	long      bool   // the line was longer than maxScanLine; text is a prefix
	eof       bool   // no line follows
	overBytes bool   // reading this line exceeded MaxBytes
}

// readLine appends the next line to buf. Every byte read counts against MaxBytes
// as it is read, including the part of a long line that is not kept, and the
// context is checked between chunks so a huge line cannot pin a worker.
func (s *scanner) readLine(br *bufio.Reader, buf []byte) (lineRead, error) {
	var ln lineRead
	for {
		chunk, err := br.ReadSlice('\n')
		ln.consumed += int64(len(chunk))
		if s.lim.bytes > 0 && s.read.Add(int64(len(chunk))) > s.lim.bytes {
			ln.overBytes = true
			ln.text = buf
			return ln, nil
		}
		if room := maxScanLine - len(buf); room > 0 {
			buf = append(buf, chunk[:min(len(chunk), room)]...)
		}
		switch {
		case err == nil:
			ln.text = bytes.TrimSuffix(buf, []byte("\n"))
			ln.long = ln.consumed-1 > maxScanLine
			return ln, nil
		case errors.Is(err, bufio.ErrBufferFull):
			if cerr := s.ctx.Err(); cerr != nil {
				return ln, cerr
			}
			continue
		case errors.Is(err, io.EOF):
			ln.text, ln.eof = buf, true
			ln.long = ln.consumed > maxScanLine
			return ln, nil
		default:
			ln.text, ln.eof = buf, true
			return ln, err
		}
	}
}

func (s *scanner) match(path string, n int, line []byte) Match {
	m := Match{Path: path, Line: n}
	if s.lim.lineBytes > 0 && len(line) > s.lim.lineBytes {
		cut := s.lim.lineBytes
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		line, m.Truncated = line[:cut], true
	}
	m.Text = strings.ToValidUTF8(string(line), "\uFFFD")
	return m
}

// requiredLiteral is a byte string every match of pattern contains, or nil: the
// longest case-sensitive literal among the top-level pieces of the pattern.
func requiredLiteral(pattern string) []byte {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil
	}
	re = re.Simplify()
	pieces := []*syntax.Regexp{re}
	if re.Op == syntax.OpConcat {
		pieces = re.Sub
	}
	var best []rune
	for _, p := range pieces {
		if p.Op == syntax.OpLiteral && p.Flags&syntax.FoldCase == 0 && len(p.Rune) > len(best) {
			best = p.Rune
		}
	}
	if len(best) == 0 {
		return nil
	}
	return []byte(string(best))
}
