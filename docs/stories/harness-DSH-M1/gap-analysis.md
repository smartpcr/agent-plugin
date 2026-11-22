# M1 Gap Analysis: What Is Missing at `37a524a`, and How Closure Is Judged

## 1. What this document is

**Purpose.** Milestone M1 of the `dsh` story (impl.md stages DH-S-1 to DH-S-11) has merged
code for all 40 story-plan stages at commit `37a524a`. A verification pass, followed by
eight cluster reviews, found that the code and the records do not yet meet the M1
definition of done ([tech-spec.md](tech-spec.md) sec. 2.3). This document lists every gap
found, says why each one matters, and states how its closure will be judged.

**Audience.**
- **Operators** who will run the gate lanes and the manual checks. Read sec. 3, sec. 4
  and sec. 5.
- **Reviewers and the owner** who must answer the open decisions. Read sec. 5 and sec. 7.
- **Engineers** who will close the gaps. Read sec. 6 (the register) and the plan.
- **Readers new to the system.** Read the vocabulary below first.

**How to read it.** Sec. 4 gives a one-line verdict per stage. Sec. 5 gives the dozen
findings that matter most. Sec. 6 is the full register: one row per gap, with its stable
ID, its evidence, what kind of work closes it, its priority and the plan stage that closes
it. Sec. 7 lists every decision the owner must make, ordered by how much P0 work waits on
it.

**Relationship to the plan.** The design guidelines split the work into three linked
parts. This document is part **A** (what is missing and why it matters) and part **C**
(how closure is judged, sec. 2.4). The companion
[gap-closure-plan.md](gap-closure-plan.md) is part **B**: the stages, steps and test
scenarios that close the gaps. Both documents use the same gap IDs (`M1-G-<CLUSTER>-<n>`),
decision IDs (`M1-D-<CLUSTER>-<x>`) and plan stage numbers (12.1 to 19.5). Every "Closed
by" cell below is a stage number in [gap-closure-plan.md](gap-closure-plan.md).

### 1.1 Vocabulary

| Term | Meaning, and why it exists |
|---|---|
| Stage `DH-S-n`, step `n.m` | A unit of work in [impl.md](../../harness/impl.md). M1 is DH-S-1 to DH-S-11: 103 steps, 99 deliverables. |
| Deliverable `DH-D-*` | A component with a "Done when" line that names the tests proving it. |
| Test `DH-T-*` | A named local test. Rule 2 (below) counts these. |
| Scenario `DH-E-*` | A remote or real-environment acceptance run. M1 authors most scenarios; later stages run them. |
| Gate | `tools/harness-gate`: one command with 8 ordered gates (gofmt, vet, test, coverage, trace, scenario-index, budget, cross-compile). It decides "this commit passes" (QG-1). |
| Lane | One operating system on which the gate runs. Under OWNER-1 the lanes are Windows (native) and Linux (WSL). |
| Ticked / built | impl.md sec. 1.3 and sec. 1.4 rule 2: a step is ticked *built* only when its code is merged, every `DH-T` named in its deliverable's "Done when" passes under the gate, coverage holds, and its manual check is recorded. |
| Done | Built, plus every proving scenario has passed. In M1 only deliverables proven by local tests alone can be done. |
| Held open (`H-n`) | impl.md sec. 10.3: a part of a ticked step, moved by recorded decision to a named later step. It is listed open, never counted as a pass. |
| `built-through` | The marker in `test/harness/acceptance-index.yaml` that tells the gate which stages are built. |
| Cluster | One of the eight review areas: GATE, TOOL, FND (DH-S-3, -4), BOOT (DH-S-6), STORE (DH-S-7, -8), PIPE (DH-S-9, -10), LOOP (DH-S-11), CLOSE (records and closure). |

## 2. Baseline and method

### 2.1 What was examined

- **Commit:** `37a524a` ("split to M1-M5") on `main`.
- **Specs read:** [impl.md](../../harness/impl.md) stages DH-S-1 to DH-S-11, the area and
  library specs each step links to (for example
  [toolchain.md](../../harness/upstream/toolchain.md),
  [agent-loop.md](../../harness/areas/agent-loop.md),
  [session-store.md](../../harness/areas/session-store.md)), and the story documents
  [tech-spec.md](tech-spec.md), [architecture.md](architecture.md),
  [implementation-plan.md](implementation-plan.md), [e2e-scenarios.md](e2e-scenarios.md)
  and the eleven records under `evidence/`.

### 2.2 What was run on 2026-11-22

| Command | Where | Result |
|---|---|---|
| `go build ./...`, `go vet ./...`, `go test -count=1 ./...` (no `-race`, no tags) | `src/go`, `src/sdk`, `src/harness` | All pass. |
| `go test ./...` | `tools/harness-trace` | Pass. |
| `go test ./...` | `tools/harness-gate` | `TestRepositoryPassesEveryGate` **fails at gate 2/8 (vet)** with 81 distinct findings: 53 unjoined goroutines or listeners (QG-5), 24 `time.Sleep` calls in tests (QG-4), 4 fuzz targets without `seeds_test.go` (QG-6). |
| `harness-trace verify` (read-only) | repository | PASS. |
| `wsl.exe -l -v` | development host | "no installed distributions". |
| `gh api .../branches/main/protection` | GitHub | 404; the ruleset list is empty. |

Nothing ran with `-race`, with build tags, or under the gate's later gates (3-8) at this
commit. The plain `go test` passes above are therefore **not** gate evidence.

### 2.3 What was read only

Everything else was established by reading code, specs, evidence and git history at
`37a524a`: every file:line cited in sec. 6, the CI run history (last green harness run
44675306125 at `8de1d59`, PR #15; first red run 44693247717 at `5901510`, PR #16; latest
red run 45018579821 at `dd3f78e`), and greps for `DH-T` tags. Each cluster re-verified
the earlier pass's claims and marked each gap **CONFIRMED** (seen in code or docs) or
**PARTLY** (true in part, or a risk not yet proven by a run).

### 2.4 How closure is judged (part C)

**The status rule.** Built is not done. A skipped, missing, blocked or failed required
check is never a pass. A gap stays open until its closing evidence exists at a named
commit.

| Disposition | Meaning | The gap is closed when |
|---|---|---|
| **BUILD** | Code or tests must be written. | The code is merged; each named `DH-T` test exists, carries its tag (the convention of plan Stage 13.6) and passes under `harness-gate` on both lanes at the closing commit; the stage's evidence record names the tests and the commit; any manual check is recorded and labelled `manual`. |
| **RECORD** | A document, record or tick must be corrected. | The edit is merged, cites its decision in the new `decisions.md` where one applies, and `harness-trace verify` passes. |
| **DECIDE** | The owner must choose before work can proceed. | The answer is recorded in `decisions.md` with status `in force` (plan Stage 12.1), **and** the follow-on BUILD or RECORD work then closes as above. An unanswered decision keeps every gap that waits on it open. |

A gap that a decision moves to a later stage is closed for M1 only as a **held-open**
entry with its closing stage, listed in `evidence/DH-S-11.md` "Open items".

**Counting rule used in this document.** A gap whose fragment disposition includes a
decision is counted as DECIDE; otherwise as BUILD if any code work is needed; otherwise
as RECORD.

**Priority.** P0 blocks M1 closure (plan Phases 12 to 18). P1 must close before a named
later consumer stage (mostly plan Phase 19) and does not block M1. P2 is tracked. Priority
never weakens the completion criteria above.

**The M1 definition of done, restated under OWNER-1 and OWNER-2** (to be adopted by plan
Stage 12.1), at one commit on `main`:
1. every impl.md step 1.1 to 11.7 is ticked *built* under rule 2, and the story plan
   agrees;
2. `tools/harness-gate` passes all gates on Windows (native) and on Linux (WSL), each run
   recorded in the local-lane evidence format;
3. every stage DH-S-1 to DH-S-11 has a complete evidence record (impl.md sec. 7.3);
4. the M1 manual check is recorded on a signed-in Claude (and, per M1-D-LOOP-6, Copilot)
   route with no API key;
5. H-21 is listed open with its closing step 16.10, together with every other held-open
   item created by a decision in sec. 7.

## 3. Owner decisions applied (2026-11-22)

**OWNER-1: platforms.** The Linux gate lane runs under WSL on the Windows development
host. The macOS lane is skipped, and macOS is removed from M1 scope by owner decision; it
is never a pass. "Gate green" for M1 means Windows (native) plus Linux (WSL).
- **What it changes.** The DoD bullet "passes on the three CI operating systems" must be
  rewritten (M1-G-GATE-19, M1-G-TOOL-12, M1-G-CLOSE-12). A WSL distribution must be
  provisioned (M1-G-GATE-20, the long pole). Local lanes need a runbook and an evidence
  format that replaces CI run ids (M1-G-GATE-22, M1-G-CLOSE-13). CI still lists macOS
  (M1-G-GATE-21, M1-G-TOOL-13). Lanes must use the floor toolchain go1.26.0
  (M1-G-TOOL-14). Manual checks that said "each OS" re-run on Windows and WSL
  (M1-G-FND-23, M1-G-BOOT-17). POSIX-only store tests now depend on the WSL lane
  (M1-G-STORE-30), and store wording still names macOS (M1-G-STORE-31).
- **What it does not change.** Historical macOS passes (DH-S-1 at `e13abf3`, DH-S-6 at
  `42d0d7d`) stay in the records, labelled historical. Every macOS row reads "not run --
  removed from M1 by OWNER-1".
- **WSL pitfall, in plain words.** The Windows drive seen from WSL (`/mnt/e`, a 9p/drvfs
  mount) reports every file as mode 0777, ignores `chmod`, has unreliable file locks and
  change events, and is slow. Privacy tests, SQLite WAL and the budget gate would give
  false results there. Every WSL run uses a WSL-native clone on ext4.

**OWNER-2: models.** DeepSeek and the DeepSeek API key are no longer required. Real-model
checks use only Claude and GitHub Copilot through the user's signed-in session, with no
API key.
- **What it changes.** No signed-in route exists (M1-G-PIPE-12). The DH-S-9 probe and the
  M1 check are DeepSeek-only (M1-G-PIPE-11, M1-G-LOOP-12). The preflight demands
  `DSH_DEEPSEEK_API_KEY` (M1-G-TOOL-15). Thirteen authored scenarios use DeepSeek in their
  Given (M1-G-CLOSE-14, with M1-G-LOOP-13 and M1-G-PIPE-14). The DeepSeek provider's fate
  must be decided (M1-G-PIPE-13). Credentialed scenarios need a host where a signed-in
  session can exist (M1-G-TOOL-16).
- **The tension it creates.** An official vendor client runs its own system text and loop,
  so "the bytes sent equal the request rebuilt from the log" (SP-1, DH-O-4) can hold only
  at the harness-to-client boundary. See sec. 7.1.

## 4. Verdict by stage

"Code state" counts steps, approximately, from the register's "Where" fields: **I** =
implemented with no open code or named-test gap; **P** = partial (code or a named test
missing or narrower than the spec); **M** = wholly missing. P0 gaps are counted by the
gap's primary stage; gaps that span stages are in the last row.

