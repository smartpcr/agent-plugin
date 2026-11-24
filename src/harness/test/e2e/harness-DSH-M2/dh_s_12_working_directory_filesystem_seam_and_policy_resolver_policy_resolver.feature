@story-harness:DSH-M2 @phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver @stage-policy-resolver @setup-inline
Feature: Policy resolver (Stage 2.4)
  The sandbox policy resolver turns a session's mode, working directory,
  workspace root and grants into one call's sandbox.Policy, logs and announces
  mode changes, renders the model-visible policy section per platform and is
  guarded by the M2-C-6 policy-text gate.

  Scenario: Resolver precedence
    Given a workspace-write resolver over working directory "W" below workspace root "P" with scratch
    And a directory grant "G" on the session
    When Resolve runs for the session
    Then the write roots are exactly the workspace root, the grant and scratch, in that order
    And an explicit read-only Resolve yields no write roots
    And a resolver whose default mode is read-only yields no write roots

  Scenario: SetMode is logged and announced
    Given an in-memory live session under a read-only resolver
    And a sandbox.ModeChanging listener that records each dispatch
    When SetMode changes the session to "danger-full-access"
    Then exactly one sandbox.ModeChanging dispatch from "read-only" to "danger-full-access" was observed
    And during the dispatch the old mode was still in force and no sandbox/mode event was logged
    And exactly one sandbox/mode event to "danger-full-access" is appended to the session log
    And the next Resolve uses "danger-full-access"

  Scenario: Renderer fills the fixture template per platform
    Given the test-only fixture template "testdata/fixture_template.txt"
    When policy.Render runs with the resolver of each platform "linux", "darwin" and "windows"
    Then each output is byte-identical to its "testdata/render_fixture_<platform>.golden"

  Scenario: No text means no section
    Given a build with no "world/sandbox/policy/text/" directory
    When the prompt builder composes a session prompt with the resolver's section registered
    Then policy.Text returns policy.ErrNoText on every platform
    And the prompt has no sandbox policy section

  Scenario: Policy-text gate refuses unproven text
    Given the committed fixture trees under "tools/harness-gate/testdata/policytext/"
    When "harness-gate policy-text --root <tree>" runs on each tree
    Then only "no_text" and "text_with_matching_pass" exit 0
    And "text_without_sandbox7" and "text_edited_after_pass" exit non-zero naming "M2-C-6"
    And the combined output equals "testdata/policytext/expected.golden"
