# Tools from scripts: the `dsh tool` command line (what replaces the PTC guest runtime): Go specification

> **Status: design, not built. The choice is decided by the repository owner (OD-2,
> recorded as [DH-ADR-8](../design.md#dh-adr-8)): there is no
> embedded interpreter. The harness ships an external Go command line through which
> ordinary scripts call tools.**
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The earlier
> revision compared engines for running model-written JavaScript and recommended one;
> that comparison is now section 5.3, "Options considered and not taken".
> Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Everything this document proposes about the command line
> (names, flags, exit codes, the endpoint) is an Inference **for the owning area agent to
> confirm**: the owner decided the mechanism, not its details. By
> [design.md §4.4](../design.md#44-process-model) area A03 owns the mechanism, with A09,
> A10, A11 and A24; this document fills in that section's outline and does not
> override it. Go modules named in
> section 5.3 were checked on 2026-11-18 with `go list -m -json <module>@latest` and by
> reading the licence file in the module cache. Nothing was built, run or measured.

| | |
|---|---|
| **Library ID** | `DH-LIB-PTCGUEST` |
| **Upstream unit** | none of its own; it is the Go answer to `UP-DEP-node:vm`, `UP-DEP-acorn`, `UP-DEP-acorn-walk` and `UP-DEP-birpc`, whose rows link here |
| **Upstream version** | Node `>=24.9.0` is the runtime upstream requires (`package.json:9`) |
| **Kind** | runtime; in upstream it is the Node host itself, started again as a child process |
| **Used by** | `ptc-runtime`, `core` (`tool-executor-ptc`), `workflow`, `experimental` (`cua`) |
| **Go choice** | **own code**: a `dsh tool` command (part of the `dsh` binary) and a host endpoint behind the guarded tool pipeline. No JavaScript engine, no interpreter and no new module |
| **Related areas** | A03 (tool pipeline, tool endpoint and call tokens: `internal/tools`); A11 (what the model is taught, sub-call limits, workflows: `internal/program`); A09 and A10 (shell, terminals, sandbox); A24 (the command line). Documents: [../areas/tools.md](../areas/tools.md), [../areas/ptc-workflow.md](../areas/ptc-workflow.md), [../areas/execution.md](../areas/execution.md), [../areas/sandbox-ssh.md](../areas/sandbox-ssh.md), [../areas/app-cli.md](../areas/app-cli.md) |

## 1. What it is and why upstream needs it

**Programmatic tool calling (PTC)** is a second way for a model to use tools. In the
ordinary ("native") way the model asks for one tool call, waits for the result, and asks
for the next. In PTC the model writes a small **program** that calls tools as functions,
loops over results, filters them and prints only what matters. The harness runs the
program and returns one result. Intermediate values never enter the conversation, which
saves tokens and round trips on work such as "search forty files and count the matches".

Upstream's **guest runtime** is whatever executes that program. The program is the
**guest**: code written by the model, treated as untrusted. The harness is the **host**:
it owns the tools, the policy checks, the session log and the process the guest runs in.
A **binding** is a host function made visible to the guest as an ordinary async function
(for example `tools.bash`); calling it sends a message to the host and waits for the
reply.

Upstream needs no extra component for this because its host is Node: it starts a second
Node process and evaluates the program there with Node's `vm` module. A Go host has no
JavaScript engine.

**What the owner decided.** The Go harness does not get one. Instead:

- The model writes an **ordinary script**: a shell script, or code in any language
  already on the machine. It runs it with the ordinary shell tool, like any command.
- The harness ships a **tool command**, `dsh tool call <name> --args ...`, that such a
  script invokes whenever it wants a tool. The tool command asks the running host to
  make the call and prints the result.
- **Every call made that way goes through the guarded tool pipeline and is logged**,
  exactly as if the model had asked for it natively.
- There is **no embedded interpreter**. The JavaScript guest runtime is not built.

Terms used below. The **outer call** is the model's call to the shell tool that starts
the script. A **sub-call** is one tool call the script makes through the tool command.
The **tool endpoint** is the local address on which the host listens for the tool
command. The **call token** is a secret the host creates for one outer call; it is how
the host knows which session, agent and turn a sub-call belongs to.

Four upstream features touch the guest runtime. The last column is the consequence of
the owner's decision; rows marked Inference are for the owning area to confirm.

| Feature | What the model writes upstream | Shipped state upstream | In the Go design |
|---|---|---|---|
| `run_code` (PTC mode) | A TypeScript program against a generated `tools` object | Opt-in: the executor is `disabled: true` in the base bundle (`packages/bundle/base/cordis.patch.yml:533-535`) and selected by the `ptc` agent preset (`packages/bundle/web-app/presets/ptc.patch.yml:5-9`) | No `run_code` tool. The model runs a script with the shell tool; the script calls `dsh tool call` |
| `workflow` tool | A JavaScript script that starts subagents with `agent()`, `parallel()`, `pipeline()` | On by default: the runtime, the workflow engine and the `workflow` tool are mounted in the base bundle (`packages/bundle/base/cordis.patch.yml:418-427`) | The JavaScript script form cannot be kept. Inference: a workflow is an ordinary script that starts subagents through `dsh tool call`; see `DH-LIB-PTCGUEST-R15` and section 7 |
| `cua_run_code` (computer use, experimental) | A TypeScript program against a retained `cua` object | Experimental (`packages/experimental/cua/src/tool-registration.ts:149-152`) | Not built: the owner made computer use an external MCP provider. That provider's tools are reachable through `dsh tool call` like any other tool |
| Model-written plugins (`cordis-host-runner`) | A cordis plugin evaluated in a `node:vm` realm inside the host | Not a guest-runtime consumer | [DH-ADR-4](../design.md#dh-adr-4) moves these to plugin processes; see [node-builtins-vm-worker-module.md](node-builtins-vm-worker-module.md) |

## 2. How upstream uses it

This section is unchanged evidence. It is what the tool command has to stand in for.

### 2.1 Use sites

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/ptc-runtime/ptc-runtime/src/types.ts` (rendered in `docs/subsystems/ptc-runtime.md:18-54`, `:87-108`, `:248-253`) | The seam: one request in (program, bindings, working directory, deadline, sandbox policy, cancel signal), one result out (value, ordered output, truncation flag, failure, sandbox facts) | `PtcRuntime.resolve`, `run`, `release`, `registerEnvironmentInitializer` |
| `packages/ptc-runtime/ptc-runtime-node/src/worker.ts:302-316` | Start the child: heap flag, confine the command line through the sandbox seam, spawn with a separate control pipe | `ctx.sandbox.confine`, `ctx.subprocess.spawn` |
| `packages/ptc-runtime/ptc-runtime-node/src/evaluators/*.ts` | Evaluate the program in one of four forms (async function body, Node REPL, `vm` Script, scoped REPL) | `node:vm` `compileFunction`, `Script`, `createContext` |
| `packages/ptc-runtime/ptc-runtime-node/src/rpc.ts:33-57`, `protocol.ts:46-93` | The control channel between host and child | `birpc`, see [birpc.md](birpc.md) |
| `packages/core/tool-executor-ptc/src/ptc.ts:217-796` | The `run_code` tool: generated SDK, sub-call scheduling, logging, limits | the seam above |
| `packages/workflow/workflow-ptc/src/runtime.ts:67-99` | Run a workflow script inside the same child, in a second `vm` context with five helper functions | `vm.Script`, `vm.createContext`, `runInContext({ timeout })` |
| `packages/experimental/cua/src/tool-registration.ts:144-152` | A private program tool with one binding and an initializer that installs a global | `createRunCodeTool`, `registerEnvironmentInitializer` |

### 2.2 What the guest program can do

**Language.** The program is TypeScript from which types are erased without being
checked ("erasable syntax only", no `enum` or namespaces;
`packages/ptc-runtime/ptc-runtime-node/src/execution.ts:157`). Types are removed on the
host with Node's `stripTypeScriptTypes` (`execution.ts:131-136`). In the default
`per-call` mode the program is the body of an async function, so `await` and `return`
work at the top level (`execution.ts:154-162`). The model is told to use `async`/`await`,
`try`/`catch` and `Promise.all` (`packages/core/tool-executor-ptc/README.md:119-122`).
Dynamic `import()` loads modules; static `import` is not accepted in the script forms
(`packages/ptc-runtime/ptc-runtime-node/README.md:91`).

**Host API the guest sees.**

| Global | Behaviour | Evidence |
|---|---|---|
| `tools.<name>(args)` | One async function per visible tool. Arguments and results are lossless JSON. Names are own properties, so a tool called `__proto__` is safe. | `docs/subsystems/ptc-runtime.md:120`, `:149-161` |
| `ToolCallError` | A failed tool call rejects with this class; `toolName` names the tool. `try`/`catch` continues the program. | `packages/core/tool-executor-ptc/README.md:120`; `docs/subsystems/ptc-runtime.md:130-138` |
| `inspect(...values)` | The only sanctioned output. Renders text, objects and images in order. Passing an unchanged tool result shows the tool's own rendered text. | `packages/ptc-runtime/ptc-runtime-node/README.md:30`, `:73-79` |
| `Image` | A stored image with `path`, `width`, `height`; shown to the model only when inspected. | `packages/core/tool-executor-ptc/README.md:73` |
| `console` | Present and captured, but the model is told not to use it. | `packages/ptc-runtime/ptc-runtime-node/README.md:81` |
| **All of Node** | The filesystem, network, child processes and installed packages, through `await import(...)`. `process.env` starts empty. | `execution.ts:157`; `packages/ptc-runtime/ptc-runtime-node/README.md:89`, `:153` |
| Workflow only: `agent`, `parallel`, `pipeline`, `phase`, `log`, `args` | Start a subagent and await its text or structured result; combinators; progress narration. The model is told the script has "no filesystem, network, timer, or Node.js APIs". | `packages/workflow/tool-workflow/src/index.ts:160-168`; `packages/workflow/workflow-ptc/src/runtime.ts:77-85` |

The last-but-one row is the most important fact in this section. An upstream PTC
program is not only "a script that calls tools": it is a full Node program. Upstream
chose that deliberately and rejected "fresh raw-JavaScript isolates and host tool
callbacks" because removing Node APIs "would change PTC's programming and logging model"
(`.agents/notes/implemented/architecture/2026-09-11-sandboxed-node-ptc-runtime.md:43`).
The consequence is that the Node `vm` context is "not a security boundary"
(`packages/ptc-runtime/ptc-runtime-node/README.md:91`,
`packages/workflow/workflow-ptc/README.md:158`): the only real fence is the
operating-system sandbox applied to the child process
(`2026-09-11-sandboxed-node-ptc-runtime.md:9`, `:41`). The owner's decision keeps that
shape and makes it plain: a script is an ordinary program under the sandbox, with
whatever language and system access the sandbox policy allows.

**How a tool call crosses the boundary.** Host and child exchange newline-delimited JSON
on a pipe that is separate from the program's stdout and stderr
(`packages/ptc-runtime/ptc-runtime-node/README.md:155`). The child calls `invoke(global,
name, args)` and awaits `{ value, inspection? }`; it reports output with `log(text)` and
`image(id)`; the host calls `run(cwd, data)` once per evaluation and may send `revoke()`
and `discardOutput()` (`protocol.ts:46-93`). The host treats every byte from the child
as untrusted: it bounds each message, allows only named methods, checks the binding name
against an allow-list and validates the result envelope (`rpc.ts:43`, `:54-56`, `:62-64`;
`protocol.ts:102-116`). On the host, each sub-call goes through the same policy pipeline
as a native call and the same scheduling rule: calls that are safe to overlap run up to
`maxParallelSubCalls` at once, a mutating call runs alone, and results commit in
submission order (`ptc.ts:342-356`).

**Limits.**

| Limit | Default | Evidence |
|---|---|---|
| Elapsed deadline per program, including waits for tools and approvals | 120 s, ceiling 600 s; `null` (none) for workflows | `packages/ptc-runtime/ptc-runtime-node/src/config.ts:45-46`; `packages/workflow/workflow-ptc/README.md:61` |
| JavaScript heap | 512 MiB (V8 old generation only; native memory and descendants are not counted) | `config.ts:48`; `worker.ts:302`; README `:248` |
| Captured output plus completion value | 64 MiB; past it, later output is dropped and `outputTruncated` is set, and the program keeps running | `config.ts:47`; README `:85` |
| One control message, and queued control bytes | 128 MiB | `config.ts:49` |
| Simultaneous binding calls | 128 | `config.ts:50` |
| Tool-call attempts per program | 1024 per minute of the resolved deadline, as one total allowance (2048 at the default); the next attempt ends the program | `packages/core/tool-executor-ptc/src/config.ts:13-16`; `ptc.ts:470-476`, `:685-686` |
| Overlapping sub-calls | 10 | `packages/core/tool-executor-ptc/src/config.ts:15` |
| Workflow: subagents per run, items per combinator call, first synchronous slice | 1000, 4096, 5 s | `packages/workflow/workflow-ptc/README.md:43-45` |

**Cancellation.** Timeout and cancel follow one sequence: the host stops accepting
binding calls and results, sends `revoke()`, and waits `stopGraceMs` (3 s) for the
evaluation to settle; if it does not, the host terminates the whole managed process
range (README `:133`, `:157`). A program stuck in a synchronous loop cannot hear
`revoke()` and is always killed (README `:157`). The eight failure kinds are reported as
a field of the result and never thrown: `exception`, `timeout`, `abort`, `worker-exit`,
`invalid-output`, `output-limit`, `protocol`, `sandbox-unavailable`
(`docs/subsystems/ptc-runtime.md:248-253`). A program is never replayed automatically
(`packages/core/tool-executor-ptc/README.md:79`).

**What is logged.** The outer call (`tool/call` with the program source) and its result
(`tool/result`) are ordinary surface events. Each sub-call adds two log-only events:
`tool/ptc-dispatch-start` before policy runs, and `tool/ptc-dispatch` with the arguments,
the rendered result, the error and metadata (`ptc.ts:547-553`, `:520-533`). Sub-call ids
are `<parent>:ptc:<n>` (`ptc.ts:478`). Every settle event is appended before the outer
result (`ptc.ts:453-455`). Tool output that cannot be handed to the program as a value
(an image without a readable path) is queued as extra model context, with its source
recorded (`ptc.ts:583-588`). So "model-visible means logged" holds for everything that
passes through a binding. **It does not hold for direct Node effects**: a file the
program writes with `node:fs`, or a request it makes with `fetch`, leaves no event. Only
the program text records that it happened.

### 2.3 What is checked before a program runs

**None of upstream's static-analysis libraries performs a safety check on a
model-written program.**

| Library | Actual job | Evidence |
|---|---|---|
| `acorn`, `acorn-walk` | Rewrite the program so that Node's `vm` can run it: lower top-level `await` (a port of Node's REPL transform), and scan top-level declarations so a retained "scoped REPL" can copy bindings between calls. Also produce syntax errors with a line and a caret. | `packages/ptc-runtime/ptc-runtime-node/src/evaluators/vm-script-await.ts:193-290`; `scoped-repl.ts:55-120`. See [acorn.md](acorn.md) |
| `eslint-scope`, `eslint-visitor-keys` | Not PTC. They rewrite ES modules into CommonJS for the experimental browser Web Worker runtime. | `packages/experimental/webworker-runtime/src/compile/transform.ts:26-28`, `:619`. See [eslint-scope.md](eslint-scope.md) |
| `@eslint-community/regexpp` | Not PTC. It compares two readings of a regular expression in the config-schema exporter. | `packages/boot/app-boot/src/config-schema/pattern.ts:46-62`. See [eslint-communityregexpp.md](eslint-communityregexpp.md) |
| `@babel/code-frame` | Not PTC. It prints a source excerpt for a failed hot-reload build. | `packages/boot/hmr/src/error.ts:30-35`. See [babelcode-frame.md](babelcode-frame.md) |

The only checks made before a program runs are: the workflow script is parsed once on
the host so a syntax error is reported before a run exists
(`packages/workflow/workflow-ptc/src/index.ts:63`); a program containing the reserved
prefix `__$binding_` or starting with `#!` is refused (`scoped-repl.ts:57-59`); and
`run_code` arguments are validated (`ptc.ts:298-313`). Safety comes from the process
sandbox and the tool pipeline, not from inspecting the source. The Go design keeps
exactly those two sources of safety and has no source inspection at all.

### 2.4 How the model is told to write programs (the prompt contract)

The contract has four parts, and all four name the language.

1. **The tool schema.** `run_code` takes `code` and `description`, plus `timeoutMs` and
   `sandbox_permissions` with `justification` when the runtime supports them
   (`ptc.ts:53-71`, `:105-122`). The description starts "Execute a TypeScript program
   against the available tools".
2. **Runtime-owned execution instructions**, appended to the schema. They state the
   program form, that Node APIs come from `await import(...)`, that `process.env` is
   empty, that output goes through `inspect`, and style rules that exist to prevent
   escaping mistakes ("Write file contents as an array of quoted strings")
   (`execution.ts:149-157`). The retained scoped-REPL form needs about twenty sentences
   of scoping rules (`execution.ts:143`).
3. **A system-prompt section** "Writing code for run_code" with the calling rules, and
   one complete example call (`packages/core/tool-executor-ptc/README.md:110-125`).
4. **A generated TypeScript SDK**: one `declare` per visible tool with its parameter and
   result types, produced from the tool schemas
   (`packages/core/tool-executor-ptc/src/ts-types.ts:1-7`).

The executor refuses any runtime whose `language` is not `typescript`, at prompt
assembly and at execution, and never falls back to native tools (`ptc.ts:91-96`).
Upstream once supported Python programs as well and removed them to keep one language
(`.agents/notes/implemented/simplification/2026-11-12-typescript-only-ptc.md:9-13`).
Inference: under the owner's decision none of the four parts survives as written. The
language is the model's choice, so the contract shrinks to "how to call the tool
command" plus the list of tools, and all of it must be re-validated against model
behaviour ([DH-ADR-PTCGUEST-5](#dh-adr-ptcguest-5)).

## 3. Tech debt upstream carries

No `UP-DEBT` unit is assigned to this document; the limitation bullets below are units
owned by the programmatic-tool-calling area. They are listed because each one is caused
by running programs in a JavaScript engine. Search: the "Known Limitations and Deferred
Work" sections of `packages/ptc-runtime/ptc-runtime-node/README.md:240-261`,
`packages/core/tool-executor-ptc/README.md:155-166` and
`packages/workflow/workflow-ptc/README.md:148-159`; `patches/` (no patch for `acorn`,
`acorn-walk` or `birpc`); `pnpm-workspace.yaml` (no pin for them).

| Debt | What it is | Evidence (`path:line`) | What happens to it in Go |
|---|---|---|---|
| Four program forms | Async function body, Node REPL, `vm` Script and scoped REPL, each with its own semantics, guidance text and limitations | README `:52-54`, `:251-253` | Gone: there is no program form. A script is whatever its interpreter accepts ([DH-ADR-PTCGUEST-4](#dh-adr-ptcguest-4)) |
| Source rewriting | Top-level `let`, `const` and `class` lose their normal semantics after the `await` transform; stack locations refer to rewritten source | README `:251` | Gone: the host never parses or rewrites script text (DH-ADR-PTCGUEST-4) |
| Two realms | Values from bindings fail `instanceof Array` and `instanceof Error` inside a `vm` context | README `:254`; `packages/workflow/workflow-ptc/README.md:159` | Not applicable: a result crosses to another process as JSON text |
| Memory cap is partial | Only the V8 old generation is capped; native allocations and descendants are not | README `:248` | Not applicable as stated: there is no engine heap. A script is an ordinary sandboxed command and gets whatever limits the shell tool applies to commands ([DH-ADR-PTCGUEST-2](#dh-adr-ptcguest-2)) |
| Unlogged effects | Direct Node access bypasses the session log | §2.2 | **Changed, not removed.** A script is a shell command: its text is logged, its effects are confined by the sandbox, and its direct effects are not logged one by one, exactly as for any shell command. What is new is that every *tool* call it makes is logged and policy-checked ([DH-ADR-PTCGUEST-3](#dh-adr-ptcguest-3)). There is no longer a special runtime in which effects go unseen |
| Bootstrap must match the host | A remote deployment must install the matching bootstrap script and protocol revision by hand | README `:260` | The tool command is the `dsh` binary. Where the script runs on the host's machine nothing has to match. Where it runs elsewhere (a remote host, a container) the command and a route to the endpoint are both missing: section 7 |

## 4. Requirements for the Go implementation

These are the requirements the tool command and its host endpoint must meet. They
replace the requirements a guest runtime had. `DH-LIB-PTCGUEST-R10` (state kept inside an
interpreter between calls) is retired: there is no interpreter to keep state in, and
anything a script wants to keep it writes to a file. Each requirement states behaviour;
names and numbers are proposals (section 6).

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-PTCGUEST-R1` | Every run of the tool command ends in exactly one outcome from a closed set: success, tool failure, bad usage, refused before execution, denied, unavailable, token rejected, limit reached, cancelled, protocol failure. The outcome is the exit code; a failure also writes one structured error object to standard error. | `docs/subsystems/ptc-runtime.md:248-253` (failure is a closed set of kinds, never an exception) |
| `DH-LIB-PTCGUEST-R2` | A script reaches the host only through the tool command. A tool is named by an exact string that the host looks up among the tools visible to the calling agent; the name is data and is never read as a path, a flag or an expression. Arguments and results are JSON. | `docs/subsystems/ptc-runtime.md:120` |
| `DH-LIB-PTCGUEST-R3` | A failed tool call is told apart from every other failure by its exit code, and its error object names the tool, so a script can test for it and continue. | `packages/core/tool-executor-ptc/README.md:120` |
| `DH-LIB-PTCGUEST-R4` | Every sub-call runs through the guarded tool pipeline exactly as a native call does (schema check, policy, guards, approval, execution, result shaping) and under the native scheduling rule: calls overlap only when safe, a mutating call runs alone. Sub-calls are admitted in the order they arrive. | `ptc.ts:342-356`; [DH-O-6](../design.md#dh-o-6) |
| `DH-LIB-PTCGUEST-R5` | Every sub-call is logged twice (started, settled) with the outer call as its parent, inside the outer call's bracket. A sub-call still running when the outer call ends is cancelled and settled before the outer result is appended. A sub-call's result reaches the model only as part of the script's own output, which is the outer call's logged result. | `ptc.ts:453-455`, `:520-553`; [DH-O-4](../design.md#dh-o-4) |
| `DH-LIB-PTCGUEST-R6` | Standard output carries the result and nothing else: by default exactly one JSON document and a newline. Diagnostics go to standard error. The result is the same bounded result the native path produces; when result shaping shortened it, the output says so. Output is never cut in the middle of a JSON document. | README `:85`; `packages/ptc-runtime/ptc-runtime-node/README.md:30` |
| `DH-LIB-PTCGUEST-R7` | For one outer call these are each bounded: the number of sub-calls, the number waiting at once, the bytes of one argument document, and elapsed time (the outer call's deadline covers the script and every sub-call). A refused call says which bound it hit. | §2.2 limits |
| `DH-LIB-PTCGUEST-R8` | On cancel or deadline the host stops admitting sub-calls for the call token, cancels the sub-calls in flight through their own contexts, and the shell tool's stop sequence ends the script's process tree. A tool command whose caller has gone away has its sub-call cancelled. Each is logged with its cause. | README `:157` |
| `DH-LIB-PTCGUEST-R9` | The tool command works only inside a command the harness started for an agent. The spawn helper adds exactly two variables to that command's environment, the endpoint and the call token, and the sandbox policy lets the command reach that endpoint and nothing else new. Anywhere else the tool command exits "unavailable" without contacting anything. A command started by a sub-call gets no token, so nesting stops at one level. | `worker.ts:306`; README `:153`; [DH-O-7](../design.md#dh-o-7) |
| `DH-LIB-PTCGUEST-R11` | Every byte that arrives on the tool endpoint is untrusted. The token is checked before anything else. An oversized, malformed or unknown message is a protocol failure for that connection only, and never crashes, blocks or slows the host. | `rpc.ts:43-64`; `protocol.ts:102-116` |
| `DH-LIB-PTCGUEST-R12` | An argument document that is not valid JSON, or is not a JSON object, is refused by the tool command before anything is sent or logged, with line, column and the offending line. Arguments that are valid JSON but do not match the tool's schema are refused by the pipeline, as for a native call. | `vm-script-await.ts:214-217` (a positioned error before any effect); `ptc.ts:298-313` |
| `DH-LIB-PTCGUEST-R13` | The model is told how to call tools from scripts by text the harness generates: the tool command's usage, the list of callable tools and each tool's schema. When script calling is off for an agent, no token is issued and that text is absent; the two never disagree. | `ptc.ts:91-96`; `execution.ts:149-157` |
| `DH-LIB-PTCGUEST-R14` | Nothing is replayed automatically. After a denial, a crash or a restart neither the script nor any sub-call is run again; a sub-call with a start event and no settle event is closed on resume as "outcome unknown". | `packages/core/tool-executor-ptc/README.md:79` |
| `DH-LIB-PTCGUEST-R15` | There is no second runtime and no private set of functions. What upstream gave programs as helper functions (the workflow helpers `agent`, `parallel`, `pipeline`, `phase`, `log`; the computer-use `cua` object) is in Go either an ordinary tool reached through the same command, or something the script does in its own language. A tool can be marked as not callable from scripts. | `packages/workflow/workflow-ptc/src/runtime.ts:77-85`; `packages/experimental/cua/src/tool-registration.ts:144-152` |
| `DH-LIB-PTCGUEST-R16` | A call token identifies, on the host, exactly one outer call and through it one session, agent and turn. The tool command sends no session, agent or turn of its own and none is accepted. A token is unguessable, is valid from the outer call's start until it settles, and is refused the moment the outer call ends or the turn is cancelled. Its value is never written to the log or to process arguments, and the host masks it in captured command output. | `ptc.ts:478` (sub-calls are numbered under their parent); [DH-O-3](../design.md#dh-o-3) |
| `DH-LIB-PTCGUEST-R17` | A script can ask which tools it may call and what arguments each takes. The answer lists exactly the tools visible to the calling agent that are callable from scripts. | `packages/core/tool-executor-ptc/src/ts-types.ts:1-7` (the generated SDK it replaces) |

## 5. Go choice

**Decision.** Own code: a `tool` command group in the `dsh` binary and one host endpoint
behind the tool pipeline. The repository owner decided this on 2026-11-18 (OD-2): "the
harness ships an external Go command; the model writes ordinary shell scripts, or code in
any language already on the machine, that invoke it; every call passes through the
guarded tool pipeline and is logged; there is no embedded interpreter." Five decision
records carry it.

### 5.1 Decision records

<a id="dh-adr-ptcguest-1"></a>
#### DH-ADR-PTCGUEST-1 No embedded interpreter: scripts call tools through the `dsh tool` command

- **Decision.** The harness contains no JavaScript engine and no other interpreter for
  model-written code. Tools are exposed to scripts by an external command line that is
  part of the `dsh` binary. Verdict on upstream's guest runtime: **drop**; on its purpose
  (many tool calls in one model step, with only the wanted output returned): **keep**.
- **Why it is needed.** Programmatic tool calling is a shipped preset and workflows are
  on by default (§1). A Go host has no engine. An external command gives scripts the
  tools without one.
- **Why this and not an engine.** One binary, no cgo and no large dependency
  ([DH-O-14](../design.md#dh-o-14)). The model may use any language on the machine,
  which models already do with the shell tool. No interpreter means no interpreter
  defects, no engine-specific prompt contract and no source rewriting.
- **Tradeoff.**
  - A script pays a process start per sub-call, where upstream's program pays a message
    on a pipe. Work that makes thousands of tiny calls is slower.
  - Results arrive as JSON text. A shell script needs something to pick values out of
    it; the tool command offers a simple field selector (section 6) so that no other
    program is required.
  - Upstream's prompts, examples and recorded PTC sessions cannot be reused
    ([DH-ADR-2](../design.md#dh-adr-2)); how well models write such scripts must be
    measured by replay.
  - The workflow tool's JavaScript script form does not survive (§1, section 7).
- **Constraint.** The core binary is pure Go, cross-compiled (OD-9).
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-6](../design.md#dh-o-6),
  [DH-O-7](../design.md#dh-o-7), [DH-O-9](../design.md#dh-o-9),
  [DH-O-14](../design.md#dh-o-14).
- **Verified by.** The tests of [§8](#8-verification), all of which drive the real
  `dsh tool` command against a host with a fake tool pipeline.

<a id="dh-adr-ptcguest-2"></a>
#### DH-ADR-PTCGUEST-2 A script is an ordinary sandboxed command, and the tool command finds the host through that command's environment

- **Decision.** The harness adds no program-runtime process. A script runs as the shell
  tool runs any command: started by the one spawn helper, under the resolved sandbox
  policy, with an explicitly built environment
  ([DH-ADR-SECURITY-3](../upstream/security.md#dh-adr-security-3),
  [DH-ADR-SECURITY-6](../upstream/security.md#dh-adr-security-6)). The helper adds two
  variables: where the host listens, and the call token. The tool command reads them,
  connects, and makes its request. Verdict on upstream's "programs run in a child
  process under the sandbox": **keep**; on its dedicated control pipe: **change** to a
  local endpoint, because the tool command is a grandchild of the host and can be
  started any number of times.
- **Why.** A script must be killable and confined without any help from itself, which a
  child process under the sandbox already is (`DH-LIB-PTCGUEST-R7`, `-R8`). Passing the
  endpoint and token in the environment means a script needs no setup and cannot point
  the tool command at another session (`-R16`).
- **Tradeoff.** The sandbox must let a confined command reach one local endpoint even
  when its network policy is "none". That is a new, narrow permission in every sandbox
  backend and must be proven on each (section 7). The endpoint is one more listener the
  host owns.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14).
- **Verified by.** `DH-T-PTCGUEST-4`, `DH-T-PTCGUEST-5`.

<a id="dh-adr-ptcguest-3"></a>
#### DH-ADR-PTCGUEST-3 A call token carries exactly the calling agent's right to propose tool calls; every sub-call is checked and logged like a native call

- **Decision.** A sub-call is treated as a tool call proposed by the model of the agent
  that made the outer call, in that agent's turn. It sees the tools that agent sees, is
  checked by the same policy and guards, asks for approval the same way, and is logged.
  The token grants nothing else: it cannot name another session or agent, cannot skip a
  check, and is useless once the outer call has ended. Approvals follow the owner's rule
  for all calls (OD-10): one approval covers one call, unless the user grants a named
  scope for the session. Verdict on upstream's "sub-calls go through the same pipeline":
  **keep**.
- **Why.** The script is model-written and untrusted, and it can read its own
  environment. So the token must be worth no more than what the model can already do by
  asking natively. That makes leaking it harmless and makes "every call passes through
  the guarded pipeline and is logged" true by construction (`DH-LIB-PTCGUEST-R4`,
  `-R5`).
- **Tradeoff.** A script cannot do anything through the tool command that the model
  could not do natively, including when that would be convenient. A sub-call that needs
  approval blocks the script until a person answers.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-6](../design.md#dh-o-6),
  [DH-O-7](../design.md#dh-o-7).
- **Verified by.** `DH-T-PTCGUEST-3`, `DH-T-PTCGUEST-6`.

<a id="dh-adr-ptcguest-4"></a>
#### DH-ADR-PTCGUEST-4 The harness neither parses nor rewrites script text; there is no program form and no interpreter state

- **Decision.** The host passes the script to the shell tool as the model wrote it. It
  does not parse it, check its syntax, strip types from it or transform it. A syntax
  error is reported by the interpreter that runs the script, in that interpreter's
  words. Nothing is kept between two scripts except what a script writes to files.
  Verdict on upstream's four program forms, its top-level-`await` and scoped-REPL
  transforms and its retained state: **drop**.
- **Why.** The transforms exist because `node:vm` scripts cannot use top-level `await`
  and because V8 rejects a redeclared `const` in a retained scope
  (`vm-script-await.ts:1-6`; README `:121`). They cost a JavaScript parser on the host,
  relaxed language semantics (README `:251-253`) and long guidance the model must obey
  (`execution.ts:143`). With no engine there is nothing to adapt the text to. The one
  text the tool command does parse is its own argument document, and it reports a
  mistake there precisely (`DH-LIB-PTCGUEST-R12`).
- **Tradeoff.** Error messages differ by interpreter and the harness cannot improve
  them. A model that wants notebook-style state between steps must use files.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-PTCGUEST-8`.

<a id="dh-adr-ptcguest-5"></a>
#### DH-ADR-PTCGUEST-5 The script-calling prompt contract is generated by the harness and checked by a golden file

- **Decision.** What the model is told about calling tools from scripts is generated:
  the tool command's usage text, a short prompt section with the calling rules and one
  worked example, and the tool list and schemas that `dsh tool list` and
  `dsh tool describe` print. The generated text for a fixed tool set is a golden file.
  The section is present exactly when script calling is on for the agent
  (`DH-LIB-PTCGUEST-R13`). Verdict on upstream's provider-produced contract: **keep the
  rule, replace the content** (§2.4).
- **Why.** The text is model-visible, so it must be reproducible from the log
  ([DH-O-4](../design.md#dh-o-4)), and a change to the command must show up as a
  reviewed diff of what the model is told.
- **Tradeoff.** Wording changes need a golden-file update and a replay.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-10](../design.md#dh-o-10).
- **Verified by.** `DH-T-PTCGUEST-9`.

### 5.2 What the model writes

Tool names are illustrative. The example counts files that mention a word.

```sh
n=0
for f in $(dsh tool call glob --args '{"pattern":"src/**/*.go"}' --pick /paths --lines); do
  if dsh tool call grep --arg path="$f" --arg pattern=TODO --pick /count | grep -qv '^0$'; then
    n=$((n+1))
  fi
done
echo "$n files mention TODO"
```

The model sees one line. The log holds the script, one start and one settle event per
`glob` and `grep` call, and the outer result. The flags are the proposals of section 6.

### 5.3 Options considered and not taken

The earlier revision compared ways to run model-written programs inside the harness. The
owner took none of them. The comparison is kept, shortened, because it is the evidence
for what was given up.

| Option | What it was | Why it was not taken |
|---|---|---|
| A. JavaScript engine in pure Go, in a sandboxed child `dsh` process (the earlier recommendation) | The upstream program form (`await tools.x(...)`, `Promise.all`, `try`/`catch`, `inspect`) without Node APIs, on `modernc.org/quickjs` or `goja`/`sobek` | Overruled by the owner: no embedded interpreter. It would have kept upstream's language and made every program effect a logged call, at the price of a sizeable engine and, for TypeScript, a type stripper |
| B. A JavaScript engine compiled to WebAssembly on `wazero` | Same language as A with memory isolation | Same reason; its ready-made Go wrapper was also immature (v0.0.6) |
| C. An external Node or Deno child process | Exactly upstream's contract, Node APIs included | Requires or bundles a second runtime; keeps a JavaScript bootstrap in a Go repository |
| D. A different embedded language (Starlark, Lua) | A hermetic interpreter in the binary | An interpreter all the same; all four parts of the prompt contract change and model familiarity is weaker |
| E. No programmatic calling at all | Native tool calls only | Loses the token and round-trip savings; the owner's choice keeps them without an engine |
| A JavaScript engine through cgo (`rogchap.com/v8go`, `github.com/buke/quickjs-go`) | V8 or QuickJS linked into the binary | cgo, which the core binary may not use |

Modules examined for those options, all checked on 2026-11-18 ("cgo" by searching the
module source for `import "C"`). None is a dependency of the Go design.

| Module | Version (date) | Licence | cgo |
|---|---|---|---|
| `modernc.org/quickjs` | v0.27.0 (2026-10-24) | BSD-3-Clause, plus MIT for QuickJS | None |
| `github.com/dop251/goja` | v0.0.0-20261118204651-8dfece3d1e1d (2026-11-18) | MIT | None |
| `github.com/grafana/sobek` | v0.0.0-20261116133901-d070e0f1f200 (2026-11-16) | MIT | None |
| `github.com/dop251/goja_nodejs` | v0.0.0-20260918173711-b481721df8a2 (2026-09-18) | MIT | None |
| `github.com/tetratelabs/wazero` | v1.12.0 (2026-05-28) | Apache-2.0 | None in the library |
| `github.com/fastschema/qjs` | v0.0.6 (2025-10-28) | MIT | None |
| `go.starlark.net` | v0.0.0-20261021141710-4f4267fb140d (2026-10-21) | BSD-3-Clause | None |
| `github.com/yuin/gopher-lua` | v1.1.2 (2026-04-01) | MIT | None |
| `github.com/evanw/esbuild` (type stripping for A and B) | v0.28.2 (2026-08-08) | MIT | None |
| `rogchap.com/v8go` | v0.9.0 (2023-03-20) | BSD-3-Clause | **Yes** |
| `github.com/buke/quickjs-go` | v0.7.8 (2026-11-06) | MIT | **Yes** |
| `github.com/traefik/yaegi` | v0.16.3 (2026-11-05) | Apache-2.0 | None |

## 6. Design notes

Everything in this section is a proposal. Each paragraph opens with "Inference" because
the owner fixed the mechanism and gave one example (`dsh tool call <name> --args ...`);
the owning area agent confirms or changes the rest.

**The commands.** Inference: three sub-commands are enough.

| Command | What it does | Requirement |
|---|---|---|
| `dsh tool call <name>` | Make one sub-call and print its result | `-R1` to `-R8`, `-R12` |
| `dsh tool list` | Print the names and one-line descriptions of the tools this script may call | `-R17` |
| `dsh tool describe <name>` | Print one tool's argument schema as JSON Schema | `-R17` |

**How a script names a tool and passes arguments.** Inference: the tool name is the
first positional argument and is compared as a whole string, after `--` if it starts
with a dash. Arguments are one JSON object. It can be given in several ways, because
quoting JSON inside a shell string is where models make mistakes (upstream's
instructions carry style rules for exactly that: `execution.ts:149-157`).

| Flag | Source of the argument object |
|---|---|
| `--args '<json>'` | The flag's value |
| `--args-file <path>` | A file the script wrote |
| `--args -` | Standard input |
| `--arg <field>=<value>`, repeatable | Sets one top-level field to the value as a JSON **string**, with no quoting by the script. It is applied on top of the object from the flags above, so a file name with a quote or a space in it cannot break the document |
| none | An empty object |

**How results come back.** Inference: standard output only, in one of three forms.

| Flag | Standard output |
|---|---|
| default | The tool's result value as one JSON document and a newline |
| `--text` | The text the model would have seen for a native call (upstream's "tool's own rendered text": `packages/ptc-runtime/ptc-runtime-node/README.md:73-79`) |
| `--envelope` | One JSON object: sub-call id, tool name, value, rendered text, whether the result was shortened, and references to stored images |
| `--pick <json-pointer>` | With the default form: only the value at that JSON Pointer; add `--lines` to print a list one item per line, and a string is printed without quotes |

`--pick` exists so that a shell script needs no other program to read a field. It is a
fixed path, not an expression language.

**How errors come back.** Inference: one exit code per outcome of `DH-LIB-PTCGUEST-R1`,
and on failure one line of JSON on standard error:
`{"error":{"code":"...","tool":"...","message":"...","limit":"...","call":"..."}}`.
[design.md §4.4](../design.md#44-process-model) says the error is in the design's
`<domain>/<reason>` form; the "Kind" column below is the reason, and A03 names the
domain. Numbers 126 and above are avoided because shells use them.

| Exit | Kind | Meaning | Did a sub-call start (and get logged)? |
|---|---|---|---|
| 0 | none | The tool returned a result | yes |
| 1 | `tool-error` | The tool ran and reported a failure | yes |
| 2 | `usage` | Bad flags, or the argument document is not a JSON object | no |
| 3 | `refused` | Unknown or not-callable tool, or arguments that do not match the schema | yes, as a refused call |
| 4 | `denied` | Policy, a guard or the user said no; or nobody could be asked | yes |
| 5 | `unavailable` | No endpoint and token in the environment, or the host cannot be reached | no |
| 6 | `token` | The token is unknown, expired or revoked (the outer call has ended) | no |
| 7 | `limit` | A bound of `-R7` was hit; the error names it | no |
| 8 | `cancelled` | The turn was cancelled or the outer deadline passed while the call waited or ran | yes, if it had started |
| 9 | `protocol` | The host's answer was not understood, or an internal error | unknown to the caller |

**How the command finds the host and is tied to session, turn and agent.** Inference:

1. When the shell tool starts a command for an agent whose configuration allows script
   calling, the host creates a random call token and records it, in memory only, against
   the outer call (and so against its session, agent, turn and step).
2. The spawn helper sets two variables in the command's environment, for example
   `DSH_TOOL_ENDPOINT` and `DSH_TOOL_TOKEN`. Every process the script starts inherits
   them.
3. The endpoint is local to the machine and belongs to this host process, so with
   several hosts sharing one harness home (OD-6) a script always reaches the host that
   holds its session's lease. The design proposes a Unix domain socket in a private
   runtime directory, or a named pipe on Windows; neither is verified (section 7).
4. `dsh tool call` opens a connection, sends one request (wire revision, token, tool
   name, arguments) on the harness's line-delimited JSON codec ([birpc.md](birpc.md)),
   and reads one reply.
5. The host looks the token up, refuses it if the outer call is over, and hands the call
   to the tool pipeline as a call by that agent in that turn, with the outer call as
   parent. Sub-call ids are numbered under the outer call.
6. When the outer call settles, for any reason, the token is deleted.

The script passes no session, agent or turn anywhere, and there is no flag for them.

**Limits and cancellation.** Inference: defaults follow upstream where it has one (§2.2).

| Bound | Proposed default | What happens at the bound |
|---|---|---|
| Sub-calls per outer call | 1024 per minute of the outer call's deadline, as one total | The next call exits `limit`; the script continues |
| Sub-calls waiting or running at once | 128 | The next call exits `limit` |
| Sub-calls running together | 10, and a mutating call alone (the native rule) | Others wait their turn |
| One argument document | the tool pipeline's own argument bound | Exit `limit` |
| Elapsed time | the outer call's deadline | The outer call is stopped as below |

Upstream ends the whole program at the sub-call allowance (`ptc.ts:685-686`). Inference:
with a script the natural unit to refuse is the one call, and the script decides what to
do; the outer deadline still ends a script that loops. Whether time spent waiting for an
approval counts against the outer deadline, as it does upstream
(`packages/ptc-runtime/ptc-runtime-node/src/config.ts:45-46`), is for the owning area.

Cancellation (the user stops the turn, or the outer deadline passes): the host deletes
the token so no new sub-call is admitted, cancels sub-calls in flight through their
contexts and logs each as settled with the cause, and the shell tool's stop sequence
(stop request, grace period, kill of the process tree) ends the script and any tool
command still waiting. If only the tool command dies, for example the script killed it,
the host sees the connection close and cancels that one sub-call.

**What is logged.** Inference, mirroring upstream's events (§2.2).

| Event | When | Holds |
|---|---|---|
| The outer `tool/call` | The model calls the shell tool | The command or script text, unchanged |
| Sub-call started | Before policy runs for a sub-call | Sub-call id (`<outer>:sub:<n>`), tool name, arguments |
| Sub-call settled | When it finishes, is refused, is denied or is cancelled | Rendered result or error, outcome kind, metadata |
| Approval request and answer | As for a native call | Attributed to the sub-call and its outer call |
| The outer `tool/result` | The script exits or is stopped | Its output, exit status, and whether any sub-call was cut short |

Not logged: the token's value (masked in captured output), and a call the tool command
refused locally (`usage`, `unavailable`), which never reached the host. `dsh tool list`
and `describe` change nothing and return what the prompt already says; whether they
leave an event is for the owning area.

**Ownership.**

| Thing | Owner | Released |
|---|---|---|
| The tool endpoint (listener and its socket file) | the component that provides the tool pipeline's script entry, through its fiber | on unload; the socket file is removed; a stale file from a crashed host is replaced at start |
| A call token | the outer call | when the outer call settles, or the turn is cancelled |
| A sub-call | the outer call; the tool that runs it owns its own resources as for a native call | settled before the outer result |
| The script's processes | the shell tool's run, as for any command | the shell tool's stop sequence |

A script has *access* to the endpoint and to the tools its agent may call. It owns
nothing on the host.

**Failure, retry, cleanup.**

| Situation | What happens | Undone, and what stays uncertain |
|---|---|---|
| Bad flags or malformed arguments | Exit `usage` with the position; nothing was sent | Nothing happened |
| The tool fails | Exit `tool-error`; logged as a settled sub-call with its error | The tool's own effects, as for a native call |
| Policy or the user refuses | Exit `denied`; logged | Nothing ran |
| The script ignores a failure and goes on | Allowed: the script is the model's program. Every later sub-call is still checked and logged | Nothing special |
| Outer deadline or cancel | Token deleted; sub-calls in flight cancelled and logged; process tree stopped | Completed tool effects remain; a cancelled tool reports what it could not finish, as natively |
| The tool command is killed mid-call | The host cancels that sub-call and logs it | **Uncertain:** whether a mutating tool had already taken effect; the settle event says the outcome is unknown when the tool cannot tell |
| The host dies mid-script | The script's processes die with the host (spawn helper). On resume the outer call is closed as "outcome unknown", and so is each sub-call that has a start and no settle | **Uncertain:** those sub-calls. Nothing is replayed (`DH-LIB-PTCGUEST-R14`) |
| The endpoint cannot be created at start | Script calling is off for that host and says so; native tools work | Nothing |
| The sandbox backend cannot grant the endpoint | The script starts and every `dsh tool call` exits `unavailable` | Nothing; the model is told, by the error text, to call tools natively |

## 7. Risks and open questions

For the owning area agent unless another is named.

1. **Every name and number in section 6** is a proposal: the sub-commands, the flags,
   the exit codes, the variable names, the defaults.
2. **Workflows.** Upstream's `workflow` tool takes a JavaScript script and is on by
   default. With no interpreter that script cannot run. Inference: a workflow becomes a
   script that starts subagents through `dsh tool call`, with the shell's own
   background jobs for `parallel`, ordinary sequencing for `pipeline`, and a progress
   tool or standard error for `phase` and `log`. [design.md §4.4](../design.md#44-process-model)
   gives this to A11, which restates workflows "on the tool command line or as a
   declarative form" and records anything it drops. Still to decide: whether a `workflow`
   tool survives as a named entry with its own limits (upstream: no elapsed deadline,
   1000 subagents per run), and how a long workflow relates to a shell command's
   deadline.
3. **The sandbox must admit the endpoint (security area).** A command confined with no
   network must still reach one local socket, and nothing else. Each backend (Linux,
   macOS, Windows) needs a rule for it and a real-environment proof. If a backend cannot
   express it, script calling is unavailable under that backend.
4. **Windows transport (A03, A10).** The design proposes a named pipe. Inference: Go's
   standard library has no named-pipe listener, so that needs a module nobody has
   verified here; a Unix domain socket (which current Windows supports) or a loopback
   port with the token are the alternatives. The token check is the same in all three.
5. **Scripts that do not run on the host's machine.** A command on a remote host or in a
   container has neither the `dsh` binary nor a route to the endpoint. Whether the
   remote-execution areas forward both is theirs to decide; until then `dsh tool call`
   exits `unavailable` there.
6. **Which `dsh` a script finds.** The script calls `dsh` by name. Inference: the spawn
   helper should put the running binary's directory first on the command's search path,
   and the request carries a wire revision so that a different `dsh` is refused with a
   clear message instead of misbehaving.
7. **Images.** Upstream shows an image to the model only when the program passes it to
   `inspect`. A script's output is text. How a script shows the model an image (for
   example by ending with a call to a tool whose result is an image) is undecided.
8. **Model behaviour.** How reliably the target models write correct scripts against
   this command, in which languages, and how often they mis-quote JSON, must be measured
   by replay before script calling is enabled by default anywhere.
9. **Cost per call.** Each sub-call starts a process. Unmeasured. If it matters, the
   remedy inside the owner's decision is a batch form of the command, not an
   interpreter.
10. **Persistent terminals (A09).** A token belongs to one outer call. A command typed
    into a long-lived terminal session has no single outer call; which turn its
    sub-calls belong to, and when its token ends, is open in
    [design.md §4.4](../design.md#44-process-model). Until it is settled a terminal gets
    no token.
11. **A person running the command by hand (A24).** Outside a harness-started command
    there is no token, so `dsh tool call` exits `unavailable`. Whether a person may ever
    use it from their own shell is not decided.
12. **A document not yet in line.** [babelcode-frame.md](babelcode-frame.md) still
    describes `DH-LIB-PTCGUEST-R12` as a syntax error in a model-written program. The
    requirement is now about the argument document; this revision could not edit that
    file.

## 8. Verification

Local tests drive the real `dsh tool` command, started inside a command the harness
spawned, against a host with a fake tool pipeline. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-PTCGUEST-1` | local | `DH-LIB-PTCGUEST-R1`, `-R14` | A table of invocations, one per outcome: each gives the tabled exit code and error kind, and no other code appears. After a simulated host restart, a sub-call with a start and no settle is closed as "outcome unknown" and neither it nor the script is run again. |
| `DH-T-PTCGUEST-2` | local | `DH-LIB-PTCGUEST-R2`, `-R3` | Tools named `__proto__`, `--help` and `../x` are looked up as plain names and reach only their handler; a tool the agent cannot see is refused; a failing tool exits `tool-error` with its name in the error object, and a script that tests for that code continues. |
| `DH-T-PTCGUEST-3` | local | `DH-LIB-PTCGUEST-R4`, `-R5`, `DH-ADR-PTCGUEST-3` | A script starts safe and mutating sub-calls together: the overlap ceiling and the exclusive rule match the native executor; policy, guards and approval run once per sub-call; a denied approval exits `denied`; the log holds a start and a settle event per sub-call, parented to the outer call and all before the outer result. |
| `DH-T-PTCGUEST-4` | local | `DH-LIB-PTCGUEST-R7`, `-R8`, `DH-ADR-PTCGUEST-2` | A script that calls in a loop is refused at the allowance with `limit` and the bound's name. At the outer deadline a sub-call in flight is cancelled and logged, a later invocation with the same token exits `token`, and no process of the script remains. Killing the tool command mid-call cancels that sub-call only. |
| `DH-T-PTCGUEST-5` | local | `DH-LIB-PTCGUEST-R9`, `DH-ADR-PTCGUEST-2` | The environment of a harness-started command differs from the built environment by exactly the two variables. A command started by a sub-call has neither. Run with no variables, the tool command exits `unavailable` and a listener that counts connections sees none. |
| `DH-T-PTCGUEST-6` | local | `DH-LIB-PTCGUEST-R16`, `DH-ADR-PTCGUEST-3` | With agent A's token: a tool only agent B sees is refused; no flag or request field changes the session, agent or turn. A forged, expired or already-deleted token exits `token` and nothing is dispatched or logged as a call. A script that prints its token has the value masked in the outer result and in the log. |
| `DH-T-PTCGUEST-7` | local | `DH-LIB-PTCGUEST-R6`, `-R11` | Standard output of a successful call parses as exactly one JSON document; `--text`, `--envelope` and `--pick` give the tabled forms; a shortened result is marked. On a raw connection, an oversized message, a malformed line, an unknown method and a request with no token each fail that connection only, and a concurrent valid call completes. |
| `DH-T-PTCGUEST-8` | local | `DH-LIB-PTCGUEST-R12`, `DH-ADR-PTCGUEST-4` | A malformed argument document exits `usage` with line, column and the offending line, and the host receives no connection. Arguments that are valid JSON but break the schema exit `refused` and are logged as a refused sub-call. The script text in the outer call's log event equals the submitted text byte for byte. |
| `DH-T-PTCGUEST-9` | golden | `DH-LIB-PTCGUEST-R13`, `-R17`, `DH-ADR-PTCGUEST-5` | For a fixed tool set, the usage text, the prompt section, and the output of `dsh tool list` and `dsh tool describe` match a golden file and list only tools visible to the agent and callable from scripts. With script calling off, no token is issued, the variables are absent and the prompt section is absent. |
| `DH-T-PTCGUEST-10` | local | `DH-LIB-PTCGUEST-R15` | A subagent-start tool and a tool supplied by an external MCP provider are called through the command and pass through the same pipeline and log as any other; a tool marked not callable from scripts is refused; a sub-call that runs a shell command cannot itself make sub-calls. |

Real-environment scenarios belong to the owning area. Three it should declare:

- **A real model writes a script.** *Given* a real model API, an agent with script
  calling on and a workspace with known files. *When* the user asks for a count that
  needs many reads. *Then* the model issues one shell call whose script uses
  `dsh tool call`, the log holds paired sub-call events, the answer is correct, and the
  request sent to the model equals the one rebuilt from the log. *Cleanup:* delete the
  session and workspace; keep the log if the rebuild differs.
- **A confined script reaches the endpoint and nothing else.** *Given* a real
  operating-system sandbox with a workspace-write file policy and no network, on each
  supported platform. *When* a script calls one read tool through `dsh tool call` and
  then tries to open an outside network address and another user's socket. *Then* the
  tool call succeeds and is logged, and both other attempts fail. *Cleanup:* remove the
  workspace; if either attempt succeeded, keep the sandbox profile and the host log.
- **A hostile script.** *Given* the same sandbox and a script that makes one tool call
  and then loops forever. *When* its deadline passes. *Then* the outer result reports the
  deadline, the tool effect is logged, no process from the run remains, a replay of the
  script's token is refused, and the host answers the next request. *Cleanup:* remove
  the workspace; if a process remains, record its id and command line before killing it.

**Scope of the guarantee.** These tests show the stated contracts for the tested build
and inputs. They do not show that a model writes correct scripts, and they say nothing
about what a script does directly under the sandbox, which is the shell tool's contract.

## 9. Units dispositioned by this spec

This document declares `DH-LIB-PTCGUEST`, `DH-ADR-PTCGUEST-1` to `-5` and
`DH-T-PTCGUEST-1` to `-10`. It is not the `spec` of any unit row. The rows that link to
it are `UP-DEP-node:vm` ([node-builtins-vm-worker-module.md](node-builtins-vm-worker-module.md)),
`UP-DEP-acorn` and `UP-DEP-acorn-walk` ([acorn.md](acorn.md)) and `UP-DEP-birpc`
([birpc.md](birpc.md)).
