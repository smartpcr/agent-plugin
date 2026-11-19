# @vscode/ripgrep: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". No search speed in this document was measured.

| | |
|---|---|
| **Library ID** | `DH-LIB-RIPGREP` (search engine), `DH-LIB-RIPGREPGLOB` (glob matching it needs) |
| **Upstream unit** | `UP-DEP-@vscode/ripgrep` |
| **Upstream version** | 1.18.0, with one platform package per operating system and processor |
| **Kind** | native: an npm package whose only content is a prebuilt `rg` executable |
| **Used by** | util (`packages/util/ripgrep`), and through it fs (`packages/fs/tool-fs-search`) and api (`packages/api/workspace-files`) |
| **Go choice** | own code: an in-process search package on the standard library, with `github.com/bmatcuk/doublestar/v4` for glob patterns. No `rg` executable is shipped or required. |
| **Related areas** | filesystem tools, workspace files, sandbox, release |

## 1. What it is and why upstream needs it

ripgrep (`rg`) is a command-line program, written in Rust, that searches a directory tree
for lines matching a regular expression. It is fast, skips files that version control
ignores, and can also just list files. `@vscode/ripgrep` is a way to ship that program
through npm: installing it downloads the `rg` executable for the current platform, and
its single export is the path to that file.

The harness uses it for three things. The model's `grep` tool searches file contents. The
model's `glob` tool lists files that match a pattern. The UI's "find a file by name" box
lists the workspace's files. In all three the harness starts `rg` as a child process and
reads what it prints.

Upstream chose a shipped executable so that no user has to install anything
(`packages/fs/tool-fs-search/src/search-core.ts:10-12`). The price is that every way of
packaging the product has to carry, find and sign a second executable.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | What is sent to `rg` and what is read back |
|---|---|---|
| `packages/util/ripgrep/src/index.ts:14-27` | Find the executable | Three cases: a `-rg` file beside a single-file runtime, the npm platform package, or the same path with `app.asar` rewritten to `app.asar.unpacked` inside the desktop app |
| `packages/util/ripgrep/src/index.ts:38-41` | Start it | Always prepends `--no-config`, so that a configuration file on the machine cannot add `--pre` and make `rg` run an arbitrary program for every file (`search-core.ts:166-169`) |
| `packages/util/ripgrep/src/index.ts:61-109` | Read output as records | Splits standard output on newline or NUL, decodes as UTF-8, and can stop the process early |
| `packages/fs/tool-fs-search/src/grep.ts:113-118` | `grep` tool | `--json --regexp=<pattern> [--glob=<include>] [-- <path>]`. Reads one JSON object per line and keeps only `match` records: file path, line number, line text; a line that is not UTF-8 arrives as base64 and is shown as a placeholder (`grep.ts:136-162`). No `--hidden` and no `--no-ignore`, so hidden and ignored files are skipped. |
| `packages/fs/tool-fs-search/src/glob.ts:93-122` | `glob` tool | `--files --glob=<pattern> --sort=modified --no-ignore --hidden`, plus two exclusions for each version-control directory name (`.git`, `.svn`, `.hg`, `.bzr`, `.jj`, `.sl`, `glob.ts:37`). Reads one path per line. |
| `packages/fs/tool-fs-search/src/search-core.ts:194-259` | Run and classify | Exit 0 is "found", exit 1 is "nothing found", anything else is an error; an error whose text contains "regex parse error" or "error parsing glob" is reported as an invalid pattern (`:125-131`). |
| `packages/api/workspace-files/src/search.ts:78-83` | UI file-name search | `--files --sort=path --null --path-separator=/`, optionally `--hidden` and `--no-ignore`, plus exclusions. Reads NUL-separated paths, stops after `maxEntries`, and treats exit 2 as "partial result" (`:109-119`). |

Limits upstream applies around the process: 30 s per call (`search-core.ts:44`), 20 MB of
raw output (`:37`), 250 matches (`grep.ts:29`), 2,000 bytes per matched line (`:35`), 100
paths (`glob.ts:25`).

