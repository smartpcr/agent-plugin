package auth

import (
	"net/http"
	"os"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
)

// Name is the component's catalogue name.
const Name = "host.auth"

// Key is host.auth's service.
var Key = cordis.NewServiceKey[Service]("host.auth")

// The /auth/ mount and its two actions. Both are Required POSTs, so the listener's
// origin check makes them same-origin before the cookie is read.
const (
	Prefix        = "/auth/"
	SignOutPath   = Prefix + "sign-out"
	RevokeAllPath = Prefix + "revoke-all"
)

// Component is host.auth: it opens the session table in the database under
// kv.DBKey, answers listen.Authenticate and listen.Exchange, mounts /auth/ and
// provides the service under Key. Sign-ins are announced on standard error, the
// host's terminal. Unloading it ends every peer's context; the rows stay, so a
// restart signs nobody out.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	l, err := listen.Key.Get(ctx)
	if err != nil {
		return err
	}
	db, err := kv.DBKey.Get(ctx)
	if err != nil {
		return err
	}
	var a *Auth
	if _, err := ctx.Effect("session table", func() (cordis.Disposer, error) {
		var err error
		a, err = New(ctx.Lifetime(), db, Options{Config: cfg, Audience: l.Origin(), Secure: l.Secure(), Notice: os.Stderr})
		if err != nil {
			return nil, err
		}
		return a.Close, nil
	}); err != nil {
		return err
	}
	if _, err := listen.Authenticate.On(ctx, func(_ *cordis.Context, c *listen.Credentials) (listen.Peer, bool, error) {
		return a.Authenticate(c)
	}); err != nil {
		return err
	}
	if _, err := listen.Exchange.On(ctx, func(_ *cordis.Context, c *listen.Credentials) ([]*http.Cookie, bool, error) {
		return a.Exchange(c)
	}); err != nil {
		return err
	}
	if err := l.Mount(ctx, listen.Mount{Prefix: Prefix, Serve: a.Handler()}); err != nil {
		return err
	}
	_, err = Key.Provide(ctx, Service(a))
	return err
}, cordis.WithInject(listen.Key.Name(), kv.DBKey.Name()))

// Handler serves /auth/: POST sign-out ends this browser's session and expires its
// cookie; POST revoke-all, for the owner only, ends every session.
func (a *Auth) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := listen.PeerFrom(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != SignOutPath && r.URL.Path != RevokeAllPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var err error
		if r.URL.Path == SignOutPath {
			err = a.SignOut(r.Context(), p.ID())
		} else if !p.Principal().IsOwner() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		} else {
			err = a.RevokeAll(r.Context())
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: a.cookieFor(p.Principal()), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.opts.Secure})
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}
