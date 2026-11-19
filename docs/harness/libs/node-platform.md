# Node platform built-ins and their browser polyfills: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go facts were read on 2026-11-18 from the installed toolchain
> (`go1.27.4`: the files under `GOROOT/api` and `GOROOT/src`). Nothing was compiled or run.

| | |
|---|---|
| **Library ID** | `DH-LIB-NODEPLATFORM` (one group: ten Node built-in modules and four npm packages) |
| **Upstream units** | `UP-DEP-node:stream`, `UP-DEP-node:events`, `UP-DEP-node:buffer`, `UP-DEP-node:util`, `UP-DEP-node:string_decoder`, `UP-DEP-node:async_hooks`, `UP-DEP-node:assert`, `UP-DEP-node:process`, `UP-DEP-node:diagnostics_channel`, `UP-DEP-node:domain`, `UP-DEP-buffer`, `UP-DEP-readable-stream`, `UP-DEP-string_decoder`, `UP-DEP-supports-color`, and the debt unit `UP-DEBT-found.L17.1` defined here |
| **Upstream version** | Node built-ins have none. npm: `buffer` 6.0.3, `readable-stream` 4.7.0, `string_decoder` 1.3.0, `supports-color` 9.4.0 |
| **Kind** | Node built-in; the four npm packages are runtime dependencies |
| **Used by** | Nearly every package group (the inventory counts 48 files importing `node:stream`, 104 importing `node:util`). The three polyfills: `packages/experimental/webworker-runtime` only. `supports-color`: `vendor/logger-console` only |
| **Go choice** | standard library (`io`, `bufio`, `bytes`, `context`, `unicode/utf8`, `unicode/utf16`, `encoding/base64`, `encoding/hex`, `reflect`, `fmt`, `log/slog`, `os`), plus cordis typed events and one small piece of own code, an incremental text decoder |
| **Related areas** | wire protocols, subprocess and shell, terminals, LLM runtime, session store, agents. Neighbouring library specifications: [node-process-builtins.md](node-process-builtins.md), [node-net-builtins.md](node-net-builtins.md), [node-fs-path-os-zlib.md](node-fs-path-os-zlib.md), [eventsource-parser.md](eventsource-parser.md). The database driver has its own specification: [sqlite.md](sqlite.md) |

| Upstream dependency | What it is | Go replacement | Ledger disposition |
|---|---|---|---|
| `node:stream` | Byte streams with flow control | `io.Reader`, `io.Writer`, `io.Pipe`, `io.Copy` | replaced |
| `node:events` | String-named events on an object; helpers that wait for one | cordis typed events between components; channels and `context.AfterFunc` inside one | replaced |
| `node:buffer` | A byte array type and text encodings | `[]byte`, `encoding/base64`, `encoding/hex`, `unicode/utf8` | replaced |
| `node:util` | Assorted helpers: argument parsing, deep equality, value printing, callback adapters | `reflect`, `fmt`, `log/slog`; several are not needed | replaced |
| `node:string_decoder` | Decode text that arrives in pieces | Split on bytes first; an incremental decoder where that is impossible | replaced |
| `node:async_hooks` | A value that follows an asynchronous call chain without being passed | A value in `context.Context`, or a parameter | replaced |
| `node:assert` | Fail when a condition is false | `if` and an error in product code; `testing` in tests | replaced |
| `node:process` | The process object (arguments, environment, standard streams) | `os`, `runtime`, `os/signal` | replaced |
| `node:diagnostics_channel` | Subscribe to events that Node libraries publish about themselves | Nothing: the one use works around Node's HTTP client | not applicable |
| `node:domain` | Catch errors thrown later by callbacks | Nothing: the one use is inside the JavaScript program runner | not applicable |
| `buffer`, `readable-stream`, `string_decoder` (npm) | Copies of three Node built-ins that run in a browser | Nothing: they exist so that JavaScript host code runs in a Web Worker | not applicable |
| `supports-color` (npm) | Detect whether the terminal shows colour | A four-line rule on `os` | replaced |

`node:sqlite` is specified in [sqlite.md](sqlite.md).

## 1. What it is and why upstream needs it

These modules are the plumbing of a Node program: how bytes move, how one part of the
program tells another that something happened, how bytes become text, and how a value
follows a chain of asynchronous calls. An agent harness leans on that plumbing more than
most programs do, because almost everything it handles is a stream that arrives in pieces:
model output, the output of shell commands, terminal bytes, and the protocol lines of
every program that drives it.

None of these modules is a feature. What the harness needs from them is four behaviours,
each of which the Go design must state for itself because Go's defaults differ:

1. **A slow reader slows the writer** (backpressure), so that memory stays bounded.
2. **Text is never cut in the middle of a character.** UTF-8, the encoding used
   throughout, writes one character as one to four bytes. A chunk can end after the
   first byte of a three-byte character.
3. **Work can be attributed to the agent that started it** without every function taking
   an agent parameter.
4. **Something that waits can be told to stop.**

Terms used below. A **chunk** is one piece of a stream as the operating system or the
network happened to deliver it; its boundaries mean nothing. A **frame** is one complete
unit of a protocol (a line, a length-prefixed message, a server-sent event). A
**replacement character** (U+FFFD, shown as a question mark in a diamond) is what a
decoder writes in place of bytes that are not valid text. A **goroutine** is Go's
lightweight thread. A **context** (`context.Context`) is the value Go passes down a call
chain to carry cancellation, a deadline and request-scoped values.

## 2. How upstream uses it

