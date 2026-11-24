// Package closure decides whether a stage of a story may close (C57; M2-C-2): the
// two-step protocol of "harness-gate ready" and "harness-gate close" over the stage's
// evidence record docs/stories/<story>/evidence/<stage>.md and the acceptance index.
//
// An evidence record is markdown in the impl.md sec. 7.3 form. The gate reads only
// these parts of it; everything else is prose for the reviewer:
//
//	Closure status: open                     (exactly one line: open, closed, or closed at <sha>)
//
//	## Steps
//	| Step | Deliverable | Commit | Notes |
//	## Scenarios
//	| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
//	## Manual checks register
//	| Step | Check | Seen | Operator | Commit | Label |
//	## Owner defaults (M2-C-4)
//	| Default | Raised in | Confirmed | By | Commit |
//
// Each section holds one markdown table; its columns are found by header name, so a
// record may add columns. A section that is absent reads as an empty table.
package closure

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// The section headings of the tables the gate reads.
const (
	StepsHeading         = "## Steps"
	ScenariosHeading     = "## Scenarios"
	ManualChecksHeading  = "## Manual checks register"
	OwnerDefaultsHeading = "## Owner defaults (M2-C-4)"
	statusPrefix         = "Closure status:"
)

// Status is the value of a record's status line.
type Status int

const (
	StatusMissing Status = iota // no status line
	StatusOpen                  // "Closure status: open"
	StatusClosed                // "Closure status: closed" or "closed at <sha>"
	StatusInvalid               // any other value, or more than one status line
)

func (s Status) String() string {
	switch s {
	case StatusOpen:
		return "open"
	case StatusClosed:
		return "closed"
	case StatusInvalid:
		return "invalid"
	}
	return "missing"
}

// Row is one table row, keyed by lower-cased column header.
type Row struct {
	Line  int
	Cells map[string]string
}

// Get returns the cell of column name (case-insensitive) with surrounding spaces and
// one pair of enclosing backticks removed.
func (r Row) Get(name string) string {
	v := strings.TrimSpace(r.Cells[strings.ToLower(name)])
	if len(v) >= 2 && strings.HasPrefix(v, "`") && strings.HasSuffix(v, "`") && strings.Count(v, "`") == 2 {
		v = strings.TrimSpace(v[1 : len(v)-1])
	}
	return v
}

// Table is one parsed markdown table under a heading.
type Table struct {
	Present bool     // the heading exists
	Columns []string // lower-cased headers, in order
	Rows    []Row
}

// Has reports whether the table has column name.
func (t Table) Has(name string) bool {
	name = strings.ToLower(name)
	for _, c := range t.Columns {
		if c == name {
			return true
		}
	}
	return false
}

// Record is a parsed evidence record.
type Record struct {
	Path        string // as reported in messages
	Status      Status
	StatusValue string // the raw value after "Closure status:"
	StatusLine  int
	Headings    []string // every markdown heading outside code fences, trimmed, in order

	Steps         Table
	Scenarios     Table
	ManualChecks  Table
	OwnerDefaults Table

	// Extra holds the tables of the additional headings ParseDocument was asked for.
	Extra map[string]*Table
}

var closedAt = regexp.MustCompile(`^closed at [0-9a-f]{7,40}$`)

// ParseRecord reads an evidence record. It fails only on a read error or a table it
// cannot read (a section given twice, a row with more cells than its header); every
// content problem is a blocker of Ready or Close, never a parse error.
func ParseRecord(path string, r io.Reader) (*Record, error) {
	return ParseDocument(path, r)
}

// ParseDocument is ParseRecord that also reads the table under each of the extra
// headings into Record.Extra (the closure record's tables).
func ParseDocument(path string, r io.Reader, extra ...string) (*Record, error) {
	rec := &Record{Path: path, Extra: map[string]*Table{}}
	tables := map[string]*Table{
		StepsHeading:         &rec.Steps,
		ScenariosHeading:     &rec.Scenarios,
		ManualChecksHeading:  &rec.ManualChecks,
		OwnerDefaultsHeading: &rec.OwnerDefaults,
	}
	for _, h := range extra {
		t := &Table{}
		rec.Extra[h] = t
		tables[h] = t
	}
	var (
		cur     *Table // the table section being read, nil outside one
		header  bool   // cur's header row is read
		sep     bool   // cur's separator row is read
		done    bool   // cur's table has ended
		inFence bool
		lineNo  int
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		lineNo++
		line := strings.TrimRight(sc.Text(), " \t\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(line, "#") {
			rec.Headings = append(rec.Headings, trimmed)
			cur, header, sep, done = nil, false, false, false
			if t, ok := tables[trimmed]; ok {
				if t.Present {
					return nil, fmt.Errorf("%s:%d: section %q given twice", path, lineNo, trimmed)
				}
				t.Present = true
				cur = t
			}
			continue
		}
		if v, ok := strings.CutPrefix(line, statusPrefix); ok {
			v = strings.TrimSpace(v)
			if rec.StatusLine != 0 {
				rec.Status = StatusInvalid
				rec.StatusValue = fmt.Sprintf("a second status line at line %d (the first is at line %d)", lineNo, rec.StatusLine)
				continue
			}
			rec.StatusLine, rec.StatusValue = lineNo, v
			switch {
			case v == "open":
				rec.Status = StatusOpen
			case v == "closed" || closedAt.MatchString(v):
				rec.Status = StatusClosed
			default:
				rec.Status = StatusInvalid
			}
			continue
		}
		if cur == nil || done {
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			if header {
				done = true // the table ended; prose after it is not read
			}
			continue
		}
		cells := splitRow(trimmed)
		switch {
		case !header:
			for _, c := range cells {
				cur.Columns = append(cur.Columns, strings.ToLower(strings.TrimSpace(c)))
			}
			header = true
		case !sep:
			sep = true
		default:
			if len(cells) > len(cur.Columns) {
				return nil, fmt.Errorf("%s:%d: row has %d cells, its table %d columns", path, lineNo, len(cells), len(cur.Columns))
			}
			row := Row{Line: lineNo, Cells: map[string]string{}}
			for i, c := range cells {
				row.Cells[cur.Columns[i]] = strings.TrimSpace(c)
			}
			cur.Rows = append(cur.Rows, row)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rec, nil
}

// splitRow splits a markdown table row "| a | b |" into its cells; "\|" is a literal
// bar inside a cell.
func splitRow(line string) []string {
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var (
		cells []string
		b     strings.Builder
	)
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			b.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, b.String())
			b.Reset()
		default:
			b.WriteByte(line[i])
		}
	}
	return append(cells, b.String())
}
