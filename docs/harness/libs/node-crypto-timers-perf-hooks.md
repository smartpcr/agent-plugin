# Node built-ins `node:crypto`, `node:timers` and `node:perf_hooks`: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Counts come from text searches of the upstream checkout on
> 2026-11-18 and are approximate where the text says "about". Go facts were checked the
> same day with `go doc` and the `api/` files of the installed Go 1.27.4 toolchain.

| | `node:crypto` | `node:timers` | `node:perf_hooks` |
|---|---|---|---|
| **Library ID** | `DH-LIB-NODECRYPTO` | `DH-LIB-NODETIMERS` | `DH-LIB-NODEPERFHOOKS` |
| **Upstream unit** | `UP-DEP-node:crypto` | `UP-DEP-node:timers` | `UP-DEP-node:perf_hooks` |
| **Upstream version** | the Node runtime's | the Node runtime's | the Node runtime's |
| **Kind** | Node built-in | Node built-in | Node built-in |
| **Used by** | 205 files in 44 groups (inventory); 204 outside tests by search | 29 files outside tests, 78 with tests | 19 files outside tests, 29 with tests |
| **Go choice** | standard library (`crypto/rand`, `crypto/sha256`, `crypto/hmac`, `crypto/subtle`, a few others at named boundaries) | standard library (`time`, `context`) and the cordis timer service in `src/go/timer` | standard library (`time`, `testing`) |
| **Related areas** | host and authentication, storage and attachments, credentials, sandbox and SSH, MCP, release tooling ([../upstream/toolchain.md](../upstream/toolchain.md)) | every area that waits; schedules; test support | session persistence, benchmarks |

Related specifications: time zones in [js-temporal-polyfill.md](js-temporal-polyfill.md),
recurrence in [rrule.md](rrule.md), the browser hash library in
[noble-hashes.md](noble-hashes.md), the secrets and authentication decisions in
[../upstream/security.md](../upstream/security.md), the timer component in
[cordis-plugins.md](cordis-plugins.md).

## 1. What they are and why upstream needs them

`node:crypto` is Node's cryptography module. Upstream uses a small part of it for four
jobs: making **random identifiers and secrets**, computing a **hash** (a fixed-size
fingerprint of some bytes) to name or check content, **signing** the browser cookie with
a keyed hash (an HMAC, a hash only the holder of a secret key can produce), and
**comparing secrets in constant time** (so that the time taken does not reveal how many
leading characters were right). It does not use it to encrypt, to derive keys from
passwords or to make public-key signatures.

`node:timers` supplies waiting. Every import upstream makes is of the promise form
(`node:timers/promises`): "sleep this long, unless cancelled" and "let other work run
before I continue". Ordinary timers that run a callback later are globals in JavaScript
and need no import; they are counted here too, because they are the larger part of the
same requirement.

`node:perf_hooks` supplies a clock for measuring elapsed time that never jumps (a
**monotonic** clock), and tools that measure how long the single JavaScript thread was
blocked.

## 2. How upstream uses them

### 2.1 `node:crypto`

Imported names in the 204 files outside tests: `createHash` 108 files, `randomUUID` 93,
`randomBytes` 22, `timingSafeEqual` 5, `X509Certificate` 5, `createHmac` 1,
`createPublicKey` 1, `getHashes` 1. Hash algorithms by call site: SHA-256 114, SHA-512
20, SHA-1 10, MD5 4, chosen at run time 1. No call to a cipher, a key-derivation
function, `createSign` or `createVerify` exists outside tests.

