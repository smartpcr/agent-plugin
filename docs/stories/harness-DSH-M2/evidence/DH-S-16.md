# DH-S-16 evidence

Closure status: open

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
| 16.1 | DH-D-BUNDLE-3 | 87abad2 | squash-merge of #177 (Stage 6.1, Mode Layers and Five Profiles) |
| 16.2 | DH-D-CLI-9 | cc1d291 | squash-merge of #179 (Stage 6.2, Admin Tree and Secret Set) |
| 16.3 | DH-D-CLI-2 | 85cfd7c | squash-merge of #185 (Stage 6.4, Headless and SDK Launchers) |
| 16.4 | DH-D-CLI-4 | 03c59a0 | squash-merge of #188 (Stage 6.5, One Bounded Stop Path) |
| 16.5 | DH-D-CLI-5 | c331bf8 | squash-merge of #191 (Stage 6.6, Second Launch and Crash Notice) |
| 16.6 | DH-D-SDK-8 | 3527b3c | P1, pulled forward; squash-merge of #187 (Stage 6.7, Go Client Package) |
| 16.7 | DH-D-UTIL-8, DH-D-UTIL-7 | 5537e0a | P1, pulled forward; UTIL-7 held open: H-10; squash-merge of #193 (Stage 6.8, Scenario Helper and Keyless Replay) |
| 16.8 | guarded scenarios | c35a838 | the Scenarios table below; squash-merge of #195 (Stage 6.12, the guarded runner); the runs are owed (`m1-entry-block`) |
| 16.9 | DH-D-VIEW-4 | cbfbd01 | P1, pulled forward; squash-merge of #196 (Stage 6.9, Session Titles) |
| 16.10 | DH-D-BOOT-6 | fe13e4e | closes H-21; squash-merge of #183 (Stage 6.3, Config Show and Validate Close H-21) |

## Scenarios

The four guarded scenarios of step 16.8, on scratch homes (M2-EN-6); this stage does not close until they pass. DH-E-STORE-2 runs on Linux only (Claude route plus `bwrap`, tech-spec sec. 4.2). Authored here, run later: DH-E-CLI-1, DH-E-CLI-2 (step 27.8), DH-E-BUNDLE-1/-2/-3/-4.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DH-E-STORE-1 | Linux (WSL) | guarded | required | scratch home | | not run (M1 not closed, M2-DEC-1; recorded by a later workstream) | |
| DH-E-STORE-1 | Windows | guarded | required | scratch home | | not run (M1 not closed, M2-DEC-1; recorded by a later workstream) | |
| DH-E-STORE-1 | macOS | guarded | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-STORE-2 | Linux (WSL) | guarded | required | scratch home | | not run (M1 not closed, M2-DEC-1; recorded by a later workstream) | |
| DH-E-STORE-6 | Linux (WSL) | guarded | required | scratch home | | not run (M1 not closed, M2-DEC-1; recorded by a later workstream) | |
| DH-E-STORE-6 | Windows | guarded | required | scratch home | | not run (M1 not closed, M2-DEC-1; recorded by a later workstream) | |
| DH-E-STORE-6 | macOS | guarded | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-FS-3 | Linux (WSL) | guarded | required | scratch home | | not run (M1 not closed, M2-DEC-1; recorded by a later workstream) | |
| DH-E-FS-3 | Windows | guarded | required | scratch home | | not run (M1 not closed, M2-DEC-1; recorded by a later workstream) | |
| DH-E-FS-3 | macOS | guarded | required | | | out of scope (platforms-windows-wsl) | |

## Operator decisions for Stage 6.12

Answers the operator gave in the Forge open-question wizard for workstream
`ws-harness-dsh-m2-phase-dh-s-16-profiles-launchers-and-evidence-runners-stage-guarded-scenarios-on-scratch-homes`
(after iteration 2), quoted verbatim. They narrow that workstream to building the runner; they do not
close a row, waive a run or change M2-DEC-1.

