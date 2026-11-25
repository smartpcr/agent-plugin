package instructions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// Config is instructions.Config (context-compaction-skill.md sec. 6.2,
// UP-CFG-dsh-agent-instructions), project chain only. Zero fields take the
// defaults of Default.
type Config struct {
	// MaxBytes is the byte budget of the rendered section; at least MinMaxBytes.
	MaxBytes int `json:"maxBytes,omitempty"`
	// MaxSourceBytes bounds the read of one file.
	MaxSourceBytes int `json:"maxSourceBytes,omitempty"`
	// MaxTotalSourceBytes bounds the reads of one discovery.
	MaxTotalSourceBytes int `json:"maxTotalSourceBytes,omitempty"`
	// Candidates are the base names looked for in each directory, in order.
	Candidates []string `json:"candidates,omitempty"`
	// LocalCandidates are the local overlays, looked for after Candidates.
	LocalCandidates []string `json:"localCandidates,omitempty"`
}

// The defaults and the floor.
const (
	MinMaxBytes                = 65536
	DefaultMaxSourceBytes      = 1 << 20
	DefaultMaxTotalSourceBytes = 4 << 20
)

// Default is the configuration with every default.
func Default() Config {
	return Config{
		MaxBytes: MinMaxBytes, MaxSourceBytes: DefaultMaxSourceBytes, MaxTotalSourceBytes: DefaultMaxTotalSourceBytes,
		Candidates: []string{"AGENTS.md", "CLAUDE.md"},
	}
}

func (c Config) withDefaults() Config {
	d := Default()
	if c.MaxBytes <= 0 {
		c.MaxBytes = d.MaxBytes
	}
	if c.MaxSourceBytes <= 0 {
		c.MaxSourceBytes = d.MaxSourceBytes
	}
	if c.MaxTotalSourceBytes <= 0 {
		c.MaxTotalSourceBytes = d.MaxTotalSourceBytes
	}
	if len(c.Candidates) == 0 {
		c.Candidates = d.Candidates
	}
	return c
}

// Validate checks the floor and that every candidate is a plain base name.
func (c Config) Validate() error {
	c = c.withDefaults()
	if c.MaxBytes < MinMaxBytes {
		return fmt.Errorf("instructions: maxBytes %d is below the floor %d", c.MaxBytes, MinMaxBytes)
	}
	for _, n := range append(append([]string(nil), c.Candidates...), c.LocalCandidates...) {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
			return fmt.Errorf("instructions: candidate %q is not a base name", n)
		}
	}
	return nil
}

// State is what became of one candidate file.
type State string

// The states of a tracked file.
const (
	Visible    State = "visible"
	Omitted    State = "omitted"
	Duplicate  State = "duplicate"
	Unreadable State = "unreadable"
)

// File is one tracked instruction file of the fold.
type File struct {
	// Path is relative to the project root, with forward slashes.
	Path string `json:"path"`
	// Digest is the hex SHA-256 of the bytes read.
	Digest string `json:"digest,omitempty"`
	// Bytes is how many bytes were read.
	Bytes int `json:"bytes"`
	// Truncated: the read stopped at a limit, or the section cut the file.
	Truncated bool        `json:"truncated,omitempty"`
	State     State       `json:"state"`
	Reason    herr.Reason `json:"reason,omitempty"`
}

// Refusal is a candidate the fence refused; diagnosed, never rendered.
type Refusal struct {
	Path   string      `json:"path"`
	Reason herr.Reason `json:"reason"`
}

// Entry is one file a discovery found, with its text.
type Entry struct {
	File File
	Text string
	// limited: the read stopped at a source limit.
	limited bool
}

// Observation is what one discovery saw.
type Observation struct {
	// Root is the project root as given.
	Root    string
	Entries []Entry
	Refused []Refusal
	// Candidates are the absolute candidate paths looked at, in chain order:
	// the paths a watch covers.
	Candidates []string
}

