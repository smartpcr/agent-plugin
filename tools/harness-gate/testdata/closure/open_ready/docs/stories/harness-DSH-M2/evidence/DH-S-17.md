# DH-S-17 evidence (fixture: open_ready)

Closure status: open

Fixture of the DH-S-17 acceptance close (stageclose_test.go, `TestStageClosureS17Golden`): the
live record's tables, filled with fixture values. `ready --stage DH-S-17` does not read the status
line; `close --stage DH-S-17` re-checks every ready condition and requires `closed`. The
`copilot-cli` legs of DH-E-LLM-1, DH-E-LLM-3 and DH-E-VIEW-1 are recorded (M2-DEC-4): LLM-3's failed
and its failure is kept.

## Steps

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 17.1 | scenarios of step 17.1 | 0a1b2c3 |  |
| 17.2 | scenarios of step 17.2 | 0a1b2c3 |  |
| 17.3 | scenarios of step 17.3 | 0a1b2c3 |  |
| 17.4 | scenarios of step 17.4 | 0a1b2c3 |  |
| 17.5 | scenarios of step 17.5 | 0a1b2c3 |  |
| 17.6 | scenarios of step 17.6 | 0a1b2c3 |  |
| 17.7 | scenarios of step 17.7 | 0a1b2c3 |  |
| 17.8 | scenarios of step 17.8 | 0a1b2c3 |  |
| 17.9 | scenarios of step 17.9 | 0a1b2c3 |  |

## Scenarios

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DH-E-TOOLCHAIN-1 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-toolchain-1 |
| DH-E-SESSION-1 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-session-1 |
| DH-E-SESSION-2 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-session-2 |
| DH-E-AGENT-1 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-agent-1 |
| DH-E-AGENT-2 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-agent-2 |
| DH-E-AGENT-3 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-agent-3 |
| DH-E-AGENT-4 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-agent-4 |
| DH-E-LLM-1 | claude-code | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-llm-1 |
| DH-E-LLM-1 | copilot-cli | acceptance | recorded | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model copilot-fixture; run run-dh-e-llm-1 |
| DH-E-LLM-3 | claude-code | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-llm-3 |
| DH-E-LLM-3 | copilot-cli | acceptance | recorded | E1 runner:s17-linux | 0a1b2c3 | failed (signed-out client reported, not retried) | dsh sha256 5f2c0e9a; model copilot-fixture; run run-dh-e-llm-3 |
| DH-E-TOOLS-1 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-tools-1 |
| DH-E-VIEW-1 | claude-code | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-view-1 |
| DH-E-VIEW-1 | copilot-cli | acceptance | recorded | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model copilot-fixture; run run-dh-e-view-1 |
| DH-E-LOADER-1 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-loader-1 |
| DH-E-SDK-1 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-sdk-1 |
| DH-E-SANDBOX-7 | Linux (E1) | acceptance | required | E1 runner:s17-linux | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; model claude-fixture; run run-dh-e-sandbox-7 |

## Manual checks register

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |

## Owner defaults (M2-C-4)

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Fixture.

### Environment and fixture

Fixture.

### Exact tests and commands

Fixture.

### Run identifiers

Fixture.

### Results

Fixture.

### Manual checks

None.

### Cleanup evidence

Fixture.

### Open items

None.