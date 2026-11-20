@story-harness:DSH @phase-cordis-port-guarantees @stage-config-views-and-strict-decoding @setup-inline
Feature: Config views and strict decoding
  A cordis fiber keeps its raw and effective config apart from any candidate under
  validation, a strict tree rejects unknown config fields by path, and a tree logs
  only to the exporters its creator chose.

  Scenario: Candidate config does not leak into effective config
    Given a component with effective config n=1 whose schema accepts only n=1
    When a candidate with n=2 is validated and rejected
    Then the raw config still reads n=1
    And the effective config still reads n=1
    And the update hook was not called with n=2

  Scenario: Unknown config field fails with its path
    Given a strict tree and a component whose schema has the field "retries"
    When the config {"retrys": 3} is decoded strictly
    Then decoding fails with an error naming "retrys"
    And the component is not constructed

  Scenario: Tree logs only to the chosen exporter
    Given two trees, one created with an in-memory exporter and one with none
    When each tree logs a line
    Then the in-memory exporter holds exactly one record
    And no package-global sink received anything

  Scenario: Sample output after all eight changes
    Given the committed cordis-sample golden "cmd/cordis-sample/testdata/output.golden"
    When the cordis-sample golden comparison runs 20 times
    Then the cordis-sample output matches the committed golden
    And the golden carries only the re-recordings listed in the DH-S-2 evidence record