| Question id | Question | Answer (verbatim) | Effect on this record |
| --- | --- | --- | --- |
| `m1-entry-block` | `harness-gate entry` fails (harness:DSH-M1 not closed), so no guarded run can start. How should this workstream proceed? | "Merge the runner now; record runs in a later workstream" | Step 16.8's runner merges; the seven required rows above stay `not run` and remain owed. A later workstream runs both guarded lanes on a merged commit after `harness-gate entry` exits 0 and fills the rows, the run records and the cleanup evidence below. Stage 6.12's run and cleanup-evidence steps stay unticked until then. |
| `store-command-templates` | Who provides the `DSH_SCENARIO_*` command templates for DH-E-STORE-1/-6/-2 (create/resume/fork/delete/attach image), which no current dsh CLI or SDK method supports? | "Add a follow-up workstream for a scenario driver" | The guarded inputs file is written by that workstream; `harness-gate guarded` refuses to run while any input is missing. |
| `requires-executing-stage` | May a guarded run include a scenario whose index `requires` is the stage it is closing (DH-E-FS-3 requires DH-S-16)? | "Yes, the executing stage only" | `guarded.RequiresBuilt` admits the `--stage` being closed and no other unbuilt stage. |
| `store2-cleanup-coverage` | DH-E-STORE-2's closing deletion and spill-file cleanup are not checked by any test. Should this workstream add them? | "Add them in this workstream next iteration" | Done: STORE-2 step (5) and STORE-6's spill checks. |

## Manual checks register

The manual checks impl.md DH-S-16 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 16.3 | one `dsh headless` prompt on the signed-in Claude Code route (M2-I-1) with an approval answered on the terminal | | | | `manual` |
| 16.4 | Ctrl-C during a tool call | | | | `manual` |
| 16.5 | a second `dsh` beside a running host | Seen on Windows 11, release build of `./cmd/dsh` (sha256 `ae03f032...46364`) on an empty scratch home: with a first `dsh web` live (pid 34596, `http://127.0.0.1:3080`), `dsh status` listed that one host; a second `dsh web` printed `opened in the browser of the host (process 34596)` and exited 0 in 160 ms, starting no host of its own. After the first host was killed, `dsh status` named the crash report `reports/report-...-34596.txt` and suggested `dsh --safe web`, then exited 4 `host/not-running`; `dsh crash` printed the same path. Seen on WSL2 Ubuntu 24.04, a release linux/amd64 build at `0420164`, a pre-merge branch commit equal to `12c2be8` plus docs (same `src/harness` tree `aeb078f9`; see Source and build identity; sha256 `cb2489e7...71f54`) on an ext4 scratch home: with a first `dsh web` live (pid 119241), `ss` showed one listener (`127.0.0.1:3080`, pid 119241) and `dsh status` listed one host. With no browser and a host started without a terminal, a second `dsh web` returned `launch/no-channel`, exiting 4 in 18 ms and starting no host. After `kill -9`, `dsh status` named the crash report and `--safe`, then exited 4; `dsh crash` printed the same path | run by the Forge engineer agent; accepted by the operator (answer to question `dh-s-16-manual-checks`: "Accept both") | 12c2be8 | `manual` |

Logs: [runs/manual-12c2be8/](runs/manual-12c2be8/README.md). The operator accepted the 16.5 run and
the scratch `dsh secret set` run below (answer to `dh-s-16-manual-checks`: "Accept both").

- **16.3, separate half (M2-I-1).** `dsh secret set` was run with a scratch value at `12c2be8`: the dotted
  name `llm.deepseek.api-key` was refused with usage status 64 and nothing was stored;
  `secret://scratch/check` was stored at revision 1, and `SCRATCH_CHECK --json` updated the same secret
  to revision 2 (`source: store`). The value appears in no file of the home in plain text
  ([log](runs/manual-12c2be8/check-16.3-secret-set-windows.log)). Accepted by the operator with 16.5.
