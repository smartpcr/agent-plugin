# Node built-ins for files, paths, the host and compression (`node:fs`, `node:path`, `node:os`, `node:zlib`): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Statements about Go were read from the Go 1.27.4 source tree or
> from module sources in the module cache on 2026-11-18, and say so.

| | |
|---|---|
| **Library ID** | `DH-LIB-NODEFS` (files, paths, host facts), `DH-LIB-NODEFSXSYS` (system calls the standard library lacks), `DH-LIB-NODEZLIB` (gzip, DEFLATE), `DH-LIB-NODEZLIBZSTD` (Zstandard) |
| **Upstream unit** | `UP-DEP-node:fs`, `UP-DEP-node:path`, `UP-DEP-node:os`, `UP-DEP-node:zlib` |
| **Upstream version** | the Node runtime upstream ships; not a versioned dependency |
| **Kind** | Node built-in |
| **Used by** | `node:fs` 472 files in 40 groups, `node:path` 540 files in 43 groups, `node:os` 91 files in 22 groups, `node:zlib` 9 files in 3 groups (inventory counts) |
| **Go choice** | standard library (`os`, `io/fs`, `path/filepath`, `path`, `runtime`, `compress/gzip`), plus `golang.org/x/sys` for a few system calls and `github.com/klauspost/compress` for Zstandard where it is used at all |
| **Related areas** | every area; most directly filesystem tools, sandbox, persistence, credentials, settings, telemetry |

## 1. What they are and why upstream needs them

These four modules are how a Node program touches the machine it runs on. `node:fs`
reads, writes, renames and inspects files. `node:path` builds and takes apart path
strings. `node:os` answers questions about the host: where the home and temporary
directories are, which operating system this is, how many processors it has. `node:zlib`
compresses and decompresses data (gzip, DEFLATE, Brotli and, in recent Node, Zstandard).

They are imported by hundreds of files, so this specification does not list files. It
sorts the uses into classes, names the Go equivalent of each class, and records the
places where Go behaves differently from Node in a way that would cause a defect. Most
classes map straight onto the Go standard library and need no design. Three do not:
replacing a file atomically, deciding whether two paths are the same file or whether one
lies inside a directory, and Zstandard.

## 2. How upstream uses them

### 2.1 Files (`node:fs`, `node:fs/promises`)

Counts are named imports in non-test source under `packages`, `apps`, `vendor` and
`native`, tallied with a text search on 2026-11-18; they show proportion, not an exact
census.

| Class of use | Typical calls (count) | Representative site (`path:line`) | Go equivalent |
|---|---|---|---|
| Whole-file read and write | `readFile` 98, `readFileSync` 82, `writeFile` 48, `writeFileSync` 39, `appendFile` 9 | everywhere | `os.ReadFile`, `os.WriteFile`, `os.OpenFile` with `O_APPEND` |
| Directories | `mkdir` 68, `readdir` 34, `rm` 48, `mkdirSync` 35, `rmSync` 28 | everywhere | `os.MkdirAll`, `os.ReadDir`, `os.RemoveAll` |
| Existence and metadata | `existsSync` 59, `stat` 35, `lstat` 24, `access` 10 | everywhere | `os.Stat`, `os.Lstat`, `errors.Is(err, fs.ErrNotExist)` |
| Streaming | `createReadStream` 18, `open` 21 | `packages/fs/fs-local/src/fsio.ts:9` | `os.Open` and `io.Reader` |
| **Atomic replacement** | `rename` 20, `renameSync` 8, exclusive create (`wx`), `fsync` | `packages/util/atomic-write/src/index.ts:81-98`; `packages/fs/fs-local/src/fsio.ts:589-687`; `packages/session/session-persistence-jsonl/src/win32.ts:56-60` | own helper, section 6.1 |
| Create only if absent | `link` 6 (hard link as a no-replace publish) | `packages/fs/fs-local/src/fsio.ts:600`, `:652` | own helper, section 6.1 |
| Lock files | exclusive create of `<file>.lock` holding a process ID | `packages/util/atomic-write/src/index.ts:156` | a real file lock, section 6.1 |
| **File identity** | `realpath` 27, `realpathSync` 22 | `packages/fs/fs-local/src/fsio.ts:172-184`; `packages/util/home-paths/src/index.ts:42` | own helper, section 6.2 |
| Temporary files | `mkdtemp` 41, `mkdtempSync` 23 | `packages/fs/fs-local/src/fsio.ts:620-639` | `os.MkdirTemp`, `os.CreateTemp` |
| Permissions | `chmod` 3, `chmodSync` 5, mode on create | `packages/credentials/credentials-local/src/index.ts:97`, `:142-143`; `packages/fs/fs-local/src/win32.ts:103-112` | `os.Chmod` and create modes on POSIX; access-control lists through `golang.org/x/sys/windows` on Windows |
| Links | `symlink` 4, `readlink` 4 | `packages/fs/fs-local/README.md:75` | `os.Symlink`, `os.Readlink` |
| Watching | `watchFile` 1 | `packages/skill/skill-filesystem/src/index.ts:467` | [chokidar.md](chokidar.md) |

