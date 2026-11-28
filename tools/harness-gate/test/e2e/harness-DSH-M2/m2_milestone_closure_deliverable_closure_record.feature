@story-harness:DSH-M2 @phase-m2-milestone-closure @stage-deliverable-closure-record @setup-inline
Feature: Deliverable closure record (implementation-plan Stage 9.1)
  The closure predicate (closure.ProofProblems, M2-C-3) checks every state of the
  closure record against the stage records, so built and done are never merged and
  neither is claimed ahead of its evidence; the committed closure.md holds the
  tech-spec sec. 3.2 bounds (M2-I-5).

  # proof: in-process; deps: none
  Scenario: Done needs every proof
    Given an in-memory closure table marking "DH-D-CLI-2" done with proving scenario "DH-E-CLI-1"
    And a closed, built DH-S-16 stage record in which "DH-E-CLI-1" has no row
    When the closure predicate runs
    Then it refuses naming "DH-D-CLI-2" and "DH-E-CLI-1"
    And the refusal says the proving scenario is unrun under M2-C-3
    But once "DH-E-CLI-1" has a passed row with its evidence the predicate accepts the row

  # proof: golden; deps: committed closure record
  Scenario: Counts match tech-spec
    Given the committed closure record "docs/stories/harness-DSH-M2/evidence/closure.md"
    When the closure check counts rows
    Then there are 35 deliverables, 30 P0 and 5 P1
    And at most 11 rows read "done", each a done-candidate of the story
    And every "built" row names a stage or H-item
    And the closure shape check and the closure predicate find no problem against the committed stage records
