# mime-types: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". The shared analysis, the standard-library probe and
> the Go mechanism are in [mime.md](mime.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-MIMETYPES` |
| **Upstream unit** | `UP-DEP-mime-types` |
| **Upstream version** | 3.0.2 |
| **Kind** | runtime, one package |
| **Used by** | api (`packages/api/session-controller`) |
| **Go choice** | own code: the media-type table defined in [mime.md](mime.md) |
| **Related areas** | session view and file downloads |

## 1. What it is and why upstream needs it

`mime-types` maps a file name to a media type from a bundled table. It is a different
library from `mime`, with its own table.

Upstream uses it for one response header. When a session transcript refers to a file in
the workspace (an image the model produced, an HTML report), the browser fetches the
file's bytes from the host. The host must label the response with a `Content-Type`, and
it takes that from the file's extension (`packages/api/session-controller/src/media-references.ts:46`).

The label is security-relevant. The file is served from the same origin as the
authenticated UI, and its content may have been written by the model or fetched from the
web. Upstream therefore always sends `X-Content-Type-Options: nosniff` and a
`Content-Security-Policy` that sandboxes the document, so that an HTML or SVG file
opened directly cannot run script against the UI
(`packages/api/session-controller/src/media-references.ts:17`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/api/session-controller/src/media-references.ts:46` | `Content-Type` for a workspace file, with `application/octet-stream` when the extension is unknown | `mime.lookup(path)` |

No patch, pin or exemption exists (`patches/`, `pnpm-workspace.yaml` searched). The
duplication with `mime` is recorded as `UP-DEBT-found.L7.8` in [mime.md](mime.md).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-MIMETYPES-R1` | A workspace file served to the browser gets its `Content-Type` from the harness's one media-type table by extension; an unknown extension is `application/octet-stream`. The bytes are never inspected to guess a type. | `packages/api/session-controller/src/media-references.ts:46` |
| `DH-LIB-MIMETYPES-R2` | Every such response carries `X-Content-Type-Options: nosniff` and a sandboxing `Content-Security-Policy`, whatever the type. | `packages/api/session-controller/src/media-references.ts:17` |
| `DH-LIB-MIMETYPES-R3` | The type served for a path equals the type any other part of the harness reports for the same path. | `DH-LIB-MIME-R4` |

## 5. Go choice

**Decision.** No second library. The handler calls the `mediatype` package of
[mime.md](mime.md) and sets `Content-Type` itself before writing the body.

**Why.** Setting the header first is what stops Go's `http.ServeContent` and
`http.ResponseWriter` from sniffing the first 512 bytes when no type is set, which would
break `DH-LIB-MIMETYPES-R1`. One table gives `DH-LIB-MIMETYPES-R3`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** the own table of [mime.md](mime.md) | Deterministic on every machine; one answer per path | Table maintenance | see [mime.md](mime.md) section 5 |
| Standard library `mime.TypeByExtension` | No code | Reads the Windows registry or system files, so the type of a served file would depend on the machine (probe in [mime.md](mime.md)) | Go 1.27.4 `mime/type_windows.go:16` |

**Tradeoff.** As in [mime.md](mime.md): an extension missing from the table downloads
instead of displaying.

Failure and ownership: a lookup cannot fail and owns nothing. The surrounding handler
(file size limit, versioned URL, error statuses) belongs to the session-view area.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-MIMETYPES-1` | local | `DH-LIB-MIMETYPES-R1`, `DH-LIB-MIMETYPES-R2`, `DH-LIB-MIMETYPES-R3` | A file named `x.unknownext` whose bytes are HTML is served as `application/octet-stream`; a `.html` and a `.svg` file are served with their types plus `nosniff` and the sandbox policy; for a list of paths the header equals `mediatype.TypeByPath`. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-mime-types`.
