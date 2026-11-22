package approval

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// ScopeKind is what a session grant covers.
type ScopeKind string

// The scope kinds.
const (
	ScopeDirectory   ScopeKind = "directory"
	ScopeApplication ScopeKind = "application"
)

// Subject is what a call needs, offered for a grant and matched against grants:
// one directory (Path), or one application (Provider and App both). A process
// id is never a subject.
type Subject struct {
	Kind     ScopeKind `json:"kind"`
	Path     string    `json:"path,omitempty"`
	Provider string    `json:"provider,omitempty"`
	App      string    `json:"app,omitempty"`
	PID      int       `json:"pid,omitempty"`
}

// canonical is s normalised for matching, or an error when s cannot be granted.
func (s Subject) canonical() (Subject, error) {
	switch s.Kind {
	case ScopeDirectory:
		if s.Path == "" {
			return Subject{}, errors.New("approval: a directory subject names its path")
		}
		return Subject{Kind: ScopeDirectory, Path: canonicalPath(s.Path)}, nil
	case ScopeApplication:
		if s.PID != 0 {
			return Subject{}, errors.New("approval: a process id is not a subject")
		}
		if s.Provider == "" || s.App == "" {
			return Subject{}, errors.New("approval: an application subject names its provider and its app")
		}
		return Subject{Kind: ScopeApplication, Provider: s.Provider, App: s.App}, nil
	}
	return Subject{}, fmt.Errorf("approval: scope kind %q is not directory or application", s.Kind)
}

// canonicalPath is p absolute, clean and with links resolved as far as it
// exists, so a link that points out of a granted directory does not match it.
func canonicalPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	rest := ""
	for cur := abs; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// within reports whether path is dir or inside it; a prefix sibling (/w2 for
// /w) is not inside.
func within(dir, path string) bool {
	if samePath(dir, path) {
		return true
	}
	prefix := dir
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	return len(path) > len(prefix) && samePath(path[:len(prefix)], prefix)
}

// matches reports whether grant subject g covers the canonical subject s.
func (g Subject) matches(s Subject) bool {
	if g.Kind != s.Kind {
		return false
	}
	switch g.Kind {
	case ScopeDirectory:
		return within(g.Path, s.Path)
	case ScopeApplication:
		return g.Provider == s.Provider && g.App == s.App
	}
	return false
}

// Grant is one live session grant.
type Grant struct {
	ID      string    `json:"id"`
	Agent   agent.Ref `json:"-"`
	Subject Subject   `json:"subject"`
	By      By        `json:"by"`
	// Tool and Call are the request the grant was given on.
	Tool string `json:"tool,omitempty"`
	Call string `json:"call,omitempty"`
}

// Why a grant ended.
const (
	EndRevoked       = "revoked"
	EndSessionEnded  = "session-ended"
	EndHostStopped   = "host-stopped"
	EndPolicyChanged = "policy-changed"
)

// GrantCreated is grant/created.
type GrantCreated struct {
	ID      string  `json:"id"`
	Agent   string  `json:"agent"`
	Subject Subject `json:"subject"`
	By      By      `json:"by"`
	Tool    string  `json:"tool,omitempty"`
	Call    string  `json:"call,omitempty"`
}

// GrantEnded is grant/ended.
type GrantEnded struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	By     string `json:"by,omitempty"`
}

// The grant events: log-only, for the display fold and the audit. A resume never
// reads them back into the table (NG-8).
var (
	GrantCreatedType = event.MustDefine(event.Default(), event.Spec[GrantCreated]{Name: "grant/created", Version: 1,
		Validate: func(g GrantCreated) error {
			if g.ID == "" || g.Agent == "" || g.By.Principal == "" {
				return errors.New("grant/created: id, agent and principal are required")
			}
			_, err := g.Subject.canonical()
			return err
		}})
	GrantEndedType = event.MustDefine(event.Default(), event.Spec[GrantEnded]{Name: "grant/ended", Version: 1,
		Validate: func(g GrantEnded) error {
			switch {
			case g.ID == "":
				return errors.New("grant/ended: id is required")
			case !slices.Contains([]string{EndRevoked, EndSessionEnded, EndHostStopped, EndPolicyChanged}, g.Reason):
				return fmt.Errorf("grant/ended: reason %q is not in the closed set", g.Reason)
			}
			return nil
		}})
)

