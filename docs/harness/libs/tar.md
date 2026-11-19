# tar: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-TAR` |
| **Upstream unit** | `UP-DEP-tar` |
| **Upstream version** | 7.5.22 (the npm package `tar`, also called node-tar) |
| **Kind** | runtime in one host package and one generator application; build-only at the root |
| **Used by** | connector (`packages/connector/upstream-skills`), apps (`apps/plugin-market-generator`), root scripts |
| **Go choice** | standard library (`archive/tar`, `compress/gzip`) |
| **Related areas** | A14, skills ([../areas/context-compaction-skill.md](../areas/context-compaction-skill.md)); release ([../upstream/toolchain.md](../upstream/toolchain.md)). The plugin market is dropped ([DH-ADR-13](../design.md#dh-adr-13)) |

## 1. What it is and why upstream needs it

A **tar archive** is a sequence of files, each preceded by a header that gives its name,
type and size; it is usually compressed as a whole with gzip (`.tar.gz`). It is the
format in which GitHub serves a snapshot of a repository and in which npm ships a
package.

The harness reads tarballs from the network in two places. The **upstream skills**
connector downloads a pinned commit of a public skills repository, keeps the files of
one directory and the licence, checks their size and digest against pinned values, and
writes them to disk. The **plugin market generator**, a separate command that builds a
market index, downloads npm packages and reads their manifests. Both treat the archive
as hostile input. Build scripts also create and unpack tarballs for helper programs.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/connector/upstream-skills/src/archive.ts:41-77` | Stream a gzip tarball from an HTTP response; reject any entry whose path has an empty, `.`, `..` or backslash segment; require one top-level directory; accept only regular files for kept paths; reject a path that appears twice; stop when the kept bytes exceed the pinned size | `t` (list) with `strict: true` and `onReadEntry`; `entry.path`, `entry.type`, `entry.size` |
| `packages/connector/upstream-skills/src/archive.ts:93-99`, `:165` | Digest the kept files by sorted path and content and compare with the pinned SHA-256 | own code over the entries |
| `apps/plugin-market-generator/src/fetchers.ts:67-83` | Unpack the regular files of an npm tarball into memory, without `node_modules`, with a total size limit | `Parser` with `strict: true`, `entry` events, `parser.abort` |
| `scripts/build-exe-for-ssh-helper.ts:168`, `scripts/cua-helper/package.ts:82-85`, `scripts/primary-runtime/prepare.ts:79`, `:98`, `:227`, `scripts/verify-ssh-helper-artifact.ts:47` | Build time: pack helper programs reproducibly (`portable: true`) and unpack downloaded runtimes | `create`, `extract` |

## 3. Tech debt upstream carries

Search performed: every import of `tar`; `patches/` and `pnpm-workspace.yaml` (nothing);
`packages/connector/upstream-skills/README.md:141` onward (no bullet names this
library).

One workaround is visible in calling code and is small enough to record here without a
ledger unit: the library is loaded lazily because the experimental browser runtime
cannot evaluate it (`packages/connector/upstream-skills/src/archive.ts:40`). It does not
apply in Go, where there is no browser-hosted host.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-TAR-R1` | Read a gzip tarball as a stream, without writing it to disk first and without holding more than one entry in memory. | `upstream-skills/src/archive.ts:30-85` |
| `DH-LIB-TAR-R2` | Refuse the archive on the first unsafe or unexpected entry: an unsafe path segment, more than one top-level directory, a kept path that is not a regular file, a repeated path, or more bytes than allowed. Stop reading at that point. | `upstream-skills/src/archive.ts:46-72`, `plugin-market-generator/src/fetchers.ts:74-81` |
| `DH-LIB-TAR-R3` | Nothing is written to its final place until the whole set has been read and its digest matches the pinned value. | `upstream-skills/src/archive.ts:156-175` |
| `DH-LIB-TAR-R4` | A tarball the harness or its release tooling writes is byte-for-byte reproducible from the same inputs. | `scripts/build-exe-for-ssh-helper.ts:168` (`portable: true`) |

## 5. Go choice

**Decision.** The standard library: `compress/gzip` wrapped by `archive/tar`.

**Why.** Both are stream readers pulled by the caller, which is exactly the shape of the
two readers above: `tar.Reader.Next` returns one header, and the entry's data is read
through the same reader, so a size limit is an `io.LimitReader` and stopping is simply
not reading further.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `archive/tar`, `compress/gzip` | No dependency; streaming; entry types are explicit (`TypeReg`, `TypeDir`, `TypeSymlink`, and so on) | Names are not checked for safety unless the caller does it | Go 1.27.4 source. `archive/tar/reader.go:49-64`: `ErrInsecurePath` is returned only when `GODEBUG=tarinsecurepath=0` is set. |
| `github.com/klauspost/compress` (`gzip`) | Faster gzip | A module for speed nobody has asked for; the archives are a few megabytes | v1.20.4, see [node-fs-path-os-zlib.md](node-fs-path-os-zlib.md) |

**Tradeoff.** None found for these uses.

Design notes in brief:

- The path checks are the harness's own and are kept as upstream wrote them; in
  addition each name must pass `filepath.IsLocal` after conversion to the local
  separator.
- Only `TypeReg` counts as a regular file. Links, devices and sparse entries in a kept
  path refuse the archive.
- **Failure and cleanup.** The reader keeps the kept files in memory (bounded by the
  pinned size), verifies the digest, then writes them into a new temporary directory
  beside the destination and renames it into place, using the atomic helpers in
  [node-fs-path-os-zlib.md](node-fs-path-os-zlib.md). A failure at any point before the
  rename removes the temporary directory and leaves the previous skills untouched. A
  failure of one download source tries the next, as upstream does
  (`upstream-skills/src/archive.ts:161-172`).
- **Ownership.** The HTTP response body is owned by the download call and closed when it
  returns; the temporary directory is owned by the same call until the rename, after
  which the skills directory is owned by the connector.
- **Writing.** For reproducible output the writer sets a fixed modification time, zero
  user and group, sorted entry order and a gzip header without name or time.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-TAR-1` | local | `DH-LIB-TAR-R1`, `R2` | Crafted tarballs built in the test: `..` segment, absolute path, backslash segment, two top-level directories, a symbolic link at a kept path, a repeated path, and one byte over the limit. Each is refused with the specified reason, and a counting reader shows that reading stopped at the offending entry. |
| `DH-T-TAR-2` | local | `DH-LIB-TAR-R3` | With a digest mismatch, or a failure injected between write and rename, the destination directory is unchanged and no temporary directory remains. |
| `DH-T-TAR-3` | local | `DH-LIB-TAR-R4` | Packing the same tree twice, with different file times and owners on disk, gives identical bytes. |

Local tests only. Downloading the real pinned archive from GitHub is a real-environment
scenario that belongs to the skills specification. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-tar`.
