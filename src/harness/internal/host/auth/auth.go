// Package auth is host.auth (DH-D-HOST-2, docs/harness/areas/host-telemetry.md
// §6.1 "Authentication"): one-time launch tokens, the hashed browser-session table
// and the peers made from it.
//
// A launch token is 32 random bytes, valid once and for LaunchTokenTTL, and kept
// only as its hash in memory. Exchanging it creates one browser session: a random
// 256-bit session id that is the cookie's whole value, and one row in a
// machine-state namespace of storage/kv holding the SHA-256 of that id, the
// audience, the principal, a browser label, issue and expiry times and a random
// form key. Nothing stored makes a cookie: there is no signing secret. A request is
// admitted when the hash of its cookie equals a live row of this audience.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/host/access"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Config is host.auth's entry config (host-telemetry.md §6.2).
type Config struct {
	// CookieMaxAge is how long a browser session lives; at least one second.
	CookieMaxAge time.Duration `json:"cookieMaxAge,omitempty"`
	// LaunchTokenTTL is how long a minted launch token may be exchanged.
	LaunchTokenTTL time.Duration `json:"launchTokenTTL,omitempty"`
}

// Defaults for the zero fields of Config.
const (
	DefaultCookieMaxAge   = 30 * 24 * time.Hour
	DefaultLaunchTokenTTL = 10 * time.Minute
)

// Names this package fixes.
const (
	// Namespace is the session table's storage/kv namespace.
	Namespace = "host.auth.sessions"
	// CookiePrefix starts every harness session cookie's name.
	CookiePrefix = "dsh-session-"
)

// Reasons this package raises.
const (
	ReasonConfig      herr.Reason = "auth/config"
	ReasonPrincipal   herr.Reason = "auth/principal"
	ReasonUnknownPeer herr.Reason = "auth/unknown-peer"
)

// SignIn is the notice of one new browser session.
type SignIn struct {
	PeerID    string
	Principal access.Principal
	Label     string
	At        time.Time
}

// Service is host.auth's service under Key.
type Service interface {
	// Mint returns a launch URL holding a fresh one-time token.
	Mint() (string, error)
	// Issue creates a session for a principal other than the owner and returns its
	// cookie, named after the principal's kind; ttl <= 0 is CookieMaxAge.
	Issue(ctx context.Context, p access.Principal, ttl time.Duration) (*http.Cookie, error)
	// SignOut deletes one browser's session and ends its peer's context.
	SignOut(ctx context.Context, peerID string) error
	// RevokeAll deletes every session of this audience and ends every peer.
	RevokeAll(ctx context.Context) error
	// OnSignIn adds a listener of sign-in notices; cancel removes it.
	OnSignIn(fn func(SignIn)) (cancel func())
}

// row is one browser session. Its key in the namespace is Hash.
type row struct {
	ID        string           `json:"id"`
	Hash      string           `json:"hash"`
	Audience  string           `json:"audience"`
	Principal access.Principal `json:"principal"`
	Label     string           `json:"label,omitempty"`
	Issued    time.Time        `json:"issued"`
	Expires   time.Time        `json:"expires"`
	FormKey   []byte           `json:"formKey"`
}

