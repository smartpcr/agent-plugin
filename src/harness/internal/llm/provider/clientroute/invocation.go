package clientroute

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// AttemptPlaceholder stands for the attempt directory in a logged argument
// list, so the record is the same on every host and holds no profile path.
const AttemptPlaceholder = "{attempt}"

// Digest is "sha256:<hex>" of b.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ArgsDigest is the Digest of the JSON encoding of args (HTML characters not
// escaped), so an argument list rebuilt from the log digests the same.
func ArgsDigest(args []string) string {
	if args == nil {
		args = []string{}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(args) // a []string always encodes
	return Digest(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
}

// Placeholder is args with every occurrence of dir written as
// AttemptPlaceholder; ExpandPlaceholder is its inverse.
func Placeholder(args []string, dir string) []string {
	out := slices.Clone(args)
	if dir == "" {
		return out
	}
	for i, a := range out {
		out[i] = strings.ReplaceAll(a, dir, AttemptPlaceholder)
	}
	return out
}

// ExpandPlaceholder writes dir for AttemptPlaceholder in each argument.
func ExpandPlaceholder(args []string, dir string) []string {
	out := slices.Clone(args)
	for i, a := range out {
		out[i] = strings.ReplaceAll(a, AttemptPlaceholder, dir)
	}
	return out
}

// NewSessionID is a random version-4 UUID: the client session id the harness
// chooses for one attempt and logs.
func NewSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported systems
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Invocation describes one started attempt; NewInvocation turns it into the
// record the attempt settles.
type Invocation struct {
	Client  string
	Version Version
	// Program is the started program's path; the record keeps its base name.
	Program string
	// Args follow the program, as started; Dir is the attempt directory they
	// name, written as AttemptPlaceholder in the record.
	Args []string
	Dir  string
	// Stdin is what is written to standard input.
	Stdin []byte
	// Files are the attempt files, from AttemptDir.Files.
	Files []event.FileDigest
	// Env is the child environment; the record keeps its names only.
	Env       subprocess.Env
	SessionID string
}

// NewInvocation is the record of inv: the base name of the program, the
// argument list with the attempt directory as AttemptPlaceholder, its digest,
// the digest of standard input, the file digests and the environment names.
func NewInvocation(inv Invocation) event.ClientInvocation {
	args := Placeholder(inv.Args, inv.Dir)
	return event.ClientInvocation{
		Client:      inv.Client,
		Version:     inv.Version.String(),
		Program:     filepath.Base(inv.Program),
		Args:        args,
		ArgsDigest:  ArgsDigest(args),
		StdinDigest: Digest(inv.Stdin),
		Files:       slices.Clone(inv.Files),
		Env:         Names(inv.Env),
		SessionID:   inv.SessionID,
	}
}

// Recorder holds one attempt's invocation record while the attempt runs; an
// adapter's Call embeds it to implement llm.InvocationReporter.
type Recorder struct {
	mu  sync.Mutex
	rec *event.ClientInvocation
}

// Set records inv as the attempt's invocation.
func (r *Recorder) Set(inv event.ClientInvocation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := clone(inv)
	r.rec = &c
}

// Update changes the record in place; it does nothing before Set.
func (r *Recorder) Update(f func(*event.ClientInvocation)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rec != nil {
		f(r.rec)
	}
}

// Invocation is a copy of the record, nil before Set.
func (r *Recorder) Invocation() *event.ClientInvocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rec == nil {
		return nil
	}
	c := clone(*r.rec)
	return &c
}

func clone(inv event.ClientInvocation) event.ClientInvocation {
	inv.Args = slices.Clone(inv.Args)
	inv.Files = slices.Clone(inv.Files)
	inv.Env = slices.Clone(inv.Env)
	return inv
}

// AttemptDir is an attempt's private working directory: created empty, so the
// client finds no project file in it, holding only the files the adapter
// writes, and removed when the attempt ends.
type AttemptDir struct {
	path  string
	files []event.FileDigest
}

// NewAttemptDir creates an empty directory under parent (os.TempDir() when
// empty), readable by the user only.
func NewAttemptDir(parent string) (*AttemptDir, error) {
	if parent == "" {
		parent = os.TempDir()
	}
	dir, err := os.MkdirTemp(parent, "dsh-client-")
	if err != nil {
		return nil, fmt.Errorf("clientroute: the attempt directory: %w", err)
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &AttemptDir{path: dir}, nil
}

// Path is the directory.
func (d *AttemptDir) Path() string { return d.path }

// Join is the path of name inside the directory.
func (d *AttemptDir) Join(name string) string { return filepath.Join(d.path, name) }

// Write creates name, which must be a plain file name, with data and records
// its digest; it returns the file's path.
func (d *AttemptDir) Write(name string, data []byte) (string, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("clientroute: %q is not a plain file name", name)
	}
	p := d.Join(name)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("clientroute: write %s: %w", name, err)
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", fmt.Errorf("clientroute: write %s: %w", name, werr)
	}
	d.files = append(d.files, event.FileDigest{Name: name, Digest: Digest(data)})
	return p, nil
}

// Files are the digests of the files written, in writing order.
func (d *AttemptDir) Files() []event.FileDigest { return slices.Clone(d.files) }

// removeTries bounds how long Remove retries: a Windows process that was just
// stopped can hold a handle in the directory for a moment.
const removeTries = 20

// Remove deletes the directory and everything in it.
func (d *AttemptDir) Remove() error {
	var err error
	for i := range removeTries {
		if err = os.RemoveAll(d.path); err == nil {
			return nil
		}
		if i < removeTries-1 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return errors.Join(errors.New("clientroute: the attempt directory was not removed"), err)
}
