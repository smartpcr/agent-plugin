# Office documents, browser use and computer use: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md) and are cited as `path:line` under the upstream root. Statements that are
> reasoning and not read from source open with "Inference"; sections 6 to 9 are design and are inference
> throughout. IDs in this document use the area key `MEDIA`. Nothing here was built, run or tested, and
> every statement about what LibreOffice, a browser or an MCP server does on a real machine is to be proven
> by a scenario in section 9.2.

| | |
|---|---|
| **Area key** | `MEDIA` (agent A16) |
| **Upstream packages** | 4: `packages/document` (`office-kit`, `tool-office`), `packages/browser-use` (1), `packages/computer-use` (1) |
| **Serves outcomes** | [DH-O-10](../design.md#dh-o-10) (seams; the main one), [DH-O-7](../design.md#dh-o-7) (confinement, approvals, session grants), [DH-O-6](../design.md#dh-o-6) (bounded results through the one pipeline), [DH-O-4](../design.md#dh-o-4) (no tool calls a model of its own), [DH-O-1](../design.md#dh-o-1) (owned child processes), [DH-O-14](../design.md#dh-o-14) (nothing bundled or downloaded) |
| **Depends on areas** | A01 (projections, session events), A03 ([tools.md](tools.md)), A07 ([storage.md](storage.md): attachments, retention), A08 ([filesystem.md](filesystem.md)), A09 ([execution.md](execution.md): the one process starter), A10 ([sandbox-ssh.md](sandbox-ssh.md): policy), A14 (skills), A15 (MCP client), A17 (approvals, session grants), C4 to C7 (tool cards, preview, status), X4 (the browser and computer-use providers) |
| **Priority** | P1 for office documents: upstream's standard agent presets carry the office tools. P2 for the browser-use and computer-use seams: upstream ships every provider for them as experimental and off by default |

## 1. Purpose

Three capabilities let an agent work on things that are not plain text files. **Office documents**: see a page of a Word, Excel or PowerPoint file as an image, convert it to another format, and recalculate a workbook. **Browser use**: open web pages and operate them. **Computer use**: observe and operate applications on the local desktop. Each needs outside software the harness does not ship: LibreOffice, a Chromium-family browser, a desktop driver.

This area owns the three **seams**: the fixed tools the model sees, the Go interfaces a provider implements, and the rules that sit between them (queues and limits, who owns a child process, what is approved and how, what is said when the outside software is missing). It also owns the one provider that is not experimental: the service that drives an installed LibreOffice. Without this area the tool schema would change with every backend, an untrusted document would be parsed with the host's full authority, and a browser or desktop action on the user's own accounts would need no more approval than reading a file.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Engine** | The program that understands office formats. Here: the `soffice` program of a LibreOffice the operator installed, run **headless** (no window). | Layout, format conversion and formula recalculation need a real office suite ([DH-ADR-20](../design.md#dh-adr-20)). |
| **Job, reader** | A job is one engine operation on one document's bytes. A reader is one caller waiting for a job's result. | Two callers asking for the same conversion share one job; each can leave without cancelling the other. |
| **Slot, profile directory** | A slot is one of the few places a job may run at a time. Each slot has its own LibreOffice profile directory (the settings folder LibreOffice insists on writing). | Bounds memory and CPU; two LibreOffice processes must never share a profile, and the user's own profile is never touched. |
| **Scratch directory** | A private directory holding one job's copy of the input and its outputs. | The engine may write only there, and it is removed before the result returns. |
| **Generation** | A value that changes when the engine (path, version) or the rendering settings change. | A cached result made by one engine is never reused after the engine changes. |
| **Rasterise** | Turn a PDF page into a PNG image. | The model views pages as images. |
| **Provider slot** | The single place in the browser-use or computer-use service where one provider is registered. | Two backends driving the same capability at once would confuse both the model and the approval rules. |
| **Launch mode, attach mode** | Launch: the harness starts a browser with a fresh private profile and owns it. Attach: the harness connects to a browser the user already runs, with the user's logins, and never owns it. | Who owns the browser decides what cleanup may close and how much approval an action needs. |
| **Page description, element reference** | A bounded text listing of a page's or window's actionable elements taken from the **accessibility tree** (the structure screen readers use). Each element carries an opaque reference, valid until the next description. | The model acts on what it was shown; a stale reference is an error, never a guess. |
| **Origin, signed-in origin** | An origin is a scheme, host and port (`https://mail.example.com`). It is *signed-in* when acting there may act as the user ([DH-ADR-MEDIA-5](#dh-adr-media-5)). | Approval is asked per site, not per click. |
| **MCP** | Model Context Protocol: a standard by which a separate program offers tools. | The only route to computer use ([DH-ADR-19](../design.md#dh-adr-19)). |
| **Provider profile** | The list of things an external MCP server must offer to fill the computer-use seam ([§6.2](#62-contracts)). | The model's tools are fixed, so a server either fits them or is not a provider. |
| **Application grant** | A session grant ([DH-ADR-16](../design.md#dh-adr-16)) whose scope is one desktop application. | One prompt per application instead of one per click. |
| **Desktop lease** | The right of one session, for one turn, to send computer-use calls. | One desktop cannot be driven by two agents at once. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.document.office-kit` | `@deepseek-ai/dsh-office-kit` | definition and provider | `ctx.officeKit`: PDF conversion for previews, page images, format conversion and recalculation through the LibreOffice Kit npm package loaded into the host; a bounded shared queue, a transient cache and a private output store (`packages/document/office-kit/README.md:12`, `:63`) |
| `UP-PKG-packages.document.tool-office` | `@deepseek-ai/dsh-tool-office` | consumer | The tools `office_render`, `office_convert` and `office_recalculate`, declared only after an Office skill loads (`packages/document/tool-office/README.md:12`, `:28`) |
| `UP-PKG-packages.browser-use.browser-use` | `@deepseek-ai/dsh-browser-use` | definition | `ctx.browserUse`: one provider name may be registered; no browser object, operation or lifecycle (`packages/browser-use/browser-use/src/index.ts:16-45`) |
| `UP-PKG-packages.computer-use.computer-use` | `@deepseek-ai/dsh-computer-use` | definition | `ctx.computerUse`: the same 48 lines with the names changed (`packages/computer-use/computer-use/src/index.ts:16-45`) |

### 3.2 Behaviour

**The office service.** A caller hands `ctx.officeKit` an already authorised source (a key, a version, an optional size and a deferred bounded read) and an operation: `pdf`, `images`, `convert` or `recalculate` (`packages/document/office-kit/src/queue.ts:12-26`). All four share one admission queue. Queued jobs, waiting readers, reserved source bytes and running conversions are each bounded before any source byte is read (`README.md:65`); the defaults are 2 conversions, 8 queued jobs, 32 readers and 100 MiB of reserved source bytes (`src/index.ts:87-94`). Results are cached by engine generation, input extension, operation and the SHA-256 of the exact source bytes, so the same bytes at two paths convert once; a cancelled reader leaves alone and only the last reader cancels the job (`README.md:63`, `:85`). Requests carry a `foreground` or `background` priority, although nothing in the product submits a background request (`.agents/notes/proposed/simplification/2026-09-17-defer-office-background-scheduling.md:11`).

**The engine.** The provider imports the kit from its own npm installation at first use and never searches the machine for LibreOffice (`src/index.ts:346-356`, `README.md:115`). One long-lived converter per slot runs **inside the host process** (`src/index.ts:378-385`). Each job writes the input to a fresh directory under the system temporary directory, runs, reads bounded regular output files and removes the directory in a `finally` block (`src/index.ts:387-408`). Time, input bytes, output bytes, ZIP entry count, uncompressed size and font limits are passed to the kit (`src/index.ts:151-157`); the deadline starts only when the conversion starts, so time in the queue is unbounded (`README.md:116`). An absent kit does not fail start-up: the provider registers and each call fails as `unavailable` (`README.md:43`).

**Outputs.** Page images and preview PDFs are returned as bytes. Converted and recalculated files are saved in an owner-only directory under `$DSH_HOME/office-kit/outputs`, one sub-directory per owning session, and deleted after 24 hours, under size pressure, when the owning session is disposed or when the provider unloads (`README.md:39`; `src/outputs.ts:29-42`). A browser asks for a preview PDF through the remote method `officeKit.render`, which re-checks the session's file authorisation first (`src/index.ts:260-332`).

**The office tools.** Three tools share one activation rule: they are declared to an agent from the step after it loads a skill named in `activationSkills`, tracked by an in-memory mark and by looking for the tools in the last request header (`packages/document/tool-office/src/index.ts:147-166`). Each call resolves the input through the session's filesystem service, reads one byte to prove the caller may read it before a cached result can answer, and then calls the office service (`src/index.ts:230-270`). `office_render` requires a model route that accepts images, bounds the result by image count, pixels, side length and bytes (the lower of its config and the attachment store's limits), and stores the images as attachments before the result is logged (`src/index.ts:311-365`). `office_convert` and `office_recalculate` return the path of a temporary file and tell the model to copy it with the shell, because the filesystem service creates no binary files; on a remote workspace they refuse (`README.md:53`, `:107-108`). Every `unavailable` failure tells the model not to substitute another converter (`src/index.ts:300-301`). Accepted inputs and outputs per document family are a fixed table (`src/formats.ts:15-27`).

**Browser use and computer use.** Each service only records which provider is mounted. `register(name)` fails if any provider is registered, even one with the same name, and the name stays registered while the provider shuts down (`packages/browser-use/browser-use/src/index.ts:23-45`). There is deliberately no common operation interface: each provider brings its own tool catalogue through the ordinary tool pipeline (`docs/subsystems/browser-use.md:17`, `:35`; `docs/subsystems/computer-use.md:37`). The rules a provider must follow are written in documents, not code: a launched browser belongs to one live session and is closed with it, an attached browser is reserved for one session and only disconnected (`docs/subsystems/browser-use.md:21-23`); a computer-use registration does not reserve the desktop, so concurrent sessions "coordinate" themselves (`docs/subsystems/computer-use.md:43`).

**Events, persistence, config.** The area defines no event and persists no session event of its own; calls and results are the ordinary `tool/call` and `tool/result`, with rendered images as attachment references. The office service listens to `session/disposed` to release outputs (`packages/document/office-kit/src/index.ts:163`). Config: the office service (`docs/config-catalog.md:2807`) and the office tools (`docs/config-catalog.md:4691`); the two registries have none (`packages/browser-use/browser-use/README.md:34`).

### 3.3 Failure, retry, rollback and cleanup upstream

Office failures are classified (`input-too-large`, `output-too-large`, `invalid-document`, `unsupported-format`, `invalid-request`, `invalid-output`, `timeout`, `unavailable`, `failed`) and engine diagnostics stay out of the result (`packages/document/office-kit/src/index.ts:394-404`). Nothing is retried. Unload cancels the queue, deletes outputs and joins converter teardown (`src/index.ts:164-175`); expired output directories of earlier processes are swept at start by name pattern (`src/outputs.ts:193-201`). **Left behind or uncertain:** the engine shares the host process, so a native crash ends every session and an untrusted document is parsed with the host user's permissions outside any sandbox (`packages/document/tool-office/README.md:111`); a scratch directory survives a host crash. For browser and computer use, cancellation never undoes delivered input, and the registries hold nothing to clean up (`docs/subsystems/browser-use.md:25`).

## 4. Upstream decisions dissected

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `UP-DEC-implemented.architecture.2026-09-12-browser-use-provider-registration` | A name-only registry; each provider owns its tools; no common action interface "before a portable consumer exists"; a browser belongs to one live session (note `:13`, `:17`, `:31`) | keep the exclusive slot and session ownership; change the rest | [DH-O-10](../design.md#dh-o-10) is the portable consumer: a provider change must not change the model's tools. With the AI layer dropped ([DH-ADR-24](../design.md#dh-adr-24)) every backend offers the same plain actions | [DH-ADR-MEDIA-1](#dh-adr-media-1), [DH-ADR-MEDIA-5](#dh-adr-media-5) |
| `UP-DEC-implemented.architecture.2026-09-12-computer-use-provider-registration` | The same registry; an in-process native provider beside the MCP one; no "session ownership broker" for the desktop (note `:13`, `:25-29`) | keep the slot; change the interface; drop the in-process provider; adopt the broker | The in-process driver is ruled out by [DH-ADR-19](../design.md#dh-adr-19). Application grants need arguments the harness can read, which only a harness-owned catalogue gives | [DH-ADR-MEDIA-1](#dh-adr-media-1), [DH-ADR-MEDIA-6](#dh-adr-media-6) |
| `UP-DEC-implemented.architecture.2026-09-14-independent-libreoffice-kit` | The engine lives in its own repository and is consumed as an exact npm version (note `:13-15`) | replace | The Go harness consumes no engine package at all: the operator installs LibreOffice | [DH-ADR-20](../design.md#dh-adr-20), [DH-ADR-MEDIA-2](#dh-adr-media-2) |
| `UP-DEC-implemented.architecture.2026-09-15-bounded-office-conversion` | One shared queue; admission before bytes are read; identity by content digest plus generation; the last reader cancels (note `:15-19`) | keep, minus priorities; bytes go to files, not memory | The bounds do not depend on the engine and matter more when each job is a process | [DH-ADR-MEDIA-2](#dh-adr-media-2) |
| `UP-DEC-implemented.architecture.2026-10-31-native-office-tool` | Three on-demand host tools instead of a shell command; engine in the host; outputs in a private store, copied by the shell (note `:13`, `:17`, `:19`) | keep the three tools and on-demand activation; change engine placement, output delivery and how activation is remembered | The note moved the engine into the host because the shell sandbox broke it; the Go design starts the engine under a profile written for it. The Go filesystem seam writes bytes under the same policy as any write | [DH-ADR-MEDIA-3](#dh-adr-media-3), [DH-ADR-MEDIA-4](#dh-adr-media-4) |
| `UP-DEC-proposed.simplification.2026-09-17-defer-office-background-scheduling` | Proposal: remove foreground and background priorities; no caller is speculative (note `:17`) | adopt | Building unused scheduling again would repeat the debt the note names | [DH-ADR-MEDIA-2](#dh-adr-media-2) |
| Code: the provider never searches for a system LibreOffice (`office-kit/README.md:115`) | A qualified engine ships with the product | change | One Go binary cannot carry an engine; the engine version is reported instead | [DH-ADR-MEDIA-2](#dh-adr-media-2) |
| Code: an absent engine is `unavailable`, never a start-up failure; logged once per distinct cause (`office-kit/src/index.ts:358-367`) | An optional capability must not stop the host | keep | It is the rule for every operator-installed program ([../design.md §5](../design.md#5-failure-retry-rollback-and-cleanup)) | [DH-D-MEDIA-2](#dh-d-media-2) |
| Code: one byte is read before the cache may answer (`tool-office/src/index.ts:264-270`) | A cached result must not bypass read authority | keep | Same rule: a cache hit needs a successful open by this caller | [DH-D-MEDIA-1](#dh-d-media-1) |
| Code: a selection over a limit fails, nothing is truncated; a model without image input is refused before any work (`tool-office/README.md:49`) | Half a document misleads | keep | Unchanged | [DH-D-MEDIA-3](#dh-d-media-3), [DH-D-MEDIA-4](#dh-d-media-4) |
| Code: outputs are never written into the workspace (`office-kit/README.md:39`) | Only the shell's sandbox governed workspace writes | change | In Go every file mutation passes one operation service and its policy ([DH-ADR-FS-1](filesystem.md#dh-adr-fs-1)) | [DH-ADR-MEDIA-4](#dh-adr-media-4) |
| Code: a second registration fails even under the same name, and the slot is held during shutdown (`browser-use/src/index.ts:36-45`) | No accidental double mount; no overlap while resources close | keep | Unchanged, as an effect on the provider's fiber | [DH-D-MEDIA-5](#dh-d-media-5), [DH-D-MEDIA-6](#dh-d-media-6) |

## 5. Tech debt and its resolution

Eighteen units, grouped by cause. Each unit's own reason is in its ledger row.

| Group (units) | Cause | How the Go design answers it |
|---|---|---|
| **Engine location and packaging** (3: `office-kit.1`, `.3`, `.5`) | The engine is an npm package resolved next to the provider, mapped out of a desktop archive, and never looked for elsewhere | Resolved for `.1` and `.3`: the provider discovers an installed LibreOffice (configuration, standard locations, `PATH`), reports path and version, and never installs or downloads ([DH-D-MEDIA-2](#dh-d-media-2)). `.5` is not applicable: archive path mapping exists only for an Electron package |
| **Engine inside the host** (1: `tool-office.5`) | A native parser runs in the host process, outside the sandbox | Resolved: one confined child process per job ([DH-ADR-MEDIA-3](#dh-adr-media-3)) |
| **Unbounded wait** (1: `office-kit.2`) | The deadline starts when conversion starts | Resolved: `queueWait` bounds the wait and ends as `office/busy`; the tool pipeline's deadline covers both ([DH-ADR-MEDIA-2](#dh-adr-media-2)) |
| **Getting results to the user** (5: `office-kit.4`, `tool-office.1`, `.2`, `.3`, `.4`) | The filesystem service wrote no binary files and the client showed no images, so outputs sat in a private store for a shell copy, remote workspaces could not convert, pages could not be saved, and the user did not see what the model saw | Resolved: `office_convert` and `office_recalculate` write the result to a path the model names through the filesystem operation service, which works on any execution world; `office_render` can also save its PNG files; the result carries image blocks the host renders ([DH-ADR-MEDIA-4](#dh-adr-media-4), [DH-D-MEDIA-4](#dh-d-media-4)). The output store is not built |
| **Activation kept in memory** (3: `tool-office.6`, `.7`, `.8`) | Activation is an in-memory mark plus a look at the last request header | Resolved: activation is a fold over the session log, so it survives resume, fork and reload, does not depend on a script-calling mode, and is already true when the step that follows a user's skill invocation resolves its tools ([DH-ADR-MEDIA-4](#dh-adr-media-4)) |
| **Fidelity** (1: `tool-office.9`) | LibreOffice lays out differently from Microsoft Office | **Carried**: inherent in the engine, and it now also varies with the installed version. The result names the engine version ([§10](#10-open-questions-and-limitations)) |
| **A seam that holds only a name** (2: `browser-use.1`, `computer-use.1`) | Session isolation and desktop sharing are left to each provider or to "callers" | Resolved: the browser service owns the session-to-browser table and serialises calls ([DH-D-MEDIA-5](#dh-d-media-5)); the computer-use service holds a desktop lease per turn across every `dsh` process of the user ([DH-D-MEDIA-6](#dh-d-media-6)) |
| **The model cannot choose the provider** (2: `browser-use.2`, `computer-use.2`) | Configuration selects the provider | **Carried on purpose**: a tool argument must never select a backend, an endpoint or the user's own browser ([DH-ADR-MEDIA-1](#dh-adr-media-1)). What changes is the cost of switching: one config edit, and the model's tools stay the same |

## 6. Go design

### 6.1 Shape

All packages sit under `src/harness/internal/media` ([../design.md §4.2](../design.md#42-go-module-layout)). A service and the tools that expose it are separate components, so a preset that omits the tools pays no prompt tokens. The browser and computer-use providers live in the experimental family and belong to area X4; an import-direction test fails if a tools package imports a provider ([DH-ADR-ARCH-6](../upstream/architecture.md#dh-adr-arch-6)).

| Package | Component (config row) | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `office` | none (contract) | Keys `office.Key`, `office.EngineKey`; interfaces `Service`, `Engine`, `Rasterizer`; types and error codes | n/a | What tools, previews and engines share |
| `office/core` | `office.service` | `office.Service`: admission queue, job sharing, cache, archive and image limits, missing-font report | `office.EngineKey` (optional), retention (A07) | Bounds do not depend on the engine |
| `office/soffice` | `office.libreoffice` | `office.Engine`: discovery, slots, the confined headless child | `subprocess.HostKey` (A09), `policy.Resolver` (A10) | The only code that knows LibreOffice's command line |
| `office/raster` | part of `office.service` | `office.Rasterizer` | nothing | The only importer of the PDF rasteriser module ([../libs/deepseek-ai-libreoffice-kit.md](../libs/deepseek-ai-libreoffice-kit.md) §5) |
| `office/tools` | `office.tools` | Nothing; registers three tools, one prompt section and the fold `office.active` | `tools.Key`, `fs.Key`, `fs.OpsKey`; optional `office.Key`, `attachments.Key` | The model-facing consumer |
| `browser` | `browser.service` | `browser.Service` under `browser.Key`: provider slot, one browser per session, approval listener, navigation guard | `tools.Key`, session store; optional interactions (A17) | Rules that must not depend on the provider |
| `browser/tools` | `browser.tools` | Nothing; registers nine tools and a prompt section | `tools.Key`, `browser.Key`, `attachments.Key` | Off by default |
| `computer` | `computer.service` | `computer.Service` under `computer.Key`: provider slot, application grants, refused targets, desktop lease | `tools.Key`; optional interactions (A17) | The seam with no built-in provider |
| `computer/tools` | `computer.tools` | Nothing; registers eight tools and a prompt section | `tools.Key`, `computer.Key`, `attachments.Key` | Off by default |

The main interfaces (illustrative; `ctx` is a `context.Context`):

```go
package office
type Service interface {
    Status() Status                                         // available or unavailable{reason}; engine path, version, generation
    PDF(ctx, src Source) (Result, error)                    // previews
    Convert(ctx, src Source, t Target) (Result, error)      // Target{Format, Sheet}
    Recalculate(ctx, src Source, f Format) (Result, error)
    Images(ctx, src Source, sel Selection, lim ImageLimits) (Images, error)
}
type Source struct { // already authorised by the caller; Open runs only after admission
    Key, Ext string; Size int64; Version fs.Version
    Open func(ctx) (io.ReadCloser, fs.Version, error)
}
type Result interface { Open() (io.ReadCloser, error); Size() int64; Pages() int; MissingFonts() []string; Engine() EngineInfo; Release() }

package browser
type Service interface { Register(p Provider) (release func(), err error); Status() Status; For(ctx, session) (Browser, error) }
type Provider interface { Info() Info; Open(ctx, req OpenRequest) (Browser, error) } // OpenRequest{Session, Mode, Guard}
type Browser interface {
    Tabs(ctx, op TabOp) ([]Tab, error);  Page(ctx, tab) (Page, error) // Page{URL, Origin, Title, HasCredentialField}
    Navigate(ctx, tab, url string) (Page, error)
    Snapshot(ctx, tab, o SnapshotOptions) (Snapshot, error);  Screenshot(ctx, tab, o ShotOptions) (Image, error)
    Click(ctx, tab, t Target) error;  Type(ctx, tab, t Target, text string, submit bool) error
    PressKey(ctx, tab, key string) error;  Scroll(ctx, tab, t Target, dx, dy int) error;  WaitFor(ctx, tab, c Condition) error
    Close(ctx) error // launch: stop the process tree and remove the profile; attach: disconnect only
}

package computer
type Service interface { Register(p Provider) (release func(), err error); Status() Status }
type Provider interface {
    Info() Info                                             // name, version, Targeting: window | none
    Apps(ctx) ([]App, error);  Windows(ctx, app AppID) ([]Window, error) // App{ID, Name, Identity}
    Snapshot(ctx, w WindowRef) (Tree, error);  Screenshot(ctx, w WindowRef) (Image, error)
    Click(ctx, w WindowRef, t Target) error;  Type(ctx, w WindowRef, t Target, text string) error
    Key(ctx, w WindowRef, chord string) error;  Scroll(ctx, w WindowRef, t Target, dx, dy int) error
    Close(ctx) error
}
```

`office.Engine` is the interface of [../libs/deepseek-ai-libreoffice-kit.md](../libs/deepseek-ai-libreoffice-kit.md) §6 (`Info`, `ToPDF`, `Convert`, `Recalculate`), used as written. `Register` is an effect on the provider's fiber: a second registration fails while the first is held, even under the same name, and `release` waits for every open browser or running call to close first.

**How an office job runs.**

1. *Authorise.* The caller (a tool or the preview route) resolves the path through the session's filesystem seam and opens it once, so a cache hit can never answer a caller that may not read the file.
2. *Admit.* The service checks the reader, queue and source-byte bounds. A full queue, or a wait longer than `queueWait`, ends as `office/busy`.
3. *Read and identify.* The source is streamed into a new scratch directory as `source.<ext>` while its SHA-256 is computed; the file version is checked again after the read (`office/source-changed`). Size, file signature and, for ZIP-based formats, entry count and declared uncompressed size are checked by harness code before any engine sees the file ([DH-ADR-LIBREOFFICEKIT-2](../libs/deepseek-ai-libreoffice-kit.md#dh-adr-libreofficekit-2)).
4. *Share.* A job with the same generation, extension, operation and digest is joined, or its cached result returned.
5. *Run.* The engine takes a free slot and starts one confined `soffice` child for the job ([DH-ADR-MEDIA-3](#dh-adr-media-3)) under `timeout`.
6. *Check and keep.* The output must be one regular file inside the scratch directory, within `maxOutputBytes` and with the expected signature. It moves into the cache; the scratch directory is removed before any reader is answered.

**How each operation is done, and what the model is told without LibreOffice.**

| Operation (tool) | With LibreOffice | Without a usable LibreOffice |
|---|---|---|
| Render to PDF (previews; first half of page images) | `soffice --headless --convert-to pdf` on the scratch copy | Fails with `office/unavailable` |
| Page images (`office_render`) | Render to PDF, then rasterise the chosen pages in the host; limits are computed from page sizes and `dpi` before any pixel is allocated. A PDF input skips the engine. A worksheet `range` uses the scratch-copy method of the library spec §6 and fails with `office/invalid-request` where that method is not proven | Word, spreadsheet and presentation inputs fail with `office/unavailable`. A PDF input still renders |
| Convert (`office_convert`) | `--convert-to <format>` with the filter for the family; CSV names one worksheet | Fails with `office/unavailable` |
| Recalculate (`office_recalculate`) | A load and save under a slot profile set to "recalculate on load: always"; if the installed version ignores the setting the call fails as `office/unsupported-format`, never with stale numbers | Fails with `office/unavailable` |

The failure text is fixed: `cannot <operation> "<path>": LibreOffice was not found on this machine (<what was searched>). Tell the user that office tools need LibreOffice installed, or its path set in office.libreoffice.path; do not substitute another converter.` The same text with a different cause covers "found but older than the supported minimum" and "found, but no sandbox is available to run it in". The host starts normally in every case.

### 6.2 Contracts

**Tools exposed to the model.** Names are fixed by this area and never by a provider ([DH-ADR-MEDIA-1](#dh-adr-media-1)). Each is one `tools.Spec` declaration ([DH-ADR-TOOLS-1](tools.md#dh-adr-tools-1)).

| Tool | Arguments | Result | Replaces | Change from upstream |
|---|---|---|---|---|
| `office_render` | `input`, `pages?`, `sheet?`, `range?`, `dpi?` (24 to 600, default 144), `save_dir?` | Summary, missing fonts, engine version, then a label and an image block per page | `UP-TOOL-dsh-tool-office.office_render` | `save_dir` also writes each PNG as a file; a spreadsheet without `range` is rendered as its printed pages |
| `office_convert` | `input`, `format`, `sheet?` (CSV only), `output`, `overwrite?` | Path written, format, bytes, page count, missing fonts, engine version | `UP-TOOL-dsh-tool-office.office_convert` | `output` is required and the file is written there; no temporary file and no shell copy |
| `office_recalculate` | `input`, `format` (`xlsx`, `ods`), `output`, `overwrite?` | As `office_convert` | `UP-TOOL-dsh-tool-office.office_recalculate` | The same |
| `browser_navigate`, `browser_tabs`, `browser_snapshot`, `browser_screenshot`, `browser_click`, `browser_type`, `browser_press_key`, `browser_scroll`, `browser_wait_for` | An optional `tab`; a target is an element `ref` from the last snapshot or a point in the last screenshot | Text; a screenshot is an image block by attachment reference. A result says input was delivered, never that it had the intended effect | none (upstream: three provider-owned catalogues) | One catalogue for every provider |
| `computer_list_apps`, `computer_list_windows`, `computer_snapshot`, `computer_screenshot`, `computer_click`, `computer_type`, `computer_key`, `computer_scroll` | Every tool but the first takes `app` and `window`, both from the latest listing | As for the browser tools | none (upstream: provider-owned catalogues) | One catalogue; the application is always an argument |

The office tools are declared with a `When` condition that reads the fold `office.active`. `office_render` is parallel-safe; the other two are exclusive because they write files. Every browser and computer tool is exclusive.

**What an external MCP server must offer to be a computer-use provider** (the provider profile). Area X4's adapter maps these onto `computer.Provider`; the adapter and its per-server recipe are X4's.

| # | The server must | Why |
|---|---|---|
| 1 | Be a local program that speaks MCP on its standard input and output, started from a configured `command` and `args` with no shell, and report a name and version when it initialises | The host owns the child ([DH-ADR-MCP-3](../libs/modelcontextprotocol.md#dh-adr-mcp-3)); the version is recorded |
| 2 | Offer a tool that lists running applications: an identifier that stays valid while the server runs, a display name and, where the system has one, a stable identity (bundle identifier or executable path) | A grant names an application |
| 3 | Offer a tool that lists one application's windows: identifier, title, size | A call is attributed to a window and through it to an application |
| 4 | Offer observation tools that take a window identifier: an accessibility tree whose actionable elements carry references, and a screenshot of that window only, as MCP image content | Nothing outside the granted application is read |
| 5 | Offer input tools that take a window identifier and an element reference or a point in that window's screenshot: click, type text, press keys, scroll | Nothing outside the granted application is acted on |
| 6 | Report delivery and not effect, and report a stale reference, a vanished window or a missing operating-system permission as an error, without opening a system prompt during a call | The model re-observes; the user grants system permissions by hand |
| 7 | Honour MCP cancellation, and exit when its input closes | An owned lifetime with no orphan |
| 8 | Need nothing else from the host: no sampling, no elicitation, no harness-specific method | A provider calls no model ([DH-O-4](../design.md#dh-o-4)) and can never answer an approval |

A server that meets 1 and 6 to 8 but offers only whole-screen capture and global input can still register, with `Targeting: none`: `computer_list_apps` then returns the single entry `desktop`, and the only grant that can be asked for is shown as "the entire desktop, every application". A server that meets less is not a provider. **With no provider registered**, or with one whose program is missing, every `computer_*` call fails with `computer/unavailable`: `Computer use is not available: no computer-use provider is installed. An operator must install an MCP server that provides desktop control and name it in the configuration. Tell the user; do not try to control the desktop by other means.` The status view shows the same sentence with the four operator steps of [../libs/cua-helper.md](../libs/cua-helper.md) §6. Nothing is started and nothing is downloaded.

**Approval rules.** Each service registers one policy listener on `tools.Decide` and one guard ([tools.md §6.2](tools.md#62-contracts), stages 2 and 3). A listener returns `Ask` with a scope; the user answers once, or grants that scope until the session ends ([DH-ADR-16](../design.md#dh-adr-16)). Only the owner answers; with no answerer the answer is no.

| Call | Launched browser, origin not signed-in | Launched browser, signed-in origin | Attached browser (every origin) |
|---|---|---|---|
| Observe (`snapshot`, `screenshot`, `scroll`, `wait_for`, listing tabs) | The session's ordinary policy | The session's ordinary policy | Ask, scope `browser-origin:<origin>` |
| Act (`navigate`, scoped to the destination; `click`, `type`, `press_key`; opening or closing a tab) | The session's ordinary policy | Ask, scope `browser-origin:<origin>` | Ask, the same scope |
| Typing into a password or one-time-code field | Refused by the guard: `browser/credential-field`. The user signs in by hand in the browser window | Refused | Refused |
| First call in a session | n/a | n/a | Ask, scope `browser-attach` |

The guard also refuses a URL that is not `http` or `https`, a document request to a loopback, link-local, private or metadata address unless the operator allowed it ([DH-ADR-SECURITY-10](../upstream/security.md#dh-adr-security-10)), and, for a signed-in origin or an attached browser, a cross-origin form submission to an origin that has no grant. For computer use: `computer_list_apps` follows the session's ordinary policy; every other call is `Ask` with scope `app:<identity>`; the guard refuses, before any prompt, a window of the harness's own interface where it can be recognised, operating-system credential and privacy prompts, and terminals and password managers the operator has not listed (rules (c) to (g) of [DH-ADR-CUADRIVER-2](../libs/trycuacua-driver.md#dh-adr-cuadriver-2)).

**Events.** Upstream has none in this area. All three are live, in the notify form of [../design.md §4.6](../design.md#46-event-model).

| Go key | Form | Payload | Replaces |
|---|---|---|---|
| `office.StatusChanged` | notify | `Status{State, Reason, Engine{Path, Version}, Generation, Confinement}` | none; lets the status view and a preview refresh |
| `browser.StatusChanged` | notify | `Status{State, Reason, Provider, Version, Mode, Open}` | none |
| `computer.StatusChanged` | notify | `Status{State, Reason, Provider, Version, Targeting, LeaseHolder}` | none |

**Config.** Go structs with defaults as Go values; decoding is strict. Upstream defaults are kept where a field survives.

| Component | Replaces | Fields (default) |
|---|---|---|
| `office.service` | `UP-CFG-dsh-office-kit` | `maxConcurrent` (2), `maxQueued` (8), `maxReaders` (32), `queueWait` (120 s, new), `timeout` (60 s), `maxInputBytes` (50 MiB), `maxOutputBytes` (100 MiB), `maxSourceBytes` (100 MiB, at least `maxInputBytes`), `maxCachedEntries` (8), `maxCachedBytes` (128 MiB), `maxSourceEntries` (64), `pdfImageDPI` (192), `maxArchiveEntries` (10,000), `maxUncompressedBytes` (250 MiB), `fontDirectories` (platform defaults), `maxFontFiles` (20,000), `maxFontFileBytes` (256 MiB). Not carried: `maxBackgroundConversions`, the three output-store fields, and `fontFallbacks` and `maxLoadedFontBytes`, which configure the kit's own font loader |
| `office.libreoffice` | none (new) | `path` (unset: discover), `confinement` (`required`; also `strict`, `off`), `reprobeInterval` (30 s), `minVersion` (open, [§10](#10-open-questions-and-limitations)) |
| `office.tools` | `UP-CFG-dsh-tool-office` | `activationSkills` (`office`, `office-docx`, `office-xlsx`, `office-pptx`), `maxImages` (20), `maxImagePixels` (8,388,608), `maxImageDimension` (8,192), `maxImageBytes` (32 MiB) |
| `browser.service` | none (new) | `mode` (`launch`; or `attach`), `attachEndpoint` (unset; loopback only), `maxBrowsers` (4), `idleTimeout` (0: off), `downloads` (`deny`), `allowPrivateAddresses` (false), `preGrantedOrigins` (none), `snapshotMaxNodes` (1,500) |
| `computer.service` | none (new) | `allowedSensitiveTargets` (none), `preGrantedApps` (none) |

**Services.** `UP-SVC-officeKit` becomes `office.Key` with `office.Service`; `UP-SVC-browserUse` becomes `browser.Key` with `browser.Service`; `UP-SVC-computerUse` becomes `computer.Key` with `computer.Service`.

**Persisted data.** One durable event type, `browser/state` (log-only, ignorable, the whole value: `opened`, `closed` or `lost`, with provider, mode and browser version), so that after a resume or fork the first browser result can say that earlier pages and logins are gone. One fold, `office.active` (true once the log records a successful load of a skill named in `activationSkills`). Grants are A17's records. Nothing else is stored: the cache, scratch directories and browser profiles are runtime files.

**Outcome codes owned by this area.** `office/unavailable`, `office/busy`, `office/input-too-large`, `office/output-too-large`, `office/invalid-document`, `office/unsupported-format`, `office/invalid-request`, `office/invalid-output`, `office/timeout`, `office/source-changed`, `office/failed`, `office/model-has-no-image-input`; `browser/unavailable`, `browser/busy`, `browser/unknown-tab`, `browser/stale-reference`, `browser/navigation-refused`, `browser/credential-field`, `browser/download-refused`, `browser/lost`; `computer/unavailable`, `computer/busy`, `computer/stale-reference`, `computer/target-refused`, `computer/unsupported`, `computer/lost`.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Class | Owner (releases it) | Who merely has access |
|---|---|---|---|
| The installed LibreOffice, the user's browser in attach mode, the installed MCP server program, the desktop | shared | The operator or the user. The harness never modifies, closes or updates them | The harness may start or connect to them |
| A `soffice` child and its process tree | created | The job that started it, under the `office.libreoffice` fiber | n/a |
| A scratch directory | created | Its job; removed before the result returns on every path | The engine child, as its only write root with the slot profile |
| Slot profiles, the cache, the process's run directory | created | The `office.libreoffice` and `office` fibers; removed on unload. The run directory holds a kernel lock, so a later start removes run directories whose lock is free and leaves every other entry alone | Readers hold a cached result open until they release it |
| A file written by `office_convert`, `office_recalculate` or `save_dir` | created, then the user's | Nobody in the harness after the write; it is an ordinary workspace file | The session, through the filesystem seam |
| A launched browser, its pipe, profile and download directory | created | The `browser` fiber, for one session; closed with the session | That session's agents, one call at a time |
| The connection to an attached browser | created | The same; teardown disconnects and does not close | The one session holding the reservation |
| A desktop lease | created | The session's turn; released when `turn/end` commits or the session closes | n/a |
| A session grant for a website (browser use) or an application (computer use) | created | The one live session it was given to (A17); ends with it or on revocation | That session only; never a resumed session, a fork or a subagent ([DH-ADR-HUMAN-5](interaction-credentials.md#dh-adr-human-5)) |

| Situation | What happens | Undone, and what stays uncertain |
|---|---|---|
| LibreOffice is missing, too old, or no sandbox can run it | `office/unavailable` with the cause; discovery runs again at most every `reprobeInterval`, so installing it needs no restart | Nothing ran |
| The engine exceeds `timeout` or the last reader cancels | The whole process tree is stopped by the stop ladder of [execution.md](execution.md); `office/timeout` or the cancellation cause; the slot profile is deleted and rebuilt, because a killed LibreOffice can leave it locked | Nothing outside the scratch directory was writable |
| The engine crashes or returns a bad output | `office/failed` or `office/invalid-output`; diagnostics go to the log only; no automatic retry, because the same document fails the same way | The scratch directory is removed |
| The output write fails half-way | The filesystem provider's staged replace leaves the destination untouched ([filesystem.md](filesystem.md) §6.1); `save_dir` reports which page files were written | **Uncertain:** nothing; earlier page files stay and are listed |
| The host dies during a job | What the tree owner of [execution.md](execution.md) §6.1 allows: on Windows the whole tree dies, on Linux the direct child; the next start sweeps the dead run directory | **Uncertain:** on macOS the process group survives the host, and on Linux so may a descendant; survivors are reported at the next start and are still confined |
| The engine's path or version changes | A new generation; cached results of the old one are dropped | n/a |
| A browser cannot start, or its program is not found | `browser/unavailable` naming what to install; the tree is stopped and the profile removed before the error returns | Nothing is reserved |
| A browser crashes, or a provider unloads | Pending calls fail as `browser/lost`; `browser/state` records it; the next call opens a fresh browser and says earlier pages and logins are gone | No call is replayed |
| A browser or computer call is cancelled | The result says the outcome is unknown and tells the model to observe before retrying | **Uncertain:** whether input was delivered. Nothing on a page or a desktop is ever rolled back |
| A browser's process cannot be confirmed dead, or its profile cannot be removed | The session's entry is kept, the path is logged, and the status reports the state as uncertain | **Uncertain:** the leftover process or directory |
| The computer-use server crashes or hangs | Pending calls fail as `computer/lost`; its fiber restarts it under a budget (A15); the slot stays registered | **Uncertain:** what the server did before it failed |
| A second session calls computer use while a lease is held | `computer/busy` naming the holder; nothing waits | Nothing ran |
| A grant is revoked | That session's running calls for the origin or application are cancelled and awaited; the next call asks again | Delivered input stays delivered |

## 7. Decisions

<a id="dh-adr-media-1"></a>
### DH-ADR-MEDIA-1 Each capability has one fixed tool catalogue; a provider implements a Go interface and fills one exclusive slot

- **Decision.** The model-facing tools for office documents, browser use and computer use are declared by this area and do not change with the provider. A provider implements `office.Engine`, `browser.Provider` or `computer.Provider`. Browser use and computer use each have one provider slot; registration is exclusive and is held until the provider's resources are closed. No tool argument can select a provider, an endpoint or a mode: configuration does. A provider that offers tools of its own (an MCP server's catalogue) is reached through an adapter, never passed through under its own names.
- **Why.** [DH-O-10](../design.md#dh-o-10) requires that a provider change leave the model's tool schema unchanged. Upstream chose provider-owned catalogues because "no current consumer requires interchangeable action methods" (`.agents/notes/implemented/architecture/2026-09-12-browser-use-provider-registration.md:31`). Here the outcome is that consumer, and [DH-ADR-24](../design.md#dh-adr-24) reduces every browser backend to the same plain actions. A harness-owned catalogue is also the only place where approval can read the target (an origin, an application) from the arguments.
- **Tradeoff.** A backend's special abilities are out of reach until the catalogue grows. Each MCP-based backend needs a mapping that someone maintains. The library specifications follow this decision: requirement `DH-LIB-CUADRIVER-R2` in [../libs/trycuacua-driver.md](../libs/trycuacua-driver.md) now has the fixed catalogue, and [DH-ADR-STAGEHAND-2](../libs/browserbasehqstagehand.md#dh-adr-stagehand-2) left the question to this area.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-MEDIA-9`, `DH-T-MEDIA-10`, `DH-T-MEDIA-13`, [DH-E-MEDIA-4](#dh-e-media-4).

<a id="dh-adr-media-2"></a>
### DH-ADR-MEDIA-2 One office service with a bounded shared queue; LibreOffice is discovered, and runs as one process per job

- **Decision.** (a) The office service keeps upstream's admission bounds, job sharing by content digest and generation, and "the last reader cancels". It has no priorities. Source bytes and results are files, not memory. (b) The LibreOffice engine is found in this order: the configured `path`; a fixed list of standard install locations per operating system; `soffice` on `PATH`. It is probed once (`soffice --version`, confined, under a deadline) and its path, version and file identity form the generation with the rendering settings. Discovery runs at start without blocking it, when the configuration changes, and on a call that finds no engine if the last probe is older than `reprobeInterval`. (c) Each job is one `soffice` process on one of `maxConcurrent` slots, each slot with its own profile directory. There is no warm process and no pool of listeners. (d) Two deadlines: `queueWait` for the wait and `timeout` for the run; the tool's pipeline budget is their sum plus rasterising time.
- **Why.** The bounds are a resource-safety rule that does not depend on the engine. One process per job is what [DH-ADR-LIBREOFFICEKIT-3](../libs/deepseek-ai-libreoffice-kit.md#dh-adr-libreofficekit-3) decided; this area adds the reason not to pool: a LibreOffice kept listening needs a client for its remote-control protocol, for which no Go implementation was verified, and a warm process carries state from one untrusted document into the next. A fresh process gives every job a clean engine and a kill switch.
- **Tradeoff.** Every job pays a process start (unmeasured), so the cache matters and a first preview is slow. Two machines can render differently; the engine version is in every result. A search of standard locations can find a LibreOffice the operator did not intend, so the status view shows the path, and `path` overrides the search.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-6](../design.md#dh-o-6), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-MEDIA-1`, `DH-T-MEDIA-2`, `DH-T-MEDIA-3`, [DH-E-MEDIA-1](#dh-e-media-1), [DH-E-MEDIA-2](#dh-e-media-2).

<a id="dh-adr-media-3"></a>
### DH-ADR-MEDIA-3 The office engine is confined, and fails closed where it cannot be

- **Decision.** The `soffice` child is started through `subprocess.HostKey`, always on the host machine, under a sandbox policy that area A10 resolves: read-only, with the job's scratch directory and the slot's profile as the only write roots, the network denied, and the secret directory masked where the backend can. Its environment is built explicitly and holds no credential; its working and temporary directories are the scratch directory. The slot profile is written by the harness with macros disabled, link updates and update checks off. The engine only ever sees a copy of the document. `confinement` selects what happens where a backend cannot do all of this: `required` (default) runs with what the backend governs, reports the rest, and refuses when no backend works; `strict` refuses unless enforcement is full; `off` is the operator's explicit choice to run unconfined, and is reported as such.
- **Why.** A document is untrusted input to a very large native parser. Upstream runs that parser in the host with the user's permissions and lists it as a limitation (`packages/document/tool-office/README.md:111`). A process boundary plus a write fence means a compromised engine cannot change the workspace, the harness home or the user's files, and, where the network is denied, cannot send what it read anywhere.
- **Tradeoff.** Reads are unconfined on every platform ([../design.md §6](../design.md#6-security-model)), so the guarantee is "cannot write, and where enforced cannot connect", not "cannot read". On Windows the sandbox refuses a network-denied policy ([sandbox-ssh.md](sandbox-ssh.md) §6.1), so under `required` the engine runs there with the network allowed and the status says `partial`; `strict` makes the office tools unavailable on Windows. A machine with no working sandbox has no office tools unless the operator sets `off`.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-MEDIA-4`, `DH-T-MEDIA-5`, [DH-E-MEDIA-1](#dh-e-media-1), [DH-E-MEDIA-3](#dh-e-media-3).

<a id="dh-adr-media-4"></a>
### DH-ADR-MEDIA-4 Office tools write their results through the filesystem seam, and their activation is read from the log

- **Decision.** (a) `office_convert` and `office_recalculate` take a required `output` path and write the result there through the filesystem operation service, with the "create only if absent" guard, or with a version-checked replace when `overwrite` is true. `office_render` does the same for each PNG when `save_dir` is given. There is no private output store, no temporary path in a result and no shell copy. (b) The three tools are declared when the fold `office.active` is true, which the session log alone decides.
- **Why.** Upstream kept outputs out of the workspace because only the shell's sandbox governed binary writes (`.agents/notes/implemented/architecture/2026-10-31-native-office-tool.md:17`, `:35`). In Go every mutation passes one service that resolves the policy, runs the intent listeners and records the change ([DH-ADR-FS-1](filesystem.md#dh-adr-fs-1)), so the reason is gone, and with it five limitations: the shell copy, no conversion on a remote workspace, no saved page images, and the retention rules of a second store. A fold replaces the in-memory mark for the same reason that the session log is the single source of truth: resume, fork and reload then need no special case.
- **Tradeoff.** The model must name a destination before it has seen the result, and a write may raise an approval that a read-only render does not. Results up to `maxOutputBytes` pass through host memory once on their way to the filesystem provider. The tools' arguments differ from upstream's, which [DH-ADR-2](../design.md#dh-adr-2) allows.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-MEDIA-6`, `DH-T-MEDIA-7`, `DH-T-MEDIA-8`, [DH-E-MEDIA-1](#dh-e-media-1).

<a id="dh-adr-media-5"></a>
### DH-ADR-MEDIA-5 The browser service owns one browser per session, isolates it, and asks per origin where the user's logins are at stake

- **Decision.** (a) The service, not the provider, keeps the table from session to browser, opens a browser on the first tool call, runs one call at a time per session, bounds the number of browsers, and closes each with its session. A fork, a resumed session and a subagent each get a fresh browser. (b) A launched browser is found at the provider's configured path, or else at the well-known install locations of stable Chrome (Chromium and Edge once a scenario proves them); none means `browser/unavailable`, and nothing is downloaded ([../libs/puppeteerbrowsers.md](../libs/puppeteerbrowsers.md)). It is started by the one process starter with an explicitly built environment and a new owner-only profile directory under the harness home, removed on close, and is driven over an inherited pipe with no listening debug port ([DH-ADR-STAGEHAND-3](../libs/browserbasehqstagehand.md#dh-adr-stagehand-3)). An attached browser is reached at an endpoint named only in configuration, on a loopback address unless the operator says otherwise. Downloads are refused by default, uploads are not offered, and no tool runs script in a page. (c) Approval follows the table in [§6.2](#62-contracts). An origin counts as **signed-in** when the browser is attached, or when a launched browser has shown a password field on a page of that origin. The model can never type into a credential field.
- **Why.** Isolation written once in the service cannot be forgotten by a provider. The harness cannot tell reliably whether a site is logged in, so it does not guess from cookies: it goes by whose browser state it is, and by the one observable sign that credentials may have been entered. Asking per origin with a session grant is the middle path of [DH-ADR-16](../design.md#dh-adr-16): the user decides once per site. The owner decided that a grant may name one website for browser use, as "one application" ([DH-ADR-33](../design.md#dh-adr-33)): the scope `browser-origin:<origin>` is A17's `website` kind, "allow for this session" is offered on the prompt, and the grant is never copied to a resumed session, a fork or a subagent.
- **Tradeoff.** A site that shows a login box is treated as signed-in even if nobody logs in, which costs a prompt. A site signed in by a link or a stored token with no password field is missed in launch mode. Inside a granted origin the model acts without further prompts, and page text can steer it. A headless launched browser cannot be signed in to at all. Sub-resource requests are not checked by the navigation guard.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-MEDIA-10`, `DH-T-MEDIA-11`, `DH-T-MEDIA-12`, [DH-E-MEDIA-4](#dh-e-media-4).

<a id="dh-adr-media-6"></a>
### DH-ADR-MEDIA-6 Computer use is a seam filled only by an external MCP server that meets a stated profile, approved per application, one session at a time

- **Decision.** (a) The harness contains no computer-use provider. The slot is filled only by area X4's adapter in front of an MCP server the operator installed that meets the provider profile of [§6.2](#62-contracts). (b) The tools entry is off by default; enabled without a provider, every call reports `computer/unavailable` with the fixed sentence. (c) Every call except the application list needs an application grant, asked through the interaction registry, one-shot or for the session; the service checks that the `window` argument was listed under the `app` argument in that session's latest listing, so a grant for one application cannot be spent on another. (d) A session holds the desktop lease for the length of a turn; the lease is a kernel file lock in the user's runtime directory, so it also holds between separate `dsh` processes. (e) The adapter's MCP connection is private: its server's own tool names are never offered to the model.
- **Why.** [DH-ADR-19](../design.md#dh-adr-19) leaves an external server as the only route and notes that a grant naming an application "can be enforced only where a tool's arguments identify the application". A fixed catalogue in which the application is always an argument makes that true for every provider. Upstream left desktop sharing to callers because a broker "requires an explicit acquisition and release policy" (`.agents/notes/implemented/architecture/2026-09-12-computer-use-provider-registration.md:29`); a turn is that policy, and it cannot deadlock because nothing waits.
- **Tradeoff.** The harness trusts the server's own report of which application a window belongs to; an identity is a name, not proof of a publisher. Whether a given server can be mapped is unknown until its tool list is read ([§10](#10-open-questions-and-limitations)). The user, and any program that is not `dsh`, still share the desktop. Desktop control bypasses file confinement while it is enabled, and the status view says so.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-MEDIA-13`, `DH-T-MEDIA-14`, `DH-T-MEDIA-15`, [DH-E-MEDIA-5](#dh-e-media-5).

## 8. Deliverables

<a id="dh-d-media-1"></a>
### DH-D-MEDIA-1 The office service: bounded, shared, cached document operations

- **Result and why it matters.** Any caller gets a PDF, a converted file, a recalculated workbook or page images within fixed bounds, and two callers asking for the same work share it. Without it a few large documents could exhaust memory or start an unbounded number of engines.
- **How it is implemented.** The six steps of [§6.1](#61-shape). Admission is a small state machine under one mutex; a job's context derives from the service's fiber and is cancelled when its last reader leaves. The cache is a bounded, least-recently-used set of read-only files in the process's run directory, with a source-version index that skips re-reading known content. Archive limits and the missing-font report are the harness code of [DH-ADR-LIBREOFFICEKIT-2](../libs/deepseek-ai-libreoffice-kit.md#dh-adr-libreofficekit-2). `Status` and `office.StatusChanged` report the engine.
- **Components and files.** `internal/media/office`, `internal/media/office/core`.
- **Depends on.** Implementation: [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1); [DH-D-STORAGE-9](storage.md#dh-d-storage-9) (a retention step that sweeps dead run directories). Runtime prerequisite: none; with no engine it answers `office/unavailable`.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-6](../design.md#dh-o-6). **Priority.** P1.
- **Done when.** `DH-T-MEDIA-1`, `DH-T-MEDIA-2`, `DH-T-LIBREOFFICEKIT-2` and `DH-T-LIBREOFFICEKIT-4` pass and [DH-E-MEDIA-1](#dh-e-media-1) has passed.

<a id="dh-d-media-2"></a>
### DH-D-MEDIA-2 The LibreOffice engine: discovered, started headless under confinement, and cleaned up

- **Result and why it matters.** On a machine with LibreOffice the four operations work with no setup; on a machine without it the host starts and says what to install. An untrusted document is parsed by a child that can write only its own scratch and profile.
- **How it is implemented.** [DH-ADR-MEDIA-2](#dh-adr-media-2) (b) to (d) and [DH-ADR-MEDIA-3](#dh-adr-media-3). The command line and the recalculation and CSV settings are those of [../libs/deepseek-ai-libreoffice-kit.md](../libs/deepseek-ai-libreoffice-kit.md) §6. Standard output and error are collected with a bounded tail for the log. The process tree is stopped by the stop ladder on deadline, cancellation and unload; a killed slot's profile is rebuilt.
- **Components and files.** `internal/media/office/soffice`.
- **Depends on.** Implementation: [DH-D-MEDIA-1](#dh-d-media-1); [DH-D-EXEC-1](execution.md#dh-d-exec-1), [DH-D-EXEC-2](execution.md#dh-d-exec-2) and [DH-D-EXEC-4](execution.md#dh-d-exec-4) (`subprocess.HostKey`, `Lookup`, tree stop, a required launch plan); [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) and [DH-D-SANDBOX-3](sandbox-ssh.md#dh-d-sandbox-3) (`Resolver.ForHelper`: read-only, named write roots, network denied, a required level; then `Prepare`). Runtime prerequisite: an installed LibreOffice at or above the minimum version, and a working sandbox backend unless `confinement: off`.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7), [DH-O-1](../design.md#dh-o-1), [DH-O-14](../design.md#dh-o-14). **Priority.** P1.
- **Done when.** `DH-T-MEDIA-3`, `DH-T-MEDIA-4`, `DH-T-MEDIA-5`, `DH-T-LIBREOFFICEKIT-1`, `DH-T-LIBREOFFICEKIT-3` and `DH-T-LIBREOFFICEKIT-6` pass, and [DH-E-MEDIA-1](#dh-e-media-1), [DH-E-MEDIA-2](#dh-e-media-2) and [DH-E-MEDIA-3](#dh-e-media-3) have passed.

<a id="dh-d-media-3"></a>
### DH-D-MEDIA-3 Page images within fixed limits

- **Result and why it matters.** The model can look at chosen pages or slides as PNG images, and a selection that would exceed a limit fails before memory is spent.
- **How it is implemented.** The chosen pages of the PDF are rasterised in the host behind the `office.Rasterizer` interface, with the module and the fallback (one LibreOffice export per page) of the library spec §5. No PDF rasteriser is embedded, and the fallback is the only implementation, so page images need LibreOffice (Recommended default, not confirmed by the owner; [§10](#10-open-questions-and-limitations)). Image count, pixels per image, side length and total bytes are computed from the PDF's page sizes and `dpi` first. A worksheet `range` is [DH-D-MEDIA-7](#dh-d-media-7); until it is built, a request that names one fails with `office/invalid-request`.
- **Components and files.** `internal/media/office/raster`, the `Images` path of `internal/media/office/core`.
- **Depends on.** [DH-D-MEDIA-1](#dh-d-media-1); [DH-D-MEDIA-2](#dh-d-media-2) (for non-PDF inputs); [DH-LIB-LIBREOFFICEKIT](../libs/deepseek-ai-libreoffice-kit.md).
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-6](../design.md#dh-o-6). **Priority.** P1.
- **Done when.** `DH-T-LIBREOFFICEKIT-5` and `DH-T-MEDIA-2` pass and [DH-E-MEDIA-1](#dh-e-media-1) has passed.

<a id="dh-d-media-7"></a>
### DH-D-MEDIA-7 A worksheet area as an image

- **Result and why it matters.** The model can look at one cell range of a spreadsheet as an image, for a chart or a formatted table that text does not show. Split out of [DH-D-MEDIA-3](#dh-d-media-3) so that its lower priority has its own entry.
- **How it is implemented.** The scratch-copy method of the library spec §6: a copy of the workbook in the job's run directory whose print area is set to the range, exported to PDF by the engine and rasterised by the path of [DH-D-MEDIA-3](#dh-d-media-3) under the same limits. A range that names no cell, an unknown sheet or a range above the cell limit fails with `office/invalid-request` before the engine starts.
- **Components and files.** `internal/media/office/core` (range path), `internal/media/office/soffice`.
- **Depends on.** [DH-D-MEDIA-3](#dh-d-media-3); [DH-D-MEDIA-2](#dh-d-media-2).
- **Serves.** [DH-O-10](../design.md#dh-o-10). **Priority.** P2.
- **Done when.** `DH-T-MEDIA-16` passes and [DH-E-MEDIA-6](#dh-e-media-6) has passed.

<a id="dh-d-media-4"></a>
### DH-D-MEDIA-4 The three office tools

- **Result and why it matters.** After an Office skill loads, the model can render, convert and recalculate documents with one call each, on a local or a remote workspace, and the user sees the pages the model saw. Sessions that never load an Office skill pay no tokens for the tools.
- **How it is implemented.** Three declarations with the arguments of [§6.2](#62-contracts) and a shared `When` on `office.active`. Each body checks the argument rules the schema cannot express, opens the input through `fs.Key`, calls the office service, and maps its error to the fixed texts. `office_render` refuses a model without image input before any work, takes the lower of its limits and the attachment store's, and admits the images as attachments before the result is built; its `View` carries the image blocks. Writes go through the filesystem operation service ([DH-ADR-MEDIA-4](#dh-adr-media-4)).
- **Components and files.** `internal/media/office/tools`.
- **Depends on.** Implementation: [DH-D-MEDIA-1](#dh-d-media-1), [DH-D-MEDIA-3](#dh-d-media-3); [DH-D-TOOLS-1](tools.md#dh-d-tools-1) and [DH-D-TOOLS-3](tools.md#dh-d-tools-3) (the declaration, `When`, `Deadline`); [DH-D-FS-4](filesystem.md#dh-d-fs-4) (reading through `fs.Key`, and a write on the operation service with the same policy, intent and recording steps as a text write); [DH-D-STORAGE-3](storage.md#dh-d-storage-3) and [DH-D-STORAGE-4](storage.md#dh-d-storage-4) (`attachments.AdmitImages` and its image limits); [DH-D-CONTEXT-9](context-compaction-skill.md#dh-d-context-9) (a logged record of a successful skill load, direct or by the user, committed before the next step resolves its tools). The tool card that shows a result's image blocks depends on this ([DH-D-UICONV-4](ui-conversation.md#dh-d-uiconv-4)).
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7). **Priority.** P1.
- **Done when.** `DH-T-MEDIA-6`, `DH-T-MEDIA-7` and `DH-T-MEDIA-8` pass and [DH-E-MEDIA-1](#dh-e-media-1) has passed.

<a id="dh-d-media-5"></a>
### DH-D-MEDIA-5 The browser-use seam: one catalogue, one provider slot, one isolated browser per session, approval per origin

- **Result and why it matters.** An operator can change the browser backend with one configuration edit and the model's tools do not change. Sessions never see each other's pages, and an agent cannot act as the user on a signed-in site, reach a private address or leave a file on disk without the stated approval or setting.
- **How it is implemented.** The contract package, the exclusive `Register`, the session table with a per-session call queue, lazy open, `maxBrowsers`, `idleTimeout`, close on `session.Closed`, the `browser/state` event and the nine tool declarations. A snapshot is bounded by `snapshotMaxNodes` and marks truncation; a reference from an older snapshot is refused before any input is sent. Approval is the policy listener and guard of [§6.2](#62-contracts): the service asks the provider for the tab's `Page` before each call and keeps the set of signed-in origins per browser, and the provider is handed a `Guard` at open that it must call for every top-level document request. Downloads are refused through the provider; a `quarantine` mode (a bounded per-session directory with a guarded save into the workspace) is P2 inside this deliverable. A conformance suite (`browsertest`) runs against a fake provider here and against each real provider in area X4, and fails a provider that skips the guard.
- **Components and files.** `internal/media/browser` (the service, `approve.go`, `guard.go`), `internal/media/browser/tools`, `internal/media/browser/browsertest`.
- **Depends on.** Implementation: [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1); [DH-D-TOOLS-1](tools.md#dh-d-tools-1) and [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (the declaration, `tools.Decide`, guards); [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (screenshots as attachments); [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2) and [DH-D-HUMAN-3](interaction-credentials.md#dh-d-human-3) (an approval with a subject, owner-only answering, grants matched at the approval stage, `grant.Changed` on revocation); [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) (`ForHelper` with the reported `Unconfined` flag for the session's browser, which cannot run inside the file sandbox and is still an untrusted tree). At least one provider must exist ([DH-D-XCUA-2](x-cua-browser-voice.md#dh-d-xcua-2), which depends on this). Runtime prerequisite: an installed Chromium-family browser, or a running one for attach mode.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7), [DH-O-4](../design.md#dh-o-4). **Priority.** P2.
- **Done when.** `DH-T-MEDIA-9` to `DH-T-MEDIA-12`, `DH-T-STAGEHAND-2` and `DH-T-STAGEHAND-5` pass and [DH-E-MEDIA-4](#dh-e-media-4) has passed.

<a id="dh-d-media-6"></a>
### DH-D-MEDIA-6 The computer-use seam: one catalogue, the provider profile, a plain "unavailable", application grants and the desktop lease

- **Result and why it matters.** The product has a defined place for computer use and says exactly what fills it. Until an operator installs a provider the tools say so in one sentence and nothing else happens. With one, the user decides application by application what an agent may see and touch, and two agents never drive one desktop at the same time.
- **How it is implemented.** The contract package, the exclusive `Register`, the eight tool declarations, the per-session record of the latest application and window listing, stale-reference checks and the `Targeting: none` form. The policy listener returns `Ask` with scope `app:<identity>`; the guard holds the refused-target list and runs before any prompt; the lease is taken at a session's first computer-use call of a turn and released when its `turn/end` commits or the session closes. `preGrantedApps` lets an automation profile grant named applications in configuration, and the "unconfined while enabled" fact is part of `Status`. A conformance suite (`computertest`) states the provider profile as tests against a fixture MCP server.
- **Components and files.** `internal/media/computer` (the service, `approve.go`, `targets.go`, `lease.go`), `internal/media/computer/tools`, `internal/media/computer/computertest`.
- **Depends on.** Implementation: [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1); [DH-D-TOOLS-1](tools.md#dh-d-tools-1) and [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (the declaration, `tools.Decide`, guards); [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2) and [DH-D-HUMAN-3](interaction-credentials.md#dh-d-human-3), as for [DH-D-MEDIA-5](#dh-d-media-5), with the `application` scope; [DH-D-SESSION-2](core-session.md#dh-d-session-2) (the committed-event feed for `turn/end`); [DH-D-CONNECT-3](mcp-lsp-web-connector.md#dh-d-connect-3) (a private MCP connection through `Handle`: tools not registered for the model, sampling and elicitation refused, image content admitted as attachments, the child started by the one starter). The MCP adapter and one recipe depend on this ([DH-D-XCUA-1](x-cua-browser-voice.md#dh-d-xcua-1)). Runtime prerequisite: an installed MCP server that meets the profile, its operating-system permissions, and a logged-in graphical session.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7). **Priority.** P2.
- **Done when.** `DH-T-MEDIA-9`, `DH-T-MEDIA-13` to `DH-T-MEDIA-15` and `DH-T-CUADRIVER-1` to `DH-T-CUADRIVER-4` pass and [DH-E-MEDIA-5](#dh-e-media-5) has passed.

## 9. Acceptance

### 9.1 Local tests

These use fakes: a fake engine executable, a fake browser provider, a fixture MCP server, a fake answerer. They do not prove behaviour of the real programs.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-MEDIA-1` | DH-D-MEDIA-1 | Admission: reader, queue and source-byte bounds refuse before the source is opened; a wait past `queueWait` ends as `office/busy`; no priority field exists; two readers of the same bytes at different paths share one job |
| `DH-T-MEDIA-2` | DH-D-MEDIA-1, DH-D-MEDIA-3 | One reader's cancellation leaves the other's result intact; the last reader's cancels the job; a cache hit still opens the source once; a changed generation drops cached results; image limits refuse before the rasteriser is called |
| `DH-T-MEDIA-3` | DH-D-MEDIA-2 | Discovery order with a fake file system per operating system: configured path, standard locations, `PATH`; a version below the minimum is unusable; with no engine the component loads, and after `reprobeInterval` a newly present engine is found without a restart |
| `DH-T-MEDIA-4` | DH-D-MEDIA-2 | The spawn request for a job carries a confined launch with exactly the scratch and slot profile as write roots and the network denied; the environment has no credential-shaped name; with no sandbox backend `required` and `strict` refuse and `off` runs and is reported unconfined |
| `DH-T-MEDIA-5` | DH-D-MEDIA-2 | With a fake engine that sleeps, writes outside its directory, or writes an oversized or mis-signed output: the classified code is returned, the process tree is gone, the scratch directory is removed, and the slot profile is rebuilt after a kill |
| `DH-T-MEDIA-6` | DH-D-MEDIA-4 | Through the pipeline: the three tools are unknown before activation and declared on the step after a skill load; activation survives resume and fork from the log alone; a user's skill invocation activates them for the very next model request |
| `DH-T-MEDIA-7` | DH-D-MEDIA-4 | `output` is written through the filesystem operation service: refused in read-only mode, refused over an existing file without `overwrite`, version-checked with it; the same test passes against a fake remote filesystem provider; no temporary path appears in any result |
| `DH-T-MEDIA-8` | DH-D-MEDIA-4 | Golden results for every outcome code; the `office/unavailable` text names LibreOffice and forbids a substitute; a model without image input is refused before the service is called; rendered images are attachment references, with no bytes in the log |
| `DH-T-MEDIA-9` | DH-D-MEDIA-5, DH-D-MEDIA-6 | Swapping one fake provider for another through the loader leaves the tool schemas in the next request header byte-identical; a second registration fails while the first is held or closing; no package under `internal/media/browser` or `internal/media/computer` imports the LLM runtime |
| `DH-T-MEDIA-10` | DH-D-MEDIA-5 | Two sessions get two browsers; calls of one session never overlap; `maxBrowsers` refuses with `browser/busy`; closing a session closes its browser; a resumed session's first result says earlier pages are gone; a stale reference is refused before any input |
| `DH-T-MEDIA-11` | DH-D-MEDIA-5 | The approval table of §6.2, row by row, with a fake answerer: a grant for one origin does not admit another; revocation and session end stop it; no answerer means denied and the provider receives nothing; typing into a credential field is refused in every mode |
| `DH-T-MEDIA-12` | DH-D-MEDIA-5 | The guard refuses `file:` and `javascript:` URLs, a redirect to a metadata address, and a cross-origin form submission from a signed-in origin; a download is refused and reported; the conformance suite fails a provider that skips the guard |
| `DH-T-MEDIA-13` | DH-D-MEDIA-6 | With no provider every `computer_*` tool returns `computer/unavailable` with the fixed sentence, no process starts and no network call is made; the conformance suite passes against a fixture that meets the profile and names the missing item for one that does not; a `Targeting: none` fixture lists only `desktop` |
| `DH-T-MEDIA-14` | DH-D-MEDIA-6 | A window listed under application A cannot be used with `app` B; a grant for A does not admit B; a refused target is denied before any prompt; a non-owner's answer is rejected; a grant is absent in a new session |
| `DH-T-MEDIA-16` | DH-D-MEDIA-7 | With a fake engine: a range request makes a scratch copy with the print area set and never writes the source workbook; an empty range, an unknown sheet and a range over the cell limit fail before the engine starts; the result obeys the image limits of [DH-D-MEDIA-3](#dh-d-media-3) |
| `DH-T-MEDIA-15` | DH-D-MEDIA-6 | The desktop lease: a second session, and a second process on the same user, get `computer/busy` during the first's turn and succeed after its `turn/end`; a host killed while holding the lease does not block the next one (race detector, repeated) |

### 9.2 Real-environment scenarios

<a id="dh-e-media-1"></a>
#### DH-E-MEDIA-1 Linux: the four office operations against a real LibreOffice under the sandbox

- **Proves.** DH-D-MEDIA-1, DH-D-MEDIA-2, DH-D-MEDIA-3, DH-D-MEDIA-4.
- **Given.** A Linux runner with LibreOffice installed from the distribution's packages and a working sandbox backend; the built `dsh` binary; a temporary harness home and workspace; committed `docx`, `xlsx` (with one stored formula result made deliberately stale) and `pptx` fixtures; the replay provider scripted to load the Office skill and call the three tools. No credentials.
- **When.** On the runner: the session renders two pages of the `docx`, converts the `pptx` to PDF at a named `output`, recalculates the `xlsx` to a named `output`, and exports one sheet as CSV. The process list and the network namespace of the `soffice` child are sampled while it runs.
- **Then.** The PDF opens and has the expected page count; the recalculated workbook holds the correct value; the CSV has the named sheet's rows; two image attachments exist. Each result names the LibreOffice version. Every `soffice` process ran with `Confinement` full and no network; none remains afterwards; the scratch directory is gone; the source files' bytes and the user's own LibreOffice profile are unchanged; the outputs appear in the turn's recorded changes.
- **Cleanup.** Delete the temporary home and workspace. If a `soffice` process survives, fail and keep the process list and the run directory.

<a id="dh-e-media-2"></a>
#### DH-E-MEDIA-2 Windows and macOS: discovery, the minimum version and reported enforcement

- **Proves.** DH-D-MEDIA-2.
- **Given.** One Windows and one macOS runner, each with LibreOffice installed by the vendor's installer in its default location and nothing in configuration; the same fixtures and script as [DH-E-MEDIA-1](#dh-e-media-1). Run once more with the oldest LibreOffice version proposed as the minimum.
- **When.** The same tool calls run; then `office.libreoffice.path` is set to a path that does not exist and one call is repeated.
- **Then.** The engine is found with no configuration and its path and version are shown in the status. The four operations give the same observable results as on Linux, including the recalculated value. On Windows the status and each result report enforcement `partial` and name the network as ungoverned; on macOS they report what the backend governs. With the wrong path the call fails with `office/unavailable` and the fixed text, and no process is started.
- **Cleanup.** Remove the temporary home; restore the configuration. Keep the engine's logged diagnostics on failure.

<a id="dh-e-media-3"></a>
#### DH-E-MEDIA-3 A document that hangs the engine is killed and the next job works

- **Proves.** DH-D-MEDIA-2.
- **Given.** Each runner of the two scenarios above; `timeout` set to 5 seconds; a committed workbook large enough to take far longer; a small valid document.
- **When.** The large workbook is converted, and immediately afterwards the small document on the same slot (`maxConcurrent: 1`). Separately, a conversion is cancelled from the session while it runs.
- **Then.** The first call ends as `office/timeout` within the deadline plus the stop ladder's bound; no `soffice` process of that job remains; the slot profile was rebuilt; the second call succeeds. The cancelled call returns the cancellation cause and leaves no process and no scratch directory.
- **Cleanup.** As above; on failure keep the process list and the profile directory.

<a id="dh-e-media-4"></a>
#### DH-E-MEDIA-4 A real browser: one catalogue across providers, isolation, and approval on a signed-in origin

- **Proves.** DH-D-MEDIA-5.
- **Given.** Linux, macOS and Windows runners with a stable Chrome or Edge; the built binary with area X4's native provider enabled; a test web server on an allowed loopback address serving a public page, a login form and a page that offers a download; the replay provider scripted to call the browser tools; a scripted owner answerer.
- **When.** The session navigates, takes a snapshot, clicks by reference and takes a screenshot on the public page; opens the login page; tries to type into the password field; after the test signs in through the browser itself, clicks a button on that origin with the answerer first denying, then granting the origin for the session; clicks the download link; a second session opens a browser; the first session is closed. Where a second provider is installed, the run is repeated with it.
- **Then.** The server saw the click. The public-page calls raised no prompt. Typing into the password field was refused with `browser/credential-field`. The signed-in click was denied, then asked once and allowed, and later clicks on that origin asked nothing. No file was downloaded. The second session saw none of the first one's pages or cookies. After close, the browser process and profile directory are gone. The tool schemas in the request headers are identical under both providers, and every action is a `tool/call` with a `tool/result` in the log.
- **Cleanup.** Stop the test server and remove the temporary home. If a browser process survives, fail and keep the process list and the profile path.

<a id="dh-e-media-5"></a>
#### DH-E-MEDIA-5 A real external computer-use MCP server on a logged-in desktop

- **Proves.** DH-D-MEDIA-6.
- **Given.** A macOS or Windows machine with a logged-in, unlocked graphical session; an operator-installed computer-use MCP server (Cua Driver, at the version area X4's recipe names) that has been given its operating-system permissions by hand; the built binary; a small test application with one button and one text field; a terminal window open; a scripted owner answerer. First run with the server's `command` set to a path that does not exist.
- **When.** With the wrong path, one `computer_list_apps` call. Then with the right path: list applications and the test application's windows; take a snapshot with the answerer denying, then granting the application for the session; click the button and type into the field; call `computer_snapshot` on the terminal; from a second `dsh` process on the same user, call a computer tool during the first session's turn and again after it ends; revoke the grant; end the session.
- **Then.** The wrong path gives `computer/unavailable` with the fixed sentence and starts no process. The denied snapshot reached no server call. After the grant the test application records the click and the text. The terminal call is refused before any prompt. The second process gets `computer/busy`, then succeeds. After revocation the next call asks again. After the session ends no server process remains. Every call and result is in the session log and no model request came from the server.
- **Cleanup.** Close the test application and remove the temporary home. The server program and its permissions are the operator's and are left alone. On failure keep the server's output and the process list.

<a id="dh-e-media-6"></a>
#### DH-E-MEDIA-6 Linux: a worksheet range becomes one image through a real LibreOffice

- **Proves.** DH-D-MEDIA-7.
- **Given.** The runner, binary, home and `xlsx` fixture of [DH-E-MEDIA-1](#dh-e-media-1), the fixture holding a chart over cells `B2:F20` of its second sheet; the replay provider scripted to call `office_render` with that range, then with a range of a sheet that does not exist.
- **When.** The session runs both calls.
- **Then.** The first returns one image attachment within the image limits whose content shows the chart area; the source workbook's bytes are unchanged and no copy remains in the run directory; the second fails with `office/invalid-request` and no `soffice` process started for it.
- **Cleanup.** Delete the home and workspace; keep the run directory if a copy remained.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed. The evidence proves the stated contracts for the tested build, fixtures, LibreOffice and browser versions and operating systems, not every document or site.

## 10. Open questions and limitations

**Settled with other areas.** A helper gets its launch as every child does: `Resolver.ForHelper`, `Prepare`, `Spec.Launch` ([sandbox-ssh.md](sandbox-ssh.md) §6.1); the session's browser is a registered helper kind with the reported `Unconfined` flag, and both are untrusted trees. Approvals with a subject, grants matched at the approval stage, owner-only answering and `grant.Changed` are A17's as written ([interaction-credentials.md](interaction-credentials.md) §6.2). The listeners here return `Ask` with a subject ([tools.md](tools.md)): `application` for computer use and `website` for browser use, each of which a session grant may name ([DH-ADR-33](../design.md#dh-adr-33)). Stage 4 of the pipeline makes the one call to `approval.Service`; nothing here consults the grant table itself. A successful skill load is durable before the next step resolves its tools: it is the `skill` tool's result, or the sourced message of an explicit invocation, folded by `skills.loaded` ([context-compaction-skill.md](context-compaction-skill.md)). A file that is not text is written through `ops.Write` with `Raw` ([filesystem.md](filesystem.md) §6.1). The private MCP connection is `Handle` of [mcp-lsp-web-connector.md](mcp-lsp-web-connector.md).

**Safe mode and missing software.** `office.libreoffice` starts nothing at boot and carries no safe-mode switch; with no LibreOffice the office tools stay visible and report `unavailable`. The browser and computer-use providers live in optional bundles, which `--safe` disables ([DH-ADR-BOOT-12](boot.md#dh-adr-boot-12)); the two services then report `unavailable`.

**Unknown until read at run time.** Whether Cua Driver's tools meet items 2 to 5 of the provider profile: its tool list is discovered at run time and is not in the upstream checkout. If they do not, it registers with `Targeting: none` ([x-cua-browser-voice.md](x-cua-browser-voice.md)).

**Recommended default, not confirmed by the owner.** No PDF rasteriser is embedded in the binary (risk 1 of [../libs/deepseek-ai-libreoffice-kit.md](../libs/deepseek-ai-libreoffice-kit.md)): [DH-D-MEDIA-3](#dh-d-media-3) uses LibreOffice for every page image, behind one interface, so embedding one later changes no tool.

**Settled with the experimental area ([x-cua-browser-voice.md](x-cua-browser-voice.md) §10).** `computer.Provider.Snapshot` takes `SnapshotOptions{MaxNodes, MaxDepth}`, which the catalogue's `snapshot` call sets within the service's bounds. The stored size of a screenshot is `Screenshot.Stored` (width and height), readable by a provider so that a point can be scaled. `browser.Info.StartedBy` says `harness`, `operator` or `third-party`; a browser the harness did not start is never closed by it. In [DH-E-MEDIA-5](#dh-e-media-5) "no server process remains" means no process the harness started: a driver's own daemon on macOS may stay and is recorded, not counted as a failure.

**Open, to be settled by scenario.** The minimum LibreOffice version; whether the recalculation, CSV-sheet, macro and link settings hold on it; whether `soffice` under each backend can start at all with a read-only install and two write roots; per-job start time; whether Edge and Chromium behave as Chrome; whether packaged LibreOffice builds that bring their own sandbox (Flatpak, Snap) can be driven.

**Limitations kept.** Rendering differs from Microsoft Office and between LibreOffice versions (carried, `tool-office.9`). The model cannot choose a provider (carried, two units). Reads by the engine are unconfined, and on Windows so is its network. A launched browser signed in without a password field is not recognised as signed-in. The harness's own interface is a browser tab that computer use cannot always tell from other tabs ([../libs/trycuacua-driver.md](../libs/trycuacua-driver.md), risk 2). Software the operator installed is not verified by the harness. Carried rows are owned by stage `DH-S-47` of [../implementation-plan.md](../implementation-plan.md#dh-s-47).

## 11. Units dispositioned by this spec

| Type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 4 | 4 ported |
| `UP-DEBT` | 18 | 14 resolved, 1 not-applicable, 3 carried |
| `UP-TOOL` | 3 | 3 ported, with changed arguments |
| `UP-CFG` | 2 | 2 ported |
| `UP-SVC` | 3 | 3 ported |
| `UP-DEC` (handed over) | 6 | 3 ported, 3 replaced |

Go items declared: 6 decisions, 7 deliverables, 16 local tests, 6 scenarios. Rows are in `trace/fragments/A16.jsonl`.
