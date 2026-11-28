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
| 17.4 | DH-E-LLM-1, DH-E-LLM-3 | | |
| 17.5 | DH-E-TOOLS-1 | | |
| 17.6 | DH-E-VIEW-1 | | |
| 17.7 | DH-E-LOADER-1 | | |
| 17.8 | DH-E-SDK-1 | | |
| 17.9 | DH-E-SANDBOX-7 | | closes `UP-DEBT-found.Q3.2` |

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
| DH-E-LLM-1 | claude-code | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-LLM-1 | copilot-cli | acceptance | recorded | E1 | | not run (stage not started) | |
| DH-E-LLM-3 | claude-code | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-LLM-3 | copilot-cli | acceptance | recorded | E1 | | not run (stage not started) | |
| DH-E-TOOLS-1 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-VIEW-1 | claude-code | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-VIEW-1 | copilot-cli | acceptance | recorded | E1 | | not run (stage not started) | |
| DH-E-LOADER-1 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-SDK-1 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-SANDBOX-7 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |

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
`seq` 0 to n with no gap. The copilot-cli legs are recorded either way (M2-DEC-4).

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
