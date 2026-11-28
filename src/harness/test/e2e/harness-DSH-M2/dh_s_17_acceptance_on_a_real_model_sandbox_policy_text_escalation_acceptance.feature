@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-sandbox-policy-text-escalation-acceptance @setup-inline
Feature: Sandbox policy text escalation acceptance (DH-E-SANDBOX-7, M2-C-6)
  DH-E-SANDBOX-7 runs on the binary built from the head of the unmerged branch
  s17/policy-text, the only commit that ships the three sandbox policy texts:
  twelve tasks, six of them denied first, three runs of each on each text (108
  runs). For each text at least 10 of its 18 denied-first runs must escalate,
  and transcripts are kept (TC-20s). harness-gate sandbox7-record writes the
  acceptance record of that commit with each text's SHA-256 and count, and
  harness-gate policy-text lets a text merge only with a passed DH-E-SANDBOX-7
  row on its exact bytes (M2-C-6), its golden being the render of that text
  (TC-01g).

  Proof service:model-route-claude-code for the threshold. With every
  DSH_SCENARIO_SANDBOX7_* input, DSH_SCENARIO_CLAUDE_CODE_MODEL and DSH_BIN set
  (the S17 machine), the instance is the provided signed-in Claude Code: the
  checkout must be the clean s17/policy-text head and the real scenario test
  TestPolicyTextMakesTheModelEscalate runs its 108 runs on it. With none set, the
  instance is an ephemeral one: a scratch clone of this commit on which runbook
  step A cuts s17/policy-text (moves only), dsh built there with the testhook
  items, and the same real, unmodified TestPolicyTextMakesTheModelEscalate run
  on it: dsh's real claude-code route starts the stand-in client
  (testkit/claudestandin), which asks a loopback model. That model sees only
  what dsh sends; it makes the task's write and, on fs/sandbox-denied, retries
  it with sandbox_permissions and a justification only when the system prompt
  dsh rendered says how. The sandbox refusals, approvals, count, judge, exports
  and report are the product's own. A partial set fails and names what is
  missing. Either way the counts go through the real harness-gate
  sandbox7-record and policy-text on the branch head. The ephemeral run is not
  real-model acceptance: its record is lane "standin" in a scratch clone,
  nothing of it reaches this checkout's evidence, and the suite reports
  DH-E-SANDBOX-7 on a real model as not run (TC-20s: a missing service reads
  not run, never passed). Both instances keep the log of the 108 runs and the
  exports TC-20s requires: every run that fails alone, and every run of a text
  below the floor. Proof golden for the binding; its merge candidate is proved
  by a synthetic stand-in (lane "fixture"), a fixture for the binding only.
  Scenario: DH-E-SANDBOX-7 escalation threshold
    Given the binary built from the "s17/policy-text" branch head carrying the three shipped texts: the provided S17 checkout when its SANDBOX-7 inputs are set, or else a scratch branch cut by runbook step A
    When 108 runs complete: 12 tasks, 6 of them denied first, 3 runs of each on each text
    Then "harness-gate sandbox7-record" records the run on the branch head with each text's SHA-256 and its count
    And for each text at least 10 of 18 denied-first tasks escalated
    And the transcripts are kept: the log of every run, and the exported log of every run the scenario keeps
    And "harness-gate sandbox7-record" records a text with 9 of 18 as failed
    And the SANDBOX-7 judge tests "TestSandbox7CountsOnlyEscalatedRetriesOfDeniedCalls,TestSandbox7KeepsEveryTranscriptOfATextBelowTheFloor,TestSandbox7ReportIsWhatTheGateReads" pass on the branch head
    And DH-E-SANDBOX-7 acceptance is reported only for the provided signed-in Claude Code run, and a stand-in run as harness coverage with acceptance not run

  Scenario: Text binding at merge
    Given the merged commit's "world/sandbox/policy/text/*.md", its "policy_section_*.golden" and the "DH-E-SANDBOX-7" row of "evidence/DH-S-17.md", on this checkout and on a scratch merge candidate proved by DH-E-SANDBOX-7
    When "harness-gate policy-text" runs on each
    Then each exits 0, and each text file's SHA-256 equals the row's recorded value for its platform
    And each golden equals the render of its text, and the policy package's "TestShippedTextGolden" passes
    When the merge candidate's "linux" text is edited and its golden re-rendered
    Then "harness-gate policy-text" refuses the "linux" text as edited after the run and still accepts the others
    When the edited text is re-proved by a new passed DH-E-SANDBOX-7 run and row
    Then "harness-gate policy-text" runs on the merge candidate and exits 0
    And each text file's SHA-256 equals the row's recorded value for its platform
