# Node process and terminal built-ins: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go facts were read on 2026-11-18 from the installed toolchain
> (`go1.27.4`, files under `GOROOT/src`) and from `golang.org/x/sys` v0.50.0 in the
> module cache. Nothing was compiled or run.

| | |
|---|---|
| **Library ID** | `DH-LIB-NODEPROC` (one group, four Node built-in modules) |
| **Upstream units** | `UP-DEP-node:child_process`, `UP-DEP-node:tty`, `UP-DEP-node:readline`, `UP-DEP-node:repl` |
| **Upstream version** | Node built-ins; no version of their own |
| **Kind** | Node built-in |
| **Used by** | `node:child_process`: 125 files outside tests, in every part of the repository. `node:readline`: 3 release scripts. `node:repl`: one runtime package. `node:tty`: one emulation stub |
| **Go choice** | standard library (`os/exec`, `os`, `os/signal`, `context`, `bufio`), with `golang.org/x/sys` for the operating-system objects that own a process tree |
| **Related areas** | subprocess, shell, sandbox, SSH, plugin management; [node-pty.md](node-pty.md) for terminals; [execa.md](execa.md), [cross-spawn.md](cross-spawn.md), [which.md](which.md) for the npm libraries layered on the same built-in |

| Built-in | What it is | Go replacement | Ledger disposition |
|---|---|---|---|
| `node:child_process` | Start another program, connect to its input and output, wait for it, kill it | `os/exec` inside one harness process package | replaced |
| `node:readline` | Read input one line at a time; ask a question on the terminal | `bufio.Scanner` on `os.Stdin` | replaced |
| `node:repl` | An interactive JavaScript evaluator that keeps variables between inputs | A long-lived interpreter **process** started by the process package | replaced |
| `node:tty` | Ask whether a file descriptor is a terminal | Not needed for the one upstream use | not applicable |

## 1. What it is and why upstream needs it

An agent harness spends much of its time running other programs: the shell commands the
model asks for, language servers, `git`, `ssh`, sandbox launchers, package managers,
other agents. In Node all of that goes through `node:child_process`. What the harness
needs from it is less "start a program" than everything around it: stop it **and
everything it started** (its **process tree**), stop it after a deadline, keep only a
bounded amount of its output, give it an environment that does not leak the harness's
credentials, and never let a model-chosen string be interpreted by a shell by accident.

Upstream concentrated those rules in one service, `ctx.subprocess`, with one local
provider (`docs/subsystems/subprocess.md:5`). The provider is the only place meant to
call `node:child_process` for product work. The other three built-ins are marginal:
`node:readline` asks "are you sure?" in three release scripts, `node:repl` evaluates
model-written JavaScript while keeping its variables, and `node:tty` appears only inside
an emulation of Node for browser workers.

Terms used below. An **argument list** (argv) is the program name followed by its
arguments as separate strings. A **shell** is a program (`sh`, `bash`, `cmd.exe`,
PowerShell) that takes one string and decides itself where arguments begin and end, what
`&` or `|` mean, and which variables to substitute. **Quoting** is turning an argument
list into one string in such a way that the receiver recovers exactly the same list.

## 2. How upstream uses it

### `node:child_process`: the uses, classified

Search performed: every `import`, `require` and dynamic `import()` of `node:child_process`
or `child_process` in `.ts`, `.tsx`, `.mts`, `.cts`, `.mjs`, `.cjs` and `.js` files,
excluding `node_modules` and build output. 341 files import it; 216 are tests and
fixtures, which leaves 125 (the inventory counted 123 with slightly different
exclusions).

