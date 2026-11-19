# cross-spawn: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The mechanism is specified in
> [node-process-builtins.md](node-process-builtins.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-CROSSSPAWN` |
| **Upstream unit** | `UP-DEP-cross-spawn` |
| **Upstream version** | 7.0.6 (depends on `which` 2.0.2, `path-key` and `shebang-command`, `pnpm-lock.yaml:32320-32324`) |
| **Kind** | runtime, but never imported by upstream source: it is needed by third-party code that upstream embeds |
| **Used by** | experimental (`packages/experimental/subagent-oar`) |
| **Go choice** | standard library, through the harness process package |
| **Related areas** | external subagents; [which.md](which.md); [execa.md](execa.md) |

## 1. What it is and why upstream needs it

Node's `spawn` cannot start many Windows commands by name. It does not apply `PATHEXT`,
cannot run the `.cmd` files that npm installs for command-line tools, and does not
understand a script's first-line interpreter marker. cross-spawn fixes this: it finds the
real file, and when the file is not an ordinary executable it starts `cmd.exe` with a
command line escaped for `cmd.exe`.

Upstream does not call it. The package `subagent-oar` embeds a third-party library,
`@botiverse/oar`, into its own published file
(`packages/experimental/subagent-oar/tsdown.config.ts:13`), and declares cross-spawn so
that the embedded code can load it (`packages/experimental/subagent-oar/package.json:53`).
That library starts other vendors' agent command-line tools (`codex`, `claude` and
others, `packages/experimental/subagent-oar/README.md:93`) and "owns native processes;
these sessions do not use the DSH subprocess provider" (README `:103`). Inference: it
uses cross-spawn to start those tools on Windows, where they are installed as `.cmd`
files. The library's source is not in the checkout, so the call sites could not be read.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/subagent-oar/package.json:53` | Declared for the embedded `@botiverse/oar` (a dev dependency, bundled at build time) | not visible in the checkout |
| any upstream `.ts`, `.mjs` or `.js` file | no import found (searched `from 'cross-spawn'`, `require(`, `import(`) | none |

Behaviours the embedded code can be assumed to rely on, and where each goes:

| Behaviour | Go mapping |
|---|---|
| Find a command by bare name with `PATH` and `PATHEXT` | Process package lookup (`DH-LIB-NODEPROC-R2`, [which.md](which.md)) |
| Start a `.cmd` or `.bat` file through `cmd.exe` with arguments escaped for it | The checked batch run: opt-in, and refusing arguments that cannot be passed unchanged (`DH-LIB-NODEPROC-R4`, [DH-ADR-NODEPROC-1](node-process-builtins.md#dh-adr-nodeproc-1)) |
| Run a script through the interpreter named on its first line | Not provided. On POSIX the kernel does this; on Windows the caller names the interpreter as the program |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CROSSSPAWN-R1` | An external agent tool can be started by bare name on Windows whether it is installed as an executable or as a `.cmd` file, and its arguments arrive unchanged or the start is refused with a reason | `subagent-oar/README.md:93`, `:103` |
| `DH-LIB-CROSSSPAWN-R2` | Those tools are started through the harness process package, so that they get the same environment scrubbing, tree ownership and cleanup as every other child | `subagent-oar/README.md:103` (upstream's exception, which the Go design does not keep) |

## 5. Go choice

**Decision.** No module. `os/exec` already applies `PATHEXT` and can start a batch file;
what it does not do is quote for `cmd.exe`, and that rule lives in the harness process
package.

**Why.** cross-spawn exists because of a gap in Node. The remaining real problem, batch
files, is a safety rule that must not be delegated to whichever library a caller picks.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Process package: lookup plus checked batch run** (chosen) | One rule; refusal instead of guessed escaping | A tool whose arguments contain `"` or `%` cannot be started through its `.cmd` file; the caller must start the program behind it | `exec.LookPath` and `PATHEXT`: `GOROOT/src/os/exec/lp_windows.go:114`; the batch exception: `GOROOT/src/os/exec/exec.go:407-414` |
| Escape for `cmd.exe` with carets, as cross-spawn does | Accepts more arguments | `%NAME%` substitution cannot be escaped reliably on a command line, so "accepts more" includes accepting some arguments wrongly | n/a |

**Tradeoff.** Stricter than upstream on Windows for unusual arguments.

**Verdict on upstream.** *Change.* Letting an embedded library start processes outside
the provider is the exception `DH-LIB-CROSSSPAWN-R2` removes.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CROSSSPAWN-1` | local, Windows | `DH-LIB-CROSSSPAWN-R1`, `R2` | A stand-in tool installed once as `tool.exe` and once as `tool.cmd` is started by the name `tool` with ordinary arguments, which arrive unchanged; with an argument containing `%` the `.cmd` start is refused; in both cases the child's environment is the scrubbed one and the child is gone after its owner unloads |

Nothing has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-cross-spawn`
