# execa: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The shared mechanism (the harness process package, the Windows
> command-line rule and process-tree ownership) is specified in
> [node-process-builtins.md](node-process-builtins.md); this file records what execa adds
> and where each of its behaviours goes.

| | |
|---|---|
| **Library ID** | `DH-LIB-EXECA` |
| **Upstream unit** | `UP-DEP-execa` |
| **Upstream version** | 10.0.1 (requires Node 22 or later, `pnpm-lock.yaml:25332-25334`) |
| **Kind** | runtime in four product packages and one test-support package; test-only in about 55 test files |
| **Used by** | boot (`plugin-manager`, `plugin-market`), experimental (`forge-github`, `forge-gitlab`), test-support (`loader-smoke`); tests in apps, scripts and many packages |
| **Go choice** | standard library: a "run and capture" helper on the harness process package, which wraps `os/exec` |
| **Related areas** | plugin management, forge integrations, subprocess |

## 1. What it is and why upstream needs it

execa is a convenience layer over Node's `child_process`: one call runs a program, waits
for it and returns an object holding the exit code, the captured output and flags that
say what went wrong (timed out, cancelled, too much output, could not start). It also
hides Windows differences in finding and starting a program.

Upstream's process rules live in its own subprocess service, which execa does not use.
execa is what the packages **outside** that service reach for when they want to run
`git`, `pnpm`, `gh` or `glab` once and read the answer, and what tests use to start the
built product. That split is itself a debt, recorded as `UP-DEBT-found.L5.5` in
[node-process-builtins.md](node-process-builtins.md).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/boot/plugin-manager/src/operations.ts:192-198`, `:652-653`, `:681-692` | Short `pnpm` queries with captured output | `execa(cmd, args, { cwd, env, extendEnv: false, reject: false, stdin: 'ignore', timeout, cancelSignal })`; result `exitCode`, `stdout`, `stderr`, `timedOut` |
| `packages/boot/plugin-manager/src/operations.ts:386-393`, `:552-555` | Long `pnpm install` runs: streamed, not buffered; a service run must take its lifecycle scripts down with it | `stdout`/`stderr`: `'pipe'` or `'inherit'`, `buffer: false`, `killDescendants`, `cancelSignal`; the live child's `pid` and `nodeChildProcess` `exit` event |
| `packages/boot/plugin-manager/src/git-transport.ts:45-52` | Probe a Git remote; only the exit code matters | all three streams `'ignore'`, `timeout`, `killDescendants: true`, `killSignal: 'SIGKILL'` |
| `packages/boot/plugin-manager/src/github-connection.ts:74-82` | Run a command with its error stream appended to a log file | `stderr: { file, append: true }`, `buffer: false` |
| `packages/boot/plugin-market/src/repository-cache.ts:483-497` | `git` reads whose output is file content | `encoding: 'buffer'`, `stripFinalNewline: false`, `maxBuffer: { stdout, stderr }`; result `failed`, `isMaxBuffer`, `shortMessage` |
| `packages/experimental/forge-github/src/gh.ts:54-69`; `packages/experimental/forge-gitlab/src/glab.ts:68-89` | Run `gh` or `glab` with a fixed environment and optional standard input | `input`, `maxBuffer`, `timeout`, `cancelSignal`; result `isCanceled`, `timedOut`, `isMaxBuffer`, and `exitCode === undefined` meaning "never started" |
| `packages/test-support/loader-smoke/src/index.ts:238-246` | Start an example program and require a given exit code within 30 seconds | `input: ''`, `timeout`, `killSignal: 'SIGKILL'`, `reject: false`, `stripFinalNewline: false` |
| about 55 files named `*.spec.ts` or `*.e2e.ts` | Start the built command-line program, workers and scripts under test | the same subset |

Behaviours relied on, and where each goes in Go:

| Behaviour | Go mapping |
|---|---|
| A bare command name is found on `PATH`, on Windows including `PATHEXT` | Process package lookup (`DH-LIB-NODEPROC-R2`), implemented as in [which.md](which.md) |
| On Windows `pnpm` is a batch file (`pnpm.cmd`); starting it by bare name works | **Not automatic in Go, and not safe by default.** `os/exec` quotes for ordinary programs, which is wrong for `cmd.exe`. The process package refuses batch files unless the caller opts in, and then refuses arguments it cannot pass unchanged (`DH-LIB-NODEPROC-R4`, [DH-ADR-NODEPROC-1](node-process-builtins.md#dh-adr-nodeproc-1)). Inference: execa does something equivalent internally; version 10's source is not in the checkout, so this could not be read |
| `extendEnv: false` with an explicit `env` built from `scrubbedParentEnv()` | The process package always takes a complete environment (`DH-LIB-NODEPROC-R5`); in `os/exec` a non-nil `Cmd.Env` already replaces, never merges |
| `reject: false`: a failed run returns a result instead of throwing | Go returns a result and an `error`; the helper never treats a non-zero exit as an error (`DH-LIB-EXECA-R1`) |
| `timeout` with `killSignal: 'SIGKILL'`; `cancelSignal` | `context.WithTimeout` or the caller's context, with a zero grace period, so the tree is killed at once (`DH-LIB-EXECA-R5`) |
| `timedOut`, `isCanceled` | `context.Cause(ctx)` after the run: the caller owns the context and therefore the cause, as in upstream's own service |
| `killDescendants: true` | Every run has a tree owner; there is no run without one ([DH-ADR-NODEPROC-2](node-process-builtins.md#dh-adr-nodeproc-2)) |
| `maxBuffer` and `isMaxBuffer` | A capture limit that stops the run and reports "limit exceeded" (`DH-LIB-EXECA-R3`). This differs from the service's "collect" mode, which keeps the tail and carries on |
| `encoding: 'buffer'`, `stripFinalNewline: false`; elsewhere the default, which strips one final newline | Captured output is always the exact bytes; a caller that wants the newline removed removes it (`DH-LIB-EXECA-R2`) |
| `input: string`; `stdin: 'ignore' \| 'inherit'` | The process package's input modes |
| `stderr: { file, append: true }` | An `*os.File` opened for append is assigned as the stream; the child writes to it directly (`DH-LIB-EXECA-R4`) |
| `buffer: false` with `'pipe'` | The piped stream is handed to the caller unbuffered |
| `exitCode === undefined` and `shortMessage` when the program never started | A distinct "not started" result carrying the lookup or start error |

## 3. Tech debt upstream carries

Search performed: every import of `execa`; `patches/` and `pnpm-workspace.yaml` (no patch,
pin or exemption). No `UP-DEBT` unit is assigned to execa. The debt it takes part in,
several process-starting stacks with different guarantees, is `UP-DEBT-found.L5.5` in
[node-process-builtins.md](node-process-builtins.md).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-EXECA-R1` | One call runs a program to completion and returns the exit code and the captured output. "Never started", "exited non-zero", "killed by the deadline", "killed by cancellation" and "output limit exceeded" are five distinguishable outcomes, none of them a panic | `gh.ts:63-69`; `glab.ts:83-89`; `repository-cache.ts:493-497` |
| `DH-LIB-EXECA-R2` | Captured output is the exact bytes the program wrote | `repository-cache.ts:484-485`; `glab.ts:80-81` |
| `DH-LIB-EXECA-R3` | A capture limit per stream. Exceeding it stops the whole tree and is reported; the caller is never given a silently shortened result | `gh.ts:61`, `:66`; `repository-cache.ts:486`, `:495` |
| `DH-LIB-EXECA-R4` | A stream can be appended to a file without passing through harness memory | `github-connection.ts:81` |
| `DH-LIB-EXECA-R5` | A deadline or a cancellation kills the whole tree immediately, with no grace period, when the caller asks for that | `git-transport.ts:52`; `repository-cache.ts:487-488` |

