@story-harness:DSH @phase-loader-layers-and-write-back @stage-codec-seam-and-entry-schema @setup-inline
Feature: Codec seam and entry schema
  The loader decodes layer bytes through a Codec into positioned nodes, validates
  every entry in one pass, and evaluates expressions only through an injected
  Evaluator, keeping the expression source in the raw config.

  Scenario: Diagnostic carries file, line and column
    Given in-memory layer bytes named "base.json" with a type error on line 4 column 9
    When the loader validates the layer
    Then the first diagnostic is "base.json:4:9: [0].id: must be a string, got number"
    And the diagnostic text equals the committed golden "diag_position"

  Scenario: All entry errors reported in one pass
    Given an in-memory document "three.json" with three invalid entries
    When the loader validates the layer
    Then 3 diagnostics are returned
    And the diagnostics are, in document order:
      | three.json:2:11: [0].id: entry id "a:b" must not contain ":" |
      | three.json:4:3: [2].name: entry "nameless" requires a name   |
      | three.json:5:41: [3].disabeld: unknown key                   |

  Scenario: Expression seam calls the injected evaluator
    Given a loader with a fake evaluator returning 7 for "env.X"
    And an entry "e" whose config is {"times":"${{ env.X }}","text":"x"}
    When the entry is loaded
    Then the entry "e" is active
    And the effective config of "e" holds times 7
    And the raw config of "e" still holds the expression text
    And the evaluator was called once for "env.X"
