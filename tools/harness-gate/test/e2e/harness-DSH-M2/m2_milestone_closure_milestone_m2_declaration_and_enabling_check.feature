@story-harness:DSH-M2 @phase-m2-milestone-closure @stage-milestone-m2-declaration-and-enabling-check @setup-inline
Feature: Milestone M2 declaration and enabling check (implementation-plan Stage 9.2)
  The milestone predicate (closure.Story.Check, impl.md sec. 3.3) accepts M2 only when
  DH-S-17 and DH-S-18 are closed and the step 16.8 guarded scenarios passed in the
  DH-S-16 record; the closure record may read "M2 reached: yes" only then. The release
  check ("harness-gate release", M2-DOD-7, NG-7) proves nothing is published: the
  release files equal their base-commit copies, no dsh release job exists, and every
  default profile's egress set is empty.

  # proof: in-process; deps: none
  Scenario: Milestone predicate
    Given an index listing the step 16.8 guarded scenarios under DH-S-16 and one scenario each under DH-S-17 and DH-S-18
    And fixture records with DH-S-17 "closed", DH-S-18 "open" and the step 16.8 guarded rows passed
    When the milestone check runs
    Then M2 is refused naming "DH-S-18" as not closed
    And no refusal names "DH-S-17"
    And a closure record reading "M2 reached: yes" is refused while "M2 reached: no" is allowed
    Given fixture records with DH-S-17 "closed", DH-S-18 "closed" and the step 16.8 guarded rows passed
    When the milestone check runs
    Then M2 is accepted and advances built-through to "DH-S-18"
    And a closure record reading "M2 reached: yes" is allowed once built-through reads "DH-S-18"
    But with the guarded row "DH-E-FS-3" not run M2 is refused naming "DH-E-FS-3"
    And the gate's milestone command refuses M2 on the committed records, which declare "M2 reached: no"

  # proof: golden; deps: committed release files and bundles; base-commit copies captured as tools/harness-gate/testdata/release_base.golden
  Scenario: Nothing published
    Given the committed ".github/workflows/release.yml", ".goreleaser.yaml" and default profile bundles
    And their base-commit copies in "tools/harness-gate/testdata/release_base.golden" at base "896c7604c220815b7633bb148b2e22e0b65cb85a"
    When the release check compares them with the story's base commit and computes the default egress set
    Then each release file equals its base-commit copy, whose blob ID matches the golden and git history
    And no release file names dsh and no other workflow runs on a tag push or runs goreleaser
    And the default egress set computed from the shipped bundles is empty for every profile, "headless" and "sdk" included
    And the gate's release command exits 0 reporting the release files unchanged and the egress set empty
    But the gate's release command refuses a copied tree in which release.yml gains a dsh release job
