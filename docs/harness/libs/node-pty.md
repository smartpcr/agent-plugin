# node-pty: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18 with
> `go list -m -json <module>@latest`, `go mod download -json` and by reading the files in
> the module cache; nothing was compiled or run.

| | |
|---|---|
| **Library ID** | `DH-LIB-NODEPTY` |
| **Upstream unit** | `UP-DEP-node-pty` |
| **Upstream version** | 1.2.0-beta.15 (a pre-release), patched by `patches/node-pty@1.2.0-beta.15.patch` |
| **Kind** | runtime, native (a compiled Node addon plus helper executables) |
| **Used by** | subprocess (`packages/subprocess/subprocess-local` only) |
| **Go choice** | own package in the harness on top of `golang.org/x/sys` v0.50.0; no cgo, no helper executable |
| **Related areas** | subprocess and terminal; [xterm.md](xterm.md) for the screen emulator that reads what this library produces; [node-process-builtins.md](node-process-builtins.md) for ordinary (pipe) processes |

## 1. What it is and why upstream needs it

A **pseudo-terminal** (PTY) is a pair of connected devices the operating system creates
on request. A program attached to one end believes it is talking to a real terminal: it
prints prompts, enables line editing and colour, and reacts to Ctrl-C. The harness holds
the other end (the **master**), reads what the program prints and writes what a person or
the model types. A plain pipe cannot do this, because many programs (shells, debuggers,
REPLs, `ssh`, `sudo`) change behaviour or refuse to run when their input is not a
terminal. Windows has no PTY devices; its equivalent is **ConPTY** ("pseudoconsole",
Windows 10 version 1809 and later): a **console host** process (`conhost.exe`, or the
newer `OpenConsole.exe`) sits between the program and the harness and turns console
calls into a stream of terminal escape sequences.

node-pty is the Node library that allocates a PTY or a ConPTY and starts a program on
it. Node cannot do this itself, so node-pty ships compiled code for every platform. The
harness uses it in exactly one place: the local subprocess provider's `spawnTerminal`,
which is "the only non-pipe process primitive" (`docs/subsystems/subprocess.md:245`).
Everything above it is harness code: the persistent shell the model drives
(`packages/terminal/terminal-bash`), the terminal tab in the web sidebar
(`packages/api/terminal-controller`), prompt detection, scrollback and cleanup.

