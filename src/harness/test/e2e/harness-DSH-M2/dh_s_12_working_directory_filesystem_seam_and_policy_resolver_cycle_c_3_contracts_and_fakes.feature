@story-harness:DSH-M2 @phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver @stage-cycle-c-3-contracts-and-fakes @setup-inline
Feature: Cycle C-3 contracts and fakes
  The sandbox Policy contract, the fs.FS seam with its in-memory fake and the
  conformance suite every provider must pass, and the archtest rules that keep
  cycle C-3 imports pointed at the contracts and never at world/fs/local.

  # TC-01a; proof: in-process; deps: none
  Scenario: Policy has no valid zero value
    Given a zero-value sandbox policy "var p sandbox.Policy"
    And a fresh in-memory filesystem fake
    When the zero policy is passed to the fake seam's write check
    Then the write returns an herr error with code "sandbox/unavailable"
    And the fence check for a write with the zero policy returns code "sandbox/unavailable"
    And the fence check for a read with the zero policy returns code "sandbox/unavailable"
    And the zero policy neither allowed nor silently denied the write

  # TC-01a; proof: in-process; deps: none -- in-memory fake
  Scenario: Fake seam passes its own conformance suite
    Given a provider factory returning "fstest.NewMemFS()"
    When "fstest.Run" executes against the provider
    Then every conformance subtest passes and none is skipped
    And the conformance run included the subtests:
      | name                                            |
      | stale-write-refused                             |
      | fence-read-only-refuses-every-mutation          |
      | fence-workspace-write-refuses-outside-root      |
      | fence-move-with-one-end-outside-changes-nothing |
      | fence-resolves-symlinks-first                   |
      | zero-policy-is-unavailable                      |
    And a write outside the workspace-write root on the fake is refused with code "fs/sandbox-denied"
    And a write guarded by a stale version on the fake is refused with code "fs/stale-version"

  # proof: service:go-toolchain; deps: go-toolchain package loader over committed testdata
  Scenario: Inverted import rejected
    Given the committed archtest fixture "testdata/c3" where "session/workdir" imports "world/fs/local"
    When the archtest rules run over the fixture loaded by the go toolchain
    Then the rules fail naming the import "internal/session/workdir imports github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
    And the violation is reported by the rule "no inverted import across cycle C-3"
    When the archtest rules run over the real harness tree loaded by the go toolchain
    Then the real tree has no violations