### 2.2 Paths (`node:path`)

| Class of use | Typical calls (count) | Representative site | Go equivalent |
|---|---|---|---|
| Build and split | `join` 251, `dirname` 117, `basename` 39, `extname` 19 | everywhere | `filepath.Join`, `Dir`, `Base`, `Ext` |
| Make absolute | `resolve` 122, `isAbsolute` 75 | everywhere | `filepath.Abs`, `filepath.IsAbs` |
| **Containment tests** | `relative` 36 with a check for a leading `..`; `sep` 35 | `packages/fs/tool-fs-search/src/search-core.ts:273-279`; `packages/boot/hmr/src/watch-config.ts:51-52` | section 6.2 |
| **A fixed dialect regardless of host** | `posix` 16, `win32` 10 | `packages/ssh/fs-ssh/src/index.ts` (remote paths), `packages/api/workspace-files/src/index.ts`, `packages/util/workspace-path/src/index.ts:8-18` (accepts both spellings) | `path` for forward-slash paths; own code for Windows-style strings on a non-Windows host |
| Windows long paths | `toNamespacedPath` 7 | `packages/fs/fs-local/src/win32.ts:90` | done by the `os` package (see traps) |
| Paths and `file:` URLs | `pathToFileURL`, `fileURLToPath` (213 occurrences) | module loading and hot reload | mostly not applicable: they serve JavaScript module loading |

### 2.3 Host facts (`node:os`)

| Class of use | Calls (non-test count) | Representative site | Go equivalent |
|---|---|---|---|
| Temporary directory | `tmpdir` 38 | everywhere | `os.TempDir`; in tests `t.TempDir` |
| Home directory | `homedir` 20 | `packages/util/home-paths/src/index.ts:70-89`, `:135` | `os.UserHomeDir` |
| Platform and processor | `platform` 3, `arch` 1, and `process.platform` in about 100 files | `packages/util/atomic-write/src/index.ts:27` | `runtime.GOOS`, `runtime.GOARCH`, and build-tagged files |
| Processor count | `availableParallelism` 1, `cpus` 2 | worker sizing | `runtime.GOMAXPROCS(0)` |
| Account | `userInfo` 4 | `apps/desktop/src/login-shell-environment.ts:67` (login shell) | `os/user` (see traps) |
| Diagnostics | `release` 4, `totalmem` 2, `hostname` 1 | `packages/account/deepseek-account/src/index.ts:875`; `apps/desktop/src/device-info.ts:15` | `os.Hostname`; version and memory through `golang.org/x/sys` |
| Null device | `devNull` 1 | subprocess wiring | `os.DevNull` |

### 2.4 Compression (`node:zlib`)

