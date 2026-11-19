# koffi: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Module and standard-library facts were checked on 2026-11-18
> with Go 1.27.4 (`go list -m -json`, `go mod download -json`, the `LICENSE` file and the
> source in the module cache, and `go doc`).

| | |
|---|---|
| **Library ID** | `DH-LIB-KOFFI` |
| **Upstream unit** | `UP-DEP-koffi` |
| **Upstream version** | 3.1.1, pinned exactly in nine manifests (for example `packages/fs/fs-local/package.json:41`) |
| **Kind** | runtime, native (a prebuilt addon per operating system and processor) |
| **Used by** | fs, host, sandbox, session, subprocess, apps |
| **Go choice** | named Go module `golang.org/x/sys` plus the standard library; no foreign-function layer |
| **Related areas** | session store (A05), filesystem (A08), subprocess and terminals (A09), sandbox (A10); [../upstream/security.md](../upstream/security.md); [../upstream/session-and-persistence.md](../upstream/session-and-persistence.md); [node-addon-system.md](node-addon-system.md) |

## 1. What it is and why upstream needs it

A **foreign-function interface** (FFI) lets a program written in one language call a
function compiled in another. koffi is an FFI for Node: JavaScript names a system
library (`kernel32.dll`, or the C library), declares a function's argument and result
types as strings, and koffi builds the machine-level call at run time.

Upstream needs it because Node's own API stops short of the operating system in a few
places that matter to an agent harness: renaming a file so that the rename is on disk
before the call returns, holding a lock that the kernel releases when the process dies,
starting a process under a restricted identity inside a job object, editing access-control
lists, walking the process table, and replacing the current process with another program.
Each of these is one or more operating-system functions that Node does not expose.
Almost all of them are Windows functions; one use is on Linux.

Go does not have this gap. The Go runtime calls operating-system functions directly:
on Linux it issues system calls without the C library, and on Windows it loads system
libraries and calls their functions without a C compiler. `golang.org/x/sys` is the Go
project's own package of typed wrappers for those functions. So in Go the *requirements*
remain (they are operating-system behaviour), but the *library category* disappears.

## 2. How upstream uses it

Every import was read. "In Go" names the exact call: **x/sys** means
`golang.org/x/sys/windows` or `.../unix` v0.50.0 and the symbol was found in that
version's source; **stdlib** means the Go 1.27.4 standard library; **bind** means the
function is absent from x/sys and is bound once with `windows.NewLazySystemDLL(...).NewProc(...)`,
which needs no C compiler.

