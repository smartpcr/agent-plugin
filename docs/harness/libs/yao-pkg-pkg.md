# @yao-pkg/pkg and node:sea (single-file executable): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was built, run or measured.

| | |
|---|---|
| **Library IDs** | `DH-LIB-YAOPKG` (the packer), `DH-LIB-NODESEA` (the Node built-in) |
| **Upstream units** | `UP-DEP-@yao-pkg/pkg`, `UP-DEP-node:sea` |
| **Upstream version** | `@yao-pkg/pkg` 6.21.0, pinned exactly and patched (`package.json:270`, `pnpm-workspace.yaml:118`); `node:sea` as shipped with Node 24 |
| **Kind** | `@yao-pkg/pkg`: build-only. `node:sea`: Node built-in, imported by four runtime files |
| **Used by** | The Python SDK runtime (`python/sdk-runtime`), the SSH helper (`packages/ssh`), the shell environment (`packages/shell/shell-env`) |
| **Go choice** | `DH-LIB-YAOPKG`: **toolchain** (`go build`, cross-compilation, the `embed` package). `DH-LIB-NODESEA`: **standard library** (`os.Executable`) |
| **Related areas** | [../upstream/toolchain.md](../upstream/toolchain.md#dh-adr-toolchain-13) (release), [vscode-ripgrep.md](vscode-ripgrep.md) (a sidecar executable). Owning areas: command line (A24), SSH (A10), the Python SDK |

## 1. What it is and why upstream needs it

A Node program is a directory of JavaScript files plus the `node` runtime that reads
them. Two upstream products must run where no Node is installed: the runtime that the
Python SDK starts as a child process, and the helper that is copied to a remote machine
for SSH execution. For them upstream builds a **single-file executable**: one file that
contains the Node runtime and the program.

Node's own mechanism for this is SEA (single executable application): a data blob is
injected into a copy of the `node` binary. SEA alone carries one script and no file
tree, so a program that loads its plugins by package name at run time cannot use it
(`.agents/notes/implemented/architecture/2026-07-10-single-file-executable-sdk-runtime-distribution.md:81`).
`@yao-pkg/pkg` in its `--sea` mode adds a **virtual filesystem** (VFS): the package tree
is stored in the blob and file-system calls are intercepted so that Node's module loader
finds it (`:19`). `node:sea` is the built-in module a program uses to ask "am I running
from such an executable?".

A Go program is compiled to one executable by the standard build. The need for a packer
disappears; a few of its lessons do not.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `scripts/build-exe-for-python-sdk.ts:3`, `:260` | Builds `deepseek-harness-sdk-runtime-<platform>-<arch>`, one invocation per target | `pkg --sea --compress Brotli` |
| `.github/workflows/build-exe-for-python-sdk.yml`, `.github/workflows/build-exe-for-ssh-helper.yml:17`, `.github/workflows/publish-ssh-helper.yml:8` | Five native build legs (Linux x64 and arm64, macOS arm64 and x64, Windows x64); the SSH helper archives are published as release assets | The same command |
| `python/sdk-runtime/runtime-bootstrap.mjs:12` | Finds the resource directory: beside the executable when packaged, beside the script otherwise | `isSea()` |
| `packages/ssh/ssh-helper-runtime/src/index.ts:15` | Refuses to run outside the packaged executable | `isSea()` |
| `packages/ssh/ssh/src/helper-entry.ts:14` | Reports its own entry as "executable" or "node script" so the caller can verify the right file's digest | `isSea()` |
| `packages/shell/shell-env/src/index.ts:171`, `:172` | Tells shell tools where `node` is, except when the process is a packaged executable (then its own path is not a `node`) | `process.getBuiltinModule('node:sea')`, `isSea()` |

What the packaged form costs upstream, from its own decision note
(`.agents/notes/implemented/architecture/2026-07-10-single-file-executable-sdk-runtime-distribution.md`):

| Cost | Evidence |
|---|---|
| A per-target build on a native runner; Linux rebuilds a native addon inside a `manylinux` container and inspects the C library version the result needs | `:51`, `:53` |
| Files that cannot live in the VFS travel beside the executable as "sidecars": the `ripgrep` binary, a terminal spawn helper on macOS, the office engine | `:47`, `:51`, `:95` |
| Native addons inside the VFS are copied to a per-user cache on first load, because the operating system cannot load a library from a virtual file | `:26` |
| The VFS layer is community-maintained and pinned exactly; an upgrade means re-recording the patch | `:95` |

## 3. Tech debt upstream carries

Search: `patches/`, `pnpm-workspace.yaml`, the decision note above, and the four
`node:sea` call sites.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-patch.@yao-pkg__pkg@6.21.0` | A 402 KB patch to two files of the packer. It is that large because the packer ships its bootstrap as one bundled file that also contains a copy of itself as a string, so two changed lines are each thousands of characters long. It makes five changes, listed below | `patches/@yao-pkg__pkg@6.21.0.patch:1`, `:20` | **Resolved**: each cause is absent in a compiled Go binary; one lesson is kept as `DH-LIB-YAOPKG-R3` |

| Change in the patch | Why upstream needed it | In Go |
|---|---|---|
| Removes the bootstrap's child-process hook (patch `:158`) | The packer rewrote any spawned command named `node`, including inside `bash -c`, to the executable itself and added `PKG_EXECPATH` to every child environment, so a model's `node --version` started the harness (note `:24`) | Absent: a Go binary does not pose as another program. Kept as a rule (`R2`): helper-mode selection never reaches a grandchild |
| Writes extracted native addons to a temporary file and renames it (patch `:50`, `:59`) | Concurrent first starts shared one cache directory; a half-written library crashed other processes (39 of 80 in upstream's measurement, note `:26`) | No addon and no extraction when cgo is off. Kept as a rule (`R3`) for anything the binary ever writes to a shared cache |
| Adds typed directory listing to the VFS (patch `:72`, `:124`) | `readdir` with file types was not implemented for virtual directories | Absent: `embed.FS` implements `fs.ReadDirFS` |
| Rejects an archive entry whose decompressed size differs from its recorded size, and accepts empty files (patch `:112`) | A corrupt archive was read without complaint | Absent: embedded files are part of the executable image; there is no decompression step |
| Sets Brotli quality to 6 (patch `:8` to `:16`) | The packer's default made one archive take 132.8 s instead of 3.2 s (note `:51`) | Absent: no archive |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-YAOPKG-R1` | Each supported target gets one executable that runs with no interpreter, no runtime and no unpacking step on a machine that has never seen the product | note `:9`, `:93` |
| `DH-LIB-YAOPKG-R2` | The executable never substitutes itself for a command the model or user asked for. When it starts itself in a private helper mode, the selection (argument or environment variable) is removed before any further child is started | patch `:158`; `packages/ssh/ssh-helper-runtime/src/index.ts:29` |
| `DH-LIB-YAOPKG-R3` | Files compiled into the executable are read in place. If a file must exist on disk (an executable sidecar), it is written to a path derived from its digest by "temporary file in the same directory, then rename", so a path is either absent or complete, and concurrent first starts need no lock | patch `:50`; note `:26` |
| `DH-LIB-YAOPKG-R4` | The set of embedded assets is closed and listed at build time; a missing asset is a build or start-up error, never a silent fallback to a file on disk | note `:41` |
| `DH-LIB-YAOPKG-R5` | The remote helper and the SDK runtime are the same `dsh` executable in another mode, not separately built programs | note `:51` (one bootstrap dispatches to several entries) |
| `DH-LIB-NODESEA-R1` | Code that needs the location of the running program or of files shipped beside it gets it from one function, and nothing branches on "packaged or not" | the four `isSea()` sites |

## 5. Go choice

**Decision.** No packer. `go build` produces the executable; the `embed` package
carries assets; `os.Executable` replaces `isSea()`.

**Why.** R1 is what the Go toolchain does by default when cgo is off. R3 and R4 are
properties of `embed`: the files are in the binary's read-only data, opened through
`fs.FS`, with typed directory entries and no extraction.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`go build` with `CGO_ENABLED=0`, `embed`, `os.Executable`** (chosen) | One static file per target, cross-compiled from one runner; no cache, no hook, no archive | Every dependency of `dsh` must be pure Go ([../design.md](../design.md) CF-5). Embedded assets enlarge every binary | Standard toolchain, Go 1.27.4 installed. This repository's `.goreleaser.yaml:19` to `:22` already builds six targets this way for the sample program |
| An executable compressor after the build (GoReleaser has a `upx` section) | Smaller download | Compressed executables are a common cause of antivirus false positives and complicate signing. Inference; not measured | The `upx` key exists in `pkg/config/config.go` of `github.com/goreleaser/goreleaser/v2` v2.20.2; not evaluated further |
| Separate executables for the helper and the SDK runtime | Smaller helper | A second and third artifact family to build, sign and version; upstream already merged its entries behind one bootstrap | n/a |

<a id="dh-adr-yaopkg-1"></a>
#### DH-ADR-YAOPKG-1 No virtual filesystem and no run-time extraction; what must be on disk is a sidecar or an atomic cache entry

- **Decision.** Templates, static UI files, default configuration, built-in skills and catalogs are embedded and read through `fs.FS`. A third-party executable the product needs (for example a search tool) is shipped beside `dsh` in the archive or found on the machine; if a future feature must write an embedded executable to disk, it follows R3.
- **Why.** Upstream's VFS exists because Node loads code from files. Go loads nothing at run time ([DH-ADR-4](../design.md#dh-adr-4)), so only data needs carrying, and data does not need a file path.
- **Tradeoff.** An operator cannot patch a shipped template by editing a file; overrides go through configuration layers. The binary grows with its assets.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-YAOPKG-1`, `DH-T-YAOPKG-2`, and the release rehearsal `DH-E-TOOLCHAIN-2` in [../upstream/toolchain.md](../upstream/toolchain.md).

**Tradeoff of the whole choice.** Cross-compilation removes upstream's five native build
legs only while cgo stays off. The first cgo dependency brings back per-target runners
and, on Linux, a C-library floor.

## 6. Design notes

- **Targets.** Linux, macOS and Windows on amd64 and arm64, as DH-ADR-TOOLCHAIN-13 fixes. That is upstream's five targets plus Windows arm64, which upstream excludes (note `:53`).
- **What remains of the packer's job.** (a) Choosing what to embed and keeping it small: the UI and catalogs, not large optional payloads. (b) Sidecars: the archive layout must keep any sidecar beside the binary, and the program finds it through one `os.Executable`-based function that resolves symbolic links (the terminal command of the desktop application is a link). (c) Reproducibility: `-trimpath` and build information give the version and commit.
- **Helper modes.** `dsh` selects a private mode (sandbox launch stage, remote helper, update helper) from a reserved first argument. The spawn helper builds every child environment explicitly, so no selection variable is inherited (R2).
- **Remote helper.** Upstream publishes helper archives per platform and the operator installs one on the SSH host (`packages/ssh/ssh/README.md:36`). In Go the remote side needs the `dsh` archive for the remote's platform. Whether the local host uploads it, downloads it on the remote, or the operator installs it is a decision for the SSH area; embedding every platform's binary in every binary is rejected here for size.
- **`DSH_NODE_EXECUTABLE`.** Upstream exports the path of its own Node to shell tools. The Go host has no Node to offer and embeds no interpreter ([DH-ADR-8](../design.md#dh-adr-8)), so the variable is not exported. A script uses whatever languages the machine has, and reaches tools through `dsh tool call`.
- **Failure and cleanup.** Nothing is created at run time by this choice, so there is nothing to clean. A cache entry written under R3 is owned by the product version that wrote it and may be deleted by any later run that finds its digest unused.

## 7. Risks and open questions

1. **Binary size** with embedded assets is unknown until built; it also decides whether the remote helper can share the binary. Open question for the command-line area.
2. **Pure Go** is the owner's rule for `dsh` ([DH-ADR-15](../design.md#dh-adr-15)). The Python wheel's platform tags depend on it: a static binary has no C-library floor. Inference; the Python SDK agent decides the tags.
3. **Linux C library.** Inference: with cgo off the binary uses Go's own resolver and needs no `manylinux` build container. To be confirmed by running the release rehearsal on an old distribution.
4. **For the SSH area (A10):** how the helper binary reaches the remote host, and digest verification of it, which upstream does today (`packages/ssh/ssh/README.md:12`).

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-YAOPKG-1` | local | `DH-LIB-YAOPKG-R1`, `DH-LIB-YAOPKG-R4` | A test lists the embedded file systems and compares them with a committed manifest; removing a required asset fails start-up with its name; the product builds for all six targets with cgo off |
| `DH-T-YAOPKG-2` | local | `DH-LIB-YAOPKG-R3` | Many goroutines and several processes materialise the same stand-in file into one empty cache directory; every reader sees an absent or complete file and no temporary file remains |
| `DH-T-YAOPKG-3` | local | `DH-LIB-YAOPKG-R2`, `DH-LIB-YAOPKG-R5` | A child started from a helper mode has no mode selector in its arguments or environment; a command named like an interpreter is resolved through `PATH` and is never the harness |
| `DH-T-NODESEA-1` | local | `DH-LIB-NODESEA-R1` | The location function returns the real directory when the program is started through a symbolic link; an analyzer rule finds no other call to `os.Executable` in the module |

The real-environment proof that the archive runs on a clean machine of each platform is
`DH-E-TOOLCHAIN-2`. Nothing here has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-@yao-pkg/pkg`: replaced (`DH-LIB-YAOPKG`).
- `UP-DEP-node:sea`: replaced (`DH-LIB-NODESEA`).
- `UP-DEBT-patch.@yao-pkg__pkg@6.21.0`: resolved.
