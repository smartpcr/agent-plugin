# http-server: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-HTTPSERVER` |
| **Upstream unit** | `UP-DEP-http-server` |
| **Upstream version** | 14.1.1 |
| **Kind** | build-only (a development dependency of one application) |
| **Used by** | apps (`apps/web`) |
| **Go choice** | dropped: nothing replaces it |
| **Related areas** | UI build and assets |

## 1. What it is and why upstream needs it

`http-server` is a command-line program that serves a directory of files over HTTP with
no configuration. It is a developer convenience, not a library that product code
imports.

Upstream's web client is a separate build: a bundler turns the React application into a
`dist` directory of static files. One package script serves that directory so that a
developer, or a preview environment, can load the built client without starting a
harness host (`apps/web/package.json:29`). The harness itself never uses it; the real
host serves the client through its own web server.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `apps/web/package.json:29` | The `serve:preview` script: serve `dist` on every interface, port 4173, with caching disabled | command line `http-server dist -a 0.0.0.0 -p 4173 -c-1` |
| `apps/web/package.json:60` | Declared as a development dependency | none |

No source file imports it. No patch, pin or exemption exists (`patches/`,
`pnpm-workspace.yaml` searched), and no README limitation names it.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-HTTPSERVER-R1` | A developer can see the UI built from the current source tree by running the harness host itself; no second server is needed to preview it. | Inference from DH-ADR-3: the UI is rendered by the Go host and its assets are embedded |

There is no requirement to serve a client bundle on its own, because the Go design has
no client bundle that works without a host.

## 5. Go choice

**Decision.** Dropped. The unit is recorded as not applicable: it exists only because
upstream's client is a separate static build that can be previewed apart from the host.

**Why.** Under [DH-ADR-3](../design.md#dh-adr-3) the pages are HTML rendered by the host
on each request. A directory of static files cannot show them, so a static preview
server has nothing to serve. Running the host is the preview.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** nothing | Nothing to install, secure or keep current | None | n/a |
| A tiny Go file server for the embedded assets alone (`http.FileServerFS`) | Lets a designer look at static assets without a host | The assets alone are a style sheet and a few scripts; not useful without the pages | Go 1.27.4 standard library, see [serve-static.md](serve-static.md) |

**Tradeoff.** None. One upstream habit is deliberately not carried over: the preview
script binds `0.0.0.0`, every interface, which the product's own listener refuses
([ipaddr.js.md](ipaddr.js.md)).

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-HTTPSERVER-1` | local | `DH-LIB-HTTPSERVER-R1` | A test starts the host from the source tree with the development profile and fetches the start page and one embedded asset; both are served by the one listener. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-http-server`.
