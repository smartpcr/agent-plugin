# @yarnpkg/parsers: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". No harness code exists.
>
> **Unverified research notes.** Where this document says "probe", it reports an
> observation made during library research on 2026-11-18 by running a throwaway program
> against the named module outside this repository. That was against the project's rule
> that nothing is built or run in the design task, and the program was not kept, so the
> observation cannot be repeated from this repository. Every such statement is therefore
> **unverified and to be measured**: the linked packages by `DH-T-YARNPKGPARSERS-1`
> (section 8), and the parser's behaviour by `DH-T-SHELLQUOTE-1` in
> [shell-quote.md](shell-quote.md), which uses the same `syntax` package.

| | |
|---|---|
| **Library ID** | `DH-LIB-YARNPKGPARSERS` |
| **Upstream unit** | `UP-DEP-@yarnpkg/parsers` |
| **Upstream version** | 3.1.0 (`pnpm-lock.yaml`) |
| **Kind** | runtime, bundled into the browser worker of one experimental package |
| **Used by** | experimental (`packages/experimental/webworker-runtime`) |
| **Go choice** | dropped: the owner dropped the in-browser Preview runtime, its only user ([DH-ADR-17](../design.md#dh-adr-17)) |
| **Related areas** | X2, which records the drop of the Preview runtime ([../areas/x-liveshare-webworker.md](../areas/x-liveshare-webworker.md)) |

## 1. What it is and why upstream needs it

The name suggests npm package handling, and that is not what it is used for.
`@yarnpkg/parsers` is a package from the Yarn package manager that contains several
parsers. The harness uses exactly one: `parseShell`, which turns a shell command line such
as `ls *.md | grep x && echo done` into a tree of commands, pipes, redirections and
arguments. Nothing in the harness uses it to resolve or install packages.

It exists because of the experimental **Preview**: a build in which the whole harness
runs inside a browser Web Worker, with no operating system underneath
(`packages/experimental/webworker-runtime/README.md:11`). The model still calls its `bash`
tool there, so the package ships a small shell written in JavaScript: this parser, an
interpreter that walks the tree
(`packages/experimental/webworker-runtime/src/shell/interpret.ts:1-11`), and a table of
a few dozen commands such as `cd`, `ls`, `find` and `echo`, written in JavaScript over an
in-memory file system (`packages/experimental/webworker-runtime/src/shell/programs/index.ts:20-28`).
The shell directory is about 1,900 lines.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/webworker-runtime/src/shell/interpret.ts:13`, `:78` | Parse one command line; the caller supplies the test for "is this word a glob" | `parseShell(source, { isGlobPattern })` |
| `packages/experimental/webworker-runtime/src/shell/ast.ts:12-14` | The tree's types, re-exported for the interpreter | `Argument`, `CommandChain`, `CommandLine`, `ShellLine` and related types |
| `packages/experimental/webworker-runtime/tsdown.config.ts:11-32` | A build plugin redirects the import to a file inside the package that its public entry list does not offer, to keep an unused YAML-like parser and `js-yaml` (about 175 kB) out of the worker bundle | none |

## 3. Tech debt upstream carries

Search performed: every import of `@yarnpkg/parsers`; `patches/` and
`pnpm-workspace.yaml` (no patch or pin); the package README's "Known Limitations".

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-limit.packages.experimental.webworker-runtime.17` (owned by the experimental area, not dispositioned here) | The worker build pins a private path inside this package, which an upgrade can break | `packages/experimental/webworker-runtime/README.md:73`; `packages/experimental/webworker-runtime/tsdown.config.ts:11-24` | Input for that area: with [DH-ADR-YARNPKGPARSERS-1](#dh-adr-yarnpkgparsers-1) there is no bundle and no borrowed grammar, and the Preview itself is dropped ([DH-ADR-17](../design.md#dh-adr-17)), so the cause is gone. |
| (the next limitation in the same README, also owned there) | "The shell is not bash": no loops, functions or `case`; `sed` only substitutes; patterns are JavaScript regular expressions | `packages/experimental/webworker-runtime/README.md:74` | A complete shell implementation replaces a partial one if the Preview is kept (section 5) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-YARNPKGPARSERS-R1` | Where the host runs on an operating system, a shell command is run by a real shell behind the shell seam and its sandbox. The host does not interpret shell language itself. | Inference: the JavaScript shell exists only because a browser worker has no processes |
| `DH-LIB-YARNPKGPARSERS-R2` | **Not part of this design**: the owner dropped the Preview ([DH-ADR-17](../design.md#dh-adr-17)). Recorded for whoever reopens it: if a host without an operating system is ever built, shell command lines are parsed and run by one complete, maintained shell implementation with the file system and the command table supplied by the host. The harness writes neither a shell grammar nor a shell interpreter. | `packages/experimental/webworker-runtime/src/shell/interpret.ts:1-11`; `packages/experimental/webworker-runtime/README.md:74` |

## 5. Go choice

**Decision.** Dropped. The Go harness has no counterpart of this dependency: the owner
dropped the browser Preview ([DH-ADR-17](../design.md#dh-adr-17)), which was its only
user. Had the Preview been kept, the replacement would have been `mvdan.cc/sh/v3`;
upstream's interpreter is not ported in either case.

**Why.** R1: a Go host has processes, so the `bash` tool reaches a real shell and the
only consumer of the parser disappears. R2: if a shell must run inside the host after all,
a Go shell implementation with a parser and an interpreter already exists, so borrowing a
grammar from a package manager and writing an interpreter around it would repeat
upstream's partial shell.

<a id="dh-adr-yarnpkgparsers-1"></a>
### DH-ADR-YARNPKGPARSERS-1 No shell language implementation in the harness

- **Decision.** The harness does not carry its own shell parser or interpreter. On an
  operating system the real shell runs commands (R1). No build without an operating
  system exists ([DH-ADR-17](../design.md#dh-adr-17)).
- **Why.** The dependency serves one experimental package whose existence depends on the
  host being JavaScript ([DH-ADR-2](../design.md#dh-adr-2)). Verdict on upstream: drop
  the parser and the hand-written interpreter, together with the Preview they served.
- **Tradeoff.** No in-browser demonstration of the harness: nothing runs without the
  host.
- **Serves.** DH-O-10: the shell stays a seam whose providers run a real shell.
- **Verified by.** DH-T-YARNPKGPARSERS-1.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Dropped (chosen; the owner dropped the Preview, [DH-ADR-17](../design.md#dh-adr-17))** | Nothing to build or maintain | The Preview is not available | n/a |
| `mvdan.cc/sh/v3`, which a kept Preview would have needed. Not used | A parser (`syntax`) for POSIX shell and Bash with positions on errors, and an interpreter (`interp`) whose program execution, file opening and directory reading are replaceable handlers, which is the hook an in-memory file system needs | The interpreter package links `golang.org/x/sys` and `golang.org/x/term`; whether it builds and behaves for WebAssembly in a browser was not checked | v3.14.1, released 2026-09-06, BSD-3-Clause, pure Go, `go.mod` asks for Go 1.26. In the probe (unverified, to be measured) the `syntax` package was reported to link no third-party module, to parse a line with pipes, `&&`, `\|\|`, a subshell, redirections and a command substitution, and to report `1:6: reached EOF without closing quote` for bad input. Checked 2026-11-18 with `go list -m -json`, `go mod download -json` and the `LICENSE` in the module cache; the rest is from the unverified probe. |
| Port upstream's interpreter with a hand-written Go grammar | Same behaviour as upstream's Preview | About 1,900 lines to rewrite, and it stays "not bash" | n/a |

**Tradeoff.** See the decision.

## 7. Risks and open questions

1. **Settled by the owner.** The Preview is dropped
   ([DH-ADR-17](../design.md#dh-adr-17)), so this library item stays `dropped` and no
   WebAssembly host is designed.
2. **Other uses of a shell parser.** The `syntax` package of `mvdan.cc/sh/v3` is used
   elsewhere, to read shell syntax without running it: `DH-LIB-SHELLQUOTE` in
   [shell-quote.md](shell-quote.md). Its interpreter package is not linked
   (`DH-T-YARNPKGPARSERS-1`).

## 8. Verification

A local Go test. Nothing here has been run; the unverified probe looked at the alternative module only.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-YARNPKGPARSERS-1` | local | `DH-LIB-YARNPKGPARSERS-R1` | A test over the packages linked into `cmd/dsh` (`go list -deps`) fails if a shell interpreter package, such as `mvdan.cc/sh/v3/interp`, is among them. |

`DH-LIB-YARNPKGPARSERS-R2` has no test: it is not part of this design
([DH-ADR-17](../design.md#dh-adr-17)).

## 9. Units dispositioned by this spec

`UP-DEP-@yarnpkg/parsers` (dropped).