| Class of use | Example sites (`path:line`) | API used |
|---|---|---|
| **Random identifiers** for sessions, messages, attachments, requests | `packages/core/agent-loop/src/index.ts:342`; `packages/attachment/attachment-local/src/store.ts:3`; a second implementation for browsers in `packages/util/crypto/src/index.ts:33-41` | `randomUUID()` |
| **Random secrets and tokens** (32 bytes): launch token and cookie signing key; desktop session secret; OAuth `state` and proof-key verifier; a pre-shared key for the SSH stream; local helper bearer tokens | `packages/host/authentication-token/src/browser-auth.ts:17`, `:65`, `:186`; `apps/desktop/src/parent-authentication.ts:7`; `packages/account/deepseek-account/src/index.ts:738-739`; `packages/mcp/mcp-client/src/oauth.ts:186`; `packages/ssh/ssh/src/helper-processes.ts:423`; `packages/experimental/speech-to-text-sensevoice/src/recognizer.ts:261` | `randomBytes(32)` |
| **Random file-name suffixes** (4 to 8 bytes) for temporary and spill files | `packages/util/atomic-write/src/index.ts:88`; `packages/spill/spill-local/src/store.ts:110`; `packages/subprocess/subprocess-local/src/output.ts:206` | `randomBytes(6).toString('hex')` |
| **Content address and integrity** with SHA-256: attachment objects, session file generations, skill archives, the SSH helper binary | `packages/attachment/attachment-local/src/store.ts:25`, `:323`; `packages/session/session-persistence-jsonl/src/generation.ts:517`, `:549`; `packages/connector/upstream-skills/src/archive.ts:94`; `packages/ssh/ssh/src/helper.ts:115` | `createHash('sha256')`, streaming `update`, `digest('hex')` |
| **Fingerprints and chained digests** with SHA-256: search-index fingerprint, model request prefix binding | `packages/session-query/session-query-sqlite/src/index.ts:888`; `packages/llm/llm-pi-ai/src/prefix-binding.ts:35` | as above |
| **Names derived from a hash** (shorten or sanitise a name, often truncated): lock names, cache directories, tool-name suffix, Windows capability identifier | `packages/session/session-persistence-sqlite/src/win32.ts:155`; `packages/boot/plugin-market/src/repository-cache.ts:265`; `packages/mcp/mcp-client/src/tools.ts:110`; `packages/sandbox/sandbox-windows-acl/src/workspace-sid.ts:36-39` | `createHash('sha256')...slice(0, n)` |
| **Hiding a secret inside a key**: a cache scope built from the hash of authorisation headers | `packages/llm/llm-deepseek/src/listing-cache.ts:89` | `createHash('sha256')` |
| **SHA-1** where nothing is being protected: asset revisions, a weak HTTP `ETag`, instruction-file identity, a content-addressed capture directory; and where an outside format fixes it: Git blob ids, GitLab line codes, npm `shasum` | `packages/client/modules/src/assets.ts:56`; `packages/host/frontend-static/src/index.ts:111`; `packages/context/agent-instructions/src/digest.ts:15`; `packages/deliverables/workspace-changes/src/capture.ts:62`; `scripts/cua-helper/build-inputs.ts:52`; `packages/experimental/forge-gitlab/src/position.ts:51` | `createHash('sha1')` |
| **SHA-512 and MD5** in release and packaging scripts: npm integrity strings, update manifests, the upload checksum an object store demands | `apps/desktop/src/core-package-set.ts:153`; `apps/desktop-publisher/src/desktop-publish-cos.ts:38`, `:68`, `:76`; `scripts/release/publish.ts:67` | `createHash('sha512')`, `createHash('md5')` |
| **Cookie signature** | `packages/host/authentication-token/src/browser-auth.ts:143-150` | `createHmac('sha256', secret)` |
| **Constant-time comparison** of the launch token, cookie signature, desktop secret, OAuth `state`, bearer token | `packages/host/authentication-token/src/browser-auth.ts:111-115`, `:161-162`; `packages/host/authentication-desktop/src/index.ts:39`; `packages/account/deepseek-account/src/index.ts:763-764`; `packages/mcp/mcp-client/src/oauth.ts:209`; `packages/experimental/speech-to-text-sensevoice/src/process-server.ts:22` | `timingSafeEqual` after a length check |
| **OAuth proof key (PKCE)**: the challenge is the SHA-256 of the verifier | `packages/account/deepseek-account/src/index.ts:740` | `createHash('sha256').digest('base64url')` |
| **Certificates** in Windows signing scripts: thumbprint and public-key comparison | `apps/desktop/scripts/windows-signing-preflight.ts:62`; `apps/desktop/scripts/windows-token-identity.ts:40`, `:66` | `X509Certificate`, `createPublicKey` |

Webhook signatures are verified by another library, not by `node:crypto`
(`packages/experimental/webhook-github/src/handler.ts:103`); that dependency belongs to
another specification.

### 2.2 `node:timers` and global timers

| Class of use | Example sites (`path:line`) | API used |
|---|---|---|
| **Cancellable sleep**: retry back-off, polling, lock retry | `packages/account/deepseek-account/src/logout.ts:26`; `packages/llm/llm-deepseek/src/file-store.ts:301`; `packages/util/atomic-write/src/index.ts:284`; `packages/session/session-persistence-sqlite/src/migration.ts:62`; `packages/subprocess/subprocess-local/src/linux-scope.ts:102`; `vendor/include/src/index.ts:574` | `setTimeout(ms, undefined, { signal })` from `node:timers/promises` (31 importing files with tests) |
| **Yield**: let the single thread serve other work during a long loop | `packages/session/session-persistence-sqlite/src/sqlite.ts:232`, `:431`, `:745`; `packages/session/session-persistence-jsonl/src/generation.ts:404`, `:686`; `packages/api/session-controller/src/list.ts:127`; `packages/experimental/deepseek-chat-import/src/archive.ts:242` | `setImmediate()` (35 files), `scheduler.yield()` (12 files) |
| **Callback timers**, not imported: about 134 `setTimeout(` and 9 `setInterval(` call sites in package and application source outside tests and client code; about 29 `.unref()` calls so a timer does not keep the process alive | `packages/schedule/schedule/src/runtime.ts:222-224` | global `setTimeout`, `setInterval`, `clearTimeout`, `unref` |
| **Operation deadlines**: about 47 `AbortSignal.timeout(` sites and a shared deadline helper whose abort reason carries a timeout code | `apps/plugin-market-generator/src/fetchers.ts:53`; `packages/util/timeout/src/index.ts:12-22` | `AbortSignal.timeout`, `AbortSignal.any` |
| **Owned timers** through the cordis timer service | two sites outside tests ([cordis-plugins.md](cordis-plugins.md), DH-ADR-LOADER-10) | `ctx.timeout`, `ctx.interval` |
| **Long waits cut into pieces** because a Node timer cannot exceed 2,147,483,647 ms (about 24.8 days) | `packages/schedule/schedule/src/runtime.ts:29`, `:223`; `packages/util/timeout/src/index.ts:25` | a constant, duplicated |

