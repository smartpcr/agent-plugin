package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
)

// CoverageFloor is the minimum share of statements, in percent, that every package
// must cover (QG-3, DH-ADR-14). There is no exemption list.
const CoverageFloor = 85

// Block is one block of a text coverage profile ("go tool covdata textfmt" or
// "-coverprofile" output).
type Block struct {
	File                                 string // import path of the file, e.g. example.com/m/p/p.go
	StartLine, StartCol, EndLine, EndCol int
	Stmts                                int
	Count                                int64
}

type blockKey struct {
	file                                 string
	startLine, startCol, endLine, endCol int
}

func (b Block) key() blockKey {
	return blockKey{b.File, b.StartLine, b.StartCol, b.EndLine, b.EndCol}
}

// Profile is a parsed text coverage profile.
type Profile struct {
	Mode   string
	Blocks []Block
}

// ParseProfile reads a text coverage profile:
//
//	mode: set
//	example.com/m/p/p.go:3.24,5.2 1 1
func ParseProfile(r io.Reader) (*Profile, error) {
	p := &Profile{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if mode, ok := strings.CutPrefix(line, "mode:"); ok {
			p.Mode = strings.TrimSpace(mode)
			continue
		}
		b, err := parseBlock(line)
		if err != nil {
			return nil, fmt.Errorf("coverage profile line %d: %w", n, err)
		}
		p.Blocks = append(p.Blocks, b)
	}
	return p, scanner.Err()
}

func parseBlock(line string) (Block, error) {
	// file:startLine.startCol,endLine.endCol stmts count; the file may hold ':'.
	colon := strings.LastIndex(line, ":")
	if colon < 0 {
		return Block{}, fmt.Errorf("no file in %q", line)
	}
	fields := strings.Fields(line[colon+1:])
	if len(fields) != 3 {
		return Block{}, fmt.Errorf("want range, statements and count in %q", line)
	}
	b := Block{File: line[:colon]}
	start, end, ok := strings.Cut(fields[0], ",")
	if !ok {
		return Block{}, fmt.Errorf("bad range in %q", line)
	}
	var err error
	if b.StartLine, b.StartCol, err = parsePos(start); err != nil {
		return Block{}, err
	}
	if b.EndLine, b.EndCol, err = parsePos(end); err != nil {
		return Block{}, err
	}
	if b.Stmts, err = strconv.Atoi(fields[1]); err != nil {
		return Block{}, fmt.Errorf("bad statement count in %q", line)
	}
	if b.Count, err = strconv.ParseInt(fields[2], 10, 64); err != nil {
		return Block{}, fmt.Errorf("bad hit count in %q", line)
	}
	return b, nil
}

func parsePos(s string) (int, int, error) {
	l, c, ok := strings.Cut(s, ".")
	if !ok {
		return 0, 0, fmt.Errorf("bad position %q", s)
	}
	line, err1 := strconv.Atoi(l)
	col, err2 := strconv.Atoi(c)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("bad position %q", s)
	}
	return line, col, nil
}

// Merge combines profiles of the same code taken in different processes (the test
// process and every re-executed child): a block is covered when any process
// covered it, and its count is the sum of theirs.
func Merge(profiles ...*Profile) *Profile {
	merged := &Profile{}
	index := map[blockKey]int{}
	for _, p := range profiles {
		if p == nil {
			continue
		}
		if merged.Mode == "" {
			merged.Mode = p.Mode
		}
		for _, b := range p.Blocks {
			if i, ok := index[b.key()]; ok {
				merged.Blocks[i].Count += b.Count
				continue
			}
			index[b.key()] = len(merged.Blocks)
			merged.Blocks = append(merged.Blocks, b)
		}
	}
	return merged
}

// PackageCoverage is the statement coverage of one package.
type PackageCoverage struct {
	Package        string
	Covered, Total int
}

// Percent is the covered share of statements, in percent.
func (c PackageCoverage) Percent() float64 {
	if c.Total == 0 {
		return 100
	}
	return 100 * float64(c.Covered) / float64(c.Total)
}

// Coverage merges profiles and returns the statement coverage of every package
// they mention, sorted by package path.
func Coverage(profiles ...*Profile) []PackageCoverage {
	byPkg := map[string]*PackageCoverage{}
	for _, b := range Merge(profiles...).Blocks {
		pkg := path.Dir(b.File)
		c := byPkg[pkg]
		if c == nil {
			c = &PackageCoverage{Package: pkg}
			byPkg[pkg] = c
		}
		c.Total += b.Stmts
		if b.Count > 0 {
			c.Covered += b.Stmts
		}
	}
	var out []PackageCoverage
	for _, c := range byPkg {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b PackageCoverage) int { return strings.Compare(a.Package, b.Package) })
	return out
}

// CheckFloor fails every package whose statement coverage is under floor percent,
// naming it as "<package>: 70% < 85% of statements". A package with no statements
// has nothing to cover and passes. No package is exempt (QG-2, QG-3).
func CheckFloor(coverage []PackageCoverage, floor int) error {
	var errs []error
	for _, c := range coverage {
		if c.Total == 0 || c.Covered*100 >= floor*c.Total {
			continue
		}
		errs = append(errs, fmt.Errorf("%s: %s%% < %d%% of statements (%d of %d covered)",
			c.Package, formatPercent(c.Percent()), floor, c.Covered, c.Total))
	}
	return errors.Join(errs...)
}

// formatPercent rounds down to one decimal, so a package just under the floor is
// never shown as reaching it.
func formatPercent(p float64) string {
	return strconv.FormatFloat(math.Floor(p*10)/10, 'f', -1, 64)
}