- **16.3 headless prompt and 16.4 Ctrl-C: not seen, blocked.** The release build has no model route in its
  catalog. No catalog item wraps `llm/provider/claudecode`, and the only route a built binary can insert
  is `llm.testkit-route`, behind the `testhook` build tag. With no route, `dsh headless` cannot reach the
  signed-in Claude Code route or make a tool call, so neither check can be seen. The operator decided
  (answer to `dh-s-16-claude-route`: "Pause until a stage adds the Claude Code route to the release
  build") that both rows stay empty until a later stage puts that route in the release catalog. They are
  recorded then, on that build. Until then this record cannot pass `ready --built`.

## Owner defaults (M2-C-4)

The defaults of [impl.md sec. 9.1](../../../harness/impl.md#91-defaults-the-owner-has-not-confirmed) whose
"Confirm before" is DH-S-16. The owner confirms each, or changes the document that holds it, before this
stage closes; `--built` refuses a row with no confirmation or no name in By.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |
| Second `dsh web` reuses the host | I3 Q1 | A second `dsh web` reuses the host on the same address | Yes, as written | operator (answer `dh-s-16-owner-defaults`: "Confirm all three") | 12c2be8 |
| Windows double-click | I3 Q8 | Nothing special on a double-click on Windows | Yes, as written | operator (answer `dh-s-16-owner-defaults`: "Confirm all three") | 12c2be8 |
| `dsh headless` progress | U-K2-4 | `dsh headless` progress | Yes, as written | operator (answer `dh-s-16-owner-defaults`: "Confirm all three") | 12c2be8 |

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Manual checks and package tests at `12c2be8` (main, every step above merged). Go `go1.27.4`
windows/amd64. The binary is `go -C src/harness build ./cmd/dsh` (release, no tags), embedded version
`dsh (devel) (12c2be8d83b487422ce7115951361b8a1fb367d1; go1.27.4; windows/amd64)`, SHA-256
`ae03f032229965bc2088a2c9d1d17489551062c384237bbd91f22c89db546364`.

The WSL leg of 16.5 used a linux/amd64 release build (same command, no tags, go1.27.4) made at
`0420164476527c9bab1eb4a27f85e3a35d79faa2`, embedded version
`dsh (devel) (0420164476527c9bab1eb4a27f85e3a35d79faa2; go1.27.4; linux/amd64)`, SHA-256
`cb2489e768815da546b5c3484e627f74da382a40cbac88baa209a55e2b971f54`
([log](runs/manual-12c2be8/check-16.5-wsl.log)). `0420164` is a pre-merge commit on this PR's branch
and is not on main; after a squash-merge it may be unreachable. Its parent chain starts at `12c2be8`
and `git diff 12c2be8 0420164` touches only `docs/`. The checkable identity is the product tree:
`12c2be8^{tree}` is `3c13d019550ad8a2c9b58bde75716552255ae4f5`, and `12c2be8:src/harness` and
`0420164:src/harness` are both tree `aeb078f9b774620f56bc96dda9b8e7aca67d3eea`, so the WSL binary was
built from the `src/harness` that `12c2be8` holds. Its embedded version names `0420164`, not `12c2be8`,
so the binary's bytes cannot be reproduced from main; the claim rests on the tree equality above.
The lane records cited below at `a91abac` (also docs-only over `12c2be8`, `src/harness` tree
`aeb078f9`) and `2da0f59` (`src/harness` tree `6cfdf5bd`, which adds the one-line gofmt fix) are
likewise pre-merge branch commits; they are failing, uncommitted records and prove no row. At closure: the commit of the three
modules (one commit), with the binary's SHA-256 and embedded version on both lanes.

### Environment and fixture

Not yet recorded. Gate venue: both local lanes, `tools/harness-gate/lanes/win.ps1` (Windows native) and
`lanes/wsl.sh` (Linux under WSL); macOS out of scope (`platforms-windows-wsl`).

### Exact tests and commands

At `12c2be8`, Windows 11, from the worktree root:

- DH-T-TOOLCHAIN-1, -3, -5 (the tests the `harness-replay` gate step runs):
  `go -C src/harness test -tags=replay -v ./test/replay/...`. `ok` (63.6 s). These pass:
  `TestLayerFilesCarryTheirTag` and `TestReplayChildGetsNoKey` (TOOLCHAIN-1),
  `TestBoundaryParsersRunTheirCommittedCrashers` (TOOLCHAIN-3),
  `TestReplayIsDeterministicAndLeavesTheFixture` (TOOLCHAIN-5), `TestReplayMatchesTheCommittedOracle`,
  `TestLaunchReplayStopsATreeHoldingItsOutput` and `TestLaunchReplayCapsItsOutput`.
- DH-T-TOOLCHAIN-6: `go -C src/harness test -v ./internal/testkit/scenario/`. `ok`, all 22 tests `PASS`,
  among them `TestRequiredScenarioWithMissingInputFails` and
  `TestCheckFailsARequiredScenarioWithoutAPassedRow`. Also
  `GOWORK=off go -C tools/harness-gate test -run TestPreflightChecksTheSignIn ./acceptance/`, which is `ok`.
  Status of TOOLCHAIN-6's three sites:

  | Site | Run? | Result | Commit |
  | --- | --- | --- | --- |
  | `src/harness` `./internal/testkit/scenario/` | yes | `ok` | `12c2be8` |
  | `tools/harness-gate` `./acceptance/` (`TestPreflightChecksTheSignIn`) | yes | `ok` | `12c2be8` |
  | gate root (`TestScenarioGatesStopWhenSignedOut`, `tools/harness-gate/scenariorun_test.go`) | **no** | not run; it runs only inside the lanes (Full harness gate policy), which have not reached the `self` gate | owed at the closing commit |

  Lane stops (pre-merge branch commits; failing records, not committed):
  at `a91abac` both lanes stopped at gate 1/14 `gofmt` on
  `src/harness/internal/testkit/scenarios/sdk_stdio_scenario_test.go` (a missing blank line inherited
  from `e3da93b`, which `2da0f59` formats), records `win-a91abac-20261127T1447Z.txt` (sha256
  `534742a5…c0b`) and `wsl-a91abac-20261127T1447Z.txt` (sha256 `31581c6d…c8d`).
  At `2da0f59` both lanes passed `gofmt` and stopped at gate 2/14 `vet` on QG-4/QG-5 analyzer findings in
  17 test files inherited from main, records `win-2da0f59-20261127T1454Z.txt` (sha256 `026b007c…146a`,
  76 findings) and `wsl-2da0f59-20261127T1454Z.txt` (sha256 `065cf279…5376`, 85 findings); see the
  [`dh-s-16-vet-inherited` decision](#decision-dh-s-16-vet-inherited).
- Manual checks: the commands in [runs/manual-12c2be8/](runs/manual-12c2be8/README.md).
- The record check, on this record with `Closure status: open`:
  `GOWORK=off go -C tools/harness-gate run . ready --stage DH-S-16 --built --root ../..`. Before this
  update it found 29 blockers. Afterwards the steps are cleared, and these remain open: the seven
  guarded rows (`m1-entry-block`), the manual checks (16.3 and 16.4 not seen, every Operator cell then pending)
  and the three owner defaults. After the operator's answers, the Operator cell of 16.5 and the three
  owner-default rows are filled, and `ready` finds 13 blockers. Still open: the seven guarded rows, and 16.3 and 16.4 (paused until
  the release build has the Claude Code route).

Not yet recorded: the lanes' output and `DSH_REQUIRE_SCENARIOS` (`harness-gate require --stage DH-S-16`).
Step 16.8 runs, once per lane on the stage's merged commit,
`tools/harness-gate/lanes/wsl.sh --guarded --inputs FILE` and `lanes/win.ps1 -Guarded -Inputs FILE`
([the gate's README](../../../../tools/harness-gate/README.md#guarded-mode---guarded--guarded-stage-612)):
`harness-gate entry`, `harness-gate require --stage DH-S-16`, then `go test -tags scenario -count=1 -p 1
-run <plan> ./internal/testkit/scenarios` on scratch homes, recorded in
`runs/guarded-<wsl|win>-<sha7>-<stamp>.txt`. Each record prints one `row:` line per scenario, copied into
the Scenarios table with the record named in Artifacts. Not yet run: `harness-gate entry` fails while
harness:DSH-M1 is not closed (M2-DEC-1); per [Operator decisions](#operator-decisions-for-stage-612)
(`m1-entry-block`) the runner merges with this stage and a later workstream runs both lanes and records
their rows, records and cleanup evidence here.

### Run identifiers

Not yet recorded: CI run and job ids, or the local run-record id and its SHA-256 (M1 runbook template);
session ids and provider request ids for model calls.

### Results

The Scenarios table above, and the local test results per step at closure. Not yet recorded.

### Manual checks

The Manual checks register above.

### Cleanup evidence

Not yet recorded. Step 16.8: the scratch homes and the edited file compared before and after; the deleted
sessions' rows, blobs and spill files shown gone; the log read with `seq` 0 to n and no gap. Each scenario
logs these observations as `evidence:` lines while its scratch home still exists, and the guarded record
copies them into its `cleanup evidence:` section (with the `scenarios` gate's listing of the scratch
directory before and after the run):

- DH-E-STORE-6: the home's files before and after the deletions; the spill files of both deleted sessions
  before (a fixture precondition); after the deletion and after the interrupted deletion's restart, each
  session gone (not listed, not tombstoned), its spill directory absent and its unshared images absent; the
  fork's log `seq` 0 to n before and after; the store check clean.
- DH-E-STORE-2 (WSL lane): the first model request sent after the resume's prompt (counted from the
  requests already sent, never the latest one) carries the outcome-unknown result; after it, the host killed and the session deleted;
  its log `seq` 0 to n; its spill files and blobs before; afterwards no session, no tombstone, no blob and no
  spill file in the home, and the home's files before and after.
- DH-E-STORE-1: the log read from the database, `seq` 0 to n with no gap, and the lease epoch.
- DH-E-FS-3: the file's SHA-256 before, after, and the last acknowledged write.

### Open items

Held open across M2 (the rows of [closure.md, Held-open items](closure.md#held-open-items)), all `open`
at `12c2be8`:

| Item | Deliverable | What waits | Closes at |
| --- | --- | --- | --- |
| H-1 | DH-D-SDK-4 | the schedule activation hook | the stage that builds schedules (after M2) |
| H-2 | DH-D-SDK-4 | validation against more than one provider route | after M2 |
| H-3 | DH-D-SDK-4 | inline image admission | after M2 |
| H-4 | DH-D-SDK-5 | a goal's stop signal | after M2 |
| H-10 | DH-D-UTIL-7 | replay of sessions with children, until the lineage records exist | after M2 |

H-21 is closed by step 16.10 (`fe13e4e`). Built-not-done: BUNDLE-3 until DH-S-22; CLI-2, CLI-4 and CLI-5
until DH-S-27; UTIL-8 until DH-S-39. At `12c2be8`, DH-T-TOOLCHAIN-1, -3 and -5 pass, and so does DH-T-TOOLCHAIN-6 at its
`src/harness` and `acceptance` sites. Its gate-root site (`TestScenarioGatesStopWhenSignedOut`) has not
run: the lanes at `a91abac` stopped at `gofmt` on a file inherited from `e3da93b`, which this branch now formats, and the lanes at `2da0f59` stopped at `vet` on analyzer findings in 17 inherited test files (`dh-s-16-vet-inherited`; see Exact tests and commands). The lanes must show all four passing at the closing commit. The step 16.8 cleanup evidence
is still owed.

**Not advanced yet (implementation-plan Stage 6.13, M2-DOD-1).** `built-through` in
`test/harness/acceptance-index.yaml` stays at `DH-S-11`. The S12..S16 states in
[closure.md](closure.md) stay `not built` because none of the DH-S-12 to DH-S-16 records passes
`ready --built` yet, and harness:DSH-M1 is not closed: every M1 record still reads `Closure status: open`.

<a id="decision-dh-s-16-vet-inherited"></a>
**Operator decision (answer to `dh-s-16-vet-inherited`: "A separate repair workstream fixes them; this
branch merges as is").** The `vet` findings at `2da0f59` are the project's own analyzers, QG-5
(goroutine in a test without a `t.Cleanup` release, 61) and QG-4 (`time.Sleep` in a test file, 24), in
17 test files inherited from main, none in this stage's diff (Full harness gate policy, rule 4). A
separate repair workstream fixes them on main; this branch merges without fixing them. The later S16
closure workstream reruns both lanes, and with them the gate-root TOOLCHAIN-6 site, after that repair
merges.
**Operator decision (answer to `dh-s-16-closure-wait`: "Merge the partial record now and finish closure
in a later workstream").** This workstream merges the partial record with `Closure status: open`.
A later workstream does the rest of Stage 6.13 once harness:DSH-M1 is closed, the guarded rows have
passed and the release build has the Claude Code route. That work is: record 16.3 and 16.4, prove the
gate-root TOOLCHAIN-6 site through green lanes, advance `built-through` to `DH-S-16`, update the
closure.md S12..S16 states, and run `ready` then `close`.
