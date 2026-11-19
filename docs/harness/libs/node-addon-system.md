# @deepseek-ai/node-addon-system (`native/system`): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Module and standard-library facts were checked on 2026-11-18
> with Go 1.27.4 (`go list -m -json`, `go mod download -json`, the `LICENSE` file and the
> source in the module cache, and `go doc`).

| | |
|---|---|
| **Library IDs** | `DH-LIB-NATIVESYSTEM` (the launch stage and the process lock, as harness code); `DH-LIB-GOLANDLOCK` (the Go module that builds the Landlock ruleset) |
| **Upstream units** | `UP-PKG-native.system`, `UP-PKG-native.system.packages.entry`, and the decision note `UP-DEC-implemented.architecture.2026-09-07-prebuilt-system-primitives` |
| **Upstream version** | workspace package, one shared version for the entry package and four platform packages (`native/system/docs/packaging.md:3`) |
| **Kind** | native: upstream's own C code, the only native code upstream writes itself |
| **Used by** | sandbox, session, ssh, shell (tests), apps (build script) |
| **Go choice** | own code in the harness binary; `github.com/landlock-lsm/go-landlock` to build the ruleset; standard library `syscall.Flock` for the lock |
| **Related areas** | sandbox, session store, subprocess, SSH, release; [../upstream/security.md](../upstream/security.md) decisions `DH-ADR-SECURITY-2` and `-3`; [koffi.md](koffi.md) for the Windows side |

## 1. What it is and why upstream needs it

`native/system` holds two small C programs and the machinery to ship them.

**`landlock-run`, a launcher.** *Landlock* is a Linux kernel feature that lets an
unprivileged process give up file access for itself and everything it later starts. A
*ruleset* is an allow-list: the access kinds it governs and, per directory, which of
them stay allowed. A restricted process cannot widen its own ruleset. A *launcher* is a
tiny program that restricts itself and then turns into the real command, so the command
is born confined while the harness that started it is not. Upstream uses this as the
second-choice Linux sandbox, for hosts where bubblewrap is missing or forbidden
(`native/system/packages/entry/src/main.c:4-11`).

**`system.node`, a file-lock binding.** `flock` is the POSIX system call that takes an
advisory lock on an open file; the kernel drops the lock when the last descriptor closes,
including when the process dies. Upstream uses it so that two processes never append to
one session log. Node has no file-lock call, so upstream wrote a Node addon for it
(`native/system/packages/entry/src/flock.c:1-6`).

**The packaging.** Because both are compiled C, upstream builds them on four native CI
runners, in up to three variants per platform, and publishes five npm packages under one
version, so that installing the harness never compiles anything
(`.agents/notes/implemented/architecture/2026-09-07-prebuilt-system-primitives.md:9-13`).

In Go the two needs remain and the packaging does not: a Go program can make the
Landlock and `flock` system calls itself, and `go build` produces the binary for every
platform from one machine.

## 2. How upstream uses it

