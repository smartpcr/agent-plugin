# which: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The mechanism is specified in
> [node-process-builtins.md](node-process-builtins.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-WHICH` |
| **Upstream unit** | `UP-DEP-which` |
| **Upstream version** | 2.0.2 as declared; 5.0.0 and 6.0.1 are also in `pnpm-lock.yaml` through other packages (`pnpm-lock.yaml:28336-28348`) |
| **Kind** | runtime, but never imported by upstream source: needed by embedded third-party code, and by cross-spawn |
| **Used by** | experimental (`packages/experimental/subagent-oar`) |
| **Go choice** | own code: a short lookup function in the harness process package, modelled on `os/exec` |
| **Related areas** | subprocess, external subagents; [cross-spawn.md](cross-spawn.md) |

## 1. What it is and why upstream needs it

`which` answers "which file would run if I typed this command name?": it searches the
directories listed in `PATH`, on Windows trying each extension in `PATHEXT`.

Upstream does not import it. `subagent-oar` declares it
(`packages/experimental/subagent-oar/package.json:55`) for the embedded `@botiverse/oar`
library, whose patch "applies environment overrides to native installation probes"
(`packages/experimental/subagent-oar/README.md:105`). Inference: the library uses `which`
to probe whether a vendor's agent tool is installed. Where upstream needs the same
answer in its own code it wrote the lookup by hand: `resolveExecutable` in the local
subprocess provider (`packages/subprocess/subprocess-local/src/index.ts:168-208`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/subagent-oar/package.json:55` | Declared for the embedded `@botiverse/oar` | not visible in the checkout |
| any upstream `.ts`, `.mjs` or `.js` file | no import found | none |
| `packages/subprocess/subprocess-local/src/index.ts:168-208` (upstream's own equivalent) | Verify an absolute path, or search a bare name on the **child's** `PATH` with `.COM;.EXE;.BAT;.CMD` or `PATHEXT`; refuse a relative path; relative `PATH` entries resolve from the harness's working directory; names of `PATH` and `PATHEXT` compare without regard to case on Windows (`:386-392`) | none |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-WHICH-R1` | A command name is resolved against an **explicit** environment (the one the child will receive), not the harness's own | `subprocess-local/src/index.ts:175`, `:201-208`; `docs/subsystems/subprocess.md:11` |
| `DH-LIB-WHICH-R2` | The result distinguishes "not found" from other failures with a stable error, and a missing or unreadable `PATH` directory is skipped, not fatal | `index.ts:183-198`; `subprocess.md:251` |
| `DH-LIB-WHICH-R3` | The lookup never returns a file in the current directory for a bare name, and reports whether the result is a batch file so that the caller can apply the batch rule | Inference from `DH-LIB-NODEPROC-R2` and `R4` |

## 5. Go choice

**Decision.** Own code, about forty lines in the process package: split `PATH` with
`filepath.SplitList`, try each `PATHEXT` extension on Windows, accept a regular file that
is executable (the execute permission on POSIX, the extension on Windows).

**Why.** `exec.LookPath` is nearly this, but it always reads the **harness's** `PATH`
and `PATHEXT` from the process environment (`GOROOT/src/os/exec/lp_windows.go:114`).
The harness scrubs and overrides the child's environment, so the two can differ, and a
lookup against the wrong one either fails to find a program the child could run or
finds one it could not. `LookPath`'s refusal to use the current directory (`exec.ErrDot`,
`lp_windows.go:154-195`) is kept as a rule of the own function.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Own lookup taking an explicit environment** (chosen) | Correct for a scrubbed or overridden `PATH`; tells the caller about batch files | A small piece of code to keep in step with platform rules | Go 1.27.4 source as cited |
| `exec.LookPath` | Standard, maintained | Reads the harness's own environment; cannot be given another | as above |
| Temporarily set the process environment around `LookPath` | No own code | A data race in a concurrent host; rejected | n/a |

**Tradeoff.** The harness repeats a little of what the standard library does.

**Verdict on upstream.** *Keep* `resolveExecutable`'s behaviour; *drop* the separate npm
library, which only the embedded code needed.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-WHICH-1` | local, all systems | `DH-LIB-WHICH-R1`, `R2`, `R3` | With a temporary directory on an explicit `PATH` that the test process does not have: a program there is found; one only on the test's own `PATH` is not; a non-executable file is skipped; a missing directory is skipped; a file in the current directory is not returned for a bare name; on Windows `tool` finds `tool.cmd` and reports it as a batch file |

Nothing has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-which`
