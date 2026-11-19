# mime: Go specification

> **Status: design, not built.** Short form with the debt table. Upstream facts were read
> at the pinned commit recorded in [../README.md](../README.md). Statements that are
> reasoning and not read from source open with "Inference". Standard-library behaviour
> was read in the Go 1.27.4 source. No harness code exists. This spec also holds the
> design shared with [mime-types.md](mime-types.md).
>
> **Unverified research notes.** Where this document says "probe", it reports an
> observation made during library research on 2026-11-18 by running a throwaway program
> on one Windows 11 machine outside this repository. That was against the project's rule
> that nothing is built or run in the design task, and the program was not kept, so the
> observation cannot be repeated from this repository. It is an **unverified
> illustration, to be measured**; the decision rests on the cited source lines, and
> `DH-T-MIME-2` (section 8) proves that the harness's answers do not depend on the
> machine's table.

| | |
|---|---|
| **Library ID** | `DH-LIB-MIME` |
| **Upstream unit** | `UP-DEP-mime` |
| **Upstream version** | 4.1.1 |
| **Kind** | runtime in `packages/util/file-drag`; bundled into the client in `packages/client/ui-file-actions` |
| **Used by** | util, client |
| **Go choice** | own code: one table of file extensions and media types compiled into the harness, with the standard library `mime` package for parsing and formatting header values |
| **Related areas** | A23, which owns the `mediatype` package ([../areas/util-test-support.md](../areas/util-test-support.md)); A18 (static assets, [../areas/host-telemetry.md](../areas/host-telemetry.md)); A19 (file downloads) |

## 1. What it is and why upstream needs it

A **media type** (also called a MIME type, for example `image/png`) names the format of
some bytes. `mime` is a JavaScript library holding a table that maps file extensions to
media types and back.

Upstream needs the table in both directions. Going from a file name to a type, the
browser client decides whether a path is an image and what actions to offer for it
(`packages/client/ui-file-actions/src/client/context-menus.tsx:61`). Going from a type to
an extension, the desktop shell chooses a safe file name when the user drags content out
of the application: a file declared as `image/png` but named `photo.jpeg` is renamed,
and a name with no extension gets one (`packages/util/file-drag/src/index.ts:49`).