| Class | Where | Files | What it does | Go answer |
|---|---|---|---|---|
| **A. The process provider** | `packages/subprocess/subprocess-local/src` | 6 | The sanctioned place. `spawn` for every managed child (`spawn.ts:475-481`), `spawnSync` for `taskkill` (`spawn.ts:120`, `windows-inspector.ts:153`), `execFile`, `spawn` and `spawnSync` for `systemd-run` and `systemctl` (`linux-scope.ts:4`), `execFileSync` for `ps` (`process-inspector.ts:123`), `spawn` for the Windows job helper (`windows-job.ts:4`, `:152`) | The harness process package (section 6). Most helper programs disappear because Go can call the operating system directly |
| **B. Product code that starts programs itself** | `packages/ssh/ssh/src/index.ts:251`, `:299`; `packages/sandbox/sandbox-local/src/index.ts:88-122`; `packages/sdk/client/src/client.ts:225`; `packages/bundle/web-app/src/index.ts:135`; `packages/host/open-in-app/src/resolver.ts:81`; `packages/host/directory-picker-native/src/win32-dialog-host.ts:29`; `packages/util/native-command/src/runner.ts:27`; `packages/client/product-analytics/src/environment-query.ts:31`; `packages/experimental/liveshare/src/host/tunnel-worker.ts:84`; `native/system/packages/entry/src/index.ts:120`; 9 files under `apps/desktop/src`, `apps/desktop-publisher/src` and `apps/plugin-market-generator/src` | 20 | Each sets its own subset of deadline, output limit, kill signal, window hiding and environment. Four more product packages do the same through execa ([execa.md](execa.md)) | All go through the same process package (`DH-LIB-NODEPROC-R14`); the debt is `UP-DEBT-found.L5.5` |
| **C. Node emulated in a browser worker** | `packages/experimental/webworker-runtime/src/node/builtin_modules/implemented/child_process.ts` | 1 | Re-implements part of the module's interface for code running in a Web Worker | Not a use of the built-in; belongs to that package's area |
| **D. Test infrastructure** | `packages/test-support/session-snapshot/src/launcher.ts:10`, `benchmarks/support/built-worker.ts:3`, `vitest.config.ts:1` | 3 | Start the product under test | Go tests use `os/exec` directly; the single-package rule applies to product code only |
| **E. Build and release scripts** | `scripts/` (44), `apps/desktop/scripts/` (41), `native/system/scripts/` (7), and 3 others | 95 | Run `git`, `pnpm`, compilers, signing and packaging tools | Replaced by the Go build tooling; see [../upstream/toolchain.md](../upstream/toolchain.md) |

Functions imported outside tests: `execFileSync` (41 files), `spawn` (39), `spawnSync`
(31), `execFile` (26). `exec`, `execSync` and `fork` are never imported, and no product
source passes `shell: true`. Upstream never hands a string to a shell through this
module; where a shell is wanted (the `bash` tool), the shell is an explicit `argv[0]`.

### What the provider builds on top of the built-in

| Behaviour | Evidence (`path:line`) |
|---|---|
| The request is fully explicit: argument list, working directory, what to do with each of the three standard streams, a grace period, a cancellation signal, environment entries. No defaults are applied | `docs/subsystems/subprocess.md:91-133` |
| Program lookup: an absolute path is verified; a bare name is searched on the child's `PATH` with `.COM;.EXE;.BAT;.CMD` (or `PATHEXT`) on Windows; a relative path with a separator is refused | `packages/subprocess/subprocess-local/src/index.ts:168-208` |
| Environment: start from the harness's own environment minus every name matching `KEY`, `PASSWORD`, `SECRET` or `TOKEN` and minus every `DSH_` name, then add the caller's entries; an entry set to "undefined" removes a name | `packages/subprocess/subprocess/src/index.ts:54`, `:73-76`; `subprocess.md:15` |
| Streams: ignore, raw pipe, inherit, or **collect** (keep the last N bytes in memory, optionally write the whole stream to a private file up to a cap) | `subprocess.md:39-89`; `subprocess-local/README.md:47` |
| Result: exit code or signal only. Whether a kill was a timeout or a cancellation is the caller's knowledge | `subprocess.md:225-240` |
| Tree: POSIX children lead their own process group (`detached`), Windows children have no window (`windowsHide`). Termination addresses the tree: `kill(-pid)` on POSIX, `taskkill /T /F` on Windows, or the stronger owners in the next row | `spawn.ts:93-152`, `:475-481` |
| Stronger owners: a Linux user-systemd scope, and a Windows job object held by a helper Node process started for every command. Fallbacks are announced once | `subprocess-local/README.md:86`, `:148-151`; `index.ts:250-290` |
| Windows command line: built by upstream's own `quoteArg`, for the job helper's `CreateProcess` call | `packages/subprocess/win32-process/src/process.ts:26-55` |
| Host exit: on a normal Node exit, a synchronous pass kills what it can; a killed or crashed harness needs "an external supervisor" | `subprocess-local/README.md:66`, `:155` |

