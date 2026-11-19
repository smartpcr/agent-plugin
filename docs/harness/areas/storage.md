# Non-session storage, attachments, output spill, workspaces and settings: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md). Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `STORAGE`. Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `STORAGE` (cluster `A07`) |
| **Upstream packages** | 11 in five groups: `storage` (4), `attachment` (2), `spill` (3), `workspace` (1), `settings` (1) |
| **Serves outcomes** | [DH-O-2](../design.md#dh-o-2), [DH-O-4](../design.md#dh-o-4), [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10), [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A05 (the one database, sessions table, leases), A03 (tool pipeline), A01 and A02 (event registry, pre-step gate), A04 (token estimate, image pricing), A08 (`fsx`, filesystem seam), A21 (layers), A23 (`schema`, `watch`), A19 and the UI areas (routes and pages) |
| **Priority** | P0 for the machine-state store, the home layout and result bounding, because a first working harness needs a place for state and must never send an unbounded tool result to a model. P1 for attachments, workspaces, settings and retention. P2 for quarantine |

## 1. Purpose

A harness keeps more than conversation logs. It remembers which project directories the user works in and which sessions belong to each, keeps the images and files a person attaches, saves the full text of a tool result that is too large to show a model, and lets a person change settings of a running system. This area owns all of that: every piece of stored data that is not the session log. Without it there is no project list, an attached screenshot is lost on restart, one large command output fills the model's context, and a settings page has nowhere to write.

The session log itself, its database, leases and import command belong to area A05. Secret values belong to area A17. Configuration layer files belong to area A21; this area owns only the form a person edits them through.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Machine state** | Data the program owns and no person edits by hand: the workspace list, session flags, composer drafts. | It needs transactions and several processes may write it, so it lives in database tables ([DH-ADR-PERSISTENCE-9](../upstream/session-and-persistence.md#dh-adr-persistence-9)). |
| **Namespace, record version** | A namespace is one named set of key-value records with one owner package. Each record stores the version of the Go type it was written with. | Small state gets a home without a new table; an old record can be converted when read. |
| **Namespace revision** | A counter stored with a namespace and increased in the same transaction as every write to it. | It is how one process notices that another process changed the data. |
| **Blob, content address, collector** | A blob is a file of bytes kept outside the database. Its content address is the SHA-256 digest of its bytes, used as its name. The collector is the routine that deletes blobs nothing references. | Equal bytes are stored once, a name can never point at changed bytes, and tools address blobs by path. |
| **Attachment, attachment reference** | An attachment is an image or file a person or a tool attached to a message. Its reference is the small record written into a session event: digest, media type, size, dimensions. | The log stays small and never holds image bytes, a temporary path or a browser URL. |
| **Normalized image, request version** | The normalized image is the one upright, metadata-free copy stored at admission. A request version is a smaller copy derived for one model route and cached. | History stays stable while each model gets the size it accepts. |
| **Spill, preview, notice, locator** | To spill is to save the full text of an oversized tool result to a file. The model sees a preview (the head and tail), a notice saying how much was left out, and a locator (where the full text is). | A model's context is finite; nothing useful is thrown away. |
| **Reference row** | A database row saying "this session event uses this blob or spill file", written in the same transaction as the event. | Retention can tell garbage from data without scanning every log. |
| **Grace period** | The minimum age an unreferenced blob must reach before it may be deleted. | A blob is stored before the event that references it, so a fresh unreferenced blob is normal, not garbage. |
| **Maintenance duty** | Work that must happen once per harness home, not once per process: garbage collection, sweeps. | Several `dsh` processes share one home ([DH-ADR-12](../design.md#dh-adr-12)). |
| **Workspace, ledger** | A workspace is a registered project directory with a title and an ordered list of sessions. The ledger is that stored list. | "Which sessions belong to this project" is not a fact of any one session. |
| **Archive, pin, activity provider** | Archiving hides a session; pinning keeps it on top. An activity provider is a component that can say what still runs for a session and stop it. | A hidden session must never keep spending tokens. |
| **Settings form, live field** | A form is the editable view of one composed entry's config. A live field is one its component can apply without restarting. | A person changes a running system without editing YAML. |

Framework terms (component, fiber, effect, service, seam, layer, writable layer, lease) are defined in [../design.md §2](../design.md#2-vocabulary).

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.storage.storage` | `dsh-storage` | definition | The hub `ctx.storage`: a name-to-backend table plus a map of mounted "data forms". It does no I/O itself (`docs/subsystems/storage.md:11`, `:24`). |
| `UP-PKG-packages.storage.storage-json` | `dsh-storage-json` | provider | Backend `json`: one pretty-printed file per unit rewritten whole, or one file per record (`packages/storage/storage-json/README.md:74-76`). |
| `UP-PKG-packages.storage.storage-sqlite` | `dsh-storage-sqlite` | provider | Backend `sqlite`: one JSON document per row in one database file (`packages/storage/storage-sqlite/README.md:73`). |
| `UP-PKG-packages.storage.storage-domain` | `dsh-storage-domain` | consumer | The typed API everything else uses (`ctx.storageDomain`): declared domains, schema validation, one write queue per domain, the `domain/changed` event (`docs/subsystems/storage.md:143`). |
| `UP-PKG-packages.attachment.attachment` | `dsh-attachment` | definition | The abstract store `ctx.attachments`, reference types, batch admission and stable error codes (`packages/attachment/attachment/src/index.ts:77-80`). |
| `UP-PKG-packages.attachment.attachment-local` | `dsh-attachment-local` | provider | Content-addressed files under the harness home, image validation and normalization through sharp, the request-version cache (`packages/attachment/attachment-local/README.md:84-92`). |
| `UP-PKG-packages.spill.spill` | `dsh-spill` | definition | The abstract store `ctx.spillStore` with one method, `saveText` (`docs/subsystems/spill.md:90`). |
| `UP-PKG-packages.spill.spill-local` | `dsh-spill-local` | provider | Private files named `<root>/session-<hash>/<random>-<name>` and a start-up age sweep (`packages/spill/spill-local/src/store.ts:108-116`). |
| `UP-PKG-packages.spill.spill-policy` | `dsh-spill-policy` | consumer | A listener on `tools/post-execute` that replaces an oversized result with a preview and a notice (`packages/spill/spill-policy/src/index.ts:134-151`). |
| `UP-PKG-packages.workspace.workspace` | `dsh-workspace` | other (entity service) | `ctx.workspaceRegistry`: project records, session grouping, order, pin, archive (`packages/workspace/workspace/src/index.ts:187-188`). |
| `UP-PKG-packages.settings.settings` | `dsh-settings` | definition and provider | `ctx.settings`: forms over the volatile fields of profile entries, written through the config editor (`packages/settings/settings/src/index.ts:224`). |

### 3.2 Behaviour

**The storage hub.** A backend owns one medium and offers key-value operations on named units; the domain layer is its only consumer (`docs/subsystems/storage.md:5`, `:47`). The domain plugin's config names a default backend and per-domain routes (`packages/storage/storage-domain/src/index.ts:52-61`). Reads are synchronous from memory. A write goes to the backend first, then changes memory, then emits `domain/changed`, in one queue per domain (`packages/storage/storage-domain/src/domain.ts:167`, `:273`). The event is in-process only (`docs/subsystems/storage.md:170`). Six domains exist: `workspace`, `schedule`, `session_projcache` and three experimental ones (`packages/workspace/workspace/src/spec.ts:76`, `packages/schedule/schedule/src/storage.ts:135`, `packages/session/session-projection-cache/src/spec.ts:100`, `packages/experimental/cua/src/permission-store.ts:112`, `packages/experimental/claude-code-mods/src/host-ops.ts:76`, `packages/experimental/client-ui-forge/src/states.ts:30`).

**Attachments.** A producer hands bytes to the store, which returns a reference only after the object is durable; the session event is appended afterwards (`docs/subsystems/attachment.md:5-7`). The identifier is opaque to consumers and is `sha256:<digest>` locally (`docs/subsystems/attachment.md:13`). Images (PNG, JPEG, WebP, GIF) are checked against byte, pixel and side limits, fully decoded, then normalized: orientation applied, metadata removed, scaled down, re-encoded (`packages/attachment/attachment-local/README.md:41-49`, `:59`). A batch is validated whole before any member is saved (`packages/attachment/attachment/src/index.ts:116-123`). Objects are staged in a temporary file, synced and published by an exclusive hard link, then made read-only (`packages/attachment/attachment-local/src/store.ts:322`, `:358`, `:371`). A read recomputes the digest and probes the header against the reference (`packages/attachment/attachment-local/README.md:86`). Any other file is stored byte for byte with no type or size limit and reaches the model as one line naming a read-only path (`packages/attachment/attachment/README.md:40`, `:105`). Request versions are cached under the home and can be deleted at any time (`packages/attachment/attachment-local/README.md:90`).

**Spill.** After the other post-execute listeners accept a result, the policy estimates its token cost. Over the configured `maxInlineTokens` it saves the full text, keeps the head and the tail, and appends a notice with the file path and the hint "Use read with offset/limit, or grep this path" (`packages/spill/spill-policy/src/index.ts:99-119`, `packages/spill/spill-local/src/index.ts:160`). Images are priced by the active model route and kept or omitted whole; the saved text holds a readable path at each image position (`packages/spill/spill-policy/src/index.ts:58-88`). Results of `read`, blocked results and value replacements pass through (`:136`). With no `maxInlineTokens` the plugin does nothing (`:50-51`). Files are created exclusively with mode `0600` in a `0700` directory (`packages/spill/spill-local/src/store.ts:113-115`). With no configured root they land in a per-process directory under the system temporary directory (`packages/spill/spill-local/src/store.ts:36-38`).

**Settings.** The settings plugin stores nothing of its own. It lists the fields a plugin marked `.volatile()` in its config schema, per profile entry, with the resolved value, the inherited value, the profile override and a revision (`docs/subsystems/settings.md:9`). An edit is validated against the plugin's complete config, refused when the revision is stale, and written to the active profile's patch file through the ordinary loader reconciliation (`docs/subsystems/settings.md:13`, `packages/settings/settings/src/index.ts:391-395`). An edit that a higher-priority layer would shadow is refused (`packages/settings/settings/README.md:37`). Secret fields are redacted to presence markers (`README.md:49`). `settings/document-updated` tells form clients to re-read (`packages/settings/settings/src/types.ts:75`).

**Workspace.** A workspace record holds a canonical path (from `realpath`), a title and an ordered list of session ids; one global record holds the display order, the archive set, the pin set and a marker for an interrupted two-step mutation (`packages/workspace/workspace/src/spec.ts:24-30`, `:57-65`). A session may belong to at most one workspace, decided by its recorded working directory (`packages/workspace/workspace/README.md:73`). Archiving asks listeners of `workspace/session-activity` what still runs and refuses if anything does; with `stopActivity` it writes the archive first and then dispatches `workspace/session-stop` (`packages/workspace/workspace/src/index.ts:390-412`, `:515`). A separate gate rejects every model step of an archived session (`.agents/notes/implemented/feature/2026-09-21-archive-stops-running-session-work.md:18`). Removing a workspace never deletes its folder or its sessions (`README.md:77-79`).

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | What it leaves behind |
|---|---|---|
| A storage write fails after the file was replaced | The call rejects; the caller must close and reopen the domain to learn the outcome (`docs/subsystems/storage.md:104`) | An unknown outcome; a domain that reports `unknown-state` rejects everything until closed (`:143`) |
| Two processes write one JSON unit | Last completed replacement wins (`packages/storage/storage-json/README.md:143`) | A silently lost write |
| An image in a batch is invalid | Nothing is saved (`packages/attachment/attachment/src/index.ts:94-100`) | Nothing |
| Storage fails half-way through a batch | No references are returned | Published objects nobody references, never collected (`:96-97`) |
| A stored image is missing or corrupt | The read fails with a stable code; every later model request fails on it (`.agents/notes/proposed/bug-fix/2026-08-20-attachment-read-quarantine.md:9`) | A session that cannot continue |
| A spill save fails, or image pricing is missing | A warning; the original result stays inline in full (`packages/spill/spill-policy/src/index.ts:128-130`) | An unbounded result in the model's context |
| The notice does not fit the budget | Detected after the save; the original stays inline (`:104-114`) | An unreferenced spill file |
| Spill files age | One sweep at start-up deletes files older than 30 days, whatever references them (`packages/spill/spill-local/src/index.ts:96-118`) | Nothing until the next restart; an old locator in a live session stops working |
| A workspace create or delete is interrupted | A stored marker lets the next start finish or undo it (`packages/workspace/workspace/README.md:127`) | Nothing, if the marker itself was written |
| A stop request fails after an archive | Logged; the archive stays (`packages/workspace/workspace/src/index.ts:104-111`) | Possibly running work, bounded by the step gate |

## 4. Upstream decisions dissected

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `UP-DEC-proposed.architecture.2026-07-24-domain-kv-storage-and-workspace` | A hub with pluggable backends and a typed domain layer, so that cross-session data such as the workspace has a home (note `:9-12`, `:36-42`) | **Change** | The need is real. The hub, two backends, routes and two file layouts serve six domains and give two failure models. The first verdict ("a backend registry fits Go well") is overruled by PERSISTENCE-9 | [DH-ADR-STORAGE-1](#dh-adr-storage-1) |
| `UP-DEC-proposed.architecture.2026-07-28-storage-root-and-derived-medium-recovery` | One storage root under the harness home; a domain that holds only derived data is reset when damaged, an authoritative one fails loudly (note `:19-30`) | **Keep the idea, change the mechanism** | Everything is under the home already. Authority is declared per namespace; a derived record that cannot be decoded is dropped, an authoritative one fails its owner. Damage to the medium is damage to the database, which A05 handles | [DH-ADR-STORAGE-1](#dh-adr-storage-1) |
| `UP-DEC-implemented.architecture.2026-07-08-tool-output-spill-files` | A thin storage interface, a local backend and a policy that replaces an oversized final result with a preview and a locator (note `:17-25`) | **Keep, with three changes** | The split and the "read it later with ordinary tools" idea are sound. Changed: a failed save still bounds the result; the reference is structured data, not recognised text; the store gains read, delete and retention | [DH-ADR-STORAGE-4](#dh-adr-storage-4), [-5](#dh-adr-storage-5) |
| `UP-DEC-implemented.architecture.2026-09-19-profile-owned-live-configuration` | Every value is declared once in its plugin's config; settings only lists live fields and writes to the profile patch; a shadowed edit fails before saving (note `:13-15`) | **Keep** | One owner for shape, default, storage and notification. The Go loader's `merge` operation removes the side effect that an edit pins every other field of the entry (note `:17`); the legacy `settings.yaml` import has nothing to import | [DH-ADR-STORAGE-6](#dh-adr-storage-6) |
| `UP-DEC-implemented.feature.2026-09-21-archive-stops-running-session-work` | Archive never hides running work: refuse by default, stop on request after the durable write, and block every model step of an archived session (note `:13-18`) | **Keep, tighten** | A hidden session that spends tokens is a product defect. Providers become a Go interface; a two-phase mark closes the window between the check and the write; the archive set must work across processes | [DH-ADR-STORAGE-7](#dh-adr-storage-7) |
| `UP-DEC-proposed.bug-fix.2026-08-20-attachment-read-quarantine` | Not built upstream: record an unreadable image as a logged quarantine event and replace it with deterministic text, so one lost file does not end a session (note `:13-17`) | **Adopt** | The replacement is derived from the log, so requests stay reconstructable ([DH-O-4](../design.md#dh-o-4)); bytes are never substituted | [DH-D-STORAGE-10](#dh-d-storage-10) |
| Reads from memory; writes go disk, then memory, then event (`docs/subsystems/storage.md:143`) | Consumers never see a value that is not durable | **Keep the order, change the role of memory** | With several processes, memory is a cache checked against a revision, not the authority | [DH-ADR-STORAGE-2](#dh-adr-storage-2) |
| Domain versions with `compatibleVersions`; storage never converts values (`docs/subsystems/storage.md:100-110`) | Avoids a migration runner before the first release | **Change** | A stamp per unit cannot say which generation one record is; a version per record with a converter on read can | [DH-D-STORAGE-1](#dh-d-storage-1) |
| Persist before the event; content address; verify on read (`packages/attachment/attachment/README.md:66-68`) | The log never references a missing or different object | **Keep** | It is the rule that makes a crash leave garbage, never a dangling reference | [DH-ADR-STORAGE-3](#dh-adr-storage-3) |
| The identifier is opaque; a consumer asks the store for a path (`docs/subsystems/attachment.md:13`) | The layout can change | **Keep** | Same reason; a Go defined type enforces it | [DH-D-STORAGE-3](#dh-d-storage-3) |
| Normalize once, derive per route; limits bind admission only (`packages/attachment/attachment-local/README.md:78-80`) | History is stable; tightening a limit never breaks old images | **Keep** | Independent of the image library ([libs/sharp.md](../libs/sharp.md)) | [DH-D-STORAGE-4](#dh-d-storage-4) |
| Generic files are stored verbatim with no limit (`packages/attachment/attachment/README.md:40`) | Files are data the model reads on demand | **Change** | Verbatim storage is right; "no size limit" lets one upload fill the disk | [DH-D-STORAGE-3](#dh-d-storage-3) |
| The store is retention-neutral; collection is deferred (`packages/attachment/attachment/README.md:67`) | Forks share objects, so deleting with one session is wrong | **Change** | The reasoning is right and is exactly why collection must count references | [DH-ADR-STORAGE-3](#dh-adr-storage-3) |
| The spill seam has one method and no retrieval (`packages/spill/spill/README.md:83`) | Keep the seam small | **Change** | Without read and delete, retention and access rules cannot be stated at the seam | [DH-D-STORAGE-5](#dh-d-storage-5) |
| A spill failure keeps the full result inline (`docs/subsystems/spill.md:92`) | Never turn a successful call into an error | **Change** | It keeps success by giving up the bound, which [DH-O-6](../design.md#dh-o-6) does not allow. A truncated success keeps both | [DH-ADR-STORAGE-4](#dh-adr-storage-4) |
| `read` results are exempt, to avoid a read-then-spill loop (`packages/spill/spill-policy/src/index.ts:136`) | Reading a spill file must not spill again | **Keep as a tool property** | A name check in a policy is fragile; the tool declares that it bounds its own output | [DH-D-STORAGE-6](#dh-d-storage-6) |
| Spill files are private and cannot be redirected: random name, exclusive create (`packages/spill/spill-local/README.md:79`) | A planted link must not capture a tool result | **Keep** | Unchanged | [DH-D-STORAGE-5](#dh-d-storage-5) |
| Forms show only volatile fields; optimistic revisions; redacted secrets (`packages/settings/settings/README.md:33`, `:49`) | A form must not restart the system or leak a secret | **Keep** | The revision becomes a content hash so that two processes agree on it | [DH-ADR-STORAGE-6](#dh-adr-storage-6) |
| One record per canonical path; a generated id is the key; the ledger alone decides ownership (`packages/workspace/workspace/README.md:93-94`) | A path can be rewritten; ownership is a workspace fact | **Keep** | Validated once at attach instead of on every read | [DH-ADR-STORAGE-7](#dh-adr-storage-7) |
| Two-write mutations with a stored marker (`packages/workspace/workspace/README.md:96`) | The backend has no transaction across records | **Drop** | One database transaction; the marker has no purpose | [DH-D-STORAGE-7](#dh-d-storage-7) |
| Removing a workspace never touches the folder or the sessions; a first-use default workspace is created once (`README.md:65-69`, `:77-79`) | Registration is not ownership of the directory | **Keep** | The directory is the user's; see [§6.3](#63-ownership-failure-retry-rollback-and-cleanup) | [DH-ADR-STORAGE-7](#dh-adr-storage-7) |

## 5. Tech debt and its resolution

Debt IDs are shortened: `limit.<x>` stands for `UP-DEBT-limit.packages.<x>`. Every row is `resolved` or `not-applicable` in the ledger; none is carried.

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `limit.storage.storage.1` | `kv` is the only data shape; a `log` facet is deferred | `packages/storage/storage/README.md:130` | Resolved: there are no facets. The session log has its own store in the same database (A05); machine state uses typed tables ([DH-ADR-STORAGE-1](#dh-adr-storage-1)) |
| `limit.storage.storage.2` | Reading a form before its plugin mounts throws | `README.md:131` | Resolved: a consumer injects the service by typed key and is not constructed until it is provided ([DH-D-STORAGE-1](#dh-d-storage-1)) |
| `limit.storage.storage-domain.1` | Change events are in-process; a second process sees nothing | `packages/storage/storage-domain/README.md:146` | Resolved: a namespace revision detects foreign writes ([DH-ADR-STORAGE-2](#dh-adr-storage-2)) |
| `limit.storage.storage-domain.2` | No cross-table transactions, indexes or composite keys | `README.md:147` | Resolved: `Update` is one SQL transaction; entities that need indexes or composite keys are real tables ([DH-D-STORAGE-1](#dh-d-storage-1), [-7](#dh-d-storage-7)) |
| `limit.storage.storage-domain.3` | No migration runner for incompatible values | `README.md:148` | Resolved: a version per record and a registered converter on read; layout changes use the store migration of PERSISTENCE-6 ([DH-D-STORAGE-1](#dh-d-storage-1)) |
| `limit.storage.storage-json.1` | `single` rewrites the whole unit on every write | `packages/storage/storage-json/README.md:142` | Resolved: no JSON backend; a write changes one row ([DH-ADR-STORAGE-1](#dh-adr-storage-1)) |
| `limit.storage.storage-json.2` | No cross-process write lock; last writer wins | `README.md:143` | Resolved: the database serialises writers with a bounded wait ([DH-ADR-STORAGE-2](#dh-adr-storage-2)) |
| `limit.storage.storage-json.3` | Windows rename without write-through | `README.md:144` | Resolved: no state file is published by rename; the files that remain (blobs, the request cache) use `fsx` ([DH-ADR-STORAGE-3](#dh-adr-storage-3)) |
| `limit.storage.storage-json.4` | A per-record batch delete is not crash-atomic | `README.md:145` | Resolved: a batch is one transaction ([DH-D-STORAGE-1](#dh-d-storage-1)) |
| `limit.storage.storage-sqlite.1` | The synchronous driver blocks the event loop | `packages/storage/storage-sqlite/README.md:130` | Not applicable: Node has one thread; in Go a database call blocks one goroutine |
| `limit.storage.storage-sqlite.2` | No busy wait; a competing writer is rejected at once | `README.md:131` | Resolved: one bounded busy wait and one classified error ([DH-ADR-STORAGE-2](#dh-adr-storage-2); `DH-LIB-SQLITE` R4) |
| `limit.storage.storage-sqlite.3` | Only the current physical layout opens | `README.md:132` | Resolved: tables join the one database's forward-only layout migration with backup (A05, PERSISTENCE-6) ([DH-D-STORAGE-1](#dh-d-storage-1)) |
| `limit.storage.storage-sqlite.4` | Version checks are unit-wide | `README.md:133` | Resolved: the version is a column of every record ([DH-D-STORAGE-1](#dh-d-storage-1)) |
| `limit.storage.storage-sqlite.5` | The open sequence is duplicated with the search provider | `README.md:134` | Resolved: one database opened by one package ([DH-ADR-STORAGE-1](#dh-adr-storage-1)) |
| `limit.attachment.attachment.1` | Limits apply to images only; other files have no type or size limit; no audio or video handling | `packages/attachment/attachment/README.md:118` | Resolved for size: a per-file and a per-message limit ([DH-D-STORAGE-3](#dh-d-storage-3)). Audio and video stay generic files by decision; see [§10](#10-open-questions-and-limitations) |
| `limit.attachment.attachment.2` | Attachments are never deleted | `README.md:119` | Resolved: reference-aware collection ([DH-D-STORAGE-9](#dh-d-storage-9)) |
| `limit.attachment.attachment.3` | An unsent draft lives only in the browser | `README.md:120` | Resolved at the storage level: a draft record per session that also keeps its staged attachments alive ([DH-D-STORAGE-3](#dh-d-storage-3)); the composer (area C3) writes it |
| `limit.attachment.attachment-local.1` | Images are kept forever | `packages/attachment/attachment-local/README.md:137` | Resolved: as `attachment.2` ([DH-D-STORAGE-9](#dh-d-storage-9)) |
| `limit.attachment.attachment-local.2` | Objects are local to one machine | `README.md:138` | Resolved for the two cases where it hurts: a remote execution world reads by identifier through the host, and a session export carries its objects ([DH-D-STORAGE-3](#dh-d-storage-3)). Two machines sharing one live store is not a goal ([DH-ADR-12](../design.md#dh-adr-12)) |
| `limit.attachment.attachment-local.3` | An animated GIF becomes its first frame | `README.md:139` | Resolved: the model still gets one frame, and the original bytes are kept as a linked file so the transcript can show the animation ([DH-D-STORAGE-4](#dh-d-storage-4)) |
| `limit.attachment.attachment-local.4` | Output bytes depend on the installed native library build | `README.md:140` | Resolved: encoders are Go code pinned by `go.mod`; a transform version is part of every cache identity ([DH-ADR-SHARP-1](../libs/sharp.md#dh-adr-sharp-1), [DH-D-STORAGE-4](#dh-d-storage-4)) |
| `limit.spill.spill.1` | No retrieval or deletion API | `packages/spill/spill/README.md:135` | Resolved: `Open`, `Stat` and `Delete` are part of the contract ([DH-D-STORAGE-5](#dh-d-storage-5)) |
| `limit.spill.spill.2` | The owner session names a directory but authorises nothing | `README.md:136` | Resolved at the seam: `Open` checks that the caller's session owns the file or descends from the owner ([DH-ADR-STORAGE-5](#dh-adr-storage-5)). Reading the path with a file tool stays possible for any session of the same user, the general gap of [../design.md §6](../design.md#6-security-model) |
| `limit.spill.spill-local.1` | A long-running host is not swept until restart | `packages/spill/spill-local/README.md:126` | Resolved: the maintenance duty runs at start and on a timer ([DH-D-STORAGE-9](#dh-d-storage-9)) |
| `limit.spill.spill-local.2` | A locator needs a consumer on the same filesystem | `README.md:127` | Resolved: the path is mapped through the filesystem seam, and where it cannot be mapped the `spill_read` tool reads on the host ([DH-ADR-STORAGE-5](#dh-adr-storage-5)) |
| `limit.spill.spill-policy.1` | A notice is recognised by its text, which a tool can print | `packages/spill/spill-policy/README.md:146` | Resolved: the logged result carries a structured spill field only the policy sets ([DH-ADR-STORAGE-4](#dh-adr-storage-4)) |
| `limit.spill.spill-policy.2` | Without pricing or a readable image path the original stays visible | `README.md:147` | Resolved: a fallback image cost and truncation without a file keep the bound ([DH-ADR-STORAGE-4](#dh-adr-storage-4)) |
| `limit.spill.spill-policy.3` | A notice that cannot fit disables replacement and leaves an orphan file | `README.md:148` | Resolved: the budget has a validated minimum and the notice is sized before the save ([DH-D-STORAGE-6](#dh-d-storage-6)) |
| `limit.settings.settings.1` | Entries inside nested includes are not editable | `packages/settings/settings/README.md:73` | Resolved: a form addresses an entry by its id in the composed tree and the one writable layer may hold an operation for any entry ([DH-ADR-STORAGE-6](#dh-adr-storage-6)); A21 confirms nested addressing |
| `limit.settings.settings.2` | A reset cannot delete a value a lower layer supplies | `README.md:74` | Resolved: `reset` removes the override, and `clear` writes the `merge` operation's null, which deletes the lower value ([DH-D-STORAGE-8](#dh-d-storage-8)) |
| `limit.workspace.workspace.1` | Removal never deletes data; session deletion is absent | `packages/workspace/workspace/README.md:170` | Resolved: session deletion exists (A05) and removal can cascade to it on request ([DH-D-STORAGE-7](#dh-d-storage-7)). The folder is never deleted, by decision ([DH-ADR-STORAGE-7](#dh-adr-storage-7)) |
| `limit.workspace.workspace.2` | A missing path keeps recorded ownership that cannot be verified | `README.md:171` | Resolved: membership is checked once, at attach, and stored; nothing is re-derived from a path later ([DH-ADR-STORAGE-7](#dh-adr-storage-7)) |
| `limit.workspace.workspace.3` | Outside changes to a directory are seen late | `README.md:172` | Resolved: directory status is read when the list is rendered, with a two-second cache ([DH-D-STORAGE-7](#dh-d-storage-7)) |
| `limit.workspace.workspace.4` | Archive and unarchive check the session differently | `README.md:173` | Resolved: flags reference the sessions table and are removed with the session, so a flag for a missing session cannot exist ([DH-D-STORAGE-7](#dh-d-storage-7)) |
| `limit.workspace.workspace.5` | The activity check and the archive write are two steps | `README.md:174` | Resolved: a pending mark is written first and already blocks new steps; the check follows; the mark is then confirmed or removed ([DH-ADR-STORAGE-7](#dh-adr-storage-7)) |
| `limit.workspace.workspace.6` | Re-adding a directory starts with no sessions | `README.md:175` | Resolved: adding a workspace can adopt ungrouped sessions that ran in that directory ([DH-D-STORAGE-7](#dh-d-storage-7)) |
| `UP-DEBT-marker.packages/workspace/workspace/src/index.ts:258` | `create(path, title?)` kept a parameter with no caller | `packages/workspace/workspace/src/index.ts:258` | Resolved: `Add` takes no title; `Rename` sets it ([DH-D-STORAGE-7](#dh-d-storage-7)) |
| `UP-DEBT-found.A07.1` | A settings edit stores the entry's complete config, so later bundle changes to its other fields never arrive | `.agents/notes/implemented/architecture/2026-09-19-profile-owned-live-configuration.md:17` | Resolved: an edit writes a `merge` operation holding only the edited fields ([DH-ADR-STORAGE-6](#dh-adr-storage-6)) |
| `UP-DEBT-found.A07.2` | With no configured root, spill files land in a per-process temporary directory, so a resumed session's locators point into an old directory that a later sweep may remove | `packages/spill/spill-local/src/store.ts:36-38` | Resolved: spill files always live under the harness home ([DH-D-STORAGE-5](#dh-d-storage-5)) |

## 6. Go design

### 6.1 Shape

Where each kind of upstream state lands:

| Upstream state | Upstream medium | Go home | Owner |
|---|---|---|---|
| Workspace records, order, archive and pin sets | `workspace` domain, JSON file | Tables `workspaces`, `workspace_sessions`, `session_flags` in the one database | this area |
| Schedules; in Go also the model-listing cache, the upload index and the Windows sandbox journal | `schedule` domain; none | Namespaces or tables in the same database, through `kv` | A13, A04, A10 |
| Projection cache | `session_projcache` domain | Projection checkpoints committed with events (PERSISTENCE-7) | A05 |
| Three experimental stores | domains | `kv` namespaces if kept. A stored computer-use permission list conflicts with [DH-ADR-16](../design.md#dh-adr-16) | X areas |
| Attachment objects; in Go also change hunks and presented copies (A08) | Files under `attachments/v1`; memory | Files under `blobs/v1`; reference rows in the database | this area |
| Request versions of images | Files under `cache` | Files under `cache/request-images`, bounded | this area |
| Spill files | Files under a root or a temporary directory | Files under `spill/`; reference rows in the database | this area |
| Composer drafts | Browser memory only | `kv` namespace `draft` | this area stores, C3 uses |
| Settings values | The profile patch file | `merge` operations in the one writable layer | A21 stores, this area edits |

Packages under `src/harness/internal/storage`:

| Package | Component and service | Why it exists |
|---|---|---|
| `home` | No service. Paths of the layout below and the permission check. `home.Layout` exposes `Secrets` and `Run` as the two paths every sandbox backend denies to a confined command (A10) | One place knows where things are and who may read them |
| `kv` | Component `storage.kv`, service key `kv.Key`, interface `kv.Store` | Typed machine state with one failure model |
| `blob` | Component `storage.blob`, key `blob.Key`, interface `blob.Store`; the `blob.Referrer` interface | Content-addressed bytes for any area: attachments are built on it, and area A08 stores change hunks and presented copies in it |
| `attachments`, `attachments/local`, `attachments/image` | Direct seam: contract `attachments.Store` (key `attachments.Key`), provider component `storage.attachments.local`, the image pipeline | The store can change provider without touching consumers ([DH-O-10](../design.md#dh-o-10)) |
| `spill`, `spill/local`, `spill/policy`, `spill/tool` | Direct seam: contract `spill.Store` (key `spill.Key`), provider `storage.spill.local`, policy component `storage.spill.policy`, tool component `storage.spill.tool` | Storage, the decision to spill and the model-facing tool are separate components, so a preset mounts only what it needs |
| `workspace` | Component `storage.workspace`, key `workspace.Key`, interface `workspace.Registry`; the `ActivityProvider` registry. Creating a workspace for a directory honours the `Roots` of A18's directory browser: a path outside them is refused | The project list and the archive rule |
| `settings`, `settings/layer` | Direct seam: contract `settings.Forms` (key `settings.Key`), provider `storage.settings.layer` over boot's `config.Service` (A21) | Forms without a second store |
| `retention` | Component `storage.retention`, key `retention.Key` (`Register(name, step)`) | The once-per-home maintenance duty; other areas add their own sweep steps |

An import-direction test fails if a consumer imports a provider package (ARCH-6). The main interfaces, one per contract package (`ctx` is a `context.Context`, first in every method):

```go
// kv: one namespace has one owner, which closes it with its fiber.
type Store interface { Open(ctx, spec Spec) (Namespace, error) } // Spec: Name, Version, Authority (Authoritative | Derived)
// A key is one string; a namespace that needs a composite key joins its parts with kv.Key(parts...) (C1's ui-prefs: principal, name, key).
type Table[T any] interface {                    // from kv.TableOf[T](ns, upgrade)
    Get(ctx, key string) (T, bool, error)        // a copy, from a snapshot at a known revision
    All(ctx) (map[string]T, Revision, error)
    Update(ctx, fn func(tx Tx[T]) error) error   // one transaction: disk, then memory, then notice
    Fresh(ctx) error                             // reload if another process advanced the revision
} // kv.Counter(ctx, name): a named revision row that an area's own tables bump in their transaction (A13's schedules)
// blob: bytes named by their SHA-256; a reference is returned only after the object is durable.
type Store interface {
    Put(ctx, kind Kind, r io.Reader, limit int64) (Ref, error)
    Open(ctx, ref Ref) (io.ReadCloser, error)              // digest and length verified while streaming
    Touch(ctx, refs ...Ref) error                          // before an event cites a stored reference
    ReferencedBy(ctx, s SessionID, ref Ref) (bool, error)  // does this session, or a session it was forked from, cite the object?
}
// attachments: admission, verified reads and per-route image versions, on top of blob.
type Store interface {
    AdmitImages(ctx, in []ImageInput) ([]ImageRef, error)  // all or none
    SaveFile(ctx, name string, r io.Reader) (FileRef, error)
    Open(ctx, ref Ref) (io.ReadCloser, error)              // image or file, verified against the reference
    RequestImage(ctx, ref ImageRef, t Target) (RequestImage, error)
    Crop(ctx, ref ImageRef, region Rect) (ImageRef, error) // a new stored image, for read_image (A08)
    HostPath(ref Ref) (string, bool)                       // for the filesystem seam to map
    LookupPath(hostPath string) (Ref, bool)                // is this path one of the store's objects?
}
// spill: full text out of the model's sight, retrievable later.
type Store interface {
    Reserve(owner SessionID, name string) Ref              // fixed-size id, nothing written yet
    Create(ctx, ref Ref, src Source, limit int64) (Writer, error) // Close syncs; Abort removes the file
    Open(ctx, caller SessionID, id string) (io.ReadSeekCloser, error) // the owner or its forks only
    Stat(ctx, id string) (Info, error)
    Delete(ctx, owner SessionID) error
    HostPath(id string) (string, bool)
}
// workspace: who can say what runs for a session, and stop it.
type ActivityProvider interface {
    Report(ctx, s SessionID) ([]Activity, error)
    Stop(ctx, s SessionID) error                           // the user's own stop path; does not wait for settlement
}
// settings: forms over the composed configuration.
type Forms interface {
    Describe(ctx) ([]Form, error)                          // secrets as presence markers
    Apply(ctx, entry EntryID, ops []PathOp, at Revision) (Form, error) // set | reset | clear; one config.Service.Apply call
    DocumentPath() string
}
```

Home layout. This area owns the layout as a whole; the first and last lines hold other areas' files:

```
<home>/store/dsh.sqlite, <home>/store/run/              the database and instance locks, as area A05 proposes
<home>/blobs/v1/<kind>/<2 hex>/<sha256>                 objects; kind is image, file or data
<home>/blobs/v1/names/<2 hex>/<sha256>/<name>           read-only named link to a file object
<home>/blobs/tmp/, <home>/blobs/gc.lock                 staging on the same volume; the collection lock
<home>/spill/<16 hex of SHA-256(session id)>/<24 hex random>-<safe name>
<home>/cache/request-images/<variant id>                derived, deletable
<home>/config/, <home>/secrets/, <home>/plugins/         layers (A21), file secret store (A17), approved plugin copies and state (A22)
<home>/run/                                             runtime files of other areas
```

Kinds, for backup and cleaning: `config` is configuration; `store`, `blobs`, `spill`, `secrets` and `plugins` are data; `run` is state a restart recreates; `cache` is deletable.

**What sharing one home requires of each store** ([DH-ADR-12](../design.md#dh-adr-12)):

| Store | Requirement |
|---|---|
| `kv` and the workspace tables | Every write is a database transaction with the bounded busy wait. Memory is a cache tagged with the namespace revision. A read that gates an action calls `Fresh` first. No process assumes it is the only writer |
| Blobs and attachments | Publishing is idempotent: the same digest is the same bytes, and an existing name is success. A saver holds `gc.lock` shared; the collector holds it exclusively. No process keeps an index of objects in memory. A request-cache entry is replaced atomically, and two processes that derive the same variant write the same bytes |
| Spill | Names are random and created exclusively, so writers never collide. Nothing is cached |
| Settings | Every write is one `config.Service.Apply` (A21), which takes boot's layer lock, re-reads the layers, checks and reconciles. This area takes no lock and writes no file. The revision is boot's per-entry revision, the same in every process |
| Maintenance | One process at a time, under area A05's duty lease. Every step may be repeated safely |

### 6.2 Contracts

**Events.** Live events are typed keys declared by the owning package, in the dispatch forms of [../design.md §4.6](../design.md#46-event-model).

| Go event or interface | Form | Replaces | Payload and rule |
|---|---|---|---|
| `kv.Changed` | notify | `UP-EVT-domain/changed` | Namespace, key, operation (`put`, `delete`), revision, origin (`local`, `foreign`). Raised after commit, in commit order per namespace. No value: a reader calls `Get`. A foreign change is raised when a refresh sees a new revision |
| `settings.FormChanged` | notify | `UP-EVT-settings/document-updated` | Entry id and new revision. Raised when the loader announces a settled reload and a form's revision differs |
| `workspace.ActivityProvider.Report` | registry call, results concatenated | `UP-EVT-workspace/session-activity` | An interface instead of a waterfall: a provider cannot drop another provider's answer. An error counts as "something runs" |
| `workspace.ActivityProvider.Stop` | registry call, all at once, failures aggregated and logged | `UP-EVT-workspace/session-stop` | Called after the archive is durable; a failure does not undo it |
| `workspace.SessionArchived` | parallel | `UP-EVT-workspace/session-archived` | Session id. Listeners register follow-up work and return; a failure is logged |
| `workspace.SessionUnarchiving` | parallel | `UP-EVT-workspace/session-unarchiving` | Session id. Any failure keeps the session archived |
| `workspace.Changed` | notify | none (upstream reuses `domain/changed`) | Which workspace or session flag changed, for the sidebar fragment |

Durable session events registered by this area (payload version 1): `attachment/quarantined` and `attachment/recovered` (reference, failure class), both log-only. The tool-result event of area A01 gains an optional field `spill` (id, saved bytes, omitted bytes, omitted images); payload structs that cite a blob or a spill file implement `blob.Referrer`.

**Config.** Go structs with defaults as a Go value, decoded strictly (SCHEMA-1, SCHEMA-3).

| Go struct | Replaces | Fields and defaults |
|---|---|---|
| `attachments.Config` | `UP-CFG-dsh-attachment-local` | Upstream's nine limits with the same defaults (`packages/attachment/attachment-local/src/index.ts:152-164`), except: `dshHome` is gone (the home comes from boot); `imageCompressionConcurrency` becomes `DecodeBudgetPixels` (64,000,000 pixels in flight per process); new `MaxFileBytes` (512 MiB), `MaxFilesPerMessage` (20), `RequestCacheMaxBytes` (1 GiB) |
| `spill.StoreConfig` | `UP-CFG-dsh-spill-local` | `MaxAge` (720 h, live; 0 keeps a file while it is referenced), `MaxSaveBytes` (64 MiB). `root` is gone: files live under the home |
| `spill.PolicyConfig` | `UP-CFG-dsh-spill-policy` | `MaxInlineTokens` (12,500, live, minimum 512), `ImageTokenFallback` (1,500). The policy has no "off" value |
| `retention.Config`, `kv.Config` | none | `Interval` (6 h), `UnreferencedGrace` (24 h, minimum 1 h); `RefreshInterval` (1 s) |
| none | `UP-CFG-dsh-storage-domain`, `UP-CFG-dsh-storage-json`, `UP-CFG-dsh-storage-sqlite` | Dropped: there are no backends to route to, no JSON root, and the database path and WAL mode are fixed by A05 and [DH-ADR-12](../design.md#dh-adr-12) |

Inference: 12,500 tokens is taken from upstream's own example configurations (`packages/spill/spill-policy/README.md:38`); upstream's default is "off". This budget and the other new defaults are recommended defaults, not confirmed by the owner.

**Persisted data.** Tables are created by the one database's layout migration and written through its driver package (A05).

| Table | Columns | Rule |
|---|---|---|
| `kv` | `namespace`, `key`, `version`, `value` (JSON), `updated_at`; key `(namespace, key)` | A record whose version is newer than the binary knows fails the read of that record |
| `kv_ns` | `namespace`, `revision`, `authority` | The revision is increased in the transaction of every write |
| `workspaces` | `id`, `path` (canonical), `requested_path`, `title`, `position`, `created_at`, `updated_at`; `path` unique | |
| `workspace_sessions` | `session_id` (unique, references sessions, deleted with the session), `workspace_id`, `position` | One session, at most one workspace |
| `session_flags` | `session_id` (references sessions, deleted with it), `archive_state` (`pending`, `archived`), `archived_at`, `pinned_at` | A pending or archived session is never pinned |
| `blob_refs` (area A05's table) | `session`, `seq`, `ref` | This area defines `ref`: `<kind>:<id>` with kind `image`, `file`, `data` or `spill`. A05 writes the rows in the append transaction from `blob.Referrer`, copies them on fork and removes them on delete. A draft lists its references in its own `draft` record, which the collector also reads |

**Tools exposed to the model.** One, `spill_read`: arguments `id`, optional `offset`, `limit` and `pattern`; the result is a bounded slice of the saved text and is never spilled again. It is mounted only for an agent whose filesystem provider cannot map the spill directory, so a local session pays no prompt tokens for it.

**What the model sees for an oversized result.** The preview keeps the original order: head, a `[...]` marker, tail, then one notice, for example `(Omitted 48211 bytes. Full result: <path>, kept until 2026-12-18. Use read with offset and limit, or grep.)`. When the file could not be saved the notice says so and gives the reason, and the preview is all there is. The text is logged as shown ([DH-O-4](../design.md#dh-o-4)).

**Errors.** One `<domain>/<reason>` code each: `storage/busy`, `storage/record-version`, `attachment/invalid-image`, `attachment/too-large`, `attachment/too-many`, `attachment/unsupported-type`, `attachment/not-found`, `attachment/corrupt`, `attachment/read-failed`, `spill/not-found`, `spill/not-owner`, `spill/too-large`, `workspace/session-active`, `workspace/not-a-directory`, `settings/conflict`, `settings/rejected`, `settings/shadowed`, `settings/unsaved`.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership.** Each component's fiber owns its timers, its open namespaces, its lock handles and its in-flight transforms, and releases them when it unloads. The harness home is shared: a process has access to it and never assumes it owns it.

| Resource | Class | Who releases it, and what cleanup may undo |
|---|---|---|
| A `kv` record, a workspace row, a flag | Created | The owning package, by an explicit delete. Rows that reference a session go with the session |
| A blob: an attachment object, a change hunk, a presented copy | Created, shared between sessions and forks | Only the collector, and only when no reference row and no draft names it and it is older than the grace period. The area that stored it never deletes it |
| A spill file | Created for one session, readable by its forks | The collector: unreferenced and past the grace period, or past `MaxAge`; or `Delete` when the session is deleted and no fork cites it |
| A request-cache entry, a staging file | Created, derived | Anyone; rebuilt on demand |
| The workspace directory and its files | The user's; never adopted | Nobody in the harness. Registration gives the right to list it, not to delete it |
| The writable layer file | Shared with the person who edits it | Never deleted; a hand edit wins a conflict |

**Access.** `blob.Open` and `attachments.Open` verify bytes and do not decide who may see them; the calling route applies the access check (area A19). `spill.Open` checks session lineage. A path from `HostPath` is given only to the filesystem seam.

| Situation | What happens | Undone, retried or left uncertain |
|---|---|---|
| A `kv` commit fails or reports busy | Memory is unchanged, no notice is raised, the caller gets `storage/busy` or the I/O error | The caller may retry. If the commit's outcome is unknown the namespace reloads from the database before its next use; there is no quarantined state |
| A stored record cannot be decoded | In an authoritative namespace the read fails and the owning component fails to load; in a derived namespace the record is dropped and logged | Nothing is deleted from an authoritative namespace automatically |
| An image in a batch is refused, or a transform is cancelled or panics | Nothing is published; a decoder panic becomes `attachment/invalid-image` | A staging file may remain; the sweep removes it |
| The host dies after publishing objects and before the event | The objects have no reference row | The collector removes them after the grace period; never a dangling reference |
| A referenced object is missing or corrupt | The read returns `attachment/not-found` or `attachment/corrupt`; the request path records a quarantine and continues ([DH-D-STORAGE-10](#dh-d-storage-10)) | **Uncertain:** whether the bytes can be restored; they are never replaced automatically |
| A spill write fails or exceeds its limit | The policy truncates the result to the budget with a notice that says the rest was not saved; a stream writer (area A09) is aborted and its file removed | The omitted part is lost; the tool call is still a success |
| The collector dies mid-run | The kernel releases the collection lock and the duty lease can be taken by another process | The next run repeats; a deleted file was garbage already |
| Archive: a provider's `Report` fails | Treated as active; the pending mark is removed | Nothing changed |
| Archive: the host dies between the pending mark and the confirmation | At start, a pending mark older than one minute whose session no live process holds is removed | The session is visible again; nothing was stopped |
| A `Stop` fails after an archive | Logged; the archive stays; the step gate blocks further model steps | **Uncertain:** a job that ignores its stop keeps running without model requests |
| A settings edit fails validation, or its revision is stale | Refused before anything is written; the running tree is unchanged | Nothing |
| The writable layer cannot be saved | `config.Service.Apply` returns `unsaved`: the edit stays applied in memory and the form reports `settings/unsaved` until boot's flush succeeds ([../design.md §5](../design.md#5-failure-retry-rollback-and-cleanup)) | The person knows the edit is not saved |

## 7. Decisions

<a id="dh-adr-storage-1"></a>
### DH-ADR-STORAGE-1 No storage hub: machine state is typed tables in the one database
- **Decision.** Apply [DH-ADR-PERSISTENCE-9](../upstream/session-and-persistence.md#dh-adr-persistence-9). There is no hub, no backend registry, no JSON backend and no route table. State with relations or ordering (workspaces) gets real tables. Small state gets a namespace in one `kv` table with a version per record. Each namespace declares whether it is authoritative or derived.
- **Why.** One medium has one failure model, one backup and one way to be shared by several processes. Upstream's generality served six domains.
- **Tradeoff.** An operator can no longer read state as a JSON file; a `dsh` command must show it (area A24). Damage to the database affects all state at once (handled by A05). Adding a table is a layout migration.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-STORAGE-1`, `-2`, `-3`.

<a id="dh-adr-storage-2"></a>
### DH-ADR-STORAGE-2 Memory is a cache checked against a stored revision
- **Decision.** Every write to a namespace or to the workspace tables increases a stored revision in the same transaction. A process serves reads from memory at a known revision. Before a read that gates an action (is this session archived?) it compares the revision and reloads if it differs. A timer does the same once a second and raises `kv.Changed` with origin `foreign` for what changed.
- **Why.** It answers the open question of [../design.md §4.7](../design.md#47-storage): how cached machine state stays current when another process writes. A gate must not act on stale data; a sidebar may be a second late.
- **Tradeoff.** A gating read costs one indexed row read. A foreign change is noticed after up to one refresh interval. Polling was chosen over a cross-process notification channel because it needs no extra mechanism and fails safe. Inference: the database-wide change counter that area A05 polls for sessions can skip the row read when nothing changed at all; the stored revision says which namespace changed.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-STORAGE-2`, [DH-E-STORAGE-1](#dh-e-storage-1).

<a id="dh-adr-storage-3"></a>
### DH-ADR-STORAGE-3 Blobs are files named by SHA-256, referenced by rows, collected under a lock
- **Decision.** Bytes an event must be able to reference after a restart (attachments, and other areas' data such as change hunks) are files under the home, named by the SHA-256 of their content ([DH-ADR-NODECRYPTO-1](../libs/node-crypto-timers-perf-hooks.md#dh-adr-nodecrypto-1)), stored before the event that cites them and verified on read. Every citing event gets `blob_refs` rows in its append transaction (area A05's table); a draft lists its references in its own record. The collector deletes an object only when no row and no draft names it and its modification time is older than the grace period. A saver that finds the object already present updates its modification time; savers hold `gc.lock` shared and the collector holds it exclusively for each batch.
- **Why.** The order "blob, then event" means a crash leaves garbage, never a dangling reference. The lock closes the one race that order leaves open: an old unreferenced object being re-attached while the collector is deleting it.
- **Tradeoff.** Two stores (files and rows) must be kept in step by rule, not by one transaction. A staged upload that is cited after the grace period must be re-checked, which is why admission calls `Touch` and verifies existence before the event is appended. Savers wait while the collector works on a batch.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-STORAGE-6`, `-7`, `-14`, `-15`, [DH-E-STORAGE-2](#dh-e-storage-2).

<a id="dh-adr-storage-4"></a>
### DH-ADR-STORAGE-4 A tool result is always bounded; the spill reference is data, not text
- **Decision.** The policy is a listener on the tool pipeline's shape-and-bound stage (area A03), so it runs for every entry into the pipeline, and its budget has no "off" value. A result over the budget is replaced by a preview and a notice. If the full text cannot be saved, or an image cannot be priced or located, the result is still cut to the budget and the notice says what was lost. The logged result carries a structured `spill` field that only the policy sets. A tool that bounds its own output declares it and is skipped. The pipeline's own byte ceiling applies afterwards and is the bound where this component is not mounted.
- **Why.** [DH-O-6](../design.md#dh-o-6) says results are bounded. Upstream's best-effort rule gives up the bound exactly when something is already wrong. A structured field lets the UI and retention trust a spill reference without parsing text a tool could imitate.
- **Tradeoff.** On a save failure the middle of the output is unrecoverable, where upstream would have shown all of it. The budget is an estimate, not a tokenizer's count. The fallback image cost may omit an image a route could have afforded.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-STORAGE-10`, `-11`, `-12`, [DH-E-STORAGE-3](#dh-e-storage-3).

<a id="dh-adr-storage-5"></a>
### DH-ADR-STORAGE-5 A spill file can be read from any execution world, and only by its session's line
- **Decision.** The locator shown to the model is the host path mapped through the filesystem seam. Where the active provider cannot map it (a remote host), the `spill_read` tool is mounted and named in the notice. `spill.Open`, which that tool uses, allows the owning session and sessions forked from it, and nobody else.
- **Why.** [DH-O-10](../design.md#dh-o-10): changing the filesystem provider must not break retrieval. A fork inherits its parent's notices, so it must be able to follow them.
- **Tradeoff.** One more tool in remote sessions. A local path can still be read by any session of the same user that learns it; names are random and directories owner-only, and [../design.md §6](../design.md#6-security-model) already states that reads are unconfined.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-STORAGE-9`, `-13`, [DH-E-STORAGE-4](#dh-e-storage-4).

<a id="dh-adr-storage-6"></a>
### DH-ADR-STORAGE-6 Settings is a view over the one writable layer, not a store
- **Decision.** A form lists the fields a component's config type tags as live (CF-3 in [../design.md §8](../design.md#8-conflicts-and-gaps-between-the-foundation-recommendations)). An edit changes configuration only through boot's one edit path, `config.Service.Apply` ([boot.md §6.2](boot.md#62-contracts), "The edit seam", [DH-ADR-BOOT-14](boot.md#dh-adr-boot-14), [DH-D-BOOT-7](boot.md#dh-d-boot-7)): this area translates the form's field operations into `Merge` and `Reset` operations on the entry and passes the revision the form was rendered at. Boot composes the candidate, decodes it strictly into the component's config struct, validates it, runs the static check, the shadow check and the computed-field rule under its cross-process lock, writes the profile layer and reconciles. This area never writes a layer file and never calls the loader's write path. The revision is boot's revision of the entry. The running component receives the change through the loader's update hook ([DH-ADR-LOADER-7](../libs/cordis-loader.md#dh-adr-loader-7)) and applies it or is restarted alone.
- **Why.** A second settings store would have to be reconciled with the layers after every restart and concurrent edit. Writing only the edited fields removes upstream's pinning (`UP-DEBT-found.A07.1`).
- **Tradeoff.** A saved value lives in the user's layer and shadows later bundle changes to that one field; the form shows which layer supplied each value. Fields that are not live are edited in the layer file. Secret fields hold credential references ([DH-ADR-SECURITY-6](../upstream/security.md#dh-adr-security-6)), so the form shows presence, never a value.
- **Serves.** [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-STORAGE-19`, `-20`, `-21`, [DH-E-STORAGE-5](#dh-e-storage-5).

<a id="dh-adr-storage-7"></a>
### DH-ADR-STORAGE-7 Workspaces: a stored ledger, a two-phase archive, and no power over the directory
- **Decision.** Membership is a row written when a session is attached, after its recorded working directory resolved to the workspace's canonical path; it is never re-derived. Archive and pin flags are rows, not session events, so a process that does not hold a session's lease can still archive it. Archiving writes a `pending` mark (which the step gate already treats as blocked), asks every activity provider, then confirms or removes the mark; with "stop" it confirms first and then asks providers to stop. A session whose lease another live process holds reports that as activity, and that process stops its own work when it sees the flag. The harness never deletes a workspace directory.
- **Why.** The pending mark closes upstream's window between the check and the write. Rows work across processes where an in-memory set does not. The directory is the user's.
- **Tradeoff.** A crash can leave a pending mark, cleared at the next start. A foreign process reacts within one refresh interval, so at most one more model step may begin there. Session flags are not part of a session export.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-STORAGE-16`, `-17`, `-18`, [DH-E-STORAGE-1](#dh-e-storage-1).

<a id="dh-adr-storage-8"></a>
### DH-ADR-STORAGE-8 Stored data is owner-only, kept by stated defaults, and not encrypted by the harness
- **Decision.** These are the proposed answers to open questions 8 and 9 of [../upstream/session-and-persistence.md](../upstream/session-and-persistence.md), for the stores of this area; A05 states the rule for the database file.
  - *Permissions.* Directories are created `0700` and files `0600` (objects `0400`), whatever the process mask. At start, on POSIX, a store directory owned by another user or writable by group or others stops the component that uses it. On Windows the mode bits mean nothing (`DH-LIB-NODEFS` R8): a home the harness creates gets an access-control list for the current user, SYSTEM and Administrators only; an existing home is checked and a wider list is reported.
  - *Retention.* Attachments: kept while referenced; unreferenced after 24 hours are collected. Spill: 30 days from creation, or sooner once unreferenced and past the grace period. Request cache: 1 GiB, oldest first. Staging files: 24 hours. Machine state: until its owner deletes it. Only the operator changes these, through configuration; no tool and no plugin can.
  - *Encryption at rest.* None. The harness relies on the operating system's disk encryption.
- **Why.** Permissions keep other local users out, which is all [../design.md §6](../design.md#6-security-model) promises. A key for harness-side encryption would sit on the same machine, readable by the same user and by every command the model runs, so it would add nothing against the stated threats, and [DH-ADR-NODECRYPTO-1](../libs/node-crypto-timers-perf-hooks.md#dh-adr-nodecrypto-1) rules out home-made confidentiality.
- **Tradeoff.** A stolen unencrypted disk exposes attachments and spilled output. A spilled result may contain a secret a tool printed; it is stored as the pipeline accepted it, after any redaction, and is deleted by retention, not on demand. A 30-day-old locator in a live session stops working; the notice states the date.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-STORAGE-4`, `-14`, [DH-E-STORAGE-6](#dh-e-storage-6).

<a id="dh-adr-storage-9"></a>
### DH-ADR-STORAGE-9 Once-per-home duties run under a duty lease and can always be repeated
- **Decision.** Retention is one named duty under the duty lease that area A05 specifies (a row in its `duties` table, held under the same liveness rule as a session lease). At start and every `Interval` the retention component tries to take the duty without waiting. The process that gets it runs the collection and the sweeps and releases it; the others skip the round. Other areas add their own sweep steps with `retention.Register` (the filesystem area's stale lock files, for example). Every step is safe to repeat and to interrupt.
- **Why.** It answers, for this area, which process performs a duty that must happen once per home ([../design.md §4.7](../design.md#47-storage)), and it uses the one mechanism the home already has instead of a second lock. One runner with registered steps keeps every sweep in one log line and one schedule.
- **Tradeoff.** If no process runs, nothing is collected. A sweep of a very large home runs in whichever process happened to win, bounded by a batch size and a pause between batches. A step registered by another area runs with this component's schedule, not its own.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-STORAGE-15`, [DH-E-STORAGE-2](#dh-e-storage-2).

## 8. Deliverables

<a id="dh-d-storage-1"></a>
### DH-D-STORAGE-1 Program state survives restarts in one typed store
- **Result and why it matters.** A component declares a namespace and a Go record type and gets durable, transactional records with change notices. Every later feature that needs state (schedules, drafts, experimental stores) uses it instead of inventing a file.
- **How it is implemented.** A thin layer over the one database handle: `Open` registers the namespace row and loads a snapshot; `Update` runs the callback in one write transaction, increases the revision, then swaps the snapshot and raises `kv.Changed`. A record's stored version selects a registered converter on read; rows are rewritten only when next written. `Fresh` and the refresh timer implement [DH-ADR-STORAGE-2](#dh-adr-storage-2). Reads return copies (ARCH-11).
- **Components and files.** `internal/storage/kv`; component `storage.kv`.
- **Depends on.** [DH-D-STORE-2](session-store.md#dh-d-store-2): `sqlitedb.Tx` and `sqlitedb.Read`, which pass through the store's writer and busy policy, and its ordered layout migrations, to which this area adds its tables. Library `DH-LIB-SQLITE`. Runtime prerequisite: a home on a local filesystem.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-STORAGE-1`, `-2`, `-3` pass. They run against real database files, and `DH-T-STORAGE-2` against two store instances on one file, which is the cross-process property; the real two-process scenario [DH-E-STORAGE-1](#dh-e-storage-1) belongs to [DH-D-STORAGE-7](#dh-d-storage-7), whose workspace operations it drives.

<a id="dh-d-storage-2"></a>
### DH-D-STORAGE-2 The storage directories exist, are private, and are checked at start
- **Result and why it matters.** Every store finds its directory under the home, created owner-only, and a home that other users could read or replace is refused or reported before any data is written.
- **How it is implemented.** `home.Layout` derives every path from the home that boot resolved once. `home.Ensure` creates directories with explicit modes, then checks owner and mode on POSIX and the access-control list on Windows, as [DH-ADR-STORAGE-8](#dh-adr-storage-8) states. It also creates the collection lock file. The paths of other areas' directories come from the same layout, so no package builds a home path by hand.
- **Components and files.** `internal/storage/home`.
- **Depends on.** [DH-D-FS-1](filesystem.md#dh-d-fs-1): `fsx` (create-exclusive, file locks, Windows access-control lists; `DH-LIB-NODEFS`, [DH-ADR-NODEFS-1](../libs/node-fs-path-os-zlib.md#dh-adr-nodefs-1)). [DH-D-BOOT-2](boot.md#dh-d-boot-2): the resolved home path.
- **Priority.** P0. **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-STORAGE-4` passes and [DH-E-STORAGE-6](#dh-e-storage-6) has passed on Linux, macOS and Windows.

<a id="dh-d-storage-3"></a>
### DH-D-STORAGE-3 Attached images and files are kept durably and read back verified
- **Result and why it matters.** A person attaches an image or a file; it is still there after a restart, in a fork and in an export, and a changed or missing object is detected, never served.
- **How it is implemented.** The `blob` component streams bytes into a staging file while hashing, syncs, publishes by exclusive hard link, syncs the directory and sets the object read-only; an existing name with the same digest is success and gets its time updated. It is a service of its own, so areas with other bytes to keep (area A08's change hunks and presented copies) use the same store, limits and collector. The attachment store adds admission on top. Generic files keep upstream's layout of one object plus read-only named links, with the new size limits enforced while streaming. A read streams and verifies the digest and length; images are also checked against the reference's format and dimensions. `Touch` re-validates stored references before an event cites them. `LookupPath` tells a file tool that a path is already a stored object, so it is not admitted twice. A draft record in namespace `draft` lists its staged uploads, so an unsent draft and its files survive a reload. Objects can be exported and imported by digest, so a session export may carry them (area A06).
- **Components and files.** `internal/storage/blob`, `internal/storage/attachments`, `internal/storage/attachments/local`.
- **Depends on.** [DH-D-STORAGE-1](#dh-d-storage-1), [DH-D-STORAGE-2](#dh-d-storage-2). [DH-D-STORE-2](session-store.md#dh-d-store-2): `blob_refs` rows written, copied on fork and deleted in the transaction of the event's append, fork or delete, from the `blob.Referrer` interface. [DH-D-FS-1](filesystem.md#dh-d-fs-1): `fsx`. **Used by.** The filesystem seam, which maps a host path into the execution world, and by `read_image` ([DH-D-FS-4](filesystem.md#dh-d-fs-4), [DH-D-FS-7](filesystem.md#dh-d-fs-7)), and by the upload routes and the composer ([DH-D-UICORE-8](ui-core.md#dh-d-uicore-8)).
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-10](../design.md#dh-o-10). **Done when.** `DH-T-STORAGE-5`, `-6`, `-7` pass and [DH-E-STORAGE-2](#dh-e-storage-2) has passed.

<a id="dh-d-storage-4"></a>
### DH-D-STORAGE-4 An untrusted image is checked within fixed limits and stored in one safe form
- **Result and why it matters.** A hostile or broken image costs bounded time and memory and is refused with a stable code; an accepted one is upright, free of metadata and small enough for any model, and each model route gets a cached copy of the right size.
- **How it is implemented.** The pipeline of [libs/sharp.md](../libs/sharp.md) (`DH-LIB-SHARP`, requirements R1 to R10): byte limit, format sniffing, header-only size check, a weighted limiter on decoded pixels, a full decode inside `recover`, normalization, re-encoding with the JPEG quality ladder or PNG, and a decode of the output before it is published. This area adds three things. The limiter is one budget per process (`DecodeBudgetPixels`); an image larger than the budget runs alone. An animated source keeps its original bytes as a linked file object beside the first-frame image. `Crop` cuts a region out of a stored image and admits the result as a new image, for the `read_image` tool of area A08. The request cache is keyed by a variant id over the attachment id, transform version, target and encoder settings, written atomically, shared in-process by single flight, and bounded by the sweep.
- **Components and files.** `internal/storage/attachments/image`.
- **Depends on.** [DH-D-STORAGE-3](#dh-d-storage-3); [DH-ADR-SHARP-1](../libs/sharp.md#dh-adr-sharp-1). [DH-D-LLM-4](llm.md#dh-d-llm-4): each route's target size and byte target, from the catalogue.
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7). **Done when.** `DH-T-STORAGE-8` passes (it runs the image library's checks `DH-T-SHARP-1` to `-6` against this pipeline) and [DH-E-STORAGE-2](#dh-e-storage-2) has passed.

<a id="dh-d-storage-5"></a>
### DH-D-STORAGE-5 The full text of a large result is saved privately and can be read, listed and deleted
- **Result and why it matters.** Nothing a tool produced is lost when the model is shown only a preview, and the saved text has an owner, a lifetime and a way to be removed.
- **How it is implemented.** `Reserve` returns an id of fixed length (session directory hash plus 96 random bits plus a sanitised name) without touching the disk, so the caller can size its notice first. `Create` makes the session directory `0700` and the file exclusively `0600` and returns a writer with a byte limit, so a caller can save a text it already has or a stream of unknown length (shell output, area A09); `Close` syncs and makes the reference valid, `Abort` removes the file. `Open` resolves the id inside the spill root through a directory-rooted handle and checks session lineage. `Delete` removes a session's files that no other session cites.
- **Components and files.** `internal/storage/spill`, `internal/storage/spill/local`.
- **Depends on.** [DH-D-STORAGE-2](#dh-d-storage-2). [DH-D-STORE-2](session-store.md#dh-d-store-2) for a session's fork lineage (`Store.Stat`), and [DH-D-STORE-6](session-store.md#dh-d-store-6) for the `store.Deleting` event, on which this area deletes the session's spill files.
- **Priority.** P0. **Outcome.** [DH-O-6](../design.md#dh-o-6), [DH-O-10](../design.md#dh-o-10). **Done when.** `DH-T-STORAGE-9` passes and [DH-E-STORAGE-3](#dh-e-storage-3) has passed.

<a id="dh-d-storage-6"></a>
### DH-D-STORAGE-6 No tool result larger than the budget reaches a model
- **Result and why it matters.** One verbose command cannot fill the context window, whether the call came from the model, a direct caller or a script on the tool command line, and the model can still get at the rest.
- **How it is implemented.** A component registers on the around event of the pipeline's shape-and-bound stage (area A03 names it `tools.Shape`), outermost, so that it sees the content every other listener accepted. It prices text with the shared token estimate and images with the route's calculator or the fallback. Over budget, it reserves an id, builds the worst-case notice, splits the remaining budget between head and tail (text may be cut at a rune boundary; an image is kept or omitted whole), saves the full text with an image line at each image position, and returns the preview, the notice and the structured `spill` field. Config validation rejects a budget below the size of the largest possible notice. A failed save follows [DH-ADR-STORAGE-4](#dh-adr-storage-4). The `spill_read` tool is a separate component.
- **Components and files.** `internal/storage/spill/policy`, `internal/storage/spill/tool`.
- **Depends on.** [DH-D-STORAGE-5](#dh-d-storage-5). [DH-D-TOOLS-2](tools.md#dh-d-tools-2): the `SetSpill` seat (the shaping listener that runs after all others), the `BoundsOwnOutput` property of a tool, the spill reference in the result, and `maxResultBytes`, which a load-time check keeps above this budget. [DH-D-SESSION-1](core-session.md#dh-d-session-1): the `spill` field on the tool-result event. [DH-D-LLM-6](llm.md#dh-d-llm-6): the token estimate and image pricing.
- **Priority.** P0. **Outcome.** [DH-O-6](../design.md#dh-o-6), [DH-O-4](../design.md#dh-o-4). **Done when.** `DH-T-STORAGE-10`, `DH-T-STORAGE-11`, `DH-T-STORAGE-12` and `DH-T-STORAGE-13` pass and [DH-E-STORAGE-3](#dh-e-storage-3) and [DH-E-STORAGE-4](#dh-e-storage-4) have passed.

<a id="dh-d-storage-7"></a>
### DH-D-STORAGE-7 Projects group sessions, and a hidden session never keeps working
- **Result and why it matters.** The sidebar shows the user's project directories in a stable order with their sessions, pinned sessions on top and archived ones hidden; archiving a busy session either refuses and says what runs, or stops it first.
- **How it is implemented.** `Add` canonicalises the path, records the file identity, and in one transaction inserts the workspace and, if asked, adopts ungrouped sessions whose recorded directory resolves to it. `Attach` validates once and inserts the membership row. `Remove` deletes the workspace rows only, or on request cascades to the session store's delete and refuses if any session is active. `EnsureDefault` creates the first-use workspace only when no workspace and no session has ever existed, checked inside the transaction, and remembers that it did. Archive follows [DH-ADR-STORAGE-7](#dh-adr-storage-7). The component registers a deny-only guard on the pre-step interception that refuses a step when the session or an ancestor is pending or archived, after calling `Fresh`. Directory status is a `stat` at render time with a two-second cache.
- **Components and files.** `internal/storage/workspace`.
- **Depends on.** [DH-D-STORAGE-1](#dh-d-storage-1). [DH-D-STORE-2](session-store.md#dh-d-store-2) and [DH-D-STORE-3](session-store.md#dh-d-store-3): the sessions table key to reference, header fields (working directory, creator), the lease holder in `Store.Stat`, and session delete. [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4): the step gate is an `agent.PreStep` listener that returns `Reject` for a pending or archived session. **Used by.** Activity providers registered by the driver, the subagent manager, jobs and schedules ([DH-D-AGENT-6](agent-loop.md#dh-d-agent-6), [DH-D-SUBAGENT-1](subagent-team.md#dh-d-subagent-1), [DH-D-JOBS-1](jobs-schedule-goal-plan.md#dh-d-jobs-1)). Routes and the sidebar are [DH-D-API-6](api.md#dh-d-api-6) and [DH-D-UISIDE-2](ui-sidebar-workspace.md#dh-d-uiside-2).
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Done when.** `DH-T-STORAGE-16`, `-17`, `-18` pass and [DH-E-STORAGE-1](#dh-e-storage-1) has passed.

<a id="dh-d-storage-8"></a>
### DH-D-STORAGE-8 A person edits live settings in a form, and a bad edit changes nothing
- **Result and why it matters.** The settings page lists what can be changed while the system runs, shows where each value comes from, applies an edit without a restart, and refuses a stale, invalid or shadowed edit with a message that names the field.
- **How it is implemented.** `Describe` walks the composed tree and, for each entry whose config type has live fields, derives the form schema from the type (`schema` package), the effective value, the value without the writable layer, the override, the supplying layer and catalogue key of each field (ownership index) and the entry's revision. `Describe` reads `config.Service.Tree`. `Apply` first calls `config.Service.Preview` with the operations, so that a field error names the field, then `config.Service.Apply` with the same operations and revision, and maps its result: `applied`, `overridden(layer)` (a higher layer supplies the field), `failed(issues)`, `unsaved(error)`, or a stale revision. `reset` is boot's `Reset`; `clear` is a `Merge` of null. `settings.FormChanged` follows the loader's settled-reload announcement.
- **Components and files.** `internal/storage/settings`, `internal/storage/settings/layer`.
- **Depends on.** [DH-D-BOOT-7](boot.md#dh-d-boot-7) (`config.Service`: preview, apply under the cross-process lock, entries addressed at any depth) and [DH-D-BOOT-6](boot.md#dh-d-boot-6) (the tree with its supplying layers); [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) for schema derivation and the live tag. **Used by.** Routes and the page, [DH-D-API-7](api.md#dh-d-api-7) and [DH-D-UISET-1](ui-settings-plugins.md#dh-d-uiset-1).
- **Priority.** P1. **Outcome.** [DH-O-2](../design.md#dh-o-2). **Done when.** `DH-T-STORAGE-19`, `-20`, `-21` pass and [DH-E-STORAGE-5](#dh-e-storage-5) has passed. That scenario writes the user's configuration layer, so it runs in this deliverable's build stage.

<a id="dh-d-storage-9"></a>
### DH-D-STORAGE-9 Disk use is bounded: unreferenced and expired data is removed
- **Result and why it matters.** A home does not grow without limit, and nothing a live session still needs is deleted.
- **How it is implemented.** Under the duty lease ([DH-ADR-STORAGE-9](#dh-adr-storage-9)), in order: walk blob objects and, per batch under the exclusive `gc.lock`, delete those older than the grace period that no `blob_refs` row and no draft names; delete spill files past `MaxAge` or unreferenced past the grace period, and empty session directories; trim the request cache to its size limit, oldest first; remove old staging files; clear stale pending archive marks; then run the steps other areas registered. The walk never follows a symbolic link and skips anything it did not create. Each run logs counts and bytes freed.
- **Components and files.** `internal/storage/retention`.
- **Depends on.** [DH-D-STORAGE-3](#dh-d-storage-3), [DH-D-STORAGE-5](#dh-d-storage-5). [DH-D-STORE-8](session-store.md#dh-d-store-8): the duty lease. Reference rows are removed when a session is deleted ([DH-D-STORE-6](session-store.md#dh-d-store-6)). Timer from `src/go/timer`.
- **Priority.** P1. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-STORAGE-14`, `-15` pass and [DH-E-STORAGE-2](#dh-e-storage-2) has passed. That scenario deletes stored objects, so it runs in this deliverable's build stage.

<a id="dh-d-storage-10"></a>
### DH-D-STORAGE-10 A lost or damaged image is set aside instead of ending the session
- **Result and why it matters.** When an image in history can no longer be read, the session continues: the model is told, in fixed words, that an image is unavailable, and the fact is in the log.
- **How it is implemented.** `Quarantine` appends `attachment/quarantined` for `not-found` and `corrupt` at once, and for `read-failed` after one retry that honours cancellation. A registered message projection replaces a quarantined image with deterministic text (display name, identifier prefix, failure class) and skips the read. `Recover` appends `attachment/recovered` only after a full verified read. Concurrent requests that hit the same reference produce one event.
- **Components and files.** `internal/storage/attachments` (sub-package `quarantine`).
- **Depends on.** [DH-D-STORAGE-3](#dh-d-storage-3). [DH-D-SESSION-6](core-session.md#dh-d-session-6): the two durable event types are declared by this area and `attachment/quarantined` carries the message rewrite. [DH-D-LLM-5](llm.md#dh-d-llm-5): the image policy reports an unreadable image as the Repairable failure `llm/attachment-unreadable`, naming the reference. [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4): this area's `AttemptFailed` listener then calls `Quarantine` and asks for one retry, so the request is rebuilt from the log with the placeholder.
- **Priority.** P2. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-4](../design.md#dh-o-4). **Done when.** `DH-T-STORAGE-22` passes.

## 9. Acceptance

### 9.1 Local tests

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-STORAGE-1` | DH-D-STORAGE-1, DH-ADR-STORAGE-1 | Order of effects: a listener of `kv.Changed` sees the value in memory and in a second connection; an injected commit failure leaves memory unchanged and raises nothing; concurrent `Update` callbacks never interleave (race detector) |
| `DH-T-STORAGE-2` | DH-D-STORAGE-1, DH-ADR-STORAGE-2 | Two store instances on one database file: a write through one is returned by the other after `Fresh` and announced as `foreign` after one refresh; a held write lock yields `storage/busy` after the bound |
| `DH-T-STORAGE-3` | DH-D-STORAGE-1, DH-ADR-STORAGE-1 | A record with an older version is converted on read and rewritten only on its next write; a newer version fails that record's read; an undecodable record fails an authoritative namespace and is dropped from a derived one |
| `DH-T-STORAGE-4` | DH-D-STORAGE-2, DH-ADR-STORAGE-8 | With a permissive process mask, created directories are `0700` and files `0600`; a store directory that is group-writable or owned by another user stops the component (POSIX build); the Windows build asserts the access-control entries of a created home |
| `DH-T-STORAGE-5` | DH-D-STORAGE-3 | Equal bytes are stored once; a flipped byte, a truncated file and a reference with a wrong length or dimension each fail the read with the right code; a file over `MaxFileBytes` is refused mid-stream and leaves no object; `LookupPath` recognises a stored object's path and nothing else |
| `DH-T-STORAGE-6` | DH-D-STORAGE-3, DH-ADR-STORAGE-3 | Fault injection at each step of publishing: no reference is returned unless the object and its directory entry are synced; a batch with one invalid image publishes nothing |
| `DH-T-STORAGE-7` | DH-D-STORAGE-3, DH-ADR-STORAGE-3 | A process killed between publish and event append, in a loop, never leaves an event citing a missing object; a draft's staged file survives a collection run and is collected after the draft is deleted |
| `DH-T-STORAGE-8` | DH-D-STORAGE-4 | The pixel budget admits one maximal image and queues a second; a cancelled waiter leaves the queue; the same source and target give the same variant id and bytes; a corrupt cache entry is regenerated; an animated GIF yields a first-frame image linked to its original bytes; `Crop` returns a new stored image of the region |
| `DH-T-STORAGE-9` | DH-D-STORAGE-5, DH-ADR-STORAGE-5 | A suggested name with separators, `..`, a NUL or a device name stays one segment; a planted symbolic link at the target fails the create; a writer that is aborted or passes its limit leaves no file; `Open` succeeds for the owner and a fork and returns `spill/not-owner` for an unrelated session; an id that escapes the root is refused (fuzzed) |
| `DH-T-STORAGE-10` | DH-D-STORAGE-6, DH-ADR-STORAGE-4 | Through the real pipeline with a fake tool: a result under budget is unchanged; an oversized one fits the budget, keeps order, and its notice names the saved file whose content equals the full text; the logged event has the `spill` field |
| `DH-T-STORAGE-11` | DH-D-STORAGE-6, DH-ADR-STORAGE-4 | With a failing store, a missing image calculator and an unmappable image path, the result is still within budget and the notice states what was not saved; no file is orphaned |
| `DH-T-STORAGE-12` | DH-D-STORAGE-6, DH-ADR-STORAGE-4 | A tool that prints a look-alike notice gets no `spill` field; a tool marked as bounding its own output is skipped; a budget below the minimum is a config load error; a property test shows no accepted budget yields an over-budget result |
| `DH-T-STORAGE-13` | DH-D-STORAGE-6, DH-ADR-STORAGE-5 | `spill_read` returns a bounded slice by offset, limit and pattern, is never spilled itself, and is mounted only when the filesystem provider cannot map the spill directory |
| `DH-T-STORAGE-14` | DH-D-STORAGE-9, DH-ADR-STORAGE-3, DH-ADR-STORAGE-8 | With virtual time: a referenced object is never deleted; an unreferenced one is deleted only after the grace period; an object cited by a fork survives deletion of the parent; spill files expire at `MaxAge`; the cache is trimmed oldest first; a symbolic link in a store directory is neither followed nor deleted |
| `DH-T-STORAGE-15` | DH-D-STORAGE-9, DH-ADR-STORAGE-3, DH-ADR-STORAGE-9 | Two retention components on one home: only one runs a round, including a step another package registered; a saver that re-attaches an old unreferenced object while the collector runs ends with the object present (race detector, repeated) |
| `DH-T-STORAGE-16` | DH-D-STORAGE-7, DH-ADR-STORAGE-7 | A path and a symbolic link to it are one workspace; a session attaches only when its directory resolves to the workspace; a second attach elsewhere is refused; deleting a session removes its membership and flags; adopt-on-add takes only ungrouped sessions |
| `DH-T-STORAGE-17` | DH-D-STORAGE-7, DH-ADR-STORAGE-7 | Archive with a provider that reports activity writes nothing lasting and returns `workspace/session-active` with the list; a turn that starts after the pending mark is blocked at its first step; a failing `Report` refuses; "stop" confirms, calls every provider and keeps the archive when one fails; archiving clears the pin |
| `DH-T-STORAGE-18` | DH-D-STORAGE-7 | `EnsureDefault` creates once, never after a session existed and never again after its workspace was removed; a stale pending mark is cleared at start; removal leaves the directory untouched |
| `DH-T-STORAGE-19` | DH-D-STORAGE-8, DH-ADR-STORAGE-6 | Through the real loader and boot's `config.Service` with layer files: an edit is exactly one `config.Service.Apply` call and this package opens no layer file for writing (source check); the profile layer gains one `merge` operation holding only the edited field, other layer files are byte-identical, the component's update hook receives the value without a restart, and a later change to another field of the same entry in a lower layer still arrives |
| `DH-T-STORAGE-20` | DH-D-STORAGE-8, DH-ADR-STORAGE-6 | A stale revision, a value that fails validation, a field that is not live and an edit a higher layer shadows are each refused with their code, the field path and no file change; `reset` restores the inherited value and `clear` removes a lower layer's value |
| `DH-T-STORAGE-21` | DH-D-STORAGE-8, DH-ADR-STORAGE-6 | A secret field appears as a presence marker in every form output (golden file); two form services on the same layer files compute the same revision; with the layer file read-only the form reports `settings/unsaved` and saves once it is writable |
| `DH-T-STORAGE-22` | DH-D-STORAGE-10 | A missing object yields one quarantine event under concurrent requests and a stable placeholder; a rebuilt request from the log is identical; cancellation quarantines nothing; recovery requires a verified read |

### 9.2 Real-environment scenarios

<a id="dh-e-storage-1"></a>
#### DH-E-STORAGE-1 Two real processes share machine state on one home
- **Proves.** DH-D-STORAGE-7 (with DH-ADR-STORAGE-2 and DH-ADR-STORAGE-7).
- **Given.** A fresh temporary home on a local disk; the built `dsh` binary; process A started as a web host, which needs the workspace routes of [DH-D-API-6](api.md#dh-d-api-6), and process B as a command-line run on the same home; a real model API key supplied at run time; a session in B running a turn with a long shell command; the controller (`go test` under the scenario build tag) drives A over HTTP on loopback and B through its standard input.
- **When.** (1) In A, add a workspace through the HTTP surface. (2) In B, list workspaces. (3) In A, archive B's session without "stop". (4) In A, archive it with "stop". (5) In A, unarchive it; in B, send a new prompt.
- **Then.** B lists the workspace within two seconds. Step 3 is refused and names the other process's activity; nothing is hidden. After step 4 B's turn ends as cancelled by the user, no model request is logged for the session after the archive time plus one refresh interval, and A hides the session. After step 5 the session accepts the prompt and its log is contiguous.
- **Cleanup.** Stop both processes and delete the temporary home; on failure keep the home and both logs.

<a id="dh-e-storage-2"></a>
#### DH-E-STORAGE-2 An attached image survives a crash, a restart and collection, against a real model
- **Proves.** DH-D-STORAGE-3, DH-D-STORAGE-4, DH-D-STORAGE-9, DH-ADR-STORAGE-3, DH-ADR-STORAGE-9.
- **Given.** A fresh home; a vision-capable real model API; a 20-megapixel JPEG with EXIF orientation and location data; `UnreferencedGrace` set to its minimum and a test clock offset.
- **When.** (1) Attach the image and ask what it shows. (2) Kill the host during a second upload, after its object is published. (3) Restart and ask a follow-up about the first image. (4) Fork the session, delete the parent, advance past the grace period and wait for a maintenance round. (5) Ask the fork about the image.
- **Then.** The model's answer matches the picture's upright content. The stored object has no EXIF data. After step 2 no event cites the second object, and after step 4 its file is gone. The first image's object is still present and step 5 succeeds. The request sent in step 3 has the same image bytes as in step 1.
- **Cleanup.** Delete the fork and the home; on failure keep the `blobs` directory listing and the database.

<a id="dh-e-storage-3"></a>
#### DH-E-STORAGE-3 A five-megabyte command output is bounded and recoverable under a real sandbox
- **Proves.** DH-D-STORAGE-5, DH-D-STORAGE-6, DH-ADR-STORAGE-4.
- **Given.** One run each on Linux, macOS and Windows with the real sandbox backend in `workspace-write` mode; a real model API; a workspace file that prints 5 MB of numbered lines with a marker on line 40,000.
- **When.** (1) Ask the model to run the command and report the marker line. (2) Make the spill directory read-only and repeat in a new session.
- **Then.** In step 1 the logged tool result is within the budget, carries the `spill` field, and the model finds the marker by reading or searching the saved file with its file tools; the saved file is `0600` on POSIX and equals the full output. In step 2 the result is still within the budget, the notice says the full output was not saved, the tool call is a success and the turn completes.
- **Cleanup.** Restore the directory mode and delete the sessions; on failure keep the spill directory and the session export.

<a id="dh-e-storage-4"></a>
#### DH-E-STORAGE-4 A spilled result is retrievable when the execution world is a remote host
- **Proves.** DH-D-STORAGE-6, DH-ADR-STORAGE-5.
- **Given.** A real SSH host configured as the session's execution world (area A10); a real model API; a remote command that prints 2 MB.
- **When.** Ask the model to run the command and quote its last line, then its 10,000th line.
- **Then.** The notice names `spill_read`, not a local path. The model retrieves the 10,000th line through `spill_read`. A second session that calls `spill_read` with the same id gets `spill/not-owner`.
- **Cleanup.** Delete both sessions; remove nothing on the SSH host that the harness did not create.

<a id="dh-e-storage-5"></a>
#### DH-E-STORAGE-5 A settings edit in a real browser changes a running host, and a bad edit changes nothing
- **Proves.** DH-D-STORAGE-8, DH-ADR-STORAGE-6.
- **Given.** A host with the web UI and a second `dsh` process on the same home; a real browser; a launch started with a command-line overlay that sets one live field.
- **When.** (1) Change `MaxInlineTokens` in the settings page. (2) Submit an out-of-range value. (3) Edit the field the overlay sets. (4) Open the page in two tabs, save in one, then save in the other. (5) Put a syntax error into the layer file by hand.
- **Then.** After step 1 the next oversized result in both processes uses the new budget with no component restart recorded, and the layer file holds one `merge` operation. Steps 2 and 3 show a message naming the field (and, in step 3, the overlay) and the file is unchanged. The second save in step 4 is refused as stale. Step 5 is reported with file, line and column and the running values are unchanged.
- **Cleanup.** Restore the layer file from the copy taken before the scenario.

<a id="dh-e-storage-6"></a>
#### DH-E-STORAGE-6 Another local user cannot read stored data
- **Proves.** DH-D-STORAGE-2, DH-ADR-STORAGE-8.
- **Given.** Linux, macOS and Windows machines, each with two ordinary user accounts; user 1 has a home with an attachment and a spill file; the process mask is permissive.
- **When.** As user 2, list and read user 1's `blobs`, `spill` and `cache` directories. Then, as user 1, make the `spill` directory group-writable (POSIX) and restart the host.
- **Then.** Every access by user 2 is denied. After the mode change the spill component refuses to start with a message naming the directory, and the rest of the host runs.
- **Cleanup.** Restore the mode; delete the test home and, if created for the scenario, the second account.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

**Requested of other areas** (this document designs against what they state today).

1. **Host-path mapping (filesystem.md, sandbox-ssh.md).** `fsx` file locks and Windows access-control lists are in [DH-D-FS-1](filesystem.md#dh-d-fs-1). Still not stated there: how a filesystem provider maps a host path of this store (`HostPath`) into its execution world. Until it is, a spill or attachment is read by reference through this area's API, which works in every world, and a path is handed out only for the local world. The other areas' needs are in the contracts above: the blob store, look-up by path and crop for A08, a stream writer for A09, typed tables for A10.

**Open facts.** (2) Whether a file marked read-only on Windows can have its modification time updated was not verified; the design leaves objects writable by their owner on Windows. (3) PNG for transparent images may exceed the 4 MiB target ([libs/sharp.md](../libs/sharp.md) §7); an animated GIF is stored as its first frame plus the original bytes as a linked file. (4) The cost of `Fresh` on the step gate and of the collector's directory walk on a large home is unmeasured. (5) New defaults (file size limit, grace period, token budget, cache size) are proposals without measurements.

**Limitations.**

- Reading a spill or attachment path with a file tool is not bound to a session; only the store's own API checks lineage.
- Audio and video are stored as generic files; no model route receives them as media.
- Stored data is not encrypted by the harness ([DH-ADR-STORAGE-8](#dh-adr-storage-8)).
- The decoded-pixel budget is per process. A change made by another process is seen after up to one refresh interval, except by gating reads.
- No permission or approval rule is stored as machine state: [DH-ADR-16](../design.md#dh-adr-16) forbids rules that outlive a session, so upstream's computer-use permission store has no table here. What other areas keep in `kv` is theirs to name: browser sessions are stored as hashes by A18, and the UI language is C1's preference in `ui-prefs`, not a setting of this area.

**Settled with other areas, for the record.** A05 supplies `sqlitedb.Tx` and `sqlitedb.Read`, the sessions key for foreign keys, fork lineage and the lease holder in `Store.Stat`, `blob_refs` maintained in its own transactions, the `store.Deleting` event and the duty lease; a `ref` is `<kind>:<id>`; a session export does not bundle blobs, the archive of [DH-D-VIEW-8](session-views.md#dh-d-view-8) carries attachments. A03 supplies the `SetSpill` seat, `BoundsOwnOutput` and the ceiling check. Schedules (A13) keep their own tables and use `kv.Counter` for their revision. Every configuration write goes through boot's `config.Service.Apply`, which also answers which layer is writable (the profile layer) and how a nested entry is addressed.

## 11. Units dispositioned by this spec

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 11 | 7 ported, 1 replaced, 1 merged, 2 dropped |
| `UP-DEBT` | 39 (37 assigned, 2 found) | 38 resolved, 1 not-applicable |
| `UP-EVT` | 6 | 4 ported, 2 replaced |
| `UP-CFG` | 6 | 3 ported, 3 dropped |
| `UP-SVC` | 6 | 4 ported, 1 replaced, 1 dropped |
| `UP-DEC` | 6 | 4 ported, 2 replaced |

Go items declared: 10 deliverables, 9 decisions, 22 local tests, 6 scenarios. The rows are in `trace/fragments/A07.jsonl`.
