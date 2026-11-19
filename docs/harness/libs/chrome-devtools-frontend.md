# chrome-devtools-frontend: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". This is a short-form specification.

| | |
|---|---|
| **Library ID** | `DH-LIB-CHROMEDEVTOOLSFRONTEND` |
| **Upstream unit** | `UP-DEP-chrome-devtools-frontend` |
| **Upstream version** | 1.0.1638082 (`pnpm-lock.yaml:24598`), tied to source revision `0e1186138ed519d9659c1874bf6375eca4483c72` (`packages/experimental/inspector/scripts/devtools/source.ts:5`, `:7`) |
| **Kind** | build-only (a development dependency whose compiled output is published inside one package) |
| **Used by** | `experimental` (`packages/experimental/inspector`) |
| **Go choice** | dropped |
| **Related areas** | experimental inspector |

## 1. What it is and why upstream needs it

`chrome-devtools-frontend` is the source code of the **Chrome DevTools user interface**:
the Console, Sources, Network, Elements, Memory and Performance panels a developer sees
when pressing F12 in Chrome. It is only a user interface. It shows whatever a program
tells it over the **Chrome DevTools Protocol (CDP)**, the debugging protocol that
Chromium and Node's JavaScript engine (V8) both speak.

Upstream's experimental **inspector** package lets a developer inspect a running harness
host and its browser clients in that familiar interface. The package needs a copy of
the interface it can serve itself, so that the panel can sit inside the harness's own web
page as a bottom panel instead of in a separate Chrome window
(`packages/experimental/inspector/README.md:38`, `:40`). That copy is built from this
dependency.

What the panels show comes from two places. Console evaluation, Sources, breakpoints,
heap snapshots and CPU profiles come from V8 itself, reached through Node's built-in
inspector (`packages/experimental/inspector/README.md:53`, `:162`). The Elements panel
and the Network panel are synthetic: upstream feeds them the tree of cordis contexts and
fibers and the host's captured HTTP requests, translated into CDP messages
(`packages/experimental/inspector/README.md:57`, `:63`).

## 2. How upstream uses it

Nothing imports the package at run time. It is read by build scripts and tests.

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/inspector/scripts/devtools/inputs.ts:12` | Locate the installed source and refuse any version but the pinned one | `import.meta.resolve('chrome-devtools-frontend/package.json')` |
| `packages/experimental/inspector/scripts/build-devtools.ts:40` | Compile the TypeScript sources with Vite into `lib/devtools`, with a manifest that records source, version and revision | The package's `front_end/` source tree |
| `packages/experimental/inspector/scripts/devtools/vite.ts:104` | Remove the reference to Google's hosted copy so the result works offline | Text replacement in the built HTML |
| `packages/experimental/inspector/tests/devtools-source.host.spec.ts:15`, `tests/devtools-build.e2e.ts:56`, `tests/workspace-prefetch.host.spec.ts:23` | Check the source layout, the built output and the pin | Files under `node_modules/chrome-devtools-frontend/front_end/` |

## 3. Tech debt upstream carries

No patch exists for the package. One debt was found in the inspector README:

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L3.7` | The harness maintains its own build of a large third-party application. The npm snapshot "is an ancestor of the Chromium 150 release branch, not an exact copy of its later stable release"; the build replaces inputs that Chromium's own build system would generate (style modules, image variables, locale constants); and an update means changing the pin and the source configuration together and re-checking every panel. | `packages/experimental/inspector/README.md:42`, `:44`, `:46` | The Go harness does not build or ship the DevTools interface (DH-ADR-CHROMEDEVTOOLSFRONTEND-1), so there is no snapshot to track and no substitute inputs to maintain. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CHROMEDEVTOOLSFRONTEND-R1` | No harness build step compiles DevTools sources and no release artifact contains them. Developer inspection must not add a JavaScript build to the product. | DH-ADR-3; debt above |
| `DH-LIB-CHROMEDEVTOOLSFRONTEND-R2` | The two things the inspector shows that do not depend on V8, the live cordis tree and the host's captured HTTP exchanges, remain available to a developer by another means. This is a requirement on the inspector area, recorded here so the drop does not lose it. | `packages/experimental/inspector/README.md:57`, `:63`, `:136` |