// Discover reads the project chain of dir under root through fsys.
func Discover(ctx context.Context, fsys fs.FS, dir, root string, c Config) (Observation, error) {
	c = c.withDefaults()
	if fsys == nil {
		return Observation{}, fmt.Errorf("instructions: no filesystem seam")
	}
	if !filepath.IsAbs(dir) || !filepath.IsAbs(root) {
		return Observation{}, herr.Newf(fs.CodeInvalidPath, "instructions: %q and %q must be absolute", dir, root)
	}
	dir, root = filepath.Clean(dir), filepath.Clean(root)
	if !fs.Within(root, dir) {
		return Observation{}, herr.Newf(fs.CodeInvalidPath, "instructions: %s is not below the project root %s", dir, root)
	}
	ri, err := fsys.Stat(ctx, fs.Ref{Path: root})
	if err != nil {
		return Observation{}, err
	}
	if !ri.Exists {
		return Observation{}, herr.Newf(fs.CodeNotFound, "instructions: the project root %s does not exist", root)
	}
	canonRoot := ri.Target.Display
	obs := Observation{Root: root}
	rel, _ := filepath.Rel(root, dir)
	dirs := []string{root}
	if rel != "." {
		cur := root
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			dirs = append(dirs, cur)
		}
	}
	names := append(append([]string(nil), c.Candidates...), c.LocalCandidates...)
	total, seen := 0, map[string]bool{}
	for _, d := range dirs {
		for _, n := range names {
			p := filepath.Join(d, n)
			obs.Candidates = append(obs.Candidates, p)
			shown := filepath.ToSlash(must(filepath.Rel(root, p)))
			e, refused, ok := readOne(ctx, fsys, p, canonRoot, c, &total)
			if refused != "" {
				obs.Refused = append(obs.Refused, Refusal{Path: shown, Reason: refused})
			}
			if !ok {
				continue
			}
			e.File.Path = shown
			if e.File.State == Visible {
				if seen[e.File.Digest] {
					e.File.State, e.Text = Duplicate, ""
				}
				seen[e.File.Digest] = true
			}
			obs.Entries = append(obs.Entries, e)
		}
	}
	return obs, nil
}

func must(s string, _ error) string { return s }

// readOne reads candidate p. ok is false for an absent or refused file.
func readOne(ctx context.Context, fsys fs.FS, p, canonRoot string, c Config, total *int) (e Entry, refused herr.Reason, ok bool) {
	in, err := fsys.Stat(ctx, fs.Ref{Path: p})
	if err != nil {
		return Entry{File: File{State: Unreadable, Reason: herr.Kind(err)}}, "", true
	}
	if !in.Exists {
		return Entry{}, "", false
	}
	if !fs.Within(canonRoot, in.Target.Display) {
		return Entry{}, fs.CodeSandboxDenied, false
	}
	if in.Type != fs.TypeFile {
		return Entry{File: File{State: Unreadable, Reason: fs.CodeNotRegularFile}}, "", true
	}
	left := c.MaxTotalSourceBytes - *total
	if left <= 0 {
		return Entry{File: File{State: Omitted, Reason: fs.CodeTooLarge}}, "", true
	}
	rc, oi, err := fsys.Open(ctx, fs.Ref{Path: p})
	if err != nil {
		return Entry{File: File{State: Unreadable, Reason: herr.Kind(err)}}, "", true
	}
	defer rc.Close()
	// The target may have moved between Stat and Open: fence what was opened.
	if !fs.Within(canonRoot, oi.Target.Display) {
		return Entry{}, fs.CodeSandboxDenied, false
	}
	limit := min(c.MaxSourceBytes, left)
	data, err := io.ReadAll(io.LimitReader(rc, int64(limit)+1))
	if err != nil {
		return Entry{File: File{State: Unreadable, Reason: herr.Kind(err)}}, "", true
	}
	limited := len(data) > limit
	if limited {
		data = data[:limit]
	}
	*total += len(data)
	sum := sha256.Sum256(data)
	return Entry{
		File:    File{Digest: hex.EncodeToString(sum[:]), Bytes: len(data), Truncated: limited, State: Visible},
		Text:    string(data),
		limited: limited,
	}, "", true
}
