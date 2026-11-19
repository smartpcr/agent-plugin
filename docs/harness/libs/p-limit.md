# p-limit: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-PLIMIT` |
| **Upstream unit** | `UP-DEP-p-limit` |
| **Upstream version** | 7.3.3 |
| **Kind** | runtime, three packages |
| **Used by** | session (`session-persistence-jsonl`, `session-persistence-sqlite`), experimental (`forge-gitlab`) |
| **Go choice** | named Go module `golang.org/x/sync` (`errgroup`) |
| **Related areas** | forge (experimental); any area that fans out work |

## 1. What it is and why upstream needs it

When a program starts many tasks at once, for example one network request per item of a
list, it can overwhelm the thing it is calling or its own file-descriptor limit. A
**concurrency limit** lets at most N of them run at a time and makes the rest wait.
p-limit is a JavaScript helper that does exactly that for promises.

Upstream uses it in two situations: listing stored sessions, which opens one file per
session, and reading from the GitLab API, which rate-limits its callers.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/session/session-persistence-jsonl/src/index.ts:378`, `:1184-1187`, `:1206`, `:1235` | One limiter per store (3 by default, `:104`) shared by every listing read: project directories, session directories, header reads. Every task is allowed to settle and failures are collected per item (`:264`). | `pLimit(n)`, `limit(fn)` |
| `packages/session/session-persistence-sqlite/src/persistence.ts:37`, `:184` | The same limiter for directory reads of the segment files | `pLimit(n)`, `limit(fn)` |
| `packages/experimental/forge-gitlab/src/pool.ts:12-17`, called at `src/provider.ts:123` and `src/review.ts:294` | Run a task for every item, at most `maxConcurrentRequests` at a time; wait until all have settled so none outlives a failure; then fail with the first failure | `pLimit(n)`, `Promise.allSettled` |

## 3. Tech debt upstream carries

Search performed: every import of `p-limit`; `patches/` and `pnpm-workspace.yaml`
(nothing); the consuming READMEs. None found.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-PLIMIT-R1` | Run a task for each item of a list with at most N running at once. | all sites |
| `DH-LIB-PLIMIT-R2` | The call returns only after every started task has returned. No task is left running in the background. | `forge-gitlab/src/pool.ts:10` |
| `DH-LIB-PLIMIT-R3` | The caller chooses between two failure rules: stop starting new work and cancel the rest on the first failure, or let every task finish and report each item's own result. | `pool.ts:14-16` against `session-persistence-jsonl/src/index.ts:264` |
| `DH-LIB-PLIMIT-R4` | Cancelling the caller's context stops tasks that are waiting for a slot. | `session-persistence-jsonl/src/index.ts:331` ("queued tasks of a cancelled listing run and reject at their signal check") |

## 5. Go choice

**Decision.** `golang.org/x/sync/errgroup`: a `Group` with `SetLimit(n)`, created with
`errgroup.WithContext`.

**Why.** It is the standard Go idiom and maps onto each requirement: `SetLimit` bounds
the number of running goroutines, `Go` blocks while the group is full, `Wait` returns
after all have finished, and the group's context is cancelled on the first error.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `golang.org/x/sync/errgroup` | One well-known construct; first-error and cancellation built in | A module, although one maintained by the Go team | `golang.org/x/sync` v0.25.0, 2026-10-15, BSD 3-Clause, no requirements, no cgo. `errgroup.go` exports `WithContext`, `SetLimit`, `TryGo`. Checked with `go list -m -json`, `go mod download -json`, `LICENSE` and source in the module cache, 2026-11-18. |
| Standard library only: a buffered channel as a semaphore plus `sync.WaitGroup` | No dependency | Every call site re-implements first-error capture and cancellation, which is where such code goes wrong | `sync.WaitGroup.Go` exists in Go 1.27.4 (`sync/waitgroup.go:236`) |

**Tradeoff.** None of substance; the module is small and is likely to be needed by
other parts of the harness.

How the two failure rules are written:

- **Stop on first failure** (the GitLab reads). `g, ctx := errgroup.WithContext(parent)`;
  each task takes `ctx`; `g.Wait()` returns the first error after all tasks have
  returned. This is stricter than upstream, which lets the remaining requests run to
  completion before reporting; in Go they are cancelled.
- **Let everything finish** (per-item results). Tasks never return an error to the
  group; each writes its own result or error into its slot of a pre-sized slice.

Two traps to avoid. `Go` blocks the calling goroutine when the group is full, so the
loop that submits work must itself select on the context if items come from a channel.
A limit belongs to the resource, not the call: when several operations must share one
budget, as upstream's listing limiter does, they share one semaphore
(`golang.org/x/sync/semaphore` or a buffered channel) owned by the service that owns the
resource, and it lives as long as that service's fiber.

What remains of the upstream uses: the two session-store limiters disappear, because
listing sessions becomes one indexed database query
([DH-ADR-PERSISTENCE-3](../upstream/session-and-persistence.md#dh-adr-persistence-3)).
The GitLab pool remains if the experimental area keeps the forge features. The idiom is
recorded here mainly for new Go code that fans out: parallel tool calls, file search
workers, model catalog refreshes.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-PLIMIT-1` | local | `DH-LIB-PLIMIT-R1`, `R2` | A helper `Each(ctx, items, n, task)` run with a counter of tasks in flight never exceeds `n`, and returns only after the last task has returned, under the race detector. |
| `DH-T-PLIMIT-2` | local | `DH-LIB-PLIMIT-R3`, `R4` | In stop-on-first-failure mode, a failing task cancels the context seen by the others and no new task starts. In finish-all mode every item has a result. Cancelling the parent context while tasks wait for a slot returns without starting them. |

Local tests only. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-p-limit`.