## 5. Go choice

**Decision.** No module. A helper `Run(ctx, spec) (Result, error)` in the harness process
package, built on the same `Start` every other caller uses.

**Why.** Everything execa provides here is either already in `os/exec` (explicit
environment, context cancellation, byte output) or is a rule the process package must
own for safety (lookup, Windows command lines, tree ownership). A second library with
its own defaults is how upstream ended up with `UP-DEBT-found.L5.5`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`Run` helper on the process package** (chosen) | One set of rules for product code; about a screen of code | Callers lose execa's long list of options, of which upstream uses the dozen above | `os/exec` in Go 1.27.4: `CommandContext`, `Cmd.Env`, `Cmd.Cancel`, `Cmd.WaitDelay` (`GOROOT/src/os/exec/exec.go`) |
| Call `os/exec` directly at each site | Nothing to write | Recreates the debt: each site would choose its own lookup, quoting and kill behaviour, and on Windows would mis-quote batch files | n/a |

**Tradeoff.** Tests may still call `os/exec` directly; they start trusted programs with
fixed arguments. Product code may not (`DH-LIB-NODEPROC-R14`).

## 8. Verification

Local tests against real child processes. Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-EXECA-1` | local, all systems | `DH-LIB-EXECA-R1`, `R5` | A helper program is made to exit 0, exit 3, sleep past a deadline, sleep until cancelled, and be absent; each gives its own outcome, and after the deadline case a grandchild it started is gone |
| `DH-T-EXECA-2` | local, all systems | `DH-LIB-EXECA-R2`, `R3` | Output with and without a final newline, and binary output, come back byte for byte; output one byte over the limit stops the run and reports the limit |
| `DH-T-EXECA-3` | local, all systems | `DH-LIB-EXECA-R4` | Two runs append their error stream to the same file; the file holds both, in order |

## 9. Units dispositioned by this spec

- `UP-DEP-execa`