## 5. Go choice

**Decision.** Dropped. See the decision record below.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **A. Drop the DevTools interface; show harness state as server-rendered pages and use Go's own tools for the rest** (chosen) | No JavaScript build; follows DH-ADR-3; Go already has a profiler (`net/http/pprof`), an execution tracer (`runtime/trace`) and a debugger (Delve, an external tool) | Developers lose the familiar DevTools panels; there is no in-page expression evaluator for the host | Standard library packages; nothing to verify as a module. |
| B. Keep a CDP endpoint in Go that serves only the synthetic parts (cordis tree as Elements, HTTP capture as Network), and let the developer open it with the DevTools already inside their Chrome | Familiar interface for two panels; nothing shipped, because upstream already supports opening an external DevTools window from a printed URL (`packages/experimental/inspector/README.md:99`) | The harness must implement CDP message handling by hand; Console, Sources, Memory and Performance would be empty, which is confusing; CDP changes with Chrome | Not examined beyond upstream's README. |
| C. Ship a prebuilt copy of the interface as static files | Keeps the embedded bottom panel | Needs the JavaScript toolchain at release time, keeps the debt in section 3, and still has no V8 behind it | Licence BSD-3-Clause per `THIRD_PARTY_NOTICES.md:87`. |

<a id="dh-adr-chromedevtoolsfrontend-1"></a>
### DH-ADR-CHROMEDEVTOOLSFRONTEND-1 The Go harness does not build or ship the Chrome DevTools interface

- **Decision.** The dependency is dropped. The Go harness contains no DevTools
  interface, built or prebuilt. Verdict on upstream's embedded DevTools panel: **drop**
  the mechanism; the inspector area decides how each capability is offered instead.
- **Why.** The interface is a viewer for a CDP debugging target. Most of what upstream
  shows in it comes from V8 through Node's inspector. Inference: a Go process has no V8
  and no CDP debugging target, so Console evaluation, Sources, breakpoints, heap
  snapshots and CPU profiles have nothing to connect to. Keeping the interface for the
  two synthetic panels would bring back a JavaScript build that DH-ADR-3 removes, and
  the maintenance debt in section 3.
- **Tradeoff.** The embedded bottom panel disappears, and so does evaluating an
  expression inside the running host. Go's profiler, tracer and debugger cover
  performance and stepping, but in different tools.
- **Serves.** [DH-O-14](../design.md#dh-o-14) (one binary per platform); consistent with DH-ADR-2 and DH-ADR-3.
- **Verified by.** `DH-T-CHROMEDEVTOOLSFRONTEND-1`.

**Open question for the inspector area.** Option B is compatible with this decision,
because it ships nothing from this dependency. Whether it is worth building is theirs
to decide, together with how `DH-LIB-CHROMEDEVTOOLSFRONTEND-R2` is met.

## 8. Verification

Local test. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CHROMEDEVTOOLSFRONTEND-1` | local | `DH-LIB-CHROMEDEVTOOLSFRONTEND-R1` | A release-content check lists every file embedded in the binary and every file in the release archive and fails if any path or licence notice belongs to the DevTools interface, or if the build invoked a JavaScript bundler for an inspector asset. |

`DH-LIB-CHROMEDEVTOOLSFRONTEND-R2` is proven by the inspector area's own tests, not
here.

## 9. Units dispositioned by this spec

| Unit | Disposition |
|---|---|
| `UP-DEP-chrome-devtools-frontend` | dropped by DH-ADR-CHROMEDEVTOOLSFRONTEND-1 |
| `UP-DEBT-found.L3.7` | resolved by DH-ADR-CHROMEDEVTOOLSFRONTEND-1 |
