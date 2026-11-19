# @vitejs/plugin-react: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | None. No Go item is needed |
| **Upstream unit** | `UP-DEP-@vitejs/plugin-react` |
| **Upstream version** | 4.7.0 (MIT, `THIRD_PARTY_NOTICES.md:250`) |
| **Kind** | build-only |
| **Used by** | apps (`apps/web`, development dependency only) |
| **Go choice** | not applicable |
| **Related areas** | A25 (web application assembly); [go-ui-stack.md](go-ui-stack.md); [build-test-toolchain.md](build-test-toolchain.md) |

## 1. What it is and why upstream needs it

Vite is the tool that turns upstream's web client source into the JavaScript files a
browser loads. React components are written in JSX, a syntax browsers do not understand.
This plugin teaches Vite to compile JSX and, during development, to swap a changed
component into the running page. It is never part of the running product: it runs on a
developer's machine or in the build.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `apps/web/vite.config.ts:6`, `:174` | One entry in the plugin list of the web client's production build | `react()` with no options |
| `apps/web/package.json:59` | Declared as a development dependency | n/a |

No other import exists (searched for `@vitejs/plugin-react`). There is no patch, pin or
exemption for it. The same configuration refuses to run Vite's own development server
(`apps/web/vite.config.ts:47-55`), so only the JSX compilation is in use.

## 4. Requirements for the Go implementation

None. The need it serves, turning component source into something a browser can run,
does not exist in the Go design.

## 5. Go choice

**Decision.** Not applicable. Under [DH-ADR-3](../design.md#dh-adr-3) the user interface
is HTML rendered by the Go host. There are no React components, so there is no JSX to
compile, and there is no bundle step, so there is no bundler to plug into. Pages are
templates embedded in the binary
([DH-ADR-GOUISTACK-1](go-ui-stack.md#dh-adr-gouistack-1)); the little JavaScript that
remains is loaded as plain ES modules
([DH-ADR-GOUISTACK-6](go-ui-stack.md#dh-adr-gouistack-6)). Vite itself is dispositioned in
[build-test-toolchain.md](build-test-toolchain.md).

**Why.** The dependency exists only because the upstream client is a React application
built by a JavaScript bundler.

**Tradeoff.** None from this package. What is given up by not having a React client is
listed in [go-ui-stack.md](go-ui-stack.md) section 7.2.

## 8. Verification

Nothing to verify for this package. That the build needs no Node, npm or bundler is
`DH-LIB-GOUISTACK-R3`, checked by `DH-T-GOUISTACK-3` in [go-ui-stack.md](go-ui-stack.md).

## 9. Units dispositioned by this spec

- `UP-DEP-@vitejs/plugin-react`