Packaging code that exists only because of the executable:
`apps/desktop/scripts/electron-builder-config.mjs:78` (keep it outside the archive),
`apps/desktop/tests/macos-signature.spec.ts:75` (sign it),
`packages/experimental/webworker-runtime/src/node/external_packages/ripgrep.ts:2` (a stub
where it cannot run), and a copy step for the Python runtime
(`packages/fs/tool-fs-search/README.md:230`).

## 3. Tech debt upstream carries

Search performed: every reference to `@vscode/ripgrep` outside lock files; `patches/` (no
patch); `pnpm-workspace.yaml` (no pin; the package has no install script entry); the
"Known Limitations" sections of `packages/util/ripgrep`, `packages/fs/tool-fs-search` and
`packages/api/workspace-files`. The README bullets are ledger units owned by the area
agents; they are repeated here because the executable causes them.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L9.2` | Finding the executable depends on how the product was packaged (three code paths), and each packaging has its own step to carry, unpack or sign it. An unsupported platform or a damaged install makes both tools fail. | `packages/util/ripgrep/src/index.ts:14-27`; `apps/desktop/scripts/electron-builder-config.mjs:78`; `apps/desktop/tests/macos-signature.spec.ts:75`; `packages/fs/tool-fs-search/README.md:230` | Resolved by [DH-ADR-RIPGREP-1](#dh-adr-ripgrep-1): search is code inside the one binary, so there is nothing to find, unpack or sign. |
| (owned by the util area) | The executable must be on the same machine as the files; a remote workspace has no search and there is no fallback | `packages/util/ripgrep/README.md:68` | `DH-LIB-RIPGREP-R6`: search reads through the harness's filesystem interface. |
| (owned by the util area) | A file name that is not valid UTF-8 comes back with replacement characters and cannot be opened by that string | `packages/util/ripgrep/README.md:69` | `DH-LIB-RIPGREP-R7`: there is no text pipe to decode; Go strings hold the name's bytes unchanged. |
| (owned by the filesystem area) | Search and file reads are not proven to see the same workspace | `packages/fs/tool-fs-search/README.md:229` | `DH-LIB-RIPGREP-R6`: one filesystem view for both. |
| (owned by the filesystem area) | The search process is started outside the sandbox | `packages/fs/tool-fs-search/src/search-core.ts:166` | `DH-LIB-RIPGREP-R5`, `R6`: no process, no ambient configuration, same path policy as reads. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-RIPGREP-R1` | Content search: given a regular expression, a root (file or directory) and an optional include glob, return each matching line as file path, 1-based line number and line text. Files ignored by version control, hidden files and binary files are skipped. | `grep.ts:113-162` |
| `DH-LIB-RIPGREP-R2` | File listing: given a glob pattern and a root, return matching file paths ordered by modification time, including ignored and hidden files but never the contents of version-control directories. | `glob.ts:93-122` |
| `DH-LIB-RIPGREP-R3` | File-name listing for the UI: every file under a root in path order, with switches for hidden files and ignore rules and a list of excluded names; stops after a set number of files and says that the result is incomplete. | `workspace-files/src/search.ts:78-119` |
| `DH-LIB-RIPGREP-R4` | Every search honours a deadline and cancellation promptly and is bounded in matches, bytes per line and files visited. A result cut short by a bound says so; it is never returned as if complete. | `search-core.ts:37-44`, `:140-156`; `grep.ts:29-35` |
| `DH-LIB-RIPGREP-R5` | Nothing on the machine outside the call (environment variables, configuration files) can change what a search does or make it run a program. | `ripgrep/src/index.ts:38-41` |
| `DH-LIB-RIPGREP-R6` | Search needs no executable besides the harness and reads files through the same filesystem interface and path policy as the read tools. | `ripgrep/README.md:68`, `tool-fs-search/README.md:229` |
| `DH-LIB-RIPGREP-R7` | A matched line that is not valid UTF-8 is reported with a placeholder instead of failing the search. A file name that is not valid UTF-8 can still be opened from the result. | `grep.ts:158-160`, `ripgrep/README.md:69` |
| `DH-LIB-RIPGREP-R8` | A pattern that cannot be compiled is reported as "invalid pattern" with the compiler's message, distinct from "search failed". | `search-core.ts:125-131` |
| `DH-LIB-RIPGREP-R9` | The same search over the same files returns results in the same order. | Inference: results are persisted in the session log and shown to the model, so replay and tests need a stable order |

