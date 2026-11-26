// Package assets is host.assets: the files pages reference, embedded in the binary
// and served under /assets/ (DH-D-HOST-1, docs/harness/libs/serve-static.md).
//
// Only GET and HEAD are answered. A path with a dot segment, an encoded or
// backslash separator, a directory and a miss are 404 and never return file bytes.
// The content type comes from the harness table (internal/util/mediatype) with
// nosniff. A file whose name carries its content hash ("app.3f2a9c1b.js") is
// immutable with a long cache lifetime; any other gets the build hash as its ETag
// and no-cache, so a matching If-None-Match is 304.
package assets

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/mediatype"
)

// Name is the component's catalogue name.
const Name = "host.assets"

// Prefix is the mount's prefix.
const Prefix = "/assets/"

//go:embed files
var embedded embed.FS

// Files is the embedded file system, rooted at its files directory.
func Files() fs.FS {
	sub, err := fs.Sub(embedded, "files")
	if err != nil {
		panic("assets: embedded files: " + err.Error())
	}
	return sub
}

// hashed matches a base name that carries a content hash before its extension.
var hashed = regexp.MustCompile(`^[^/]+[.-][0-9a-fA-F]{8,64}\.[A-Za-z0-9]+$`)

// Immutable is the Cache-Control of a content-hashed file.
const Immutable = "public, max-age=31536000, immutable"

// BuildHash is a hash over every file of fsys, names and bytes: the ETag of the
// files that carry no hash of their own. It changes exactly when the files do.
func BuildHash(fsys fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%s\x00", p, b)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// Handler serves fsys; the request path is the file's path below the mount.
func Handler(fsys fs.FS, build string) http.Handler {
	etag := `"` + build + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name, ok := fileName(r)
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			// A directory fails ReadFile as well, so it is a miss.
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", contentType(name))
		h.Set("X-Content-Type-Options", "nosniff")
		if hashed.MatchString(path.Base(name)) {
			h.Set("Cache-Control", Immutable)
		} else {
			h.Set("Cache-Control", "no-cache")
			h.Set("ETag", etag)
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
	})
}

// fileName returns the fs.FS name a request asks for, or false for a path that
// could name something other than one file under the root.
func fileName(r *http.Request) (string, bool) {
	raw := strings.ToLower(r.URL.RawPath)
	if strings.Contains(raw, "%2f") || strings.Contains(raw, "%5c") || strings.Contains(r.URL.Path, `\`) {
		return "", false
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" || !fs.ValidPath(name) {
		return "", false
	}
	return name, true
}

func contentType(name string) string {
	typ, ok := mediatype.ByPath(name)
	if !ok {
		return mediatype.Unknown
	}
	if strings.HasPrefix(typ, "text/") || typ == "application/javascript" || typ == "application/json" {
		return typ + "; charset=utf-8"
	}
	return typ
}

// Component is host.assets: it mounts the embedded files at /assets/ as an
// anonymous mount, so a page's files load before and without a credential.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	l, err := listen.Key.Get(ctx)
	if err != nil {
		return err
	}
	files := Files()
	build, err := BuildHash(files)
	if err != nil {
		return err
	}
	return l.Mount(ctx, listen.Mount{
		Prefix:   Prefix,
		Classify: func(*http.Request) listen.Class { return listen.Anonymous },
		Serve:    http.StripPrefix(strings.TrimSuffix(Prefix, "/"), Handler(files, build)),
	})
}, cordis.WithInject(listen.Key.Name()))
