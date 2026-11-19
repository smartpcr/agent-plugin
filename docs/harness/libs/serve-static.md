# serve-static: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". Go standard-library facts were read in the Go 1.27.4
> source on 2026-11-18.

| | |
|---|---|
| **Library ID** | `DH-LIB-SERVESTATIC` |
| **Upstream unit** | `UP-DEP-serve-static` |
| **Upstream version** | 2.2.1 |
| **Kind** | runtime, one experimental package |
| **Used by** | experimental (`packages/experimental/inspector`) |
| **Go choice** | standard library: `net/http` (`http.FileServerFS`, `http.ServeFileFS`, `http.ServeContent`) with `embed` and `io/fs` |
| **Related areas** | web host, UI assets, inspector |

## 1. What it is and why upstream needs it

`serve-static` is the Express middleware that serves files from a directory: it maps a
URL path to a file, refuses paths that escape the directory, sets the content type and
caching headers, and answers conditional and range requests.

Upstream uses it once. The experimental inspector ships a copy of the Chrome DevTools
front end as a directory of files and serves it under `/inspector/devtools` to the owner
only (`packages/experimental/inspector/src/host/plugin.ts:89`). The main web client is
**not** served by this library; the `frontend-static` package has its own code for that.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/inspector/src/host/plugin.ts:80` | Loaded only after the inspector worker starts, so a platform without it never loads the library | dynamic `import('serve-static')` |
| `packages/experimental/inspector/src/host/plugin.ts:89` | Serve one directory with no directory index, no redirect for a directory path, fall through to the caller on a miss, and `X-Content-Type-Options: nosniff` on every file | `serveStatic(root, { index: false, redirect: false, fallthrough: true, setHeaders })` |
| `packages/experimental/inspector/src/host/plugin.ts:96` | Around it, written by hand: owner-only access (403 otherwise), `GET` and `HEAD` only (405 otherwise), a redirect from the bare prefix to the entry page, 404 on a miss, 500 on an error | none |

No patch, pin or exemption exists (`patches/`, `pnpm-workspace.yaml` searched), and no
README limitation names it.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SERVESTATIC-R1` | Static files are served from a fixed root; a request path can never reach a file outside it, including through `..`, encoded separators or symbolic links. | what the library is for |
| `DH-LIB-SERVESTATIC-R2` | Only `GET` and `HEAD` are answered; a directory path is not listed and not redirected; a miss is 404. | `packages/experimental/inspector/src/host/plugin.ts:91`, `:101` |
| `DH-LIB-SERVESTATIC-R3` | Every static response carries `X-Content-Type-Options: nosniff` and a content type from the harness's own table ([mime.md](mime.md)), not one guessed from the bytes. | `packages/experimental/inspector/src/host/plugin.ts:92` |
| `DH-LIB-SERVESTATIC-R4` | Conditional requests (`If-None-Match`, `If-Modified-Since`) and byte ranges work, so a browser can revalidate and resume. | behaviour `serve-static` provides by default |
| `DH-LIB-SERVESTATIC-R5` | Access is decided before the file handler runs, by the route's declared class (anonymous asset, or owner only). | `packages/experimental/inspector/src/host/plugin.ts:97` |

## 5. Go choice

**Decision.** The standard library. Assets compiled into the binary are an `embed.FS`
served through `http.FileServerFS`, or `http.ServeFileFS` for a single file; a file
found at run time is served with `http.ServeContent`.

**Why.** `http.FileServerFS` over an `io/fs.FS` gives R1 by construction: an `fs.FS`
rejects any name that is not a clean, rooted-inside path, and an embedded file system
has no symbolic links. `http.ServeContent` implements conditional requests and ranges
(R4). The remaining requirements are a few lines of wrapper that upstream also wrote by
hand (method check, headers, access).

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `net/http` + `embed` | No dependency; containment guaranteed by `io/fs`; ranges and conditionals included | See the three gaps below | Go 1.27.4: `net/http/fs.go:856` (`ServeFileFS`), `:992` (`FileServerFS`), `:247` (`ServeContent`) |
| A third-party static-file module | Ready-made caching options | Nothing the standard library lacks for this use; none was evaluated | none named |

What the standard library does **not** do, and the wrapper must:

1. `http.FileServer` lists directories and redirects `/dir` to `/dir/` and `/index.html`
   to `/`. R2 forbids the listing, so the wrapper answers 404 for a directory.
2. When a file's extension is unknown it sniffs the first bytes to guess a type. R3
   requires the wrapper to set `Content-Type` from the harness table first, which stops
   the sniffing.
3. An embedded file has no modification time, so `Last-Modified` is absent. The wrapper
   sets an `ETag` from the build's asset hash, or serves hashed file names as immutable.

**Tradeoff.** Assets are fixed at build time; changing one means a new binary. That is
the intent of DH-ADR-3.

Ownership and failure: the handler holds a file only for the duration of one response.
A read error after headers are sent closes the connection, as in any Go handler. There
is nothing to release when the owning fiber unloads except the route registration.

Whether the DevTools front end is served at all depends on the inspector area: Node's
inspector protocol has no direct Go equivalent, so that area must first say what the Go
inspector is.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-SERVESTATIC-1` | local | `DH-LIB-SERVESTATIC-R1`, `DH-LIB-SERVESTATIC-R2` | Requests for `../x`, `%2e%2e/x`, `a/%2fb`, a directory, a missing file and a `POST` return 404 or 405 and never file bytes from outside the root. |
| `DH-T-SERVESTATIC-2` | local | `DH-LIB-SERVESTATIC-R3`, `DH-LIB-SERVESTATIC-R4`, `DH-LIB-SERVESTATIC-R5` | A file with an unknown extension is served as `application/octet-stream` with `nosniff`; a matching `If-None-Match` returns 304; a `Range` request returns 206; a non-owner request to an owner-only root returns 403 before any file is opened. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-serve-static`.