## 5. Go choice

<a id="dh-adr-ripgrep-1"></a>
### DH-ADR-RIPGREP-1 Search in process, in pure Go; no `rg` executable

- **Decision.** The three search functions are implemented by a harness package that
  walks the filesystem and matches in process. The release contains no `rg` executable
  and does not look for one on the machine.
- **Why.** The release plan is one self-contained binary per platform built without cgo
  ([DH-ADR-TOOLCHAIN-13](../upstream/toolchain.md#dh-adr-toolchain-13)). A second
  executable breaks that in every variant (see the table below). An in-process search
  also removes three upstream limits at once: it works wherever the harness's filesystem
  interface works, it obeys the same sandbox policy as file reads, and it cannot be
  redirected by configuration on the machine.
- **Tradeoff.** Searching is slower than ripgrep, by an amount that has not been
  measured, and the regular-expression dialect changes
  ([DH-ADR-RIPGREP-2](#dh-adr-ripgrep-2)). If `DH-E-RIPGREP-1` misses its budget, the
  recorded alternative is the sidecar option below, taken by a new decision; the
  `Searcher` interface in section 6 exists so that this would not change any caller.
- **Serves.** `DH-LIB-RIPGREP-R5`, `R6`, `R7`; removes `UP-DEBT-found.L9.2`.
- **Verified by.** `DH-T-RIPGREP-1` to `DH-T-RIPGREP-7`, `DH-E-RIPGREP-1`,
  `DH-E-RIPGREP-2`.

| Option | What it does to the single-binary release | Search speed on a large repository | Other effects |
|---|---|---|---|
| **Chosen: pure Go, in process** | Nothing. One binary, pure Go. | Inference: slower. Go's `regexp` guarantees linear time but has none of ripgrep's vectorised literal search; for content search over a very large tree, expect a several-fold gap on patterns that are not plain words, less on plain words if a literal pre-filter is added (section 6). Not measured. | Same filesystem view and sandbox as reads. Regular-expression dialect is Go's. |
| Ship `rg` as a file beside the binary (what upstream does) | The release becomes an archive of two executables per platform. Both must be signed and notarised on macOS and both are scanned on Windows. The desktop and Python packagings each need a copy step. | Fastest. | Keeps upstream's limits: local files only, outside the sandbox. A new `rg` version can change its output format. |
| Embed `rg` in the binary and unpack it on first use | Still one file to download, but larger. Inference: about 4 to 6 MB more per platform. The harness must write an executable to disk and run it, which fails on volumes mounted `noexec`, is blocked by some endpoint-protection policies, and on macOS runs a file that was not notarised as part of the app. Six platform builds each need the right `rg`. | Fastest after the first run. | Same limits as the row above, plus cleanup of the unpacked file. |
| Require `rg` on the machine | Nothing in the release. | Fastest when present. | The two most used discovery tools fail on a machine without `rg`. Upstream rejected exactly this (`ripgrep/src/index.ts:11`). The user's `rg` version and configuration are outside the harness's control. |

A note on how much speed is at stake: from ripgrep's documentation, not measured here,
any `--sort` option makes ripgrep run on a single thread. Two of upstream's three uses
pass `--sort` (`glob.ts:108`, `workspace-files/src/search.ts:79`), and those two only
list files. The gap that matters is content search.

<a id="dh-adr-ripgrep-2"></a>
### DH-ADR-RIPGREP-2 The pattern dialect is Go's `regexp`, and the tool says so

- **Decision.** `grep` patterns are compiled by the standard library's `regexp` package
  (RE2 syntax). The tool description names the dialect. A pattern Go cannot compile
  returns the invalid-pattern error with Go's message.
- **Why.** Upstream's tool tells the model "ripgrep syntax" (`grep.ts:288-291`), which is
  the Rust `regex` dialect. Rust's and Go's dialects come from the same design: both
  refuse look-around and back-references, and both guarantee linear time. Inference: most
  patterns a model writes mean the same in both.
- **Tradeoff.** Some patterns differ. Inference, to be confirmed by `DH-T-RIPGREP-6`:
  `\d`, `\w`, `\s` and `\b` are ASCII-only in Go and Unicode-aware in Rust; Rust accepts
  class set operations such as `[a-z&&[^aeiou]]` and Go does not. A model that was taught
  ripgrep syntax may write these.
- **Serves.** `DH-LIB-RIPGREP-R8`.
- **Verified by.** `DH-T-RIPGREP-6`.

### Modules

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen for globs:** `github.com/bmatcuk/doublestar/v4` | `**`, `{a,b}` and character classes, the forms the tools accept (`grep.ts:293`); can validate a pattern before use | Does not implement ignore-file rules; those are own code on top | v4.10.4, 2026-10-24, MIT, no requirements, no cgo. Exports `Match`, `PathMatch`, `ValidatePattern`, `SplitPattern`. Checked with `go list -m -json`, `go mod download -json`, `LICENSE` and source in the module cache, 2026-11-18. |
| **Chosen for matching:** standard library `regexp`, `regexp/syntax`, `bytes` | No dependency; linear-time guarantee | Slower than a vectorised engine | `go doc`, Go 1.27.4 |
| Ignore rules: own code (chosen) | Exactly the rules the tools need, testable against Git itself (`DH-E-RIPGREP-2`) | About 300 lines to own | not applicable |
| Ignore rules: `github.com/boyter/gocodewalker` | A walker that already applies `.gitignore` and `.ignore` | Brings its own walk and concurrency model, which would bypass the harness filesystem interface; requires an unmaintained `fnmatch` module from 2016 | v1.5.1, 2025-08-26, MIT; requires `github.com/danwakefield/fnmatch` and `golang.org/x/sync`; no cgo; same method, 2026-11-18 |
| Ignore rules: `github.com/sabhiram/go-gitignore` | Small | No tagged release; last commit 2021 | pseudo-version of 2021-09-23, MIT; same method |
| A faster regular-expression engine: `github.com/wasilibs/go-re2` | RE2 compiled to WebAssembly, no cgo | A WebAssembly runtime inside the binary for one feature; considered only if the benchmark fails | v1.13.0, 2026-09-25; existence and date only, licence not read |

## 6. Design notes

**Owning package.** `search`, a harness package with no cordis dependency, used by the
filesystem tools and by the workspace-files service.

```go
type Searcher interface {
    Grep(ctx context.Context, q GrepQuery, emit func(Match) bool) (Summary, error)
    Files(ctx context.Context, q FilesQuery, emit func(path string) bool) (Summary, error)
}

// Source is the caller's view of the workspace: list a directory, open a file.
// It is a harness interface, not io/fs.FS (see the note below).
type Source interface {
    ReadDir(name string) ([]fs.DirEntry, error)
    Open(name string) (io.ReadCloser, error)
}

type GrepQuery struct {
    From    Source
    Root    string
    Pattern string // RE2
    Include string // one glob, optional
    Limits  Limits // matches, bytes per line, files, total bytes
}

type Summary struct {
    Incomplete bool   // a limit, the deadline or an unreadable directory cut the walk short
    Reason     string
}
```

**Why not `io/fs.FS`.** The standard interface would be the obvious view type, but
`fs.ValidPath` rejects any name that is not valid UTF-8 (`io/fs/fs.go:64-67`, Go
1.27.4), so a file with such a name could be listed and never opened. That would keep
the upstream limit `DH-LIB-RIPGREP-R7` is meant to remove.

**How a content search runs.** One goroutine walks the tree in name order, applying the
ignore rules as it descends, and hands file paths to a pool of workers sized to the
number of processors. Each worker skips a file whose first 8,000 bytes contain a NUL
byte (the test Git uses for "binary"), then scans it line by line. Before running the
regular expression on a line, a worker checks for a literal string that every match must
contain, when the pattern has one (found with `regexp/syntax`); `bytes.Index` is
vectorised in Go's runtime, so plain-word searches do not pay for the general engine.
Results are released in walk order, so the output is stable (`DH-LIB-RIPGREP-R9`).

**Ignore rules.** Content search and the UI listing honour `.gitignore` files at every
level, `.ignore` files, and `.git/info/exclude`, and skip names that start with a dot.
File listing for the `glob` tool applies none of them but always skips the six
version-control directory names. Inference: ripgrep also reads the user's global Git
ignore file; the Go design does not, because `DH-LIB-RIPGREP-R5` forbids machine-wide
configuration from changing a result.

**Failure, retry and cleanup.** A search holds only open file handles and memory, all
released when the call returns. Cancellation is checked between files and every few
thousand lines. An unreadable file or directory is skipped and sets `Incomplete`; it is
not a failure. Reaching a limit stops the walk and sets `Incomplete` with the reason.
There is no process to kill and nothing to retry.

**Ownership.** The caller owns the call; nothing outlives it. The `Source` is the
caller's, so access is decided by whoever built that view: a sandboxed tool passes its
confined view and cannot search outside it.

**Verdict on upstream's design.** Keep the tool contracts, the limits and the
invalid-pattern distinction. Drop the child process, the JSON transport, the executable
resolver and the `--no-config` defence.

## 7. Risks and open questions

- **Speed is unmeasured.** `DH-E-RIPGREP-1` is the gate. Its budget, proposed as "a
  content search for a plain word over a 100,000-file checkout returns its first 250
  matches within 3 s on a developer laptop", must be agreed by the filesystem area.
- **Dialect drift.** If models keep writing Rust-only syntax, the tool can translate the
  two cases in DH-ADR-RIPGREP-2 or the description can show examples. To be decided from
  recorded sessions, not now.
- **Remote workspaces.** Searching file by file over a slow remote filesystem is
  impractical. The SSH area decides whether its remote helper runs this same package on
  the remote machine.
- **Glob module ownership.** Another library agent may choose a glob module for
  upstream's `picomatch` uses. If it differs from `doublestar`, one of the two should be
  dropped during review.
- **Owning area.** The `search` package needs a deliverable in the filesystem area.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-RIPGREP-1` | local | `DH-LIB-RIPGREP-R1` | Over a fixture tree: matches carry the right path, line number and text; ignored, hidden and binary files are absent; the include glob narrows the set. |
| `DH-T-RIPGREP-2` | local | `DH-LIB-RIPGREP-R2`, `R3` | File listing returns ignored and hidden files but nothing under `.git`; ordering by time and by path; excluded names; the entry cap sets `Incomplete`. |
| `DH-T-RIPGREP-3` | local | `DH-LIB-RIPGREP-R4` | Cancelling the context returns within a bound; each limit stops the walk and is named in `Summary.Reason`. |
| `DH-T-RIPGREP-4` | local | `DH-LIB-RIPGREP-R5`, `R6` | With `RIPGREP_CONFIG_PATH` set and an `rg` on `PATH`, results are unchanged and no process is started; a search through a confined `Source` cannot name a file outside it. |
| `DH-T-RIPGREP-5` | local | `DH-LIB-RIPGREP-R7`, `R9` | A line with invalid UTF-8 yields the placeholder; on Linux a file with a non-UTF-8 name is found and can be opened by the returned name; repeated runs give identical order. |
| `DH-T-RIPGREP-6` | local | `DH-LIB-RIPGREP-R8` | A table of patterns: valid ones compile and match as documented; invalid ones return the invalid-pattern error; the known Rust-only forms are listed with their Go behaviour. |
| `DH-T-RIPGREP-7` | local | `DH-LIB-RIPGREP-R1` | Ignore-rule table: nested files, negation, directory-only patterns, anchored patterns, `.ignore` over `.gitignore`. |
| `DH-E-RIPGREP-1` | real environment | `DH-LIB-RIPGREP-R4` | **Given** real checkouts of a small, a medium and a very large public repository on Linux, macOS and Windows, and an `rg` installed for comparison only. **When** a fixed set of plain-word and regular-expression searches runs through the harness tool and through `rg`. **Then** every search meets the agreed budget, and the ratio to `rg` is recorded per search. **Cleanup:** remove the checkouts; keep the timing report. |
| `DH-E-RIPGREP-2` | real environment | `DH-LIB-RIPGREP-R1` | **Given** the same real checkouts with Git installed. **When** the harness lists files with ignore rules on. **Then** the set equals `git ls-files --cached --others --exclude-standard` minus hidden files, with every difference explained. **Cleanup:** none beyond removing the checkouts. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@vscode/ripgrep`, `UP-DEBT-found.L9.2`.
