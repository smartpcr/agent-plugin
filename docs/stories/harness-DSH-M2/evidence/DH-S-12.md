# DH-S-12 evidence

Closure status: open

Evidence record of [impl.md DH-S-12](../../../harness/impl.md#dh-s-12) (Working directory, filesystem seam, policy resolver and file tools) for story
`harness:DSH-M2`, in the impl.md sec. 7.3 form of the M1 records
([../../harness-DSH-M1/evidence/README.md](../../harness-DSH-M1/evidence/README.md)). Plan:
[implementation-plan.md](../implementation-plan.md) Phase 2. Decisions of
[../../harness-DSH-M1/decisions.md](../../harness-DSH-M1/decisions.md) stay in force.

**Kind.** Build, P0. Paired acceptance stage: DH-S-18.

**How this record closes** (C57; M2-C-2; [the gate's README](../../../../tools/harness-gate/README.md#stage-closure)):

1. While the line above reads `open`, fill the tables below until `harness-gate ready --stage DH-S-12 --built`
   exits 0. `ready` does not read the status line.
2. Only then does the closing PR change the line to `Closure status: closed`.
3. That PR's gate runs `harness-gate close --stage DH-S-12 --built`: every `ready` condition again, and the
   line reading `closed`. A `closed` line on a record that is not ready is refused `closed-but-not-ready`.

No M2 scenario run and no M2 stage closure happens before M1 is closed (M2-DEC-1; `harness-gate entry`,
implementation-plan Stage 1.2). The four tables below are read by the gate: keep their headings and columns.
A result is `passed`, `failed`, `not run (<reason>)`, `skipped`, `blocked` or `out of scope (<decision>)`;
skipped, blocked, not run and an empty cell count as open (impl.md sec. 7.3, M2-DEC-3), and only a
variant out of scope by a named decision is not a blocker (M2-C-2). A `recorded` leg must run; its failure
is kept and does not block (M2-DEC-4). A commit is a SHA of 7 to 40 lower-case hex digits.

## Steps

Each step's merge commit; `--built` refuses a step without one.

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 12.1 | DH-D-VIEW-10 | | cycle C-3, with 12.2 and 12.3 at one commit |
| 12.2 | DH-D-FS-4 | | cycle C-3 |
| 12.3 | DH-D-SANDBOX-1 | | cycle C-3 |
| 12.4 | DH-D-HUMAN-4 | | P1, pulled forward |
| 12.5 | DH-D-FS-6 | | |

## Scenarios

None executed here. Authored here: DH-E-FS-1, DH-E-FS-2, DH-E-VIEW-5 (run in DH-S-18), DH-E-SANDBOX-7 (run in DH-S-17) and the guarded DH-E-FS-3 (run in DH-S-16, step 16.8).

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |

## Manual checks register

The manual checks impl.md DH-S-12 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 12.2 | the write tool asked for a path outside the roots through a symbolic link: the denial (scratch directory) | | | | `manual` |
| 12.2 | a directory swapped for a link during a write: the fence held (scratch directory) | | | | `manual` |

## Owner defaults (M2-C-4)

The defaults of [impl.md sec. 9.1](../../../harness/impl.md#91-defaults-the-owner-has-not-confirmed) whose
"Confirm before" is DH-S-12. The owner confirms each, or changes the document that holds it, before this
stage closes; `--built` refuses a row with no confirmation or no name in By.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |
| Full-access label | I2 Q3 | In full-access mode a command may take over the harness, and the label says so | | | |
| UTF-8-only text tools | core §6.3 | Text tools refuse every encoding but UTF-8 | | | |

## M2 entry (implementation-plan Stage 1.2)

The entry state at the first M2 commit, as `harness-gate entry --story harness:DSH-M2` reports it
([the gate's README](../../../../tools/harness-gate/README.md#story-entry-harness-gate-entry)).

| Item | Value |
| --- | --- |
| First M2 commit | `8ac8dc6` ([plan-architecture] #96, the first commit of story harness:DSH-M2 on `main`); the entry check itself lands on top of `c1457ed`, where M1 is in the same state |
| M1 state at that commit | **open**: all eleven records `DH-S-1.md` .. `DH-S-11.md` read `Closure status: open`, no M1 closure record `docs/stories/harness-DSH-M1/evidence/closure.md`, `built-through: DH-S-8` |
| `harness-gate entry` at `c1457ed` | exit 1, 13 unmet conditions (the eleven records, the closure record, `built-through`) |
| Q `m2-entry-m1-closure` (M2-Q-2) in force | Unanswered; the default holds (tech-spec M2-DEC-1): S12..S16 may build on the built M1 contracts; no M2 scenario run (Stage 6.12 onward) and no M2 stage closure happens before `harness-gate entry` exits 0 |

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Not yet recorded. At closure: the commit of the three modules (one commit), the Go version (`go1.27.4`),
the binary's SHA-256 and embedded version.

### Environment and fixture

Not yet recorded. Gate venue: both local lanes, `tools/harness-gate/lanes/win.ps1` (Windows native) and
`lanes/wsl.sh` (Linux under WSL); macOS out of scope (`platforms-windows-wsl`).

### Exact tests and commands

Not yet recorded: the gate runner's output, the `go test` command lines and `DSH_REQUIRE_SCENARIOS`
(`harness-gate require --stage DH-S-12`).

### Run identifiers

Not yet recorded: CI run and job ids, or the local run-record id and its SHA-256 (M1 runbook template);
session ids and provider request ids for model calls.

### Results

The Scenarios table above, and the local test results per step at closure. Not yet recorded.

### Manual checks

The Manual checks register above.

### Cleanup evidence

Not yet recorded: the before-and-after comparison the stage names, or what was retained.

### Open items

Debt-audit item `UP-DEBT-found.Q3.2` (a real model giving up before its first tool call) closes when DH-E-SANDBOX-7 passes in DH-S-17. DH-D-FS-4 stays built until DH-S-28 (DH-E-FS-5).