### 2.1 Consumers

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/sandbox/sandbox-local/src/index.ts:30-34`, `:179`, `:368`, `:579-581`, `:596-598` | Landlock is the second Linux backend after bubblewrap. The provider probes it once, then wraps each confined command as `[launcher, grants..., --, argv...]` | `launcherPath`, `probe`, `LAUNCHER_BIN`, `LAUNCHER_FAILURE_EXIT` |
| `packages/sandbox/sandbox-local/src/profiles.ts:30-36` | Turns a sandbox policy into grants: read and execute on `/`; write on `/dev/null`, and in `workspace-write` mode on `/tmp` and each write root | `grantArgs` |
| `packages/sandbox/sandbox-local/src/index.ts:251-257` | Decides "the launcher failed, the command never ran": exit status 125 **and** a `landlock-run: ` line that is not the partial-enforcement notice | the CLI contract (`native/system/docs/cli-contract.md:22-30`) |
| `packages/session/session-persistence-jsonl/src/lease.ts:34`, `:90-116`; `packages/session/session-persistence-sqlite/src/lease.ts:36`, `:96` | The session write lock on POSIX: open `session.lock`, lock it without waiting, then check that the locked file is still the one at that path | `tryLockExclusive(fd)` |
| `packages/ssh/ssh-helper-runtime/src/index.ts:17-26` | The helper sent to a remote host carries `landlock-run` beside its single-file executable and installs a module-resolution hook so the entry package finds it there | `launcherPath` through a hook |
| `apps/desktop/scripts/flock-entry.ts:38-56` | A packaging script compiles the addon on demand when a checkout has not built it | `tryLockExclusive` |
| `packages/experimental/webworker-runtime/src/node/external_packages/node-addon-system-flock.ts:1-2` | A stand-in that always succeeds, for the single-process browser preview | none |

### 2.2 What the launcher does, step by step

1. Parses four flags: `--ro <path>`, `--rw <path>`, `--` and `--probe`. No other flag and
   no environment variable is read (`native/system/docs/cli-contract.md:12-18`).
2. Asks the kernel for its Landlock interface version. If the kernel has no Landlock or
   it is disabled, it stops (`main.c:231-236`).
3. Computes the access kinds to govern: thirteen from version 1, plus "refer" from
   version 2, "truncate" from 3 and "device ioctl" from 5. Version 5 is the newest it
   knows (`main.c:71-94`, `:185-191`).
4. Opens each granted path and adds a rule: read, read-directory and execute for `--ro`;
   everything governed for `--rw`. For a path that is not a directory it keeps only the
   kinds valid for a file, which is how `--rw /dev/null` works. A path that cannot be
   opened stops the launcher (`main.c:194-218`, `:244-252`).
5. Sets `no_new_privs`, the flag that stops a set-user-ID program from regaining
   privileges, then applies the ruleset to itself (`main.c:254-259`).
6. If the kernel's version is below 5 it prints one notice and continues: the command is
   still confined for everything that kernel can govern (`main.c:288-293`).
7. Replaces itself with the command, searching `PATH`, with the environment unchanged
   (`main.c:295`).

**How it reports failure.** Every failure before step 7, and a failed step 7, prints one
line starting `landlock-run: ` and exits with status 125. After a successful step 7 the
command's own status passes through, "including 125"
(`native/system/docs/cli-contract.md:22-23`). `--probe` runs steps 2 to 5 with a single
read-only grant on `/` in a short-lived process and prints `landlock: fully enforced` or
`landlock: partially enforced (older ABI)`; the JavaScript wrapper maps a non-zero exit,
a missing binary or a timeout to `unusable` (`main.c:269-283`;
`native/system/packages/entry/src/index.ts:116-127`).

The file is 298 lines of C using only the C library, linked statically against musl so
one binary serves glibc and musl systems (`main.c:30-35`;
`native/system/docs/support-matrix.md:10`).

### 2.3 What the lock binding does

One function: try an exclusive, non-waiting `flock` on a descriptor the caller owns, on a
worker thread, and hand back zero or the error number (`flock.c:55-59`). The other 100
lines exist because JavaScript has one thread: the call must not block it, the error
number must be captured on the thread that made the call, and a worker that is shut down
mid-call must not free memory still in use (`flock.c:61-94`;
`native/system/docs/architecture.md:15`). Windows does not use it
(`native/system/docs/support-matrix.md:14`); see [koffi.md](koffi.md), row 2.

## 3. Tech debt upstream carries

Search performed: `native/README.md`, `native/system/README.md`, all seven files under
`native/system/docs/`, both C sources, both TypeScript entries, the scripts and tests
directories, every import of `@deepseek-ai/node-addon-system` in `packages/` and `apps/`,
and `pnpm-workspace.yaml` (no pin, patch or exemption names this family). The 13 pin
units in this agent's assignment belong to koffi and `node-addon-require-builtin`.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L6.3` | About 460 lines of C need a build matrix of operating system by processor by C library (a static launcher, a glibc addon and a musl addon on Linux; an addon on macOS), four native CI runners, tests under four Node versions and in Alpine containers, five npm packages, a publish order, and 13 scripts to pack, verify and release them. A platform outside the matrix silently has no launcher and no lock | `native/system/docs/support-matrix.md:3-14`; `native/system/docs/architecture.md:23-25`; `native/system/docs/release.md:45-53`; `native/system/docs/packaging.md:20-26` | **Resolved** by [DH-ADR-NATIVESYSTEM-3](#dh-adr-nativesystem-3): there is no native artefact. The same Go source is compiled for every target by `go build` |
| `UP-DEBT-found.L6.4` | The remote SSH helper is a single-file Node executable, and the operating system cannot execute a launcher stored inside it. So `landlock-run` is shipped as a separate file beside the helper, and a module-resolution hook redirects the entry package to it | `packages/ssh/ssh-helper-runtime/src/index.ts:17-26` | **Resolved** by [DH-ADR-NATIVESYSTEM-4](#dh-adr-nativesystem-4): the launch stage is part of the one Go binary, on the remote host as on the local one |
| (owned by postmortem 0004) | Launcher failure is recognised from the command's exit status and standard error. A notice and a fatal line shared a prefix, and every failing command on an older kernel was reported as "sandbox unavailable" | `native/system/docs/cli-contract.md:22-30`; `docs/postmortem/0004-landlock-partial-notice-misclassified-child-failures.md:35` | Removed by [DH-ADR-SECURITY-3](../upstream/security.md#dh-adr-security-3): status travels on a separate descriptor. `DH-LIB-NATIVESYSTEM-R5` restates it for the stage |
| (not a separate unit) | A missing or unloadable addon makes every session write fail at run time, not at install | `native/system/README.md:27`; `native/system/docs/packaging.md:16-18` | Cannot occur: the lock is a system call compiled into the binary (`DH-LIB-NATIVESYSTEM-R11`) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NATIVESYSTEM-R1` | **Fail closed.** A command asked to run under Landlock either runs under a ruleset the kernel enforces or does not run. A kernel without Landlock, a ruleset error, a grant path that cannot be opened and a failed program start each prevent execution. | `main.c:23-28`, `:196-202`, `:231-236` |
| `DH-LIB-NATIVESYSTEM-R2` | **Grants.** Read, list and execute beneath each read-only root; every governed access kind beneath each read-write root; a grant on a non-directory keeps only file-valid kinds; everything not granted is denied. | `native/system/docs/cli-contract.md:12-15` |
| `DH-LIB-NATIVESYSTEM-R3` | **Inherited and irreversible.** Every descendant of the command is confined in the same way, and `no_new_privs` is set, so no set-user-ID program escapes. | `native/system/docs/cli-contract.md:34` |
| `DH-LIB-NATIVESYSTEM-R4` | **Honest completeness.** The stage reports `full` or `partial` against a fixed, named set of access kinds. `partial` means the kernel governs only some of them. | `native/system/packages/entry/src/index.ts:33-41`; `main.c:237` |
| `DH-LIB-NATIVESYSTEM-R5` | **Out-of-band status.** Whether the stage succeeded is never read from the command's exit status or output, and the command's exit status and output reach the caller unchanged, whatever their value. | `native/system/docs/cli-contract.md:22-23`; postmortem 0004 |
| `DH-LIB-NATIVESYSTEM-R6` | **Transparent hand-over.** The command keeps the stage's process ID, working directory, environment, standard streams and other inherited descriptors. | `main.c:295`; `native/system/docs/cli-contract.md:16` |
| `DH-LIB-NATIVESYSTEM-R7` | **Functional probe.** Availability is decided by really restricting a short-lived process once at start, under a time limit, not by reading a kernel version. | `main.c:269-276`; `native/system/packages/entry/src/index.ts:101-127` |
| `DH-LIB-NATIVESYSTEM-R8` | **No ambient choice of confiner.** Neither an environment variable nor the current directory can change which program applies the confinement. | `native/system/packages/entry/src/index.ts:12-14`, `:76-81` |
| `DH-LIB-NATIVESYSTEM-R9` | **Cheap start.** The stage reads no configuration, opens no store, starts no component, and adds a bounded delay per command. | Inference: the C launcher is a static program of a few kilobytes; a Go binary must earn the same property |
| `DH-LIB-NATIVESYSTEM-R10` | **POSIX write lock.** An exclusive, non-waiting lock on a descriptor the caller owns. Contention is a distinct "already owned" result. Closing the last descriptor, or the death of the process, releases it. After locking, the holder confirms the locked file is still the one at the lock path, and retries a bounded number of times if not. | `native/system/docs/flock-contract.md:9-22`; `packages/session/session-persistence-jsonl/src/lease.ts:14-19`, `:88-115` |
| `DH-LIB-NATIVESYSTEM-R11` | **Nothing to install.** No native file is built, published, selected or loaded at install or at run time; the capability cannot be absent on a supported platform. | `native/system/README.md:11`, `:27` |
| `DH-LIB-NATIVESYSTEM-R12` | **Same stage remotely.** A command run on a remote host over SSH is confined by the same stage, carried by the binary that is already there. | `packages/ssh/ssh-helper-runtime/src/index.ts:17-26` |

## 5. Go choice

### 5.1 Check of `DH-ADR-SECURITY-2` and `DH-ADR-SECURITY-3` against the C source

Those decisions say the Go binary re-executes itself as the launch stage, using
`go-landlock` on Linux. The table goes through everything the C launcher does and asks
whether Go can do it with cgo switched off.

| What the C launcher does | `main.c` | In Go without cgo | Same? |
|---|---|---|---|
| Parse four flags | `:146-182` | standard library | yes |
| Read the Landlock interface version | `:231` | `LandlockGetABIVersion` in go-landlock's `landlock/syscall` package | yes |
| Choose access kinds for that version, up to 5 | `:185-191`, `:237-238` | a table in the harness; `landlock.Config{HandledAccessFS: ...}` | yes; see DH-ADR-NATIVESYSTEM-2 |
| Create the ruleset; open each path with `O_PATH`; add a rule | `:194-218`, `:240-252` | `Config.NewRulesetFD(landlock.PathAccess(set, path))`; go-landlock opens the path the same way (`landlock/path_opt_linux.go:52`) | yes |
| Trim to file-valid kinds for a non-directory | `:203-209` | go-landlock does not do this; the stage inspects the path and chooses the kinds | **differs slightly:** C inspects the descriptor it then uses; Go inspects first and go-landlock opens again. If the path changes in between, the kernel rejects directory kinds on a file and the stage fails closed; file kinds on a directory grant less than asked |
| Stop on a path that cannot be opened | `:196-202` | go-landlock returns the error unless the rule is marked `IgnoreIfMissing` (`path_opt_linux.go:41-46`); the stage never marks it | yes |
| Set `no_new_privs`; apply the ruleset | `:254-259` | `unix.Prctl(PR_SET_NO_NEW_PRIVS, ...)`; `LandlockRestrictSelf(fd, flags)` | **differs:** both apply to the calling *thread* on kernels before interface version 8, and a Go program always has several threads. See below |
| Search `PATH`, replace the process, environment unchanged | `:295` | `exec.LookPath`, then `syscall.Exec(path, argv, os.Environ())` | nearly: Go refuses a `PATH` hit relative to the current directory; the C library retries a file with no interpreter line through `/bin/sh`, Go does not |
| Report by exit status 125 and a standard-error line | `:118-130`, `:288-293` | a record on a separate descriptor | replaced on purpose (DH-ADR-SECURITY-3) |
| `--probe` | `:269-283` | the same stage with a probe flag | yes |
| Be one static file with no C library dependency | (`native/system/scripts/build.ts:54-56`) | a `CGO_ENABLED=0` Go binary is statically linked and uses no C library | yes |
| Pass descriptors, limits and signal settings to the command | implicit | Descriptors pass. Go raises the open-file limit at start and `syscall.Exec` restores the original (`syscall/exec_unix.go:286-289`). Go keeps `SIGHUP` and `SIGINT` ignored if they were ignored at start (`go doc os/signal`); Inference: any other ignored signal returns to its default at the hand-over | nearly; `DH-E-NATIVESYSTEM-1` checks it |

**Result: nothing the C launcher does requires cgo in Go.** Seatbelt on macOS is an
external program (`sandbox-exec`), bubblewrap is an external program, and the Windows
backend is a set of Windows functions reachable without cgo ([koffi.md](koffi.md), rows
4 and 5). Two differences need real-kernel proof: the per-thread rule, and the details of
the hand-over.

**The per-thread rule.** The C launcher has one thread, so restricting "the calling
thread" restricts the process. A Go process has several. There are two sound answers.
(a) Apply the system call on every thread: `go-landlock`'s `Restrict` functions do this.
(b) Lock the goroutine to its thread, restrict that thread, and replace the process from
that same thread: the kernel keeps the calling thread's restrictions and discards the
other threads. go-landlock's own documentation describes (b) as the established way to
confine *another* program (`docs/raw_syscalls_in_go.md:140-153`).

### 5.2 The cgo question, verified

Read from the module cache for go-landlock v0.11.2 and `psx` v1.2.78:

- go-landlock's `go.mod` requires `kernel.org/pub/linux/libs/security/libcap/psx`
  v1.2.78, and its `landlock/syscall/allthreads_linux.go` imports it for answer (a).
- `psx` has two builds. Without cgo (`psx.go`, build constraint `linux && !cgo`) it is
  two short functions that call the standard library's `syscall.AllThreadsSyscall`. With cgo
  (`psx_cgo.go`, constraint `linux && cgo`) it compiles `psx.c` and `psx_calls.c`, which
  track threads and use a signal to make each one issue the call.
- The reason is in the standard library: `syscall.AllThreadsSyscall` "always returns
  ENOTSUP in binaries that use cgo" (`go doc syscall.AllThreadsSyscall`).
- The build tag `landlocktsync` removes the `psx` import, but it also sets the minimum
  interface version to 8 (`landlock/internal/min_abi_tsync.go`), and a lower version is
  then reported as "no Landlock" (`landlock/internal/abi.go`). The tag would switch
  Landlock off on every kernel older than version 8.

**Consequence for a `CGO_ENABLED=0` build:** none that costs anything. go-landlock and
`psx` compile as pure Go; no C compiler is needed; the binary stays static and
cross-compiles. The module graph gains `psx`, whose licence is "BSD-3-Clause OR
GPL-2.0-only": the harness takes it under BSD-3-Clause, and the licence inventory must
say so.

**Consequence for a build with cgo on.** No shipped artifact is built that way: the
`dsh` binary is pure Go ([DH-ADR-15](../design.md#dh-adr-15)) and there is no desktop
build ([DH-ADR-18](../design.md#dh-adr-18)). One build still has cgo on: a test binary
built with the race detector, which needs it
([DH-ADR-TOOLCHAIN-2](../upstream/toolchain.md#dh-adr-toolchain-2)), including the
`dsh` binary that replay tests start. In that build C code from `psx` is compiled and
linked on Linux. The runner already has a C toolchain, so the cost is a linked C object,
not a new build requirement. With answer (b) the stage never calls `psx`, so its
behaviour is the same in both builds; the C code is present and idle.

### 5.3 Decisions

<a id="dh-adr-nativesystem-1"></a>
#### DH-ADR-NATIVESYSTEM-1 Build the ruleset with go-landlock; enforce it on one locked thread; then replace the process

- **Decision.** Refines DH-ADR-SECURITY-3. On Linux the launch stage (1) builds the
  ruleset with `landlock.Config.NewRulesetFD`, which validates and creates it without
  enforcing; (2) calls `runtime.LockOSThread` and never unlocks; (3) sets `no_new_privs`
  with `unix.Prctl`; (4) applies the ruleset with go-landlock's `LandlockRestrictSelf`,
  without the all-threads flag; (5) writes its status record; (6) calls `syscall.Exec`.
  It does not call `Restrict`, `RestrictPaths` or anything else that reaches `psx`. The
  `landlocktsync` build tag is forbidden.
- **Why.** It is the C launcher's own sequence, it is the method go-landlock documents
  for confining another program, and it behaves identically in the pure-Go release
  build ([DH-ADR-15](../design.md#dh-adr-15)) and in a race-detector test build, where
  cgo is on. go-landlock still supplies what is easy to get wrong: structure layouts,
  validation and the knowledge of which access kind arrived in which version.
- **Tradeoff.** Correctness rests on steps 3, 4 and 6 running on one thread, which no
  compiler checks. A future edit that inserts a goroutine hand-off would confine the
  wrong thread and still appear to work. The guard is `DH-E-NATIVESYSTEM-1` and `-2` on
  real kernels plus a comment contract in the one function that holds the sequence.
- **Alternative kept open.** go-landlock's documentation states that from Go 1.28 the
  standard library can apply a ruleset to a child between fork and exec
  (`landlock/config.go:660-676`; `docs/raw_syscalls_in_go.md:137-138`). Go 1.27.4, used
  for this check, has no such field (`GOOS=linux go doc syscall.SysProcAttr`). If the
  harness's Go floor reaches a version that has it, Landlock needs no launch stage at
  all. Not verified; recorded as question 1 in section 7.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** `DH-T-NATIVESYSTEM-2`, `DH-T-NATIVESYSTEM-4`, `DH-E-NATIVESYSTEM-1`,
  `DH-E-NATIVESYSTEM-2`, `DH-E-NATIVESYSTEM-4`.

<a id="dh-adr-nativesystem-2"></a>
#### DH-ADR-NATIVESYSTEM-2 A fixed reference set, strict creation, and three explicit refusals

- **Decision.** The harness owns a table of filesystem access kinds per Landlock
  interface version, up to a named **reference version** (5 at first, as upstream). The
  stage reads the kernel's version first and (a) refuses if it is below 1; (b) asks for
  exactly the kinds of `min(kernel, reference)` in a config that is **not** best-effort,
  so a kernel that cannot honour it is an error; (c) refuses if `NewRulesetFD` returns
  no descriptor. It reports `full` when the kernel's version is at least the reference
  and `partial` otherwise. Network and scoping kinds are not requested here; they arrive
  with [DH-ADR-SECURITY-10](../upstream/security.md#dh-adr-security-10) as a new
  reference set.
- **Why.** go-landlock's convenient path is the unsafe one for a sandbox: a best-effort
  config "will succeed without error even when Landlock is not available at all"
  (`landlock/config.go:325-326`), and its ready-made version configs grow with each
  release, which would silently move the meaning of `full`. Upstream fails closed
  (`main.c:231-236`) and pins its ceiling (`main.c:94`).
- **Tradeoff.** The table is maintained by hand, as upstream's is. A newer kernel
  feature is not used until the reference is raised in a reviewed change.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** `DH-T-NATIVESYSTEM-1`, `DH-T-NATIVESYSTEM-2`, `DH-E-NATIVESYSTEM-2`,
  `DH-E-NATIVESYSTEM-3`.

<a id="dh-adr-nativesystem-3"></a>
#### DH-ADR-NATIVESYSTEM-3 No native artefact family; the lock is a system call in the process

- **Decision.** Drop the `native/system` workspace: its C sources, platform packages,
  build matrix, pack verification and release procedure. The launch stage is Go code in
  the harness binary. The POSIX lock is `syscall.Flock(fd, LOCK_EX|LOCK_NB)` from the
  standard library, called directly on the calling goroutine; the Windows lock is in
  [koffi.md](koffi.md). This is the final disposition of upstream's decision "Prebuilt
  system primitives": its goal, no compiler at install time, is kept and met by the
  language; its mechanism is not carried.
- **Why.** Every cost in `UP-DEBT-found.L6.3` comes from shipping compiled C to an
  interpreter. The addon's asynchronous machinery exists because JavaScript has one
  thread (section 2.3); a non-waiting system call on a goroutine blocks nothing.
- **Tradeoff.** The launcher is no longer a 298-line C file that can be audited alone:
  the stage's code is small, but it runs inside the Go runtime. DH-ADR-NATIVESYSTEM-4
  keeps the stage's own code path short and separately testable.
- **What the Go project still builds per platform.** No native code. One `go build` per
  target (Linux, macOS and Windows on amd64 and arm64,
  [DH-ADR-TOOLCHAIN-13](../upstream/toolchain.md#dh-adr-toolchain-13)), all from one
  machine, with no glibc or musl split and no dimension for the runtime version. What
  remains per platform is *source* (files selected by build constraint for Landlock,
  `sandbox-exec` and the Windows token) and *proof*: real runners for each operating
  system, a kernel range for Landlock, and a musl-based distribution to show the static
  binary runs there. Two things outside this family may still need a native runner and
  belong to their own specifications: code signing and notarisation, and the
  race-detector test jobs. A SQLite driver that needs cgo is not one of them: it is
  ruled out for `dsh` ([DH-ADR-15](../design.md#dh-adr-15), [sqlite.md](sqlite.md)).
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-5](../design.md#dh-o-5).
- **Verified by.** `DH-T-NATIVESYSTEM-4`, `DH-T-NATIVESYSTEM-6`, `DH-E-NATIVESYSTEM-4`.

<a id="dh-adr-nativesystem-4"></a>
#### DH-ADR-NATIVESYSTEM-4 The stage lives in the `dsh` binary, is dispatched first, and has a start-up budget

- **Decision.** `dsh sandbox-launch` is recognised in `main` before flags, configuration,
  logging or any component is touched, and its package imports only the standard
  library, `golang.org/x/sys` and go-landlock. The host finds its own binary once at
  start with `os.Executable` and uses that absolute path for every launch; no
  environment variable and no relative path can change it. The same binary is the remote
  SSH helper, so a remote host has the stage without a second file. A benchmark sets a
  budget for the stage's added delay. If the budget cannot be met inside the full binary,
  the fallback is a second, small pure-Go binary built from the same module; that costs
  one more artefact per platform but still no native build.
- **Why.** `DH-LIB-NATIVESYSTEM-R8`, `-R9` and `-R12`. Inference: a Go binary runs every
  linked package's initialisation before `main`, so the delay depends on the whole
  binary, not only on the stage; it has to be measured, not assumed.
- **Tradeoff.** Package initialisers across the harness must stay free of input and
  output, a rule the security reference already asks for
  ([../upstream/security.md](../upstream/security.md), section 7). The binary must be
  readable and executable inside the sandbox it sets up.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14).
- **Verified by.** `DH-T-NATIVESYSTEM-3`, `DH-T-NATIVESYSTEM-5`, `DH-T-NATIVESYSTEM-7`.

### 5.4 Options compared

**Landlock.**

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **go-landlock for the ruleset, own locked-thread enforcement (chosen)** | Maintained by Landlock contributors; tested against a kernel matrix; same behaviour with and without cgo | Pulls `psx` into the module graph; one-thread rule is a convention | `github.com/landlock-lsm/go-landlock` v0.11.2, released 2026-11-10, MIT (`LICENSE` read); its `go.mod` requires `golang.org/x/sys` v0.49.0 or later and `psx` v1.2.78 (`go.mod` read); the harness builds with x/sys v0.50.0, the one version every specification names; `NewRulesetFD`, `PathAccess`, `LandlockRestrictSelf`, `LandlockGetABIVersion` found in source |
| go-landlock `RestrictPaths` (all threads), then exec | Fewest lines; officially supported entry point | With cgo it runs the C signal mechanism; without care, best-effort mode fails open | same module |
| Raw system calls through `golang.org/x/sys/unix` only | No extra module; mirrors the C file line for line | The harness maintains structure sizes and the version table alone; go-landlock's maintainers ask projects not to (`docs/raw_syscalls_in_go.md:3-13`) | x/sys v0.50.0 has `SYS_LANDLOCK_*`, `LandlockRulesetAttr`, `LandlockPathBeneathAttr`, `LANDLOCK_ACCESS_FS_*`, `Prctl`, `PR_SET_NO_NEW_PRIVS`; it has no wrapper functions |
| Keep a C launcher | Smallest audited surface | One native build per Linux architecture and a second shipped file; contradicts DH-ADR-TOOLCHAIN-13 | not applicable |

**POSIX lock.**

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Standard library `syscall.Flock` (chosen)** | No dependency; the exact call upstream makes | Advisory; unreliable on some network filesystems, as upstream documents (`native/system/docs/flock-contract.md:34`) | `GOOS=linux` and `GOOS=darwin` `go doc syscall.Flock` |
| `github.com/gofrs/flock` | One API over POSIX and Windows locks | A dependency for about forty lines; hides the inode check the harness must do anyway | v0.13.2, released 2026-11-08; licence file present, terms not read in full |
| Open-file-description locks through `fcntl` | Per-descriptor semantics | Upstream measured that they do not exclude a `flock` holder (`2026-09-07-prebuilt-system-primitives.md:27`); no benefit here | not evaluated |

**Tradeoff of the whole choice.** A one-file C program is replaced by a code path inside
a large binary. The gain is one artefact, one language and no release pipeline for native
code; the price is that isolation of the stage is kept by discipline and tests, not by
being a separate program.

## 6. Design notes

**Owner.** The stage's code is a small internal package used by `cmd/dsh`
([../design.md](../design.md), section 4.2); the sandbox backends in the world family
start it. The lock is a function of the session-store family. Names are for the areas to
fix.

**Sequence of one confined start under Landlock.**

1. The sandbox seam creates a pipe and starts
   `<self> sandbox-launch --status-fd N <grants> -- <argv>` with the write end as
   descriptor `N` and the scrubbed environment
   ([DH-ADR-SECURITY-6](../upstream/security.md#dh-adr-security-6)).
2. The stage marks descriptor `N` close-on-exec, so the kernel closes it at the
   hand-over and the command can never write to it, then runs DH-ADR-NATIVESYSTEM-1
   steps 1 to 4.
3. It writes one record, `confined` with backend and `full` or `partial`, and calls
   `syscall.Exec`. A failed `Exec` writes `exec-failed` with the error and exits.
4. The seam reads the pipe to end of file. A `confined` record followed by end of file
   means the command is running. End of file with no record, or `exec-failed`, means the
   command never ran.

**Failure half-way.** Before step 3 nothing outside the stage's own process has changed:
Landlock restrictions apply only to that process, so there is nothing to roll back; the
process exits and the kernel discards the ruleset. After `Exec` succeeds the stage no
longer exists and cannot report anything. One state is uncertain by nature: if the host
dies between starting the stage and reading the record, the command may be running with
nobody waiting for it. The process-group and job ownership rules of the subprocess area
cover that case, not this stage.

**Retry.** The stage never retries. The seam may start a new stage; a refusal for a
missing kernel feature is permanent for the host's lifetime and is cached with the probe.

**The lock.** `Lock(path)` opens the lock file, calls `syscall.Flock` with
`LOCK_EX|LOCK_NB`, maps `EWOULDBLOCK` to "already owned", compares the locked
descriptor's device and inode with the path's, retries up to three times on a mismatch
as upstream does (`lease.ts:88-115`), and returns a handle whose `Close` closes the
descriptor. The descriptor is opened close-on-exec (Go's default), so no child inherits
the lock; upstream's contract notes that an inherited descriptor keeps a lock alive
(`native/system/docs/flock-contract.md:20`). The session's write fiber owns the handle
and closes it on unload. The lock file is never deleted, so the inode stays stable
(`lease.ts:119-123`).

## 7. Risks and open questions

1. **Go 1.28 and Landlock in the standard library.** If confirmed when that release is
   available, the Landlock backend can drop the launch stage. bubblewrap and
   `sandbox-exec` would still run the stage for the status record, so the stage itself
   stays. Owner: sandbox area.
2. **The status descriptor through bubblewrap and `sandbox-exec`.** DH-ADR-SECURITY-3
   assumes both pass an inherited descriptor to the program they start. Not verified
   here; it needs a real run on each. Owner: sandbox area.
3. **Shared `/tmp`.** Under Landlock upstream grants the host's `/tmp` for writing
   (`packages/sandbox/sandbox-local/src/profiles.ts:31-35`), unlike bubblewrap's private
   one. That is a policy question for the sandbox area, not a property of the stage.
4. **`psx` licence wording.** Dual licence; the BSD-3-Clause choice must appear in the
   third-party notice.
5. **Hand-over details.** `PATH` search rules and ignored signals differ slightly from
   the C library (section 5.1). If a real case depends on the `/bin/sh` retry, the stage
   adds it explicitly, as upstream's own runner does
   (`packages/subprocess/subprocess-local/src/spawn-runner.ts:139-144`).
6. **Old kernels in CI.** Proving `partial` needs kernels with interface versions 1 to 4.
   Upstream's incident 0004 happened because such tests skipped themselves
   (`docs/postmortem/0004-landlock-partial-notice-misclassified-child-failures.md:37`).
   Owner: toolchain and sandbox areas.
7. **Advisory locks on network filesystems.** Unchanged from upstream; the session-store
   area states whether such a home directory is supported.

## 8. Verification

Nothing here has been run. Local tests use a substitute at the system-call boundary or a
pure function and prove logic. Scenarios run the built binary on a real kernel and prove
enforcement; a scenario that is skipped on the platform it exists for is open, never a
pass ([DH-ADR-SECURITY-11](../upstream/security.md#dh-adr-security-11)).

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-NATIVESYSTEM-1` | local | `DH-LIB-NATIVESYSTEM-R2`, `-R4`, `DH-ADR-NATIVESYSTEM-2` | Table test of the access-kind function for versions 0 to 9: the set for each version, the file-only trim, and `full` or `partial` |
| `DH-T-NATIVESYSTEM-2` | local, substitute system calls | `DH-LIB-NATIVESYSTEM-R1`, `-R5`, `DH-ADR-NATIVESYSTEM-1` | Inject a failure at each step (version query, ruleset, path open, `no_new_privs`, restrict, exec): exec is never reached after an earlier failure, and the status descriptor holds the expected record or nothing. Assert the call order and that all three thread-bound calls happen after the thread lock |
| `DH-T-NATIVESYSTEM-3` | local | `DH-LIB-NATIVESYSTEM-R9`, `DH-ADR-NATIVESYSTEM-4` | An import-graph test: the stage package imports only the allowed modules; a start with a deliberately broken configuration file and an unwritable home still reaches the stage |
| `DH-T-NATIVESYSTEM-4` | local, build | `DH-LIB-NATIVESYSTEM-R11`, `DH-ADR-NATIVESYSTEM-3`, `DH-ADR-NATIVESYSTEM-1` | `CGO_ENABLED=0 GOOS=linux` builds for amd64 and arm64 succeed on a machine with no C compiler; the result is statically linked; no harness package imports `C`; the `landlocktsync` tag is not set; no call to go-landlock's `Restrict*` functions exists in the module |
| `DH-T-NATIVESYSTEM-5` | local, benchmark | `DH-LIB-NATIVESYSTEM-R9` | Time from starting `dsh sandbox-launch -- /bin/true` (no backend) to exit, against the budget, on the release binary |
| `DH-T-NATIVESYSTEM-6` | local, Linux and macOS | `DH-LIB-NATIVESYSTEM-R10` | Upstream's lock contract as Go tests with real descriptors and child processes: second opener is refused at once; another file is unaffected; closing an unrelated descriptor keeps the lock; a killed holder releases it; a shared-lock holder conflicts; a replaced lock file is detected by the inode check |
| `DH-T-NATIVESYSTEM-7` | local | `DH-LIB-NATIVESYSTEM-R8` | With hostile `PATH`, working directory and `DSH_*` variables set, the stage path the seam uses is still the absolute path of the running binary |

<a id="dh-e-nativesystem-1"></a>
### DH-E-NATIVESYSTEM-1 Landlock confines a command and its children on a current kernel

- **Given** a Linux runner whose kernel reports Landlock interface version 5 or higher,
  with bubblewrap made unavailable; the release `dsh` binary built with `CGO_ENABLED=0`;
  a fresh temporary directory `W` and a path `D` outside it.
- **When** the sandbox seam runs, in `workspace-write` mode with `W` as the write root:
  (1) a shell that writes `W/ok`; (2) a shell that writes `D`; (3) a shell that starts a
  second shell that writes `D`; (4) a set-user-ID helper that tries to write `D`; (5) a
  shell that prints its process ID, its environment and its ignored signals.
- **Then** (1) succeeds and `W/ok` exists; (2), (3) and (4) fail and `D` does not exist;
  each start produced a `confined, landlock, full` record; in (5) the process ID equals
  the ID the seam started, the environment equals the scrubbed one, and the ignored
  signals are recorded and compared with a direct start.
- **Cleanup** remove `W`. If removal fails, keep the directory listing and the stage
  records as evidence.
- **Proves** `DH-LIB-NATIVESYSTEM-R1`, `-R2`, `-R3`, `-R6`, `DH-ADR-NATIVESYSTEM-1`.

<a id="dh-e-nativesystem-2"></a>
### DH-E-NATIVESYSTEM-2 An older kernel is reported partial and still confines; child results are never read as launcher failure

- **Given** a Linux virtual machine whose kernel reports interface version 1, 2, 3 or 4
  (one run per available version); the same binary; a temporary directory `W`.
- **When** the seam runs commands that exit 0, 1, 2 and 125, one that prints
  `landlock-run: fatal` and `dsh sandbox-launch: failed` to standard error and exits 1,
  and one that writes outside `W`.
- **Then** every start records `confined, landlock, partial`; each exit status and each
  standard-error text reaches the caller unchanged and is reported as the command's own
  result; the outside write fails and leaves no file.
- **Cleanup** remove `W`; keep the kernel version and records.
- **Proves** `DH-LIB-NATIVESYSTEM-R4`, `-R5`, `DH-ADR-NATIVESYSTEM-2`.

<a id="dh-e-nativesystem-3"></a>
### DH-E-NATIVESYSTEM-3 Without Landlock the stage refuses and the command never runs

- **Given** a Linux virtual machine booted with Landlock disabled, and a second case
  with a grant path that does not exist; a marker path `M`.
- **When** the probe runs, and then a command that would create `M` is started through
  the Landlock backend directly.
- **Then** the probe reports the backend unusable; the start returns an error naming the
  reason; the status descriptor reached end of file with no `confined` record; `M` does
  not exist.
- **Cleanup** none needed; assert `M` is absent.
- **Proves** `DH-LIB-NATIVESYSTEM-R1`, `-R7`, `DH-ADR-NATIVESYSTEM-2`.

<a id="dh-e-nativesystem-4"></a>
### DH-E-NATIVESYSTEM-4 The stage behaves the same in both build modes and on a musl system

- **Given** two builds from one commit: the release build with `CGO_ENABLED=0`, and a
  build with cgo on and the race detector enabled, which is what replay tests start;
  a glibc-based and a musl-based Linux runner.
- **When** scenario 1 is run with each build on the glibc runner, and with the pure-Go
  build on the musl runner.
- **Then** all three runs give the same results and records as scenario 1; the pure-Go
  binary starts on the musl system without any added library.
- **Cleanup** as scenario 1.
- **Proves** `DH-LIB-NATIVESYSTEM-R11`, `DH-ADR-NATIVESYSTEM-1`, `DH-ADR-NATIVESYSTEM-3`.

## 9. Units dispositioned by this spec

- `UP-PKG-native.system`: replaced (the workspace, its build and its release are not carried).
- `UP-PKG-native.system.packages.entry`: replaced by the launch stage and the lock function.
- `UP-DEC-implemented.architecture.2026-09-07-prebuilt-system-primitives`: replaced; final
  disposition in DH-ADR-NATIVESYSTEM-3.
- `UP-DEBT-found.L6.3`, `UP-DEBT-found.L6.4`: defined here and resolved.

Go-side items declared: `DH-LIB-NATIVESYSTEM`, `DH-LIB-GOLANDLOCK`,
`DH-ADR-NATIVESYSTEM-1` to `-4`, `DH-T-NATIVESYSTEM-1` to `-7`, `DH-E-NATIVESYSTEM-1` to `-4`.