| Use site (`path:line`) | What it is used for | In the Go design |
|---|---|---|
| `packages/telemetry/otel/src/event-transport.ts:28` | gzip the body of a telemetry export request | `compress/gzip` |
| `packages/experimental/webworker-packer/src/pack.ts:656` | gzip the bundle of the experimental in-browser host | goes with that package; no in-browser host under [DH-ADR-3](../design.md#dh-adr-3) |
| `packages/session/session-persistence-jsonl/src/zstd.ts:9-22`, `src/generation.ts:54`, `:748`, and two decoders, one of which reaches into Node's private Zstandard handle | one Zstandard frame per batch of session events, with checksums | dropped for live storage ([DH-ADR-PERSISTENCE-3](../upstream/session-and-persistence.md#dh-adr-persistence-3)) |
| `packages/session/session-persistence-sqlite/src/frame.ts:9`, `:46` | Zstandard for a stored row when that makes it smaller | the same idea survives as an optional payload codec in the Go store |
| about 25 test files (`gunzipSync`, `zstdDecompressSync`, `crc32`, `deflateSync`) | inspect what the product wrote; build image fixtures | `compress/gzip`, `hash/crc32`, `compress/flate`; Zstandard only where the product writes it |
| `packages/llm/llm-pi-ai/tests/mock-server.ts:89-90` | a test server accepts Zstandard request bodies because one model provider's client sends them | for the model-provider area to decide |

## 3. Tech debt upstream carries

Search performed: a text search for every file-replacement, locking and
path-comparison idiom listed above; `pnpm-workspace.yaml:42-46` (the `koffi` build
exemption, a ledger unit owned by another agent); the "Known Limitations" sections of
`fs-local`, `atomic-write` and `home-paths`. Node built-ins have no patches or pins.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L9.5` | Replacing a file atomically is implemented three times with three different guarantees. The shared helper does not sync, by its own statement. The filesystem backend stages in a private directory, syncs, and on Windows calls `ReplaceFileW` and copies the access-control list through a foreign-function library. The session store calls `MoveFileExW` through the same library, in a second copy. A settings file and a file the model edits therefore survive a power loss differently, and Windows needs a native bridge that must be compiled or downloaded. | `packages/util/atomic-write/src/index.ts:76`, `:86-87`; `packages/fs/fs-local/src/fsio.ts:589-687`; `packages/fs/fs-local/src/win32.ts:48-54`; `packages/session/session-persistence-jsonl/src/win32.ts:56-60`; `pnpm-workspace.yaml:42-46` | Resolved by [DH-ADR-NODEFS-1](#dh-adr-nodefs-1): one helper with an explicit durability argument; Windows calls go through `golang.org/x/sys/windows` with no bridge. |
| (owned by the util area) `UP-DEBT-marker.packages/util/atomic-write/src/index.ts:86` | `TODO(settings-atomic-durability)`: sync the file and its directory and keep owner-only permissions on Windows | `packages/util/atomic-write/src/index.ts:86` | `DH-LIB-NODEFS-R1` and `R8`. |
| (owned by the filesystem area) | Creating a file only if absent needs hard links, which some filesystems refuse | `packages/fs/fs-local/README.md:140` | `DH-LIB-NODEFS-R2`: the rename-without-replace system call where the platform has one, hard link as the fallback. |
| (owned by the persistence agent) | A Zstandard decoder that uses Node's private handle | `packages/session/session-persistence-jsonl/src/zstd-private-decoder.ts:2` | Not applicable: a synchronous streaming decoder is the ordinary API in Go ([session-and-persistence.md](../upstream/session-and-persistence.md) section 10). |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NODEFS-R1` | Replace a file so that any reader sees the complete old or the complete new content. The caller states whether the replacement must survive power loss; if so the file and its directory are synced before success is reported. Permissions of the result are stated or preserved, never left to chance. A failure leaves the old file and no temporary file. | `atomic-write/src/index.ts:61-98`; `fs-local/src/fsio.ts:589-687` |
| `DH-LIB-NODEFS-R2` | Create a file only if no file has that name, safely against another process doing the same. | `fs-local/src/fsio.ts:600`, `:652` |
| `DH-LIB-NODEFS-R3` | Two paths that name the same file are recognised as the same, through symbolic links, letter case and Windows short names. A version token for a file changes whenever the file is rewritten. | `fs-local/src/fsio.ts:172-184`; `fs-local/README.md:75`, `:135` |
| `DH-LIB-NODEFS-R4` | "Is this path inside that directory" is answered by one function that is correct for `..`, for different volumes, and for symbolic links that point outside. | `search-core.ts:273-279`; `watch-config.ts:51-52` |
| `DH-LIB-NODEFS-R5` | Paths on disk use the host's form. Paths in protocols, archives, glob patterns, URLs and stored records use forward slashes. Conversion happens once, at the boundary. | `workspace-files/src/search.ts:79` (`--path-separator=/`); `workspace-path/src/index.ts:8-18` |
| `DH-LIB-NODEFS-R6` | The harness home is resolved in a fixed order: explicit configuration, then an environment variable, then a directory under the user's home; a blank override counts as unset; a leading `~` is expanded. | `home-paths/src/index.ts:87-89`, `:127-136` |
| `DH-LIB-NODEFS-R7` | Temporary files and directories are created exclusively, owner-only, on the same volume as their final destination when they will be renamed into place, and are removed by whoever created them on every path. | `fs-local/src/fsio.ts:620-640`; `atomic-write/src/index.ts:88-95` |
| `DH-LIB-NODEFS-R8` | A file that holds secrets is owner-only from the moment it exists. On POSIX a secrets file readable by others is refused. On Windows the document states exactly what is checked. | `credentials-local/src/index.ts:97`, `:142-143`; `credentials-local/README.md:115` |
| `DH-LIB-NODEFS-R9` | Code branches on the kind of a file error (not found, already exists, permission, different volume, in use), never on message text, and the kinds mean the same on every platform. | 243 occurrences of `ENOENT`, 56 of `EEXIST`, 73 of `EPERM`, `EBUSY` or `EACCES` in non-test source |
| `DH-LIB-NODEZLIB-R1` | Compress and decompress gzip as streams. | `telemetry/otel/src/event-transport.ts:28` |
| `DH-LIB-NODEZLIB-R2` | A session export can be compressed with a format that ordinary tools on every platform can read. | [DH-ADR-PERSISTENCE-12](../upstream/session-and-persistence.md#dh-adr-persistence-12) |
| `DH-LIB-NODEZLIB-R3` | Decompressing data from outside the process is bounded in output size and memory. | Inference: imported exports and uploads are untrusted |

## 5. Go choice

**Decision.** The standard library for everything in section 2 that has a direct
equivalent. Two modules fill the gaps: `golang.org/x/sys` for system calls the standard
library does not expose, and `github.com/klauspost/compress` for Zstandard, only where
Zstandard is used.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** standard library `os`, `io/fs`, `path/filepath`, `path`, `runtime`, `compress/gzip`, `compress/flate`, `hash/crc32` | No dependency; one behaviour to learn | Gaps listed in the next rows | Go 1.27.4 source |
| **Chosen:** `golang.org/x/sys` (`windows`, `unix`) | Maintained by the Go team; gives `MoveFileEx` with the write-through flag, `LockFileEx`, `GetDriveType`, `GetFileInformationByHandle`, `GetNamedSecurityInfo` and `SetNamedSecurityInfo`, `RtlGetVersion` on Windows; `Renameat2` with `RENAME_NOREPLACE`, `Flock`, `Statfs` on Linux | `ReplaceFileW` and a total-memory call are not wrapped; they would be loaded by name from `kernel32.dll`, still without cgo | v0.50.0, 2026-11-06, BSD 3-Clause, no requirements. Fourteen files import `C`; all are type generators or files for AIX, Solaris, Hurd and gccgo, none compiled for Linux, macOS or Windows. Checked with `go list -m -json`, `go mod download -json`, `LICENSE`, and by searching the `windows` and `unix` sources for each function named here, 2026-11-18. |
| **Chosen for Zstandard:** `github.com/klauspost/compress` (`zstd`) | Pure Go; streaming encoder and decoder; a decoder memory limit (`WithDecoderMaxMemory`) | A large module of which one package is used | v1.20.4, 2026-11-09, no requirements, no cgo. Its `LICENSE` opens with a BSD 3-Clause text (Go Authors, Klaus Post) and then lists other licences for named directories only (`gzhttp`, `s2/cmd`, `snappy`, `internal/snapref`); `zstd` is not among them, so the BSD 3-Clause text governs it. `zstd/internal/xxhash` carries its own MIT licence. Same method, 2026-11-18. |
| Zstandard from the standard library | No dependency | Does not exist as a public package: `go list std` on Go 1.27.4 shows `internal/zstd` only, and `go doc compress/zstd` reports that the package is not in the standard library | `go list std`, `go doc`, 2026-11-18 |
| `github.com/google/renameio/v2` for atomic replacement | A known helper | Inference from its documentation, not re-read here: it does not offer its atomic functions on Windows, which is where upstream's difficulty lies | v2.0.2, 2026-01-10, Apache 2.0; existence, date and licence file only |

This answers the question left open in
[session-and-persistence.md](../upstream/session-and-persistence.md) section 12: the Go
standard library does not cover Zstandard, and the `zstd` package of the named module is
under the BSD 3-Clause licence.

<a id="dh-adr-nodefs-1"></a>
### DH-ADR-NODEFS-1 One package owns replacement, identity and containment; the rest is the standard library

- **Decision.** A harness package, `fsx`, provides exactly the operations where Go and
  Node differ or where upstream has more than one implementation: atomic replacement,
  create-if-absent, file locks, file identity and version tokens, containment, and
  owner-only creation. All other file work calls the standard library directly. Calling
  `os.Rename` to publish durable state outside `fsx` is a lint finding.
- **Why.** Upstream's three replacement implementations (`UP-DEBT-found.L9.5`) drifted
  because each caller solved the problem locally. The operations are few, subtle and
  platform-specific; everything else is not worth wrapping.
- **Tradeoff.** A rule that a linter has to enforce. A wrapper also hides which system
  call runs, so `fsx` documents each platform's sequence.
- **Serves.** `DH-LIB-NODEFS-R1` to `R4`, `R7`, `R8`.
- **Verified by.** `DH-T-NODEFS-1` to `DH-T-NODEFS-5`, `DH-E-NODEFS-1`.

<a id="dh-adr-nodezlib-1"></a>
### DH-ADR-NODEZLIB-1 gzip by default; Zstandard only inside the store

- **Decision.** Anything a person or another tool may open (a session export, an HTTP
  body) is compressed with gzip from the standard library. Zstandard is used only for
  the optional payload codec inside the session database, if and when that codec is
  switched on, and for reading a `.zst` file someone supplies.
- **Why.** The export exists for portability and inspection
  ([DH-ADR-PERSISTENCE-12](../upstream/session-and-persistence.md#dh-adr-persistence-12));
  gzip can be read by the tools already on every operating system and by every language.
  Zstandard's advantages, speed and ratio on many small frames, matter on the append
  path of the store, not for an occasional export.
- **Tradeoff.** Exports are somewhat larger and slower to write than with Zstandard. Not
  measured.
- **Serves.** `DH-LIB-NODEZLIB-R1` to `R3`.
- **Verified by.** `DH-T-NODEZLIB-1`, `DH-T-NODEZLIB-2`.

## 6. Design notes

### 6.1 Replacing a file (`fsx.Replace`)

```go
type ReplaceOptions struct {
    Perm      fs.FileMode // required when the target does not exist
    KeepPerm  bool        // copy mode (POSIX) or access-control list (Windows) from the existing target
    Durable   bool        // sync the file and its directory before returning
}
func Replace(path string, write func(io.Writer) error, opt ReplaceOptions) error
func CreateIfAbsent(path string, write func(io.Writer) error, opt ReplaceOptions) error
```

Sequence: create a temporary file in the *same directory* with an exclusive create and
owner-only mode; run `write`; set the final mode; sync the file if `Durable`; close it;
rename it over the target; if `Durable`, sync the directory (POSIX) or use
`MoveFileEx` with the write-through flag (Windows). On any failure before the rename the
temporary file is removed and the target is untouched. After the rename the operation
has succeeded; a failed directory sync is returned as an error that says the content is
in place but its durability is unknown.

**Retry.** On Windows a rename fails while another process (an indexer, a virus
scanner, an editor) holds the target open. `Replace` retries on a sharing violation or
access denied for a bounded time, as upstream does (eight tries from 20 ms to 200 ms,
`atomic-write/src/index.ts:20-23`), then fails.

**Create-if-absent.** Linux: `renameat2` with `RENAME_NOREPLACE`. Windows: `MoveFileEx`
without the replace flag. Elsewhere, and where the first is unsupported: hard link then
remove the temporary name, which is upstream's method. These are for paths the host
reaches by name. Inside a write root the file tools work through directory-rooted
handles (`os.Root`), which offer no rename that refuses to replace; there area A08
uses a hard link, or, where linking is refused, reserves the name with an exclusive
create and then replaces the reservation ([../areas/filesystem.md](../areas/filesystem.md),
`DH-D-FS-1`).

**Locks.** Upstream's lock file holding a process ID becomes an operating-system lock on
a lock file (`flock`, `LockFileEx`). The kernel releases it when the holder dies, so
there is no stale-lock takeover logic.

**Ownership.** The call owns the temporary file until the rename. Nothing outlives the
call, so no fiber owns anything; a lock handle returned to a caller is registered as an
effect on the caller's fiber.

### 6.2 Identity and containment

- **Same file.** `os.SameFile` on two `Stat` results, not string comparison. Where a
  map key is needed, `fsx.ID(path)` returns the device and file numbers (POSIX) or the
  volume serial and file index from `GetFileInformationByHandle` (Windows).
- **Version token.** Identity, size, modification time in nanoseconds and, on POSIX,
  change time, as upstream uses (`fs-local/README.md:135`). These live in the
  platform-specific `Sys()` value, so `fsx` has one small file per platform.
- **Canonical spelling** for display and for watch registration:
  `filepath.EvalSymlinks` on the nearest existing ancestor, with the missing remainder
  appended, which is upstream's rule (`fsio.ts:193-200`).
- **Containment.** `fsx.Inside(root, path)` compares canonical forms component by
  component. Access that must not escape a directory does not rely on a path test at
  all: it goes through an `os.Root`, as the security foundation already decides
  ([DH-ADR-SECURITY-9](../upstream/security.md#dh-adr-security-9)).

### 6.3 Home, temporary files and host facts

- **Home.** The default home location is the `homedir` package of area A23
  ([../areas/util-test-support.md](../areas/util-test-support.md)), not a function of
  `fsx`; the layout under it is area A07's. It implements `DH-LIB-NODEFS-R6` over
  `os.UserHomeDir`. Whether the Go harness keeps one directory under the user's home, as
  upstream does (`~/.dsh` with categories named after the XDG kinds,
  `home-paths/src/index.ts:177`), or uses `os.UserConfigDir` and `os.UserCacheDir`, is a
  product decision for the boot area.
- **Temporary files.** Scratch work uses `os.MkdirTemp("", …)`. Anything that will be
  renamed into place is created beside its destination, never in the system temporary
  directory, because a rename across volumes fails.
- **Host facts.** `runtime.GOOS` and `GOARCH`; `runtime.GOMAXPROCS(0)` for sizing worker
  pools; `os.Hostname`; operating-system version and memory through `golang.org/x/sys`
  in build-tagged files. Platform-specific code lives in files selected by build tags
  (`_windows.go`, `_unix.go`), not in `if runtime.GOOS` branches, so that each platform
  compiles only its own system calls.

### 6.4 Traps: where Go differs from Node

"Verified" means read in the Go 1.27.4 source on 2026-11-18. The rest is marked.

| Trap | What goes wrong | What to do |
|---|---|---|
| Files opened by Go on Windows cannot be renamed or deleted by anyone while open | Verified: `os.OpenFile` calls `syscall.Open` (`os/file_windows.go:162`), which passes `FILE_SHARE_READ \| FILE_SHARE_WRITE` and not the delete flag (`syscall/syscall_windows.go:395`). The harness's own reader can make its own `Replace` fail. | Keep handles short-lived; the retry in 6.1; tests that replace a file while it is being read. |
| `os.Rename` is atomic but not durable | Verified for Windows: it is `MoveFileEx` with only the replace flag (`os/file_windows.go:263-264`, `internal/syscall/windows/syscall_windows.go:357-366`). Inference for POSIX: `rename(2)` does not sync the directory. | `fsx.Replace` with `Durable`. |
| `os.Rename` and `os.Root.Rename` are different system calls on Windows | Verified: the rooted form renames by handle with POSIX semantics (`internal/syscall/windows/at_windows.go:439-461`); the plain form does not. Inference: they fail in different situations when the target is open. | `fsx.Replace` is tested through both paths (`DH-T-NODEFS-1`). |
| `os.Chmod` and create modes mean almost nothing on Windows | Inference from the `os` documentation: only the read-only attribute is affected. A file created with mode `0600` is as readable as its directory allows. | `DH-LIB-NODEFS-R8`: set the access-control list through `golang.org/x/sys/windows`, or rely on the profile directory and say so. Upstream skips the check on Windows (`credentials-local/README.md:115`). |
| `io/fs.FS` cannot name every file | Verified: `fs.ValidPath` rejects names that are not valid UTF-8 (`io/fs/fs.go:64-67`). | Internal interfaces that must reach every file take OS paths, not `fs.FS` ([vscode-ripgrep.md](vscode-ripgrep.md)). |
| `path` and `path/filepath` are different packages | `path` always uses `/`; `filepath` uses the host separator. Using `filepath` on a URL or an archive name breaks on Windows; using `path` on a disk path breaks drive letters. | `DH-LIB-NODEFS-R5`; convert with `filepath.ToSlash` and `FromSlash` at the boundary only. |
| A containment test by string prefix is wrong | `filepath.Rel` returns an error for paths on different Windows volumes, and a prefix test accepts `/work-evil` as inside `/work`. | `fsx.Inside`, or `os.Root`. |
| The temporary directory may be spelled with a Windows short name | Observed in this session: the temporary path contains `XIAODO~1.RED`. Comparing it as text with a long path fails; upstream canonicalises for the same reason (`home-paths/src/index.ts:29-42`). | Verified: on Windows `filepath.EvalSymlinks` rewrites each component to the spelling a directory lookup returns, described in the source as the "correct case" (`path/filepath/symlink_windows.go:25-61`). Inference: the same lookup turns a short name into the long one; `DH-T-NODEFS-3` confirms it. Compare identities, not strings. |
| Letter case | Windows and default macOS volumes ignore case; Linux does not. A map keyed by path text has duplicate or missing entries. | Key by `fsx.ID`. |
| `os/user` without cgo | Inference: with cgo disabled, account lookup reads `/etc/passwd` and environment variables and does not see accounts from a directory service. | Use `os.UserHomeDir` for the home; treat the login shell as optional, as upstream already does (`login-shell-environment.ts:67`). |
| File errors | Node code tests `error.code`. | `errors.Is` with `fs.ErrNotExist`, `fs.ErrExist`, `fs.ErrPermission`; `syscall.EXDEV` and Windows sharing violations are wrapped by `fsx` into two named errors so callers do not import `syscall`. |
| Symbolic links on Windows | Creating one needs a privilege or Developer Mode. | Tests that need links skip with a stated reason when creation fails. |
| File operations ignore `context.Context` | A blocked read on a network drive cannot be cancelled. | Check the context between chunks; put a deadline on the surrounding operation. |
| Long Windows paths | Inference from the `os` source naming (`fixLongPath`): absolute paths are extended automatically; upstream's explicit conversion (`fs-local/src/win32.ts:90`) is needed only for direct system calls. | `fsx` converts before calling `golang.org/x/sys/windows` functions. |

### 6.5 Compression

gzip and DEFLATE are used through `io.Reader` and `io.Writer` wrappers. Every reader of
outside data is wrapped in `io.LimitReader` (`DH-LIB-NODEZLIB-R3`). If the payload codec
is enabled, one Zstandard encoder and one decoder are created per store, reused across
calls, and owned by the store's fiber; the decoder is created with a memory limit.

## 7. Risks and open questions

- **Durability on Windows** rests on `MoveFileEx` with the write-through flag. That it
  gives the guarantee the store needs is the documented behaviour of the call, not
  something a local test can prove.
- **Access-control lists.** Whether owner-only on Windows is enforced or documented as
  "inherits from the profile directory" is for the credentials area.
- **A second declaration of `golang.org/x/sys` or `github.com/klauspost/compress`** may
  be written by the security or persistence library agents. One should remain.
- **Home layout** (one directory or the operating system's configuration and cache
  directories) is for the boot area.
- **Owning area.** `fsx` needs a deliverable; the filesystem area is the natural owner,
  with persistence, credentials and settings as consumers.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-NODEFS-1` | local | `DH-LIB-NODEFS-R1`, `R7` | A reader in a loop sees only complete old or new content while a writer replaces the file 1,000 times. A failure injected at each step leaves the old content and no temporary file. On Windows, a replacement made while another handle holds the target open succeeds after the handle closes within the retry window and fails cleanly otherwise. |
| `DH-T-NODEFS-2` | local | `DH-LIB-NODEFS-R2` | Fifty goroutines and two processes race `CreateIfAbsent` on one name: exactly one succeeds and the file holds that writer's content. |
| `DH-T-NODEFS-3` | local | `DH-LIB-NODEFS-R3` | A file reached directly, through a symbolic link, with different letter case (Windows, macOS) and through a short name (Windows) has one identity. The version token changes after an in-place write and after a replacement, and not after a read. |
| `DH-T-NODEFS-4` | local | `DH-LIB-NODEFS-R4`, `R5` | Containment table: `..`, sibling with a common prefix, another volume, a link that points outside. Round-trip of host paths to forward-slash form and back, including drive and network-share paths. |
| `DH-T-NODEFS-5` | local | `DH-LIB-NODEFS-R6`, `R8`, `R9` | Home resolution order with blank and `~` values. On POSIX a secrets file with group or other bits is refused and a new one is created `0600` regardless of the process mask. Each error kind is produced on each platform and matched with `errors.Is`. |
| `DH-E-NODEFS-1` | real environment | `DH-LIB-NODEFS-R1` | **Given** a virtual machine for each of Linux, macOS and Windows with a disk that can be cut off (power-off of the virtual machine, not a clean shutdown), running a loop that replaces a file durably and records each acknowledged version on a second machine. **When** the machine is powered off at a random moment and restarted, 100 times. **Then** the file is always complete and is never older than the last acknowledged version. **Cleanup:** delete the virtual disks; keep the version log of any failing run. |
| `DH-T-NODEZLIB-1` | local | `DH-LIB-NODEZLIB-R1`, `R2` | gzip round trip of a stream larger than memory with flat heap; a compressed export is listed by the system `gzip -l` in a recorded golden output. |
| `DH-T-NODEZLIB-2` | local | `DH-LIB-NODEZLIB-R3` | A small input that expands to gigabytes is stopped at the configured limit for gzip and for Zstandard, with bounded heap. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-node:fs`, `UP-DEP-node:path`, `UP-DEP-node:os`, `UP-DEP-node:zlib`,
`UP-DEBT-found.L9.5`.
