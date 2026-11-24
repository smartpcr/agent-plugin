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
| 17.1 | DH-E-TOOLCHAIN-1 | | |
| 17.2 | DH-E-SESSION-1, DH-E-SESSION-2 | | |
| 17.3 | DH-E-AGENT-1 to -4 | | |
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
| DH-E-TOOLCHAIN-1 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-SESSION-1 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-SESSION-2 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-AGENT-1 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-AGENT-2 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-AGENT-3 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
| DH-E-AGENT-4 | Linux (E1) | acceptance | required | E1 | | not run (stage not started) | |
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