Search performed for every unit: static `import`, `require`, dynamic `import()` and
`process.getBuiltinModule()` of the module, in `.ts`, `.tsx`, `.mts`, `.cts`, `.mjs`,
`.cjs` and `.js` files outside `node_modules`. Tests, fixtures and scripts were separated
from product source by path. The Node emulation in
`packages/experimental/webworker-runtime/src/node/builtin_modules/` re-implements most of
these modules for the browser; it is counted once, in §2.8.

### 2.1 `node:stream`

| Class | Where (`path:line`) | What it does | Go idiom |
|---|---|---|---|
| **Stream ends as types** | `packages/subprocess/subprocess/src/types.ts:10`, `packages/subprocess/subprocess-local/src/spawn.ts:12`, `packages/ssh/ssh/src/protocol.ts:5`, `packages/sdk/protocol/src/transport.ts:10`, `packages/sdk/server/src/index.ts:13`, `packages/lsp/lsp-stdio/src/connection.ts:13`, `packages/subagent/subagent-codex/src/wire.ts:10`, `packages/ptc-runtime/ptc-runtime-node/src/rpc.ts:3`, `packages/host/webserver/src/index.ts:20`, `packages/api/gateway/src/stream-server.ts:4` | Name the input and output of a child process, a socket or a protocol peer | `io.Reader`, `io.Writer`, `io.ReadWriteCloser`, `net.Conn` |
| **In-process pipe** (`PassThrough`) | `packages/subprocess/subprocess-local/src/terminal.ts:61`, `packages/ssh/subprocess-ssh/src/index.ts:29-33`, `:306`, `packages/experimental/cua-windows/src/native-runtime.ts:203`, `packages/experimental/cua-macos/src/shared-native.ts:43`, `packages/ptc-runtime/ptc-runtime-node/src/evaluators/repl.ts:15` | Hand bytes from one part of the program to another | `io.Pipe` (no buffer: the writer waits for the reader), or a bounded buffer with a stated overflow rule |
| **Backpressure** | `packages/host/webserver/src/index.ts:400-418` (wait for `drain` on an HTTP response), `packages/subprocess/subprocess-local/src/terminal.ts:103-110` (pause the terminal until its consumer drains), `packages/ssh/ssh/src/protocol.ts:148-153`, `packages/client/connection/src/http-bridge.ts:116-120`, `packages/ptc-runtime/ptc-runtime-node/src/rpc.ts:43` (fail when queued output exceeds a limit) | Stop producing while the consumer is behind | A blocking `Write`. Where blocking is not acceptable, a bounded queue that disconnects or fails |
| **Copy until done** (`pipeline`, `finished`) | `packages/ssh/ssh/src/helper-processes.ts:193`, `:240`, `:246`, `:254`, `packages/connector/upstream-skills/src/archive.ts:79`, `packages/experimental/speech-to-text-sensevoice/src/runtime.ts:131`, `packages/session/session-persistence-jsonl/src/generation.ts:755`, `packages/account/deepseek-account/src/index.ts:547` | Connect a source to a sink, propagate the first error, close both | `io.Copy` in a goroutine, the error returned, both ends closed by the owner |
| **Bridging two stream models** | `packages/connector/upstream-skills/src/archive.ts:143`, `packages/client/connection/src/http-bridge.ts:92`, `packages/acp/acp/src/index.ts:375-376`, `packages/subagent/subagent-acp/src/run.ts:454-455` | Convert between Node streams and browser-style web streams | Not needed: an HTTP body, a pipe and standard input are all `io.Reader` |
| **Transform and custom sinks** | `packages/experimental/speech-to-text-sensevoice/src/runtime.ts:124` (hash while downloading), `packages/experimental/cua-windows/src/native-runtime.ts:216`, `packages/ptc-runtime/ptc-runtime-node/src/evaluators/repl.ts:16` | Observe or rewrite bytes on the way through | `io.TeeReader`, `io.MultiWriter`, or a type with a `Write` method |
| **Cancel a read** (`addAbortSignal`) | `packages/connector/cli-account/src/executor.ts:130` | End a stream when its operation is cancelled | Close the source when the context ends (`DH-LIB-NODEPLATFORM-R3`) |
| **Test doubles** | about 80 test and fixture files (126 files import the module in all, against the 48 the inventory counts), for example `packages/ssh/ssh/tests/protocol.spec.ts:1` | Fake a process or a socket | `io.Pipe`, `net.Pipe`, `bytes.Buffer` |

### 2.2 `node:events`

Product source uses three things. The rest of the 15 counted files, and about 130 test
and fixture files, use the same three.

