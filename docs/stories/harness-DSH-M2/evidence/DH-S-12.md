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
| 12.1 | DH-D-VIEW-10 | 82a8b6e | cycle C-3, with 12.2 and 12.3 at one commit: the squash-merge commit of the implementation-plan Stage 2.5 integration PR (#118), recorded after merge, that registers all three in the build catalogue and lands `cyclec3_test.go` (the members were merged separately before it: 90abd12 #111, 30aeb4c #110, 7b4e8cd #113); `session.workdir` in `builtin.Session()` |
| 12.2 | DH-D-FS-4 | 82a8b6e | cycle C-3, same commit as 12.1; `fs.local` and `fs.ops` in `builtin.FS()` |
| 12.3 | DH-D-SANDBOX-1 | 82a8b6e | cycle C-3, same commit as 12.1; `sandbox.policy` in `builtin.Sandbox()` (item exported by `fs/ops`, its one importer) |
| 12.4 | DH-D-HUMAN-4 | 1333bc8 | P1, pulled forward; squash-merge of #121 |
| 12.5 | DH-D-FS-6 | 4aed016 | squash-merge of #120 |

## Scenarios

None executed here. Authored here: DH-E-FS-1, DH-E-FS-2, DH-E-VIEW-5 (run in DH-S-18), DH-E-SANDBOX-7 (run in DH-S-17) and the guarded DH-E-FS-3 (run in DH-S-16, step 16.8).
Each is a `//go:build scenario` test under `src/harness/internal/testkit/scenarios/` and is indexed in
`test/harness/acceptance-index.yaml` under the stage that runs it:

- DH-E-FS-1: `fs_fence_scenario_test.go`, indexed under DH-S-18. 3,334 write/edit/apply_patch cycles against a swapper process; during a swap the transient `fs/not-found` and `fs/not-directory` are accepted besides `fs/sandbox-denied` (answer `fs1-transient-refusal-codes`); at least one `fs/sandbox-denied`; read-only and escalation legs.
- DH-E-FS-2: `fs_long_scenario_test.go`, indexed under DH-S-18. 20 tasks on fresh checkouts, `rg` off `PATH`; invalid-pattern share under 5%, git changes equal the logged edits, outside listing unchanged.
- DH-E-FS-3: `fs_two_process_scenario_test.go`, indexed under DH-S-16. guarded; two SDK hosts, 1,000 marker edits each with an outside editor. Deviation: each launched process has its own `DSH_HOME`, so "one home" is the shared `{locks}` directory.
- DH-E-VIEW-5: `view_workdir_scenario_test.go`, indexed under DH-S-18. move and recovery `workspace-root/change` events; `workdir/unavailable` once both directories are gone.
- DH-E-SANDBOX-7: `sandbox_policy_text_scenario_test.go`, indexed under DH-S-17. 3 policy texts x 12 tasks x 3 runs; per-platform report to `DSH_SCENARIO_REPORT`.

FS-1 and FS-3 drive dsh against a loopback scripted model endpoint (`scripted_model_test.go`); per answer
`fs-scripted-model-vs-replay` they switch to the replay provider when DH-S-16 lands it.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |

## Manual checks register

The manual checks impl.md DH-S-12 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 12.2 | the write tool asked for a path outside the roots through a symbolic link: the denial (scratch directory) | Seen on Windows 11 NTFS (junction; symbolic links not permitted to the account) and on WSL Ubuntu-24.04 ext4 (symbolic link): the write is refused `fs/sandbox-denied` and the outside directory holds only its `keep.txt` | operator-accepted agent run (answer `dh-s-12-manual-checks`: "Accept the recorded runs") | b02b689 | `manual` |
| 12.2 | a directory swapped for a link during a write: the fence held (scratch directory) | Seen on both platforms with the `util/fsx` stage-directory pin (Open items), 1,000 writes against a swapper per run: the outside directory holds only `keep.txt` and no `.dsh-stage-*` file is left in the workspace. Windows NTFS (junction), 18,439 swaps: `sandbox-denied` 905, `not-directory` 13, `io` 5, success 77. WSL ext4 (symbolic link), 3 of 3 runs: `sandbox-denied` 981/974/992, `not-directory` 2/6/5, success 17/20/3. Before the pin the same runs left 1 to ~140 staged files | operator-accepted agent run (answer `dh-s-12-manual-checks`) | b02b689 | `manual` |

## Owner defaults (M2-C-4)

The defaults of [impl.md sec. 9.1](../../../harness/impl.md#91-defaults-the-owner-has-not-confirmed) whose
"Confirm before" is DH-S-12. The owner confirms each, or changes the document that holds it, before this
stage closes; `--built` refuses a row with no confirmation or no name in By.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |
| Full-access label | I2 Q3 | In full-access mode a command may take over the harness, and the label says so | Yes, as written | operator (answer `m2-unconfirmed-defaults`: "Confirm both as written") | |
| UTF-8-only text tools | core §6.3 | Text tools refuse every encoding but UTF-8 | Yes, as written | operator (answer `m2-unconfirmed-defaults`) | |

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

Cycle C-3 integration (implementation-plan Stage 2.5, TC-01b), run from `src/harness`:
`go test -count=1 ./internal/boot/builtin -run TestCycleC3` (`internal/boot/builtin/cyclec3_test.go`, introduced by
the cycle C-3 integration PR #118). It boots `fs.local`, `session.workdir`, `sandbox.policy`
(`workspace-write`) and `fs.ops` from the build catalogue over an embedded SQLite session store in
`t.TempDir()`. A move of the session from project `A` to sibling project `B` turns a write to `A/x` into
`fs/sandbox-denied` (file untouched) while `B/x` is written, and the resolver resolves `B` as a write root and
not `A`. A second process reopens the store and resumes: the rebuilt working directory is `B` and the same
decisions hold. Relative-path writes land in the current directory through `workdir.Access` at each stage.
`TestCycleC3ProviderRestartKeepsTheWorkingDirectoryAndTheFence` reconfigures, unmounts and remounts `fs.local`:
`session.workdir` is not reloaded (its fold and attached sessions survive), `fs.ops` and `sandbox.policy` are,
and the fence holds on the new provider. Passed locally on Windows native (`go1.27.4`).

Manual checks (register above): run by driving `fs.ops` booted from the build catalogue (the `bootCycleC3`
fixture) in a scratch `t.TempDir()`, at b02b689 with this stage's diff (the check 2 re-run includes the `util/fsx` pin), on Windows native and on WSL Ubuntu-24.04 (ext4 copy of
`src`), `go1.27.4`. Check 2 ran 1,000 writes against a goroutine swapping `A/sub` for a link to the outside
directory and back.

Scenario sources: `go -C src/harness vet -tags scenario ./internal/testkit/scenarios/...` (clean).

Gate: `go -C tools/harness-gate run . --root ../..` on the working tree with this stage's diff; the lanes
(`lanes/win.ps1`, `lanes/wsl.sh`) and `--evidence` refuse an uncommitted tree, so per answer
`s12-lane-vet-inherited` they re-run on the merged SHA (the vet repair is on `main`: d6f699a #124, 7342079 #125).
Local Windows result on b02b689 plus this diff: gofmt, vet, staticcheck, modules, test, coverage, trace,
scenario-index, policy-text PASS; budget failed once under load (`TestBudgetStoreAppend` open p95 846ms over
250ms; passed 3 of 3 re-runs alone, and the `^TestBudget` set passed); cross-compile (6 targets), govulncheck
and self (`-skip ^TestRepositoryPassesEveryGate$`, after updating `TestRequireSubcommand`'s pinned lists to the
new index entries) PASS, each run on its own. That full run predates the `util/fsx` pin. On the final diff
(including the pin), only the checks that diff touches were run (operator rule `operator-standing-gate-scope`):
`gofmt -l` on the changed files (clean); `go vet` with `-tags=` and with `-tags=replay,scenario,bench` on
`./internal/util/fsx` and `./internal/testkit/scenarios/...` (clean); `go test ./internal/util/fsx` (ok);
in `tools/harness-gate`, `go vet` and `TestRequireSubcommand` (ok); `harness-gate policy-text` (ok); and the
scenario-index gate. A later revision of FS-2 imported `internal/util/textdiff/patch`. The scenario boundary
(`acceptance.BoundaryImport`) rejects that import, so the scenario-index gate failed on that tree (commit 8daae3c).
FS-2 now reads patches with its own standard-library reader (`fs2ParsePatch`, `fs2ApplyPatch`). On the current
diff the scenario-index gate passes again (39 scenario tests authored, 39 indexed in 11 stages, built through
DH-S-11), and the project analyzers (`-vettool` with the built gate) are clean on both packages.

The scenarios' own oracles have focused tests that need no live model. Run them with
`go -C src/harness test -tags=scenario -run 'TestFS2PatchOracle|TestSandbox7Counts' ./internal/testkit/scenarios/`
(both pass).
- `TestFS2PatchOracleReadsTerminatedPatches` covers FS-2's patch reader. It stops at `*** End Patch`, skips a
  heredoc wrapper and honours `*** End of File`. Seven of its eight cases failed on the previous reader.
- `TestSandbox7CountsOnlyEscalatedRetriesOfDeniedCalls` covers SANDBOX-7's escalation count. Only an approval
  of a retry of an `fs/sandbox-denied` call counts: the same tool and arguments, plus `sandbox_permissions`
  `danger-full-access` and a justification that the approval shows. An ordinary tool approval does not count.

FS-3 now checks the following:
- Each edit's returned `sha256` version is compared with the final file.
- Every editor save must write back an acknowledged version.
- An edit that went missing must be explained by a save that rewrote an earlier version.
- The kill leg freezes the process, probes its lock file with a non-blocking lock, and kills it only while the
  lock is held. It uses `fs3Freeze` and `fs3LockHeld`, written with the standard library only, for Unix and
  Windows. On Windows, a temporary self-test confirmed that the probe and the freeze work.
- The survivor's edit must be the file's version on disk, and the lock must be free afterwards.

Not yet recorded: the lane outputs on the merged SHA and `DSH_REQUIRE_SCENARIOS`
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

**Fixed here: staged files leaked under a directory swap (DH-D-FS-4, `util/fsx/rooted.go`).** `publishIn`
removed its `.dsh-stage-*` file by name through the `os.Root`; once the stage's directory had been swapped for a
link (or moved), the remove was refused or missed, the error was dropped, and the staged file stayed in the real
directory (`linkOrReserve` had the same shape). The fence held throughout. Now the stage's directory is opened
as a sub-root (`dirPin`) when the stage is created; the staged file is created and every leftover removed
through that handle, while publication stays by name through the root, so the fence decides as before. A
refused open of the directory is the stage step's error (no by-name fallback). Regression test
`TestReplaceInRemovesStageAfterDirectorySwap` (`util/fsx/replace_test.go`, `ReplaceIn` and
`CreateIfAbsentIn`) fails without the pin and passes with it on Windows and WSL; manual check 2 re-run above.
Where links are refused, `CreateIfAbsentIn` reserves the name instead. That reservation is now made and removed
through a handle on the directory the name resolves to at that moment, and removed only while it is still the
reservation (`os.SameFile`). Before this, a swap between staging and reserving made a failed publication
delete another writer's file of the same name in the original directory and leave its own reservation behind.
Regression test `TestCreateIfAbsentInRemovesItsOwnReservationAfterDirectorySwap` fails without the change
and passes with it (Windows); the fsx package passes on WSL.

**Codes during a swap outside FS-1's allowed set.** Windows also returned `fs/io` (2 to 5 of 1,000) and, once,
`fs/permission-denied`. FS-1 as authored fails on them; raised as Q `fs1-swap-io-codes`.

Debt-audit item `UP-DEBT-found.Q3.2` (a real model giving up before its first tool call) closes when DH-E-SANDBOX-7 passes in DH-S-17. DH-D-FS-4 stays built until DH-S-28 (DH-E-FS-5).