// Options are what New needs besides the database.
type Options struct {
	Config Config
	// Audience is the listener's origin; sessions of another audience never admit.
	Audience string
	// Secure sets the cookie's Secure attribute: the listener speaks TLS.
	Secure bool
	// Notice receives the terminal line of each sign-in; nil writes nothing.
	Notice io.Writer
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Auth implements Service and answers the listener's two bail keys.
type Auth struct {
	ns     *kv.Namespace[row]
	opts   Options
	cookie string // the owner cookie's name
	base   context.Context
	stop   context.CancelFunc
	unsub  func()

	mu      sync.Mutex
	tokens  map[string]time.Time // hash of a launch token -> expiry
	peers   map[string]*peer     // row key -> live peer
	notices map[int]func(SignIn)
	next    int
}

var _ Service = (*Auth)(nil)

// New opens the session table in db.
func New(ctx context.Context, db kv.DB, o Options) (*Auth, error) {
	if o.Config.CookieMaxAge == 0 {
		o.Config.CookieMaxAge = DefaultCookieMaxAge
	}
	if o.Config.LaunchTokenTTL == 0 {
		o.Config.LaunchTokenTTL = DefaultLaunchTokenTTL
	}
	if o.Config.CookieMaxAge < time.Second || o.Config.LaunchTokenTTL < 0 || o.Audience == "" {
		return nil, herr.Newf(ReasonConfig, "cookieMaxAge %s (at least 1s), launchTokenTTL %s and an audience are required",
			o.Config.CookieMaxAge, o.Config.LaunchTokenTTL)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	ns, err := kv.Open(ctx, db, kv.Spec[row]{Namespace: Namespace, Version: 1, Authority: kv.Authoritative, Now: o.Now})
	if err != nil {
		return nil, err
	}
	base, stop := context.WithCancel(context.Background())
	a := &Auth{ns: ns, opts: o, cookie: CookieName(o.Audience), base: base, stop: stop,
		tokens: map[string]time.Time{}, peers: map[string]*peer{}, notices: map[int]func(SignIn){}}
	a.unsub = ns.Subscribe(a.changed)
	return a, nil
}

// Close ends every peer's context; the rows stay.
func (a *Auth) Close() error {
	a.unsub()
	a.stop()
	return nil
}

// CookieName is the owner cookie's name for audience: the prefix and a hash of it.
func CookieName(audience string) string {
	h := sha256.Sum256([]byte(audience))
	return CookiePrefix + hex.EncodeToString(h[:8])
}

func random() ([]byte, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return b, err
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Mint implements Service.
func (a *Auth) Mint() (string, error) {
	b, err := random()
	if err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := a.opts.Now()
	a.mu.Lock()
	for h, exp := range a.tokens {
		if !now.Before(exp) {
			delete(a.tokens, h)
		}
	}
	a.tokens[hashOf(tok)] = now.Add(a.opts.Config.LaunchTokenTTL)
	a.mu.Unlock()
	return a.opts.Audience + "/?token=" + tok, nil
}

// Exchange answers listen.Exchange: the token is spent whether or not it is still
// valid, so it works once; a valid one creates an owner session.
func (a *Auth) Exchange(c *listen.Credentials) ([]*http.Cookie, bool, error) {
	if c.Audience != a.opts.Audience {
		return nil, false, nil
	}
	h := hashOf(c.Token)
	a.mu.Lock()
	exp, ok := a.tokens[h]
	delete(a.tokens, h)
	a.mu.Unlock()
	if !ok || !a.opts.Now().Before(exp) {
		return nil, false, nil
	}
	label := label(c.Request)
	cookie, r, err := a.create(c.Request.Context(), access.Owner(), label, a.opts.Config.CookieMaxAge, a.cookie)
	if err != nil {
		return nil, false, err
	}
	out := []*http.Cookie{cookie}
	// Harness cookies of another audience (another port, another host) are expired.
	for _, old := range c.Request.Cookies() {
		if strings.HasPrefix(old.Name, CookiePrefix) && old.Name != a.cookie && !strings.HasPrefix(old.Name, a.cookie+"-") {
			out = append(out, &http.Cookie{Name: old.Name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.opts.Secure})
		}
	}
	a.announce(SignIn{PeerID: r.ID, Principal: r.Principal, Label: label, At: r.Issued})
	return out, true, nil
}

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Issue implements Service.
func (a *Auth) Issue(ctx context.Context, p access.Principal, ttl time.Duration) (*http.Cookie, error) {
	if p.IsOwner() || !kindPattern.MatchString(p.Kind) {
		return nil, herr.Newf(ReasonPrincipal, "principal kind %q: Issue makes a session for a kind other than %q, written [a-z][a-z0-9-]*", p.Kind, access.KindOwner)
	}
	if ttl <= 0 {
		ttl = a.opts.Config.CookieMaxAge
	}
	c, _, err := a.create(ctx, p, "", ttl, a.cookie+"-"+p.Kind)
	return c, err
}

// create stores a new session row and returns its cookie.
func (a *Auth) create(ctx context.Context, p access.Principal, label string, ttl time.Duration, name string) (*http.Cookie, row, error) {
	id, err := random()
	if err != nil {
		return nil, row{}, err
	}
	formKey, err := random()
	if err != nil {
		return nil, row{}, err
	}
	peerID, err := random()
	if err != nil {
		return nil, row{}, err
	}
	value := base64.RawURLEncoding.EncodeToString(id)
	now := a.opts.Now()
	r := row{ID: hex.EncodeToString(peerID[:16]), Hash: hashOf(value), Audience: a.opts.Audience,
		Principal: p, Label: label, Issued: now, Expires: now.Add(ttl), FormKey: formKey}
	if err := a.ns.Update(ctx, func(tx *kv.Txn[row]) error { return tx.Put(r.Hash, r) }); err != nil {
		return nil, row{}, err
	}
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(ttl / time.Second), Expires: r.Expires,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.opts.Secure}, r, nil
}

// Authenticate answers listen.Authenticate. The owner cookie is asked first, so a
// browser that also holds an issued cookie is admitted as the owner.
func (a *Auth) Authenticate(c *listen.Credentials) (listen.Peer, bool, error) {
	if c.Audience != a.opts.Audience || c.Request == nil {
		return nil, false, nil
	}
	if err := a.ns.Fresh(c.Request.Context()); err != nil {
		return nil, false, err
	}
	if own, err := c.Request.Cookie(a.cookie); err == nil {
		if p := a.admit(own.Value, access.KindOwner); p != nil {
			return p, true, nil
		}
	}
	for _, ck := range c.Request.Cookies() {
		if kind, ok := strings.CutPrefix(ck.Name, a.cookie+"-"); ok && kind != access.KindOwner {
			if p := a.admit(ck.Value, kind); p != nil {
				return p, true, nil
			}
		}
	}
	return nil, false, nil
}

// admit is the peer of the live row value's hash names, when that row is of this
// audience and of kind; nil otherwise.
func (a *Auth) admit(value, kind string) *peer {
	if b, err := base64.RawURLEncoding.DecodeString(value); err != nil || len(b) != 32 {
		return nil
	}
	h := hashOf(value)
	r, ok, err := a.ns.Get(h)
	if err != nil || !ok || subtle.ConstantTimeCompare([]byte(r.Hash), []byte(h)) != 1 {
		return nil
	}
	if r.Audience != a.opts.Audience || r.Principal.Kind != kind || !a.opts.Now().Before(r.Expires) {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.peers[h]; ok {
		return p
	}
	ctx, cancel := context.WithCancel(a.base)
	p := &peer{id: r.ID, principal: r.Principal, formKey: r.FormKey, ctx: ctx, cancel: cancel}
	a.peers[h] = p
	return p
}

// changed ends the peer of every deleted row, whoever deleted it.
func (a *Auth) changed(c kv.Change) {
	if c.Op != kv.OpDelete {
		return
	}
	a.mu.Lock()
	p, ok := a.peers[c.Key]
	delete(a.peers, c.Key)
	a.mu.Unlock()
	if ok {
		p.cancel()
	}
}

// SignOut implements Service.
func (a *Auth) SignOut(ctx context.Context, peerID string) error {
	found := false
	err := a.ns.Update(ctx, func(tx *kv.Txn[row]) error {
		for _, k := range tx.Keys() {
			if r, ok, err := tx.Get(k); err == nil && ok && r.ID == peerID && r.Audience == a.opts.Audience {
				found = true
				return tx.Delete(k)
			}
		}
		return nil
	})
	if err == nil && !found {
		return herr.Newf(ReasonUnknownPeer, "no session of peer %q", peerID)
	}
	return err
}

// RevokeAll implements Service.
func (a *Auth) RevokeAll(ctx context.Context) error {
	return a.ns.Update(ctx, func(tx *kv.Txn[row]) error {
		for _, k := range tx.Keys() {
			if r, ok, err := tx.Get(k); err == nil && ok && r.Audience == a.opts.Audience {
				if err := tx.Delete(k); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// OnSignIn implements Service.
func (a *Auth) OnSignIn(fn func(SignIn)) (cancel func()) {
	a.mu.Lock()
	id := a.next
	a.next++
	a.notices[id] = fn
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		delete(a.notices, id)
		a.mu.Unlock()
	}
}

// announce tells the host's terminal and every listener (the open tabs) of a sign-in.
func (a *Auth) announce(s SignIn) {
	if a.opts.Notice != nil {
		fmt.Fprintf(a.opts.Notice, "dsh: a browser signed in as %s (%s) at %s; to sign every browser out, POST %s%s\n",
			s.Principal.Kind, orUnknown(s.Label), s.At.Format(time.RFC3339), a.opts.Audience, RevokeAllPath)
	}
	a.mu.Lock()
	ls := make([]func(SignIn), 0, len(a.notices))
	for _, id := range slices.Sorted(maps.Keys(a.notices)) {
		ls = append(ls, a.notices[id])
	}
	a.mu.Unlock()
	for _, l := range ls {
		l(s)
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown browser"
	}
	return s
}

// label is a short, printable browser label from the User-Agent.
func label(r *http.Request) string {
	ua := strings.Map(func(c rune) rune {
		if c < 0x20 || c > 0x7e {
			return -1
		}
		return c
	}, r.UserAgent())
	if len(ua) > 80 {
		ua = ua[:80]
	}
	return ua
}

// peer is one browser session as the listener sees it.
type peer struct {
	id        string
	principal access.Principal
	formKey   []byte
	ctx       context.Context
	cancel    context.CancelFunc
}

func (p *peer) ID() string                  { return p.id }
func (p *peer) Principal() access.Principal { return p.principal }
func (p *peer) Context() context.Context    { return p.ctx }

// FormToken is HMAC-SHA-256 of the peer's id under its row's form key.
func (p *peer) FormToken() string {
	m := hmac.New(sha256.New, p.formKey)
	m.Write([]byte(p.id))
	return hex.EncodeToString(m.Sum(nil))
}