| # | Use site (`path:line`) | OS functions called | Why upstream calls them | In Go |
|---|---|---|---|---|
| 1 | `packages/session/session-persistence-jsonl/src/win32.ts:56-65`, `:134-138`; a byte-for-byte second copy at `packages/session/session-persistence-sqlite/src/win32.ts:58-67` | `MoveFileExW` with `MOVEFILE_WRITE_THROUGH` | **Session durability.** A new session file, and each new directory above it, is created under a temporary name and renamed to its final name. Windows has no "sync the directory" call, so the rename itself must not return until it is on disk, must fail if the name exists, and must never fall back to a copy across volumes (`:4-9`, `:128-133`) | x/sys `windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)`. Stdlib `os.Rename` is not a substitute: on Windows it passes only `MOVEFILE_REPLACE_EXISTING` (`internal/syscall/windows/syscall_windows.go:366`), which replaces and does not write through |
| 2 | same files, `:151-173` | `CreateSemaphoreW`, `WaitForSingleObject` (timeout 0), `ReleaseSemaphore`, `CloseHandle` | **Session write lock.** A named kernel semaphore, `Local\dsh-session-lock-<hash of the lock path>`, with count 1. A second writer's zero-timeout wait fails at once. The object dies with its last handle, so a crashed writer never blocks a successor. A semaphore was chosen over a file lock so that readers, search and directory removal are not blocked by an open handle (`:141-147`) | x/sys `windows.LockFileEx(h, LOCKFILE_EXCLUSIVE_LOCK\|LOCKFILE_FAIL_IMMEDIATELY, ...)` on a dedicated lock file, which is the form [DH-ADR-PERSISTENCE-4](../upstream/session-and-persistence.md#dh-adr-persistence-4) asks for. If a lock with no file is required, bind `CreateSemaphoreW` and `ReleaseSemaphore` (x/sys has neither; it has `WaitForSingleObject` and `CloseHandle`). See section 7, question 1 |
| 3 | `packages/fs/fs-local/src/win32.ts:46-58`, called from `packages/fs/fs-local/src/fsio.ts:641-666` | `GetFileSecurityW`, `SetFileSecurityW`, `ReplaceFileW` | **Atomic file replacement that keeps permissions.** The file tools write a staging file and swap it in. Before writing, the target's access-control list is copied onto the empty staging file and protected from inheritance, so the content is never readable by more accounts than the target was; `ReplaceFileW` then swaps the files and keeps the target's security descriptor (`:103-134`) | x/sys `windows.GetNamedSecurityInfo` then `sd.DACL()` then `windows.SetNamedSecurityInfo(path, SE_FILE_OBJECT, DACL_SECURITY_INFORMATION\|PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)`. `ReplaceFileW` is absent from x/sys: bind it, or use `windows.MoveFileEx` with `MOVEFILE_REPLACE_EXISTING\|MOVEFILE_WRITE_THROUGH` after the list is copied (upstream already falls back to a plain rename when the target vanished, `fsio.ts:661-665`) |
| 4 | `packages/subprocess/win32-process/src/ffi.ts:272-314` | `CreateProcessW`, `CreateProcessAsUserW`, `CreatePipe`, `SetHandleInformation`, `ReadFile`, `PeekNamedPipe`, `GetFileType`, `GetStdHandle`, `WaitForSingleObject`, `GetExitCodeProcess`, `TerminateProcess`, `ResumeThread`, `CreateJobObjectW`, `SetInformationJobObject`, `QueryInformationJobObject`, `AssignProcessToJobObject`, `TerminateJobObject`, `FormatMessageW`, `GetLastError`, `CloseHandle`, and `uv_get_osfhandle` taken from the Node executable itself (`:277`, `:311`) | **Process start under a chosen identity, in a job.** Node cannot start a process with another access token, start it suspended, or place it in a job object before it runs. A **job object** is the Windows object that ends a whole process tree when its last handle closes. The target is created suspended, assigned to the job, then resumed, so no target code runs outside the job (`packages/subprocess/win32-process/README.md:30-31`) | Stdlib `os/exec` with `syscall.SysProcAttr{Token, CreationFlags, HideWindow}` creates the process and its pipes. x/sys `windows.CreateJobObject`, `SetInformationJobObject`, `AssignProcessToJobObject`, `QueryInformationJobObject`, `TerminateJobObject`, `ResumeThread`. **Limit found:** the standard library closes the new process's main-thread handle (`syscall/exec_windows.go:446`), so "create suspended, assign, resume" needs x/sys `windows.CreateProcessAsUser` or `windows.CreateProcess` called directly, which return that handle. `PeekNamedPipe`, `uv_get_osfhandle`, `FormatMessageW` and `GetLastError` have no Go counterpart to write: a goroutine blocks on the read, `os.File.Fd` gives the handle, and the error value carries the code and its text |
| 5 | `packages/sandbox/sandbox-windows-acl/src/ffi.ts:233-279` | Token: `OpenProcess`, `OpenProcessToken`, `GetTokenInformation`, `SetTokenInformation`, `CreateRestrictedToken`. Identifiers: `ConvertStringSidToSidW`, `CreateWellKnownSid`, `IsValidSid`, `GetLengthSid`, `CopySid`, `LocalAlloc`, `LocalFree`. Lists: `SetEntriesInAclW`, `InitializeAcl`, `AddMandatoryAce`, `GetNamedSecurityInfoW`, `SetNamedSecurityInfoW`. Other: `GetTempPathW`, `SetEnvironmentVariableW`, `SetConsoleCtrlHandler`, `CreateFileW`, `LockFileEx`, `UnlockFileEx` | **The Windows sandbox.** The caller's token is copied as a write-restricted, Low-integrity token; each granted directory gets an allow entry, a deny of delete-child and a Low label; a lock file serialises list edits between processes (`packages/sandbox/sandbox-windows-acl/README.md:98`, `src/acl.ts:85-123`) | x/sys `windows.OpenCurrentProcessToken`, `DuplicateTokenEx`, `SetTokenInformation`, `Token` methods, `StringToSid`, `CreateWellKnownSid`, `SID` methods, `ACLFromEntries` (wraps `SetEntriesInAclW`), `GetNamedSecurityInfo`, `SetNamedSecurityInfo`, `CreateFile`, `LockFileEx`, `UnlockFileEx`. The Low label can be built with `windows.SecurityDescriptorFromString` and `sd.SACL()` in place of `InitializeAcl` and `AddMandatoryAce`, which x/sys lacks. **`CreateRestrictedToken` is absent from x/sys: bind it** (confirms [DH-ADR-SECURITY-3](../upstream/security.md#dh-adr-security-3)). The environment and temp-path calls become `exec.Cmd.Env` and `os.TempDir` |
| 6 | `packages/subprocess/subprocess-local/src/windows-inspector.ts:242-268` | `CreateToolhelp32Snapshot`, `Process32FirstW`, `Process32NextW`, `OpenProcess`, `GetProcessTimes`, `WaitForSingleObject`, `CloseHandle` | **Process tree and identity.** Terminal teardown must find a shell's descendants and must not signal a reused process ID, so each process is identified by ID plus creation time, and liveness is a zero-time wait on its handle (`:1-10`, `:28-34`) | All in x/sys: `windows.CreateToolhelp32Snapshot`, `Process32First`, `Process32Next`, `ProcessEntry32`, `OpenProcess`, `GetProcessTimes`, `WaitForSingleObject` |
| 7 | `packages/subprocess/subprocess-local/src/linux-execve.ts:53-92`, called from `src/spawn-runner.ts:133-145` | C library `execve` and `fcntl` (clear close-on-exec on descriptors 0, 1, 2 and the control descriptor; clear non-blocking on the child's pipes) | **Become the target.** A small runner process is started inside a systemd scope and then replaces itself with the real command, so the command keeps the runner's process ID and scope. Node has no "replace this process" call | Stdlib `syscall.Exec`. Descriptors passed through `exec.Cmd.ExtraFiles` arrive without close-on-exec, so the clearing step is normally not needed; where it is, x/sys `unix.FcntlInt` or stdlib `syscall.SetNonblock` |
| 8 | `packages/host/directory-picker-native/src/win32-dialog-bindings.ts:94-183`, `:191-206` | `CoInitializeEx`, `CoCreateInstance`, `CoTaskMemFree`, `CoUninitialize`; calls through the function table of the COM objects `IFileOpenDialog` and `IShellItem`; `SetThreadDpiAwarenessContext`, `keybd_event`, `EnumThreadWindows` (with a callback), `PostMessageW`, `GetCurrentThreadId` | **Native "choose a folder" dialog** shown from a child process, closable from the parent by posting a close message to the dialog thread's windows | Dropped: there is no desktop shell ([DH-ADR-18](../design.md#dh-adr-18)), and the folder is chosen in a server-rendered directory picker in the browser ([../areas/host-telemetry.md](../areas/host-telemetry.md), `DH-ADR-HOST-8`; [../areas/ui-sidebar-workspace.md](../areas/ui-sidebar-workspace.md#dh-adr-uiside-8), `DH-ADR-UISIDE-8`). No native dialog is shown and nothing here is bound |
| 9 | `apps/desktop-host/src/windows-cli-signals.ts:9-23` | `SetConsoleCtrlHandler` with a callback | Deliver Ctrl-C and Ctrl-Break to the command line when it runs inside Electron's Node mode, which installs no console handler | Stdlib `os/signal`: `signal.Notify(c, os.Interrupt)` receives both on Windows (`go doc os/signal`, section "Windows"). The cause (Electron) is gone |
| 10 | `apps/desktop/scripts/windows-signing-stage.mjs:8-19` (build script; the one development-only consumer) | `CreateFileW` with share mode 0, `CloseHandle`, `GetLastError` | A machine-wide lock so that two code-signing stages never use the signing hardware at once | Release tooling, not the product. If kept: x/sys `windows.CreateFile` with share mode 0, or `LockFileEx` |
| 11 | `packages/experimental/webworker-runtime/src/node/external_packages/koffi.ts:1-155` | none: a stub whose every call refuses | Lets the browser-worker preview import packages that mention koffi | Not applicable: there is no second JavaScript host to stub for |

Two more facts shape the requirements. Upstream loads koffi lazily everywhere, so that a
Linux process never opens a Windows library (`packages/session/session-persistence-jsonl/src/win32.ts:53`).
And upstream deliberately did **not** use koffi for the POSIX file lock: a synchronous
call would block the event loop, and after an asynchronous call the error number is read
on the wrong thread (`.agents/notes/implemented/architecture/2026-09-07-prebuilt-system-primitives.md:29`).
That lock became a C addon instead; see [node-addon-system.md](node-addon-system.md).

## 3. Tech debt upstream carries

Search performed: every `koffi` import and every comment naming koffi in `packages/`,
`apps/`, `native/` and `scripts/`; `patches/` (no patch for koffi); the koffi entries of
`pnpm-workspace.yaml`; the "Known Limitations and Deferred Work" sections of
`packages/subprocess/win32-process/README.md`, `packages/sandbox/sandbox-windows-acl/README.md`,
`packages/subprocess/subprocess-local/README.md` and `packages/host/directory-picker-native/README.md`.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.allowBuilds.koffi` | Two things in one entry. (a) koffi's install script is allowed to run, because without a prebuilt binary it compiles from source. (b) Every consumer is pinned to exactly 3.1.1, because the source build of 3.3.2 fails under GCC 13; all nine manifests must be raised together after a packed-install check | `pnpm-workspace.yaml:42-46`; `packages/fs/fs-local/package.json:41` | **Resolved** by `DH-LIB-KOFFI`. Downloading a Go module runs no code and compiles nothing native. `golang.org/x/sys` is plain Go source built by the Go compiler for every target, so there is no prebuilt binary, no source-build fallback and no compiler-version failure. One `go.mod` line replaces nine pins ([DH-ADR-TOOLCHAIN-11](../upstream/toolchain.md#dh-adr-toolchain-11)) |
| `UP-DEBT-found.L6.1` | Workarounds in calling code for koffi's behaviour, not for Windows': a zeroed 32-byte record is passed to `LockFileEx` because koffi 3.1.1 crashes on a null pointer; the sandbox runner changes **its own** environment and passes a null block, because an explicit environment block through koffi fails with `ERROR_INVALID_PARAMETER`; the process token is opened through a real process handle because the pseudo-handle cannot be expressed; the failing call's error code is fetched by a second, separate foreign call | `packages/sandbox/sandbox-windows-acl/src/ffi.ts:150-158`; `packages/sandbox/sandbox-windows-acl/src/runner.ts:34-37`; `packages/subprocess/win32-process/README.md:75`; `packages/sandbox/sandbox-windows-acl/src/token.ts:16-19`; `packages/session/session-persistence-jsonl/src/win32.ts:64`, `:137` | **Resolved** by `DH-LIB-KOFFI-R1` and `-R6`. A Go wrapper takes typed arguments, accepts nil where Windows does, and returns the error of that same call. The standard library passes an explicit Unicode environment block to `CreateProcessAsUser` (`syscall/exec_windows.go:437-439`), so the host never edits its own environment. Inference: the koffi failure was a marshalling problem, not a Windows rule; `DH-T-KOFFI-4` proves it on Windows |
| `UP-DEBT-found.L6.2` | Structure layouts are written by hand as strings, their sizes asserted when the module loads, and checked against Windows headers by a separately compiled C++ probe. The evidence covers 64-bit Windows only; a new pointer width needs the probe redone before support is claimed | `packages/subprocess/win32-process/src/ffi.ts:146-162`; `packages/subprocess/subprocess-local/src/windows-inspector.ts:201-222`; `packages/subprocess/win32-process/README.md:44-50`, `:78` | **Resolved** by `DH-LIB-KOFFI`. x/sys types (`windows.StartupInfo`, `ProcessInformation`, `ProcessEntry32`, `Filetime`) are compiled Go structs maintained per architecture by the Go project. No layout strings, no load-time assertion, no probe. `DH-LIB-KOFFI-R7` limits own structure definitions to bound functions |
| (not a separate unit) | The Windows helper file exists twice, once per session backend, "so the two backends need no shared helper exports" | `packages/session/session-persistence-sqlite/src/win32.ts:13-14` | Removed by [DH-ADR-KOFFI-1](#dh-adr-koffi-1): one package owns each operating-system call |
| (not a separate unit) | koffi's type registry is global to the process, so a test runner that re-evaluates a module must not register a structure name twice | `packages/subprocess/subprocess-local/src/windows-inspector.ts:192-195` | Not applicable: Go types are resolved at compile time |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-KOFFI-R1` | Every operating-system call returns its own failure: the name of the call and the system's error code, from that invocation. No failure is read by a second call, and none is dropped. | `packages/subprocess/win32-process/README.md:27`; `UP-DEBT-found.L6.1` |
| `DH-LIB-KOFFI-R2` | On Windows a new file or directory can be published under its final name so that the name is durable when the call returns, the call fails if the name exists, and it never degrades to a cross-volume copy. | row 1 |
| `DH-LIB-KOFFI-R3` | A cross-process exclusive lock exists on every supported system. Contention is reported at once and never waits. The kernel releases the lock when the holder exits for any reason. Holding it does not stop other processes from reading the protected data. | row 2; `packages/session/session-persistence-jsonl/src/lease.ts:1-13` |
| `DH-LIB-KOFFI-R4` | Replacing an existing file on Windows keeps its access-control list, and at no moment is the new content readable by an account that could not read the old file. | row 3; `packages/fs/fs-local/src/fsio.ts:588-592` |
| `DH-LIB-KOFFI-R5` | On Windows a process can be started under a supplied access token inside a job object that ends the whole tree when its owner closes it, and the target cannot execute before it is in the job. A failure at any step releases every handle taken so far and leaves no suspended process behind. | row 4; `packages/subprocess/win32-process/README.md:30`, `:77` |
| `DH-LIB-KOFFI-R6` | The restricted token and the directory grants of the Windows sandbox are built from typed values (identifiers, entries, labels); no caller packs bytes or computes offsets. | row 5; `UP-DEBT-found.L6.2` |
| `DH-LIB-KOFFI-R7` | A process is identified by ID plus creation time wherever it is later signalled or waited for, so a reused ID is never acted on. | row 6 |
| `DH-LIB-KOFFI-R8` | Code for one operating system is neither compiled nor linked into the binary for another, and the logic above each call can be tested on any system by substituting the call. | lazy loading at `win32.ts:53`; `packages/subprocess/win32-process/README.md:73` |
| `DH-LIB-KOFFI-R9` | A function that `golang.org/x/sys` does not wrap is bound in exactly one place, with its argument types written once and a test that calls it on the real system. | rows 3, 5, 8 |

## 5. Go choice

**Decision.** `golang.org/x/sys` v0.50.0 for typed system calls, the standard library
(`os`, `os/exec`, `os/signal`, `syscall`) where it already covers the need, and a short
list of functions bound by hand. No FFI library and no cgo.

**Why.** Section 2 shows that all but a handful of the functions upstream reaches through
koffi exist as typed Go functions. The handful (`CreateRestrictedToken`, `ReplaceFileW`,
`CreateSemaphoreW` and `ReleaseSemaphore` if wanted, `CoCreateInstance` and four window
functions if the host shows its own dialog) are bound with the same mechanism x/sys uses
internally. This keeps the release plan's `CGO_ENABLED=0` rule
([DH-ADR-TOOLCHAIN-13](../upstream/toolchain.md#dh-adr-toolchain-13)).

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`golang.org/x/sys` + standard library (chosen)** | Typed; maintained by the Go project; pure Go on every target; removes every debt row in section 3 | Lacks a few functions (listed above), bound by hand under `DH-LIB-KOFFI-R9` | v0.50.0, released 2026-11-06, BSD-3-Clause (`LICENSE` read in the module cache); each symbol named in section 2 was found in that version's `windows/` or `unix/` source |
| Standard library `syscall` only | No dependency | Frozen: no job objects, no security-descriptor or token helpers, no `MoveFileEx` flags in the public package | `go doc syscall` on Go 1.27.4 |
| A general FFI module, `github.com/ebitengine/purego` | Closest equivalent to koffi: calls C functions without cgo | Solves a problem this design does not have. Windows functions need no FFI in Go, and Linux needs system calls, not the C library. It would bring back hand-declared signatures | v0.11.3, released 2026-11-01, Apache-2.0 (`LICENSE` read). Not evaluated further |
| `github.com/Microsoft/go-winio` for the access-control helpers | Higher-level helpers for some security calls | Does not cover restricted tokens or job objects, so x/sys is needed anyway | Existence and version only: v0.6.4, 2026-10-13. Licence not read |
| cgo against the Windows SDK headers | Every function available with real headers | Breaks `CGO_ENABLED=0`: one native build job per platform and a C toolchain | Not applicable |

**Tradeoff.** The bound functions carry hand-written argument lists, the same risk
upstream carries for all of them; the list is short, lives in one place and each entry
has a real-system test. x/sys is versioned `v0.x`: an upgrade is a reviewed `go.mod`
change.

<a id="dh-adr-koffi-1"></a>
### DH-ADR-KOFFI-1 One package per operating-system capability; no generic call-by-name layer

- **Decision.** Harness code never calls an operating-system function by name through a
  generic layer. Each capability (durable publish, process lock, permission-preserving
  replace, job-owned process start, token and grant building, process inspection) is a
  small Go function with typed arguments in the package of the area that owns it, in
  files selected by build constraint (`_windows.go`, `_linux.go`, `_darwin.go`). The
  functions x/sys lacks are bound in one internal package (proposed name
  `internal/util/winbind`) and nowhere else.
- **Why.** Upstream's binding tables are spread over seven packages, one of them copied
  whole, and each consumer re-declares layouts and re-implements error reading
  (`UP-DEBT-found.L6.1`, `UP-DEBT-found.L6.2`). A capability function gives one owner,
  one error contract and one place to test.
- **Tradeoff.** A new need means writing a function instead of adding a string to a
  table. That is the intent: each addition is reviewed as an operating-system contract.
- **Serves.** [DH-O-7](../design.md#dh-o-7) (the sandbox calls), [DH-O-5](../design.md#dh-o-5)
  (durable publish and the write lock), [DH-O-14](../design.md#dh-o-14) (one binary with
  no native payload).
- **Verified by.** `DH-T-KOFFI-1` (import check) and the per-capability tests in section 8.

## 6. Design notes

**Shape.** Each capability is an interface in its area's contract package with one
implementation per operating system, so the logic above it is tested with a fake on any
system (`DH-LIB-KOFFI-R8`). Illustrative signatures, not final names:

```go
// Durable, no-replace publication of a synced staging path.
func PublishNew(staging, final string) error
// Exclusive, non-waiting, kernel-released lock. Close releases it.
func TryLock(path string) (io.Closer, error) // ErrLocked on contention
// Start argv under token in a kill-on-close job; nothing runs outside the job.
func StartInJob(spec JobSpec) (*JobProcess, error)
```

**Ownership and cleanup.** A handle, token, job or lock is owned by the function that
created it until it is returned, and afterwards by the caller's cordis fiber, which
releases it on unload. Inside a function every acquisition is followed by a deferred
release that is cancelled only on success, so a failure half-way releases what was taken
(upstream states the same rule at `packages/subprocess/win32-process/README.md:29-30`).

**Failure half-way through a job-owned start.** Upstream admits one window: if the host
is killed between "process created suspended" and "assigned to the job", the suspended
target is left behind (`packages/subprocess/win32-process/README.md:77`). Inference:
creating the process with the job named in its start-up attribute list
(`windows.NewProcThreadAttributeList`, which x/sys provides; the job-list attribute
constant is not in x/sys and would be defined by the harness) would make creation and
membership one step. This is not verified and is left to the subprocess area as an open
question; until then the Go design has the same window and says so.

**Errors.** A wrapper returns an error that wraps `syscall.Errno` and names the call.
Callers compare with `errors.Is` (for example against `windows.ERROR_ALREADY_EXISTS`),
not by parsing text. Upstream's table that maps Windows codes to POSIX names
(`packages/session/session-persistence-jsonl/src/win32.ts:69-88`) is not ported; the
store defines its own error kinds.

**What is undone.** A failed publish leaves the staging path for the caller to remove; a
failed lock acquisition leaves nothing; a failed token or grant build releases its
handles. Changes to a user's directory access-control list are **not** undone by these
functions: their lifetime is decided by [DH-ADR-SECURITY-2](../upstream/security.md#dh-adr-security-2).

## 7. Risks and open questions

1. **Lock primitive on Windows (for the session-store area).** A file lock holds a file
   handle; upstream chose a semaphore precisely so that no handle blocks directory
   removal (`win32.ts:141-147`). With one database per harness home
   ([DH-ADR-PERSISTENCE-3](../upstream/session-and-persistence.md#dh-adr-persistence-3))
   there is no per-session directory to remove, so a lock file is acceptable.
   Recommendation: `LockFileEx` on a dedicated lock file; bind the semaphore functions
   only if a later need for a file-less lock appears.
2. **`os/exec` cannot resume a suspended child (for the subprocess and sandbox areas).**
   Section 2 row 4. The Windows flow in DH-ADR-SECURITY-3 needs a direct
   `windows.CreateProcessAsUser` call, which means the harness, not `os/exec`, builds the
   command line, the environment block and the inherited-handle list.
3. **The folder dialog.** Settled: there is no native dialog. With no desktop application
   ([DH-ADR-18](../design.md#dh-adr-18)) the one directory picker is a page the host
   renders (area C6, [../areas/ui-sidebar-workspace.md](../areas/ui-sidebar-workspace.md),
   component `ui.dirpick`), so row 8's bound functions are never written.
4. **32-bit and ARM Windows.** x/sys covers them; the bound functions must be tested on
   each architecture that ships.
5. **Durability cannot be proven by a test.** A test can show that the write-through
   flag is passed and that an existing name is refused; it cannot cut power. The
   guarantee is the one Windows documents for the flag.

## 8. Verification

Nothing here has been run. "Local" tests run under `go test` with no external service;
tests marked with an operating system call the real kernel on that system's CI runner and
are skipped elsewhere, and a skip on the system they exist for is a failure
([DH-ADR-TOOLCHAIN-6](../upstream/toolchain.md#dh-adr-toolchain-6)).

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-KOFFI-1` | local, any system | `DH-ADR-KOFFI-1`, `DH-LIB-KOFFI-R9` | A Go test parses the harness module and fails if `NewLazySystemDLL`, `NewProc` or `syscall.SyscallN` appears outside the one binding package, or if any package imports `C` |
| `DH-T-KOFFI-2` | local, Windows | `DH-LIB-KOFFI-R2`, `DH-LIB-KOFFI-R1` | Publish a staging file: the final name exists and the staging name does not. Publish onto an existing name: the error is `ERROR_ALREADY_EXISTS` or `ERROR_FILE_EXISTS` and the existing file is unchanged. Publish across volumes (where the runner has two): refused, nothing copied |
| `DH-T-KOFFI-3` | local, Windows, Linux, macOS | `DH-LIB-KOFFI-R3` | Two processes: the second `TryLock` returns `ErrLocked` without waiting; after the holder is killed the second acquires; a third process reads the protected file while the lock is held |
| `DH-T-KOFFI-4` | local, Windows | `DH-LIB-KOFFI-R5`, `DH-LIB-KOFFI-R1` (and the inference under `UP-DEBT-found.L6.1`) | Start a child under a duplicated token with an explicit environment: the child reports the given variables and the host's own environment is unchanged. Close the job: the child and a grandchild are gone. Inject a failure after creation: no process and no handle remains |
| `DH-T-KOFFI-5` | local, Windows | `DH-LIB-KOFFI-R4` | Replace a file whose list denies a second test account: after the swap the list is identical, and the staging file had the same list before any content was written |
| `DH-T-KOFFI-6` | local, Windows | `DH-LIB-KOFFI-R6`, `DH-LIB-KOFFI-R9` | Call each bound function for real: `CreateRestrictedToken` returns a token that `Token.IsRestricted` confirms; a Low label built from the descriptor string reads back as Low |
| `DH-T-KOFFI-7` | local, Windows | `DH-LIB-KOFFI-R7` | Record a child's identity, let it exit, then ask "alive?": the answer is no even if the ID is reused, because the creation time differs |
| `DH-T-KOFFI-8` | local, any system | `DH-LIB-KOFFI-R8` | `GOOS=linux`, `darwin` and `windows` builds with `CGO_ENABLED=0` succeed, and `go list -deps` for each shows no file of another system |

## 9. Units dispositioned by this spec

- `UP-DEP-koffi`: replaced by `DH-LIB-KOFFI`.
- `UP-DEBT-pin.allowBuilds.koffi`: resolved.
- `UP-DEBT-found.L6.1`, `UP-DEBT-found.L6.2`: defined here and resolved.

Go-side items declared: `DH-LIB-KOFFI`, `DH-ADR-KOFFI-1`, `DH-T-KOFFI-1` to `DH-T-KOFFI-8`.
