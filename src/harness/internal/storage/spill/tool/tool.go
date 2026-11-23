// Package tool is the spill_read tool (DH-D-STORAGE-6, DH-ADR-STORAGE-5,
// docs/harness/areas/storage.md sec. 6.2 "Tools exposed to the model"): it
// reads a bounded slice of a spilled result back, by offset and limit or by a
// pattern, through spill.Store.Open, which lets only the owning session and its
// forks read the file. Its result is bounded by the tool itself
// (BoundsOwnOutput), so it is never spilled again.
//
// It is mounted only where the agent's file tools cannot read the spill
// directory: a filesystem provider that reports it can map the directory
// (Mapper, from DH-S-12) keeps it out of the prompt. With no provider it is
// mounted.
package tool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// Name is the model-facing name.
const Name = "spill_read"

// Bounds of a read.
const (
	// DefaultMaxBytes is the most one result holds when Config.MaxBytes is zero.
	DefaultMaxBytes = 32 << 10
	// MinMaxBytes is the least Config.MaxBytes may be: room for one match line.
	MinMaxBytes = 256
	// MaxPatternBytes bounds a pattern.
	MaxPatternBytes = 1024
	// MaxLineBytes is how much of one line a pattern is matched against; the
	// rest of a longer line is skipped.
	MaxLineBytes = 64 << 10
	// MaxShownLineBytes is how much of one matching line a result shows; the
	// model reads the rest by offset.
	MaxShownLineBytes = 512
)

// In is the tool's input.
type In struct {
	// Ref is the saved file's id, as the notice cites it.
	Ref string `json:"ref" dsh:"required"`
	// Offset is the byte offset to read or search from; 0 is the start.
	Offset int64 `json:"offset,omitempty" dsh:"min=0"`
	// Limit is the most bytes the result holds; 0, or more than the tool
	// allows, is the tool's most.
	Limit int `json:"limit,omitempty" dsh:"min=0"`
	// Pattern is a regular expression (RE2 syntax); with it the result is the
	// matching lines from Offset on, each as "<byte offset>:<line>".
	Pattern string `json:"pattern,omitempty"`
}

// Config configures the tool.
type Config struct {
	// MaxBytes is the most one result holds; zero is DefaultMaxBytes. Mount it
	// within the spill policy's budget.
	MaxBytes int
}

func (c Config) normalized() (Config, error) {
	if c.MaxBytes == 0 {
		c.MaxBytes = DefaultMaxBytes
	}
	if c.MaxBytes < MinMaxBytes {
		return c, fmt.Errorf("spill_read: MaxBytes %d is below the minimum of %d", c.MaxBytes, MinMaxBytes)
	}
	return c, nil
}

// Mapper is the filesystem provider's one question (DH-S-12): whether the
// agent's file tools can read hostDir, the spill directory on this machine.
type Mapper interface {
	CanMap(hostDir string) bool
}

// Declare is the tool's declaration over store.
func Declare(store spill.Store, cfg Config) (*tools.Declaration, error) {
	if store == nil {
		return nil, errors.New("spill_read: a spill store is required")
	}
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	return tools.Define(tools.Spec[In, string]{
		Name: Name,
		Description: "Read a saved tool result that was too large to show in full. Give the ref the notice cites. " +
			"With offset and limit, the result is the text from that byte offset, at most limit bytes (" +
			strconv.Itoa(cfg.MaxBytes) + " at most); call again from offset plus its length to go on. " +
			"With a pattern (a regular expression), the result is the matching lines from offset on, each as \"<byte offset>:<line>\".",
		Run: func(ctx context.Context, in In, run tools.Run) (string, error) {
			return Read(ctx, store, string(run.Agent.Session), in, cfg.MaxBytes)
		},
		Overlap:         func(In) tools.Overlap { return tools.Free },
		BoundsOwnOutput: true,
		Effects:         []tools.Effect{tools.EffectNone},
	}), nil
}

// Mount registers the tool in svc for ctx's scope, unless m reports that the
// agent's file tools can read the spill directory; mounted reports which. With
// a nil m, or a store whose files have no host path, the tool is mounted.
func Mount(ctx *cordis.Context, svc tools.Service, store spill.Store, m Mapper, cfg Config) (h *cordis.EffectHandle, mounted bool, err error) {
	d, err := Declare(store, cfg)
	if err != nil {
		return nil, false, err
	}
	if dir, ok := Dir(store); ok && m != nil && m.CanMap(dir) {
		return nil, false, nil
	}
	h, err = svc.Register(ctx, d)
	if err != nil {
		return nil, false, err
	}
	return h, true, nil
}

// Dir is the spill directory on this machine: the directory two levels above
// any file's host path (an id is "<owner dir>/<file>"); ok is false when the
// store has no host paths.
func Dir(store spill.Store) (string, bool) {
	hp, ok := store.HostPath(store.Reserve("spill-read-probe", spill.DefaultName).ID)
	if !ok {
		return "", false
	}
	return filepath.Dir(filepath.Dir(hp)), true
}