| Use | Where | Go idiom |
|---|---|---|
| An object that emits named events (`EventEmitter`) | `packages/ssh/ssh/src/protocol.ts:58` (a protocol peer that emits `closed`, `:154`), `packages/subagent/subagent-claude-code/src/process.ts:70` | Between components: a cordis typed event key ([design.md §4.6](../design.md#46-event-model)). Inside one component: a `Done()` channel that is closed once, with an `Err()` accessor |
| Wait for one event, or stop waiting when cancelled (`once`) | `packages/fs/fs-local/src/index.ts:91`, `packages/boot/plugin-manager/src/operations.ts:395` | `select` on the channel and on `ctx.Done()` |
| Run a function when an operation is cancelled, and get a handle that removes the registration (`addAbortListener`) | `packages/boot/plugin-market/src/repository-cache.ts:132`, `packages/telemetry/otel/src/event-log.ts:85`, and four more | `context.AfterFunc`, which returns a stop function (Go 1.21, `api/go1.21.txt:7`) |

One Node behaviour has no counterpart and is not missed: an `error` event with no
listener throws and can end the process, which upstream has to design around
(`packages/subagent/subagent-claude-code/src/process.ts:81`). In Go an error is a return
value.

### 2.3 `node:buffer`

`Buffer` is also a global in Node, so the 25 importing files understate its use. In
product source `Buffer.from` appears 117 times, `Buffer.byteLength` 94, `Buffer.concat`
46 and `Buffer.alloc` 40. Text encodings used: `utf8`, `base64`, `hex`, `base64url`,
`utf16le`, `ascii`.

| Use | Where (example) | Go idiom |
|---|---|---|
| Hold and join bytes | `packages/subprocess/subprocess-local/src/output.ts:263` | `[]byte`, `append`, `bytes.Buffer` |
| Count bytes of a string (`byteLength`) | `packages/ptc-runtime/ptc-runtime-node/src/rpc.ts:43` | `len(s)`. Upstream needs a function because a JavaScript string counts 16-bit units; a Go string is its bytes |
| Encode and decode | `base64` and `hex` across attachments, feedback and session-log packages | `encoding/base64` (`StdEncoding`, `RawURLEncoding`), `encoding/hex` |
| Check that bytes are text (`isUtf8`) | `packages/boot/hmr/src/package-manifest.ts:293` | `utf8.Valid` |
| Platform maximum sizes (`constants.MAX_LENGTH`) | `packages/fs/fs-local/src/index.ts:65-66`, `packages/session/session-persistence-jsonl/src/zstd-private-decoder.ts:150` | Not applicable: a Go slice has no such engine limit. Size limits remain as stated policy, not as a platform constant |

### 2.4 `node:util`

104 importing files; 97 outside tests, of which most are scripts.

| Function | Files outside tests | Where it matters | Go answer |
|---|---|---|---|
| `parseArgs` | 44, all in `scripts/`, `apps/desktop/scripts/`, `native/` and one test server | Build and release scripts | The command-line library of [commander.md](commander.md) for `dsh`; the standard `flag` package for build tools |
| `promisify` | 25; 21 in scripts and the desktop app. Product: `packages/session/session-persistence-sqlite/src/frame.ts:10`, `packages/session/session-persistence-jsonl/src/zstd.ts:11`, `packages/fs/fs-local/src/fsio.ts:13` | Turn a callback function into one that returns a promise | Not applicable: a Go call blocks its goroutine and returns its result |
| `isDeepStrictEqual` | 18, all product: `packages/schedule/schedule/src/update.ts:2`, `packages/mcp/mcp-client/src/tools.ts:16`, `packages/boot/config-editor/src/index.ts:4`, `packages/compaction/compaction-basic/src/region.ts:9`, `packages/core/tool-executor-ptc/src/ptc.ts:9`, `packages/client/modules/src/config-sync.ts:5` and others | "Did this value change?" for configuration, tool lists and schedules | `DH-LIB-NODEPLATFORM-R12` |
| `inspect` | 12: `apps/cli/src/startup-diagnostics.ts:6`, `apps/desktop-host/src/fatal-diagnostic.ts:3`, `packages/boot/app-boot/src/index.ts:11`, `packages/subprocess/subprocess-local/src/runner-launch.ts:6`, `vendor/logger-console/src/index.ts:10`, and the program runner | Print an arbitrary value in a diagnostic | `fmt` with `%+v` and `%q`; `log/slog` attributes (`DH-LIB-NODEPLATFORM-R13`) |
| `parseEnv` | `packages/boot/app-boot/src/index.ts:225` | Parse a `.env` file at start-up | Go has no such function. Whether the Go boot reads a `.env` file at all is the boot area's decision (§7) |
| `getSystemErrorName` | `packages/subprocess/subprocess-local/src/linux-execve.ts:3` | Name an operating-system error number | `syscall.Errno` and `errors.Is` |
| `types`, `isPromise` | 4: the program runner, and `packages/core/agent/src/index.ts:11` | Ask what kind of JavaScript value something is | Not applicable |

### 2.5 `node:string_decoder` and decoding text that arrives in pieces

`StringDecoder` itself is imported by only two files outside tests and the emulation:

| Where | What it does |
|---|---|
| `packages/sdk/protocol/src/transport.ts:68`, `:182`, `:202` | The line-delimited JSON-RPC transport decodes each chunk to text, appends it to a string and then looks for a newline |
| `apps/desktop/scripts/packaging-run.mjs:40` | A release script that redacts secrets from a tool's output |

The need is wider than the import. Upstream meets it with four different mechanisms:

| Mechanism | Where | Behaviour on a split character |
|---|---|---|
| The web `TextDecoder` in streaming mode | `packages/api/terminal-controller/src/terminal.ts:370-378`, `packages/terminal/terminal-bash/src/session.ts:483-489`, `packages/fs/fs-local/src/fsio.ts:369`, `:499` | Held until the rest arrives |
| A decoding stream | `packages/llm/llm-deepseek/src/sse.ts:14` (model output, before the event parser) | Held |
| `setEncoding('utf8')` on a process stream | `packages/util/ripgrep/src/index.ts:72`, `packages/connector/cli-account/src/executor.ts:131`, `packages/ptc-runtime/ptc-runtime-node/src/worker.ts:320` | Held |
| Hand-written boundary trimming when output is cut to a size | `packages/util/output-retention/src/index.ts:211-233`, `packages/subagent/subagent/src/out-of-process.ts:27-40` | The incomplete character is dropped at the cut |
| **No handling** | `packages/subprocess/subprocess-local/src/output.ts:261-267` | See §3 |

Upstream also distinguishes two policies for bytes that are not text at all: **strict**
for files, where the read is refused as "not text"
(`packages/fs/fs-local/src/fsio.ts:352-360`), and **lossy** for process output, where
they become replacement characters (`packages/util/output-retention/src/index.ts:201`).

### 2.6 `node:async_hooks` (`AsyncLocalStorage`)

`AsyncLocalStorage` stores a value that every function called later in the same
asynchronous chain can read, without it being passed. Every use, with its Go form:

| Where | What the hidden value is | Go form |
|---|---|---|
| `packages/core/agent/src/index.ts:263-264`, `:325-374` | The **initiator**: the agent on whose behalf the current chain runs (`currentInitiator`, `requireInitiator`), set by `withInitiator` and cleared by `withoutInitiator`. A second store counts live boundaries so that disposal can wait for them (`:603-621`) | A context value, per [DH-ADR-ARCH-8](../upstream/architecture.md#dh-adr-arch-8). `withoutInitiator` becomes a derived context with the value cleared. The drain bookkeeping is not ported: nothing has to be disabled, because the value lives only as long as the contexts that carry it |
| `packages/sdk/server/src/server.ts:103`, `:317-321` | "Is this call running inside *this* query command?", so an activity can tell its own invocation from others | The activity is put in the context passed to the command, and compared there |
| `packages/boot/hmr/src/index.ts:255`, `:269`, `:294`, `:363`, `:938` | "Am I inside the exclusive reload transaction?": refuses nesting, asserts that reconciliation runs inside, and lets a transaction dispose its own entry without waiting for itself | An explicit transaction handle passed as a parameter. Go locks are not re-entrant and cannot answer "do I hold this?", so the fact has to be a value. Hot reload of code is not carried over ([DH-ADR-LOADER-8](cordis-plugins.md#dh-adr-loader-8)); configuration reload keeps the rule |
| `packages/ptc-runtime/ptc-runtime-node/src/process.ts:57`, `:67`, `:108-114`, `:145-153` | Inside the program runner's own process: which evaluation the running program code belongs to, for its log sink, image sink and host calls | Outside the Go host. On the host side an evaluation is a request with an id on the runner's connection. If the runner is ever Go code (OD-2, option b), the evaluation is a struct captured by the binding closures |
| `packages/ptc-runtime/ptc-runtime-node/src/worker.ts:146` (`AsyncLocalStorage.bind`) | Capture the caller's hidden values when a binding is registered, so that a call arriving later from the runner runs as that caller | The handler closes over the tool call's context explicitly |
| `packages/ptc-runtime/ptc-runtime-node/src/imports/module-hooks.ts:23`, `:38` | A flag read inside Node's module-resolution hook | Not applicable: exists only because of Node's module hooks |
| `packages/experimental/liveshare/src/host/relay-h2-idle.ts:20-38`, `:70` | "A relay request is on the call stack", read by a listener on a Node diagnostics channel | Not applicable (§2.7). Where Go code does need per-request hooks on an HTTP client, the standard library already uses a context value for it (`net/http/httptrace`) |
| `packages/experimental/webworker-runtime/src/node/builtin_modules/implemented/async_hooks.ts:70` | The browser emulation of the class | Not applicable (§2.8) |
| `native/system/test/fixtures/flock-worker.js:3` | A test fixture | Not applicable |

**Result of the check asked for:** every use maps to a context value, an explicit
parameter, or disappears. None needs storage attached to a goroutine.

### 2.7 `node:assert`, `node:process`, `node:diagnostics_channel`, `node:domain`

| Module | Use | Go answer |
|---|---|---|
| `node:assert` | 20 counted files; in product source it guards invariants, for example `packages/boot/hmr/src/index.ts:163`, `packages/experimental/timeline/src/storage.ts:41`, `packages/experimental/cua/src/native-client.ts:292`. Everything else is tests, fixtures and scripts | `DH-LIB-NODEPLATFORM-R15` |
| `node:process` | The 5 counted files that import it by name are governance and release scripts (`scripts/pre-merge-check.ts:13`, `scripts/verify-version-sync.ts:13`, `apps/desktop/scripts/publish-installed-update.ts:4`). The same object is a global used everywhere for arguments, environment, platform, exit and the standard streams | `os.Args`, `os.Getenv`, `runtime.GOOS`, `os.Exit`, `os.Stdin`, `os/signal`. Starting processes and signals are in [node-process-builtins.md](node-process-builtins.md). Reading the environment is restricted by the security and loader decisions, not by this specification |
| `node:diagnostics_channel` | One use: `packages/experimental/liveshare/src/host/relay-h2-idle.ts:85` subscribes to the channel on which Node's HTTP client announces each connected socket, to swallow an "idle timeout" error that the client reports on a socket no request owns (`:2-6`) | Not applicable. The defect belongs to that client. Go's `net/http` returns connection errors to the caller of the request |
| `node:domain` | One use: `packages/ptc-runtime/ptc-runtime-node/src/evaluators/scoped-repl-evaluator.ts:44-45` catches errors thrown later by a model-written program's callbacks, "as in Node's REPL, instead of terminating the process" (`:24-25`) | Not applicable to the host. Whatever runs model-written programs is a separate process (OD-2) and owns its own error capture; the host sees a failed evaluation on the runner's connection |

### 2.8 The npm polyfills and `supports-color`

`packages/experimental/webworker-runtime` is a "Browser-only harness runtime ... with the
Node-compatibility layer that lets the host tree run unchanged" (its `package.json:3`).
A browser has no `node:buffer`, `node:stream` or `node:string_decoder`, so the package
bundles npm copies and maps the built-in names to them:

| Package | Imported at | Stands in for |
|---|---|---|
| `buffer` 6.0.3 | `src/node/builtin_modules/implemented/buffer.ts:13`, `fs.ts:11`, `crypto.ts:9`, `child_process.ts:18`, `fs-watch.ts:2` | `node:buffer` |
| `readable-stream` 4.7.0 | `src/node/builtin_modules/implemented/stream.ts:9` | `node:stream` |
| `string_decoder` 1.3.0 | `src/node/builtin_modules/implemented/string_decoder.ts:4` | `node:string_decoder` |

They disappear because the owner dropped the in-browser preview runtime
([DH-ADR-17](../design.md#dh-adr-17)): nothing runs in a worker, so nothing needs a
stand-in for a Node module.

`supports-color` has one use: the default colour level of the console log exporter
(`vendor/logger-console/src/index.ts:23`). That exporter is not ported
([DH-ADR-LOADER-9](cordis-plugins.md#dh-adr-loader-9)). What remains is the rule
`DH-LIB-NODEPLATFORM-R14` for any Go code that writes colour.

## 3. Tech debt upstream carries

No `UP-DEBT` unit was assigned. Search performed: `patches/` for the four npm packages
(none; the inventory marks all four unpatched), `pnpm-workspace.yaml` for pins or
overrides naming them (none), and the consuming packages' READMEs. One item was found in
source and is defined by this specification. The others are habits the built-ins force,
listed because each maps to a requirement.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L17.1` | **Incremental reads of process output decode from an arbitrary byte position.** `readFrom(fromByte)` slices the retained bytes at the caller's offset and decodes up to whatever has arrived. Inference: when a multi-byte character is half-arrived, this read ends in a replacement character and the next read starts with another, so the model sees two wrong characters where the program wrote one. The same method trims its in-memory window "byte-exact at the cap" (`:175`), which can also start the window inside a character. The shell tools poll with it (`packages/shell/bash-local/src/index.ts:343-344`) | `packages/subprocess/subprocess-local/src/output.ts:261-267` | `DH-LIB-NODEPLATFORM-R5` and `-R6`: the reader holds back an incomplete trailing character and reports the offset of what it returned; windows start on a character boundary |
| none (habit) | Four different mechanisms for one problem (§2.5), chosen site by site | §2.5 | One decoder type and one rule (`DH-ADR-NODEPLATFORM-1`) |
| none (habit) | Two stream models (Node and web) with conversions at each border | `packages/acp/acp/src/index.ts:375-376` | One model |
| none (habit) | Hidden per-call state needs bookkeeping to dispose safely, and shared timers must remember to clear it | `packages/core/agent/src/index.ts:603-621`, `:361-374` | `DH-ADR-NODEPLATFORM-2` |
| none (habit) | Sizes are counted in three units: bytes (`Buffer.byteLength`), 16-bit units (`string.length`) and code points (`packages/session-query/session-query-sqlite/src/schema.ts:134`) | as cited | `DH-LIB-NODEPLATFORM-R8` |
| none (carried by another unit) | A whole Node emulation, with three npm polyfills, to run the host in a browser | `packages/experimental/webworker-runtime/package.json:35-41` | Not applicable in Go (§2.8) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NODEPLATFORM-R1` | Byte streams are `io.Reader` and `io.Writer` values. A producer writing to a consumer that is behind either waits or meets a stated bound; it never grows memory without limit. | §2.1 "Backpressure" |
| `DH-LIB-NODEPLATFORM-R2` | Every in-process buffer between a producer and a consumer has a byte limit and one declared overflow rule: wait, drop the oldest with a visible marker, or disconnect the consumer. | `terminal.ts:103-110`, `rpc.ts:43`; [design.md §5](../design.md#5-failure-retry-rollback-and-cleanup) (slow reader) |
| `DH-LIB-NODEPLATFORM-R3` | Every goroutine that copies or reads a stream ends when its owner's context ends. A blocked read is released by closing the source or by a read deadline. | `executor.ts:130`; [design.md §4.5](../design.md#45-concurrency-model) |
| `DH-LIB-NODEPLATFORM-R4` | Protocols are split into frames on **bytes**, and a frame is decoded to text only when it is complete. A frame larger than the configured limit is refused with a classified error. | `transport.ts:182-195` |
| `DH-LIB-NODEPLATFORM-R5` | Where text must be passed on before a frame is complete, an incremental decoder holds back an incomplete trailing character (at most three bytes) and emits it with the next chunk. At end of stream, bytes still held become one replacement character. Offsets reported to callers count only what was emitted. | §2.5; `UP-DEBT-found.L17.1` |
| `DH-LIB-NODEPLATFORM-R6` | Every cut of text by size (a kept prefix, a kept suffix, a tail window, a read from an offset, a diagnostic limit) lands on a character boundary. The harness never produces a replacement character that the source bytes did not call for. | `output-retention/src/index.ts:211-233` |
| `DH-LIB-NODEPLATFORM-R7` | Each decoding site declares its policy for bytes that are not text: **strict** (refuse, for file tools) or **lossy** (replace, for process and terminal output). | `fsio.ts:352-360`, `output-retention/src/index.ts:201` |
| `DH-LIB-NODEPLATFORM-R8` | Every size limit names its unit. Bytes for budgets, buffers and wire limits; code points for lengths shown to a person; 16-bit units only where an outside protocol demands them. | §3 |
| `DH-LIB-NODEPLATFORM-R9` | The initiating agent, and any other state that follows one call chain, travels in `context.Context` or as a parameter. No code reads goroutine identity. Background work that is started lazily and then shared detaches attribution explicitly. | §2.6; [DH-ADR-ARCH-8](../upstream/architecture.md#dh-adr-arch-8) |
| `DH-LIB-NODEPLATFORM-R10` | There is no string-named event emitter. Between components events are cordis typed keys; inside a component the end of an object's life is a channel closed exactly once plus an error accessor. | §2.2 |
| `DH-LIB-NODEPLATFORM-R11` | A callback registered against cancellation returns a stop function, and the registering fiber releases it on unload. | `repository-cache.ts:132` |
| `DH-LIB-NODEPLATFORM-R12` | "Did it change?" is defined per type: comparison of typed values where the type is comparable, an explicit `Equal` otherwise, and comparison of canonical JSON for schema-like data. An absent list and an empty list compare as the type's documentation says, not by accident. | §2.4 `isDeepStrictEqual` |
| `DH-LIB-NODEPLATFORM-R13` | Values printed in logs and diagnostics go through `fmt` or `log/slog`, are bounded in size, and print a secret as `[redacted]`. | `logger-console/src/index.ts:10` (unbounded depth); [design.md §6](../design.md#6-security-model), rule 4 |
| `DH-LIB-NODEPLATFORM-R14` | Colour codes are written only when the destination is a terminal, `NO_COLOR` is unset and `TERM` is not `dumb`. They are never written to a stream that carries a protocol. | `logger-console/src/index.ts:23`; [DH-ADR-LOADER-9](cordis-plugins.md#dh-adr-loader-9) |
| `DH-LIB-NODEPLATFORM-R15` | A broken internal invariant in product code is an error returned to the caller, or a panic only where the cordis containment of [design.md §5](../design.md#5-failure-retry-rollback-and-cleanup) turns it into a failed fiber. Assertion helpers are for tests. | §2.7 |

## 5. Go choice

**Decision.** The standard library, cordis typed events for cross-component events, and
one small own type: an incremental UTF-8 decoder. No Go module is added.

**Why.** Each requirement is a rule about how to use `io`, `context` and `unicode/utf8`,
not a missing capability. A library that imitated Node's streams or emitters would import
the two-model and hidden-state problems §3 lists.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Standard library plus one decoder type** (chosen) | Nothing to version; blocking writes give backpressure by default; `context` is already the first parameter everywhere | The rules of §6 must be followed by hand, so tests and a source check carry the weight | Symbols read in `GOROOT/api`: `io.Pipe`, `io.TeeReader`, `io.MultiWriter` (`go1.txt`), `utf8.FullRune`, `utf8.Valid`, `utf8.RuneStart` (`go1.txt`), `bufio.Scanner.Buffer` (`go1.6.txt:4`), `context.AfterFunc`, `context.WithoutCancel` (`go1.21.txt:7`, `:9`), `strings.ToValidUTF8` (`go1.13.txt:249`). Checked 2026-11-18, Go 1.27.4 |
| `golang.org/x/text/transform` for incremental decoding | A general streaming transformer, also for other encodings | A dependency for about thirty lines of UTF-8 logic. It becomes relevant only if an area must decode a non-UTF-8 encoding (§7) | Not verified here; not declared |
| A goroutine-local storage package, to keep upstream's hidden initiator | Call sites would look like upstream | Go has no supported goroutine identity; such packages read it from stack traces or runtime internals. Rejected by [DH-ADR-ARCH-8](../upstream/architecture.md#dh-adr-arch-8) | None named or verified, deliberately |
| `golang.org/x/term` for the terminal check in R14 | A direct "is this a terminal" call | Not needed for the rule itself: `os.File.Stat` reports a character device. [node-process-builtins.md §7](node-process-builtins.md#7-risks-and-open-questions) already names the module as the command-line area's candidate | v0.48.0, 2026-11-09, confirmed with `go list -m -json` on 2026-11-18; licence not re-read; not declared here |

**Tradeoff.** Go's convenience conversions hide the problem this specification exists
for. `string(bytes)` never fails, and `encoding/json` silently writes a replacement
character for invalid text (`GOROOT/src/encoding/json/encode.go:41`). A character cut in
half therefore becomes a permanent wrong character in the session log the moment the
text is stored. In JavaScript the corresponding mistake is at least visible as a decoder
choice; in Go it is the default.

<a id="dh-adr-nodeplatform-1"></a>
### DH-ADR-NODEPLATFORM-1 Split on bytes, decode whole frames, and use one incremental decoder where a frame cannot be waited for

- **Decision.** Every protocol reader finds its frame boundary in the byte stream and
  decodes only complete frames. The places that must show text before any boundary
  (§6.2) use one shared decoder type that holds back an incomplete trailing character.
  Every size cut uses one shared boundary function.
- **Why.** In UTF-8 no byte of a multi-byte character can equal a newline, a carriage
  return or an ASCII digit, so a frame found on bytes never ends inside a character.
  That removes the problem from every line-based and length-prefixed protocol without a
  decoder at all. Upstream's SDK transport does the opposite (decode each chunk, then
  search the text, `transport.ts:182-190`) and needs `StringDecoder` only because of that
  order.
- **Tradeoff.** Text that is shown live can lag by up to three bytes until the character
  completes. A stream that ends mid-character shows one replacement character.
- **Serves.** [DH-O-4](../design.md#dh-o-4) (what is logged equals what was produced),
  [DH-O-6](../design.md#dh-o-6) (results are bounded without being damaged),
  [DH-O-12](../design.md#dh-o-12), [DH-O-13](../design.md#dh-o-13).
- **Verified by.** `DH-T-NODEPLATFORM-1`, `-2`, `-3`.

<a id="dh-adr-nodeplatform-2"></a>
### DH-ADR-NODEPLATFORM-2 State that follows a call chain is a context value or a parameter; there is no goroutine-local storage

- **Decision.** This extends [DH-ADR-ARCH-8](../upstream/architecture.md#dh-adr-arch-8)
  from the initiating agent to every upstream use of `AsyncLocalStorage` (§2.6).
  *Attribution* ("who started this") is a context value with an unexported key and typed
  accessor functions. A *capability* ("you are inside the reload transaction") is a
  parameter, because a function that requires it should not compile without it. Work
  that outlives its caller derives its context with `context.WithoutCancel` and clears
  attribution in the same helper.
- **Why.** Go has no storage attached to a goroutine. Attribution is optional
  information and belongs in the carrier Go already passes; a capability is a
  precondition and belongs in the signature.
- **Tradeoff.** Upstream's value reaches every callback, timer and event listener
  without anyone writing code. In Go it reaches only where the context is passed. A
  goroutine started with a fresh context silently loses attribution; for shared timers
  and pumps that is what upstream wants anyway (`index.ts:361-367`), and elsewhere it is
  a defect a test must catch.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-1](../design.md#dh-o-1).
- **Verified by.** `DH-T-NODEPLATFORM-6`.

## 6. Design notes

### 6.1 Streams, ownership and cleanup

- **Who closes.** The component that opened a stream closes it; passing a reader to
  another function gives that function access, not ownership. A copy loop is an effect
  of the fiber that started it, so unloading the fiber closes the source and waits for
  the loop to return (`DH-LIB-NODEPLATFORM-R3`).
- **A read cannot be cancelled by a context alone.** `io.Reader` takes no context. The
  owner closes the underlying file, pipe or connection, or sets a read deadline where
  the type has one (`os.File.SetReadDeadline`, `api/go1.10.txt:591`; `net.Conn`).
- **Half-way failure.** `io.Copy` returns the first error from either side. The owner
  closes both ends, and the consumer sees end of stream or the error; bytes already
  delivered are kept. Whether the remote side received what was written is uncertain and
  is reported by the protocol above, not by the stream.
- **Line readers.** `bufio.Scanner` stops with an error on a line longer than 64 KiB
  unless given a larger buffer (`bufio.MaxScanTokenSize = 65536`, `api/go1.1.txt:22`).
  Tool results exceed that routinely, so every line-based codec sets an explicit limit
  and reports an over-long line as its own error (`DH-LIB-NODEPLATFORM-R4`).

### 6.2 Where the harness must handle a character split across chunks

| Place | Why a split can happen | Rule |
|---|---|---|
| JSON-RPC over standard streams: SDK, ACP, MCP and the plugin protocol ([design.md §4.8](../design.md#48-wire-surfaces)) | A pipe delivers arbitrary chunks | Split on the newline byte, then decode the line (`R4`). No decoder needed |
| Language-server connections | Messages are length-prefixed in bytes | Read the stated number of bytes, then decode. Positions inside documents are in 16-bit units by that protocol's default: convert with `unicode/utf16` at the border (`R8`) |
| Model provider streams | The HTTP body arrives in chunks | The event parser of [eventsource-parser.md](eventsource-parser.md) splits on bytes; each event's payload is complete JSON (`R4`). One risk remains and is listed in §7 |
| Process output read incrementally (background shell commands, jobs) | The child writes when it likes; the reader polls | The incremental decoder, with offsets that count only emitted bytes (`R5`). This is the fix for `UP-DEBT-found.L17.1` |
| Process output collected and then cut to a budget | The cut position is a byte count | The boundary function (`R6`), lossy policy (`R7`) |
| Terminal output to the browser and to the model | A pseudo-terminal delivers raw chunks | To the terminal widget: forward the bytes unchanged and let the widget decode. To anything that needs text (snapshots for the model, logs): the incremental decoder (`R5`) |
| Files read in pieces by the file tools | A read buffer ends anywhere | Strict policy (`R7`): validate across the boundary with the same decoder, and refuse the file as "not text" on the first invalid sequence |
| Spill files, diagnostics and log lines cut to a limit | Byte limits | The boundary function (`R6`) |
| Server-sent events to the browser | The host frames these itself | Each event is built from already-decoded text, so nothing can split, provided the rows above hold |

### 6.3 The incremental decoder

Inference: a proposal for the shared helper package ([design.md §4.2](../design.md#42-go-module-layout),
`internal/util`); the owning area fixes names.

| Element | Behaviour |
|---|---|
| `Decoder` (zero value ready; not safe for concurrent use; one per stream) | Holds at most three bytes between calls |
| `Write(chunk) (text, emittedBytes)` | Returns the longest prefix of held-plus-new bytes that ends on a character boundary. In lossy mode invalid bytes become replacement characters; in strict mode the first invalid sequence is returned as an error with its byte offset |
| `Flush() text` | End of stream: bytes still held become one replacement character (lossy) or an error (strict) |
| `CutPrefix(bytes, limit)`, `CutSuffix(bytes, limit)` | The longest prefix, or suffix, within `limit` bytes that starts and ends on a character boundary |

The decoder owns no resource; it is released with the stream reader that holds it. A
decoder that is dropped without `Flush` loses at most the three held bytes, which by
definition were not a complete character.

### 6.4 Values in a context

Each package that defines an attribution value exports typed functions (for the agent:
`WithInitiator(ctx, agent) ctx`, `Initiator(ctx) (agent, ok)`, `WithoutInitiator(ctx) ctx`)
and keeps the key unexported, so that no other package can set or forge it. The value is
attribution only. As upstream states, "presence is neither liveness proof nor
authorization" (`packages/core/agent/src/index.ts:352`); interfaces at lifecycle, log,
authority, process and wire boundaries still take the agent or session explicitly
([DH-ADR-ARCH-8](../upstream/architecture.md#dh-adr-arch-8)).

## 7. Risks and open questions

1. **A character split by the model provider, not by the network.** Inference: a
   provider or proxy can end one streamed text delta with the first half of a character
   written as a JSON escape and begin the next delta with the second half. JavaScript
   joins the two halves when it concatenates the strings. Go's `encoding/json` replaces
   each lone half with a replacement character when it decodes (`decode.go:100-101`), so
   the two can no longer be joined. Whether any supported provider does this is not
   known. The LLM-runtime area should decide whether the adapter inspects the raw delta
   for a trailing unpaired half before decoding. `DH-T-NODEPLATFORM-2` includes the case
   so that the behaviour is at least a recorded one.
2. **Output that is not UTF-8.** Windows programs may write in the console code page or
   in UTF-16. Upstream decodes process output as UTF-8 everywhere. The shell and
   subprocess areas must either force UTF-8 in the child's environment or decode, which
   would bring in `golang.org/x/text` and a library item of its own.
3. **`.env` files.** Upstream parses one at start-up with `parseEnv`
   (`packages/boot/app-boot/src/index.ts:225`). The boot area decides whether the Go
   boot keeps it; if so the parser is a small piece of own code or a module that area
   verifies.
4. **Terminal colour on Windows.** Old console hosts need a mode switch before they
   interpret colour codes. `DH-LIB-NODEPLATFORM-R14` says when colour may be written,
   not how the console is prepared; that belongs to the command-line area.
5. **Discipline, not mechanism.** `R1`, `R3` and `R9` are conventions. Their strength is
   the tests in §8 and code review; a linter for "goroutine started without a context"
   would help and is not specified here.
6. **New harness code.** The decoder and boundary functions (§6.3) and the context
   accessors (§6.4) are own code. They are small, shared, and needed by the first wire
   codec, so the owning areas should schedule them early. This specification declares no
   deliverable.

## 8. Verification

Local tests. Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-NODEPLATFORM-1` | local, property | `R5`, `R7`, `DH-ADR-NODEPLATFORM-1` | For a corpus containing two-, three- and four-byte characters and invalid bytes, every way of splitting the bytes into chunks gives the same text through the incremental decoder as decoding the whole. Strict mode reports the first invalid byte's offset for every split. Emitted byte counts sum to the input length minus what `Flush` replaced |
| `DH-T-NODEPLATFORM-2` | local | `R4` | A JSON-RPC line containing multi-byte characters, delivered one byte at a time, decodes to the same message as when delivered whole. A line over 64 KiB and under the configured limit is accepted; a line over the limit yields the classified error and the reader continues with the next line. A recorded case documents what two consecutive deltas carrying the two halves of one escaped character decode to |
| `DH-T-NODEPLATFORM-3` | local, property | `R6`, `R8` | For every limit from zero to the input length, the prefix cut, suffix cut, tail window and read-from-offset results are valid text and contain no replacement character absent from the source. A length limit stated in code points counts code points |
| `DH-T-NODEPLATFORM-4` | local | `R1`, `R2` | A producer writes far more than the buffer limit to a consumer that has stopped reading: memory stays under the limit, and the declared rule is observed (the writer waits, the oldest data is dropped with its marker, or the consumer is disconnected) |
| `DH-T-NODEPLATFORM-5` | local, race detector | `R3`, `R11` | Cancelling the owner's context while a goroutine is blocked reading from a child's output, a network connection and an in-process pipe returns each within a bound; the goroutine count returns to its starting value; a stop function from `context.AfterFunc` prevents the callback and is called on unload |
| `DH-T-NODEPLATFORM-6` | local | `R9`, `DH-ADR-NODEPLATFORM-2` | Nested attribution: the inner value wins and the outer is restored; a goroutine given the context sees the initiator; one given the detached context does not, and is not cancelled with its creator; a package outside the owner cannot set the value (compile-time, by the unexported key) |
| `DH-T-NODEPLATFORM-7` | local | `R10`, `R12`, `R13` | A lifecycle channel is closed exactly once under concurrent failure and close; the equality helpers treat absent and empty lists as documented for each type; a struct holding a secret prints `[redacted]` through `%v`, `%+v` and `slog`, and an oversized value is truncated at a character boundary |
| `DH-T-NODEPLATFORM-8` | local, table | `R14` | For each combination of terminal or pipe, `NO_COLOR` set or unset, and `TERM` values, the colour decision matches the rule; under the SDK and ACP profiles standard output never receives a colour code |

`R15` is checked by review: no assertion helper is imported outside test files.

## 9. Units dispositioned by this spec

- Replaced by `DH-LIB-NODEPLATFORM`: `UP-DEP-node:stream`, `UP-DEP-node:events`,
  `UP-DEP-node:buffer`, `UP-DEP-node:util`, `UP-DEP-node:string_decoder`,
  `UP-DEP-node:async_hooks`, `UP-DEP-node:assert`, `UP-DEP-node:process`,
  `UP-DEP-supports-color`.
- Not applicable: `UP-DEP-node:diagnostics_channel`, `UP-DEP-node:domain`,
  `UP-DEP-buffer`, `UP-DEP-readable-stream`, `UP-DEP-string_decoder`.
- Defined and resolved here: `UP-DEBT-found.L17.1`.
- `UP-DEP-node:sqlite` is dispositioned by [sqlite.md](sqlite.md).

Go items declared: `DH-LIB-NODEPLATFORM`, `DH-ADR-NODEPLATFORM-1`,
`DH-ADR-NODEPLATFORM-2`, `DH-T-NODEPLATFORM-1` to `DH-T-NODEPLATFORM-8`.