### 2.3 `node:perf_hooks`

| Class of use | Example sites (`path:line`) | API used |
|---|---|---|
| **Time slice**: measure how long a loop has held the thread, then yield | `packages/api/session-controller/src/list.ts:117-138`; `packages/session/session-persistence-jsonl/src/generation.ts:399-406`; `packages/session/session-persistence-jsonl/src/index.ts:1093-1112` | `performance.now()` |
| **Elapsed-time measurement** in benchmarks, performance tests and build scripts (16 of the 19 files outside tests) | `benchmarks/session-open/session-open.worker.ts:4`; `scripts/run-gates.ts:11` | `performance.now()`, `PerformanceObserver` |
| **How long the event loop was blocked** | `benchmarks/session-corpus/projection-list.worker.ts:6`; `apps/web/tests/present-svg.e2e.ts:5` | `monitorEventLoopDelay` |

The three production files use the clock only to decide when to yield.

## 3. Tech debt upstream carries

No `UP-DEBT` unit is assigned for these built-ins. Search performed: every import of the
three modules, the constants and comments beside them, the decision notes that mention
timers, and `packages/util/crypto`, `packages/util/timeout`. Two findings are recorded as
new units because no package README lists them.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L10.1` | SHA-1 is used as a content identity: a capture directory names files by the SHA-1 of their bytes and shares "identical content", and instruction files are identified by SHA-1. Two different files with the same SHA-1 can be constructed, and the content can come from the model's workspace | `packages/deliverables/workspace-changes/src/capture.ts:62-64`; `packages/context/agent-instructions/src/digest.ts:15` | Resolved: SHA-256 for every content identity ([DH-ADR-NODECRYPTO-1](#dh-adr-nodecrypto-1)) |
| `UP-DEBT-found.L10.2` | A minute or hour recurrence rule is silent during the repeated hour of an autumn clock change | `packages/util/time/src/rrule.ts:561-586` | Resolved by [DH-ADR-RRULE-2](rrule.md#dh-adr-rrule-2); explained in [rrule.md](rrule.md) |
| none assigned | Two ways to sleep. Three hand-written sleeps are kept on purpose because the test framework's fake clock "does not intercept `node:timers/promises`", while other packages import the built-in | `.agents/notes/rejected/simplification/2026-07-26-builtin-timer-promises-for-hand-rolled-sleeps.md:3`, `:9` | Removed: Go's virtual time covers every `time` timer, so there is one sleep helper ([DH-ADR-NODETIMERS-1](#dh-adr-nodetimers-1)) |
| none assigned | Almost every timer is an unowned global with hand-written cancel and `unref` | counts in §2.2 | Removed: a wait belongs to a `context.Context` or to a fiber (R1 below) |
| none assigned | The Node timer limit is written as a constant in two packages | `packages/schedule/schedule/src/runtime.ts:29`; `packages/util/timeout/src/index.ts:25` | Not applicable: a Go duration spans about 292 years |
| none assigned | A second UUID implementation and a lint rule exist because browsers refuse `crypto.randomUUID` on plain HTTP | `packages/util/crypto/src/index.ts:1-9` | Not applicable: identifiers are made by the host; the browser makes none ([DH-ADR-3](../design.md#dh-adr-3)) |
| none assigned | Persistence code yields by hand so one thread stays responsive | `packages/session/session-persistence-sqlite/src/sqlite.ts:431`, `:745` | Not applicable: goroutines are scheduled pre-emptively |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NODECRYPTO-R1` | **Identifiers** are unguessable and do not collide in practice: at least 122 random bits from the operating system's secure generator, in one text form made by one helper. | `randomUUID` use, §2.1 |
| `DH-LIB-NODECRYPTO-R2` | **Secrets and tokens** (launch token, browser session id, form token, OAuth `state` and verifier, helper and plugin capabilities) are at least 32 bytes from the same generator. There is no cookie signing key ([DH-ADR-28](../design.md#dh-adr-28)). A general-purpose pseudo-random generator is never used for them or for identifiers. | `packages/host/authentication-token/src/browser-auth.ts:17`, `:65` |
| `DH-LIB-NODECRYPTO-R3` | **Content identity and integrity** use SHA-256 over the exact bytes, computed while streaming so a large file is not held in memory, written as lower-case hexadecimal. This covers attachment objects, stored-file digests, archives, helper binaries and every fingerprint. | `packages/attachment/attachment-local/src/store.ts:25`, `:323`; `packages/ssh/ssh/src/helper.ts:115` |
| `DH-LIB-NODECRYPTO-R4` | **A name derived from a hash** uses SHA-256, states its truncated length in one place, and treats "same derived name, different source" as an error to report, never as the same thing. | `packages/mcp/mcp-client/src/tools.ts:110`; `packages/sandbox/sandbox-windows-acl/src/workspace-sid.ts:36-39` |
| `DH-LIB-NODECRYPTO-R5` | **Message authentication** uses HMAC with SHA-256 where a peer signs a message: a webhook signature is checked over the raw request bytes before parsing. A browser session is not signed: the cookie holds a random id, the host keeps only its SHA-256 hash and compares hashes in constant time ([DH-ADR-28](../design.md#dh-adr-28)). | `packages/host/authentication-token/src/browser-auth.ts:143-170`; [DH-ADR-WIRE-12](../upstream/wire-and-api.md#dh-adr-wire-12) |
| `DH-LIB-NODECRYPTO-R6` | **Every comparison of a secret** (token, signature, bearer value, OAuth `state`) takes time that does not depend on where the values differ. Only the length may be revealed. | `packages/host/authentication-token/src/browser-auth.ts:111-115` |
| `DH-LIB-NODECRYPTO-R7` | **An algorithm fixed by an outside party** is used exactly as fixed and only in the package that talks to that party: SHA-256 with unpadded base64url for the OAuth proof key, SHA-1 for Git object ids, GitLab line codes and Windows certificate thumbprints, MD5 for an object store's upload checksum, SHA-512 for update manifests. | §2.1 rows on SHA-1, SHA-512, MD5 and PKCE |
| `DH-LIB-NODECRYPTO-R8` | **No home-made confidentiality.** Harness code does not encrypt data, derive keys or make public-key signatures. Stored secrets are protected by the operating-system keychain or an owner-only file ([DH-ADR-SECURITY-6](../upstream/security.md#dh-adr-security-6)). Adding any of these needs a recorded decision. | no such use exists upstream (§2.1) |
| `DH-LIB-NODETIMERS-R1` | **Every wait has an owner and can be cancelled.** A sleep returns early with the cause when its `context.Context` is cancelled or its fiber unloads. No timer callback runs after its owner has unloaded. | `packages/account/deepseek-account/src/logout.ts:26`; [cordis-plugins.md](cordis-plugins.md) `DH-LIB-CORDISPLUGINS-R6` |
| `DH-LIB-NODETIMERS-R2` | **A deadline says why it fired.** An operation that times out is cancelled with a cause that names the timeout, so a tool result can say "timed out after N seconds" and not "cancelled". | `packages/util/timeout/src/index.ts:12-22` |
| `DH-LIB-NODETIMERS-R3` | **Scheduled work is testable without real waiting.** Any behaviour that depends on time passing (retry back-off, idle timeouts, debounce, schedule delivery) can be driven by a test in virtual time, deterministically, in milliseconds of real time. | [DH-ADR-TOOLCHAIN-2](../upstream/toolchain.md#dh-adr-toolchain-2) |
| `DH-LIB-NODETIMERS-R4` | **Long loops stay cancellable.** Where upstream yields the thread, the Go loop checks its context at the same points, so cancellation is noticed within one slice. | `packages/session/session-persistence-jsonl/src/generation.ts:399-406` |
| `DH-LIB-NODEPERFHOOKS-R1` | **Elapsed time is measured on a clock that never jumps.** Durations reported in logs, telemetry and tool results, and budgets asserted by performance tests, do not change when the system clock is corrected. | `packages/api/session-controller/src/list.ts:117`; `benchmarks/session-open/session-open.worker.ts:4` |

## 5. Go choice

**Decision.** The standard library for all three. One small harness helper each for
identifiers and for cancellable sleep. No module.

### 5.1 `node:crypto`

| Upstream call | Go | Note |
|---|---|---|
| `randomUUID()` | A harness helper that formats 16 bytes from `crypto/rand` as a version 4 UUID; replaced by the standard `uuid` package when the module's minimum Go version reaches 1.27 | `uuid` is new in Go 1.27 (`api/go1.27.txt:305-314`); the harness floor is Go 1.25 ([design.md](../design.md) CF-4) |
| `randomBytes(n)` | `crypto/rand.Read`; `crypto/rand.Text` for a ready-made token string | `Read` "never returns an error"; `Text` has "at least 128 bits" and exists since Go 1.24 (`go doc crypto/rand`, `api/go1.24.txt:47`) |
| temporary-file suffix | `os.CreateTemp` | the standard library picks the random part |
| `createHash('sha256')` | `crypto/sha256`: `Sum256` for bytes in hand, `New` with `io.Copy` for a stream | |
| `createHmac('sha256', key)` | `crypto/hmac` with `sha256.New` | |
| `timingSafeEqual` | `hmac.Equal` for signatures, `crypto/subtle.ConstantTimeCompare` otherwise | both return at once on a length mismatch, as upstream does (`go doc crypto/subtle.ConstantTimeCompare`) |
| `digest('hex')`, `digest('base64url')` | `encoding/hex`, `base64.RawURLEncoding` | |
| `createHash('sha1')`, `('sha512')`, `('md5')` | `crypto/sha1`, `crypto/sha512`, `crypto/md5`, only under R7 | |
| `X509Certificate`, `createPublicKey` | `crypto/x509`, `encoding/pem` | release tooling only |
| `getHashes()` | none | a fixed list of accepted algorithms replaces the question |

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** standard library, own identifier helper | No dependency; one audited implementation; the helper is about ten lines and disappears at Go 1.27 | The helper must set the version and variant bits correctly | `crypto/rand`, `crypto/hmac`, `crypto/subtle`, `uuid` read with `go doc` in Go 1.27.4 |
| `github.com/google/uuid` for identifiers | Well known; version 7 available today | A dependency for ten lines, made redundant by the standard package | v1.6.0 (2024-01-23), BSD-3-Clause, no requirements in `go.mod`, no `import "C"` |
| Raise the Go floor to 1.27 now and use `uuid` | No helper | Raises the floor for one small function, against CF-4's rule that a floor change needs a dependency that requires it | `api/go1.27.txt:305-314` |

<a id="dh-adr-nodecrypto-1"></a>
#### DH-ADR-NODECRYPTO-1 One algorithm policy, enforced by an import check

- **Decision.** SHA-256 is the only hash for content identity, integrity, fingerprints
  and derived names. HMAC-SHA-256 is the only message authentication. `crypto/rand` is
  the only source of identifiers, tokens and keys; `math/rand/v2` is allowed for retry
  jitter and sampling only. `crypto/sha1`, `crypto/md5` and `crypto/sha512` may be
  imported only by the packages that implement an R7 boundary. Ciphers, key derivation
  and signature packages are not imported at all (R8). A repository test reads the import
  graph and fails on any import outside this table.
- **Why.** Upstream's choices are sound where security matters (cookie, tokens, proof
  key, attachments) and loose where it thought nothing was at stake, which is how SHA-1
  came to name content that the model can influence (`UP-DEBT-found.L10.1`). A rule that
  a tool checks is cheaper than reviewing 108 call sites one at a time.
- **Tradeoff.** SHA-256 is slower than SHA-1 for large inputs; none of the affected
  sites is on a hot path. Each new outside format that fixes another algorithm needs an
  entry in the check's allow-list.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-5](../design.md#dh-o-5).
  **Verified by.** `DH-T-NODECRYPTO-1`, `DH-T-NODECRYPTO-2`, `DH-T-NODECRYPTO-4`.

<a id="dh-adr-nodecrypto-2"></a>
#### DH-ADR-NODECRYPTO-2 One identifier helper; the format is not a contract

- **Decision.** One function in the shared-utilities family makes every random
  identifier. Callers treat the result as an opaque string and never parse it. Its
  present form is a version 4 UUID.
- **Why.** Upstream mints identifiers in 93 files and in two implementations. One
  helper lets the form change in one place, for example to a time-ordered version 7 UUID
  if the persistence area wants keys that sort by creation time.
- **Tradeoff.** A caller that wants a prefix or a shorter form composes it around the
  helper. **Serves.** [DH-O-5](../design.md#dh-o-5). **Verified by.**
  `DH-T-NODECRYPTO-3`.

**Where the algorithm choice is security-relevant.** These are the sites a reviewer
should read first; each keeps upstream's algorithm.

| Site | Why it matters | Go rule |
|---|---|---|
| Launch token, cookie signing key, cookie signature (`browser-auth.ts:65`, `:144`, `:162`) | Whoever forges the cookie is the owner ([DH-ADR-SECURITY-7](../upstream/security.md#dh-adr-security-7)) | R2, R6 for the launch token and the session id, which are random values compared in constant time. There is no cookie signing key and no signature: the cookie's value is a random id of which the host stores only a hash, because a stored signing secret could be read by a command running as the same user ([DH-ADR-28](../design.md#dh-adr-28)). The keyed hash of R5 is used for the per-peer form token, whose key lives in host memory only ([../areas/api.md](../areas/api.md), `DH-ADR-API-9`) |
| Desktop session secret (`packages/host/authentication-desktop/src/index.ts:37-39`) | Same authority | Not built: there is no desktop application and no desktop session ([DH-ADR-18](../design.md#dh-adr-18)). The browser's launch token and session id are the row above |
| OAuth `state` and proof key (`deepseek-account/src/index.ts:738-740`, `:763`; `mcp-client/src/oauth.ts:186`, `:209`) | Stops a forged callback and a stolen code | R2, R6, R7 |
| SSH helper digest and stream key (`ssh/src/helper.ts:115`; `ssh/src/helper-processes.ts:423-426`) | Decides which remote binary is trusted and who may attach to a stream | R2, R3. Inference: `crypto/tls` has no pre-shared-key mode for an outside key, so the SSH area needs another way to authenticate the stream (§7) |
| Attachment and capture content addresses (`attachment-local/src/store.ts:25`; `workspace-changes/src/capture.ts:62`) | Equal digest means "the same object is served" | R3; the SHA-1 site changes |
| Windows capability identifier from a workspace path (`sandbox-windows-acl/src/workspace-sid.ts:36-39`) | Two workspaces that collide share write permission; the value is also written into access-control lists | R4; the derivation is a stable format once shipped |
| Cache scope from authorisation headers (`llm-deepseek/src/listing-cache.ts:89`) | Keeps a credential out of a map key and out of logs | R3, in memory only; never persisted or logged |

### 5.2 `node:timers` and `node:perf_hooks`

| Upstream form | Go | Note |
|---|---|---|
| cancellable sleep | A helper `Sleep(ctx, d) error` that selects on `ctx.Done()` and a `time.Timer` and returns `context.Cause(ctx)`; inside a component with no request context, `timer.Service.Sleep` | `src/go/timer/timer.go` (`Sleep` returns `cordis.ErrInactive` on unload) |
| `AbortSignal.timeout`, the deadline helper | `context.WithTimeoutCause`, `context.WithDeadlineCause` with a typed cause | since Go 1.21 (`api/go1.21.txt:8-10`) |
| callback timers, `setInterval`, debounce, throttle | `timer.Service.Timeout`, `Interval`, `Debounce`, `Throttle`: effects of the calling fiber | [cordis-plugins.md](cordis-plugins.md) DH-ADR-LOADER-10 keeps the service unchanged |
| `unref()` | none | a Go process ends when `main` returns; timers never keep it alive |
| `setImmediate()`, `scheduler.yield()`, time slices | none; the loop checks `ctx.Err()` at the same points (R4) | goroutines are pre-empted; the single-thread reason is gone |
| a busy-database retry loop with a deadline (`sqlite.ts:224-232`) | the database driver's busy timeout | owned by the persistence area |
| `Date.now()` (about 165 sites) | `time.Now()` | |
| `performance.now()` differences | `start := time.Now()` then `time.Since(start)` | `time.Now` carries a monotonic reading, and `Since` uses it |
| benchmark timing, `PerformanceObserver` | `testing.B` and the performance-budget layer ([DH-ADR-TOOLCHAIN-14](../upstream/toolchain.md#dh-adr-toolchain-14)) | |
| `monitorEventLoopDelay` | none | there is no event loop; `runtime/metrics` offers `/sched/latencies:seconds` if a scheduling-delay figure is wanted |
| the 24.8-day timer limit | none | |

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `time`, `context`, the cordis timer service, and `testing/synctest` for tests | No dependency and no extra parameter anywhere; virtual time also covers `context` deadlines and code that was not written with tests in mind | A test in virtual time cannot touch the real network or a child process | `testing/synctest` is in the standard library since Go 1.25 (`api/go1.25.txt:107-108`); its documentation was read in Go 1.27.4 |
| An injected clock interface (a `clock` service) | Works together with real I/O; a test can start at any date | Every component and helper must take and use it; `context.WithTimeout` and third-party code ignore it; two ways to wait | not a module; a design alternative |

<a id="dh-adr-nodetimers-1"></a>
#### DH-ADR-NODETIMERS-1 Waits are owned, and tests use virtual time without a clock interface

- **Decision.** Product code reads the time with `time.Now()` and waits only through a
  `context` deadline, the sleep helper or the cordis timer service. No clock interface
  is injected. Tests of timed behaviour run inside `synctest.Test`, where the `time`
  package uses a fake clock that advances only when every goroutine of the test is
  blocked. For that to work the harness follows seven rules:
  1. **Time arithmetic takes time as an argument.** Recurrence, back-off schedules and
     retention cut-offs are pure functions; they are tested with tables and no clock.
  2. **No bare `time.Sleep` in product code.** A static check allows it only inside the
     sleep helper, because it cannot be cancelled.
  3. **Nothing waits at package level.** No goroutine, ticker or timer is started from
     `init` or stored in a package variable; a `sync.WaitGroup` is never a package-level
     value. Each of these lives outside the test's isolated "bubble", where the fake
     clock cannot account for it and where touching it from the test panics (`go doc
     testing/synctest`, section "Isolation").
  4. **A timed test builds its cordis tree inside the test and unloads it before
     returning.** Timers and channels made inside the test belong to it; touching them
     from outside panics. Unloading releases every timer effect, so no goroutine is left
     and the test can end.
  5. **Inside a timed test the outside world is a fake in the same process.** The model,
     the network, child processes and anything else that blocks on an event from outside
     is replaced at its seam, because a goroutine blocked on real I/O stops virtual time
     from advancing. File I/O completes on its own and is allowed.
  6. **A component signals with channels or condition variables, and holds a mutex only
     briefly.** A goroutine waiting for a mutex does not count as blocked for the fake
     clock.
  7. **Calendar decisions use the wall clock at the moment of acting.** A timer firing is
     a hint to look, never proof that a time was reached ([rrule.md](rrule.md) §6). A
     test reaches a calendar date by sleeping in virtual time, since the fake clock
     always starts at midnight UTC on 2000-01-01.
- **Why.** Upstream's fake clock could not intercept every timer, which left two ways to
  sleep and tests that wait on real time (§3). Go's fake clock intercepts the `time`
  package itself, so one discipline covers everything, including the timer service
  (which uses `time.AfterFunc`, `time.NewTicker` and `time.NewTimer`,
  `src/go/timer/timer.go`).
- **Tradeoff.** Tests that need a real database server, a real child process or a real
  socket cannot use virtual time; they wait for an observable event under a generous
  real deadline, in the composition, replay and scenario layers. The cordis module
  itself declares `go 1.24` and keeps its own timer tests on real time; the harness
  module's tests exercise the same service in virtual time.
- **Serves.** [DH-O-15](../design.md#dh-o-15), [DH-O-1](../design.md#dh-o-1),
  [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-NODETIMERS-1`,
  `DH-T-NODETIMERS-2`, `DH-T-NODETIMERS-3`.

**Upstream verdicts.** *Keep*: SHA-256 for content, HMAC-SHA-256 and constant-time
checks for the cookie, 32-byte secrets, deadlines whose reason names the timeout, owned
timers in the framework. *Change*: SHA-1 content identities to SHA-256; unowned global
timers to owned waits; the fake clock of a test framework to the standard library's
virtual time. *Drop*: the browser UUID implementation, hand yields and time slices, the
timer-limit constant, event-loop delay measurement.

## 6. Design notes

**Owners.** Inference: the identifier helper, the sleep helper and the import check live
in the shared-utilities family (`internal/util`, `internal/testkit`); the utilities area
owns the names. Cookie signing belongs to the host family, content addressing to
storage, each using the standard library directly. There is no crypto wrapper package:
a wrapper would only hide which algorithm a call site uses.

**Ownership of what a wait creates.**

| Resource | Owner (who releases it) | Released when |
|---|---|---|
| A `time.Timer` behind `Sleep(ctx, d)` | the calling function | it returns: stopped on cancel, expired otherwise |
| A deadline context | the function that created it | its `cancel` runs (always deferred; `go vet` reports a lost cancel) |
| A timer, interval, debounce or throttle from the timer service | the fiber that asked | the handle is disposed or the fiber unloads; a tick already due does not run |
| A hash state, random bytes | the caller's stack | no release needed |

**Failure and retry.** `crypto/rand.Read` does not return an error; if the operating
system cannot supply randomness the process stops, which is the fail-closed outcome. A
signature or token that fails to verify is a plain "no" with no detail in the response.
A sleep cut short returns the cancellation cause, and the caller's retry loop ends with
that cause instead of trying again. Nothing in this specification holds durable state,
so there is nothing to roll back.

**Secrets in memory.** The cookie key and tokens are held in the redacting secret type
of [DH-ADR-SECURITY-6](../upstream/security.md#dh-adr-security-6), so that a log line or
an error cannot print them. Comparison helpers accept that type.

## 7. Risks and open questions

1. **For the sandbox and SSH area:** the stream between the host and the remote helper
   is authenticated upstream with a 32-byte pre-shared key over TLS
   (`packages/ssh/ssh/src/helper-processes.ts:423-426`). Inference: Go's `crypto/tls`
   offers no pre-shared-key handshake for a caller-supplied key, so the Go design needs
   an equivalent (for example an HMAC challenge over the already encrypted SSH channel).
   This was not verified beyond reading the package's exported names.
2. **For the persistence area:** whether identifiers should be time-ordered
   (DH-ADR-NODECRYPTO-2). A time-ordered identifier reveals its creation time.
3. **For the sandbox area:** the Windows capability identifier keeps 60 bits of a
   SHA-256. That is enough against accidents; whether it is enough against a workspace
   path chosen to collide is a question for that area's threat model.
4. **For the release owner:** MD5, SHA-1 thumbprints and SHA-512 manifests exist only in
   desktop signing and upload scripts. If the Go release uses different tooling, the R7
   allow-list shrinks accordingly.
5. **For the lead:** the `uuid` standard package would remove the identifier helper. It
   needs a Go 1.27 floor (CF-4).
6. **Virtual time and the store.** Rule 5 allows real file I/O in a timed test, but a
   database driver that waits on locks with its own timers may not behave under the fake
   clock. Inference: timed tests should use the in-memory store variant; the persistence
   area decides.
7. **Suspend and clock steps.** Inference: whether a Go timer counts time spent
   suspended differs by operating system, and no timer follows a stepped wall clock.
   Rule 7 is written so that neither matters for correctness; it bounds lateness only if
   the schedule area adopts the wake cap in [rrule.md](rrule.md) §6.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-NODECRYPTO-1` | local | `DH-LIB-NODECRYPTO-R3`, `DH-LIB-NODECRYPTO-R4`, `DH-LIB-NODECRYPTO-R7`, `DH-LIB-NODECRYPTO-R8` | The import check: a test walks the harness import graph and fails if `crypto/sha1`, `crypto/md5` or `crypto/sha512` is imported outside the allow-listed boundary packages, if any cipher, key-derivation or signature package is imported, or if `math/rand` is imported by a package on the identifier and secret deny-list. A fixture package that breaks each rule makes it fail. |
| `DH-T-NODECRYPTO-2` | local | `DH-LIB-NODECRYPTO-R5`, `DH-LIB-NODECRYPTO-R6` | A value authenticated with one key verifies (the per-peer form token is the harness's case; no cookie is signed); one changed byte in body or tag, a truncated tag and a different key are rejected, and the body is not decoded when the check fails (observed through a decode counter). Comparison helpers are called through `hmac.Equal` or `subtle.ConstantTimeCompare` only, checked statically. |
| `DH-T-NODECRYPTO-3` | local | `DH-LIB-NODECRYPTO-R1`, `DH-LIB-NODECRYPTO-R2` | The identifier helper returns the UUID text form with correct version and variant bits and no repeat in one million values; the token helper returns at least 32 bytes; both read from `crypto/rand` (the reader is swapped for a counting reader in the test). |
| `DH-T-NODECRYPTO-4` | local | `DH-LIB-NODECRYPTO-R3`, `DH-LIB-NODECRYPTO-R7` | Known answers: the streaming and one-shot SHA-256 of the same bytes agree with a published vector; the proof-key challenge of the RFC 7636 appendix B verifier equals the published challenge. |
| `DH-T-NODETIMERS-1` | local | `DH-LIB-NODETIMERS-R1`, `DH-LIB-NODETIMERS-R2` | In virtual time: a sleep returns nil after its duration; cancelled early it returns the cause; a deadline context reports its typed timeout cause; a timer whose fiber unloaded never runs its callback. |
| `DH-T-NODETIMERS-2` | local | `DH-LIB-NODETIMERS-R3` | In virtual time: a component with a retry back-off of minutes and a debounce is driven through several cycles, asserting each fire time exactly, and the test's real duration stays under one second. A fake schedule that sleeps to a date years ahead does the same. |
| `DH-T-NODETIMERS-3` | local | `DH-LIB-NODETIMERS-R3`, `DH-LIB-NODETIMERS-R4` | Static checks: no `time.Sleep` outside the helper; no package-level ticker, timer or `sync.WaitGroup` value; no goroutine started from `init`. A long loop given a cancelled context stops within one slice. |
| `DH-T-NODEPERFHOOKS-1` | local | `DH-LIB-NODEPERFHOOKS-R1` | A static check that elapsed time is computed with `time.Since` or `Sub` on values from `time.Now` that were not stripped of their monotonic reading, in the telemetry and tool-result packages. |

Local tests only. They prove the helpers, the policy check and the virtual-time
discipline. They do not prove the browser login against a real browser or a timed
delivery by a running host; those are real-environment scenarios owned by the host and
schedule areas. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-node:crypto`, `UP-DEP-node:timers`, `UP-DEP-node:perf_hooks`,
`UP-DEBT-found.L10.1`, `UP-DEBT-found.L10.2`.