// Read is the tool's body: a slice of the file ref names, read for caller, of at
// most min(in.Limit, maxBytes) bytes. Without a pattern it is the bytes from
// in.Offset, its ends moved inward to rune boundaries; with one, the matching
// lines from in.Offset on.
func Read(ctx context.Context, store spill.Store, caller string, in In, maxBytes int) (string, error) {
	if in.Offset < 0 {
		return "", fmt.Errorf("spill_read: offset %d is negative", in.Offset)
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	maxBytes = max(maxBytes, MinMaxBytes)
	limit := maxBytes
	if in.Limit > 0 && in.Limit < limit {
		limit = in.Limit
	}
	var re *regexp.Regexp
	if in.Pattern != "" {
		if len(in.Pattern) > MaxPatternBytes {
			return "", fmt.Errorf("spill_read: the pattern is longer than %d bytes", MaxPatternBytes)
		}
		var err error
		if re, err = regexp.Compile(in.Pattern); err != nil {
			return "", fmt.Errorf("spill_read: the pattern: %w", err)
		}
	}
	if caller == "" {
		return "", errors.New("spill_read: the call has no session")
	}
	rc, err := store.Open(ctx, caller, in.Ref)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	size, err := rc.Seek(0, io.SeekEnd)
	if err != nil {
		return "", fmt.Errorf("spill_read: %w", err)
	}
	if in.Offset > size {
		return "", fmt.Errorf("spill_read: offset %d is past the end of the %d-byte file", in.Offset, size)
	}
	if _, err := rc.Seek(in.Offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("spill_read: %w", err)
	}
	if re != nil {
		// A search result has room for at least one cut match line.
		return grep(ctx, rc, in.Offset, size, re, max(limit, MinMaxBytes))
	}
	return slice(rc, in.Offset, limit)
}

// slice reads up to limit bytes, without splitting a rune at either end: a
// start inside a rune moves to the next rune, an end inside one to its start.
func slice(r io.Reader, offset int64, limit int) (string, error) {
	buf := make([]byte, limit+utf8.UTFMax)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("spill_read: %w", err)
	}
	data := buf[:n]
	start := 0
	for offset > 0 && start < utf8.UTFMax-1 && start < len(data) && !utf8.RuneStart(data[start]) {
		start++
	}
	end := min(limit, len(data))
	if end < len(data) {
		for e := end; e > start && e > end-utf8.UTFMax; e-- {
			if utf8.RuneStart(data[e]) {
				end = e
				break
			}
		}
	}
	return string(data[start:max(start, end)]), nil
}

// grep is the lines from offset on that re matches, as "<offset>:<line>\n", at
// most limit bytes of a file of size bytes; when more matches are left a last
// line says where to go on. A first match too long for limit is cut to fit, so
// a search always moves forward.
func grep(ctx context.Context, r io.Reader, offset, size int64, re *regexp.Regexp, limit int) (string, error) {
	br := bufio.NewReaderSize(r, MaxLineBytes)
	var out strings.Builder
	pos := offset
	// Room is kept for the longest "more" line this file can need.
	reserve := len(moreLine(size))
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		lineStart := pos
		line, err := br.ReadSlice('\n')
		pos += int64(len(line))
		long := errors.Is(err, bufio.ErrBufferFull)
		if err != nil && !long && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("spill_read: %w", err)
		}
		text := strings.TrimSuffix(string(line), "\n")
		if long {
			// Match the first MaxLineBytes only; skip the rest of the line.
			for errors.Is(err, bufio.ErrBufferFull) {
				var more []byte
				more, err = br.ReadSlice('\n')
				pos += int64(len(more))
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return "", fmt.Errorf("spill_read: %w", err)
			}
		}
		if len(line) > 0 && re.MatchString(text) {
			entry := entryOf(lineStart, text, MaxShownLineBytes)
			if room := limit - out.Len() - reserve; len(entry) > room {
				if out.Len() > 0 {
					out.WriteString(moreLine(lineStart))
					return out.String(), nil
				}
				entry = entryOf(lineStart, text, room-len(entryOf(lineStart, "", 0))-len("..."))
			}
			out.WriteString(entry)
		}
		if err != nil {
			break
		}
	}
	if out.Len() == 0 {
		return fmt.Sprintf("(no line from offset %d matches)", offset), nil
	}
	return out.String(), nil
}

// entryOf is one match line, its text cut to at most shown bytes at a rune
// boundary and marked "...".
func entryOf(at int64, text string, shown int) string {
	if len(text) > shown {
		cut := max(shown, 0)
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "..."
	}
	return strconv.FormatInt(at, 10) + ":" + text + "\n"
}

// moreLine says the search stopped at a match it had no room for.
func moreLine(at int64) string {
	return fmt.Sprintf("[more matches: search again from offset %d]\n", at)
}