### `node:readline`, `node:repl`, `node:tty`

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `scripts/publish-npm-baseline.ts:956-961`; `apps/desktop/scripts/publish-installed-update.ts:22`; `apps/desktop/scripts/package-installed-update.ts:2` | Ask the operator to confirm a publish by pressing Enter | `createInterface({ input, output })`, `question(prompt)`, `close()` from `node:readline/promises` |
| `packages/ptc-runtime/ptc-runtime-node/src/evaluators/repl.ts:21-43` | Evaluate model-written programs one after another so that variables declared by one are visible to the next, inside a runner process that is already a child of the harness | `start({ input, output, terminal: false, prompt: '', useGlobal: false, ignoreUndefined: true })`, `repl.eval(code, context, file, callback)`, `repl.context`, `repl.close()` |
| `packages/experimental/webworker-runtime/src/node/builtin_modules/implemented/tty.ts:19`, `mock/repl.ts:5` | Stand-ins so that code written for Node loads in a browser worker: `isatty` always answers false, `repl.start` always fails | none of the real module |

## 3. Tech debt upstream carries

Search performed: the classification above; `patches/` and `pnpm-workspace.yaml` (built-ins
have no entries); the "Known Limitations" of `subprocess-local`. No inventory `UP-DEBT`
unit is assigned to this group. The provider's own limitations are dispositioned by the
subprocess area; those the Go mechanism changes are noted in section 5.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L5.5` | The process rules live in one service, but about two dozen product packages start programs without it: 20 files call `node:child_process` directly (class B) and four product packages call execa. Each states its own part of the rules. For example `ssh` sets a 64 KiB output cap (`packages/ssh/ssh/src/index.ts:252`); `open-in-app` detaches and drops the streams (`resolver.ts:81-84`); `product-analytics` sets its own kill signal, deadline and output cap (`environment-query.ts:31-33`); `sandbox-local` blocks the event loop on a probe with a timeout (`index.ts:88-90`). Upstream's own audit rejected execa for one such site because it lacked the provider's teardown guarantees, while other sites adopted it | class B list above; `packages/boot/plugin-manager/src/operations.ts:386-393`; `.agents/notes/rejected/simplification/2026-07-26-dependency-swaps-rejected-by-nih-audit.md:49` | **Resolved** by [DH-ADR-NODEPROC-1](#dh-adr-nodeproc-1): one harness package is the only importer of `os/exec`, checked by a tool (`DH-LIB-NODEPROC-R14`). A caller that wants a weaker contract (a detached launch) asks that package for it by name |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NODEPROC-R1` | A program is started from an argument list. No string is ever handed to a shell by the process package; a caller that wants a shell names it as the program | `subprocess.md:93`, `:104`; no `exec` or `shell: true` in product source |
| `DH-LIB-NODEPROC-R2` | Program lookup: an absolute path is checked to be an executable file; a bare name is searched on the **child's** `PATH`; a relative path with a separator is refused; the current directory is never searched implicitly | `subprocess-local/src/index.ts:168-208` |
| `DH-LIB-NODEPROC-R3` | On Windows every argument reaches an ordinary program exactly as given, including empty strings, spaces, tabs, double quotes and trailing backslashes | `win32-process/src/process.ts:26-45` |
| `DH-LIB-NODEPROC-R4` | On Windows a program that resolves to a batch file (`.bat`, `.cmd`) is refused unless the caller opts in. With opt-in, an argument that cannot be passed safely through `cmd.exe` is refused with an error naming the argument position; it is never passed in altered form | Inference from `index.ts:204` (batch extensions are resolved) together with the `cmd.exe` rules in [DH-ADR-NODEPROC-1](#dh-adr-nodeproc-1) |
| `DH-LIB-NODEPROC-R5` | The child's environment is built explicitly: a scrubbed base, plus the caller's entries, minus the caller's removals. Names compare without regard to case on Windows | `subprocess/src/index.ts:54-76`; `subprocess-local/src/index.ts:386-392` |
| `DH-LIB-NODEPROC-R6` | Each standard stream is independently ignored, piped to the caller, inherited, or collected with a memory bound that keeps the tail and an optional bounded spill file | `subprocess.md:39-89` |
| `DH-LIB-NODEPROC-R7` | Cancellation starts termination of the whole tree: a polite request where the system has one, then a forced kill after the grace period. The result reports exit code or signal and nothing about the cause | `subprocess.md:109-123`, `:225-240` |
| `DH-LIB-NODEPROC-R8` | "Finished" has two separate meanings that a caller can wait for: the program exited, and the whole tree is gone. A grandchild that keeps a pipe open cannot delay the first beyond the grace period | `subprocess.md:137`, `:168-174`, `:110-116` |
| `DH-LIB-NODEPROC-R9` | The tree is owned by an operating-system object chosen **before** the program runs. When only a weaker owner is available the harness says so once, and never re-runs a command through a weaker owner after a stronger one may have started it | `subprocess-local/README.md:86`; `index.ts:273-290` |
| `DH-LIB-NODEPROC-R10` | Children do not outlive the harness. What is guaranteed when the harness is killed is stated per operating system, and is at least what upstream guarantees | `subprocess-local/README.md:66`, `:155` |
| `DH-LIB-NODEPROC-R11` | On Windows a background child opens no window | `subprocess-local/README.md:28`; `spawn.ts:480` |
| `DH-LIB-NODEPROC-R12` | A caller can ask for a detached launch: a program that is meant to outlive the harness, with no stream or handle kept | `packages/host/open-in-app/src/resolver.ts:81-84` |
| `DH-LIB-NODEPROC-R13` | A long-lived child with piped input and output can be kept across many requests and is terminated with its owner. This is what a stateful interpreter needs | `ptc-runtime-node/src/evaluators/repl.ts:13-43` |
| `DH-LIB-NODEPROC-R14` | No harness package other than the process package imports `os/exec` or calls `os.StartProcess`; tests are exempt | `UP-DEBT-found.L5.5` |
| `DH-LIB-NODEPROC-R15` | A tool that asks for confirmation reads one line from standard input and refuses to proceed when standard input is not a terminal | `scripts/publish-npm-baseline.ts:956-961` |

## 5. Go choice

**Decision.** The standard library. `os/exec` starts ordinary programs; `context` carries
cancellation and deadlines; `os/signal` lets the harness shut down in order; `bufio`
reads lines. `golang.org/x/sys` v0.50.0 (BSD-3-Clause, checked 2026-11-18, already
required elsewhere in the design) supplies the tree owners. All of it is wrapped by one
harness package ([DH-ADR-NODEPROC-1](#dh-adr-nodeproc-1),
[DH-ADR-NODEPROC-2](#dh-adr-nodeproc-2)).

**Why.** Go can call the operating system without a helper program or a foreign-function
library, which is what forced most of upstream's machinery. The table maps each upstream
mechanism to its Go counterpart.

| Upstream mechanism | Go counterpart | Verified fact |
|---|---|---|
| `spawn(program, args, { cwd, env, stdio })` | `exec.CommandContext` with `Dir`, `Env`, `Stdin`, `Stdout`, `Stderr` | `GOROOT/src/os/exec/exec.go` |
| Abort signal, then TERM, then KILL after `graceMs` | `Cmd.Cancel` (called when the context ends; the package sends the polite signal or terminates the job) and `Cmd.WaitDelay` (after it, the process is killed and its pipes are closed) | `exec.go:125-128`, `:205-227`, `:282` |
| "A descriptor held by a survivor cannot hold the outcome open" | `Cmd.WaitDelay` again: `Wait` returns `ErrWaitDelay` instead of blocking on a pipe a grandchild still holds | `exec.go:125-128` |
| POSIX `detached: true` and `kill(-pid)` | `SysProcAttr{Setpgid: true}` and `unix.Kill(-pgid, sig)` | `GOROOT/src/syscall/exec_linux.go:75-77`, `exec_libc2.go:20-22` |
| Linux user-systemd scope through `systemd-run` and polling `systemctl` | Start the child directly inside a control group: `SysProcAttr{UseCgroupFD: true, CgroupFD: fd}`; or keep the scope. For the subprocess area | `exec_linux.go:107-108` |
| Windows job object held by a helper Node process, bound through `koffi` | The harness holds the job handle itself: `windows.CreateJobObject`, `SetInformationJobObject` with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, `AssignProcessToJobObject`, `TerminateJobObject` | `golang.org/x/sys@v0.50.0/windows/zsyscall_windows.go:1785`, `:1907`, `:3388`, `:3492`; `types_windows.go:2552` |
| `taskkill /T /F` child process | `TerminateJobObject`; no child process | as above |
| `ps` child process and `/proc` reads for process identities | `/proc` reads on Linux, `unix.SysctlKinfoProcSlice` on macOS, `windows.CreateToolhelp32Snapshot` on Windows; no child process | `x/sys@v0.50.0/unix/syscall_darwin.go:520`; `windows/zsyscall_windows.go:1983` |
| `windowsHide: true` | `SysProcAttr{HideWindow: true}` or `CREATE_NO_WINDOW` in `CreationFlags` | `GOROOT/src/syscall/exec_windows.go:293-295`; `x/sys/windows/types_windows.go:244` |
| `quoteArg` and `buildCommandLine` | `syscall.EscapeArg`, applied by `os/exec` to every argument; `SysProcAttr.CmdLine` when the caller must supply the whole line | `syscall/exec_windows.go:22-104`, `:294`, `:346-349` |
| Program lookup with `PATHEXT` | `exec.LookPath`, which also refuses to resolve a bare name to the current directory (`exec.ErrDot`) | `GOROOT/src/os/exec/lp_windows.go:114`, `:154-195` |
| Synchronous cleanup in Node's `exit` event | No counterpart: Go runs no code at `os.Exit` or when killed. Replaced by operating-system owners ([DH-ADR-NODEPROC-2](#dh-adr-nodeproc-2)) | Go language specification and `os.Exit` documentation |
| `spawnSync(..., { timeout })` for probes | The same bounded run as any other; Go has no separate synchronous form | n/a |
| `readline.question` | `bufio.Scanner` over `os.Stdin`, after checking that standard input is a character device with `os.Stdin.Stat()` | standard library |
| `repl.start` with retained context | Not in the Go host. The interpreter is a child process (`DH-LIB-NODEPROC-R13`); which interpreter is the tool-runtime area's decision ([DH-ADR-4](../design.md#dh-adr-4) puts model-written code in separate processes) | n/a |

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Standard library plus `golang.org/x/sys`, wrapped once** (chosen) | No new module; direct system calls replace four kinds of helper process; cancellation and wait bounds are built in | `os/exec` cannot place a Windows child in a job before it runs, so the Windows path creates the process suspended or uses its own `CreateProcess` call (shared with [node-pty.md](node-pty.md)) | Go 1.27.4 source as cited; `golang.org/x/sys` v0.50.0, 2026-11-06, BSD-3-Clause |
| `github.com/shirou/gopsutil/v4` for process trees | Ready-made tree and identity queries on three systems | A large module for three small queries; trees are a snapshot and do not own anything, which is the weakness upstream's fallback already has | v4.26.10, 2026-10-24; existence and date by `go list`; licence not read because not chosen |

**Tradeoff.** The harness owns a few hundred lines of platform code for tree ownership.
Upstream owns far more for the same purpose (a 730-line scope launcher, a 507-line
runner, a 641-line Win32 binding), so this is a reduction, but it is still the code
where a defect kills the wrong process or leaks one.

<a id="dh-adr-nodeproc-1"></a>
### DH-ADR-NODEPROC-1 One process package starts every program, from an argument list, with an explicit rule for Windows command lines

- **Decision.** A single harness package is the only code that starts programs. It takes
  an argument list and never a command string. On Windows it applies the rules below.
- **Why.** Windows does not pass an argument list to a program. It passes **one string**,
  and each program splits it by its own rules. Quoting that is correct for one receiver
  is an injection for another. There are three receivers that matter:

  | Receiver | How it splits the string | What the package does |
  |---|---|---|
  | An ordinary program (`.exe`, `.com`) using the C runtime or `CommandLineToArgvW` | Space and tab separate arguments outside double quotes. A backslash is literal unless a run of backslashes is followed by `"`: 2n backslashes then `"` give n backslashes and a quote that opens or closes; 2n+1 give n backslashes and a literal `"` | Uses `os/exec` unchanged. `syscall.EscapeArg` produces exactly the inverse: an empty argument becomes `""`; `"` becomes `\"`; backslashes are doubled only immediately before a `"` and, when the argument is wrapped, at its end; the argument is wrapped in quotes only if it contains a space or a tab |
  | `cmd.exe`, and therefore **every `.bat` and `.cmd` file** | `cmd.exe` reads the line first, with different rules: `&`, `\|`, `<`, `>`, `(`, `)` and `^` act outside *its* notion of quotes; `\"` does not escape a quote for it; `%NAME%` is substituted even inside quotes. An argument such as `"&calc` quoted by the first rule ends `cmd.exe`'s quoted region and runs `calc` | **Refuses by default** (`DH-LIB-NODEPROC-R4`). With opt-in, runs `%ComSpec% /d /s /c "<line>"` through `SysProcAttr.CmdLine`, every argument wrapped in double quotes with trailing backslashes doubled, and refuses any argument containing `"`, `%`, `!`, carriage return, line feed or NUL, because none of them can be passed through unchanged. **As built (M1, plan 16.5.1 of the M1 gap-closure plan):** the opt-in is `Spec.Batch = AllowChecked` in `world/subprocess`. It runs `cmd.exe` from the system directory (`GetSystemDirectory`), not `%ComSpec%`, because the environment can point `%ComSpec%` anywhere and the checks above encode the rules of the system `cmd.exe`. A command line longer than 8191 UTF-16 units, `cmd.exe`'s limit, is refused with `subprocess/line-too-long`. `LookupProgram` resolves a bare name and reports whether the result is a batch file, so a caller decides whether to opt in; plain `Lookup` still refuses one |
  | A program with its own parser (`msiexec.exe` is the documented case) | Its own | Not handled generically: the caller supplies the whole line through an explicit "raw command line" option, which is reviewed where it is used |

  Why a bare name makes this urgent: lookup with `PATHEXT` resolves `pnpm`, `npm`,
  `npx`, `yarn` and many other developer tools to `.cmd` files. Upstream runs `pnpm` by
  bare name (`packages/boot/plugin-manager/src/operations.ts:192`, `:386`). A Go
  translation that called `exec.Command("pnpm", args...)` would resolve the batch file
  and quote its arguments by the first rule. The Go documentation says so itself: batch
  files "have a different unquoting algorithm" and the caller must "do the quoting
  yourself" (`GOROOT/src/os/exec/exec.go:407-414`).
- **Verdict on upstream.** *Keep* "argv is never shell-interpreted" and the single
  service. *Change* the exceptions: no product package starts programs by itself.
- **Tradeoff or constraint.** Some legitimate arguments cannot be passed to a batch file
  (a percent sign, a quotation mark). The caller gets an error and must call the real
  program behind the batch file instead. This is deliberate: a refusal is visible, a
  silently altered argument is not.
- **Serves.** `DH-LIB-NODEPROC-R1` to `R5`, `R14`; `UP-DEBT-found.L5.5`.
- **Verified by.** `DH-T-NODEPROC-1` to `DH-T-NODEPROC-4`, `DH-T-NODEPROC-10`.

<a id="dh-adr-nodeproc-2"></a>
### DH-ADR-NODEPROC-2 A process tree is owned by an operating-system object held by the harness, not by a scan or a helper process

- **Decision.** Before a program runs, the process package places it under an owner that
  the operating system keeps track of, and it terminates and waits on that owner.

  | System | Owner | Polite stop | Forced stop | If the harness is killed |
  |---|---|---|---|---|
  | Windows | A job object with "kill on close", whose only handle is held by the harness. The child is created suspended, assigned, then resumed, so no descendant can start outside the job | None for a process without a console; a protocol-level request (closing its input) is the caller's affair | `TerminateJobObject` | The handle closes and Windows kills the tree. Stronger than upstream, which needs a helper process per command to hold the handle |
  | Linux | A control group the child is started in (directly, or a user-systemd scope as upstream does), when one is available; otherwise a process group | `SIGTERM` to the group | `SIGKILL` to the group or the control group | With `Pdeathsig` set, the direct child is signalled; descendants in a scope are stopped by systemd. Without either, they survive |
  | macOS | A process group (the system offers no stronger persistent owner) | `SIGTERM` to the group | `SIGKILL` to the group | They survive unless the harness handles the signal |

  The harness also handles `SIGTERM`, `SIGINT` and `SIGHUP` and performs normal disposal,
  which upstream leaves to an external supervisor (`subprocess-local/README.md:155`).
- **Why.** A scan of the process table sees a moment and misses anything that left the
  tree before it; upstream documents that gap for its fallback
  (`subprocess-local/README.md:154`). An owner kept by the system has no such gap. Go can
  hold these owners directly, so the helper processes upstream needs disappear.
- **Verdict on upstream.** *Keep* the model (one owner for both signalling and waiting,
  chosen before the command, disclosed fallback, never replay). *Change* the mechanism.
- **Tradeoff or constraint.** `Pdeathsig` fires when the **thread** that started the
  child ends, not the process, so children must be started from a thread that lives as
  long as the harness. A job does not contain a process that was created with permission
  to break away; upstream has the same exclusion (`README.md:151`). macOS stays the weak
  platform.
- **Serves.** `DH-LIB-NODEPROC-R7` to `R10`.
- **Verified by.** `DH-T-NODEPROC-5` to `DH-T-NODEPROC-8`.

## 6. Design notes

**Shape.** The package belongs to the subprocess area. Its request mirrors upstream's
explicit spec, in Go form:

```go
type Spec struct {
    Argv   []string            // Argv[0] is the program
    Dir    string
    Env    Env                 // scrubbed base + entries + removals, resolved by the caller
    Stdin  StdinMode           // Ignore | Pipe | Data([]byte)
    Stdout, Stderr OutputMode  // Ignore | Pipe | Inherit | Collect{MaxBytes, Spill}
    Grace  time.Duration
    Batch  BatchPolicy         // Refuse (default) | AllowChecked          (Windows)
    Detach bool                // R12: outlives the harness, nothing kept
}

func (s *Service) Start(ctx context.Context, spec Spec) (*Process, error)

// Process: Stdin/Stdout/Stderr when piped; Collected readers; Exited() and
// TreeGone() as separate waits (R8); Terminate() idempotent.
```

**Ownership and access.** The service owns every `Process` it started and the system
object behind it. The service is a cordis service; the fiber that mounted it terminates
every tree and waits for it when it unloads. A caller that received a `Process` may use
it (read, write, terminate) but does not own its release: if the caller's own fiber
unloads first, the process is still terminated by the service. A detached launch
(`Detach`) is the one case where the harness creates something it deliberately does not
own; it keeps no record of it and can undo nothing.

**Failure and cleanup.**

| Situation | What happens | What is left |
|---|---|---|
| Lookup fails, or a batch file is refused | Error before anything is created | Nothing |
| The owner cannot be created (no job support, no control group) | The weaker owner is selected and announced once; the command has not started yet, so nothing is replayed | Nothing extra |
| The program fails to start after the owner exists | The owner is released; the caller gets the start error | Nothing |
| On Windows the harness is killed between "create suspended" and "resume" | The job's "kill on close" removes the suspended process. Upstream documents a leak in this window (`README.md:151`) | Nothing |
| Cancellation | Polite stop, wait up to `Grace`, forced stop, wait for the tree | Nothing; if the tree cannot be confirmed gone, the error says so and the `Process` stays registered for the next attempt |
| A grandchild keeps the output pipe open | `Exited()` completes after `Grace` with what was read; `TreeGone()` keeps waiting on the owner | The grandchild, still owned |
| The spill file cannot be written | Collection continues in memory; the result is marked truncated with no spill path; one error is logged (upstream's behaviour, `README.md:47`) | A partial spill file is removed |

Nothing is retried automatically. Starting a program twice is never assumed safe.

**Interpreter processes (`R13`).** The process package gives the tool-runtime area a
child with piped input and output that lives until its owner unloads. Keeping variables
between evaluations is the interpreter's job inside that child; the host only frames
requests and replies.

## 7. Risks and open questions

- **The plugin manager and batch files.** If the Go plugin manager still drives a
  package manager on Windows, it meets `DH-LIB-NODEPROC-R4` at once. Whether it does is
  the plugin area's decision under [DH-ADR-4](../design.md#dh-adr-4).
- **Linux owner.** A control group needs delegation from the session's service manager;
  a scope needs a reachable user manager, with the costs upstream lists
  (`subprocess-local/README.md:149-150`). The subprocess area chooses and states the
  fallback.
- **The interpreter for model-written programs.** `node:repl` is replaced by "a process",
  not by a named interpreter. Open for the tool-runtime area.
- **`node:tty`.** The Go command-line front end will want to know whether its output is a
  terminal (colour, prompts). That need is not an upstream use of `node:tty` and is left
  to the command-line area; `golang.org/x/term` v0.48.0 (BSD-3-Clause, checked
  2026-11-18) is the obvious candidate and is not declared here.
- **New harness package.** The process package and the import check are own code for the
  subprocess area to schedule.

## 8. Verification

Local tests, run on each operating system's CI runner against real processes. Nothing
has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-NODEPROC-1` | local, Windows | `R3` | A helper program prints its arguments one per line. A table of arguments (empty, spaces, tabs, embedded quotes, trailing backslashes, backslashes before quotes, non-ASCII text) and a generated set of random arguments all arrive unchanged |
| `DH-T-NODEPROC-2` | local, Windows | `R4` | A batch file is refused by default. With opt-in, plain arguments arrive unchanged, and each of `"`, `%`, `!`, CR, LF, NUL and the string `"&echo injected` is refused before any process starts; a marker file proves no injected command ran |
| `DH-T-NODEPROC-3` | local, all | `R1`, `R2` | A bare name is found on the child's `PATH` and not on the harness's; a relative path is refused; a program in the current directory is not found by bare name |
| `DH-T-NODEPROC-4` | local, all | `R5` | The child prints its environment: credential-shaped and `DSH_` names are absent, caller entries are present, a removal removes, and on Windows `Path` and `PATH` are one name |
| `DH-T-NODEPROC-5` | local, all | `R7`, `R8` | A child that ignores the polite stop is killed after the grace period; a child whose grandchild holds the pipe reports exit within the grace period while the tree wait continues until the grandchild is killed |
| `DH-T-NODEPROC-6` | local, Windows, Linux | `R9` | A grandchild that detaches itself (new session on Linux, new process group on Windows) is still terminated with the tree and still delays the tree wait |
| `DH-T-NODEPROC-7` | local, all | `R10` | A test harness process starts a tree and is then killed without warning; the tree is gone on Windows, the direct child is gone on Linux, and the documented survivors on macOS are exactly those documented. A second run sends `SIGTERM` and finds nothing left on any system |
| `DH-T-NODEPROC-8` | local, Windows | `R9`, `R11` | Killing the test harness between "create suspended" and "resume" (an injected pause) leaves no process; a background child creates no window |
| `DH-T-NODEPROC-9` | local, all | `R6`, `R12`, `R13` | Collection keeps the tail and marks truncation; the spill file holds the whole stream up to its cap and is private; a detached program outlives the test process; a piped child answers a hundred requests and is gone after its owner unloads |
| `DH-T-NODEPROC-10` | local, static check | `R14`, `R15` | A source check fails the build when a non-test file outside the process package imports `os/exec`; the confirmation helper returns "refused" when standard input is a pipe |

## 9. Units dispositioned by this spec

- `UP-DEP-node:child_process`
- `UP-DEP-node:readline`
- `UP-DEP-node:repl`
- `UP-DEP-node:tty`
- `UP-DEBT-found.L5.5`
