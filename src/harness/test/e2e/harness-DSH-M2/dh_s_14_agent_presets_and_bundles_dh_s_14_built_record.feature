@story-harness:DSH-M2 @phase-dh-s-14-agent-presets-and-bundles @stage-dh-s-14-built-record @setup-inline
Feature: DH-S-14 built record
  DH-S-14 (agent presets and bundles) closes as a build stage: its merged commit
  passes every gate on both local lanes, the coverage check included (M2-DOD-1),
  and its evidence record docs/stories/harness-DSH-M2/evidence/DH-S-14.md is
  complete for "harness-gate ready --stage DH-S-14 --built" while its status line
  still reads open (C57 two-step protocol, implementation-plan Stage 1.1).

  # M2-DOD-1; proof: service:go-toolchain (the lanes' own --evidence records of
  # the merged commit, checked byte for byte)
  # The merged commit is DSH_E2E_DHS14_COMMIT when set, else the commit
  # "[impl] DH-S-14 Built Record" on this checkout's history.
  Scenario: S14 gate green on both lanes
    Given the merged commit of stage DH-S-14
    When harness-gate runs on "win.ps1" and "wsl.sh" for that commit
    Then the "win" lane record is an intact clean-tree evidence record of that commit on "windows"
    And the "wsl" lane record is an intact clean-tree evidence record of that commit on "linux"
    And every gate of each lane record passes and the record exits 0
    And the "coverage" gate is among the passing gates of each lane record
    And "docs/stories/harness-DSH-M2/evidence/DH-S-14.md" records both lane results with the commit SHA

  # C57 step 1; proof: golden (the committed record, read from HEAD, is the input)
  Scenario: Record complete
    Given the committed "docs/stories/harness-DSH-M2/evidence/DH-S-14.md" reading "Closure status: open"
    When "harness-gate ready --stage DH-S-14 --built" runs on it
    Then it exits 0 with the output of "testdata/dh_s_14_built_record_ready.golden"
    And it reports no missing manual check or commit field
    And the status line is not inspected: the record reading "Closure status: closed" is ready the same
    And the check is live: the record without the commit of step "14.3" is not ready, naming "step 14.3 (DH-D-BUNDLE-4): no commit SHA recorded"
    And the check is live: the record without the commit of the manual check of step "14.3" is not ready, naming "no commit recorded"