Two terms used below. A **session** is the group of processes started from one terminal;
its first process is the **session leader**. The **foreground process group** is the set
of processes that currently receive keyboard signals such as Ctrl-C; a shell moves it
from itself to each command it runs.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/subprocess/subprocess-local/src/index.ts:55-68` | Load the addon lazily on the first terminal request; a load failure becomes the stable error `SUBPROCESS_PTY_UNAVAILABLE` | `require('node-pty')` through `createLazyRequire` |
| `packages/subprocess/subprocess-local/src/index.ts:315-339` | Start `argv[0]` with its arguments on a new terminal with a working directory, an environment, a terminal type (`TERM`), columns and rows. On Windows only, ask for the console host that node-pty ships (`useConptyDll: true`) | `spawn(file, args, { name, cols, rows, cwd, env, useConptyDll })` |
| `packages/subprocess/subprocess-local/src/terminal.ts:96` | Identity of the top-level process | `IPty.pid` |
| `packages/subprocess/subprocess-local/src/terminal.ts:100-112` | Ordered output, already decoded to text by node-pty and re-encoded to bytes by the caller; reading is paused when the consumer's buffer is full and resumed when it drains | `onData`, `pause()`, `resume()` |
| `packages/subprocess/subprocess-local/src/terminal.ts:113-131` | Exit code and terminating signal of the top-level process; the output stream is ended here | `onExit({ exitCode, signal })` |
| `packages/subprocess/subprocess-local/src/terminal.ts:141-151` | Input and resize; both reject after exit | `write(data)`, `resize(cols, rows)` |
| `packages/subprocess/subprocess-local/src/terminal.ts:205-212` | Windows interrupt: there is no signal, so Ctrl-C is the input byte `0x03` | `write('\x03')` |
| `packages/subprocess/subprocess-local/src/terminal.ts:257`, `:350`, `:358`, `:385` | Last-resort kill of the top-level process when its identity could not be captured | `kill(signal)` on POSIX, bare `kill()` on Windows |
| `packages/subprocess/subprocess-local/src/terminal.ts:410-411`, `:427-428` | Stop listening at cleanup | `IDisposable.dispose()` |

What upstream does **not** get from node-pty, and builds beside it:

- **The foreground process group.** node-pty's public interface "exposes no master fd"
  (`.agents/notes/implemented/feature/2026-07-16-persistent-pty-sessions.md:149`), so the
  provider reads it from the process table: field `tpgid` of `/proc/<pid>/stat` on Linux
  (`process-inspector.ts:447-449`) and one `/bin/ps -o tpgid= -p <pid>` child process per
  query on macOS (`process-inspector.ts:519-521`).
- **Signals to that group**, descendants, start-time identities against process-id reuse
  and "is the foreground program waiting for input" (`process-inspector.ts`, 560 lines).
- **Tree cleanup.** A Linux user-systemd scope where available, otherwise a scan of the
  process table (`subprocess-local/README.md:58`, `:154`).

## 3. Tech debt upstream carries

Search performed: every reference to `node-pty`, `spawn-helper` and
`DSH_NODE_PTY_SPAWN_HELPER` in the checkout (source, scripts, workflows, notes); the
patch; the `allowBuilds` and `patchedDependencies` entries in `pnpm-workspace.yaml`; the
"Known Limitations" of `subprocess-local`, `terminal-bash` and `terminal-controller`.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-patch.node-pty@1.2.0-beta.15` | A two-part patch. **Part 1 (POSIX):** node-pty starts programs through a small helper executable, `spawn-helper`, that it expects beside its addon. Upstream's single-file executables (the Python SDK runtime, the SSH helper) have no such directory, so the patch makes the path come from `DSH_NODE_PTY_SPAWN_HELPER`, or from a file named `<executable>-spawn-helper` beside the executable. **Part 2 (Windows):** after killing a terminal, node-pty only released its output-reading worker when more output arrived; a program that had already exited produced none, so the worker was never released. The patch releases it immediately | `patches/node-pty@1.2.0-beta.15.patch:9-27`, `:35-36`; `apps/desktop/scripts/runtime-patch-policy.ts:22` | **Resolved** by [DH-ADR-NODEPTY-1](#dh-adr-nodepty-1). Part 1: there is no helper executable. The Go runtime performs "new session, make this the controlling terminal" itself between fork and exec (`syscall.SysProcAttr` fields `Setsid`, `Setctty`, `Ctty`, present for Linux and macOS in Go 1.27). Part 2: the cause is a ConPTY rule, not a node-pty bug, so Go has the same obligation and meets it by design, not by patch: the output pipe is read by its own goroutine until end of stream, and close is ordered and bounded (`DH-LIB-NODEPTY-R10`) |
| `UP-DEBT-pin.allowBuilds.node-pty` | pnpm refuses install scripts unless a package is exempted. node-pty is exempted because its install step selects or compiles the native addon | `pnpm-workspace.yaml:34-35` | **Resolved** by [DH-ADR-NODEPTY-1](#dh-adr-nodepty-1): Go modules cannot run code at install or build time, and the chosen code has no native part to compile |
| `UP-DEBT-pin.allowBuilds.@deepseek-ai/dsh-subprocess-local@file:packages/subprocess/subprocess-...` | The harness's own package needs an install script: npm packaging strips the executable bit from node-pty's macOS `spawn-helper`, so a `postinstall` restores it | `pnpm-workspace.yaml:47-49`; `packages/subprocess/subprocess-local/package.json:46`; `packages/subprocess/subprocess-local/scripts/ensure-spawn-helper.mjs:1-16` | **Resolved** by [DH-ADR-NODEPTY-1](#dh-adr-nodepty-1): no helper file exists, so there is no permission to restore |
| `UP-DEBT-found.L5.1` | The terminal's foreground process group is not read from the terminal. Because node-pty hides the master descriptor, it is inferred from the process table: a `/proc` read on Linux and a `ps` child process per query on macOS, polled during every wait for readiness | `.agents/notes/implemented/feature/2026-07-16-persistent-pty-sessions.md:149`; `packages/subprocess/subprocess-local/src/process-inspector.ts:447-449`, `:519-521` | **Resolved** by `DH-LIB-NODEPTY-R7`: the harness owns the master descriptor and asks the kernel directly (`ioctl(master, TIOCGPGRP)`), one system call, the same on Linux and macOS |
| `UP-DEBT-found.L5.2` | Windows terminals do not use the operating system's console host. Server 2022 and Windows 10 22H2 repaint a prompt as newline, `dsh>`, "cursor right one" instead of `dsh> `; upstream's readiness check compares raw bytes after a marker, never matches, and every command waits out a silence timer. The fix was to ship and require node-pty's own `conpty.dll` and `OpenConsole.exe` through an option node-pty marks experimental; a missing file makes every terminal fail | `.agents/notes/implemented/bug-fix/2026-09-28-pty-bundled-console-host.md:11-19`, `:33-35`; `packages/subprocess/subprocess-local/src/index.ts:321-325`; `packages/subprocess/subprocess-local/README.md:153` | **Resolved** by [DH-ADR-NODEPTY-2](#dh-adr-nodepty-2): the default is the operating system's ConPTY, and readiness reads the emulated screen, where both byte forms give the same text ([xterm.md](xterm.md), `DH-LIB-XTERM-R8`). A console host supplied by the operator stays possible, as configuration and not as a bundled requirement |
| `UP-DEBT-found.L5.3` | Release work that exists only because the PTY is a native addon: rebuilding the Linux addon against an old C library and checking the symbol versions it needs, checking the macOS deployment target of `spawn-helper`, shipping `-spawn-helper` beside each single-file executable, unpacking helpers from the desktop archive, and a file policy that keeps the right `conpty` binaries per platform | `.github/workflows/build-exe-for-python-sdk.yml:226-260`, `:497-501`; `scripts/prepare-linux-node-pty.sh:8-10`; `scripts/build-exe-for-python-sdk.ts:434-435`; `python/sdk-runtime/src/deepseek_harness_runtime/__init__.py:88-91`; `apps/desktop/scripts/runtime-file-policy.ts:38-44`; `apps/desktop/scripts/electron-builder-config.mjs:78` | **Resolved** by [DH-ADR-NODEPTY-1](#dh-adr-nodepty-1) and `DH-LIB-NODEPTY-R13`: one statically linked binary per platform contains the PTY code; there is no addon, C library floor, helper or sidecar to build, check or ship |
| `UP-DEBT-found.L5.4` | On Windows node-pty cannot be trusted to report or cause exit. `kill(signal)` throws, a bare `kill()` behaves differently per console host, and a shell killed from outside "may never fire node-pty's exit notification", so cleanup shells out to `taskkill`, polls the process table to learn the shell is gone, and then fabricates the exit result | `packages/subprocess/subprocess-local/src/terminal.ts:367-376`, `:464-478`; `packages/subprocess/subprocess-local/README.md:152` | **Resolved** by `DH-LIB-NODEPTY-R6`: the harness creates the process itself and keeps its process handle, so exit is observed by waiting on that handle and termination is `TerminateProcess` or a job object, with no dependency on a library's notification |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NODEPTY-R1` | A program can be started on a new terminal on Linux, macOS and Windows from an argument list that is never interpreted by a shell, with an explicit working directory, environment, terminal type, columns and rows | `subprocess-local/src/index.ts:303-339` |
| `DH-LIB-NODEPTY-R2` | On POSIX the program is the leader of a new session whose controlling terminal is the new PTY, and the process id reported is the program's own, never a helper's | `subprocess-local/README.md:58`; persistent-PTY note `:151` (a helper pid forced upstream to give up session-wide signalling) |
| `DH-LIB-NODEPTY-R3` | Output is delivered as ordered **bytes**, with backpressure: a slow reader slows reading from the terminal and does not grow an unbounded buffer. The stream ends after the last byte once the program has exited | `terminal.ts:100-112`, `:121`; `docs/subsystems/subprocess.md:278` |
| `DH-LIB-NODEPTY-R4` | Input can be written while the program runs; a write after exit fails with a stable error | `terminal.ts:141-145` |
| `DH-LIB-NODEPTY-R5` | The size can be changed while the program runs; a resize after exit fails with a stable error | `terminal.ts:148-151`; `subprocess.md:247` |
| `DH-LIB-NODEPTY-R6` | Exit is always observed, with an exit code or a signal, including when the program is killed from outside the harness | `terminal.ts:113-131`, `:464-478` |
| `DH-LIB-NODEPTY-R7` | On POSIX the foreground process group is read from the terminal with one system call | `process-inspector.ts:447-449`, `:519-521` |
| `DH-LIB-NODEPTY-R8` | A named signal can be sent to the foreground process group on POSIX. On Windows an interrupt is delivered as the input byte `0x03`, and stop and hang-up are refused as unsupported, not ignored | `terminal.ts:196-220`; `subprocess-local/README.md:152` |
| `DH-LIB-NODEPTY-R9` | Closing a terminal is one awaited, repeatable operation that leaves no open descriptor, handle, goroutine or console host process behind, whether the program exited by itself, was terminated, or never started | `terminal.ts:222-233`, `:406-429` |
| `DH-LIB-NODEPTY-R10` | On Windows, closing never blocks forever: output is drained concurrently until end of stream, and the close has a deadline after which the reader is released by closing the pipe | patch part 2 (`patches/node-pty@1.2.0-beta.15.patch:35-36`) |
| `DH-LIB-NODEPTY-R11` | If a terminal cannot be allocated, the request fails with one stable error that carries the cause; it never falls back to pipes | `index.ts:57-67`; `subprocess-local/README.md:72` |
| `DH-LIB-NODEPTY-R12` | On Windows the console host is the operating system's by default. An operator may configure a different one by path; a configured host that is missing or unloadable fails the request and names the path | `index.ts:321-325`; bundled-console-host note `:35` |
| `DH-LIB-NODEPTY-R13` | Terminals work from the product binary alone, built with `CGO_ENABLED=0`, copied to a machine that has no compiler, no Node and no extra files | debts `L5.3` and the two `allowBuilds` pins |

## 5. Go choice

**Decision.** Recorded as [DH-ADR-NODEPTY-1](#dh-adr-nodepty-1): a small harness package
allocates terminals with `golang.org/x/sys` directly, on all three operating systems.

**Why.** The Windows half has to be the harness's own code whatever is chosen for POSIX.
Go's `os/exec` cannot attach a pseudoconsole: its Windows `SysProcAttr` has no field for
the process attribute list that carries one (fields read in
`GOROOT/src/syscall/exec_windows.go:293-301`). The harness therefore calls
`CreateProcess` itself, and it wants that one call to also start the program suspended
inside a job object (process-tree ownership, see
[node-process-builtins.md](node-process-builtins.md)) and, optionally, to use an
operator-supplied console host (`DH-LIB-NODEPTY-R12`). None of the modules checked
exposes that combination. The POSIX half is about a hundred lines over documented
`ioctl` calls, and `golang.org/x/sys` is already required by the sandbox and persistence
designs, so the choice adds no module.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Own package on `golang.org/x/sys`** (chosen) | One interface for three systems; no new module; owns the master descriptor (`R7`) and the Windows process handle (`R6`); one `CreateProcess` call can carry pseudoconsole and job | The harness owns platform details: `ioctl` numbers, environment block, command line, close ordering | `golang.org/x/sys` v0.50.0, 2026-11-06, BSD-3-Clause, no cgo in the packages used. `windows` has `CreatePseudoConsole`, `ResizePseudoConsole`, `ClosePseudoConsole`, `NewProcThreadAttributeList`, `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`, `StartupInfoEx`, `CreateProcess`, `ComposeCommandLine`, `CreateJobObject`, `AssignProcessToJobObject`, `TerminateJobObject`, `NewLazyDLL`. `unix` has `TIOCGPTN`, `TIOCSPTLCK`, `TIOCGPGRP`, `TIOCSWINSZ` (Linux) and `TIOCPTYGRANT`, `TIOCPTYUNLK`, `TIOCPTYGNAME`, `TIOCGPGRP` (macOS), with `IoctlGetInt` and `IoctlSetWinsize`. It does **not** define `PROC_THREAD_ATTRIBUTE_JOB_LIST`; the harness declares that constant |
| `github.com/creack/pty` for POSIX, own code for Windows | The most widely used Go PTY code; returns the master as `*os.File`, so `R7` still works | A second code path and a module for about 250 lines; Windows is not supported: `StartWithSize` returns `ErrUnsupported` (`start_windows.go`) | v1.1.24, 2024-10-31, MIT. No cgo in the build: the files that `import "C"` carry `//go:build ignore`. `github.com/creack/pty/v2` exists as v2.0.1 (2023-10-28) and is older than v1.1.24 |
| `github.com/aymanbagabas/go-pty` | One interface for POSIX and ConPTY | Wraps process creation, so no job object and no console-host choice; pulls in `github.com/u-root/u-root` and `golang.org/x/crypto` for an SSH feature the harness does not use; depends on `creack/pty` | v0.2.3, 2026-05-17, MIT, no cgo |
| `github.com/charmbracelet/x/xpty` with `github.com/charmbracelet/x/conpty` | Maintained by an organisation; `conpty` is 315 lines and a useful reference | `conpty.Spawn` takes a `syscall.ProcAttr`, so again no job object and no console-host choice; it copies 217 lines of `os/exec` internals to build the environment and resolve the program, which the harness would then have twice | `xpty` v0.1.4, 2026-07-30; `conpty` v0.2.0, 2025-11-17; both MIT, no cgo. No reference to `conpty.dll` or `OpenConsole` in either |
| `github.com/UserExistsError/conpty` | Small | Windows only; last release 2024-07-09; same limits as above | v0.1.4, MIT, no cgo |

**Tradeoff.** The harness maintains platform code that upstream delegated (Inference:
about 400 lines, judged from the 315-line `conpty_windows.go` and the roughly 250 lines
of `creack/pty` that apply to Linux and macOS). In exchange it removes a pre-release native dependency, its patch, two install
script exemptions and the release steps listed under `UP-DEBT-found.L5.3`. `creack/pty`
remains a drop-in replacement for the POSIX half if maintaining it proves costly.

<a id="dh-adr-nodepty-1"></a>
### DH-ADR-NODEPTY-1 Terminals are allocated by harness code on `golang.org/x/sys`, with no cgo and no helper executable

- **Decision.** One harness package provides `Start`, `Read`, `Write`, `Resize`,
  `ForegroundGroup`, `Signal`, `Wait` and `Close` for a terminal on Linux, macOS and
  Windows, written against `golang.org/x/sys/unix` and `golang.org/x/sys/windows`.
- **Why.** See "Why" above: Windows needs own code in any case, POSIX is small, and the
  result has one interface, one owner of the descriptors and no native build.
- **Verdict on upstream.** *Change.* Upstream's choice of node-pty was right for Node,
  which has no way to allocate a terminal. It is the cause of every debt in section 3.
- **Tradeoff or constraint.** `CGO_ENABLED=0` rules out binding a C library. The harness
  owns close ordering on Windows, which is the part most likely to hide a defect.
- **Serves.** `DH-LIB-NODEPTY-R1` to `R13`.
- **Verified by.** `DH-T-NODEPTY-1` to `DH-T-NODEPTY-9`, each run on the three operating
  systems where it applies.

<a id="dh-adr-nodepty-2"></a>
### DH-ADR-NODEPTY-2 Windows uses the operating system's console host; another host is configuration, never a bundled requirement

- **Decision.** `CreatePseudoConsole` in `kernel32.dll` is the default. An operator may
  point the harness at a directory holding `conpty.dll` and `OpenConsole.exe`, the pair
  node-pty ships (`apps/desktop/tests/runtime-file-policy.spec.ts:45-46`). The harness
  then loads that DLL with `windows.NewLazyDLL` and calls its pseudoconsole entry points.
  Inference: those entry points mirror the `kernel32` ones, and the files are published
  by Microsoft under the MIT licence; both must be confirmed before the override is
  built. The product does not ship those files.
- **Why.** Upstream switched to a bundled host because older Windows versions repaint a
  prompt in a different byte form and the readiness check compared bytes
  (`UP-DEBT-found.L5.2`). Inference: once readiness compares the text on the emulated
  screen, the two byte forms are indistinguishable, because "newline, `dsh>`, cursor
  right one" and "`dsh> `" leave the same characters to the left of the cursor. The
  reason for bundling then disappears, and with it a second executable to ship, sign and
  keep in step with the DLL.
- **Verdict on upstream.** *Change.* Upstream rejected "teach the sanitizer to tolerate
  the console host" because it would hard-code one host's habits into a byte filter
  (note `:25`). Reading an emulated screen is not that: it applies the terminal's rules,
  not one host's.
- **Tradeoff or constraint.** The fix depends on the emulator in [xterm.md](xterm.md)
  and on the terminal area adopting screen-based prompt matching. Until a Windows
  Server 2022 run confirms it, the override exists as the fallback. Loading a DLL by
  path is done with `syscall`, not cgo, so the release assumption holds.
- **Serves.** `DH-LIB-NODEPTY-R12`; `UP-DEBT-found.L5.2`.
- **Verified by.** `DH-T-NODEPTY-8`, and `DH-T-XTERM-8` for the screen comparison.

## 6. Design notes

**Shape.** The owning package is the local subprocess provider in the harness module
(the subprocess area names it). The interface the rest of the harness sees:

```go
type TerminalSpec struct {
    Argv       []string          // Argv[0] is the program; never passed to a shell
    Dir        string
    Env        []string          // complete environment, already scrubbed by the caller
    Term       string            // value of TERM, for example "xterm-256color"
    Cols, Rows int
    Grace      time.Duration     // wait between "terminate" and "kill"
}

type Terminal interface {
    Pid() int
    io.Reader                                   // ordered bytes; io.EOF after exit
    io.Writer                                   // input; ErrExited after exit
    Resize(cols, rows int) error
    ForegroundGroup() (pgid int, err error)     // ErrUnsupported on Windows
    Signal(sig Signal) error                    // to the foreground group
    Wait(ctx context.Context) (ExitStatus, error)
    Close(ctx context.Context) error            // idempotent; see cleanup below
}
```

**POSIX mechanism.** Open the master (`/dev/ptmx`), unlock it and learn the other end's
name with the `ioctl` calls listed in section 5, open that end, set the size, and start
the program with `os/exec`, the terminal as its standard input, output and error, and
`SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}`. The parent closes its copy of the
program's end immediately, so that end of stream on the master means "every process that
held the terminal has gone".

**Windows mechanism.** Create two pipes and a pseudoconsole over them, build a process
attribute list holding the pseudoconsole (and the job, when the subprocess area asks for
one), and call `CreateProcess` with `EXTENDED_STARTUPINFO_PRESENT` and
`CREATE_UNICODE_ENVIRONMENT`. The command line is built with
`windows.ComposeCommandLine`; the rules and the batch-file exception are in
[node-process-builtins.md](node-process-builtins.md#dh-adr-nodeproc-1). One goroutine
reads the output pipe until end of stream.

**Bytes, not text.** node-pty decodes output to text and upstream re-encodes it
(`terminal.ts:108`). The Go reader returns the bytes; the consumer decodes, which keeps a
character that is split across two reads intact without a round trip.

**Backpressure.** `Read` is a blocking call on the master or the output pipe. A consumer
that stops reading stops the kernel buffer from draining, which blocks the program's
writes. That is the behaviour `pause()` and `resume()` approximate upstream, without
state to keep in step.

**Ownership.** The terminal handle owns the master descriptor (or the two pipe ends and
the pseudoconsole), the process handle and the reader goroutine. The subprocess service
owns every terminal handle; the fiber that mounted the service closes them all when it
unloads, as upstream's service disposal does (`subprocess-local/src/index.ts:135-166`).
Access is a separate matter: which agent or session may use a terminal is decided by the
terminal service above, not here.

**Failure and cleanup.**

| Situation | What happens | What is left |
|---|---|---|
| Allocation fails before the program starts (no PTY devices, ConPTY missing, configured host unloadable) | Everything opened so far is closed in reverse order; the caller gets `ErrPTYUnavailable` wrapping the cause | Nothing |
| The program cannot be started (not found, not executable) | The terminal is closed; the caller gets the start error | Nothing |
| The program exits by itself | `Read` returns the remaining bytes and then end of stream; `Wait` returns the status; `Close` releases the descriptors | Nothing after `Close` |
| `Close` while the program runs | Terminate, wait up to `Grace`, kill, wait for exit, then release. Returns an error naming the surviving process if exit cannot be confirmed; the handle stays registered so that a later `Close` retries | On error: the process and its handle, reported, never forgotten |
| `Close` on Windows with unread output | The reader goroutine keeps draining; `ClosePseudoConsole` is called; if end of stream does not arrive before the deadline the read handle is closed to release the reader | Nothing |
| The harness process dies | Not this package's job: the tree owner (job object, scope or process group) decides, see [node-process-builtins.md](node-process-builtins.md#dh-adr-nodeproc-2) | Depends on the tree owner |

`Close` is safe to call again after any of these. Nothing here is retried automatically:
starting a program twice is not safe to assume.

## 7. Risks and open questions

- **ConPTY close ordering differs by Windows version.** Inference from Microsoft's
  ConPTY documentation: before Windows 11 24H2, `ClosePseudoConsole` waits for attached
  programs to leave and for the output pipe to be drained. `DH-LIB-NODEPTY-R10` and
  `DH-T-NODEPTY-7` exist for this; they must run on Windows 10 22H2 or Server 2022 as
  well as a current Windows 11.
- **A terminal inside a job object.** Upstream leaves Windows terminals outside job
  containment (`subprocess-local/src/index.ts:284`). Inference: a program attached to a
  pseudoconsole can be placed in a job, since the console host is a separate process
  that ends when the pseudoconsole is closed. `DH-T-NODEPTY-9` checks it. For the
  subprocess area to decide.
- **Screen-based prompt matching is another area's work.** [DH-ADR-NODEPTY-2](#dh-adr-nodepty-2)
  depends on the terminal area adopting it. If it does not, `UP-DEBT-found.L5.2` returns
  as a requirement to ship a console host.
- **macOS has no persistent owner for a process tree.** Unchanged from upstream
  (`subprocess-local/README.md:149`); owning the master descriptor improves what can be
  observed (session and foreground group) but not what can be guaranteed.
- **Linux input-wait detection.** Upstream's "is the foreground program blocked reading
  the terminal" probe reads `/proc/<pid>/task/<tid>/syscall`. It is independent of the
  PTY library and is carried to the subprocess area unchanged; nothing here replaces it.
- **New harness package.** The PTY code is own code. The subprocess area must schedule
  it as part of its local provider deliverable.

## 8. Verification

Each test runs against a real terminal on the operating system named, in that system's
CI runner. They are local tests: no external service is involved. Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-NODEPTY-1` | local, Linux, macOS, Windows | `R1`, `R3`, `R4` | Start a program that reports whether its input is a terminal, the size it sees and `TERM`; write a line and read the echo; the bytes arrive in order and the stream ends after exit |
| `DH-T-NODEPTY-2` | local, Linux, macOS | `R2`, `R7` | The child reports its process id, session id and controlling terminal: the pid equals `Pid()`, it leads its session, and `ForegroundGroup()` changes from the shell's group to a child's while a foreground command runs |
| `DH-T-NODEPTY-3` | local, all | `R5` | Resize and have the program print the size it now sees; resize after exit returns the stable error |
| `DH-T-NODEPTY-4` | local, all | `R6` | Exit code for a normal exit; signal for a POSIX kill; and a status, not a hang, when the program is killed by an unrelated process (`taskkill /F` on Windows) |
| `DH-T-NODEPTY-5` | local, all | `R8` | A program in raw mode that ignores the byte `0x03` still receives an interrupt sent with `Signal` on POSIX; on Windows the interrupt arrives as Ctrl-C and stop and hang-up return `ErrUnsupported` |
| `DH-T-NODEPTY-6` | local, all | `R9`, `R11` | After `Close` in each row of the cleanup table the process has no extra descriptors, handles or goroutines and no console host child; a forced allocation failure returns `ErrPTYUnavailable` with its cause |
| `DH-T-NODEPTY-7` | local, Windows 10 22H2 or Server 2022, and Windows 11 | `R10` | Close a terminal whose program has exited and one whose program is flooding output; both return within the deadline |
| `DH-T-NODEPTY-8` | local, Windows | `R12` | Default allocation needs no file beside the binary; a configured console-host directory is used when present and fails with the path in the error when a file is missing |
| `DH-T-NODEPTY-9` | local, Windows; build check, all | `R13` and the job-object question | `go build` with `CGO_ENABLED=0` succeeds for every release target and `go version -m` shows `CGO_ENABLED=0`; on Windows a terminal started inside a job dies with the job, including a grandchild |

## 9. Units dispositioned by this spec

- `UP-DEP-node-pty`
- `UP-DEBT-patch.node-pty@1.2.0-beta.15`
- `UP-DEBT-pin.allowBuilds.node-pty`
- `UP-DEBT-pin.allowBuilds.@deepseek-ai/dsh-subprocess-local@file:packages/subprocess/subprocess-...`
- `UP-DEBT-found.L5.1`, `UP-DEBT-found.L5.2`, `UP-DEBT-found.L5.3`, `UP-DEBT-found.L5.4`