// MaxGrantsPerSession caps the live grants of one agent.
const MaxGrantsPerSession = 32

// The grant error codes.
var (
	ErrGrantNotFound = errors.New("approval/grant-not-found")
	ErrGrantCap      = errors.New("approval/grant-cap")
	ErrGrantRefused  = errors.New("approval/grant-refused")
)

// Grants is the in-memory table of session grants (DH-D-HUMAN-3), keyed by the
// agent's ref. The ref carries the incarnation, so a resumed, forked or child
// agent starts with none: nothing is copied and nothing is read back from the log.
type Grants struct {
	mu      sync.Mutex
	byAgent map[agent.Ref][]Grant
}

// NewGrants is an empty table.
func NewGrants() *Grants { return &Grants{byAgent: map[agent.Ref][]Grant{}} }

// List is a copy of ref's live grants, oldest first.
func (g *Grants) List(ref agent.Ref) []Grant {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.byAgent[ref])
}

// Roots is ref's granted directories, for the sandbox's policy resolver.
func (g *Grants) Roots(ref agent.Ref) []string {
	var out []string
	for _, gr := range g.List(ref) {
		if gr.Subject.Kind == ScopeDirectory {
			out = append(out, gr.Subject.Path)
		}
	}
	return out
}

// Match is ref's first grant covering s; none for a nil or ungrantable subject.
func (g *Grants) Match(ref agent.Ref, s *Subject) (Grant, bool) {
	if g == nil || s == nil {
		return Grant{}, false
	}
	c, err := s.canonical()
	if err != nil {
		return Grant{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, gr := range g.byAgent[ref] {
		if gr.Subject.matches(c) {
			return gr, true
		}
	}
	return Grant{}, false
}

// grantChannels are the channels whose allowed-session answer may create a grant:
// a person at the browser or the terminal. An sdk or acp answer, a plugin and the
// tool endpoint cannot.
var grantChannels = []string{"ui", "cli", "tui"}

// create records a grant of s for ref given by by, logging grant/created first.
func (g *Grants) create(ctx context.Context, log Log, ref agent.Ref, s Subject, by By, tool, call string) (Grant, error) {
	if !slices.Contains(grantChannels, by.Channel) {
		return Grant{}, fmt.Errorf("%w: channel %q cannot grant", ErrGrantRefused, by.Channel)
	}
	c, err := s.canonical()
	if err != nil {
		return Grant{}, fmt.Errorf("%w: %v", ErrGrantRefused, err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.byAgent[ref]) >= MaxGrantsPerSession {
		return Grant{}, ErrGrantCap
	}
	gr := Grant{ID: "grt-" + mintID()[4:], Agent: ref, Subject: c, By: by, Tool: tool, Call: call}
	d, err := GrantCreatedType.Draft(GrantCreated{ID: gr.ID, Agent: ref.String(), Subject: c, By: by, Tool: tool, Call: call})
	if err == nil {
		_, err = log.Append(ctx, d)
	}
	if err != nil {
		return Grant{}, fmt.Errorf("approval: append grant/created: %w", err)
	}
	g.byAgent[ref] = append(g.byAgent[ref], gr)
	return gr, nil
}

// Revoke ends ref's grant id, logging grant/ended{revoked} by who.
func (g *Grants) Revoke(ctx context.Context, log Log, ref agent.Ref, id, who string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	i := slices.IndexFunc(g.byAgent[ref], func(gr Grant) bool { return gr.ID == id })
	if i < 0 {
		return fmt.Errorf("%w: %s", ErrGrantNotFound, id)
	}
	if err := appendEnded(ctx, log, id, EndRevoked, who); err != nil {
		return err
	}
	g.byAgent[ref] = slices.Delete(g.byAgent[ref], i, i+1)
	return nil
}

// EndAll ends every grant of ref with reason (session-ended at disposal,
// host-stopped, policy-changed at a permission switch). Every grant is
// forgotten even when its grant/ended cannot be written, so it never
// authorises again (failing closed); the joined errors are returned, and the
// log's unended grant/created is closed at the next resume.
func (g *Grants) EndAll(ctx context.Context, log Log, ref agent.Ref, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var errs []error
	for _, gr := range g.byAgent[ref] {
		if err := appendEnded(ctx, log, gr.ID, reason, ""); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", gr.ID, err))
		}
	}
	delete(g.byAgent, ref)
	return errors.Join(errs...)
}

func appendEnded(ctx context.Context, log Log, id, reason, who string) error {
	d, err := GrantEndedType.Draft(GrantEnded{ID: id, Reason: reason, By: who})
	if err == nil {
		_, err = log.Append(context.WithoutCancel(ctx), d)
	}
	if err != nil {
		return fmt.Errorf("approval: append grant/ended: %w", err)
	}
	return nil
}

// Sessions finds a live session to write grant/ended to (session.Service).
type Sessions interface {
	Get(id session.ID) (session.Session, bool)
}

// Bind ties the table to the agent lifecycle under root, so cleanup needs no
// caller: when an agent is created (a new or resumed incarnation) every grant
// its session's log leaves open is ended session-ended and the table drops any
// older incarnation of the session; when an agent is disposed its grants end
// session-ended while its session is still live, and the table forgets them
// either way. A failed close of the log fails the creation. The returned handles
// unbind.
func (g *Grants) Bind(root *cordis.Context, sessions Sessions) ([]*cordis.EffectHandle, error) {
	created, err := agent.Created.On(root, func(_ *cordis.Context, n agent.CreatedNotice) error {
		ref := n.Agent.Ref()
		g.forgetOlder(ref)
		s, ok := sessions.Get(ref.Session)
		if !ok {
			return nil
		}
		_, err := CloseGrantsOnResume(context.Background(), s)
		return err
	})
	if err != nil {
		return nil, err
	}
	disposed, err := agent.Disposed.On(root, func(_ *cordis.Context, n agent.DisposedNotice) error {
		if s, ok := sessions.Get(n.Ref.Session); ok {
			return g.EndAll(context.Background(), s, n.Ref, EndSessionEnded)
		}
		g.mu.Lock()
		delete(g.byAgent, n.Ref)
		g.mu.Unlock()
		return nil
	})
	if err != nil {
		created.Dispose()
		return nil, err
	}
	return []*cordis.EffectHandle{created, disposed}, nil
}

// forgetOlder drops the grants of ref's session held by any other incarnation.
func (g *Grants) forgetOlder(ref agent.Ref) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for r := range g.byAgent {
		if r.Session == ref.Session && r != ref {
			delete(g.byAgent, r)
		}
	}
}

// GrantLog is a session the resume closer reads and appends to.
type GrantLog interface {
	Log
	Len() event.Offset
	Read(ctx context.Context, from event.Offset, limit int, opts ...session.ReadOption) ([]event.Event, error)
}

// CloseGrantsOnResume appends grant/ended{session-ended} for each grant/created
// of log with no grant/ended, so the display fold of a resumed session lists no
// grant. The table itself is never filled from the log.
func CloseGrantsOnResume(ctx context.Context, log GrantLog) ([]string, error) {
	var open []string
	const page = 256
	for from, n := event.Offset(0), log.Len(); from < n; from += page {
		evs, err := log.Read(ctx, from, page)
		if err != nil {
			return nil, err
		}
		for _, ev := range evs {
			switch ev.Type() {
			case GrantCreatedType.Name():
				if c, err := GrantCreatedType.As(ev); err == nil {
					open = append(open, c.ID)
				}
			case GrantEndedType.Name():
				if e, err := GrantEndedType.As(ev); err == nil {
					open = slices.DeleteFunc(open, func(id string) bool { return id == e.ID })
				}
			}
		}
	}
	for _, id := range open {
		if err := appendEnded(ctx, log, id, EndSessionEnded, ""); err != nil {
			return nil, err
		}
	}
	return open, nil
}