A second library, `mime-types`, does the first of those jobs on the host
([mime-types.md](mime-types.md)).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/util/file-drag/src/index.ts:55` | The preferred extension for a declared media type | `mime.getExtension(type)` |
| `packages/util/file-drag/src/index.ts:57` | Whether the name's existing extension is one of the extensions that type allows | `mime.getAllExtensions(type)` |
| `packages/client/ui-file-actions/src/client/context-menus.tsx:61`, `:89` | The media type of a path or name, in the browser | `mime.getType(path)` |

## 3. Tech debt upstream carries

Search performed: imports of `mime` and `mime-types`; `patches/` and
`pnpm-workspace.yaml` (no patch, pin or exemption for either); the two use sites'
READMEs.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L7.8` | Two libraries with two separately maintained tables answer the same question in one product: `mime` in the client and the desktop shell, `mime-types` on the host. The type the host serves a file as and the type the client assumes for the same path can differ. | `packages/api/session-controller/src/media-references.ts:15`; `packages/util/file-drag/src/index.ts:2` | Resolved by `DH-LIB-MIME`: one table in one Go package answers every lookup, and the UI is rendered by the host, so there is no second copy in a browser bundle. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-MIME-R1` | Given a file name or path, return its media type from the extension, without reading the file, case-insensitively; unknown extensions return "unknown", which callers serve as `application/octet-stream`. | `packages/client/ui-file-actions/src/client/context-menus.tsx:61`; `packages/api/session-controller/src/media-references.ts:46` |
| `DH-LIB-MIME-R2` | Given a media type (parameters such as `; charset=` ignored), return its preferred extension and the full set of extensions it allows. | `packages/util/file-drag/src/index.ts:54` |
| `DH-LIB-MIME-R3` | The answers are the same on every operating system and every machine, and change only when the harness changes. | Inference: a type decides how a browser treats a file served from the authenticated UI origin (`packages/api/session-controller/src/media-references.ts:17`) |
| `DH-LIB-MIME-R4` | There is exactly one table; the static-asset handler, file downloads, drag-out naming and UI decisions all read it. | debt L7.8 |

## 5. Go choice

**Decision.** Own code: a small package (called `mediatype` here) with a table written
as Go data, covering the types the harness serves or names: web assets, source and text
formats, images, audio, video, documents, archives and fonts. `mime.ParseMediaType` and
`mime.FormatMediaType` from the standard library parse and build header values.

**Why not `mime.TypeByExtension`.** The standard library's lookup starts from a table of
about 60 entries and then **adds whatever the machine provides**: the Windows registry
(`mime/type_windows.go:16`) or the system's `mime.types` and `globs2` files on Unix
(`mime/type_unix.go:22`). That breaks R3. Probe reported on one Windows 11 machine, Go 1.27.4 (an unverified
illustration, to be measured; the source lines above are the reason):

| Extension | `mime.TypeByExtension` | Expected |
|---|---|---|
| `.mjs` | `text/plain; charset=utf-8` | a JavaScript type |
| `.ts` | `text/plain; charset=utf-8` | not a registry accident |
| `.csv` | `application/vnd.ms-excel` | `text/csv` |
| `.md`, `.woff2`, `.yaml` | empty | known types |

`mime.ExtensionsByType("image/jpeg")` was reported to return six extensions including `.jfif` and
`.pjp` from the registry, and nothing says which is preferred, so R2 fails as well.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** own table + standard `mime` for header syntax | Deterministic; both directions; preferred extension is explicit; no dependency | The table is ours to maintain (a few hundred lines of data) | Go 1.27.4 `mime/mediatype.go:21`, `:142` |
| `mime.TypeByExtension` and `mime.ExtensionsByType` | No code | Machine-dependent results; no preferred extension; small built-in table | Go 1.27.4 `mime/type.go:165`, `:215`; probe 2026-11-18 (unverified) |
| A content-sniffing module | Finds the type of bytes with no name | A different job: upstream never inspects bytes, and sniffing is what `nosniff` forbids the browser to do | none evaluated, none named |

**Tradeoff.** A rare extension the table lacks is served as
`application/octet-stream` (a download) instead of opening in the browser. That is the
safe failure. The package must never call `mime.AddExtensionType` or
`mime.TypeByExtension`, so that other code in the process cannot change its answers.

Design notes: the package is pure data and functions, owns no resource, and needs no
fiber. `TypeByPath(name) (string, bool)`, `PreferredExtension(type) (string, bool)` and
`Extensions(type) []string` are the whole surface. The package is `mediatype` in area
A23's leaf utilities ([../areas/util-test-support.md](../areas/util-test-support.md)).
Native drag-out is dropped with the desktop application
([DH-ADR-18](../design.md#dh-adr-18); a download link replaces it). Its file-name rule
(reserved Windows names, byte-length limits, `packages/util/file-drag/src/index.ts:51`
to `:74`) is kept for downloads as `mediatype.SafeName`.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-MIME-1` | local | `DH-LIB-MIME-R1`, `DH-LIB-MIME-R2` | Table test: every entry maps extension to type and back; the preferred extension is among the allowed set; lookups ignore case and type parameters; an unknown extension reports "unknown". |
| `DH-T-MIME-2` | local | `DH-LIB-MIME-R3`, `DH-LIB-MIME-R4` | After `mime.AddExtensionType(".md", "x/y")` is called in the test process, the package's answer for `.md` is unchanged; a `go/analysis` check finds no call to `mime.TypeByExtension` or `http.DetectContentType` outside test code. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-mime`, `UP-DEBT-found.L7.8`.
