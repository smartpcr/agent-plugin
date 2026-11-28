# DH-S-17 evidence

Closure status: open

Evidence record of [impl.md DH-S-17](../../../harness/impl.md#dh-s-17) (Acceptance of the core loop, store, launchers and SDK against a real model) for story
`harness:DSH-M2`, in the impl.md sec. 7.3 form of the M1 records
([../../harness-DSH-M1/evidence/README.md](../../harness-DSH-M1/evidence/README.md)). Plan:
[implementation-plan.md](../implementation-plan.md) Phase 7. Decisions of
[../../harness-DSH-M1/decisions.md](../../harness-DSH-M1/decisions.md) stay in force.

**Kind.** Acceptance, P0. Pairs with build stages DH-S-2, DH-S-3, DH-S-5, DH-S-7 to DH-S-11 and DH-S-14 to DH-S-16.

**How this record closes** (C57; M2-C-2; [the gate's README](../../../../tools/harness-gate/README.md#stage-closure)):

1. While the line above reads `open`, fill the tables below until `harness-gate ready --stage DH-S-17`
   exits 0. `ready` does not read the status line.
2. Only then does the closing PR change the line to `Closure status: closed`.
3. That PR's gate runs `harness-gate close --stage DH-S-17`: every `ready` condition again, and the
   line reading `closed`. A `closed` line on a record that is not ready is refused `closed-but-not-ready`.

No M2 scenario run and no M2 stage closure happens before M1 is closed (M2-DEC-1; `harness-gate entry`,
implementation-plan Stage 1.2). The four tables below are read by the gate: keep their headings and columns.
A result is `passed`, `failed`, `not run (<reason>)`, `skipped`, `blocked` or `out of scope (<decision>)`;
skipped, blocked, not run and an empty cell count as open (impl.md sec. 7.3, M2-DEC-3), and only a
variant out of scope by a named decision is not a blocker (M2-C-2). A `recorded` leg must run; its failure
is kept and does not block (M2-DEC-4). A commit is a SHA of 7 to 40 lower-case hex digits.

## Steps

An acceptance step is ticked only by its scenarios; the commit is the one the scenarios ran at.

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 17.1 | DH-E-TOOLCHAIN-1 | | not run; blocked, see [Stage 7.2 blockers](#stage-72-blockers) |
| 17.2 | DH-E-SESSION-1, DH-E-SESSION-2 | | not run; blocked, see [Stage 7.3 blockers](#stage-73-blockers) |
| 17.3 | DH-E-AGENT-1 to -4 | | not run; blocked, see [Stage 7.3 blockers](#stage-73-blockers) |
| 17.4 | DH-E-LLM-1, DH-E-LLM-3 | | not run; blocked, see [Stage 7.4 blockers](#stage-74-blockers) |
| 17.5 | DH-E-TOOLS-1 | | not run; blocked, see [Stage 7.4 blockers](#stage-74-blockers) |
| 17.6 | DH-E-VIEW-1 | | not run; blocked, see [Stage 7.4 blockers](#stage-74-blockers) |
| 17.7 | DH-E-LOADER-1 | | not run; blocked, see [Stage 7.4 blockers](#stage-74-blockers) |
| 17.8 | DH-E-SDK-1 | | not run; blocked, see [Stage 7.5 blockers](#stage-75-blockers) |
| 17.9 | DH-E-SANDBOX-7 | | closes `UP-DEBT-found.Q3.2` (still open); not run, see [Stage 7.6 blockers](#stage-76-blockers) |

## Scenarios

The 14 scenarios of the steps, on E1: Linux with the signed-in Claude Code route (required) and Copilot CLI route (recorded, M2-DEC-4), on a self-hosted runner or a named WSL machine (M2-EN-8). A row closes a deliverable only with commit, environment and artifacts (M2-DEC-5); the record keeps binary checksum, model id and run id in Artifacts.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DH-E-TOOLCHAIN-1 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-SESSION-1 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-SESSION-2 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-AGENT-1 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-AGENT-2 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-AGENT-3 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-AGENT-4 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-LLM-1 | claude-code | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-LLM-1 | copilot-cli | acceptance | recorded | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-LLM-3 | claude-code | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-LLM-3 | copilot-cli | acceptance | recorded | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-TOOLS-1 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-VIEW-1 | claude-code | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-VIEW-1 | copilot-cli | acceptance | recorded | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-LOADER-1 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-SDK-1 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-SANDBOX-7 | Linux (E1) | acceptance | required | E1 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |

## Manual checks register

None: impl.md DH-S-17 counts no manual check ("An acceptance step is ticked only by its scenarios").

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |

## Owner defaults (M2-C-4)

None: impl.md sec. 9.1 names no default to confirm before DH-S-17.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |

## Stage 7.2 blockers

implementation-plan Stage 7.2 (impl.md 17.1, DH-E-TOOLCHAIN-1 first, architecture.md rule 7) was taken
up on `c5250d8` and could not run. Nothing below is evidence of a run; the TOOLCHAIN-1 row stays
`not run` and is owed. Each blocker on its own stops the run:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (DH-S-1 to DH-S-11 read `Closure status: open`; no `harness-DSH-M1/evidence/closure.md`). M2-DEC-1: no M2 scenario run before M1 closes. | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `c5250d8`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| No record command. `TestRecordThenReplayWithoutAKey` needs `DSH_SCENARIO_TOOLCHAIN1_DIR`, `DSH_SCENARIO_TOOLCHAIN1_RECORD` and `DSH_SCENARIO_TOOLCHAIN1_REPLAY`; none is committed, and the replay package (`src/harness/test/replay`) only replays committed recordings on the `replay` route (`DSH_REPLAY_REFRESH` rewrites oracles from a replay, not from a live route), so no command records a session against the `claude-code` route into a scenario directory. | `git grep DSH_SCENARIO_TOOLCHAIN1` on `c5250d8`: only the test and e2e-scenarios.md | a record mode (or driver) for the signed-in route and a committed scenario directory with manifest and workspace seed, plus the two command templates |
| Who runs it. [runbook-s17-wsl.md](runbook-s17-wsl.md): the signed-in run is by the owner or an operator, "never an agent session". | runbook, "Who, where, and with what" | the operator runs the runbook (or the `s17` job) on a merged commit once the two blockers above clear |

When it runs, the TOOLCHAIN-1 row takes the commit of step 1 of the runbook, `E1 wsl:<host>/<distro>`
(or `E1 runner:<name>`), and in Artifacts the `dsh` SHA-256, the claude-code model id, the run id and the
record's "request received equals request rebuilt" result (M2-DEC-5); the copilot-cli leg is recorded
either way (M2-DEC-4).

## Stage 7.3 blockers

implementation-plan Stage 7.3 (impl.md 17.2 and 17.3: `DH-E-SESSION-1`, `DH-E-SESSION-2`,
`DH-E-AGENT-1` to `-4`) was taken up on `aa11dac` and could not run. Nothing below is evidence of
a run; the six rows stay `not run` and are owed. Each blocker on its own stops the run:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (DH-S-1 to DH-S-11 read `Closure status: open`; no `harness-DSH-M1/evidence/closure.md`). M2-DEC-1: no M2 scenario run before M1 closes. | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `aa11dac`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| `DH-E-TOOLCHAIN-1` has not run. impl.md DH-S-17 Sequence and architecture.md rule 7 run the keyless replay first, so a wrong build is found before a prompt is spent; its row reads `not run` ([Stage 7.2 blockers](#stage-72-blockers)). | Scenarios table, TOOLCHAIN-1 row | the TOOLCHAIN-1 row reads `passed` in the same run |
| No committed scenario inputs. The tests need `DSH_SCENARIO_SESSION1_{LAYER,PROMPT,REPLAY}`, `DSH_SCENARIO_SESSION2_{LAYER,PROMPT}`, `DSH_SCENARIO_AGENT1_{LAYER,PROMPT,REPLAY}`, `DSH_SCENARIO_AGENT2_{LAYER,PROMPT,REPLAY}`, `DSH_SCENARIO_AGENT3_{LAYER,PROMPT,STOP,LOG,ALIVE}`, `DSH_SCENARIO_AGENT4_{LAYER,PROMPT}` and `DSH_SCENARIO_CLAUDE_CODE_MODEL` (each test's doc comment). The runbook takes them from the operator's shell or the runner's environment; none is committed. | `git grep -l -e DSH_SCENARIO_SESSION1_ -e DSH_SCENARIO_SESSION2_ -e DSH_SCENARIO_AGENT` on `aa11dac`: only the six tests and e2e-scenarios.md | the operator (or the runner's environment) supplies the inputs; none is a credential |
| Who runs it. [runbook-s17-wsl.md](runbook-s17-wsl.md): the signed-in run is by the owner or an operator, "never an agent session". | runbook, "Who, where, and with what" | the operator runs the runbook (or the `s17` job) on a merged commit once the blockers above clear |

What this stage did change: `harness-gate scenarios` ran the six scenarios in one `go test` run
with the other single-process scenarios, and `go test` runs a package's tests in file order
(`agent_stop_kill` before `agent_turn`, `session_kill_resume` before `session_rebuild`), so the
host-killing scenarios would have started first. `stageOrder` in
`tools/harness-gate/scenariorun.go` now gives DH-S-17 four phases: `DH-E-TOOLCHAIN-1`, the
single-process scenarios (`DH-E-SESSION-1`, `DH-E-AGENT-1`, `-2`, `-4` and the others), the
host-killing `DH-E-SESSION-2` and `DH-E-AGENT-3`, then `DH-E-SANDBOX-7` (impl.md DH-S-17
Sequence; `TestPhasesOrderDHS17`).

When they run, each of the six rows takes the commit of step 1 of the runbook and its machine
(`E1 wsl:<host>/<distro>` or `E1 runner:<name>`); in Artifacts, the `dsh` SHA-256, the claude-code
model id, the run id, the session export and the prompt count of the run (M2-C-5); the
`DH-E-SESSION-2` row also records that the resumed session's log read from the scratch store runs
`seq` 0 to n with no gap. These six scenarios are single required `Linux (E1)` legs; none has a
recorded `copilot-cli` leg.

## Stage 7.4 blockers

implementation-plan Stage 7.4 (impl.md 17.4 to 17.7: `DH-E-LLM-1`, `DH-E-LLM-3` and `DH-E-VIEW-1` with
one subtest per route, then `DH-E-TOOLS-1` and `DH-E-LOADER-1`) was taken up on `aa11dac` and could not
run. Nothing below is evidence of a run: the eight rows stay `not run`, and that includes the three
`copilot-cli` legs, because a recorded leg must run before its result is kept (M2-DEC-4) and a
recorded leg not run counts as open. Each blocker on its own stops the run:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (DH-S-1 to DH-S-11 read `Closure status: open`; no `harness-DSH-M1/evidence/closure.md`). M2-DEC-1: no M2 scenario run before M1 closes. | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `aa11dac`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| Stage order: Stage 7.4 depends on Stage 7.2, and `DH-E-TOOLCHAIN-1` runs first in S17 (architecture.md rule 7). Its row is `not run`, see [Stage 7.2 blockers](#stage-72-blockers). | Steps table, row 17.1 | the TOOLCHAIN-1 row reads `passed` |
| No committed scenario inputs. Each test stops at `need` without its inputs, none of which is committed (`git grep` on `aa11dac` finds them only in the tests and e2e-scenarios.md): `TestTurnWithReasoningAndToolCall` (LLM-1) `DSH_SCENARIO_LLM1_LAYER`, `_PROMPT`, `_PROMPT2`, `_USAGE`; `TestSignedOutIsAFact` (LLM-3) `DSH_SCENARIO_LLM3_LAYER`, `_PROMPT`, `_MAX_TOKENS`; `TestModelTitlesSession` (VIEW-1) `DSH_SCENARIO_VIEW1_LAYER`, `_BAD_LAYER`, `_PROMPT`; `TestGuardedPipelineRefusesTheWrite` (TOOLS-1) `DSH_SCENARIO_TOOLS1_LAYER`, `_PROMPT`, `_REPLAY`; `TestRunningHarnessFollowsProfilePatchEdit` (LOADER-1) `DSH_SCENARIO_LOADER_PROFILE`, `_START`, `_INSPECT`, `_DISABLE`, `_STOP`. | `src/harness/internal/testkit/scenarios/{llm_turn,llm_signed_out,view_title,tools_pipeline,loader_edit}_scenario_test.go` doc comments | the operator exports them per [runbook-s17-wsl.md](runbook-s17-wsl.md) "Route inputs", or the runner's environment carries them |
| Who runs it. [runbook-s17-wsl.md](runbook-s17-wsl.md): the signed-in run is by the owner or an operator, "never an agent session"; both clients (`claude`, required; `copilot`, recorded) must be signed in on the S17 machine. | runbook, "Who, where, and with what" | the operator runs the runbook (or the `s17` job) on a merged commit once the blockers above clear |

When they run (step 7 of the runbook, one `harness-gate scenarios --stage DH-S-17` run), each of the
eight rows takes the run's commit and `E1 wsl:<host>/<distro>` (or `E1 runner:<name>`), and in Artifacts
the `dsh` SHA-256, the route's model id and the scenario's report row. The `claude-code` legs of LLM-1,
LLM-3 and VIEW-1, and TOOLS-1 and LOADER-1, must read `passed`; the `copilot-cli` legs keep the result
the run's `scenarios: recorded leg: <Test>/copilot-cli ...` line gives, `failed (<reason>)` included
(M2-DEC-4). The LOADER-1 artifacts quote the log line showing the edited configuration in force on the
next turn.

## Stage 7.5 blockers

implementation-plan Stage 7.5 (impl.md 17.8, DH-E-SDK-1: `TestSDKDrivesBuiltBinaryOverStdio` in
`src/harness/internal/testkit/scenarios/sdk_stdio_scenario_test.go`, authored in Stage 6.11) was taken
up on `aa11dac` and could not run. Nothing below is evidence of a run; the SDK-1 row stays `not run`
and is owed. Each blocker on its own stops the run:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (DH-S-1 to DH-S-11 read `Closure status: open`; no `harness-DSH-M1/evidence/closure.md`). M2-DEC-1: no M2 scenario run before M1 closes. | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `aa11dac`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| DH-E-TOOLCHAIN-1 has not run. It runs first in S17 (architecture.md "Run order" rule 7; the runbook's step 7 runs it in the first phase), and its row reads `not run`, see [Stage 7.2 blockers](#stage-72-blockers). | the DH-E-TOOLCHAIN-1 row of the Scenarios table above | the TOOLCHAIN-1 row reads `passed` |
| Who and where. [runbook-s17-wsl.md](runbook-s17-wsl.md): the signed-in run is by the owner or an operator, "never an agent session", on Linux (WSL 2) with `bwrap` installed; the scenario declares `OS: linux` and skips elsewhere. | runbook, "Who, where, and with what"; `scenario.Needs` of the test | the operator runs the runbook (or the `s17` job) on a merged commit once the two blockers above clear |

Inputs the operator supplies (the runbook's "Route inputs"; none is a credential, none is committed):
`DSH_SCENARIO_SDK1_LAYER`, the path of a profile layer template (`{workspace}` expanded) whose policy asks
before writes; `DSH_SCENARIO_SDK1_LONG_PROMPT`, a prompt whose turn runs for minutes; and
`DSH_SCENARIO_CLAUDE_CODE_MODEL`, `DSH_SCENARIO_CLAUDE_CODE_EFFORT`.

When it runs, the SDK-1 row takes the commit of step 1 of the runbook, `E1 wsl:<host>/<distro>` (or
`E1 runner:<name>`), and in Artifacts the `dsh` SHA-256, the claude-code model id, the session id and the
run ids (`RunResult.MessageID`) of the allowed, rejected and answered runs (M2-DEC-5). Its pass covers
both Stage 7.5 scenarios: the approval, question, cancel, kill and resume steps with stdout holding
frames only (M2-C-10), and each run's result equal to the last assistant message of its turn in the
history the second host reads back from the scratch home's SQLite store.

## Stage 7.6 blockers

implementation-plan Stage 7.6 (impl.md 17.9, DH-E-SANDBOX-7 on the unmerged branch
`s17/policy-text`) was taken up on `6edc2a3` and could not run. Nothing below is evidence of a run:
the SANDBOX-7 row stays `not run`, no shipped text exists on `main`, and `UP-DEBT-found.Q3.2` stays
open. Each blocker on its own stops the run:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (M2-DEC-1). | the Stage 7.2 to 7.5 blockers above; unchanged on `6edc2a3` | M1 closure merges and `entry` exits 0 |
| Order: SANDBOX-7 runs last in S17 (architecture.md R-03), after the 13 other scenarios; every one of them is `not run`. | Scenarios table above | the other 13 rows are run (the copilot-cli legs recorded) |
| No `s17` job to dispatch. `.github/workflows/harness-scenarios.yml` has `preflight`, `scenarios` and `s18`, no `s17` job (Stage 7.1); a CI file is not changed by this stage. | `git grep -n s17 -- .github` on `6edc2a3`: no match | the `s17` job lands, or the operator runs [runbook-s17-wsl.md](runbook-s17-wsl.md) section "DH-E-SANDBOX-7 on s17/policy-text" by hand |
| No committed scenario inputs: `DSH_SCENARIO_SANDBOX7_TASKS` (the twelve tasks), `DSH_SCENARIO_SANDBOX7_LAYER`, `DSH_SCENARIO_SANDBOX7_PREVIOUS`, `DSH_SCENARIO_SANDBOX7_ATTEMPT`, `DSH_SCENARIO_CLAUDE_CODE_MODEL`. | `TestPolicyTextMakesTheModelEscalate` doc comment | the operator supplies them (none is a credential) |
| Who, and where the text lives. The signed-in run is by the owner or an operator, never an agent session. The branch `s17/policy-text` is a git commit and push that the Forge engineer brief for this stage does not allow ("DO NOT run `git add`, `git commit`, `git push` ..."; Forge commits only the workstream branch, which merges to `main`, where M2-C-6 forbids the text). Every byte the branch ships is therefore on `main` already, outside `policy/text/`, and the branch is moves only. | runbook, "Who, where, and with what"; Forge engineer brief | the operator follows the runbook section, steps A to C (A is three `git mv` and one commit) |

What this stage did change on `main` (no shipped text, so `harness-gate policy-text` prints
`ok: no sandbox policy text`):

- The three drafts, `src/harness/internal/world/sandbox/policy/testdata/draft_text/{linux,darwin,windows}.md`:
  test data only, embedded by no build. Their SHA-256 values are `dd4a974b87b958c84b6cf453ba8ffc0a1ea8cb64390579d83043b9485fc8857c`
  (linux), `fd4649a3890a8687af8914e236994b2bacfba3ffbac5b004c5bb29a07e6441dd` (darwin) and
  `26cd09e944ad13f796cc9886fa618c5793ebdd91c1ff9f033268f7db39c423d2` (windows); step A of the runbook
  moves them byte for byte to `text/`, so these are the values the row will record unless a draft
  is re-worded first. The wording asks the model to make the call and let the sandbox answer, and
  on `fs/sandbox-denied` to retry the same call with `sandbox_permissions` and a `justification`
  (the answer to upstream's five-in-twelve give-ups, `UP-DEBT-found.Q3.2`).
- The branch's other files, in `src/harness/internal/world/sandbox/policy/testdata/s17_branch/`:
  `policy_section_{linux,darwin,windows}.golden`, the render of each draft (`TestDraftTextGolden`
  checks them against the drafts on `main`), and `text_embed.go.txt`, the `//go:embed text/*.md`
  loader (checked to parse as the `policy` package). Step A moves them to `testdata/` and
  `text_embed.go` without re-recording, so the branch commit holds the texts and their goldens
  together; the move was rehearsed on `6edc2a3` plus this change: the policy and prompt tests
  pass, `dsh` builds, and `harness-gate policy-text` refuses the three texts for want of a row.
- `loadTexts` (`shipped_text.go`): the branch's `text_embed.go` loads `text/*.md` through it; a stray
  file or a text that does not render in every mode fails the package's tests.
- `TestShippedTextGolden`: `testdata/policy_section_<platform>.golden` exists exactly when that
  platform's text ships and equals its render in each mode (TC-01g), so a golden and its text
  arrive in one commit; `harness-gate policy-text` binds the text's bytes to the run (M2-C-6).
- `TestNoTextMeansNoSection` and `TestResolvePrecedence` now read the shipped set: on `main` they
  still require no `text/` directory and no text version; on the branch they require `text/` to be
  exactly the embedded texts.
- `TestPolicyTextMakesTheModelEscalate` reports and judges only the texts that ran, so a shard
  per text (M2-R-06) is not failed for the two texts it did not run. Each text is judged in its own
  subtest, and a text below the floor keeps the transcript of every one of its runs, not only of
  runs that failed alone (TC-20s; `TestSandbox7KeepsEveryTranscriptOfATextBelowTheFloor`).
- `harness-gate sandbox7-record --attempt <id>` takes the counts from `--report` files (one per
  run or shard, each platform once, read under `--root` when relative). Each report names the
  commit, the attempt (`DSH_SCENARIO_SANDBOX7_ATTEMPT`) and each text's SHA-256 as the scenario
  read them; a report of another commit, attempt or text bytes, or a `--go-test-log` without that
  attempt's provenance line or a pass, records nothing (`TestSandbox7RecordReadsShardReports`).
  The runbook runs S17 once on the branch head, SANDBOX-7 last; if time runs out it starts a new,
  separately named shard attempt, and step C records from the attempt file, failing closed.

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Not yet recorded. At closure: the commit of the three modules (one commit), the Go version (`go1.27.4`),
the binary's SHA-256 and embedded version.

### Environment and fixture

Not yet recorded. Per row of the Scenarios table: environment id (tech-spec sec. 4.3), operating system
and version, sandbox backend and reported enforcement, versions of external programs, fixture.

### Exact tests and commands

Not yet recorded: the gate runner's output, the `go test` command lines and `DSH_REQUIRE_SCENARIOS`
(`harness-gate require --stage DH-S-17`).

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

Manual checks: none count here (impl.md DH-S-17).
