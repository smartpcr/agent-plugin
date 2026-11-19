# Filesystem seam, file tools and turn deliverables: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". IDs in this document use the area key `FS`. Facts about Go's
> `os.Root` were read in the Go source tree (`src/os/root.go`, `api/go1.24.txt`,
> `api/go1.25.txt`) on 2026-11-18. `os.OpenRoot` is in the standard library since Go 1.24
> and `Root.Link` and `Root.Rename` since Go 1.25, so all exist at the module floor `go 1.26.0`. Nothing here has been built, run or measured.

| | |
|---|---|
| **Area key** | `FS` (agent A08) |
| **Upstream packages** | 11: eight under `packages/fs`, three under `packages/deliverables` |
| **Serves outcomes** | [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10); also [DH-O-4](../design.md#dh-o-4), [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6), [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A03 (tool pipeline, prompt sections), A10 (policy resolver, SSH provider), A07 (attachments, blob store, spill), A23 (glob helper, watch service), A09 (starting `git`), A06 (session working directory), A01 and A05 (event registry, projections), A19 and C5 (routes and cards) |
| **Priority** | P0 for the seam and the six core tools: an agent that cannot read, search and edit files is not a coding harness. P1 for read-before-edit, image reading and deliverables. P2 for the single-editor tool. |

## 1. Purpose

This area is how the model touches files. It gives the model nine tools: read a file, read an image, write, edit, apply a multi-file patch, list files by pattern, search file contents, declare a finished file, and a single-command editor some models already know. Behind the tools sits one swappable **filesystem seam**, so the same tools work on the local disk or on a remote host. Two safety rules live here: a **fence** that stops file tools writing outside the directories a session may write to, and an optional **read-before-edit** rule that refuses to change a file the model has not seen or that changed since it was seen. The area also records what each turn hands to the user: the files the model declared and the files the turn changed.

Without it the model could only reach files through shell commands: unbounded output, no stale-edit protection, no diff cards, and no record of what a turn changed.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Provider** | The component that implements the filesystem interface for one execution world (local disk, a remote host). | Tools must not know where files live ([DH-O-10](../design.md#dh-o-10)). |
| **Target** | A path after the provider resolved it: an opaque identity key plus a display path. | Two spellings of one file (links, letter case) must be one file to locks and guards. |
| **Version** | A token for "this file, in this state": file metadata plus, when the content was read, a SHA-256 digest of it. | Lets a later write prove the file is still what was seen. |
| **Guard** | A condition attached to one mutation: create only if absent, or replace only if the version matches. | Stale-edit protection without a lock held between tool calls. |
| **Write root** | A directory a session may write under: its working directory, configured extra roots, a temporary area. Resolved per call by the sandbox policy (area A10). | Defines "the workspace" precisely. |
| **Fence** | The rule that a file-tool mutation must land inside a write root. | File tools run inside the host process, where the process sandbox does not apply. |
| **Rooted handle** | Go's `os.Root`: an open directory whose methods refuse any name that resolves outside it. | Closes the gap between checking a path and using it. |
| **Operation service** | The one host service every file tool calls. It resolves the path, obtains the policy, asks for intent decisions, calls the provider and records the observation. | Policy must not depend on each tool remembering to apply it. |
| **Intent** | A typed question asked before a mutation: "may this happen, and under which guard?" | Lets other components allow, tighten or deny file access without importing the tools. |
| **Observation** | A recorded fact that a session saw a file present at a version, or confirmed it absent. | The evidence the read-before-edit rule checks. |
| **Hunk** | One changed region of a text with a few unchanged context lines around it. | The unit a diff card shows and a patch applies. |
| **Patch** | Here always the model's patch format (`*** Begin Patch`), which can add, update, delete and move several files in one call. It is not a unified diff. | One call for related edits. |
| **RE2** | The regular-expression syntax of Go's `regexp` package: linear time, no look-around, no back-references. | The dialect `grep` accepts in the Go design. |
| **Deliverable** | A file the model declared as a result for the user with the `present` tool. | Separates "the answer" from files merely touched. |
| **Changed-files summary** | The list of files a turn changed, with line counts and comparisons. | Lets the user review a turn without reading every tool card. |
| **Snapshot, manifest, capture** | A snapshot is a Git tree of the working directory at one moment. A manifest is a list of paths with size and time, taken without Git. A capture is a copy of one file taken just before a file tool changes it. | Three sources of "before" for the summary, from richest to cheapest. |
| **Blob store** | The harness home's content-addressed file store (area A07). | Where bytes go that an event must be able to reference after a restart. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.fs.fs` | `dsh-fs` | definition | Abstract `FileSystem` service (`ctx.fs`): resolve, stat, read, list, watch, write, edit, patch, delete, move; the error codes; the four `fs/*` events (`packages/fs/fs/src/index.ts:67-93`, `src/types.ts:190-207`) |
| `UP-PKG-packages.fs.fs-local` | `dsh-fs-local` | provider | Local disk: identity by real path, atomic publication, a per-target lock, version tokens, watching through chokidar (`packages/fs/fs-local/README.md:75-77`) |
| `UP-PKG-packages.fs.fs-sandbox` | `dsh-fs-sandbox` | provider | Subclass of the local provider that adds the fence to the five mutations (`packages/fs/fs-sandbox/src/index.ts:55`, `:156-178`) |
| `UP-PKG-packages.fs.fs-patch` | `dsh-fs-patch` | other (pure library) | Parses the model's patch format and applies hunks to text, following OpenAI Codex `rust-v0.158.0` (`packages/fs/fs-patch/README.md:45`) |
| `UP-PKG-packages.fs.fs-observation-policy` | `dsh-fs-observation-policy` | other (policy listener) | Read-before-edit: answers the three intent events from a per-session map and records observations (`packages/fs/fs-observation-policy/src/index.ts:28`, `:128-140`) |
| `UP-PKG-packages.fs.tool-fs` | `dsh-tool-fs` | consumer | Tools `read`, `read_image`, `write`, `edit`, `apply_patch` and their prompt guidance |
| `UP-PKG-packages.fs.tool-fs-search` | `dsh-tool-fs-search` | consumer | Tools `glob` and `grep`, each starting the bundled `rg` executable as a child process |
| `UP-PKG-packages.fs.tool-str-replace-editor` | `dsh-tool-str-replace-editor` | consumer | One tool, `str_replace_editor`, with commands `view`, `create`, `str_replace`, `insert` on absolute paths |
| `UP-PKG-packages.deliverables.tool-present` | `dsh-tool-present` | consumer | Tool `present`: declares existing files as deliverables without copying them |
| `UP-PKG-packages.deliverables.workspace-changes` | `dsh-workspace-changes` | provider (`ctx.workspaceChanges`) | Records which files each top-level turn changed and serves per-file comparisons |
| `UP-PKG-packages.deliverables.deliverable-file-references` | `dsh-deliverable-file-references` | other (prompt section) | One fixed system-prompt paragraph on linking files and when to call `present` |

### 3.2 Behaviour

**The seam.** A deployment mounts exactly one provider as `ctx.fs`. Every operation first resolves a path to a target; consumers may show `displayPath` and must not parse `targetKey` (`docs/subsystems/filesystem.md:13`). Reads return UTF-8 text or a typed error. A write stages a temporary file beside the target, syncs it and publishes it atomically (`packages/fs/fs-local/README.md:76`). Write, edit, patch, delete and move accept an optional guard; without one they are unconditional (`docs/subsystems/filesystem.md:222`). The local version token is `dev:ino:size:mtimeNs:ctimeNs` (`packages/fs/fs-local/src/fsio.ts:77`).

**The fence.** `fs-sandbox` resolves the per-call policy. `read-only` rejects every mutation; `workspace-write` canonicalises the target again and requires it to lie under a write root; `danger-full-access` is unfenced. A move checks both ends before either effect (`packages/fs/fs-sandbox/src/index.ts:140-142`, `:156-178`). Reads are never fenced. The shipped base profile mounts this provider (`packages/bundle/base/cordis.patch.yml:553-554`).

**The policy events.** Before a mutation a tool dispatches `fs/write-intent`, `fs/edit-intent` or `fs/delete-intent`, each a waterfall with a single decision slot where the first listener to answer wins; after success it emits `fs/observed` (`docs/subsystems/filesystem.md:291`). With no listener the tool gets "no guard". The observation policy answers from a `WeakMap<session, Map<targetKey, observation>>`: a write to an unseen or absent target becomes "create only if absent", a write to a seen target becomes "replace only at that version", and an edit or delete of an unseen target is refused (`packages/fs/fs-observation-policy/src/index.ts:65-97`). The policy ships disabled (`packages/bundle/base/cordis.patch.yml:298-300`).

**The tools.** `read` returns numbered lines in a fixed envelope with a pagination footer, capped at 2,000 lines, 2,000 characters per line and 51,200 bytes (`packages/fs/tool-fs/src/read.ts:15`, `src/read-render.ts:11-14`), and may overlap with other calls (`src/read.ts:136`). `write` and `edit` ask the intent slot, call the provider once and emit the observation (`src/write.ts:121-124`, `src/edit.ts:133`). Under a confining provider they advertise two extra arguments, `sandbox_permissions` and `justification`, with which the model asks for one wider retry (`src/sandbox.ts:45-46`, `:62-69`). `apply_patch` checks the whole patch against simulated state first and changes nothing if any part is refused; it then commits in order, stops at the first failure and does not roll back (`packages/fs/tool-fs/README.md:129`, `:310`). `read_image` exists only while an attachment store is mounted and refuses a model route that does not accept images (`src/index.ts:90`, `src/read-image.ts:137`). `glob` and `grep` build an argument list for `rg`: `grep` passes neither `--hidden` nor `--no-ignore`, while `glob` passes both and excludes six version-control directory names (`packages/fs/tool-fs-search/src/grep.ts:113-118`, `src/glob.ts:37`, `:108-110`). `grep` tells the model its pattern is "ripgrep syntax" (`src/grep.ts:288-291`). `str_replace_editor` repeats the intent and observation steps in its own code (`packages/fs/tool-str-replace-editor/src/index.ts:257`, `:294`, `:347`).

**Deliverables.** `present` checks that each path is a regular file, without reading it, and when the tool result is final and not an error appends a log-only `deliverables/presented` event holding absolute paths (`packages/deliverables/tool-present/src/index.ts:88-108`). The changes recorder takes a Git snapshot of the work tree at turn start and turn end through a private index and a temporary object directory, so the user's repository is not written (`packages/deliverables/workspace-changes/src/git.ts:139`, `:165`), and copies each file a file tool is about to change by listening on the intent events (`src/index.ts:168-183`). At turn end it appends `workspace/changes` carrying only the turn number (`src/recorder.ts:373`); the summary and comparisons stay in memory and are served by `ctx.workspaceChanges` until the session is disposed (`docs/subsystems/deliverables.md:140`). Subagent sessions are not recorded (`src/index.ts:62-63`). Neither event reaches the model.

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | What it leaves |
|---|---|---|
| A guarded mutation finds the file changed | `FS_STALE_VERSION`; the tool appends "re-read the file, then retry" (`packages/fs/tool-fs/src/error.ts:39`) | Nothing changed |
| The fence refuses | `FS_SANDBOX_DENIED`, shown as `[sandbox: file access denied under <mode> mode]` with an escalation hint (`packages/fs/fs-sandbox/README.md:52`) | Nothing changed |
| A patch fails after commit started | Stops; earlier operations stay; a move may have published its destination and kept its source (`sourceDeleted: false`). After an I/O error or cancellation the tool compares versions and reports `unknown` if they moved (`packages/fs/tool-fs/src/patch/execute.ts:111`) | Partial changes, reported per operation |
| Removing the staging directory fails after a successful write | The write still succeeds (`packages/fs/fs-local/README.md:141`) | A private leftover directory |
| An `fs/observed` listener throws | The tool call fails although the file was already changed (`docs/subsystems/filesystem.md:291`) | A changed file reported as an error |
| A Git step of the recorder fails | The turn's record is abandoned with a warning (`packages/deliverables/workspace-changes/README.md:62`) | No card for that turn |
| The host restarts | Observations and change summaries are gone (`packages/fs/fs-observation-policy/README.md:128`, `packages/deliverables/workspace-changes/README.md:88`) | Earlier turns have no card; files must be re-read before guarded edits |

## 4. Upstream decisions dissected

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `UP-DEC-implemented.bug-fix.2026-10-26-read-guidance-line-ranges` | The `read` guidance names `cat`, `sed`, `head`, `tail` and says `offset` and `limit` read a line range, because one session read ranges with `sed` 60 times. The note's own benchmark measured the sentence as neutral. | keep | Model-facing wording backed by an observed failure; it does not depend on JavaScript. The sentence is carried word for word and pinned by a golden test. | [DH-ADR-FS-6](#dh-adr-fs-6) |
| `UP-DEC-implemented.feature.2026-09-08-present-workspace-source-files` | `present` declares files and copies nothing, to avoid a second store and a second place to edit; persistent copies were deferred "until a persistence design owns them". | change | Declaration is kept. Two things the note depended on are gone: there is no desktop application to open a file in, and the Go design has a blob store with retention. A fingerprint and a bounded copy remove three limits the note accepted. | [DH-ADR-FS-10](#dh-adr-fs-10) |
| `UP-DEC-implemented.feature.2026-09-28-apply-patch-file-editing` | One Codex-style patch per call; a pure parser and matcher pinned to Codex `rust-v0.158.0`; whole-patch preflight; ordered commit with no rollback; the argument is named `input` because 14% of recorded calls used that key. | keep | The format, the argument name and the failure semantics are product behaviour models were measured against. Rollback stays rejected for upstream's reason: undoing could overwrite a concurrent outside edit. | [DH-ADR-FS-6](#dh-adr-fs-6), [DH-ADR-FS-11](#dh-adr-fs-11) |
| `UP-DEC-implemented.feature.2026-10-29-shipped-fs-mutation-defaults` | `apply_patch` on by default in the base profile and the Web `standard` preset; read-before-edit off by default; decided from benchmark runs. | keep, with a correction | The Go defaults follow the note, and the owner decided `apply_patch` on by default ([DH-ADR-35](../design.md#dh-adr-35)). The pinned source does not: both rows say `applyPatch: false` (`packages/bundle/base/cordis.patch.yml:306`, `packages/bundle/web-app/presets/standard.patch.yml:31`) while the note and `packages/fs/tool-fs/README.md:58` say `true`. Recorded as `UP-DEBT-found.A08.1`. | [DH-ADR-FS-5](#dh-adr-fs-5), [DH-ADR-FS-6](#dh-adr-fs-6) |
| Contract, provider, policy and tools are four packages (`packages/fs/fs/README.md:54-56`) | Policy stays off the provider so a remote provider inherits no model-facing rule. | keep | This is the seam of [DH-ADR-ARCH-6](../upstream/architecture.md#dh-adr-arch-6). | [DH-ADR-FS-1](#dh-adr-fs-1) |
| Each tool dispatches the intent events itself (`packages/fs/tool-fs/README.md:102`) | The tool is "the executor"; policy is an event gate. | change | Three packages repeat the steps, and a tool that forgets them is unguarded (`UP-DEBT-found.A08.3`). | [DH-ADR-FS-1](#dh-adr-fs-1) |
| The fence is a subclass that replaces the provider (`packages/fs/fs-sandbox/src/index.ts:55`) | Swap one package to confine. | change | Choosing the unfenced provider by composition is a silent bypass, and a remote provider gets no fence from a local subclass. | [DH-ADR-FS-2](#dh-adr-fs-2) |
| Canonicalise, then check, then write; the remaining race is accepted (`packages/fs/fs-sandbox/README.md:81`) | A tighter boundary needs primitives judged too costly to port. | change | Go has the primitive ([DH-ADR-SECURITY-9](../upstream/security.md#dh-adr-security-9)). | [DH-ADR-FS-2](#dh-adr-fs-2) |
| Intent events have one decision slot, first listener wins (`packages/fs/fs-observation-policy/README.md:80`) | Simple; the policy owning the slot is "the default-deployment convention". | change | Order of registration decides safety. A closed decision set where the strictest answer wins does not. | [DH-ADR-FS-3](#dh-adr-fs-3) |
| Authorization is freshness, not completeness of the view (`packages/fs/fs-observation-policy/README.md:131`) | Any windowed read authorises an overwrite of an unchanged file. | keep as default | Kept as the default; the observation now records whether the whole file was seen, so a deployment can ask for more. | [DH-ADR-FS-5](#dh-adr-fs-5) |
| Search is a child `rg` process, outside the seam (`packages/fs/README.md:38`) | Keeps providers free of a search API. | change | Already decided: [DH-ADR-RIPGREP-1](../libs/vscode-ripgrep.md#dh-adr-ripgrep-1). In process, search reads through the provider, which removes the reason for the split. | [DH-D-FS-3](#dh-d-fs-3) |
| No timeout on file tools (`docs/subsystems/filesystem.md:382`) | A timer cannot stop a system call already running. | change | True of the system call, not of the tool call. A Go `context.Context` gives a deadline at every check point, with the uncertain case stated. | [DH-ADR-FS-8](#dh-adr-fs-8) |
| Changes are recorded from Git snapshots plus captures, and served from memory (`packages/deliverables/workspace-changes/README.md:44-48`, `:88`) | A card whose content the host can no longer open is not shown. | change | The capture method is kept. Serving from memory is dropped: the summary goes into the event and the hunks into the blob store. | [DH-ADR-FS-9](#dh-adr-fs-9) |
| Guidance is a separate package from the tool (`packages/deliverables/deliverable-file-references/README.md:28`) | Web and SDK share the paragraph without sharing the tool. | keep | A prompt section and a tool stay separate components ([../design.md](../design.md) §4.2). | [DH-D-FS-10](#dh-d-fs-10) |

## 5. Tech debt and its resolution

IDs in the first column omit the prefix `UP-DEBT-limit.packages.`; the ledger rows carry the full IDs. A bullet that describes a hazard, a missing guarantee or an unbounded cost is `resolved` only where the design removes it. A bullet that states a product boundary the Go design keeps on purpose is `carried`, with its reason in [DH-ADR-FS-11](#dh-adr-fs-11). The implementation plan does not exist yet, so carried rows name the placeholder stage `DH-S-0`; the plan must replace it with the stage that builds the deliverable named in the row.

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `fs.fs.1` | Edits, patches and moves need UTF-8 text | `packages/fs/fs/README.md:122` | **Carried**, narrowed. A move without hunks is a byte-preserving rename and accepts any regular file. Edits and patches are text operations by definition; other encodings are not added. [DH-ADR-FS-11](#dh-adr-fs-11) |
| `fs.fs.2` | A move is two effects, not a transaction | `:123` | **Carried**, narrowed. A plain move inside one write root that may replace its destination is one atomic rename. A move that must not replace, that applies hunks, or that crosses roots stays two effects with the outcome stated. [DH-ADR-FS-11](#dh-adr-fs-11) |
| `fs.fs.3` | Targets follow links; callers reject link sources with a separate `lstat`; no copy or recursive operations | `:124` | **Carried**, narrowed. Every operation takes a "do not follow the final link" option, so the racy two-step is gone. Copy and recursive directory operations stay outside the model-facing contract. [DH-ADR-FS-11](#dh-adr-fs-11) |
| `fs.fs.4` | No I/O deadline | `:125` | Resolved: every operation takes a `context.Context`; the operation service applies a default deadline. [DH-ADR-FS-8](#dh-adr-fs-8) |
| `fs.fs.5` | Resolve, then operate: two round trips for a remote provider | `:126` | Resolved: each operation takes the unresolved path and returns the resolved target in its outcome. [DH-D-FS-4](#dh-d-fs-4) |
| `fs.fs-local.1` | `config.cwd` is not containment | `packages/fs/fs-local/README.md:133` | Resolved: there is no unfenced provider to mount. The base directory is only a default for relative paths, and the fence comes from the policy. [DH-ADR-FS-2](#dh-adr-fs-2) |
| `fs.fs-local.2` | A watch is lost when the parent directory is recreated on Linux | `:134` | Resolved: `Watch` delegates to the harness watch service, whose requirement covers recreated parents ([DH-ADR-CHOKIDAR-1](../libs/chokidar.md#dh-adr-chokidar-1), `DH-T-CHOKIDAR-3`). |
| `fs.fs-local.3` | Version tokens depend on file metadata | `:135` | Resolved: a version carries a content digest wherever the content was read; the digest decides. [DH-ADR-FS-4](#dh-adr-fs-4) |
| `fs.fs-local.4` | An edit holds the whole file and its edited copy in memory | `:136` | Resolved by a bound: text mutations refuse a file above `textMaxBytes` with `fs/too-large`. [DH-ADR-FS-8](#dh-adr-fs-8) |
| `fs.fs-local.5` | An overwrite retains the prior text; the returned `after` is unbounded | `:137` | Resolved: outcomes carry bounded hunks, never whole texts. [DH-ADR-FS-8](#dh-adr-fs-8) |
| `fs.fs-local.6` | Reads sample 8,192 bytes for binary content, edits scan everything | `:138`; `src/fsio.ts:19` | Resolved: one classifier in `fsx`, applied to the whole content on both paths. [DH-D-FS-1](#dh-d-fs-1) |
| `fs.fs-local.7` | The per-target lock is in-process only | `:139` | Resolved for harness processes: a cross-process lock keyed by file identity. Other programs cannot be serialised by any design; they are detected by the re-check before publication. [DH-ADR-FS-4](#dh-adr-fs-4) |
| `fs.fs-local.8` | Guarded creation needs hard links | `:140` | Resolved: where linking is refused, creation reserves the name with an exclusive create and replaces the reservation. [DH-D-FS-1](#dh-d-fs-1) |
| `fs.fs-local.9` | Cleanup after a successful publication is best effort | `:141` | Resolved: staging is one file beside the target that the rename consumes; there is no staging directory. A leftover after a failure has a recognisable name and is swept. [DH-D-FS-1](#dh-d-fs-1) |
| `fs.fs-observation-policy.1` | Observations do not survive a resume | `packages/fs/fs-observation-policy/README.md:128` | Resolved: observations are log-only session events folded by a projection. [DH-ADR-FS-5](#dh-adr-fs-5) |
| `fs.fs-observation-policy.2` | A caller with no session can never satisfy the policy | `:129` | Resolved: a file tool call without a session is refused before it runs, so no caller class exists that may call the tool yet never pass. [DH-D-FS-5](#dh-d-fs-5) |
| `fs.fs-observation-policy.3` | A read outside the `read` tool records nothing | `:130` | Resolved: observation is recorded by the operation service, so every tool that shows file content to the model through it counts. [DH-ADR-FS-1](#dh-adr-fs-1) |
| `fs.fs-observation-policy.4` | A partial read authorises a full overwrite | `:131` | Resolved as a capability: an observation records `full` or `partial`, and `overwriteRequires: full-view` is a setting. The default stays `fresh`. [DH-ADR-FS-5](#dh-adr-fs-5) |
| `fs.fs-patch.1` | The matcher sees only LF text; callers convert line endings | `packages/fs/fs-patch/README.md:101` | Resolved: `patch.Apply` detects, normalises, matches and restores the line-ending style itself. [DH-D-FS-2](#dh-d-fs-2) |
| `fs.fs-patch.2` | The API takes complete strings, not a stream | `:102` | Resolved for parsing: the parser is fed line by line and reports each finished operation. Applying works on text already bounded by `textMaxBytes`. [DH-D-FS-2](#dh-d-fs-2) |
| `fs.fs-sandbox.1` | A policy check, not a kernel boundary; a race remains | `packages/fs/fs-sandbox/README.md:125` | Resolved: rooted handles ([DH-ADR-SECURITY-9](../upstream/security.md#dh-adr-security-9), [DH-ADR-FS-2](#dh-adr-fs-2)). The stated remainder: mount points inside a root. |
| `fs.fs-sandbox.2` | Fence and runner agree only because both call one function | `:126` | Resolved: the fence takes roots only from the policy value ([DH-ADR-SECURITY-1](../upstream/security.md#dh-adr-security-1)); `DH-T-FS-9` fails if a filesystem package computes roots itself. |
| `fs.fs-sandbox.3` | Does not confine unless the policy service is composed | `:127` | Resolved: the provider requires the policy resolver and does not load without it. [DH-ADR-FS-2](#dh-adr-fs-2) |
| `fs.tool-fs.1` | Patch commits do not roll back | `packages/fs/tool-fs/README.md:310` | **Carried.** Kept on purpose; see [DH-ADR-FS-11](#dh-adr-fs-11). |
| `fs.tool-fs.2` | No model-facing directory listing | `:311` | Resolved: `read` on a directory returns a bounded one-level listing. [DH-ADR-FS-6](#dh-adr-fs-6) |
| `fs.tool-fs.3` | `read` handles UTF-8 text only; PDF, audio and video deferred | `:312` | **Carried**, narrowed. The directory error is gone, and a non-text file is answered with the tool that reads it. `read` stays a text tool. [DH-ADR-FS-11](#dh-adr-fs-11) |
| `fs.tool-fs.4` | The extension declares the image type; a mismatch is refused | `:313` | Resolved: the file signature decides; the extension is not consulted. [DH-D-FS-7](#dh-d-fs-7) |
| `fs.tool-fs.5` | An attachment object read by path is admitted again as a new source | `:314` | Resolved: the tool asks the attachment store whether the path is one of its objects and returns the existing reference. [DH-D-FS-7](#dh-d-fs-7) |
| `fs.tool-fs.6` | The image preview depends on a client plugin slot | `:315` | Not applicable: there are no client plugin slots ([DH-ADR-3](../design.md#dh-adr-3)); the tool's own card template renders the image from the reference in the result. |
| `fs.tool-fs.7` | No way to re-read a region of an image | `:316` | Resolved: `read_image` takes an attachment reference as well as a path, and an optional `region`. [DH-D-FS-7](#dh-d-fs-7) |
| `fs.tool-fs.8` | No timeout surface | `:317` | Resolved, as `fs.fs.4`. [DH-ADR-FS-8](#dh-adr-fs-8) |
| `fs.tool-fs-search.1` | Search and file access are not proven to see one workspace | `packages/fs/tool-fs-search/README.md:229` | Resolved: search walks the provider's own view from the session's working directory, and returned paths are the provider's display paths. [DH-D-FS-3](#dh-d-fs-3) |
| `fs.tool-fs-search.2` | The bundled binary is fixed; remote filesystems have no search | `:230` | Resolved: no binary ([DH-ADR-RIPGREP-1](../libs/vscode-ripgrep.md#dh-adr-ripgrep-1)); a remote provider is searched through its view or runs the same package remotely. |
| `fs.tool-fs-search.3` | A pattern such as `src/**/*.ts` still walks the whole workspace | `:231` | Resolved: the walk starts at the pattern's literal prefix and skips a directory no match can lie under. [DH-D-FS-3](#dh-d-fs-3) |
| `fs.tool-fs-search.4` | One bounded page; no offset, case switch or other output modes | `:232` | Resolved: `offset` on both tools, `output` on `grep`, and `(?i)` taught in the description. Paging no longer needs a spill store. [DH-ADR-FS-6](#dh-adr-fs-6) |
| `fs.tool-fs-search.5` | Sampling balances only across top-level entries | `:233` | Resolved: sampling splits its budget at every directory level. [DH-D-FS-3](#dh-d-fs-3) |
| `fs.tool-str-replace-editor.1` | UTF-8 text only | `packages/fs/tool-str-replace-editor/README.md:137` | **Carried**, as `fs.fs.1`. [DH-ADR-FS-11](#dh-adr-fs-11) |
| `fs.tool-str-replace-editor.2` | No `replace_all` | `:138` | **Carried.** The tool's value is the schema some models already know; `edit` offers `replace_all`. [DH-ADR-FS-11](#dh-adr-fs-11) |
| `fs.tool-str-replace-editor.3` | Without the policy and sandbox packages its mutations are unconditional | `:139` | Resolved: the tool is a front end over the operation service, which always applies the fence and asks for intent. [DH-ADR-FS-1](#dh-adr-fs-1) |
| `deliverables.deliverable-file-references.1` | Guidance assumes a host that renders links and cards | `packages/deliverables/deliverable-file-references/README.md:85` | Resolved: the section is rendered only for a carrier that declares it shows file links, and the card sentences only where it shows cards. [DH-D-FS-10](#dh-d-fs-10) |
| `deliverables.tool-present.1` | A file can be replaced between the check and a desktop application opening it | `packages/deliverables/tool-present/README.md:92` | Resolved: no desktop application opens it ([DH-ADR-18](../design.md#dh-adr-18)). The host opens the file once and checks and streams from that one handle. [DH-ADR-FS-10](#dh-adr-fs-10) |
| `deliverables.tool-present.2` | Edits change what opens; a deleted or moved file cannot be opened | `:93` | Resolved: the declaration stores a digest and a bounded copy; the card says "changed since presented" or "source missing" and can open the copy. [DH-ADR-FS-10](#dh-adr-fs-10) |
| `deliverables.tool-present.3` | Exports hold declarations, not contents; persistent versions deferred | `:94` | Resolved: the copy is a blob the event references, so an export that includes blobs includes it. [DH-ADR-FS-10](#dh-adr-fs-10) |
| `deliverables.workspace-changes.1` | Summaries and comparisons live only in host memory | `packages/deliverables/workspace-changes/README.md:88` | Resolved: [DH-ADR-FS-9](#dh-adr-fs-9). |
| `deliverables.workspace-changes.2` | `core.splitIndex` and git-lfs still write into the user's Git directory | `:89` | Resolved, subject to `DH-E-FS-4`: Git is run with both switched off for the call; a repository where that cannot hold uses the manifest instead. [DH-ADR-FS-9](#dh-adr-fs-9) |
| `deliverables.workspace-changes.3` | A Git failure abandons the turn's record | `:90` | Resolved: any Git failure falls back to manifest and captures, and the event records which coverage was used and why. [DH-ADR-FS-9](#dh-adr-fs-9) |
| `deliverables.workspace-changes.4` | The first snapshot copies every untracked file into temporary space | `:91` | Resolved by a bound: untracked files above `maxFileBytes` are excluded, and above `snapshotMaxBytes` in total the turn uses the manifest. [DH-ADR-FS-9](#dh-adr-fs-9) |
| `deliverables.workspace-changes.5` | Edits the user makes during a turn are attributed to the turn | `:92` | Resolved: each file is labelled `tool` (a file tool changed it) or `other` (changed during the turn by something else), and the card says so. [DH-ADR-FS-9](#dh-adr-fs-9) |
| `deliverables.workspace-changes.6` | Outside a repository, shell edits are missing | `:93` | Resolved: the manifest lists them as added, modified or deleted; a comparison exists where a "before" exists. [DH-ADR-FS-9](#dh-adr-fs-9) |
| `deliverables.workspace-changes.7` | Outside snapshot coverage only paths a file tool names are captured | `:94` | **Carried**, narrowed and shown. Ignored files, and files outside the working directory, are still listed only when a file tool names them; the event's `coverage` field says so and the card shows it. [DH-ADR-FS-11](#dh-adr-fs-11) |
| `deliverables.workspace-changes.8` | Every file-tool edit copies its whole file, even where the snapshot covers it | `:95` | Resolved: the recorder evaluates ignore rules in process and skips the capture for a path the turn's snapshot covers. [DH-D-FS-9](#dh-d-fs-9) |
| `deliverables.workspace-changes.9` | A comparison serves a file's complete text, including files outside the workspace | `:96` | Resolved: only bounded hunks are stored and rendered; `compareScope: workspace` lists outside files without content; the route declares the resources it reads (area A19). [DH-ADR-FS-9](#dh-adr-fs-9) |
| `deliverables.workspace-changes.10` | A coarse comparison carries every line of both sides | `:97` | Resolved: above `hunkMaxBytes` a coarse result carries counts only. [DH-ADR-FS-8](#dh-adr-fs-8) |
| `deliverables.workspace-changes.11` | `path` keeps Windows separators | `:98` | Resolved: stored paths use forward slashes and are converted at the boundary (`DH-LIB-NODEFS-R5`). [DH-D-FS-9](#dh-d-fs-9) |
| `UP-DEBT-found.A08.1` | The decision note and two READMEs say `apply_patch` ships on; the shipped rows say off | `.agents/notes/implemented/feature/2026-10-29-shipped-fs-mutation-defaults.md:13`; `packages/bundle/base/cordis.patch.yml:306` | Resolved: a default is a Go value and the configuration catalog is generated from the running registry ([DH-ADR-TOOLCHAIN-10](../upstream/toolchain.md#dh-adr-toolchain-10)), so a document cannot disagree with it. [DH-ADR-FS-6](#dh-adr-fs-6) |
| `UP-DEBT-found.A08.2` | The search README says both tools include hidden and ignored files; `grep` skips them | `packages/fs/tool-fs-search/README.md:12`; `src/grep.ts:113-118` | Resolved: each tool's description states its own rule and a golden test pins it. [DH-D-FS-6](#dh-d-fs-6) |
| `UP-DEBT-found.A08.3` | Intent and observation steps are written out in three tool implementations | `packages/fs/tool-fs/src/write.ts:121`; `src/edit.ts:133`; `packages/fs/tool-str-replace-editor/src/index.ts:257` | Resolved: [DH-ADR-FS-1](#dh-adr-fs-1). |
| `UP-DEBT-found.A08.4` | Intent has one first-wins slot by convention, and a throwing `fs/observed` listener fails a call whose write succeeded | `docs/subsystems/filesystem.md:291` | Resolved: [DH-ADR-FS-3](#dh-adr-fs-3). |

## 6. Go design

### 6.1 Shape

| Package (under `src/harness/internal`) | Kind | Why it exists |
|---|---|---|
| `util/fsx` | library, no cordis | The few file operations where platforms differ: replace, create-if-absent, lock, identity, version, containment, text classification, line endings |
| `util/textdiff`, `util/textdiff/patch` | library, no cordis | Line diff for display; the model's patch format: parse and apply |
| `util/search` | library, no cordis | Content search and file listing in process |
| `world/fs` | contract | `FileSystem` interface, service keys, types, error codes, the three live events, the durable event type; sub-package `fstest` is the conformance suite every provider must pass |
| `world/fs/local` | provider component `fs.local` | Local disk with the fence. Provides `fs.Key`. Injects the policy resolver (A10) and the watch service (A23); both required |
| `world/fs/ops` | component `fs.ops` | The operation service. Provides `fs.OpsKey`. Injects `fs.Key`, the policy resolver and the session working directory |
| `world/fs/observe` | policy component `fs.observation` | Read-before-edit: one listener and one projection. Provides nothing. Off by default |
| `world/fs/tools` | consumer component `fs.tools` | Registers the tools and their prompt sections. Injects `fs.OpsKey` and the tool registry; attachments and spill are optional |
| `world/deliver`, `deliver/changes`, `deliver/present` | contract, component `deliver.changes`, component `deliver.present` | Turn deliverables: event types and the `Changes` service; the recorder; the `present` tool and the guidance section |

Inference: [../design.md](../design.md) §4.2 gives `internal/util` to area A23 and does not place deliverables. This specification proposes the names above; A08 owns the three `util` packages named in the area brief, and A23 owns the family.

```go
package fs // world/fs: the contract

type Ref struct{ Path, Dir string; NoFollowFinal bool } // unresolved path; Dir is the base for a relative Path
type Target struct{ Key TargetKey; Display string }     // Key is opaque
type Version struct{ Meta string; Digest [32]byte; HasDigest bool }
type Guard interface{ guard() } // closed set: NoGuard, CreateIfAbsent, ReplaceIfVersion{Version}

type FileSystem interface {
    Stat(ctx context.Context, r Ref) (Info, error) // Info{Target, Exists, Type, Size, Version}
    Open(ctx context.Context, r Ref) (io.ReadCloser, Info, error) // bytes; the caller bounds and classifies
    ReadDir(ctx context.Context, r Ref, limit int) (entries []DirEntry, truncated bool, err error)
    Write(ctx context.Context, r Ref, content []byte, g Guard, p Policy) (WriteOutcome, error)
    Edit(ctx context.Context, r Ref, e Edit, g Guard, p Policy) (EditOutcome, error) // literal replacement or hunks
    Delete(ctx context.Context, r Ref, g Guard, p Policy) (DeleteOutcome, error)
    Move(ctx context.Context, src, dst Ref, o MoveOptions, p Policy) (MoveOutcome, error)
    Watch(ctx context.Context, r Ref, changed func(error)) (stop func(), err error)
    World() World // ProcessPath, FileURL, FromHostPath, Contains: coordinates for other capabilities
}
```

`Policy` is the sandbox policy value resolved for one call (mode and write roots), owned by area A10. It is a required argument of every mutation, so a caller cannot forget it. An optional `Searcher` interface lets a provider run search on its own side; without it `util/search` walks the provider through `ReadDir` and `Open`. The interfaces take operating-system paths and are not built on `io/fs.FS`, which rejects names that are not valid UTF-8 ([../libs/node-fs-path-os-zlib.md](../libs/node-fs-path-os-zlib.md) §6.4).

**How a path maps when the files are not on the host's own disk.** A `Ref` is a world name plus a path in that world's own syntax; no path is ever valid in two worlds, and a path is never rewritten by string surgery outside `World()`. The four functions are the whole rule. (1) `ProcessPath(ref)` is the path a process started in that world must be given: for the local world and for an SSH world it is the path unchanged, because the process and the files are on the same machine; a provider whose processes see another root (a container, a sandbox that binds the workspace elsewhere) returns the path under that root. The local sandbox backends bind every write root at its own path, so under them `ProcessPath` is the identity too. (2) `FromHostPath(path)` answers whether a path of the host machine names a file of this world: the local world accepts it; an SSH world refuses it with `fs/not-in-world`, because a host path means nothing on the remote machine. (3) `FileURL(ref)` is a display and link form that names the world (`dsh-file://<world>/<path>`), never a `file:` URL for a remote world, and the UI opens it through a host route that reads through this seam. (4) `Contains(root, ref)` compares inside one world, by that world's rules for case and separators. A session's working directory, its write roots and every tool argument are paths of the session's world; the harness home, attachments and spill are always host paths and are never handed to a remote process. A value that must cross, such as a file a remote command should read, is copied through the seam into the world's scratch, and the copy's path is used.

**How a mutation runs.** A tool calls `ops.Write`, `Edit`, `Patch`, `Delete` or `Move` with the model's path. The operation service (1) takes the session from the call context ([DH-ADR-ARCH-8](../upstream/architecture.md#dh-adr-arch-8)) and refuses a call without one; (2) builds the `Ref` from the session's working directory; (3) resolves the policy, passing on an escalation request if the model made one; (4) dispatches `fs.Intent` and takes the strictest decision; (5) dispatches `fs.BeforeMutation`, where the changes recorder takes its capture; (6) calls the provider; (7) appends the durable `fs/observed` event and notifies `fs.Observed`. A read runs steps 1, 2, 6 and 7. `ops.Write` takes bytes; a caller that writes a file that is not text (the office tools of A16) sets `Raw`, which skips line-ending handling and the text classifier and keeps every other step. A model-proposed call, a direct caller and a script using `dsh tool call` ([DH-ADR-8](../design.md#dh-adr-8)) all enter through the tool pipeline and run the same tool body.

**How the local provider writes.** Under `workspace-write` it finds the write root that contains the canonical form of the target, opens (or reuses) an `os.Root` on that root, and performs every step by a name relative to it: stage a file beside the target, write, sync, re-check the version, rename. Under `read-only` it refuses. Under `danger-full-access` it uses plain paths. Reads are never fenced, as upstream. An absolute symbolic link is refused by `os.Root` (`src/os/root.go:41-43`); the provider reads the link, and if its target lies inside a write root it re-enters that root with the remainder, at most eight times. The final operation is always a rooted one.

### 6.2 Contracts

**Live events** (dispatch forms from [../design.md](../design.md) §4.6):

| Go event key | Form | Payload and result | Replaces |
|---|---|---|---|
| `fs.Intent` | around | Request: operation (`write`, `edit`, `patch`, `delete`, `move-source`, `move-destination`), target, session. Result, a closed set: `Unguarded`, `CreateIfAbsent`, `ReplaceIfVersion{v}`, `RequireVersion{v}`, `Deny{code, reason}` | `UP-EVT-fs/write-intent`, `UP-EVT-fs/edit-intent`, `UP-EVT-fs/delete-intent` |
| `fs.BeforeMutation` | ordered | Operation, target, session, call id. An error stops the mutation before any write | The capture listeners upstream prepends to the intent waterfalls (`packages/deliverables/workspace-changes/src/index.ts:168-183`) |
| `fs.Observed` | notify | Target, observation (`present` with version and coverage, or `absent`), session | `UP-EVT-fs/observed` |

**Durable event types**, registered at start-up with a payload struct and version ([DH-ADR-PERSISTENCE-2](../upstream/session-and-persistence.md#dh-adr-persistence-2)); all are log-only, so none reaches the model:

| Type | Payload (the whole value, never a delta) | Ignorable | Replaces |
|---|---|---|---|
| `fs/observed` | Target key, display path, `present` with version and coverage, or `absent` | yes: losing it only forces a re-read | Upstream's in-memory map |
| `deliverables/presented` | Turn, call id, files: path, description, size, digest, blob reference or the reason no copy was kept | no, as upstream | Upstream's event of the same name |
| `workspace/changes` | Turn, working directory, files (path, display, status, source, added, deleted, flags), totals, coverage and its reason, blob reference of the hunks | yes: losing it loses a card | Upstream's event plus the in-memory summary |

**Services.** `fs.Key` (`fs.FileSystem`) and `fs.OpsKey` (`ops.Service`) replace `UP-SVC-fs`. `deliver.ChangesKey` (`deliver.Changes`: `Summary(sessionID, seq)` and `Diff(ctx, sessionID, seq, index)`) replaces `UP-SVC-workspaceChanges`; both answers come from the log and the blob store, so they work for any session, live or not.

**Configuration.** Each is a Go struct with defaults as a Go value, decoded strictly ([DH-ADR-SCHEMA-3](../libs/zod.md#dh-adr-schema-3)).

| Go struct | Fields and defaults | Replaces | What changed |
|---|---|---|---|
| `local.Config` | `baseDir` (process working directory; a default for relative paths, never containment), `textMaxBytes` 32 MiB | `UP-CFG-dsh-fs-local` | `diffBasisMaxBytes` moves to `ops.Config` |
| none | The fence has no configuration of its own; mode and roots come from the policy resolver | `UP-CFG-dsh-fs-sandbox` | Merged into the provider |
| `ops.Config` | `opTimeout` 60 s, `diffMaxBytes` 10 MiB, `diffMaxLines` 50,000, `hunkMaxBytes` 256 KiB | part of `UP-CFG-dsh-fs-local` | Size bounds from [DH-ADR-DIFF-1](../libs/diff.md#dh-adr-diff-1) |
| `tools.Config` | `read`, `readImage`, `write`, `edit`, `applyPatch` all `true`; `readLimit` 2,000; `readMaxLineLength` 2,000; `readMaxBytes` 51,200 | `UP-CFG-dsh-tool-fs` | `applyPatch` defaults to `true`; `readStreamMinSize` is gone because every read streams |
| `tools.SearchConfig` | `glob`, `grep` `true`; `globMaxResults` 100; `grepMaxMatches` 250; `grepMaxLineBytes` 2,000; `timeout` 30 s; `maxFilesVisited` 1,000,000; `sampleOverCap` `false`; `metaMaxBytes` 65,536 | `UP-CFG-dsh-tool-fs-search` | `sampleOverCap` gets the default every shipped row states; `rawOutputMaxBytes`, `graceMs` and `stderrMaxBytes` described a child process and are gone |
| `tools.EditorConfig` | `enabled` `false`; `maxOutputChars` 16,000; `description` | `UP-CFG-dsh-tool-str-replace-editor` | Off unless a preset turns it on |
| `present.Config` | `maxFiles` 8; `keepCopy` `true`; `copyMaxBytes` 20 MiB | `UP-CFG-dsh-tool-present` | Two fields for the copy |
| `changes.Config` | `git` `auto` or `off`; `gitTimeout` 30 s; `gitOutputMaxBytes` 8 MiB; `maxFiles` 500; `maxFileBytes` 2 MiB; `snapshotMaxBytes` 1 GiB; `manifestMaxFiles` 50,000; `turnDiffBytes` 64 MiB; `compareScope` `all` or `workspace` | `UP-CFG-dsh-workspace-changes` | `diffTimeoutMs` is gone: the diff bound is input size, not time |
| `observe.Config` | `overwriteRequires` `fresh` or `full-view` (default `fresh`) | upstream has none | New |

**Tools exposed to the model.** Names and argument names are upstream's. `sandbox_permissions` and `justification` are the model's request for one wider call; they are passed to the policy resolver, which asks for approval, and carry no authority themselves ([../design.md](../design.md) §6).

| Tool | What the model can do | Arguments | Bounded result | Change from upstream |
|---|---|---|---|---|
| `read` | See a text file with line numbers, or one level of a directory | `file_path`, `offset?`, `limit?` | Envelope of path, type and numbered lines with a footer; 2,000 lines, 2,000 characters a line, 51,200 bytes | A directory is listed instead of refused; a non-text file is answered with the tool that reads it; the whole content is hashed while it is scanned |
| `read_image` | Put a PNG, JPEG, WebP or GIF in front of an image-capable model | `file_path` (a path or an attachment reference), `region?` | A reference to the stored image and its facts; limits are the attachment store's | The signature decides the type; an attachment object is not admitted twice; a region can be re-read |
| `write` | Create a file or replace its whole content | `file_path`, `content`, `sandbox_permissions?`, `justification?` | `Created file` or `Updated file`; the card's hunks are bounded | Always fenced; content above `textMaxBytes` is refused |
| `edit` | Replace literal text once, or everywhere | `file_path`, `old_string`, `new_string`, `replace_all?`, the two escalation fields | One sentence | Always fenced; re-check before publication; bounded size |
| `apply_patch` | Add, update, delete and move several files in one call | `input`, the two escalation fields | A list of files, or completed, failed, unknown and not-executed operations | On by default ([DH-ADR-35](../design.md#dh-adr-35)); the applier restores line endings itself |
| `glob` | Find files by path pattern, newest first, including hidden and ignored files but not version-control directories | `pattern`, `path?`, `offset?` | 100 paths and an omission count | In process; walks only where the pattern can match; `offset` pages |
| `grep` | Search file contents by regular expression; hidden, ignored and binary files are skipped | `pattern`, `path?`, `include?`, `output?` (`lines`, `files`, `count`), `offset?` | 250 matches, 2,000 bytes a line, grouped by file | RE2 instead of ripgrep syntax ([DH-ADR-FS-7](#dh-adr-fs-7)); in process; `output` and `offset` |
| `present` | Declare finished files for the user | `files[]` of `path`, `description?` | `Presented <path>` per file; at most `maxFiles` | Records a digest and keeps a bounded copy |
| `str_replace_editor` | View, create, replace once, or insert lines, through one tool on absolute paths | `command`, `path`, `file_text?`, `old_str?`, `new_str?`, `insert_line?`, `view_range?` | 16,000 characters with a clipping notice | Off by default; a front end over the operation service |

**Errors** use the `<domain>/<reason>` table: `fs/not-found`, `fs/not-directory`, `fs/not-text`, `fs/not-regular-file`, `fs/too-large`, `fs/permission-denied`, `fs/sandbox-denied`, `fs/io`, `fs/stale-version`, `fs/not-observed`, `fs/ambiguous-edit`, `fs/edit-not-found`, `fs/aborted`, `fs/patch-mismatch`, `fs/same-target`, `fs/already-exists` (one for each upstream `FS_*` code, `packages/fs/fs/src/types.ts:190-207`), plus `fs/timeout`; `patch/syntax`, `patch/symlink-source`; `search/invalid-pattern`, `search/failed`, `search/aborted`. Upstream's `SEARCH_RAW_OUTPUT_OVERFLOW` has no counterpart: there is no raw output.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership.** The `fs.local` fiber owns its open rooted handles, its in-process and cross-process locks and its watch registrations, and releases them on unload. A staged file belongs to the call that created it. The `deliver.changes` fiber owns one scratch directory per session (captures and the temporary Git object directory) and every `git` child process, and removes them when the session closes or the fiber unloads. Blobs belong to the blob store (A07), which retains them while an event references them. **Access** is separate: any component may read through the provider; a mutation needs a policy value, which only the resolver issues; an approval widens one call. The user's files are never owned by the harness: an existing file a tool edits is adopted for one atomic replacement and keeps its mode or access-control list.

| Situation | What happens | What is undone, and what stays uncertain |
|---|---|---|
| A provider fails to load (no policy resolver, bad config) | The fiber is `Failed`; `fs.ops` and the tools do not load, so no file tool is offered | Nothing was acquired |
| The provider is unloaded or replaced | Dependents unload first; a call in flight finishes or sees its context cancelled | Nothing; a staged file is removed by its call |
| A mutation is cancelled or passes its deadline before publication | The staged file is removed; `fs/aborted` or `fs/timeout` | Nothing changed |
| A mutation fails after publication began | The rename is one step, so the file is old or new. A failed sync afterwards is reported as "in place, durability unknown" | **Uncertain** only on a provider that cannot say (a lost remote transport): the result is `unknown` and the model is told to re-read |
| The version re-check before publication fails | `fs/stale-version`; the staged file is removed | Nothing changed |
| A staged file cannot be removed | The call's result stands; the path is logged and swept later by name and age | A leftover file with a recognisable name |
| An `fs.Intent` or `fs.BeforeMutation` listener fails or panics | The mutation does not run; the tool returns a structured error (fail closed) | Nothing changed |
| An `fs.Observed` listener fails | Logged and contained; the call succeeds | The durable event is already appended |
| A patch stops part-way | Reported per operation; no rollback | Earlier operations stay; a move may have published its destination |
| The host dies during a mutation | On resume the pipeline closes the call as "outcome unknown" ([../design.md](../design.md) §5) | **Uncertain:** the file is old or new, never partial; a staged file may remain and is swept |
| `git` is missing, fails, or a budget is exceeded | The turn falls back to manifest and captures; the event records the coverage and the reason | Nothing; the user's repository is never written |
| The host dies before `workspace/changes` is appended | That turn has no card. Blobs written for it are unreferenced and retention collects them | Nothing dangling |
| A read blocks on a dead network mount | The call returns `fs/timeout` at its deadline; the blocked goroutine is counted and its handle closed when it returns | **Uncertain:** the goroutine lives until the system call returns |

No operation is retried by this area, except the bounded retry of a Windows rename while another program holds the target open ([../libs/node-fs-path-os-zlib.md](../libs/node-fs-path-os-zlib.md) §6.1).

## 7. Decisions

<a id="dh-adr-fs-1"></a>
### DH-ADR-FS-1 One operation service; tools are schema and rendering only

- **Decision.** Every model-facing file tool calls `ops.Service`. Only that service resolves the session path, obtains the policy, dispatches the events and calls the provider. A tool holds its schema, its argument checks and its result text.
- **Why.** Upstream writes the same intent, provider and observation steps in three tool implementations (`UP-DEBT-found.A08.3`), and a tool that omits them is unguarded.
- **Tradeoff.** One more layer and service key. A host component that calls the provider directly records no observation, which is correct: the model saw nothing.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-FS-11`, `DH-T-FS-26`.

<a id="dh-adr-fs-2"></a>
### DH-ADR-FS-2 The fence is part of the provider contract and uses rooted handles

- **Decision.** Every mutation of `FileSystem` takes the policy and must enforce it; a provider that cannot does not load. The local provider enforces `workspace-write` through `os.Root` handles opened on the write roots and checks on each call that a handle still names its root. There is no separate unfenced provider; the only bypass is the mode `danger-full-access`. This applies [DH-ADR-SECURITY-9](../upstream/security.md#dh-adr-security-9) and [DH-ADR-SECURITY-1](../upstream/security.md#dh-adr-security-1).
- **Why.** Upstream's fence is a subclass, so mounting the base class removes it silently and a remote provider gets none. `os.Root` refuses a name that resolves outside its directory, which closes the race upstream accepted.
- **Tradeoff.** `os.Root` does not stop at mount points and refuses absolute symbolic links (`src/os/root.go:41-46`); the provider re-enters a root for an absolute link whose target is inside one, which is extra code to prove. `Root.Rename` and `Root.Link` exist from Go 1.25 (`api/go1.25.txt:77`, `:82`), so the module's minimum Go version is at least 1.25. A move between two write roots cannot be one rename.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-FS-9`, `DH-T-FS-10`, [DH-E-FS-1](#dh-e-fs-1).

<a id="dh-adr-fs-3"></a>
### DH-ADR-FS-3 Three typed events replace four, and the strictest decision wins

- **Decision.** One around event, `fs.Intent`, returns a closed decision; when several listeners answer, `Deny` beats a version requirement, which beats `CreateIfAbsent`, which beats `Unguarded`, whatever the order of registration. Work that must precede a write (the recorder's capture) moves to an ordered event, `fs.BeforeMutation`. Recording moves to a notify event, `fs.Observed`.
- **Why.** Upstream's single slot is first-wins by convention, and its capture listener must be registered ahead of the policy and delegate to it. Its `fs/observed` is an unguarded emit, so a throwing listener fails a call whose write succeeded (`UP-DEBT-found.A08.4`).
- **Tradeoff.** Three keys instead of one mechanism. Needs the around key with separate request and result types, [DH-D-CORDIS-7](../libs/cordis.md#dh-d-cordis-7): listeners register with `On`, the caller dispatches with `Around(ctx, req, terminal)`, and a listener's error or recovered panic reaches the caller as an error.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-FS-11`, `DH-T-FS-12`.

<a id="dh-adr-fs-4"></a>
### DH-ADR-FS-4 A version is metadata plus a content digest, and every rewrite re-checks it

- **Decision.** `Version` holds the metadata token and, whenever the content was read, its SHA-256. Where both sides have a digest the digest decides, so a change of modification time alone is not stale and a rewrite that leaves metadata unchanged is. Every read-modify-write re-checks the version it read immediately before it publishes, with or without a guard. Harness processes serialise on a cross-process lock keyed by file identity.
- **Why.** Upstream's token is metadata only and its lock is in-process (`packages/fs/fs-local/README.md:135`, `:139`). Several `dsh` processes may share a workspace ([DH-ADR-12](../design.md#dh-adr-12)), and with the policy off by default an unguarded edit would otherwise lose an outside change made between its read and its write.
- **Tradeoff.** A guarded overwrite of a large file reads it once to hash it. Programs that are not `dsh` take no lock; between the re-check and the rename a very short window remains for them.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-FS-3`, `DH-T-FS-13`, [DH-E-FS-3](#dh-e-fs-3).

<a id="dh-adr-fs-5"></a>
### DH-ADR-FS-5 Observations are logged; read-before-edit stays optional and off by default

- **Decision.** The operation service appends a log-only `fs/observed` event for each observation. The policy component folds those events with a registered projection ([DH-ADR-PERSISTENCE-7](../upstream/session-and-persistence.md#dh-adr-persistence-7)) and answers `fs.Intent` from the fold. It ships disabled, as upstream decided from benchmark runs. Its rule stays freshness; `overwriteRequires: full-view` asks that the whole file was shown.
- **Why.** Upstream loses observations on resume and reload. A projection over the log survives a restart, is inherited by a fork, and is checked against the disk by the digest, so an inherited observation of a file that changed since is simply stale.
- **Tradeoff.** One small event per read and mutation, written even while the policy is off, so that turning it on mid-session works. With it off, a model can overwrite a file it never read; that is upstream's shipped behaviour.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-FS-14`, `DH-T-FS-15`.

<a id="dh-adr-fs-6"></a>
### DH-ADR-FS-6 Tool names and arguments are kept; five additions

- **Decision.** The nine tools keep upstream's names, argument names, result envelopes and guidance sentences, including the `read` sentence restored by the 2026-10-26 note. Additions: `apply_patch` is on by default, as the 2026-10-29 note decided and the owner confirmed ([DH-ADR-35](../design.md#dh-adr-35)); `read` lists a directory; `glob` and `grep` take `offset`; `grep` takes `output`; `read_image` takes an attachment reference and `region`.
- **Why.** Models were measured against these schemas (the patch argument was renamed to `input` after 14% of calls used that key). Each addition removes a listed limit and is optional for the model.
- **Tradeoff.** A few more schema tokens on every request. `apply_patch` on by default follows the note and the owner's decision, not the pinned YAML (`UP-DEBT-found.A08.1`). An optional argument that does not affect the requested operation is accepted (`DH-LIB-ZOD-R13`): an empty `include` on `grep`, and an empty `justification` or `sandbox_permissions` on `write`, `edit` and `apply_patch`, mean "absent"; an empty `path` means the working directory; no other empty string is read as absent.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-FS-16` to `DH-T-FS-19`, [DH-E-FS-2](#dh-e-fs-2).

<a id="dh-adr-fs-7"></a>
### DH-ADR-FS-7 The grep tool states the RE2 dialect in its description and in its error

- **Decision.** [DH-ADR-RIPGREP-2](../libs/vscode-ripgrep.md#dh-adr-ripgrep-2) fixed the dialect. This decision fixes how the model is told. The tool description reads: "Search file contents with a regular expression in Go RE2 syntax. Not supported: look-ahead, look-behind, back-references and class intersections such as `[a-z&&[^aeiou]]`. `\d`, `\w`, `\s` and `\b` match ASCII only; use `\p{L}` or `\p{Nd}` for Unicode. Start the pattern with `(?i)` to ignore case." The `pattern` parameter says "Regular expression (Go RE2 syntax)". An uncompilable pattern returns `search/invalid-pattern` with Go's message and, when the pattern contains `(?=`, `(?!`, `(?<=`, `(?<!`, a back-reference or `&&[`, one sentence naming that construct as unsupported. The pattern is never rewritten.
- **Why.** Upstream teaches "ripgrep syntax" (`packages/fs/tool-fs-search/src/grep.ts:291`), and a model taught that may write forms Go rejects. A description is the one place a model reads before its first call; the error is where it learns after a mistake. Rewriting would make the logged argument mean something other than what ran ([DH-O-6](../design.md#dh-o-6)).
- **Tradeoff.** About 60 more description tokens on every request with `grep`. ASCII-only classes can silently match less than the model expects; the description says so, and no error can.
- **Serves.** [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-FS-19`, `DH-T-RIPGREP-6`, [DH-E-FS-2](#dh-e-fs-2).

<a id="dh-adr-fs-8"></a>
### DH-ADR-FS-8 File text in memory and in results is bounded, and every operation has a deadline

- **Decision.** A text mutation refuses a file or content above `textMaxBytes`. Outcomes carry hunks computed by `textdiff` under the provider's lock, never whole before and after texts; a coarse result above `hunkMaxBytes` carries counts only. Every operation takes a context, and the operation service applies `opTimeout` unless the pipeline set a shorter deadline. A deadline or cancellation acts at check points up to the start of publication, never after it.
- **Why.** Upstream holds whole files with no cap, returns unbounded `after` text, and stores every line of both sides when a comparison degrades. Its reason for no timeout is true of one system call, not of a tool call.
- **Tradeoff.** A file above 32 MiB cannot be edited by the text tools; the model uses the shell. A read blocked in the kernel is abandoned, not stopped.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-FS-6`, `DH-T-FS-20`.

<a id="dh-adr-fs-9"></a>
### DH-ADR-FS-9 Turn changes are durable; Git is an optional operator-installed helper

- **Decision.** The `workspace/changes` event carries the whole summary, and the hunks of the listed files are stored as one blob before the event is appended ([DH-ADR-PERSISTENCE-9](../upstream/session-and-persistence.md#dh-adr-persistence-9)). "Before" comes from three sources, best first: a Git snapshot, when `git` is installed and the working directory is in a repository; a capture, for every file a file tool changes; and a manifest, which lists what else changed where there is no snapshot. Git runs with a private index and object directory, as upstream, and with `core.splitIndex` and the LFS filters switched off for the call. Each file is labelled `tool` or `other`. The event states its coverage.
- **Why.** A card that vanishes on restart contradicts [DH-O-12](../design.md#dh-o-12). Reimplementing Git's index to avoid the child process is not justified; treating Git as software the operator installs matches [DH-O-10](../design.md#dh-o-10).
- **Tradeoff.** Hunks are computed at turn end, not on demand, bounded by `turnDiffBytes`; files beyond the budget are listed without a comparison. Blobs use disk until retention removes them. Inference: the two Git switches are untested; `DH-E-FS-4` decides, and a repository where they fail uses the manifest.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-FS-23`, `DH-T-FS-24`, [DH-E-FS-4](#dh-e-fs-4).

<a id="dh-adr-fs-10"></a>
### DH-ADR-FS-10 `present` declares, fingerprints and keeps a bounded copy

- **Decision.** `present` records for each file its path, size and SHA-256, and stores a copy in the blob store when the file is no larger than `copyMaxBytes` and `keepCopy` is on. The card opens the current file, says when it differs from what was presented or is missing, and can open the copy. The host serves bytes from one opened handle on which it has checked that the file is regular.
- **Why.** Upstream deferred copies for want of a persistence design and accepted three limits (`packages/deliverables/tool-present/README.md:92-94`). The blob store is that design, and with no desktop application ([DH-ADR-18](../design.md#dh-adr-18)) the host is the only opener.
- **Tradeoff.** The tool now reads each presented file once. Copies of user files sit in the harness home until retention removes them; `keepCopy: false` restores upstream's behaviour. A file above the bound has a fingerprint and no copy.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-FS-25`, [DH-E-FS-6](#dh-e-fs-6).

<a id="dh-adr-fs-11"></a>
### DH-ADR-FS-11 Boundaries kept on purpose

- **Decision.** Five upstream limits stay, each as a stated rule. (a) Text tools handle UTF-8 text, and, since the debt re-sweep (U-K1-14), text that starts with a UTF-16 byte-order mark: `fsx` decodes such a file (little- or big-endian, per the mark), every text operation (`read`, `edit`, `apply_patch`, `str_replace_editor`) works on the decoded text, and a write re-encodes in the same byte order with the same mark and the same line endings; the version digest stays over the bytes on disk, so the stale-version check is unchanged. Scope boundary of this record: other encodings without a mark, audio and video are not read as text, because without a mark the encoding is a guess and a literal match against a guessed decoding can edit the wrong bytes. (b) A patch is not rolled back. (c) A move that must not replace its destination, applies hunks or crosses roots is two effects. (d) `str_replace_editor` has no `replace_all`. (e) Changes to ignored files and to files outside the working directory are listed only when a file tool made them. Copying and recursive directory operations stay out of the provider contract.
- **Why.** (a) Editing by literal match needs one known encoding; a byte-order mark names it, which is why UTF-16 with a mark is in and every unmarked non-UTF-8 encoding is out. Such files are common on Windows (PowerShell scripts, `.reg` files). (b) Undoing could overwrite an outside edit made meanwhile, which is upstream's recorded reason. (c) `os.Root` offers no rename that refuses to replace, so creating one name without replacing and removing another cannot be one step. (d) The tool exists for the schema models already know. (e) Walking ignored trees every turn costs more than it tells, and files outside the write roots cannot be enumerated. The shell, under the process sandbox, covers the rest.
- **Tradeoff.** Eight ledger rows are `carried`. Each limit is visible: an error names the right tool, a patch report lists every operation, a move outcome states both effects, the changes card shows its coverage.
- **Serves.** [DH-O-16](../design.md#dh-o-16). **Verified by.** `DH-T-FS-16`, `DH-T-FS-18`, `DH-T-FS-22`, `DH-T-FS-23`, `DH-T-FS-27`.

## 8. Deliverables

<a id="dh-d-fs-1"></a>
### DH-D-FS-1 `fsx`: one place for replacing, creating, locking and identifying files

- **Result and why it matters.** Every part of the harness replaces a file, creates one only if absent, locks, compares identity and classifies text the same way on every platform. Upstream has three replacement implementations with three guarantees ([../libs/node-fs-path-os-zlib.md](../libs/node-fs-path-os-zlib.md) §3).
- **How it is implemented.** The design of that library specification, section 6, with what this area adds. Each operation has a path form and a form that works by name inside an `os.Root`. Rooted create-if-absent stages a file beside the target and publishes it with `Root.Link`; where linking is refused it reserves the name with an exclusive create, renames the staged file over the reservation, and removes a reservation it made if it fails. Staged files carry one recognisable prefix; a provider that stages in a directory first removes such files older than a day. `Version` computes the digest from a reader. `IsText` and the line-ending helpers give one verdict for readers and editors; `Decode` and `Encode` turn a file that starts with a UTF-16 byte-order mark into text and back in the same byte order ([DH-ADR-FS-11](#dh-adr-fs-11)). Package `internal/util/fsx`, with one file per platform for system calls.
- **Components and files.** `internal/util/fsx`, one file per platform for system calls.
- **Depends on.** `DH-LIB-NODEFS`, `DH-LIB-NODEFSXSYS`. Runtime: none.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-5](../design.md#dh-o-5). **Priority.** P0.
- **Done when.** `DH-T-FS-1`, `DH-T-FS-2`, `DH-T-FS-3`, `DH-T-FS-27` and `DH-T-NODEFS-1` to `-4` pass and `DH-E-NODEFS-1` has passed.

<a id="dh-d-fs-2"></a>
### DH-D-FS-2 `textdiff`: line diffs for display and the model's patch format

- **Result and why it matters.** One package computes every diff a card shows, deterministically, and one sub-package parses and applies the patch format. A stored diff is the same on every machine and in every replay.
- **How it is implemented.** The diff half is [../libs/diff.md](../libs/diff.md) section 6, plus the counts-only coarse result above `hunkMaxBytes`. The patch half ports upstream's parser and matcher rule for rule (`packages/fs/fs-patch/README.md:47-79`): the parser is a line-fed state machine that keeps input line numbers and yields each finished operation; `Apply` normalises line endings, runs the four matching passes against the original text, returns the complete new text with the original style restored, or a structured failure and no text. Upstream's test cases become a table. The package keeps the Apache-2.0 attribution upstream's `LICENSE` carries for the Codex-derived rules. Packages `internal/util/textdiff` and `textdiff/patch`.
- **Depends on.** `DH-LIB-DIFF`; [DH-D-FS-1](#dh-d-fs-1). Nothing here parses Git patch text: the forge area dropped its only consumer ([x-forge-worktree.md](x-forge-worktree.md)), so `DH-LIB-DIFFGIT` is not used and its removal is requested of the library specification.
- **Components and files.** `internal/util/textdiff`, `internal/util/textdiff/patch`.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-12](../design.md#dh-o-12). **Priority.** P0.
- **Done when.** `DH-T-FS-4`, `DH-T-FS-5`, `DH-T-FS-6` and `DH-T-DIFF-1` to `-3` pass.

<a id="dh-d-fs-3"></a>
### DH-D-FS-3 `search`: content search and file listing in process

- **Result and why it matters.** `grep`, `glob` and the UI's file finder search without a second executable, through the same view of files as the read tools.
- **How it is implemented.** [../libs/vscode-ripgrep.md](../libs/vscode-ripgrep.md) section 6, with four additions. The walk starts at the literal prefix of the pattern and does not enter a directory no match can lie under. A result can start at an offset, which is safe because order is stable. A content search can return matching lines, matching files or counts. Over-cap sampling splits its budget across the children of each directory in turn, not only at the top. The ignore-rule matcher is exported, so the changes recorder can ask whether a path is ignored without starting `git`. Package `internal/util/search`.
- **Depends on.** `DH-LIB-RIPGREP`, `DH-LIB-RIPGREPGLOB`; [DH-D-UTIL-4](util-test-support.md#dh-d-util-4) (the one glob helper of [DH-ADR-PICOMATCH-1](../libs/picomatch.md#dh-adr-picomatch-1)).
- **Components and files.** `internal/util/search`.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-14](../design.md#dh-o-14), [DH-O-6](../design.md#dh-o-6). **Priority.** P0.
- **Done when.** `DH-T-FS-7` and `DH-T-RIPGREP-1` to `-7` pass and `DH-E-RIPGREP-1` and `DH-E-RIPGREP-2` have passed. The time budget proposed there (a plain word over 100,000 files, first 250 matches within 3 s) is accepted by this area as the starting budget.

<a id="dh-d-fs-4"></a>
### DH-D-FS-4 The filesystem seam: contract, fenced local provider, operation service and events

- **Result and why it matters.** File tools cannot leave the write roots ([DH-O-7](../design.md#dh-o-7)), and the provider can be replaced without changing a tool schema ([DH-O-10](../design.md#dh-o-10)).
- **How it is implemented.** Section 6. The contract package holds the interface, types, error codes, event keys and `fstest`, a suite of provider-independent cases (identity through links and case, guards, atomic publication, the fence table, move outcomes, bounds, cancellation). The local provider is built on `fsx` and `os.Root`. The operation service is the only importer of both the policy resolver and the events. A direct seam: the provider component provides `fs.Key` itself; a preset that needs another world mounts another provider in its own realm. Packages `world/fs`, `world/fs/local`, `world/fs/ops`.
- **Depends on.** [DH-D-FS-1](#dh-d-fs-1), [DH-D-FS-2](#dh-d-fs-2), `DH-D-CORDIS-3`, `DH-D-CORDIS-4`; [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) (the policy resolver, the policy value, the escalation request and the denial marker text); [DH-D-UTIL-3](util-test-support.md#dh-d-util-3) (the watch service); [DH-D-VIEW-10](session-views.md#dh-d-view-10) (the session working directory, also when a session enters a worktree); [DH-D-CORDIS-7](../libs/cordis.md#dh-d-cordis-7) (the around key). The SSH provider ([DH-D-SANDBOX-9](sandbox-ssh.md#dh-d-sandbox-9)) depends on this deliverable and must pass `fstest`; it runs `search` on the remote machine.
- **Components and files.** `internal/world/fs` (with `fstest`), `internal/world/fs/local`, `internal/world/fs/ops`.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Priority.** P0.
- **Done when.** `DH-T-FS-8` to `DH-T-FS-13`, `DH-T-FS-20` and `DH-T-FS-26` pass and [DH-E-FS-1](#dh-e-fs-1), [DH-E-FS-3](#dh-e-fs-3) and [DH-E-FS-5](#dh-e-fs-5) have passed.

<a id="dh-d-fs-5"></a>
### DH-D-FS-5 Read-before-edit: logged observations and the optional policy

- **Result and why it matters.** A deployment can require that the model saw a file before changing it, and that the file has not changed since; the evidence survives a restart.
- **How it is implemented.** The contract registers the durable `fs/observed` type. The operation service appends it after each successful read, write, edit, patch, delete and move, and after a read that found nothing. The policy component registers a projection from session to a map of target key to observation, and one `fs.Intent` listener that applies upstream's table (`packages/fs/fs-observation-policy/src/index.ts:65-97`): write to unseen or absent gives `CreateIfAbsent`; write to present gives `ReplaceIfVersion`; edit, patch, delete or move-source of unseen gives `Deny` with `fs/not-observed`, of absent gives `Deny` with `fs/not-found`, of present gives `RequireVersion`. The tools turn `fs/not-observed` and `fs/stale-version` into upstream's two remedy sentences (`packages/fs/tool-fs/src/error.ts:33`, `:39`). Package `world/fs/observe`.
- **Depends on.** [DH-D-FS-4](#dh-d-fs-4); [DH-D-SESSION-1](core-session.md#dh-d-session-1) (a durable event type); [DH-D-VIEW-1](session-views.md#dh-d-view-1) (a projection); [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (a file tool call always carries a session).
- **Components and files.** `internal/world/fs/observe`.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-5](../design.md#dh-o-5). **Priority.** P1.
- **Done when.** `DH-T-FS-14` and `DH-T-FS-15` pass.

<a id="dh-d-fs-6"></a>
### DH-D-FS-6 The six core tools: `read`, `write`, `edit`, `apply_patch`, `glob`, `grep`

- **Result and why it matters.** The model can inspect, search and change files with bounded results and stated errors. This is the minimum for a coding agent.
- **How it is implemented.** One component registers each enabled tool with a Go argument struct (the schema is derived from it) and a prompt section that is rendered only while the tool is visible to that agent, as upstream. Result text follows upstream's envelopes and footers exactly (`packages/fs/tool-fs/README.md:210`, `:238`, `:266`), pinned by golden files. `read` streams through the classifier, numbers lines, stops retaining at the byte cap and keeps counting and hashing to the end. `apply_patch` plans, preflights against simulated state, then commits in order through the operation service and classifies an I/O failure as `failed` or `unknown` by comparing versions. `glob` and `grep` call `search` with the provider's view and the limits from configuration; an over-cap result offers `offset` and, where a spill store is mounted, the saved complete list. `read`, `glob` and `grep` declare themselves safe to overlap; the mutating tools do not. Package `world/fs/tools`.
- **Depends on.** [DH-D-FS-2](#dh-d-fs-2), [DH-D-FS-3](#dh-d-fs-3), [DH-D-FS-4](#dh-d-fs-4); [DH-D-TOOLS-1](tools.md#dh-d-tools-1), [DH-D-TOOLS-2](tools.md#dh-d-tools-2) and [DH-D-TOOLS-5](tools.md#dh-d-tools-5) (tool registration, result bounding, prompt sections conditional on tool visibility). [DH-D-STORAGE-5](storage.md#dh-d-storage-5) (spill, used when a spill store is mounted).
- **Components and files.** `internal/world/fs/tools`.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-10](../design.md#dh-o-10). **Priority.** P0.
- **Done when.** `DH-T-FS-16` to `DH-T-FS-19` and `DH-T-FS-27` pass and [DH-E-FS-2](#dh-e-fs-2) has passed.

<a id="dh-d-fs-7"></a>
### DH-D-FS-7 `read_image`

- **Result and why it matters.** An image-capable model can look at an image file or re-read part of one, and a text-only route's history stays free of images.
- **How it is implemented.** Registered only while the attachment store is mounted. The body refuses a route whose model does not declare image input, reads at most the store's byte limit, identifies PNG, JPEG, WebP or GIF from the signature, and hands the bytes to the store, which normalises and returns a durable reference; the result is logged with that reference before the next request. A path that is already a store object, or an attachment reference, returns the existing reference. `region` asks the store for a crop.
- **Depends on.** [DH-D-FS-4](#dh-d-fs-4); [DH-D-STORAGE-3](storage.md#dh-d-storage-3) and [DH-D-STORAGE-4](storage.md#dh-d-storage-4) (save, look up by path, crop and limits); [DH-D-LLM-4](llm.md#dh-d-llm-4) (whether a route accepts images).
- **Components and files.** `internal/world/fs/tools` (the `read_image` tool).
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-4](../design.md#dh-o-4). **Priority.** P1.
- **Done when.** `DH-T-FS-21` passes.

<a id="dh-d-fs-8"></a>
### DH-D-FS-8 `str_replace_editor`

- **Result and why it matters.** A preset for a model trained on the single-editor tool can offer it, with the same fence, policy and records as the core tools.
- **How it is implemented.** One tool with a `command` argument, registered by the tools component when enabled. `view` is `ops.Read` rendered in the tool's own format, or a two-level listing that omits hidden, `node_modules` and `__pycache__` entries; `create` is a write with `CreateIfAbsent`; `str_replace` is an edit that requires one match; `insert` is an edit at a line boundary. Null placeholders for unused parameters are treated as omitted, and a relative path is refused with the upstream hint (`packages/fs/tool-str-replace-editor/src/index.ts:97`).
- **Components and files.** `internal/world/fs/tools` (the `str_replace_editor` tool).
- **Depends on.** [DH-D-FS-4](#dh-d-fs-4), [DH-D-FS-6](#dh-d-fs-6).
- **Serves.** [DH-O-10](../design.md#dh-o-10). **Priority.** P2.
- **Done when.** `DH-T-FS-22` passes.

<a id="dh-d-fs-9"></a>
### DH-D-FS-9 Changed files of a turn, recorded durably

- **Result and why it matters.** After each top-level turn the user sees which files changed, by how much and how, and still sees it after a restart.
- **How it is implemented.** A recorder per session, driven by committed `turn/start` and `turn/end` events and by `fs.BeforeMutation`. At turn start it takes a Git snapshot if it can, otherwise a manifest. Before the first mutation of a path in a turn it captures the file into scratch, unless the snapshot covers that path. At turn end it takes the second snapshot or manifest, lists differences, labels each file, computes hunks within `turnDiffBytes`, stores them as one blob and appends `workspace/changes`. `git` is started through the process package with an explicit environment, no ambient configuration, a timeout and bounded output, as upstream (`packages/deliverables/workspace-changes/src/git.ts:69`). Stored paths use forward slashes. `deliver.Changes` reads the event and the blob. Packages `world/deliver`, `deliver/changes`.
- **Depends on.** [DH-D-FS-2](#dh-d-fs-2), [DH-D-FS-3](#dh-d-fs-3), [DH-D-FS-4](#dh-d-fs-4); [DH-D-EXEC-1](execution.md#dh-d-exec-1) and [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) (starting `git` under a `ForHelper` policy, read-only with its private index and object directory as the one write root: a repository's own configuration can name programs for Git to run, so this child is never a trusted launch); [DH-D-STORAGE-3](storage.md#dh-d-storage-3) and [DH-D-STORAGE-9](storage.md#dh-d-storage-9) (blob store and retention). A child agent's file changes are logged in the child's own session, by its own recorder ([subagent-team.md](subagent-team.md)); in the parent's turn they appear as `other`. The route and the card depend on this ([DH-D-API-9](api.md#dh-d-api-9), [DH-D-UIWORK-8](ui-work.md#dh-d-uiwork-8)). Runtime: `git` 2.13 or later is optional.
- **Components and files.** `internal/world/deliver`, `internal/world/deliver/changes`.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Priority.** P1.
- **Done when.** `DH-T-FS-23` and `DH-T-FS-24` pass and [DH-E-FS-4](#dh-e-fs-4) and [DH-E-FS-6](#dh-e-fs-6) have passed.

<a id="dh-d-fs-10"></a>
### DH-D-FS-10 `present` and the file-reference guidance

- **Result and why it matters.** The model can hand the user a finished file, the record survives edits and deletion within a bound, and the model is told how to link files only where links are shown.
- **How it is implemented.** `present` validates the count, refuses a missing path, a directory and a final symbolic link, reads each file once to hash it and to store the copy, and returns `Presented <path>` lines. The event is appended only when the call's result is final and not an error, including a call nested in a script. The guidance is one prompt section holding upstream's paragraph (`packages/deliverables/deliverable-file-references/README.md:70`), rendered when the carrier declares that it shows file links; its sentences about cards are included only when the carrier shows cards and `present` is visible. Package `deliver/present`.
- **Depends on.** [DH-D-FS-4](#dh-d-fs-4); [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (blob store); [DH-D-TOOLS-5](tools.md#dh-d-tools-5) (prompt sections); [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (`tools.Settled`, the final-result notification). The open and download route and the card depend on this ([DH-D-API-9](api.md#dh-d-api-9), [DH-D-UIWORK-8](ui-work.md#dh-d-uiwork-8)).
- **Components and files.** `internal/world/deliver/present`.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-5](../design.md#dh-o-5). **Priority.** P1.
- **Done when.** `DH-T-FS-25` passes and [DH-E-FS-6](#dh-e-fs-6) has passed.

## 9. Acceptance

### 9.1 Local tests

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-FS-1` | DH-D-FS-1 | Rooted replace and create-if-absent: one winner among 50 goroutines and two processes; with linking disabled the reservation path gives the same result; a failure at each step leaves the old content and no staged file; stale staged files are swept |
| `DH-T-FS-2` | DH-D-FS-1 | One text verdict for a reader and an editor: a NUL at byte 9,000 and invalid UTF-8 are "not text" on both paths; the dominant line ending is restored after an edit |
| `DH-T-FS-3` | DH-D-FS-1 | Version: a change of modification time alone is not stale; a rewrite with size and times restored is stale; a version without a digest falls back to metadata |
| `DH-T-FS-4` | DH-D-FS-2 | Patch parser table from the pinned Codex cases; feeding line by line equals parsing whole; errors carry the input line; heredoc wrappers and Unicode whitespace as specified |
| `DH-T-FS-5` | DH-D-FS-2 | Patch apply: the four matching passes, anchors, end-of-file hunks, the trailing-empty-line retry; CRLF text keeps CRLF; a failure returns the hunk line and stage and no text; fuzz: output is complete text or a failure |
| `DH-T-FS-6` | DH-D-FS-2 | A coarse result above `hunkMaxBytes` carries counts and no lines; below it, one whole-replacement hunk |
| `DH-T-FS-7` | DH-D-FS-3 | `src/**/*.ts` never lists a directory outside `src`; offset pages concatenate to the full result; `files` and `count` output; sampling at every level reaches deep directories |
| `DH-T-FS-8` | DH-D-FS-4 | `fstest` passes for the local provider and an in-memory provider; with the provider swapped through the real loader the tool schemas are byte-identical; with a counting fake provider a listing with a cap requests no more than the cap plus one entries and reports truncation |
| `DH-T-FS-9` | DH-D-FS-4 | Fence table: `read-only` refuses all five mutations; `workspace-write` inside and outside each root; a move with one end outside changes neither file; the provider does not load without a policy resolver; no filesystem package computes write roots |
| `DH-T-FS-10` | DH-D-FS-4 | A directory swapped for a link between resolution and write never causes a write outside the root; an absolute link to inside a root is followed, to outside is refused; a root replaced by a link is refused on the next call |
| `DH-T-FS-11` | DH-D-FS-4 | For each operation the order is intent, before-mutation, provider, durable event, notify; a before-mutation error leaves the file untouched; a panicking observer does not fail the call |
| `DH-T-FS-12` | DH-D-FS-4 | Two intent listeners in either registration order: the stricter decision wins; a listener error denies; no listener gives `Unguarded` |
| `DH-T-FS-13` | DH-D-FS-4 | Fifty concurrent edits of one file: one succeeds, the rest are stale; an outside write injected between read and publication is detected; two processes serialise on the lock and a killed holder releases it |
| `DH-T-FS-14` | DH-D-FS-5 | Policy table: unseen, absent, present against write, edit, patch, delete and both ends of a move; a partial read with `fresh` and with `full-view` |
| `DH-T-FS-15` | DH-D-FS-5 | Replaying a session log rebuilds the same observations; a fork inherits them; an inherited observation of a file changed on disk is stale |
| `DH-T-FS-16` | DH-D-FS-6 | `read` golden files: envelope, the three footers, line truncation, offset out of range, exact total after the byte cap, directory listing, the routing hint for an image; the guidance sentence equals upstream's |
| `DH-T-FS-17` | DH-D-FS-6 | `write` and `edit` golden files; the denial marker and hint; an escalation field without its partner is refused; `fs/too-large` |
| `DH-T-FS-18` | DH-D-FS-6 | `apply_patch`: a refused preflight changes nothing; commit stops at the first failure with earlier operations kept; an I/O error with a moved version is `unknown`; a partial move is reported; error-flag rules as upstream |
| `DH-T-FS-19` | DH-D-FS-6 | `grep` description golden file contains the RE2 text; a table of Rust-only patterns returns `search/invalid-pattern` with the construct named; the pattern in the log equals the argument; `glob` includes hidden and ignored files and `grep` does not |
| `DH-T-FS-20` | DH-D-FS-4 | Cancellation and deadline before publication leave no change and no staged file; a provider that blocks returns `fs/timeout` and the abandoned-operation count rises |
| `DH-T-FS-21` | DH-D-FS-7 | A PNG named `.jpg` is accepted as PNG; a text route is refused; a store object is not admitted twice; a region outside the image is an argument error |
| `DH-T-FS-22` | DH-D-FS-8 | Command table with null placeholders; relative path refused; `create` on an existing file is `fs/already-exists`; zero and two matches refused; the same events as `edit` |
| `DH-T-FS-23` | DH-D-FS-9 | With a fake Git and with none: capture once per path per turn; a reverted change is not listed; labels `tool` and `other`; a restart serves the same summary and hunks; a Git failure and each exceeded budget degrade with a recorded reason; forward-slash paths on Windows |
| `DH-T-FS-24` | DH-D-FS-9 | The `git` argument list and environment match a golden file: private index, object directory, alternates, the two switches, no ambient configuration |
| `DH-T-FS-25` | DH-D-FS-10 | Only regular files; digest recorded; the copy is stored before the event; no event for an error or blocked result; the guidance appears only for a carrier that shows links |
| `DH-T-FS-26` | DH-D-FS-4 | Import direction: tools import `ops`, never a provider; only `fsx` publishes by rename |
| `DH-T-FS-27` | DH-D-FS-1, DH-D-FS-6 | UTF-16 with a byte-order mark (U-K1-14): a little-endian and a big-endian file with CRLF endings round-trip through `read`, `edit` and `apply_patch` with the same mark, byte order and endings; an edit whose `old_string` matches only in the decoded text succeeds; the version digest is over the bytes on disk; a UTF-16 file without a mark, and a file with a mark followed by an invalid surrogate, are refused as not text with the error that names the shell |

### 9.2 Real-environment scenarios

<a id="dh-e-fs-1"></a>
#### DH-E-FS-1 The fence holds on real filesystems while a path is being swapped

- **Proves.** DH-D-FS-4.
- **Given.** Linux (ext4), macOS (APFS) and Windows (NTFS) machines; the built `dsh`; a session in `workspace-write` with one working directory and one extra write root; a helper process that swaps a workspace sub-directory for a symbolic link to a directory outside every root, in a tight loop.
- **When.** A direct caller issues 10,000 `write`, `edit` and `apply_patch` calls into that sub-directory through the tool pipeline, then repeats one write under `read-only` and under an approved `danger-full-access` escalation.
- **Then.** No file outside the roots is created or changed (compared by a hash listing of the outside directory before and after). Every refused call returns `fs/sandbox-denied` and leaves no staged file. The `read-only` write is refused; the escalated write succeeds and is logged with its approval.
- **Cleanup.** Stop the helper; delete the temporary directories. If an outside file changed, keep both listings and the session log.

<a id="dh-e-fs-2"></a>
#### DH-E-FS-2 A real model searches with RE2 and edits files with the shipped tools

- **Proves.** DH-D-FS-6.
- **Given.** A real model API key supplied at run time; a small public repository checked out into a temporary directory; the default tool set; no `rg` on `PATH`.
- **When.** A fixed set of 20 tasks is run, each needing a content search, a read of a line range, one edit and one multi-file patch.
- **Then.** Every `grep` argument in the log is the pattern that ran. Inference, proposed threshold: fewer than 5% of `grep` calls return `search/invalid-pattern`, and each such call is followed by a corrected call, not by a shell search. No child process named `rg` is started. Files on disk equal the logged edits.
- **Cleanup.** Delete the checkout; keep the session logs and the count of invalid patterns by construct.

<a id="dh-e-fs-3"></a>
#### DH-E-FS-3 Two `dsh` processes and an outside editor change one file

- **Proves.** DH-D-FS-4.
- **Given.** One harness home and one workspace on each of Linux, macOS and Windows; two real `dsh` processes with one session each; a script that rewrites the same file with a real editor's save sequence (write a temporary file, rename).
- **When.** Both sessions issue 1,000 `edit` calls on the file while the script saves it every few milliseconds.
- **Then.** The final file is a complete result of a sequence of whole writes. Every edit either took effect or returned `fs/stale-version`; none is reported as applied and then missing. Killing one process mid-run releases its lock, and the other continues.
- **Cleanup.** Stop the processes; delete the workspace; keep both session logs if a reported edit is missing.

<a id="dh-e-fs-4"></a>
#### DH-E-FS-4 Turn changes with real Git leave the user's repository untouched

- **Proves.** DH-D-FS-9.
- **Given.** Real `git` on Linux, macOS and Windows; three repositories: plain, one with `core.splitIndex` on, one using git-lfs with a tracked large file; a fourth directory that is not a repository; a recursive hash listing of each `.git` directory.
- **When.** A turn changes files with `edit`, `apply_patch` and a shell command, including an ignored file and a file above `maxFileBytes`; the host is then restarted.
- **Then.** Each `.git` directory hashes the same as before, including no new `sharedindex.*` file and nothing new under `.git/lfs`; if either differs for a repository class, that class is switched to the manifest and this scenario is rerun. The summary lists tool and shell changes with labels and coverage; the non-repository directory lists the shell change without a comparison. After the restart the same summary and hunks are served. Scratch directories are gone after the session closes.
- **Cleanup.** Delete the repositories and the harness home; keep the before and after listings of any `.git` directory that changed.

<a id="dh-e-fs-5"></a>
#### DH-E-FS-5 The same tools on a real remote host

- **Proves.** DH-D-FS-4.
- **Given.** A real SSH host reachable with the operator's own configuration, and the SSH provider of [DH-D-SANDBOX-9](sandbox-ssh.md#dh-d-sandbox-9) mounted in place of the local one.
- **When.** The `fstest` suite and the tasks of DH-E-FS-2 run against it; the connection is cut during one mutation.
- **Then.** The tool schemas sent to the model equal those of the local run. The suite passes. The interrupted mutation is reported as `unknown` and is not retried.
- **Cleanup.** Remove the remote temporary directory; keep the provider log if the suite failed.

<a id="dh-e-fs-6"></a>
#### DH-E-FS-6 A user reviews changed and presented files in a real browser, before and after a restart

- **Proves.** DH-D-FS-9, DH-D-FS-10.
- **Given.** The built `dsh` serving its UI to a real browser; a session whose model edited two files and presented one; the routes and cards of [DH-D-API-9](api.md#dh-d-api-9) and [DH-D-UIWORK-8](ui-work.md#dh-d-uiwork-8).
- **When.** The user opens the changed-files card and a comparison, opens the presented file, edits that file outside the harness, deletes it, and restarts the host between steps.
- **Then.** The card shows both files with counts and hunks before and after the restart. The presented file opens; after the outside edit the card says it changed since it was presented; after deletion it says the source is missing and opens the copy. No request returns a file's complete text for a comparison.
- **Cleanup.** Close the session; delete the home; keep the browser's network log if any step failed.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Carried limits** are listed in [DH-ADR-FS-11](#dh-adr-fs-11). Each is a scope boundary of that record, owned by stage `DH-S-47` of [../impl.md](../impl.md#dh-s-47).
2. **`os.Root` and absolute links.** That re-entering a root for an absolute link is safe on every platform is reasoning from the `os.Root` documentation, not a tested fact; `DH-T-FS-10` and `DH-E-FS-1` decide. If it fails, absolute links are refused under `workspace-write`, which some workspaces will notice.
3. **Minimum Go version** is at least 1.25 for this area ([../design.md](../design.md) CF-4 is still open).
4. **Windows.** How `Root.Rename` behaves while another program holds the target open, and whether the access-control list survives a rooted replacement, are untested (`DH-T-NODEFS-1`, `DH-T-KOFFI-5`).
5. **Search speed** is unmeasured ([../libs/vscode-ripgrep.md](../libs/vscode-ripgrep.md) §7). If the budget is missed, the alternative recorded there needs a new decision.
6. **Decided: `apply_patch` is on by default** ([DH-ADR-35](../design.md#dh-adr-35), [DH-ADR-FS-6](#dh-adr-fs-6)).
7. **Settled with other areas.** `fsx` offers a non-blocking exclusive lock, on Windows also on a byte range beyond the end of a file, which the session lease uses ([session-store.md](session-store.md) §6.2). `ReadDir` takes a limit and reports truncation (U-K2-1): the local provider reads the directory in batches and stops at the limit, a remote provider passes the limit to its helper, and the search walker passes "no limit". A limited listing is in the operating system's directory order, so a caller sorts what it received and says the listing is partial; the files route asks for its cap plus one. `Stat` after `Open` reads the metadata from the open handle, so a listing route cannot be made to read an unbounded directory or a swapped file ([api.md](api.md), `DH-D-API-9`). `deliver.Changes.Diff` returns a file's hunks in pages, and a presented file's kept copy is readable by its reference ([ui-work.md](ui-work.md)). A `When` condition receives the carrier's capabilities; a session export bundles no blobs (the archive of `DH-D-VIEW-8` carries attachments); a directory move, a worktree entry included, is a `workspace-root/change` event. **Shared homes.** A cross-process lock file per file identity lives under the home's runtime directory; sweeping old lock files is registered as a once-per-home job with the one runner of [DH-D-STORE-8](session-store.md#dh-d-store-8).
8. **No "reveal in file manager".** Neither this area nor the execution area opens a file or its folder in a host application; the drop is recorded in [DH-ADR-HOST-9](host-telemetry.md#dh-adr-host-9).
9. **Limits of the guarantee.** The fence confines file tools, not reads, and not what a shell command does (the process sandbox does that). Tests with a fake Git, a fake provider or a fake model are not scenarios.

## 11. Units dispositioned by this spec

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 11 | 7 ported, 3 merged, 1 replaced |
| `UP-DEBT` | 58 (54 assigned, 4 found) | 49 resolved, 8 carried, 1 not-applicable |
| `UP-TOOL` | 9 | 9 ported (all exposed; changes in section 6.2) |
| `UP-EVT` | 4 | 3 merged into `fs.Intent`, 1 ported |
| `UP-CFG` | 7 | 6 ported, 1 merged |
| `UP-SVC` | 2 | 2 ported |
| `UP-DEC` | 4 | 3 ported, 1 replaced |

Rows are in `trace/fragments/A08.jsonl`, with 11 decisions, 10 deliverables, 27 local tests and 6 scenarios declared under the key `FS`.