| Stage | Steps | impl.md ticks | Code state I / P / M | P0 gaps | Open manual checks | Closed by (plan phases) |
|---|---|---|---|---|---|---|
| DH-S-1 toolchain | 9 | 9/9 | 4 / 5 / 0 (gate narrower than spec; red since #16) | 20 | 3-OS record at `e13abf3` is historical; re-run on both lanes | 12, 13 (P1: 19) |
| DH-S-2 cordis | 8 | 8/8 | 8 / 0 / 0 (test hygiene only) | 3 | example output partial, no commit | 13 |
| DH-S-3 loader | 9 | 9/9 | 6 / 3 / 0 | 3 | recorded, no commit | 14 |
| DH-S-4 leaf packages | 14 | 14/14 | 7 / 3 / 0, plus 4 untagged and unaudited | 14 | watcher and search checks **pending** | 13, 14 |
| DH-S-5 session core | 13 | 13/13 | 13 / 0 / 0 (test hygiene only; not re-read by a code cluster) | 2 | a test run only, no commit | 13 |
| DH-S-6 boot and launch | 17 | 17/17 | 8 / 9 / 0 | 11 | 3-OS record at `42d0d7d` (Linux from `ubuntu-latest`); re-record on Windows and WSL | 13, 14 (P1: 19) |
| DH-S-7 SQLite store | 6 | 6/6 | 4 / 2 / 0 | 13 | run as tests, no commit | 13, 15 |
| DH-S-8 state and views | 7 | 4/7 | 2 / 5 / 0 | 17 | run as tests, no commit; 8.3-8.5 closure never done | 13, 15 |
| DH-S-9 provider and retry | 3 | 0/3 | 2 / 1 / 0; signed-in route missing (new) | 3 | good/bad-key check **pending**, must be retargeted | 16 |
| DH-S-10 tools and approvals | 10 | 10/10 | 6 / 4 / 0 (10.6 lacks `spill_read`) | 8 | three checks **never run** | 16 |
| DH-S-11 agent loop | 7 | 4/7 | 4 / 3 / 0 (11.3 lacks the prompt call) | 17 | M1 check **pending** | 13, 17 |
| Cross-stage | -- | -- | -- | 14 | default per-step check: fewer than 20 of 103 steps have any record | 12, 13, 18 |
| **Total** | **103** | **94/103** | | **125** | | |

**Verdict.** impl.md ticks 94 of 103 steps, but no stage can currently be shown *built*
under rule 2: the gate is red, about half of the named tests are untagged or missing, and
most evidence records lack a commit and gate output. No step is wholly missing; every gap
is part of a step.

## 5. Headline findings

Most important first.

1. **The gate has been red since PR #16, and `main` has no required checks.** Every CI
   run since 2026-11-20 stops at vet. Twenty PRs (#16 to #84) merged red and added the 81
   findings, because branch protection returns 404 and no ruleset exists. Gates 3 to 8
   (race, coverage, trace, scenario index, budgets, cross-compile) have not run on any
   code after DH-S-1. (M1-G-GATE-1, -15 to -18.)
2. **The model gets no system prompt.** The driver never calls prompt assembly, so every
   request carries only history, tool schemas and controls: no identity line, persona,
   tool guidance or environment. The log cannot say which sections built a prompt,
   because nothing built one. Recording M1 now would demonstrate a model with no
   instructions. (M1-G-LOOP-1, -2, -7.)
3. **DH-S-10 is not really closed.** Its record says "closed", but `spill_read` does not
   exist, so the model cannot read the rest of a large result. The approval policy
   (`never`) does not exist. An approval open at a crash is never closed, and the model is
   told "outcome unknown" for a call that never ran. "Allow for this session" creates no
   grant, because no subject is passed. (M1-G-PIPE-1, -3, -4, -6, -14.)
4. **Linux children are orphaned.** In the process-group fallback, the rung the DH-S-6
   evidence records on Linux, no `Pdeathsig` is set, so a host crash leaves the whole
   child tree running, direct child included. (M1-G-BOOT-5.)
5. **Secret names never reach `BuildEnv`, and nothing masks released secrets.** A named
   reference such as `secret://github/pat` maps to `GITHUB_PAT`, which no deny rule
   catches, and no caller passes the store. No code calls `Store.Mask`, so a tool that
   echoes a released value logs it in clear. The child-process half is latent (no
   production spawn yet); the tool-result half is reachable now. SP-11 is an M1
   invariant. (M1-G-BOOT-6.)
6. **Migrations run with no backup.** A newer `dsh` upgrades the database layout step by
   step, each in its own transaction, with no copy taken first and no check for a live
   older instance. A failure halfway leaves a half-upgraded file. Homes are scratch-only
   until step 16.8, but layouts 2 to 4 already exist. (M1-G-STORE-1, -2.)
7. **Every synced append rewrites the event index, which grows quadratically.** The event
   index view changes on every event and is stored as one JSON document, so each append
   rewrites a document that grows with the log: O(n) per append, O(n^2) per session,
   several MB per append at 100k events. (M1-G-STORE-13.)
8. **A failing view loses log events.** A refused derived row rolls back the events in
   the same transaction, the opposite of the spec, so a fold defect ends the turn in
   error. (M1-G-STORE-10.)
9. **Durations are stored as nanoseconds in fingerprinted events.** `time.Duration`
   derives to an integer, not `"30s"`, and `vocab.Failure.RetryAfter` already persists
   that form in fingerprinted session events. Fixing it later is a persisted-format
   change; now it is cheap. (M1-G-FND-12.)
10. **The bookkeeping is unreliable.** 130 of the 259 `DH-T` IDs that M1 "Done when" lines
    name appear nowhere in the code (an upper bound on missing tests). Ticks disagree
    between impl.md and the story plan, in both directions. `built-through` is stuck at
    DH-S-7. DH-S-10 and DH-S-11 were built before DH-S-9 closed, against the entry rule,
    with no recorded decision. (M1-G-CLOSE-1 to -4, -9.)
11. **The signed-in routes strain the rebuildable-request guarantee.** Under OWNER-2 the
    real model is reached through an official client that adds its own system text and
    loop. "The bytes sent equal the request rebuilt from the log" (DH-O-4, SP-1) can then
    hold only at the client boundary. The owner must accept a narrowed guarantee, or
    choose another route. (M1-G-PIPE-12, M1-D-PIPE-3, -4, M1-D-CLOSE-3.)
12. **The toolchain gate is narrower than its spec, with no recorded decision.**
    staticcheck, module tidy/verify, govulncheck, three analyzers and the reasoned
    exception are absent; the gate's own tests run nowhere; `src/go` depends on godog
    against "standard library only". (M1-G-TOOL-1 to -11.)
13. **No WSL distribution is installed on the host.** The Linux half of the DoD cannot
    run until one is provisioned. This is the long pole and should start first.
    (M1-G-GATE-20.)

## 6. Gap register

Every gap from every fragment appears in exactly one row. "Closed by" gives stage
numbers in [gap-closure-plan.md](gap-closure-plan.md); a second number in brackets is a
later step that completes it. "Same as" marks a duplicate kept under both IDs; "overlaps"
marks a related gap that closes separately. File paths are under
`src/harness/internal/` unless they start with `src/`, `tools/`, `.github/`, `docs/` or
`test/`.

### 6.0 Totals

| Cluster | Gaps | P0 | P1 | P2 | BUILD | DECIDE | RECORD |
|---|---|---|---|---|---|---|---|
| GATE | 27 | 22 | 3 | 2 | 18 | 5 | 4 |
| TOOL | 18 | 13 | 3 | 2 | 9 | 6 | 3 |
| FND | 23 | 15 | 4 | 4 | 16 | 4 | 3 |
| BOOT | 17 | 9 | 8 | 0 | 12 | 3 | 2 |
| STORE | 31 | 27 | 2 | 2 | 22 | 4 | 5 |
| PIPE | 14 | 11 | 3 | 0 | 8 | 4 | 2 |
| LOOP | 16 | 15 | 1 | 0 | 9 | 5 | 2 |
| CLOSE | 14 | 13 | 1 | 0 | 4 | 4 | 6 |
| **Total** | **160** | **125** | **25** | **10** | **98** | **35** | **27** |

Totals count IDs, so duplicates kept under two IDs are counted twice (sec. 6.9).

### 6.1 GATE: the red repository gate (DH-S-1 gates, test hygiene in every stage)

The 81 vet findings are test-code violations. The fix patterns are: register a cleanup
that unblocks and then joins each goroutine; run timing tests in a `testing/synctest`
bubble (virtual time); replace polling with an explicit signal; move fuzz seeds into
`seeds_test.go`; fix the analyzer when it is wrong, never suppress (QG-2). Citations: QG-4
`tech-spec.md:243`, QG-5 `:244`, QG-6 `:245`; gate code `tools/harness-gate/gates.go`,
analyzers `tools/harness-gate/analyzers/`.

| ID | Gap | Where | Evidence | Verified | Disp. | Pri. | Closed by |
|---|---|---|---|---|---|---|---|
| M1-G-GATE-1 | The gate is red at HEAD, so the M1 DoD is unmet and no later gate result exists for M1 code. | DH-S-1 Result; tech-spec sec. 2.3; DH-D-TOOLCHAIN-2 | gate log ends `FAILED at gate vet`; runs 44675306125 (last green), 44693247717 (first red), 45018579821 (latest) | CONFIRMED | BUILD | P0 | [13.7](gap-closure-plan.md) (final run [18.1](gap-closure-plan.md)) |
| M1-G-GATE-2 | cordis config and tree tests start 7 goroutines with no joining cleanup; a failing test leaks into the next. | DH-S-2; DH-D-CORDIS-* | `src/go/cordis/config_test.go:137,151,164,455,456`; `tree_internal_test.go:25,68` | CONFIRMED | BUILD | P0 | [13.1](gap-closure-plan.md) |
| M1-G-GATE-3 | cordis unload tests: 15 unjoined goroutines and a settle sleep, so "unload is final" (SP-10) rests on wall-clock settling. | DH-S-2; DH-T-CORDIS-5..14 | `src/go/cordis/unload_test.go:96..824`, sleep `:500` | CONFIRMED | BUILD | P0 | [13.1](gap-closure-plan.md) |
| M1-G-GATE-4 | Four untrusted-parser packages register fuzz seeds inline, with no `seeds_test.go`. | DH-S-4 (#30); DH-D-TOOLCHAIN-3 | `util/caltime/caltime_test.go:215`, `glob/glob_test.go:260`, `recur/rrule_test.go:346`, `version/version_test.go:155` | CONFIRMED | BUILD | P0 | [13.1](gap-closure-plan.md) |
| M1-G-GATE-5 | The cleanup analyzer flags a listener assigned to a struct field even though it is released (false positive). | QG-5; DH-T-TOOLCHAIN-8 | `util/egress/egress_test.go:32,51`; `analyzers/cleanup.go` `assignedIdents` | CONFIRMED | BUILD | P0 | [13.1](gap-closure-plan.md) |
| M1-G-GATE-6 | Session core and util tests: 6 unjoined goroutines. | DH-S-4, DH-S-5 | `session/scope/scope_test.go:142`, `session/event/event_test.go:380`, `session/session_test.go:191`, `session/views/views_test.go:86`, `util/fsx/replace_test.go:431`, `util/watch/watchtest/manual_test.go:218` | CONFIRMED | BUILD | P0 | [13.2](gap-closure-plan.md) |
| M1-G-GATE-7 | Session query ordering tests sleep 2 ms and poll a counter (3 sleeps), so the ordering proof depends on the wall clock. | DH-S-8; DH-D-VIEW-* | `session/query/visibility_test.go:297,303,333` | CONFIRMED | BUILD | P0 | [13.2](gap-closure-plan.md) |
| M1-G-GATE-8 | SQLite shutdown and blob GC-lock tests: 3 goroutines, 3 sleeps; the close-during-busy-wait proof can pass without reaching the busy wait. | DH-S-7, DH-S-8; DH-D-STORE-*, DH-D-STORAGE-* | `session/store/sqlite/shutdown_test.go:37,42,44`; `storage/blob/gclock_test.go:37,61,122` | CONFIRMED | BUILD | P0 | [13.2](gap-closure-plan.md) |
| M1-G-GATE-9 | Boot tests: 4 goroutines and a sleep inside a manual-clock loop. | DH-S-6; DH-D-BOOT-* | `boot/audit_test.go:70,91`; `boot/boot_test.go:98,434,632` | CONFIRMED | BUILD | P0 | [13.2](gap-closure-plan.md) |
| M1-G-GATE-10 | Workspace archive, llm runtime and human tests: 6 goroutines. | DH-S-5, DH-S-8 | `storage/workspace/archive_test.go:338,497`; `llm/runtime_test.go:506,551,621`; `human/human_test.go:199` | CONFIRMED | BUILD | P0 | [13.2](gap-closure-plan.md) |
| M1-G-GATE-11 | Agent registry and tool-schedule tests: 2 goroutines, 3 sleeps used to create overlap. | DH-S-5; step 11.2; DH-T-AGENT-26 | `agent/agent_test.go:285,288,291`; `tools/core/schedule_test.go:150,184` | CONFIRMED | BUILD | P0 | [13.3](gap-closure-plan.md) |
| M1-G-GATE-12 | Agent driver tests: 6 goroutines, 6 sleeps; the cancel and teardown proofs (SP-3, SP-4) are timing-based, and one negative assertion is a wall-clock wait. | DH-S-11; DH-T-AGENT-5..22 | `agent/driver/lifecycle_test.go:142,215,253,255,268,302,321,471,474,559`; `driver_test.go:447`; `retry_test.go:278` | CONFIRMED | BUILD | P0 | [13.3](gap-closure-plan.md) |
| M1-G-GATE-13 | Delegate subagent tests: 2 goroutines, 3 sleeps and a copied poll helper. | step 11.6; DH-D-SUBAGENT-1 | `delegate/subagent/subagent_test.go:93,117,230,233,354` | CONFIRMED | BUILD | P0 | [13.3](gap-closure-plan.md) |
| M1-G-GATE-14 | Scenario-layer files (tagged): 1 goroutine and 4 polls on real child processes that block on no fact. | DH-S-7; DH-E-STORE-* | `testkit/scenarios/store_two_process_scenario_test.go:37,179`; `store_crash_recovery_scenario_test.go:98,106,149` | CONFIRMED | BUILD | P0 | [13.3](gap-closure-plan.md) |
| M1-G-GATE-15 | The race-and-shuffle test gate has never run on post-DH-S-1 code for many packages (`src/sdk`, `boot`, `cli`, `util/*`, `host`, `schema` and others). | QG-4; step 1.3; DH-T-TOOLCHAIN-2 | `gates.go` `testGate`; `.github/workflows/e2e-*.yml` cover only some packages | PARTLY | BUILD | P0 | [13.7](gap-closure-plan.md) |
| M1-G-GATE-16 | The 85% coverage floor is likely missed in several packages; each miss needs tests, not an exemption. | QG-3; step 1.5; DH-T-TOOLCHAIN-7 | `agent` 81.6%, `human/question` 79.4% (`evidence/DH-S-10.md:39`); `session/scope` 84.8% (`evidence/DH-S-5.md:37`); `tools/conform` has no tests | PARTLY (risk) | BUILD | P0 | [13.7](gap-closure-plan.md) |
| M1-G-GATE-17 | The trace, scenario-index, budget and cross-compile gates have not run since DH-S-1. | QG-7..9; steps 1.2, 1.7 | `harness-trace verify` PASS read-only; budgets `session/store/sqlite/budget_test.go:15-19` | PARTLY | BUILD | P0 | [13.7](gap-closure-plan.md) |
| M1-G-GATE-18 | The harness CI job is not a required check, so a red gate can merge again. | QG-10 `tech-spec.md:249`; DH-ADR-TOOLCHAIN-12 | `gh api .../branches/main/protection` 404; rulesets `[]` | CONFIRMED | DECIDE (M1-D-GATE-18) | P1 | [13.7](gap-closure-plan.md) |
| M1-G-GATE-19 | OWNER-1 is not reflected in the DoD or toolchain docs, so M1 would close against unstated criteria. Overlaps M1-G-TOOL-12 (record part) and M1-G-CLOSE-12. | tech-spec sec. 2.3, QG-4, QG-10, EN-6, R-06; impl.md DH-S-1; DH-ADR-TOOLCHAIN-6 | `tech-spec.md:141`; `docs/harness/upstream/toolchain.md:229`; `implementation-plan.md:71,85` | CONFIRMED | RECORD | P0 | [12.1](gap-closure-plan.md) |
| M1-G-GATE-20 | The Linux (WSL) lane does not exist: no distribution is installed. Overlaps M1-G-TOOL-12 (provisioning) and M1-G-CLOSE-13 (runbook). | OWNER-1; EN-6; R-08 | `wsl.exe -l -v`; `util/fsx/remote_linux.go:7-14` refuses 9p homes | CONFIRMED | BUILD | P0 | [12.3](gap-closure-plan.md) |
| M1-G-GATE-21 | The CI matrix still includes macOS. Same as M1-G-TOOL-13; closed by 12.3. | `.github/workflows/ci.yml` harness job | `ci.yml:130` | CONFIRMED | DECIDE (M1-D-GATE-21) | P0 (raised by the lead to match M1-G-TOOL-13) | [12.3](gap-closure-plan.md) |
| M1-G-GATE-22 | There is no evidence format for a local-lane run, so a pass cannot be audited without a CI run id. Same as M1-G-TOOL-12 (evidence part) and M1-G-CLOSE-13 (template). | impl.md sec. 7.3; EV-7; plan Stage 1.4 | `evidence/DH-S-1.md:94-127` | CONFIRMED | RECORD | P0 | [12.4](gap-closure-plan.md) (lane capture [12.3](gap-closure-plan.md)) |
| M1-G-GATE-23 | Gates and the exception mechanism the spec requires are absent (staticcheck, module tidy and verify, govulncheck, reasoned site exceptions). Same as M1-G-TOOL-1, -2, -4, -7; closed by 13.5 (tidy and verify by 13.4). | DH-D-TOOLCHAIN-2; DH-ADR-TOOLCHAIN-8; DH-T-TOOLCHAIN-8 | `toolchain.md:246,380-382,539`; `gates.go` `Gates`; `architecture.md:162` | CONFIRMED | DECIDE (M1-D-GATE-23) | P1 | [13.5](gap-closure-plan.md) |
| M1-G-GATE-24 | The wait and release rules have blind spots: `<-time.After` settles pass, any `t.Cleanup` satisfies the analyzer, and test-kit helpers are unchecked. | QG-4, QG-5 | `analyzers/nosleep.go`, `analyzers/cleanup.go`; `src/go/cordis/unload_test.go:213` | CONFIRMED | DECIDE (M1-D-GATE-24) | P0 | [13.1](gap-closure-plan.md) |
| M1-G-GATE-25 | e2e-tagged godog tests are never vetted, so a whole test layer escapes QG-4 and QG-5. Same as M1-G-TOOL-17; closed by 13.5. | QG-2; `vetGate` | 66 files in `src/harness/test/e2e/harness-DSH/`; sleeps e.g. `..._deadlines_and_result_budget_test.go:114` | CONFIRMED | DECIDE (M1-D-GATE-25) | P2 | [13.5](gap-closure-plan.md) |
| M1-G-GATE-26 | The coverage gate floors every package of all modules, `main` packages included, stricter than the spec. | DH-ADR-TOOLCHAIN-7 | `toolchain.md:236`; `gates.go` `coverageGate` | CONFIRMED | RECORD | P2 | [12.2](gap-closure-plan.md) |
| M1-G-GATE-27 | The `inherited-vet-gate-failure` record does not cover the findings DH-S-8, -10 and -11 added, so the evidence understates the debt. | `evidence/DH-S-9.md:110-121` | same | CONFIRMED | RECORD | P0 | [13.3](gap-closure-plan.md) (per-stage lines from [13.2](gap-closure-plan.md)) |

### 6.2 TOOL: the DH-S-1 toolchain built narrower than its spec

Spec sources: [toolchain.md](../../harness/upstream/toolchain.md),
[build-test-toolchain.md](../../harness/libs/build-test-toolchain.md), impl.md DH-S-1
(lines 419-462).

| ID | Gap | Where | Evidence | Verified | Disp. | Pri. | Closed by |
|---|---|---|---|---|---|---|---|
| M1-G-TOOL-1 | staticcheck is not a gate. Overlaps M1-G-GATE-23. | step 1.4; DH-D-TOOLCHAIN-2; DH-T-TOOLCHAIN-8 | `tools/harness-gate/gates.go:132-142`; `toolchain.md:246,255` | CONFIRMED | BUILD | P0 | [13.5](gap-closure-plan.md) |
| M1-G-TOOL-2 | No module-hygiene gate (tidy, verify, a reason comment per pin); the test named DH-T-TOOLCHAIN-11 proves a narrower contract. Overlaps M1-G-GATE-23. | step 1.1; DH-D-TOOLCHAIN-1; DH-T-TOOLCHAIN-11; RM-10 | `tools/harness-gate/modcheck/rules.go:10-125`; godog without a reason at `src/harness/go.mod:17`; `toolchain.md:273` | CONFIRMED | BUILD | P0 | [13.4](gap-closure-plan.md) |
| M1-G-TOOL-3 | The gate's own tests (modcheck, analyzers, preflight) are run by no gate and no CI job, so they can regress silently. | DH-T-TOOLCHAIN-8, -9, -11; DH-T-SDK-17 | `gates.go:115-128`; no workflow runs `go -C tools/harness-gate test` | CONFIRMED | BUILD | P0 | [13.4](gap-closure-plan.md) |
| M1-G-TOOL-4 | govulncheck is not a gate. Overlaps M1-G-GATE-23. | step 1.4; DH-D-TOOLCHAIN-2, -6 | `toolchain.md:246,255,457`; `build-test-toolchain.md:245` | CONFIRMED | DECIDE (M1-D-TOOL-1) | P0 (P1 if release-only) | [13.5](gap-closure-plan.md) |
| M1-G-TOOL-5 | There is no tools module, so linter versions are not pinned anywhere. | DH-D-TOOLCHAIN-1 | `tools/go.mod` absent; `toolchain.md:371-372` | CONFIRMED | DECIDE (M1-D-TOOL-2) | P0 | [13.4](gap-closure-plan.md) |
| M1-G-TOOL-6 | Three toolchain-owned analyzers are missing (`Context.Root()` callers, discarded errors, unchecked assertions and `unsafe`); import rules are met by archtest and the HTTP rule belongs to util. | step 1.4; DH-T-TOOLCHAIN-8 | `tools/harness-gate/analyzers/analyzers.go:34`; `toolchain.md:165-167,246` | PARTLY | BUILD (+ RECORD) | P0 | [13.5](gap-closure-plan.md) (record [12.2](gap-closure-plan.md)) |
| M1-G-TOOL-7 | No site-level reasoned exception, and no test that an exception without a reason fails. Overlaps M1-G-GATE-23. | DH-T-TOOLCHAIN-8; DH-ADR-TOOLCHAIN-8 | `analyzers.go:16-17`; `gates_test.go:206`; `toolchain.md:539` | CONFIRMED | BUILD | P0 | [13.5](gap-closure-plan.md) |
| M1-G-TOOL-8 | The release rehearsal is a raw cross-compile with no archives, checksum file or replay, not a GoReleaser snapshot. | step 1.9; DH-D-TOOLCHAIN-5; DH-E-TOOLCHAIN-2 | `tools/harness-gate/rehearse.go:39-81`; `.goreleaser.yaml:14-27` | CONFIRMED | DECIDE (M1-D-TOOL-3) | P1 (before DH-S-18) | [19.1](gap-closure-plan.md) |
| M1-G-TOOL-9 | The `harness-replay` CI job and the replay gate are scheduled in no stage (an orphan requirement). | DH-D-TOOLCHAIN-4; DH-T-TOOLCHAIN-5 | `toolchain.md:423`; impl.md step 1.8 omits it | CONFIRMED | RECORD | P1 (DH-S-16) | [12.2](gap-closure-plan.md) (build [19.1](gap-closure-plan.md)) |
| M1-G-TOOL-10 | `src/go`, a published library, requires godog against "standard library only"; product packages are stdlib-only by import. | DH-S-2 Entry; RM-3, RM-10 | `src/go/go.mod:5` (#17); `ci.yml:79` comment now false | CONFIRMED | DECIDE (M1-D-TOOL-4) | P0 | [13.4](gap-closure-plan.md) |
| M1-G-TOOL-11 | The DH-S-1 narrowing (8-gate set, unchanged `release.yml`) has no recorded decision. | NG-11; DC-2 `tech-spec.md:211` | `implementation-plan.md:33,73`; `architecture.md:162,700` | CONFIRMED | RECORD | P0 | [12.2](gap-closure-plan.md) |
| M1-G-TOOL-12 | The three-OS gate becomes Windows plus WSL, and there is no procedure or evidence capture for a local lane. Overlaps M1-G-GATE-19, -20, -22, M1-G-CLOSE-12, -13. | DH-S-1 Result and Exit; QG-4, QG-10; DH-D-TOOLCHAIN-4 | `evidence/DH-S-1.md:102` (CI run 44670956416 at `e13abf3`) | CONFIRMED | BUILD (+ RECORD) | P0 | [12.3](gap-closure-plan.md) (record [12.2](gap-closure-plan.md)) |
| M1-G-TOOL-13 | The CI matrix and its pinning test still name macOS. Same as M1-G-GATE-21. | DH-D-TOOLCHAIN-4; DH-T-TOOLCHAIN-9 | `.github/workflows/ci.yml:130`; `harness-scenarios.yml`; `tools/harness-gate/scenarios_test.go:241` | CONFIRMED | DECIDE (M1-D-TOOL-5) | P0 | [12.3](gap-closure-plan.md) |
| M1-G-TOOL-14 | Local lanes do not run on the floor toolchain go1.26.0 (records show go1.27.4), so code needing Go 1.27 could pass. | RM-2 `tech-spec.md:226`; R-05 | `evidence/DH-S-1.md:9,57,100` | CONFIRMED | BUILD | P0 | [12.3](gap-closure-plan.md) |
| M1-G-TOOL-15 | The preflight hard-codes the DeepSeek key, a requirement OWNER-2 removed; the weekly cron fails without it. | step 1.8; QG-10 | `tools/harness-gate/acceptance/scenarios.go:272`; `harness-scenarios.yml:61,87`; `acceptance_test.go:216-219` | CONFIRMED | BUILD | P0 | [12.3](gap-closure-plan.md) |
| M1-G-TOOL-16 | Credentialed scenarios have no signed-in route or host: a hosted runner cannot hold a signed-in session. | DH-ADR-TOOLCHAIN-6, -12; DH-E-TOOLCHAIN-1 | `toolchain.md:554` | CONFIRMED (new) | DECIDE (M1-D-TOOL-6) | P1 (before DH-S-17) | [19.2](gap-closure-plan.md) |
| M1-G-TOOL-17 | e2e-tagged godog suites are never compiled by the gate. Same as M1-G-GATE-25. | DH-ADR-TOOLCHAIN-1 `toolchain.md:182` | `gates.go:45` `LayerTags` | CONFIRMED | BUILD | P2 | [13.5](gap-closure-plan.md) |
| M1-G-TOOL-18 | DH-S-1 paperwork does not match the build: no Stage 1.2 record, goldens' gate unstated, golden refusal depends on `CI` being set. | impl.md sec. 1.4 rule 3; EV-3 | `testkit/golden/golden.go:55` | CONFIRMED | RECORD | P2 | [12.2](gap-closure-plan.md) (`CI=1` in [12.3](gap-closure-plan.md)) |

### 6.3 FND: loader and leaf packages (DH-S-3, DH-S-4)

A `DH-T` named in a DH-S-3 or DH-S-4 "Done when" line is P0 even if only an M2 stage uses
the behaviour. Behaviour that only a spec's "How" describes is P1 for its first consumer.

| ID | Gap | Where | Evidence | Verified | Disp. | Pri. | Closed by |
|---|---|---|---|---|---|---|---|
| M1-G-FND-1 | The layer write guard refuses a stale write instead of re-reading and merging three ways inside the lock; DH-T-LOADER-15's third clause is unproven. A proof gap, not live data loss: the harness edit path does not use `LayerSet`. | steps 3.9, 3.5; DH-D-LOADER-13, -7; DH-T-LOADER-15 | `src/go/loader/layerset.go:540-548`; `layers_test.go:395-417` | CONFIRMED | BUILD | P0 | [14.1](gap-closure-plan.md) |
| M1-G-FND-2 | The story plan and DH-S-3 evidence describe the refusal as the requirement. | plan line 169; `evidence/DH-S-3.md:73` | documents | CONFIRMED | RECORD | P0 | [14.1](gap-closure-plan.md) |
| M1-G-FND-3 | DH-T-LOADER-14 is not proven for the layer source (`AcceptUpdate` entry not restarted; anonymous neighbour keeps its fiber). | step 3.5; DH-D-LOADER-7; DH-T-LOADER-14 | `src/go/loader/layers_test.go:468-480`; `loader_test.go:249-254` (ConfigFile only) | CONFIRMED / PARTLY | BUILD | P0 | [14.1](gap-closure-plan.md) |
| M1-G-FND-4 | Apply, reload and unload are not written to the `loader` logger; only an in-memory log exists. | step 3.7; DH-D-LOADER-8 | `src/go/loader/loader.go:72-78,385,800-810` | PARTLY | BUILD | P2 | [14.1](gap-closure-plan.md) |
| M1-G-FND-5 | The debounce uses its own `time.Timer`, not `timer.Debounce`; behaviour is equivalent. | step 3.8; DH-D-LOADER-11 | `src/go/loader/layerset.go:1075-1081` | CONFIRMED | DECIDE (M1-D-FND-3) | P2 | [14.5](gap-closure-plan.md) |
| M1-G-FND-6 | The JSON-RPC codec has no write-stall deadline, and its option names differ from sec. 6.1; DH-T-SDK-4 cannot pass. | step 4.1; DH-D-SDK-1; DH-T-SDK-4 | `src/sdk/jsonrpc/conn.go:30-50,267` | CONFIRMED | BUILD | P0 | [14.2](gap-closure-plan.md) |
| M1-G-FND-7 | The codec has no one-shot mode for the tool endpoint. | DH-D-SDK-1 "How" | `src/sdk/jsonrpc/conn.go` | CONFIRMED | BUILD | P1 (DH-S-26) | [14.2](gap-closure-plan.md) |
| M1-G-FND-8 | There is no fake-peer test kit for `jsonrpc`. | DH-D-SDK-1 "Components" | `src/sdk/jsonrpc/conn_test.go` | CONFIRMED | BUILD | P1 (DH-S-15) | [14.2](gap-closure-plan.md) |
| M1-G-FND-9 | Nothing negotiates the protocol version or gates calls on `initialize`; DH-T-SDK-7 is absent. | step 4.3; DH-D-SDK-2; DH-T-SDK-7; DH-ADR-SDK-3 | `src/sdk/protocol/methods.go:94-116`; `types.go:46,68` | CONFIRMED | BUILD | P0 | [14.3](gap-closure-plan.md) |
| M1-G-FND-10 | No generic handler-registration helper, so DH-T-SDK-6's two-way check is half proven. | DH-D-SDK-2; DH-T-SDK-6; DH-ADR-SDK-2 | `src/sdk/protocol/schema_test.go:73-104` | PARTLY | BUILD | P0 (real handler set P1) | [14.3](gap-closure-plan.md) |
| M1-G-FND-11 | The protocol schema is derived inside `src/sdk`, not by the harness `protoschema` with `schema.Of`; two derivations can drift. | DH-D-SDK-2; DH-ADR-SDK-8 | `src/sdk/protocol/schema.go:14-30`; `implementation-plan.md:206` | CONFIRMED | DECIDE (M1-D-FND-1) | P1 (DH-S-15) | [14.3](gap-closure-plan.md) |
| M1-G-FND-12 | A `time.Duration` derives to integer nanoseconds, not `"30s"`, and that form is already persisted in fingerprinted events. | step 4.2; DH-D-UTIL-1; DH-T-UTIL-2 | `llm/vocab/failure.go:129`; `llm/vocab/testdata/fingerprints.golden:9`; `llm/retry.go:50-58`; `agent/driver/budget.go:34` | CONFIRMED | BUILD | P0 | [14.4](gap-closure-plan.md) |
| M1-G-FND-13 | A tagged union does not derive to `oneOf`. | DH-T-ZOD-8; DH-LIB-ZOD-R9 | no `Union`; `schema/validate.go:317` | CONFIRMED | BUILD | P0 | [14.4](gap-closure-plan.md) |
| M1-G-FND-14 | The model-subset check lives in `vocab`, not as `schema.CheckSubset`. | DH-T-UTIL-1 | `llm/vocab/controls.go:192` | CONFIRMED | BUILD | P0 | [14.4](gap-closure-plan.md) |
| M1-G-FND-15 | There is no `Tolerant` option, so an old build cannot read a newer persisted record. | DH-T-ZOD-5 | `schema/schema.go:36-42` | CONFIRMED (new) | BUILD | P0 | [14.4](gap-closure-plan.md) |
| M1-G-FND-16 | There is no `Changed` or `Fields`; `storage/settings` re-derives live fields by hand. | DH-T-UTIL-2; DH-ADR-SCHEMA-5 | `storage/settings/settings.go:283-315` | CONFIRMED | BUILD | P0 | [14.4](gap-closure-plan.md) |
| M1-G-FND-17 | There is no `DecodeForm` or `Project`, though an M1 "Done when" line names them. | DH-T-UTIL-2 | absent | CONFIRMED | DECIDE (M1-D-FND-2) | P0 (P2 if moved) | [14.4](gap-closure-plan.md) |
| M1-G-FND-18 | There is no `schema.Locate`. | `util-test-support.md:176` | absent | CONFIRMED (new) | BUILD | P1 (DH-S-34) | [14.4](gap-closure-plan.md) |
| M1-G-FND-19 | Package `llm` does not alias the vocabulary types; consumers import `vocab`. | DH-D-LLM-11 | no `type X = vocab.X` in `llm/*.go` | CONFIRMED | DECIDE (M1-D-FND-3) | P2 | [14.5](gap-closure-plan.md) |
| M1-G-FND-20 | Catalog families carry no owning area. | DH-D-BOOT-1 | `boot/catalog/families.go:8`; `evidence/DH-S-4.md:102-104` | CONFIRMED | BUILD | P2 | [14.5](gap-closure-plan.md) |
| M1-G-FND-21 | Leaf tests carry no `DH-T` IDs and clause coverage is unaudited (e.g. FS-1's two-process clause has no test). Overlaps M1-G-CLOSE-9. | steps 4.1-4.3, 4.6-4.9; DH-D-SDK-1, -2, UTIL-1, -3, FS-1..3 | no `DH-T-` in `src/sdk/jsonrpc`, `src/sdk/protocol`, `schema`, `util/{fsx,textdiff,search,watch}` | CONFIRMED | BUILD | P0 (FS-27 tool clauses P1) | [14.5](gap-closure-plan.md) |
| M1-G-FND-22 | The DH-S-4 evidence has no test mapping for steps 4.1-4.9. Overlaps M1-G-CLOSE-5. | impl.md sec. 7.3 | `evidence/DH-S-4.md:3-7,105-107` | CONFIRMED | RECORD | P0 | [14.5](gap-closure-plan.md) |
| M1-G-FND-23 | The two DH-S-4 manual checks (watcher with real editors, search against a real repository) are pending. Overlaps M1-G-CLOSE-6. | DH-S-4 "Manual checks"; EV-3 | `evidence/DH-S-4.md:73-74` | CONFIRMED | RECORD | P0 | [14.5](gap-closure-plan.md) |

### 6.4 BOOT: boot, launch, egress, secrets and test kit (DH-S-6)

Process-tree ownership, in short: the harness owns every program it starts and their
descendants (the *tree*); the operating-system *owner* is a job object (Windows), a
control group (Linux) or, as a weak fallback, a process group, which a child can leave
with `setsid`. The harness may stop only trees it created, and a detached launch (the
browser) is created but never owned. Ownership is separate from access (who may use a
process handle) and from trust (whether a model started the tree).

| ID | Gap | Where | Evidence | Verified | Disp. | Pri. | Closed by |
|---|---|---|---|---|---|---|---|
| M1-G-BOOT-1 | `retain` (one way to cut text) and the code frame (source line with caret) are missing. | step 6.1; DH-D-UTIL-5; DH-T-UTIL-9, DH-T-CODEFRAME-1 | no `util/retain`; `util/herr/herr.go`; `storage/spill/policy/policy.go:147`; `implementation-plan.md:339` | CONFIRMED | BUILD | P0 | [14.6](gap-closure-plan.md) |
| M1-G-BOOT-2 | The spawn package is a subset of DH-D-EXEC-1: no `Run`, private channels, `Detach`, batch opt-in or in-job creation. | step 6.2; DH-D-EXEC-1; DH-T-EXECA-1..3, NODEPROC-2, -8, -10, CROSSSPAWN-1 | `world/subprocess/subprocess.go:65-79,184`; `owner_windows.go:44-60` | PARTLY (wider than claimed) | BUILD (subject to M1-D-BOOT-1) | P1 (DH-S-13) | [19.3](gap-closure-plan.md) (batch opt-in moved to [16.5](gap-closure-plan.md)) |
| M1-G-BOOT-3 | Linux ownership never asks for a delegated scope; no owners runtime file; two stop rungs instead of five. | step 6.3; DH-D-EXEC-2; DH-T-EXEC-4, -5; U-K1-1 | `world/subprocess/owner_linux.go:24,80-86`; `subprocess.go:248` | CONFIRMED | BUILD (subject to M1-D-BOOT-1) | P1 (DH-S-13) | [19.4](gap-closure-plan.md) |
| M1-G-BOOT-4 | No trust mark, `PeerTree` or `Limits`. | step 6.3; DH-T-EXEC-17; U-K1-2 | `world/subprocess/subprocess.go:380-387` | CONFIRMED | BUILD (subject to M1-D-BOOT-1) | P1 (DH-S-15, -31) | [19.4](gap-closure-plan.md) |
| M1-G-BOOT-5 | The Linux process-group fallback sets no `Pdeathsig`, so a host crash leaves the whole tree running, direct child included. | step 6.3; DH-D-EXEC-2; DH-T-NODEPROC-7 | `world/subprocess/owner_unix.go:23-31`; `owner_linux.go:129,181-205` | CONFIRMED (read, not run) | BUILD | P0 | [14.6](gap-closure-plan.md) |
| M1-G-BOOT-6 | Named secret references never reach `BuildEnv`, DH-T-HUMAN-12 is missing, and nothing masks released secrets in tool results (SP-11). | step 6.6; DH-D-HUMAN-7; DH-T-HUMAN-12; SP-11 `tech-spec.md:280` | `world/subprocess/env.go:40-63`; `testkit/launcher/launcher.go:121`; `human/secret/secret.go:103`; `human/secret/store.go:216-219` | CONFIRMED | BUILD | P0 | [14.6](gap-closure-plan.md) (lease half [19.5](gap-closure-plan.md)) |
| M1-G-BOOT-7 | The rest of DH-D-HUMAN-7 is absent: managed store writes, `.env` source, leases and source checks. | step 6.6; DH-T-HUMAN-10, -11 | `human/secret/store.go`; `implementation-plan.md:357` | PARTLY | BUILD (subject to M1-D-BOOT-1) | P1 (DH-S-16) | [19.5](gap-closure-plan.md) |
| M1-G-BOOT-8 | No source check stops HTTP clients built outside egress; `transport.Over` is an unguarded seam. | steps 6.15, 6.16; DH-D-HOST-4, UTIL-2; DH-T-HOST-19, UNDICI-8 | `llm/transport/transport.go:72-78`; `host/sharing/sharing.go:85` | CONFIRMED / PARTLY | BUILD | P0 | [14.6](gap-closure-plan.md) |
| M1-G-BOOT-9 | DH-T-HOST-17 (a recorded run of each shipped profile) cannot be built in DH-S-6: its inputs arrive in DH-S-16 and M3. | step 6.15; DH-D-HOST-4; DH-T-HOST-17 | `docs/harness/areas/host-telemetry.md:483` | CONFIRMED (scheduling) | DECIDE (M1-D-BOOT-2) | P1 (DH-S-16) | [14.6](gap-closure-plan.md) (recorded as held open) |
| M1-G-BOOT-10 | Egress has no child-environment proxy overlay and no dial guard against private addresses. | step 6.16; DH-D-UTIL-2; DH-T-UTIL-5, UNDICI-7, -9 | `util/egress/egress.go`, `resolve.go` | PARTLY | BUILD | P1 (DH-S-13) | [19.3](gap-closure-plan.md) |
| M1-G-BOOT-11 | The test kit lacks `compose`, `egresstest` and `faultserver`, and its clock is not a synctest bubble. | step 6.17; DH-D-UTIL-6; DH-T-UTIL-12..15 | `testkit/clock/clock.go:24`; `tools/conform/conform.go:32` meets the conform row | PARTLY | BUILD (subject to M1-D-BOOT-1) | P1 (DH-S-12) | [19.5](gap-closure-plan.md) |
| M1-G-BOOT-12 | The usage-error exit status is 2 in code; the area spec and DH-T-CLI-1, -6 say 64. | step 6.9; DH-D-CLI-3 | `cli/output/status.go:15-21`; `docs/harness/areas/app-cli.md:244,251`; `implementation-plan.md:372,379` | CONFIRMED | DECIDE (M1-D-BOOT-3) | P0 | [14.6](gap-closure-plan.md) |
| M1-G-BOOT-13 | DH-S-6 tests carry almost no `DH-T` IDs (13 packages untagged). Overlaps M1-G-CLOSE-9. | every 6.x step; rule 2; sec. 7.3 | `world/subprocess`, `human/secret`, `cli`, `host/sharing`, `util/egress`, `testkit/*`, `archtest` | CONFIRMED | BUILD | P0 | [14.6](gap-closure-plan.md) |
| M1-G-BOOT-14 | DH-D-BOOT-3 lacks `profile reset` and the safe-mode launcher layer, so `--safe` does nothing. | step 6.7; DH-T-BOOT-8, -23 | `boot/launch/launch.go:84`; `boot/expr/expr.go:44` | PARTLY | BUILD (subject to M1-D-BOOT-1) | P1 (DH-S-16) | [19.5](gap-closure-plan.md) |
| M1-G-BOOT-15 | H-21 is held open as planned and must stay listed at closure. | step 6.12; DH-D-BOOT-6; H-21 `impl.md:3585` | `evidence/DH-S-6.md:277-280` | CONFIRMED (expected) | RECORD | P0 | [14.6](gap-closure-plan.md) (listed at [18.1](gap-closure-plan.md)) |
| M1-G-BOOT-16 | The ticks and "all seventeen built" overstate DH-S-6. Overlaps M1-G-CLOSE-2. | steps 6.1-6.3, 6.6, 6.7, 6.15-6.17 | `impl.md:669-685`; `evidence/DH-S-6.md:7` | CONFIRMED | DECIDE (M1-D-BOOT-1) | P0 | [14.6](gap-closure-plan.md) |
| M1-G-BOOT-17 | DH-S-6 evidence and plan need corrections: a false bash-tool claim, unticked boxes, silent narrowing, macOS rows and Linux rows from `ubuntu-latest`. Overlaps M1-G-CLOSE-5. | `evidence/DH-S-6.md`; plan Phase 6 | `DH-S-6.md:211-215,288-289`; `implementation-plan.md:339-423`; `.github/workflows/harness-manual-checks.yml:37` | CONFIRMED | RECORD | P0 | [14.6](gap-closure-plan.md) |

### 6.5 STORE: SQLite store, state and views (DH-S-7, DH-S-8)

A *view* (or fold) is state computed from the session log, for example the session list
summary or the event index. A stored view keeps a checkpoint row so a reader need not fold
the whole log. The spec requires a reader to be told whether a value is current, behind
or pending, and requires a view failure never to cost a log event.

| ID | Gap | Where | Evidence | Verified | Disp. | Pri. | Closed by |
|---|---|---|---|---|---|---|---|
| M1-G-STORE-1 | A layout upgrade is not refused while another instance is alive; an older running `dsh` is migrated underneath. | step 7.1; DH-D-STORE-2; DH-T-STORE-2; DH-ADR-STORE-5(a) | `session/store/sqlite/open.go:297,407-411`; no `migration-blocked` code | CONFIRMED | BUILD | P0 | [15.1](gap-closure-plan.md) |
| M1-G-STORE-2 | No backup before a migration, and each layout step commits on its own, so a failure leaves a half-upgraded file. | step 7.1; DH-T-STORE-2; `session-store.md:347-348` | `session/store/sqlite/open.go:451-471`; no `VACUUM INTO` | CONFIRMED | BUILD | P0 | [15.1](gap-closure-plan.md) |
| M1-G-STORE-3 | Two DH-T-STORE-2 clauses are untested: the refusal when WAL cannot be entered, and owner-only files. | step 7.1 | `open.go:397` (`CodeNoWAL`) | CONFIRMED (new) | BUILD | P0 | [15.1](gap-closure-plan.md) |
| M1-G-STORE-4 | The backlog bound has nothing to bound: appends commit synchronously, so DH-T-STORE-4's backlog clause cannot pass as written. | step 7.1; DH-T-STORE-4; `MaxBacklogBytes` | `session/store/sqlite/handles.go:177-257` | PARTLY | DECIDE (M1-D-STORE-A) | P0 | [15.1](gap-closure-plan.md) |
| M1-G-STORE-5 | DH-T-STORE-3 and -4 are untagged, and three clauses are untested (unusable after a failed rollback, retry after a failed commit, fork equals parent prefix). | step 7.1 | `open.go:496` (`CodeUnusable` untested) | CONFIRMED | BUILD | P0 | [15.1](gap-closure-plan.md) |
| M1-G-STORE-6 | DH-T-STORE-18 measures only the append: no 100k-event open, fork, heavy-tailed list, memory-limited rerun or bench corpus. | step 7.1; `libs/sqlite.md` M2, M4 | `session/store/sqlite/budget_test.go:33` | CONFIRMED | BUILD | P0 | [15.1](gap-closure-plan.md) |
| M1-G-STORE-7 | Nobody owns the budget numbers or DH-T-VIEW-25's search and `TreeMessages` clauses, so step 8.5 can never be ticked. | DH-T-STORE-18, VIEW-15, VIEW-25 | none | CONFIRMED (new) | DECIDE (M1-D-STORE-B) | P0 | [15.3](gap-closure-plan.md) (budgets also [15.1](gap-closure-plan.md)) |
| M1-G-STORE-8 | DH-S-7 evidence defers tests to later stages that never recorded them, yet its closure table says "met". Overlaps M1-G-CLOSE-5. | DH-S-7 exit evidence | `evidence/DH-S-7.md:162-168,327-333` | PARTLY | RECORD | P0 | [15.1](gap-closure-plan.md) |
| M1-G-STORE-9 | The driver bypasses a mounted checkpoint policy; the step end runs no checkpoint. | step 7.3; DH-D-STORE-4; DH-T-STORE-8 | `agent/driver/step.go:119-135,189-190` | CONFIRMED | BUILD | P2 | [15.1](gap-closure-plan.md) |
| M1-G-STORE-10 | A failed derived write rolls back its events, the opposite of the spec, so a fold defect ends the turn in error. | step 8.2; DH-D-VIEW-2; DH-T-VIEW-5, STORE-3 | `session/store/sqlite/handles.go:228-235`; `TestMultiEventAppendIsAtomic` asserts the opposite | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-11 | Views have no `Stored`/`Exact` freshness, no `view/behind` and no `pending`, so a reader is never told it holds stale state. | step 8.2; DH-T-VIEW-7; DH-ADR-VIEW-2 | `session/views/views.go:57-63,129-134`; `views/fold.go:305-351` | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-12 | Listing folds every listed session with no bound; one page can block on the largest log. | step 8.2 | `session/query/query.go:281-297` | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-13 | There are no cadences, so the event index is rewritten whole on every append: O(n^2) per session. | step 8.2; `CheckpointEveryEvents` | `session/views/views.go:35-51`; `fold.go:217-251`; `builtin.go:394-430` | CONFIRMED (new) | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-14 | Writing a derived row needs the session lease, so an unheld session's rows are never refreshed. | step 8.2; DH-T-VIEW-6 | `handles.go:228`; `session/live.go:185-232` | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-15 | Rebuilds have no background sweep, cannot resume and have no `Rebuild` API, so cold sessions stay stale. | step 8.2; DH-T-VIEW-6 | `session/views/fold.go:191-205` | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-16 | Nothing prunes unknown view keys, and `hosts` lacks `binary_version`. | step 8.2; DH-T-VIEW-8 | `session/store/sqlite/schema.go:79`; `implementation-plan.md:516` | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-17 | There is no `Status` showing the operator which folds are behind, pending or failed. | step 8.2 | none | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-18 | No property test that a checkpoint plus the suffix equals the full fold (DH-T-VIEW-1). | steps 8.2, 8.3 | `session/views/*_test.go` | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-19 | No golden log for each view (DH-T-VIEW-9). | step 8.3; DH-D-VIEW-3 | only `views/testdata/list_by_project.golden` | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-20 | No `DH-T-VIEW` IDs in code; DH-T-VIEW-8's fork and delete clauses are untested. Overlaps M1-G-CLOSE-9. | steps 8.2, 8.3 | `views/stored_test.go:73`; `views/views_test.go:175` | CONFIRMED | BUILD | P0 | [15.2](gap-closure-plan.md) |
| M1-G-STORE-21 | DH-T-VIEW-15 lacks the stable-paging test and the 10k benchmark. | step 8.5; DH-D-VIEW-5 | `session/query/visibility_test.go:284,363` | CONFIRMED | BUILD | P0 | [15.3](gap-closure-plan.md) |
| M1-G-STORE-22 | DH-T-VIEW-25 (first page over the heavy-tailed corpus; snapshot of a 100k-event session) does not exist. | step 8.5 | none | CONFIRMED | BUILD (scope per M1-D-STORE-B) | P0 | [15.3](gap-closure-plan.md) |
| M1-G-STORE-23 | DH-T-VIEW-13 and -14 exist but carry no ID. | step 8.5 | `session/query/query_test.go:106`; `visibility_test.go:42` | CONFIRMED | BUILD | P0 | [15.3](gap-closure-plan.md) |
| M1-G-STORE-24 | The built-in views are checkpoint documents, not the spec's derived tables (`session_summary`, `turn_index`, `event_class`, `event_link`). | step 8.3; DH-D-VIEW-3 | `session/store/sqlite/schema.go`; `views/builtin.go:97,179,254,383-396` | CONFIRMED (new) | DECIDE (M1-D-STORE-C) | P1 (DH-S-23) | [15.3](gap-closure-plan.md) |
| M1-G-STORE-25 | The DH-S-8 closure regressed in the paperwork: #65 unticked the Stage 8.2 steps, left their scenarios ticked, and the closure was never done. Overlaps M1-G-CLOSE-1, -3. | plan `:532,538-540`; `impl.md:778-780`; `test/harness/acceptance-index.yaml:30` | `git show dc986f8` (#65); `evidence/DH-S-8.md:105` | CONFIRMED | RECORD | P0 | [15.3](gap-closure-plan.md) |
| M1-G-STORE-26 | Ticks disagree with rule 2 in both directions (plan unticked though built; impl.md 7.1, 8.2, 8.7 ticked though tests missing). Overlaps M1-G-CLOSE-1. | plan 7.1, 7.2, 8.1; impl.md 7.1, 8.2, 8.7 | `implementation-plan.md:457-523`; `impl.md:728,777,782` | CONFIRMED | RECORD | P0 | [15.3](gap-closure-plan.md) |
| M1-G-STORE-27 | DH-T-STORAGE-1..7 carry no IDs, and the publish-then-kill loop of DH-T-STORAGE-7 is missing. | steps 8.1, 8.7 | `storage/blob`, `attachments` tests; `evidence/DH-S-8.md:23,45-66` | PARTLY | BUILD | P0 | [15.3](gap-closure-plan.md) |
| M1-G-STORE-28 | DH-D-STORAGE-3's draft record is not built, and the evidence reassigns it against the spec. | step 8.7; DH-T-STORAGE-7 | `evidence/DH-S-8.md:76-77`; `storage.md:427` | CONFIRMED (new) | DECIDE (M1-D-STORE-D) | P1 (DH-S-21) | [15.3](gap-closure-plan.md) |
| M1-G-STORE-29 | The specs name packages, a path and a writer goroutine that do not exist. | `session-store.md:171,202`; `session-views.md:203` | code: `session/store/sqlite`, `<home>/dsh.db`, synchronous appends | CONFIRMED (new) | RECORD | P2 | [15.3](gap-closure-plan.md) |
| M1-G-STORE-30 | POSIX-only store and storage tests have no lane under OWNER-1, so they never run. | DH-T-STORE-7; `TestSpillFilesArePrivate`; `TestPlantedLinkRefused` | `evidence/DH-S-8.md:47,53` | CONFIRMED | BUILD | P0 | [15.3](gap-closure-plan.md) |
| M1-G-STORE-31 | The store's platform wording still names macOS. Overlaps M1-G-GATE-19, M1-G-CLOSE-12. | DH-D-STORE-2 Done-when; DH-T-STORE-7; impl.md 16.8 | `session-store.md:459` | CONFIRMED | RECORD | P0 | [15.3](gap-closure-plan.md) |

### 6.6 PIPE: provider, retry, signed-in routes (DH-S-9) and tool pipeline (DH-S-10)

| ID | Gap | Where | Evidence | Verified | Disp. | Pri. | Closed by |
|---|---|---|---|---|---|---|---|
| M1-G-PIPE-1 | There is no `spill_read` tool, so the model sees a 64 KB preview and cannot read the rest. | step 10.6; DH-D-STORAGE-6; DH-T-STORAGE-13 | no `storage/spill/tool`; `storage.md:450-454` | CONFIRMED | BUILD | P0 | [16.3](gap-closure-plan.md) |
| M1-G-PIPE-2 | The result budget ignores images (latent: nothing produces images at M1). | step 10.6; DH-T-STORAGE-11 | `storage/spill/policy/policy.go:129-158` | CONFIRMED | BUILD | P0 | [16.3](gap-closure-plan.md) |
| M1-G-PIPE-3 | No approval policy (`ask`/`never`), no `approval/policy` fold and no prompt section, so an unattended run cannot be told "never ask". | step 10.3; DH-D-HUMAN-2; DH-T-HUMAN-3 | `human/approval/approval.go:182-279` | CONFIRMED | BUILD | P0 | [16.1](gap-closure-plan.md) |
| M1-G-PIPE-4 | An approval open at a crash is never closed: the audit keeps an unanswered request and the model is told "outcome unknown" for a call that never ran. | step 10.3; DH-T-HUMAN-4 | `human/approval/grants.go:374-401`; `tools/core/pipeline.go:261-264`; `session/surface/brackets.go:186-188` | CONFIRMED | BUILD | P0 | [16.1](gap-closure-plan.md) |
| M1-G-PIPE-5 | The approval outcome vocabulary (4 vs 5), default timeout (5 min vs wait) and audit fields (no peer) differ from the spec, by a documented story choice. | step 10.3; DH-T-HUMAN-3 | `approval.go:31-40,52,98-101`; `tools/errors.go:55-60`; `implementation-plan.md:626` | CONFIRMED | DECIDE (M1-D-PIPE-1) | P0 | [16.1](gap-closure-plan.md) |
| M1-G-PIPE-6 | The pipeline passes no approval subject, so "allow for this session" silently creates no grant. | steps 10.4, 10.10; DH-D-TOOLS-2, HUMAN-3 | `tools/pipeline.go:242-262`; `tools/core/pipeline.go:321-322` | CONFIRMED | BUILD | P1 (DH-S-12) | [16.2](gap-closure-plan.md) |
| M1-G-PIPE-7 | Parts of the guarded pipeline are missing or untested: `tools.Settled`, `Block`, panicking listeners, order permutation, output-schema violation. | step 10.4; DH-T-TOOLS-3..7 | `tools/pipeline.go:318`; `tools/core/pipeline.go:427-443` | CONFIRMED | BUILD | P0 | [16.2](gap-closure-plan.md) |
| M1-G-PIPE-8 | `DH-T` tags are missing or wrong: TOOLS-3 and -4 swapped, TOOLS-6 on the TOOLS-7 test, five IDs untagged. Overlaps M1-G-CLOSE-9. | `tools.md:468-472`; `interaction-credentials.md:505-506` | `tools/core/pipeline_test.go:214,238`; `deadline_test.go:380`; `implementation-plan.md:634,636` | PARTLY | RECORD | P0 | [16.1](gap-closure-plan.md), [16.2](gap-closure-plan.md) |
| M1-G-PIPE-9 | The retry listener does its own waiting, against the decision contract: two clocks, a blocked around-chain, and a cancel reported as the provider failure. | step 9.2; DH-D-LLM-3, AGENT-4 | `llm/retry/retry.go:246-263`; `agent/driver/step.go:236-244`; `llm.md:435-436` | PARTLY | DECIDE (M1-D-PIPE-2) | P1 (DH-S-16) | [16.4](gap-closure-plan.md) |
| M1-G-PIPE-10 | No composition mounts retry, and the route's retry policy never reaches it, so a 429 in a streamed turn ends the turn. | step 9.2; DH-D-LLM-3 | `llm/retry/retry.go:85-94`; `agent/f2_test.go:167-231`; `step.go:230` | CONFIRMED | BUILD | P1 (DH-S-16) | [16.4](gap-closure-plan.md) |
| M1-G-PIPE-11 | The DH-S-9 manual probe cannot prove "no retry on a streamed turn": it is buffered, vacuous, retries and prints the request id unmasked. | DH-S-9 manual check `impl.md:826-827` | `llm/manual_deepseek_test.go:49,72-98` | CONFIRMED | BUILD | P0 | [16.5](gap-closure-plan.md) |
| M1-G-PIPE-12 | There is no signed-in model route for Claude or Copilot, so neither the DH-S-9 check nor M1 can run under OWNER-2. Overlaps M1-G-CLOSE-12, M1-G-LOOP-12. | DH-S-9 manual check; DH-D-LLM-2; EV-6; tech-spec sec. 5.5 | only `llm/provider/deepseek` exists | CONFIRMED (new) | DECIDE (M1-D-PIPE-3, -4) | P0 | [16.5](gap-closure-plan.md) |
| M1-G-PIPE-13 | Keep or delete the DeepSeek provider code, which fixes how step 9.1 is ticked. | step 9.1; DH-D-LLM-2 | `llm/provider/deepseek`; `llm/provider_test.go:342` | CONFIRMED | DECIDE (M1-D-PIPE-5) | P0 | [16.5](gap-closure-plan.md) |
| M1-G-PIPE-14 | DH-S-9 and DH-S-10 closure paperwork is wrong or missing: DH-S-10 says "closed", tables and reviews are missing, scenarios target DeepSeek. Overlaps M1-G-CLOSE-1, -4, -14. | `evidence/DH-S-9.md`, `DH-S-10.md`; plan Phases 9-10 | `DH-S-10.md:9`; `DH-S-9.md:86-89`; `implementation-plan.md:571-573` | CONFIRMED | RECORD | P0 | [16.6](gap-closure-plan.md) |

### 6.7 LOOP: agent loop (DH-S-11)

| ID | Gap | Where | Evidence | Verified | Disp. | Pri. | Closed by |
|---|---|---|---|---|---|---|---|
| M1-G-LOOP-1 | The driver never assembles the prompt, so every request goes out with no system prompt. | step 11.3; DH-D-AGENT-3; DH-T-AGENT-7, -8; `agent-loop.md` sec. 6.2 point 4 | `agent/driver/step.go:35-50`; `driver.go:38-66`; `lifecycle.go:24-35` | CONFIRMED | BUILD | P0 | [17.1](gap-closure-plan.md) |
| M1-G-LOOP-2 | Admission is two appends, not one batch; the header lacks fingerprint and manifest; `toolOrder` is ignored; tool changes are never announced. | step 11.3; DH-ADR-AGENT-2; DH-ADR-TOOLS-8 | `step.go:86,179,276-294`; `session/event/core.go:153-157`; `agent/driver/toolset.go:41-49` | CONFIRMED | BUILD | P0 (route variants P1) | [17.1](gap-closure-plan.md) |
| M1-G-LOOP-3 | The first system prompt cannot be surface node 0, because the claim logs the user message first. | step 11.3; DH-D-AGENT-2, -3 | `agent/inbox.go:444-462`; `session/surface/history.go:208-211`; `llm/wire/anthropic/encode.go:107-108` | CONFIRMED (new) | DECIDE (M1-D-LOOP-1) | P0 | [17.1](gap-closure-plan.md) |
| M1-G-LOOP-4 | An unchanged `request/header` is appended again at step 2 (raw-byte schema comparison suspected). | step 11.3 | `agent/testdata/f2_log.golden` line 15 equals line 6; `step.go:296-301` | CONFIRMED (symptom) | BUILD | P0 | [17.1](gap-closure-plan.md) |
| M1-G-LOOP-5 | DH-T-AGENT-7, the three-step golden, does not exist; the tagged test runs two steps with no golden. | step 11.3; DH-T-AGENT-7 | `agent/driver/driver_test.go:259-279` | CONFIRMED | BUILD | P0 | [17.1](gap-closure-plan.md) |
| M1-G-LOOP-6 | DH-T-AGENT-28 has only a provisional budget, and no held-open entry covers it. | step 11.3; DH-T-AGENT-28 | `agent/driver/continuation_bench_test.go` | CONFIRMED | DECIDE (M1-D-LOOP-2) | P0 | [17.4](gap-closure-plan.md) (bench re-run [17.1](gap-closure-plan.md)) |
| M1-G-LOOP-7 | The M1 composition mounts neither the prompt component nor the context pipeline. | steps 11.3, 11.7; DH-D-AGENT-3, CONTEXT-1 | `agent/f2_test.go:16-36,167-231` | CONFIRMED | BUILD | P0 | [17.1](gap-closure-plan.md) |
| M1-G-LOOP-8 | DH-D-SUBAGENT-1's required tests are not written, and some behaviour is missing (grandchild wait, time limit, notices, capability check). | step 11.6; DH-T-SUBAGENT-1..4, -9, -14 | `delegate/subagent/manager.go:193-197`; `subagent_test.go` untagged | CONFIRMED | DECIDE (M1-D-LOOP-3) + BUILD | P0 | [17.2](gap-closure-plan.md) |
| M1-G-LOOP-9 | The context pipeline has no compaction slot, and DH-T-CONTEXT-2's re-entry clause is untested. | step 11.7; DH-D-CONTEXT-1; DH-T-CONTEXT-2 | `context/request/pipeline.go:75-84,213-260` | CONFIRMED | DECIDE (M1-D-LOOP-4) | P0 | [17.3](gap-closure-plan.md) |
| M1-G-LOOP-10 | DH-T-CONTEXT-1 covers one request, not a recorded log with every observation forbidden. | step 11.7; DH-T-CONTEXT-1 | `context/request/pipeline_test.go:23-76` | PARTLY | BUILD | P0 | [17.3](gap-closure-plan.md) |
| M1-G-LOOP-11 | The context `Output` lacks `After` and `Reword`. | step 11.7 | `context/request/pipeline.go:63-65` | CONFIRMED | BUILD | P1 (DH-S-25) | [17.3](gap-closure-plan.md) |
| M1-G-LOOP-12 | The M1 manual check is bound to DeepSeek and has not run. Overlaps M1-G-CLOSE-6, -12; depends on M1-G-PIPE-12. | DH-S-11 manual check; tech-spec sec. 2.3 | `agent/m1_manual_test.go:30-72`; `evidence/DH-S-11.md:46-106` | CONFIRMED | BUILD | P0 | [17.4](gap-closure-plan.md) |
| M1-G-LOOP-13 | The seven DH-S-11 scenario files need a DeepSeek key, and their review is not recorded. Part of M1-G-CLOSE-14. | DH-S-11 Scenarios; rule 4 | `testkit/scenarios/agent_budget_scenario_test.go:38` | CONFIRMED | BUILD (+ RECORD) | P0 | [17.4](gap-closure-plan.md) |
| M1-G-LOOP-14 | Step ticks for 11.3-11.7 disagree across impl.md, the story plan and the evidence. Overlaps M1-G-CLOSE-1. | impl.md 11.3-11.7; plan 11.2-11.4 | `evidence/DH-S-11.md:9-13` | CONFIRMED | RECORD | P0 | [17.4](gap-closure-plan.md) |
| M1-G-LOOP-15 | The turn-budget defaults are recorded as both confirmed and unconfirmed. | impl.md sec. 9.1; tech-spec sec. 5.5; DC-4 | `agent/driver/budget.go:8-14`; `tech-spec.md:300` | PARTLY | DECIDE (M1-D-LOOP-5) | P0 | [17.4](gap-closure-plan.md) |
| M1-G-LOOP-16 | impl.md ticks step 11.6, which is not built. Same family as M1-G-LOOP-14. | `impl.md:913` | see M1-G-LOOP-8 | CONFIRMED | RECORD | P0 | [17.4](gap-closure-plan.md) |

### 6.8 CLOSE: bookkeeping, traceability and the closure path

| ID | Gap | Where | Evidence | Verified | Disp. | Pri. | Closed by |
|---|---|---|---|---|---|---|---|
| M1-G-CLOSE-1 | The three tick sources (impl.md, story plan, evidence) disagree, some rows are inverted, and 108 of 135 local scenario boxes are unticked. | steps 1.1-11.7; rule 2 `impl.md:90` | `impl.md:440-914`; `implementation-plan.md` | PARTLY | RECORD | P0 | [18.1](gap-closure-plan.md) |
| M1-G-CLOSE-2 | Ticks were made under a red gate and without their named tests. | rule 2 | `evidence/DH-S-4.md:104`; `DH-S-9.md:108-121`; `DH-S-1.md:120` | CONFIRMED | DECIDE (M1-D-CLOSE-1) | P0 | [18.1](gap-closure-plan.md) |
| M1-G-CLOSE-3 | `built-through` is moved by hand, is stuck at DH-S-7, and nothing ties it to the ticks. | DH-D-TOOLCHAIN-4; step 1.8 | `test/harness/acceptance-index.yaml:15-16,30`; `tools/harness-gate/acceptance/index.go:155-162` | CONFIRMED | BUILD (+ RECORD) | P0 | [13.6](gap-closure-plan.md) (raise at [18.1](gap-closure-plan.md)) |
| M1-G-CLOSE-4 | The entry rule was broken: DH-S-10 and DH-S-11 were built before DH-S-9 closed, and DH-S-9 before DH-S-8, with no recorded decision. | rule 1; `impl.md:846,896` | `evidence/DH-S-10.md:74-77`; `DH-S-9.md:123` | CONFIRMED | RECORD | P0 | [12.1](gap-closure-plan.md) |
| M1-G-CLOSE-5 | Evidence records miss sec. 7.3 fields (commit, gate output, run ids, cleanup), so no stage can be repeated by a reviewer. | impl.md sec. 7.3 `:3187-3202`; EV-7 | `evidence/DH-S-2.md` to `DH-S-11.md` | CONFIRMED | RECORD | P0 | [12.4](gap-closure-plan.md) (filled at [18.1](gap-closure-plan.md)) |
| M1-G-CLOSE-6 | The manual-check register is incomplete: fewer than 20 of 103 steps have any record; S4, S9 and S11 are pending and S10 never ran. | rule 3; EV-3 | `evidence/DH-S-4.md:73-74`; `DH-S-9.md`; `DH-S-10.md`; `DH-S-11.md` | PARTLY | DECIDE (M1-D-CLOSE-2) + RECORD | P0 | [12.4](gap-closure-plan.md) (run at [18.1](gap-closure-plan.md)) |
| M1-G-CLOSE-7 | DH-E-EXEC-10 and DH-E-STORAGE-6 are not authored, though DH-S-6 owes them; EXEC-10 is the only proof of U-K1-1. | rule 4; sec. 7.5 `impl.md:3283`; `impl.md:692` | no `//dsh:scenario` for either; `e2e-scenarios.md:113` | CONFIRMED | BUILD | P0 | [13.6](gap-closure-plan.md) |
| M1-G-CLOSE-8 | The scenario-index gate cannot detect a rule-4 omission, so M2 stages can repeat it. | DH-D-TOOLCHAIN-4 | `tools/harness-gate/acceptance/scenarios.go:170-173` | CONFIRMED | BUILD | P1 (before DH-S-12 closes) | [13.6](gap-closure-plan.md) |
| M1-G-CLOSE-9 | 130 of the 259 required `DH-T` IDs are not in code, and nothing checks the informal tag convention. | rule 2; Done-when lines of 99 deliverables | grep over `src/` and `tools/` | CONFIRMED (upper bound) | BUILD | P0 | [13.6](gap-closure-plan.md) (gated at [18.1](gap-closure-plan.md)) |
| M1-G-CLOSE-10 | Operator decisions are scattered over five files, some are unnamed, and OWNER-1 and OWNER-2 are recorded nowhere. | DC-2; NG-11 | `DH-S-11.md:159-170`; `DH-S-9.md:74,86,120,130,133`; `DH-S-8.md:6,108`; `architecture.md:805` | CONFIRMED | RECORD | P0 | [12.1](gap-closure-plan.md) |
| M1-G-CLOSE-11 | Document inconsistencies: "106 steps" (actually 103), stale status table, 11.3 notes, H-21 without its closing step, schedule omitting two scenarios. | DC-3 | `tech-spec.md:355`; `impl.md:50-56`; `evidence/DH-S-6.md:279`; `e2e-scenarios.md:113` | CONFIRMED | RECORD | P0 (items 2, 3, 5 P2) | [12.1](gap-closure-plan.md) |
| M1-G-CLOSE-12 | The M1 DoD is not restated under OWNER-1 and OWNER-2; as written it requires a macOS lane and a DeepSeek key. Overlaps M1-G-GATE-19, M1-G-TOOL-12, M1-G-PIPE-12. | tech-spec sec. 2.3, QG-4, QG-10, EV-6, EN-3, A-2, A-3; M2 tech-spec `:121-132` | `.github/workflows/ci.yml:130`; `harness-manual-checks.yml:37`; `acceptance/scenarios.go:272` | CONFIRMED | DECIDE (M1-D-CLOSE-3) + RECORD | P0 | [12.1](gap-closure-plan.md) (checks run at [18.1](gap-closure-plan.md)) |
| M1-G-CLOSE-13 | No runbook or evidence format for the Windows-native and WSL lanes. Same as M1-G-GATE-22 and the evidence part of M1-G-TOOL-12. | OWNER-1; impl.md sec. 7.3 | `evidence/DH-S-1.md:96-104` | CONFIRMED | RECORD | P0 | [12.4](gap-closure-plan.md) |
| M1-G-CLOSE-14 | Thirteen authored scenarios use DeepSeek in their Given. Contains M1-G-LOOP-13 (seven files) and overlaps M1-G-PIPE-14 (three files). | rule 4; `llm.md:526,534,542` | 13 files in `testkit/scenarios/` | CONFIRMED | DECIDE (M1-D-CLOSE-4) | P0 (P1 if deferred) | [16.5](gap-closure-plan.md) (DH-S-9 files), [17.4](gap-closure-plan.md) (DH-S-11 files), [18.1](gap-closure-plan.md) |
| M1-G-CLOSE-9.1 | `DH-T-AGENT-14` has no test: no test asserts the frames of an extension point (one `Start`, dense indexes, one `End` after the record is committed, `abandoned` when the append fails). Found by 13.6.3. | step 5.11; DH-D-AGENT-4 Done-when (`agent-loop.md:448`); `agent-loop.md:521` | `trace-tests --stage DH-S-5`: missing at 5.11; no frame assertion under `internal/agent` | CONFIRMED | BUILD | P0 | before [18.1](gap-closure-plan.md) (18.1.4 makes `trace-tests` a gate) |
| M1-G-CLOSE-9.2 | `DH-T-VIEW-1` has no test: no property test shows that a checkpoint at any cut plus the suffix folds to the same state and rows as a full fold. Found by 13.6.3. | step 5.13; DH-D-VIEW-1; `session-views.md:516` | `trace-tests --stage DH-S-5`: missing at 5.13; `session/views/*_test.go` has no such property | CONFIRMED | BUILD | P0 | before [18.1](gap-closure-plan.md) (18.1.4) |
| M1-G-CLOSE-9.3 | `DH-T-VIEW-4` has no test: no import check of fold packages (I/O, clocks, randomness), no fold-twice determinism test, no slow-fold report. Found by 13.6.3. | step 5.13; DH-D-VIEW-1; `session-views.md:519` | `trace-tests --stage DH-S-5`: missing at 5.13; no `archtest` rule for fold packages | CONFIRMED | BUILD | P0 | before [18.1](gap-closure-plan.md) (18.1.4) |
| M1-G-CLOSE-9.4 | `DH-T-VIEW-3` is tagged on `TestRegistrationIsOnceAndBeforeUse` and `TestDispatchByTypeAndContainedPanic`, but its clause "a component that needs the registry fails to load without it" has no assertion. Found by 13.6.3. | step 5.13; `session-views.md:518` | `session/views/views_test.go` | CONFIRMED | BUILD | P0 | before [18.1](gap-closure-plan.md) (18.1.4) |
| M1-G-CLOSE-9.5 | `DH-T-HUMAN-2` is tagged on `TestCancelUnbindUnloadAndRefusals`, but its clause "an answer from the tool endpoint, the control endpoint or a plugin has no route" has no assertion. Found by 13.6.3. | step 5.12; `interaction-credentials.md:504` | `human/human_test.go` names no endpoint or plugin | CONFIRMED | BUILD | P0 | before [18.1](gap-closure-plan.md) (18.1.4) |

Sub-gaps M1-G-CLOSE-9.1 to 9.5 are parts of M1-G-CLOSE-9 and are not counted separately in the register totals. DH-S-2 has no missing `DH-T` ID after 13.6.3 (12 of 12 tagged).

### 6.9 Duplicates and overlaps

Duplicates keep both IDs; both rows close at the same stage. Overlaps are related gaps that
close separately and must not be counted as one proof.

| Topic | Gap IDs | Relationship | Closes in |
|---|---|---|---|
| Spec gates and exceptions missing | M1-G-GATE-23 = M1-G-TOOL-1, -2, -4, -7 | same | 13.5 (tidy and verify 13.4) |
| e2e layer not vetted | M1-G-GATE-25 = M1-G-TOOL-17 | same | 13.5 |
| CI matrix names macOS | M1-G-GATE-21 = M1-G-TOOL-13 | same | 12.3 |
| Local-lane evidence format | M1-G-GATE-22 = M1-G-TOOL-12 (evidence) = M1-G-CLOSE-13 | same | 12.4 (lane capture 12.3) |
| WSL lane provisioning | M1-G-GATE-20, M1-G-TOOL-12 (provisioning), M1-G-TOOL-14 | overlap | 12.3 |
| DeepSeek preflight | M1-G-TOOL-15 | single (also named in M1-G-CLOSE-12) | 12.3 |
| DoD restated for OWNER-1 and OWNER-2 | M1-G-GATE-19, M1-G-TOOL-12 (record), M1-G-STORE-31, M1-G-CLOSE-12 | overlap | 12.1, 12.2, 12.3, 15.3 |
| Ticks disagree | M1-G-CLOSE-1, M1-G-STORE-25, -26, M1-G-LOOP-14, -16, M1-G-PIPE-14 | overlap: each cluster fixes its stages; 18.1 reconciles all | 15.3, 16.6, 17.4, 18.1 |
| Ticks without named tests | M1-G-CLOSE-2, M1-G-BOOT-16 | overlap | 14.6, 18.1 |
| Untagged `DH-T` tests | M1-G-CLOSE-9, M1-G-FND-21, M1-G-BOOT-13, M1-G-STORE-20, -23, -27, M1-G-PIPE-8 | overlap: 13.6 builds the convention and checker; each cluster tags its own | 13.6, 14.5, 14.6, 15.2, 15.3, 16.1, 16.2 |
| Incomplete evidence records | M1-G-CLOSE-5, M1-G-FND-22, M1-G-BOOT-17, M1-G-STORE-8 | overlap | 12.4, 14.5, 14.6, 15.1, 18.1 |
| Pending manual checks | M1-G-CLOSE-6, M1-G-FND-23, M1-G-PIPE-11, M1-G-LOOP-12 | overlap | 12.4, 14.5, 16.5, 17.4, 18.1 |
| Signed-in route | M1-G-PIPE-12, M1-G-LOOP-12, M1-G-TOOL-16, M1-G-CLOSE-12 | overlap; one design (sec. 7.1) | 16.5, 17.4, 19.2, 12.1 |
| DeepSeek-bound scenarios | M1-G-CLOSE-14 contains M1-G-LOOP-13 and the three files of M1-G-PIPE-14 | overlap; the rest (including the two toolchain files) belong to CLOSE-14 only | 16.5, 17.4, 18.1 |
| Inherited vet failure record | M1-G-GATE-27, M1-G-CLOSE-2 | overlap | 13.3, 18.1 |
| `built-through` stuck | M1-G-CLOSE-3, M1-G-STORE-25 | overlap | 13.6, 15.3, 18.1 |
| Gate red | M1-G-GATE-1 and the 81 findings (M1-G-GATE-2 to -14) | M1-G-GATE-1 closes only after all of them | 13.1-13.3, 13.7 |

## 7. Decisions needed

Every DECIDE gap has a row here. The table is ordered by how much P0 work waits on the
answer. Each answer is recorded in `decisions.md` (plan Stage 12.1). Recommendations are
the clusters'; the owner decides.

| ID | Question | Options | Recommendation | What waits on it |
|---|---|---|---|---|
| M1-D-GATE-24 | What is the wait and release idiom for tests? (M1-G-GATE-24) | A strict `nosleep`; synctest waits inside a bubble, signals outside; one testkit poll helper for real processes; cleanup must unblock and join. B as A, plus `time.Sleep` allowed inside a bubble. C also flag bare `<-time.After` outside a bubble. | A | 13.1, 13.2, 13.3, then 13.7 and every later gate run (all 81 findings) |
| M1-D-PIPE-3 | How does the harness reach Claude and Copilot without an API key? (M1-G-PIPE-12) | A drive the user's installed official clients (`claude`, the Copilot CLI or SDK) as child processes; B extract the stored OAuth token and call vendor APIs (possible terms breach); C Anthropic API sign-in (Console account, Claude only). | A for both vendors; offer C later for full rebuild on Claude | 16.5, 16.6, 17.4, 18.1; 19.2 |
| M1-D-PIPE-4 | What do the harness guarantees mean on a client route? (M1-G-PIPE-12) | A boundary guarantee: log and rebuild the client invocation; declare the client's additions opaque; harness tools only via the guarded pipeline. B treat the client as a delegated child. C accept only token or API routes so SP-1 holds in full. | A, recorded as an owner scope change to SP-1, EV-6, DH-O-4 | 16.5, 17.1 (what the logged prompt means), 17.4, 18.1 |
| M1-D-CLOSE-3 | What does the M1 real-model check prove under OWNER-2? (M1-G-CLOSE-12) | A boundary route (as M1-D-PIPE-4 A); B extracted token, direct API; C external-agent route, real-model check proves reachability, brackets and cancel only. | A | 12.1 (DoD text), 12.4 (procedures), 16.5, 17.4, 18.1 |
| M1-D-PIPE-5 | Keep or delete the DeepSeek provider? (M1-G-PIPE-13) | A keep as keyless, fake-tested ("built, not proven against a real API"); B delete `provider/deepseek`, keep transport and wire. | A | 16.5; coverage triage in 13.7 (M1-G-GATE-16) |
| M1-D-PIPE-2 | Who waits before a retry? (M1-G-PIPE-9) | A the policy returns `Retry{After}` and the driver waits; new `RetryWaitEnded` notice. B the listener waits, as built; amend three specs. | A | 16.4, which is an entry of P0 stage 16.5 |
| M1-D-CLOSE-1 | What happens to ticks made before rule 2 held? (M1-G-CLOSE-2) | A untick now, re-tick at closure; B keep, record `m1-provisional-ticks`, re-verify all 103 at the closure commit. | B | 12.1, every cluster's tick steps (14.x to 17.x), 18.1 |
| M1-D-LOOP-1 | Where is the claimed user message logged, so the first system prompt can be node 0? (M1-G-LOOP-3) | A the claim writes only the splice; the user message is admitted at point 8. B write `system/message` at turn open before the splice. C accept a late system prompt. | A | 17.1, then 17.4 and the M1 record |
| M1-D-BOOT-1 | How does M1 treat DH-S-6 deliverables that were cut short? (M1-G-BOOT-16; covers BOOT-2, -3, -4, -7, -11, -14) | a build everything before M1; b hold each cut part open as `H-n` with a closing step in its consuming stage; c untick 6.2, 6.3, 6.6, 6.7, 6.15-6.17. | b | 14.6, 19.3-19.5, 18.1 |
| M1-D-TOOL-1 | Is govulncheck an M1 gate? (M1-G-TOOL-4) | a a gate now, as ADR-9 lists; b release-only at DH-S-46. | a | 13.5 |
| M1-D-GATE-23 | How are the gates the spec requires but the runner lacks handled, and site exceptions? (M1-G-GATE-23) | A record the M1 narrowing; hold staticcheck and tidy/verify open before DH-S-12, govulncheck for DH-S-46; build exceptions only when needed. B add all now. | A (conflicts with TOOL; see sec. 10) | 13.5 (records the M1 gate list) |
| M1-D-TOOL-5 | What does CI keep? (M1-G-TOOL-13) | a hosted ubuntu and windows `harness` lanes as merge checks, macOS dropped; b remove the `harness` job; c self-hosted runner with a WSL label. | a | 12.3 |
| M1-D-GATE-21 | What does CI keep? (M1-G-GATE-21; same question as M1-D-TOOL-5) | A ubuntu and windows, regression signal only; B windows only; C keep macOS non-blocking. | A | 12.3 |
| M1-D-TOOL-2 | Where is the tools module? (M1-G-TOOL-5) | a `tools/harness-gate/go.mod` becomes it; amend the spec path; b a new `tools/go.mod`, analyzers moved into it. | a | 13.4, 13.5 |
| M1-D-TOOL-4 | What happens to `src/go`'s godog dependency? (M1-G-TOOL-10) | A a nested test-only module `src/go/test/e2e`; B amend RM-3 to allow reasoned test-only requirements; C rewrite the six suites as plain Go tests. | A | 13.4 |
| M1-D-PIPE-1 | Which approval outcome vocabulary is authoritative? (M1-G-PIPE-5) | A the area spec's five outcomes, its tool codes, timeout 0 (wait), peer in the record; B keep the story's four and amend three specs. | A | 16.1, 16.2, 16.6 |
| M1-D-LOOP-6 | Which routes must M1 pass? (M1-G-LOOP-12) | A Claude must pass; Copilot recorded either way; B both must pass. | A (CLOSE.4 assumes both; see sec. 10) | 17.4, 18.1 |
| M1-D-CLOSE-4 | Re-author the 13 DeepSeek-bound scenarios before M1 closes? (M1-G-CLOSE-14) | A yes, swap route and credential Given and re-review; B record "Given superseded by OWNER-2" as open, re-author before DH-S-17. | A if M1-D-CLOSE-3 is decided early, else B | 16.5, 17.4, 18.1 |
| M1-D-CLOSE-2 | Does a scripted probe count as a manual check? (M1-G-CLOSE-6) | A yes, when a named person runs it at the recorded commit, watches it and records it `manual`; B only an interactive exercise counts. | A, plus the rule-3 default per uncovered step | 12.4, 18.1 |
| M1-D-STORE-A | What should DH-T-STORE-4's backlog clause assert? (M1-G-STORE-4) | a amend the spec: no backlog while appends are synchronous; test "durable once Append returns"; b build the group-commit writer with the 64 MiB bound. | a | 15.1 |
| M1-D-STORE-B | Where do the budget numbers come from, and who owns DH-T-VIEW-25's search clauses? (M1-G-STORE-7) | a provisional budgets from the first measured run, pinned at 16.7; search clauses to DH-D-VIEW-6 (DH-S-23); b hold open as a new `H-23`; c change nothing (8.5 never ticks in M1). | a | 15.1, 15.3 |
| M1-D-LOOP-2 | What is the DH-T-AGENT-28 budget? (M1-G-LOOP-6) | A pin 100 ms for M1 and hold open an `H-n` closing at 16.7; B keep 11.3 open until 16.7. | A | 17.4 |
| M1-D-LOOP-3 | What is the M1 scope of DH-T-SUBAGENT-3, -4 and -14? (M1-G-LOOP-8) | A build the grandchild wait and a minimal capability check now; B amend `subagent-team.md:434` so M1 keeps -1, -2, -9 and the M1 legs of -3; the rest move to DH-S-29. | B | 17.2 |
| M1-D-LOOP-4 | What does compaction need at M1? (M1-G-LOOP-9) | A build the hook only, plus a fake-compactor test; B move the re-entry clause of DH-T-CONTEXT-2 to step 25.3. | A | 17.3 |
| M1-D-LOOP-5 | Are the turn-budget defaults (500 / 8 / none) confirmed? (M1-G-LOOP-15) | confirm in writing, constants stand; or leave unconfirmed, move them into a `driver.Config` default and unexport. | confirm in writing | 17.4 |
| M1-D-BOOT-3 | What is the usage-error exit status? (M1-G-BOOT-12) | A 64, as the area spec and three tests say; B 2, correcting three spec rows under DC-2. | A (2 is also Go's panic status) | 14.6 |
| M1-D-FND-2 | When do `DecodeForm` and `Project` land? (M1-G-FND-17) | a build both now (about 50 lines each); b move those clauses of DH-T-UTIL-2 to DH-S-19 and DH-S-23. | a | 14.4 |
| M1-D-TOOL-1b | If staticcheck reports more than about 100 findings, may it land advisory-first? (sub-question of M1-D-TOOL-1) | yes; no, fix everything first (ADR-8). | no | 13.5 |
| M1-D-GATE-18 | Make the harness job a required check? (M1-G-GATE-18) | A a ruleset on `main` requiring the lanes kept by M1-D-GATE-21; B leave it advisory. | A | 13.7 (owner action in repository settings, once the gate is green) |
| M1-D-TOOL-6 | Where do credentialed scenarios run? (M1-G-TOOL-16) | a locally on the development host via a new `harness-gate scenarios --stage`; b a self-hosted runner logged in as the user. | a | 19.2 |
| M1-D-TOOL-3 | When does the rehearsal become GoReleaser? (M1-G-TOOL-8) | a now, without the replay check; b at plan Stage 19.1, before DH-S-18. | b; record D-TOOLCHAIN-5 as "built in part" | 19.1 |
| M1-D-BOOT-2 | Where does DH-T-HOST-17 run? (M1-G-BOOT-9) | a split HOST-4 into a new ID closing at DH-S-16; b hold it open as a new `H-n` closing at step 16.7; c keep it in DH-S-6 (impossible). | b | 14.6 |
| M1-D-FND-1 | Where is the protocol schema derived? (M1-G-FND-11) | a keep the SDK deriver and `schema.json`; add a harness test that `schema.ForType` derives every protocol type; b move derivation to `internal/wire/protoschema` now. | a | 14.3 |
| M1-D-STORE-C | Do built-in views stay checkpoint documents or become the spec's tables? (M1-G-STORE-24) | a keep documents with cadences; amend the specs; build tables at DH-S-23; b build the four tables now (layout 6). | a | 15.3 |
| M1-D-STORE-D | When is the draft record built? (M1-G-STORE-28) | a now; b hold open as `H-n` closing at DH-S-21's composer step; c move to DH-D-STORAGE-10 (step 49.7). | b | 15.3 |
| M1-D-FND-3 | Accept two small spec deviations: own debounce timer, no `llm` aliases? (M1-G-FND-5, -19) | a accept both and amend `cordis-plugins.md:163`, `llm.md:463`; b build both. | a | 14.5 |
| M1-D-GATE-25 | Vet the `e2e` layer? (M1-G-GATE-25) | A add `e2e` to the vet tags and fix 3 sleeps and 11 goroutine files; B record e2e as outside the gate. | A, but after M1 (conflicts with M1-G-TOOL-17 in 13.5; see sec. 10) | 13.5 |

### 7.1 The signed-in route design (M1-D-PIPE-3, -4, -5 and M1-D-CLOSE-3)

This is the decision that most limits what M1 can prove. Under OWNER-2 the real model is
reached through the user's own signed-in vendor client, with no API key. The safest and
recommended route (M1-D-PIPE-3 A) drives the official client as a child process: the
client authenticates, and the harness never reads a token. But that client adds its own
system text, context handling and loop. The guarantee that "the bytes sent equal the
request rebuilt from the log" (SP-1, DH-O-4) can then hold only at the harness-to-client
boundary. M1-D-PIPE-4 A and M1-D-CLOSE-3 A accept that, as a recorded owner scope change:
the logged and rebuildable unit becomes the client invocation (argv, stdin, environment
names without values, client version), the client's additions are declared opaque, and
keyed routes keep full SP-1 against fakes. The alternatives either carry legal risk
(token extraction) or move the M1 demonstration off the harness's own driver
(delegation). Two consequences reach other clusters. First, the vendor client becomes the
first production child process, so the secret-name fix (M1-G-BOOT-6) becomes a
prerequisite of plan Stage 16.5, and so may parts of M1-G-BOOT-2 and -10 (P1, Phase 19);
the plan already moves the batch opt-in of M1-G-BOOT-2 into 16.5 for npm-installed
clients on Windows. Second, the prompt work of plan Stage 17.1
(M1-G-LOOP-1) is honest on this route only if the client lets the harness supply the
whole system prompt and tool list and returns tool calls instead of running them.

### 7.2 The wait idiom (M1-D-GATE-24)

All 81 vet findings are fixed by one of a few patterns, and the analyzer accepts some
fixes that do not meet the rule (any `t.Cleanup` passes; `<-time.After` "settles" pass).
Unless the idiom is written down first, the repair of 77 test sites could satisfy the
analyzer and still leave timing-based proofs. Option A needs no analyzer change and
follows the precedent already in `cordis-sample`. Plan Stages 13.1 to 13.3 cannot start
until this is answered, and every later gate run (13.7, 18.1) waits on them.

### 7.3 Ticks made under a red gate (M1-D-CLOSE-1)

Rule 2 says a step is ticked only when its named tests pass under the gate. The gate has
been red since DH-S-2, and about half of the named tests are untagged, so every tick after
DH-S-1 is unproven. Option B keeps the ticks as provisional and makes the closure commit
the single place where each of the 103 ticks is confirmed; option A unticks everything now
and has each cluster re-tick. B avoids churn because sec. 2.3 already requires a
one-commit re-verification; under either option a step that fails rule 2 at the closure
commit stays unticked and M1 stays open.

### 7.4 The prompt order (M1-D-LOOP-1)

The spec wants the first system prompt at surface node 0, ahead of the user's message.
Today the inbox logs the claimed user message at turn start, before any prompt could be
admitted, so fixing M1-G-LOOP-1 alone would put the system prompt after the user message
on every first turn, with a wire diagnostic on every request. Option A moves the user
message into the one admission batch, as the spec and upstream do; its cost is an inbox
change and a defined crash outcome (a claimed but unadmitted message is reported
discarded, never run twice). The F2 golden changes under every option.

### 7.5 DH-S-6 deliverables cut short (M1-D-BOOT-1)

Seven DH-S-6 steps are ticked although parts of their deliverables were narrowed when the
story plan was written (spawn helpers, delegated Linux scopes, trust marks, secret store
writes, the composition test kit, safe mode). Their first consumers are M2 stages. Option
b records each part as a held-open item with a closing step in its consuming stage, the
mechanism impl.md already uses for H-21. That keeps M1 honest without pulling M2 work
forward; plan Stage 14.6 stays P0 under every option.

## 8. Claims refuted or corrected by the cluster pass

| Earlier claim | Finding | Source |
|---|---|---|
| Blob reads verify the digest too late. | **Refuted.** `blob.Open` verifies digest and length while streaming, exactly as `storage.md:202` says (`storage/blob/blob.go:442-457`). The duty sits with consumers, which must not act on bytes before EOF and `Close` return nil. | STORE |
| C-1: "14 circle members versus 17 built" is an inconsistency. | **Refuted.** impl.md sec. 10.2 lists 14 circle members; the three outside are EXEC-2, BOOT-3 and BOOT-6. Only sec. 3.2's wording "all of them" is loose. | BOOT |
| `conform` is missing from the test kit. | **Mostly refuted.** `tools/conform.Cancellation` (`tools/conform/conform.go:32`) is A03's own suite, as DH-ADR-UTIL-6 asks; archtest holds the import rules. | BOOT |
| The backlog bound is missing. | **Partly.** It is missing, but it cannot be reached by design: appends commit synchronously (M1-G-STORE-4). | STORE |
| `transport.Over` is a live egress bypass. | **Narrowed.** It has no production caller; it is an unguarded seam (M1-G-BOOT-8). | BOOT |
| Secrets leak to child processes at M1. | **Narrowed.** Latent: no production code starts a child yet. The tool-result half is reachable now (M1-G-BOOT-6). | BOOT |
| About 105 / 44 / 8 vet findings. | **Corrected.** Those were diagnostic lines; the unique counts are 53 / 24 / 4 = 81. | GATE |
| Gates 3-8 have never run. | **Partly.** They passed at DH-S-1 (runs 44670956416, 44675306125), never after #16. | GATE |
| The `go` CI job proves `src/go` vets clean. | **Corrected.** It vets without the project analyzers, so it stays green while the gate is red. | GATE |
| The import-rule analyzer is missing; the HTTP-client rule is a DH-S-1 gap; `GOWORK=off` of DH-T-TOOLCHAIN-11 is untested; `src/go` imports godog. | **Corrected.** Import rules exist as archtest tests; the HTTP rule is util's (DH-S-16); `GOWORK=off` is tested (`modcheck/toolchain_test.go:62`); `src/go` product packages are stdlib-only by import, only the module file and e2e package break the rule. | TOOL |
| "No tests for DH-T-TOOLS-7." | **Narrowed.** A ceiling test exists but carries the TOOLS-6 tag; the real gaps are `Settled`, `Block` and output-schema tests. | PIPE |
| The retry listener diverges from its spec. | **Partly.** Real, but llm.md itself contradicts the contract (`:435` against `:436`). | PIPE |
| LOOP ticks are wrong. | **Facts right, cause corrected.** 11.5 is truly built (the plan confuses built with done); 11.6 is truly not built. | LOOP |
| FND: loader logs lifecycle; LOADER-14 proven; SDK-6 met. | **Partly each.** An in-memory log exists but no log lines; the anonymous-neighbour clause is proven for `ConfigFile`, not `LayerSet`; the reasons half of DH-T-SDK-6 holds. | FND |
| The loader's stale-write refusal loses user data. | **Narrowed.** The harness edit path (`boot/config/service.go:771-810`) does not use `LayerSet`, so FND-1 is a proof gap at M1, not live data loss. | FND |
| The checkpoint-policy bypass is at `step.go:190-191`. | **Confirmed, one line off** (`step.go:189-190`); the step end also runs no checkpoint. | STORE |
| A derived-write failure "keeps the core intact". | **Partly.** True only of "never ahead"; the events themselves are lost (M1-G-STORE-10). | STORE |
| DH-ADR-STORE-5 is "sec. 6.4(a)". | **Corrected.** It is DH-ADR-STORE-5(a) in session-store.md sec. 7; there is no sec. 6.4. | STORE |
| DH-S-6 evidence is "the gate on three OSes". | **Corrected.** It cites `harness-manual-checks` run 44860891149, not a gate run. | CLOSE |
| impl.md S11 "4/7" matches the story plan. | **Corrected.** impl.md ticks 11.1, 11.2, 11.5 and 11.6, the opposite of what the plan ticks. | CLOSE |

## 9. Inconsistencies found in impl.md and the story docs

| # | Inconsistency | Citation | Fixed in plan stage |
|---|---|---|---|
| 1 | "106 steps" is used where impl.md S1-S11 has 103 steps and 99 deliverables. | `tech-spec.md:355`; `architecture.md:805`; `implementation-plan.md:731`; `evidence/DH-S-11.md:142` | 12.1 |
| 2 | impl.md sec. 1.2 status table still says "Not started". | `impl.md:50-56` | 12.1 |
| 3 | The DoD says "three CI operating systems"; OWNER-1 says Windows and WSL. Also DH-ADR-TOOLCHAIN-6 ("macOS and Windows are required lanes") and DH-D-TOOLCHAIN-4 ("no platform lane can be switched off"). | `tech-spec.md:141`; `toolchain.md:229,418-421` | 12.1, 12.3 |
| 4 | The M2 tech-spec assumes macOS and a DeepSeek key (M2-DEC-1, M2-EN-1, M2-R-01, M2-A-1). | `docs/stories/harness-DSH-M2/tech-spec.md:121-132` | 12.1 (drafts for the M2 owner) |
| 5 | Story plan boxes are unticked where impl.md ticks the steps (Phases 1, 3, 4, 6), and ticked where impl.md leaves them open (11.3, 11.4, 11.7, 9.2, 9.3). | `implementation-plan.md` Phases 1-11; `impl.md:440-914` | 18.1 (per cluster: 15.3, 16.6, 17.4) |
| 6 | The story 11.3 note "stays unticked until DH-E-AGENT-3" confuses built with done. | `implementation-plan.md` Stage 11.3 | 12.1 |
| 7 | `DH-S-9.md` says the operator approved raising `built-through` past S8; `DH-S-10.md` forbids it. | `evidence/DH-S-9.md:130`; `evidence/DH-S-10.md:78` | 12.1 |
| 8 | `DH-S-6.md` lists H-21 without its closing step 16.10. | `evidence/DH-S-6.md:279`; `impl.md:1150,3585` | 12.1 |
| 9 | The scenario schedule omits DH-E-EXEC-10 and DH-E-STORAGE-6. | `e2e-scenarios.md:113` | 12.1 |
| 10 | DH-D-FS-1's Done-when names DH-T-FS-27, whose tool clauses need DH-S-12 tools; propose a split. | `docs/harness/areas/filesystem.md:391` | 12.1, 14.5 |
| 11 | DH-T-HOST-17 is in a DH-S-6 Done-when line but needs DH-S-16 and M3 inputs. | `host-telemetry.md:483` | 12.1 (M1-D-BOOT-2) |
| 12 | impl.md sec. 3.2 "the stage closes only when all of them are built" refers to the 14 circle members, not 17 steps. | `impl.md` sec. 3.2 | 12.1 |
| 13 | The test kit spec says "no component takes a clock interface", yet `testkit/clock.Manual` satisfies `boot.Clock`. | `docs/harness/areas/util-test-support.md:248,542` | 12.1 |
| 14 | `context-compaction-skill.md` sec. 6.1 names the package `context/inject`; the code uses `request` (decision `context-package-name`). | `context-compaction-skill.md` sec. 6.1 | 12.1 |
| 15 | Spec suggestions to log under DC-3: `egress.Wrap` has one user (X3, P2); `storage/spill/policy` duplicates `retain`. | `util-test-support.md:395-400`; `storage/spill/policy/policy.go:147` | 12.1 |
| 16 | DH-ADR-TOOLCHAIN-2 puts race on Linux only ("wherever cgo is available"); QG-4 is stricter. Acceptable, but unrecorded. | `toolchain.md:189-191`; `tech-spec.md:243` | 12.2 |
| 17 | impl.md step 1.6 puts the golden helper under D-TOOLCHAIN-3; toolchain.md puts it under D-2. | `toolchain.md:389,394` | 12.2 |
| 18 | The story plan Stage 1.3 cites ADR-TOOLCHAIN-4 for build tags, which are ADR-1. | `implementation-plan.md` Stage 1.3 | 12.2 |
| 19 | impl.md step 1.8 omits `harness-replay`; step 1.9 is narrower than D-TOOLCHAIN-5. | `impl.md:449`; `toolchain.md:436-447` | 12.2 |
| 20 | The story narrowed the gate list with no DC-3 record; QG-2 "no exemption list" is in tension with the spec's site exceptions. | `architecture.md:162`; `tech-spec.md:241`; `toolchain.md:246` | 12.2, 13.5 |
| 21 | The `ci.yml` comment "the module has no dependencies" is false for `src/go`. | `.github/workflows/ci.yml:79` | 13.4 |
| 22 | Story step 3.9 says "refused" where the spec says "re-read and merged". | `implementation-plan.md:169` | 14.1 |
| 23 | Story step 4.1 names `max line bytes` (spec: `MaxFrameBytes`); step 4.3 names `schema.json` and re-implements the derivation in `src/sdk`. | `implementation-plan.md:206` | 14.2, 14.3 |
| 24 | The DH-S-4 evidence maps tests only for 4.10-4.14. | `evidence/DH-S-4.md:3-7` | 14.5 |
| 25 | The DH-S-6 evidence claims a bash tool result; no bash tool exists before DH-S-13. | `evidence/DH-S-6.md:288-289` | 14.6 |
| 26 | Story plan step texts narrow UTIL-5, EXEC-1 and HUMAN-7 without saying so; the plan fixes usage status 2 against the area spec's 64. | `implementation-plan.md:339,357,372,379` | 14.6 |
| 27 | DH-S-6 fixtures use `DSH_DEEPSEEK_API_KEY`; DH-E-STORAGE-6 and step 16.8 name macOS. | `implementation-plan.md:350` | 14.6, 15.3 |
| 28 | The DH-S-7 Closure table says "met" while four test items were deferred. | `evidence/DH-S-7.md:162-168,327-333` | 15.1 |
| 29 | #65 reset the four Stage 8.2 steps but left their three scenario boxes ticked; treat all seven as one closure unit. | `implementation-plan.md:538-540` | 15.3 |
| 30 | The DH-S-8 evidence assigns the draft record to DH-D-STORAGE-10, against `storage.md:427`. | `evidence/DH-S-8.md:76-77` | 15.3 |
| 31 | The prune deferral is an operator note naming no stage. | `implementation-plan.md:516` | 15.3 (and 12.1 names the decision) |
| 32 | Specs name `sqlitedb`, `sqlitestore`, `projection`, `<home>/store/dsh.sqlite` and a writer goroutine; none exists. | `session-store.md:171,202`; `session-views.md:203` | 15.3 |
| 33 | `session-store.md` says "Windows, Linux and macOS" for DH-D-STORE-2. | `session-store.md:459` | 15.3 |
| 34 | The story fixes four approval outcomes against `interaction-credentials.md` sec. 6.2. | `implementation-plan.md:626`; `e2e-scenarios.md:1882` | 16.1 |
| 35 | The story plan swaps DH-T-TOOLS-3 and -4. | `implementation-plan.md:634,636` | 16.2 |
| 36 | llm.md DH-D-LLM-3 contradicts itself on who waits before a retry. | `docs/harness/areas/llm.md:435,436` | 16.4 |
| 37 | `DH-S-9.md` says the request id is masked; the probe prints it raw. | `evidence/DH-S-9.md:61`; `llm/manual_deepseek_test.go:97` | 16.5 |
| 38 | `DH-S-10.md` claims closure that impl.md sec. 1.3 does not allow. | `evidence/DH-S-10.md:9` | 16.6 |
| 39 | The architecture F2 diagram shows the user message admitted at point 8; the code admits it at point 2. | `architecture.md` sec. 6.2 | 17.1 |
| 40 | The story plan (and STORE's M1-D-STORE-B) cite H-10 for provisional budgets; H-10 is the replay of a session with children. | `implementation-plan.md:695`; `impl.md` sec. 10.3 | 17.4 |
| 41 | The story scenario "Turn budget closes the turn" cites DH-T-AGENT-8; the ID is -22. | `implementation-plan.md` Stage 11.2 | 17.4 |
| 42 | `driver.Config` (agent-loop.md sec. 6.2) does not exist; the budget lives in `Options`. Turn-budget defaults are both "owner-confirmed" and "Unconfirmed". | `agent-loop.md` sec. 6.2; `tech-spec.md:300`; `agent/driver/budget.go:8-14` | 17.4 |

## 10. Scope of these findings

**What this analysis establishes.**
- The gaps listed in sec. 6 exist in the code and documents at commit `37a524a`, as read
  by the eight cluster reviews and cited by file and line.
- On 2026-11-22, on the Windows development host, plain `go build`, `go vet` and
  `go test -count=1` pass in `src/go`, `src/sdk` and `src/harness` without race or tags,
  `tools/harness-trace` tests pass, and the repository gate fails at vet with 81 distinct
  findings.

**What it does not establish.**
- It is a read of code at one commit plus one test run. It says nothing about later
  commits.
- No test ran with `-race`, `-shuffle`, build tags, the coverage floor, budgets or
  cross-compilation, and nothing ran on Linux or WSL. Gaps marked PARTLY (for example the
  coverage risk M1-G-GATE-16 and the triage gaps M1-G-GATE-15, -17) are risks until the
  gate runs; new failures found then become sub-gaps (`M1-G-GATE-15.n`, `-16.n`).
- Absence of a gap is not proof of correctness. DH-S-2 and DH-S-5 were not re-read by a
  code cluster beyond test hygiene and tagging; a later read may find more.
- The `DH-T` counts (130 of 259 missing) come from greps and are an upper bound on missing
  tests: some misses are untagged tests.
- Recommendations in sec. 7 are the clusters'; none is in force until the owner records
  it.
- Two gaps had no closing step in their fragment; their "Closed by" stage follows the
  plan's step map: M1-G-BOOT-9 (14.6, held open under M1-D-BOOT-2) and M1-G-STORE-24
  (15.3, the spec amendment under M1-D-STORE-C option a).

**Points the lead must settle between fragments.** The plan records a working default
for items 1 to 4; each still needs the owner or lead to confirm it.
1. **macOS return stage.** GATE says DH-S-18 entry; TOOL says DH-S-27; CLOSE says by
   DH-S-16 entry (step 16.8 runs on macOS); BOOT and FND offer DH-S-27, DH-S-16.8 or
   DH-S-18.
2. **Gate list scope.** M1-D-GATE-23 recommends recording the narrowing and holding
   staticcheck, tidy/verify (before DH-S-12) and govulncheck (DH-S-46) open; TOOL builds
   them in M1 as P0 (13.4, 13.5) and M1-D-TOOL-1 recommends govulncheck now. Similarly
   M1-D-GATE-25 recommends vetting e2e after M1, while M1-G-TOOL-17 is built in 13.5.
3. **Local-lane formats.** Run ids differ (`<lane>-<sha7>-<UTC>` in GATE; the file's
   SHA-256 in TOOL; `<yyyymmdd>-<lane>-<sha7>` in CLOSE), as do log paths
   (`evidence/gate-runs/` versus `evidence/runs/`) and the WSL Go install (`/usr/local/go`
   with `GOTOOLCHAIN=local` versus `$HOME/sdk` with `GOTOOLCHAIN=go<floor>`).
4. **DeepSeek-bound scenarios.** M1-D-CLOSE-4 may defer re-authoring (option B), while
   M1-G-LOOP-13 and M1-G-PIPE-14 retarget them now as P0.
5. **Routes M1 must pass.** M1-D-LOOP-6 recommends "Claude must pass, Copilot recorded";
   CLOSE.4 (plan 18.1) records the check on both.
6. **Phase 19 prerequisites.** A vendor-client route is the first production spawn. The
   plan moves only the batch opt-in of M1-G-BOOT-2 into 16.5; whether `Run`, private
   channels (M1-G-BOOT-2) or the child proxy overlay (M1-G-BOOT-10) are also needed before
   16.5 is open. Plan Stage 16.4 (P1 work) is an entry of P0 Stage 16.5, so it is on the
   M1 critical path.
7. **Priority of the CI matrix change.** M1-G-GATE-21 is P1; its duplicate M1-G-TOOL-13
   is P0.

**Settled by the lead (2026-11-22).** These settlements keep the two documents
consistent. They are not owner decisions, and each one still follows whatever the owner
records for the named decision.
- Item 2: the plan's default holds. The missing gates are built in 13.4 and 13.5, and
  the `e2e` layer is vetted in 13.5.7. The owner confirms this through M1-D-TOOL-1 and
  M1-D-GATE-23/-25.
- Item 3: the plan's formats hold (12.3.4, 12.4.2):
  - the run id is `<lane>-<sha7>-<UTC yyyymmddThhmmZ>`, kept under `evidence/runs/` with
    the log file's SHA-256 beside it;
  - go1.26.0 is pinned on both lanes, and the gate fails setup on any other version.
- Item 4: option A. All 13 DeepSeek-bound scenario files are re-authored before M1
  closes, which matches the P0 priority of M1-G-LOOP-13 and M1-G-PIPE-14.
- Item 5: M1-D-LOOP-6 holds. Claude must pass; Copilot is run and recorded either way.
  Plan step 18.1.6 now says so.
- Item 7: M1-G-GATE-21 is raised to P0. Changing the lanes breaks the three-OS pinning
  test, so its duplicate M1-G-TOOL-13 is P0 as well.
- Item 6 and the MCP-bridge question stay open until plan step 16.5.2 settles them.
- Item 1 (the macOS return stage) and any change to impl.md's own text under DC-2 are
  owner decisions.

**Owner answers (2026-11-22).** These are recorded in `decisions.md` by plan Stage 12.1.
- **M1-D-PIPE-3 and -4, and M1-D-CLOSE-3:** option A, adopted. dsh drives the user's
  installed official `claude` and Copilot CLIs as child processes and never reads a
  token. SP-1 is narrowed to the client-invocation boundary.
- **The macOS return stage:** not planned yet. macOS stays an open scope removal with no
  return stage, and it is never a pass.
- **M1-D-CLOSE-1:** option B, adopted. Ticks made under the red gate are kept as
  provisional and re-verified at the closure commit.
- **DC-2:** impl.md's text is not changed. Only pointer lines are added to the decision
  log.
