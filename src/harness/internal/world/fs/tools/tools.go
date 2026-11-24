// Package tools is fs.tools (C35, DH-D-FS-6, docs/harness/areas/filesystem.md
// sec. 6.2 "Tools exposed to the model"): the six file tools read, write, edit,
// apply_patch, glob and grep, each declared from a Go argument struct and
// registered in tools.Service. Every file access goes through the operation
// service (world/fs/ops), so every mutation is fenced by the session's policy
// and every read and write is observed; no tool imports a provider.
//
// Results are bounded by the tools themselves (each declares BoundsOwnOutput,
// so the pipeline's spill seat leaves them alone): read stops at
// Config.ReadLimit lines, ReadMaxLineLength characters a line and ReadMaxBytes
// bytes; glob returns GlobMaxResults paths and grep GrepMaxMatches matches,
// each within SearchMaxBytes and with an offset to page on. When a spill
// store is given and a default read, or a search page, did not hold the whole
// result, the complete result (the whole file; every path; every match, up to
// SpillMaxMatches) is saved there (storage/spill): the footer cites it and the
// result carries the reference as tools.Content.Spill, so the pipeline logs it
// in the tool/result and the session store tracks the citation. read, glob and
// grep declare overlapping reads on the path they name; write, edit and
// apply_patch declare writes on every path they change. grep runs util/search
// in process: no external program (rg) is started.
//
// Text is UTF-8 only (impl.md sec. 9.1, the unconfirmed default "text tools
// refuse every encoding but UTF-8"): checkText in text.go is the one place read
// and edit decide what text is.
package tools

import (
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
)

// Name is the component's catalogue name.
const Name = "fs.tools"

// The model-facing tool names (upstream's).
const (
	ReadName       = "read"
	WriteName      = "write"
	EditName       = "edit"
	ApplyPatchName = "apply_patch"
	GlobName       = "glob"
	GrepName       = "grep"
)

// Defaults of Config (filesystem.md sec. 6.1 tools.Config, and the tool table).
const (
	DefaultReadLimit         = 2000
	DefaultReadMaxLineLength = 2000
	DefaultReadMaxBytes      = 51200
	DefaultTextMaxBytes      = 32 << 20
	DefaultGlobMaxResults    = 100
	DefaultGrepMaxMatches    = 250
	DefaultGrepMaxLineBytes  = 2000
	DefaultSpillMaxMatches   = 100_000
	DefaultSearchMaxBytes    = 51200
)

// Config configures the tools; a zero bound is its default.
type Config struct {
	// ApplyPatch registers apply_patch; nil is true (DH-ADR-35: on by default).
	ApplyPatch *bool `json:"applyPatch,omitempty"`
	// ReadLimit is the most lines one read returns.
	ReadLimit int `json:"readLimit,omitempty"`
	// ReadMaxLineLength is the most characters of one line a read shows.
	ReadMaxLineLength int `json:"readMaxLineLength,omitempty"`
	// ReadMaxBytes is the most bytes of numbered lines one read returns.
	ReadMaxBytes int `json:"readMaxBytes,omitempty"`
	// TextMaxBytes is the largest file a text tool reads, edits or writes.
	TextMaxBytes int `json:"textMaxBytes,omitempty"`
	// GlobMaxResults is the most paths one glob returns.
	GlobMaxResults int `json:"globMaxResults,omitempty"`
	// GrepMaxMatches is the most matches one grep returns.
	GrepMaxMatches int `json:"grepMaxMatches,omitempty"`
	// GrepMaxLineBytes is the most bytes of one matching line grep shows.
	GrepMaxLineBytes int `json:"grepMaxLineBytes,omitempty"`
	// SpillMaxMatches is the most matches the complete list grep saves to the
	// spill store holds, when its page did not hold them all.
	SpillMaxMatches int `json:"spillMaxMatches,omitempty"`
	// SearchMaxBytes is the most bytes of listing one glob or grep page shows;
	// the page then holds fewer than its count of results.
	SearchMaxBytes int `json:"searchMaxBytes,omitempty"`
}

