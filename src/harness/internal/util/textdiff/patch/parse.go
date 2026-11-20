package patch

import "strings"

// Markers of the format.
const (
	BeginPatch = "*** Begin Patch"
	EndPatch   = "*** End Patch"
	AddFile    = "*** Add File: "
	DeleteFile = "*** Delete File: "
	UpdateFile = "*** Update File: "
	MoveTo     = "*** Move to: "
	EndOfFile  = "*** End of File"
)

// Op is what a hunk does to its file.
type Op int

const (
	OpAdd Op = iota
	OpDelete
	OpUpdate
)

// Hunk is one file operation of a patch.
type Hunk struct {
	Op   Op
	Path string
	// MoveTo is the new path of an update that renames, or "".
	MoveTo string
	// Contents is the new file of an add: every "+" line with "\n".
	Contents string
	// Chunks are the changes of an update.
	Chunks []Chunk
	// Line is the 1-based patch line of the hunk's header.
	Line int
}

// Chunk is one change of an update hunk.
type Chunk struct {
	// Context, when HasContext, is the "@@ <context>" line: the chunk's old lines
	// are sought after the first line equal to it.
	Context    string
	HasContext bool
	// Old lines are replaced by New lines.
	Old, New []string
	// EOF: the chunk ended with "*** End of File", so its old lines are sought at
	// the end of the file only.
	EOF bool
	// Line is the 1-based patch line that opened the chunk.
	Line int
}

// Patch is a parsed patch.
type Patch struct {
	Hunks []Hunk
}

type state int

const (
	sStart       state = iota // before "*** Begin Patch"
	sBody                     // expecting a hunk header or "*** End Patch"
	sAdd                      // in an add hunk's "+" lines
	sUpdateHead               // just after "*** Update File:", "*** Move to:" may follow
	sChunks                   // between chunks of an update hunk
	sChunkOpened              // after an "@@" line, before the chunk's first line
	sChunkBody                // in a chunk's lines
	sDone                     // after "*** End Patch"
)

// Parser is the line-fed parser: Feed each line of the patch text in order, then
// call End. Feeding line by line is equivalent to Parse on the whole text. A Parser
// is not safe for concurrent use.
type Parser struct {
	state   state
	line    int
	heredoc bool // a heredoc wrapper opened the text
	closed  bool // the heredoc wrapper was closed
	hunks   []Hunk
	chunk   *Chunk
	inChunk int // lines in the open chunk
	err     *Error
}

// Feed takes the next line of the patch, without its "\n". After the first error
// every Feed and End return that error.
func (p *Parser) Feed(line string) error {
	if p.err != nil {
		return p.err
	}
	p.line++
	if err := p.step(strings.TrimSuffix(line, "\r")); err != nil {
		p.err = err
		return err
	}
	return nil
}

func isHeredocOpen(t string) bool {
	return t == "<<EOF" || t == "<<'EOF'" || t == `<<"EOF"`
}

func (p *Parser) step(line string) *Error {
	t := strings.TrimSpace(line)
	switch p.state {
	case sStart:
		switch {
		case t == "":
		case t == BeginPatch:
			p.state = sBody
		case isHeredocOpen(t) && !p.heredoc:
			p.heredoc = true
		default:
			return syntaxError(p.line, "The first line of the patch must be '%s'", BeginPatch)
		}
		return nil

	case sBody:
		return p.header(t)

	case sAdd:
		if rest, ok := strings.CutPrefix(line, "+"); ok {
			h := &p.hunks[len(p.hunks)-1]
			h.Contents += rest + "\n"
			return nil
		}
		return p.header(t)

	case sUpdateHead:
		p.state = sChunks
		if rest, ok := strings.CutPrefix(t, MoveTo); ok {
			p.hunks[len(p.hunks)-1].MoveTo = rest
			return nil
		}
		return p.step(line)

	case sChunks:
		h := &p.hunks[len(p.hunks)-1]
		switch {
		case t == "":
			return nil
		case strings.HasPrefix(line, "***"):
			if len(h.Chunks) == 0 {
				return syntaxError(h.Line, "Update file hunk for path '%s' is empty", h.Path)
			}
			return p.header(t)
		case line == "@@":
			p.open(Chunk{Line: p.line})
			p.state = sChunkOpened
			return nil
		case strings.HasPrefix(line, "@@ "):
			p.open(Chunk{Context: line[3:], HasContext: true, Line: p.line})
			p.state = sChunkOpened
			return nil
		case len(h.Chunks) > 0:
			return syntaxError(p.line, "Expected update hunk to start with a @@ context marker, got: '%s'", line)
		}
		// The first chunk may omit its "@@" line.
		p.open(Chunk{Line: p.line})
		p.state = sChunkBody
		return p.body(line, t)

	case sChunkOpened:
		p.state = sChunkBody
		return p.body(line, t)

	case sChunkBody:
		return p.body(line, t)

	case sDone:
		switch {
		case t == "":
		case p.heredoc && !p.closed && strings.HasSuffix(t, "EOF"):
			p.closed = true
		default:
			return syntaxError(p.line, "The last line of the patch must be '%s'", EndPatch)
		}
		return nil
	}
	return nil
}

