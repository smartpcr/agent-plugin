@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-signed-in-route-runner-and-s17-workflow @setup-inline
Feature: Signed-in route runner and S17 workflow
  Stage 7.1 of harness:DSH-M2 commits the s17 job of harness-scenarios.yml, which
  runs the DH-S-17 scenarios on a self-hosted runner over the signed-in routes and
  carries no model key (M2-NG-2), and "harness-gate require --stage DH-S-17", which
  derives the required scenario list from the committed acceptance index (M2-DEC-3).
  Both are proved against committed goldens under tools/harness-gate/testdata.

  Scenario: Workflow carries no key
    Given the committed workflow ".github/workflows/harness-scenarios.yml"
    When the workflow lint in tools/harness-gate scans it
    Then the harness-gate workflow lint tests pass on the committed workflow
    And no input, env or secret reference names a model key
    And the "s17" job's runs-on is a self-hosted label list
    And the "s17" job runs only on workflow_dispatch of stage "DH-S-17"
    And the "s17" job equals the committed golden "testdata/s17_job.golden"

  Scenario: A model key seeded into the s17 job is caught by the lint
    Given the committed workflow ".github/workflows/harness-scenarios.yml"
    And the "s17" job is seeded with env "OPENAI_API_KEY" read from secret "LAB_KEY"
    When the workflow lint in tools/harness-gate scans it
    Then the lint reports 2 model-key references
    And the "s17" job differs from the committed golden "testdata/s17_job.golden"

  Scenario: A hosted runner for the s17 job is caught
    Given the committed workflow ".github/workflows/harness-scenarios.yml"
    And the "s17" job's runs-on is replaced by "ubuntu-latest"
    When the workflow lint in tools/harness-gate scans it
    Then the "s17" job's runs-on is not a self-hosted label list

  Scenario: Required list derivation
    Given the committed index "test/harness/acceptance-index.yaml"
    When "harness-gate require --stage DH-S-17" runs
    Then it exits 0
    And it prints exactly 14 distinct scenario IDs
    And the printed IDs are the S17 IDs of tech-spec sec. 4.2 rows 5-18
    And the output equals the committed golden "testdata/s17_require.golden"

  Scenario: A stage the index does not list is refused
    Given the committed index "test/harness/acceptance-index.yaml"
    When "harness-gate require --stage DH-S-99" runs
    Then it exits 1
    And it prints nothing on stdout