func or(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func (c Config) normalized() Config {
	c.ReadLimit = or(c.ReadLimit, DefaultReadLimit)
	c.ReadMaxLineLength = or(c.ReadMaxLineLength, DefaultReadMaxLineLength)
	c.ReadMaxBytes = or(c.ReadMaxBytes, DefaultReadMaxBytes)
	c.TextMaxBytes = or(c.TextMaxBytes, DefaultTextMaxBytes)
	c.GlobMaxResults = or(c.GlobMaxResults, DefaultGlobMaxResults)
	c.GrepMaxMatches = or(c.GrepMaxMatches, DefaultGrepMaxMatches)
	c.GrepMaxLineBytes = or(c.GrepMaxLineBytes, DefaultGrepMaxLineBytes)
	c.SpillMaxMatches = max(or(c.SpillMaxMatches, DefaultSpillMaxMatches), c.GrepMaxMatches)
	c.SearchMaxBytes = or(c.SearchMaxBytes, DefaultSearchMaxBytes)
	return c
}

// files is what every tool body shares: the operation service, the optional
// spill store and the normalized configuration.
type files struct {
	ops   *ops.Service
	spill spill.Store
	cfg   Config
}

// Declarations are the enabled tools over o; store may be nil (read then has
// no spill reference to offer).
func Declarations(o *ops.Service, store spill.Store, cfg Config) ([]*htools.Declaration, error) {
	if o == nil {
		return nil, errors.New("fs.tools: the operation service is required")
	}
	f := &files{ops: o, spill: store, cfg: cfg.normalized()}
	ds := []*htools.Declaration{f.read(), f.write(), f.edit()}
	if cfg.ApplyPatch == nil || *cfg.ApplyPatch {
		ds = append(ds, f.applyPatch())
	}
	ds = append(ds, f.glob(), f.grep())
	for _, d := range ds {
		if err := d.Err(); err != nil {
			return nil, err
		}
	}
	return ds, nil
}

// Mount registers the enabled tools in svc for ctx's scope.
func Mount(ctx *cordis.Context, svc htools.Service, o *ops.Service, store spill.Store, cfg Config) (*cordis.EffectHandle, error) {
	ds, err := Declarations(o, store, cfg)
	if err != nil {
		return nil, err
	}
	return svc.RegisterSet(ctx, ds)
}

// Component is fs.tools: it injects the operation service and the tool
// registry. No spill store is provided under a service key yet, so the
// component mounts read without one; a host that has a store calls Mount.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	o, err := ops.Key.Get(ctx)
	if err != nil {
		return err
	}
	svc, err := htools.Key.Get(ctx)
	if err != nil {
		return err
	}
	_, err = Mount(ctx, svc, o, nil, cfg)
	return err
}, cordis.WithInject(ops.Key.Name(), htools.Key.Name()))

// Item is the fs.tools catalogue item.
func Item() catalog.Item {
	return catalog.Item{Name: Name, Component: Component, Config: reflect.TypeFor[Config]()}
}

// call is the operation-service call of one tool call.
func call(run htools.Run) ops.Call {
	return ops.Call{Session: run.Agent.Session, CallID: run.CallID}
}

// pathKey is the overlap key of a path argument: the cleaned path, compared
// case-insensitively on Windows. A relative and an absolute name of one file
// are two keys: the pipeline classifies a call from its arguments alone.
func pathKey(p string) string {
	if p == "" {
		p = "."
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return "fs:" + p
}

// kindOr is err's reason, or def when it has none.
func kindOr(err error, def herr.Reason) herr.Reason {
	if k := herr.Kind(err); k != "" {
		return k
	}
	return def
}

// version is the model-facing form of a file version: the content digest when
// the provider has one, else its metadata token.
func version(v fs.Version) string {
	if v.HasDigest {
		return "sha256:" + hex.EncodeToString(v.Digest[:])
	}
	if v.Meta != "" {
		return "meta:" + v.Meta
	}
	return "unknown"
}