// header handles a line that must be a hunk header or the end of the patch.
func (p *Parser) header(t string) *Error {
	if path, ok := strings.CutPrefix(t, AddFile); ok {
		p.hunks = append(p.hunks, Hunk{Op: OpAdd, Path: path, Line: p.line})
		p.state = sAdd
		return nil
	}
	if path, ok := strings.CutPrefix(t, DeleteFile); ok {
		p.hunks = append(p.hunks, Hunk{Op: OpDelete, Path: path, Line: p.line})
		p.state = sBody
		return nil
	}
	if path, ok := strings.CutPrefix(t, UpdateFile); ok {
		p.hunks = append(p.hunks, Hunk{Op: OpUpdate, Path: path, Line: p.line})
		p.state = sUpdateHead
		return nil
	}
	if t == EndPatch {
		p.state = sDone
		return nil
	}
	return syntaxError(p.line, "'%s' is not a valid hunk header. Valid hunk headers: '%s{path}', '%s{path}', '%s{path}'",
		t, AddFile, DeleteFile, UpdateFile)
}

func (p *Parser) open(c Chunk) {
	h := &p.hunks[len(p.hunks)-1]
	h.Chunks = append(h.Chunks, c)
	p.chunk = &h.Chunks[len(h.Chunks)-1]
	p.inChunk = 0
}

// body handles a line of an open chunk.
func (p *Parser) body(line, t string) *Error {
	c := p.chunk
	if line == EndOfFile {
		if p.inChunk == 0 {
			return syntaxError(p.line, "Update hunk does not contain any lines")
		}
		c.EOF = true
		p.state = sChunks
		return nil
	}
	if line == "" {
		c.Old = append(c.Old, "")
		c.New = append(c.New, "")
		p.inChunk++
		return nil
	}
	switch line[0] {
	case ' ':
		c.Old = append(c.Old, line[1:])
		c.New = append(c.New, line[1:])
	case '-':
		c.Old = append(c.Old, line[1:])
	case '+':
		c.New = append(c.New, line[1:])
	default:
		if p.inChunk == 0 {
			if t == EndPatch {
				return syntaxError(p.line, "Update hunk does not contain any lines")
			}
			return syntaxError(p.line, "Unexpected line found in update hunk: '%s'. Every line should start with ' ' (context line), '+' (added line), or '-' (removed line)", line)
		}
		p.state = sChunks
		return p.step(line)
	}
	p.inChunk++
	return nil
}

// End finishes the patch and returns it.
func (p *Parser) End() (*Patch, error) {
	if p.err != nil {
		return nil, p.err
	}
	switch {
	case p.state == sStart:
		// Only blank lines were fed: report the last of them.
		p.err = syntaxError(max(p.line, 1), "The first line of the patch must be '%s'", BeginPatch)
	case p.state != sDone:
		p.err = syntaxError(p.line+1, "The last line of the patch must be '%s'", EndPatch)
	case p.heredoc && !p.closed:
		p.err = syntaxError(p.line+1, "The heredoc wrapper of the patch is not closed with EOF")
	}
	if p.err != nil {
		return nil, p.err
	}
	return &Patch{Hunks: p.hunks}, nil
}

// Parse parses a whole patch text.
func Parse(text string) (*Patch, error) {
	var p Parser
	text = strings.TrimSuffix(text, "\n")
	for line := range strings.SplitSeq(text, "\n") {
		if err := p.Feed(line); err != nil {
			return nil, err
		}
	}
	return p.End()
}
