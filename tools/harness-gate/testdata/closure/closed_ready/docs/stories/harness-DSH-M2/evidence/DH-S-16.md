# DH-S-16 evidence (fixture: closed_ready)

Closure status: closed

Evidence record of [impl.md DH-S-16](../../../harness/impl.md#dh-s-16) (Profiles, launchers and evidence runners) for story
`harness:DSH-M2`, in the impl.md sec. 7.3 form of the M1 records
([../../harness-DSH-M1/evidence/README.md](../../harness-DSH-M1/evidence/README.md)). Plan:
[implementation-plan.md](../implementation-plan.md) Phase 6. Decisions of
[../../harness-DSH-M1/decisions.md](../../harness-DSH-M1/decisions.md) stay in force.

**Kind.** Build, P0. Paired acceptance stage: DH-S-17.

**How this record closes** (C57; M2-C-2; [the gate's README](../../../../tools/harness-gate/README.md#stage-closure)):

1. While the line above reads `open`, fill the tables below until `harness-gate ready --stage DH-S-16 --built`
   exits 0. `ready` does not read the status line.
2. Only then does the closing PR change the line to `Closure status: closed`.
3. That PR's gate runs `harness-gate close --stage DH-S-16 --built`: every `ready` condition again, and the
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
| 16.1 | DH-D-BUNDLE-3 | 0a1b2c3 |  |
| 16.2 | DH-D-CLI-9 | 0a1b2c3 |  |
| 16.3 | DH-D-CLI-2 | 0a1b2c3 |  |
| 16.4 | DH-D-CLI-4 | 0a1b2c3 |  |
| 16.5 | DH-D-CLI-5 | 0a1b2c3 |  |
| 16.6 | DH-D-SDK-8 | 0a1b2c3 | P1, pulled forward |
| 16.7 | DH-D-UTIL-8, DH-D-UTIL-7 | 0a1b2c3 | P1, pulled forward; UTIL-7 held open: H-10 |
| 16.8 | guarded scenarios | 0a1b2c3 | the Scenarios table below |
| 16.9 | DH-D-VIEW-4 | 0a1b2c3 | P1, pulled forward |
| 16.10 | DH-D-BOOT-6 | 0a1b2c3 | closes H-21 |

## Scenarios

The four guarded scenarios of step 16.8, on scratch homes (M2-EN-6); this stage does not close until they pass. DH-E-STORE-2 runs on Linux only (Claude route plus `bwrap`, tech-spec sec. 4.2). Authored here, run later: DH-E-CLI-1, DH-E-CLI-2 (step 27.8), DH-E-BUNDLE-1/-2/-3/-4.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DH-E-STORE-1 | Linux (WSL) | guarded | required | scratch home | 0a1b2c3 | passed | run-dh-e-store-1.txt |
| DH-E-STORE-1 | Windows | guarded | required | scratch home | 0a1b2c3 | passed | run-dh-e-store-1.txt |
| DH-E-STORE-1 | macOS | guarded | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-STORE-2 | Linux (WSL) | guarded | required | scratch home | 0a1b2c3 | passed | run-dh-e-store-2.txt |
| DH-E-STORE-6 | Linux (WSL) | guarded | required | scratch home | 0a1b2c3 | passed | run-dh-e-store-6.txt |
| DH-E-STORE-6 | Windows | guarded | required | scratch home | 0a1b2c3 | passed | run-dh-e-store-6.txt |
| DH-E-STORE-6 | macOS | guarded | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-FS-3 | Linux (WSL) | guarded | required | scratch home | 0a1b2c3 | passed | run-dh-e-fs-3.txt |
| DH-E-FS-3 | Windows | guarded | required | scratch home | 0a1b2c3 | passed | run-dh-e-fs-3.txt |
| DH-E-FS-3 | macOS | guarded | required |  |  | out of scope (platforms-windows-wsl) |  |

## Manual checks register

The manual checks impl.md DH-S-16 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 16.3 | one `dsh headless` prompt on the signed-in Claude Code route (M2-I-1) with an approval answered on the terminal | seen as described | operator | 0a1b2c3 | `manual` |
| 16.4 | Ctrl-C during a tool call | seen as described | operator | 0a1b2c3 | `manual` |
| 16.5 | a second `dsh` beside a running host | seen as described | operator | 0a1b2c3 | `manual` |

## Owner defaults (M2-C-4)

The defaults of [impl.md sec. 9.1](../../../harness/impl.md#91-defaults-the-owner-has-not-confirmed) whose
"Confirm before" is DH-S-16. The owner confirms each, or changes the document that holds it, before this
stage closes; `--built` refuses a row with no confirmation or no name in By.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |
| Second `dsh web` reuses the host | I3 Q1 | A second `dsh web` reuses the host on the same address | confirmed as is | owner | 0a1b2c3 |
| Windows double-click | I3 Q8 | Nothing special on a double-click on Windows | confirmed as is | owner | 0a1b2c3 |
| `dsh headless` progress | U-K2-4 | `dsh headless` progress | confirmed as is | owner | 0a1b2c3 |

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Not yet recorded. At closure: the commit of the three modules (one commit), the Go version (`go1.27.4`),
the binary's SHA-256 and embedded version.

### Environment and fixture

Not yet recorded. Gate venue: both local lanes, `tools/harness-gate/lanes/win.ps1` (Windows native) and
`lanes/wsl.sh` (Linux under WSL); macOS out of scope (`platforms-windows-wsl`).

### Exact tests and commands

Not yet recorded: the gate runner's output, the `go test` command lines and `DSH_REQUIRE_SCENARIOS`
(`harness-gate require --stage DH-S-16`).

### Run identifiers

Not yet recorded: CI run and job ids, or the local run-record id and its SHA-256 (M1 runbook template);
session ids and provider request ids for model calls.

### Results

The Scenarios table above, and the local test results per step at closure. Not yet recorded.

### Manual checks

The Manual checks register above.

### Cleanup evidence

Not yet recorded. Step 16.8: the scratch homes and the edited file compared before and after; the deleted
sessions' rows, blobs and spill files shown gone; the log read with `seq` 0 to n and no gap.

### Open items

Held open: H-10 (DH-D-UTIL-7); H-1 to H-4 from DH-S-15; H-21 closes with step 16.10. Built-not-done: BUNDLE-3 until DH-S-22; CLI-2, CLI-4, CLI-5 until DH-S-27; UTIL-8 until DH-S-39. Exit evidence also needs DH-T-TOOLCHAIN-1, -3, -5 and -6 passing and the step 16.8 cleanup evidence.
