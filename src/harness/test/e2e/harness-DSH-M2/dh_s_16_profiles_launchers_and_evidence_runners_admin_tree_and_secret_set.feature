@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-admin-tree-and-secret-set @setup-inline
Feature: Admin tree and secret set
  A class admin command runs in the embedded admin composition, never a
  launchable profile, so it builds no agent, model client or carrier; a secret
  reaches the managed store from standard input only; the command reference is
  the committed golden (TC-13b, TC-13g).

  # The secret is named TEST_K: a secret name has an owner and a name
  # (secret://test/k), a one-word "k" is refused by the name rule itself.

  Scenario: Admin tree builds no agent
    Given the built dsh binary and a temp home that does not exist yet
    When "dsh sandbox status" runs with the component trace on
    Then it exits 0 with the sandbox status report
    And its component trace lists no agent, model client or carrier
    And its component trace is exactly the read-only admin view "boot.logging,host.sharing,sandbox.local"
    And the temp home was not created

  Scenario: Secret only from stdin
    Given the built dsh binary and a temp home that does not exist yet
    When "dsh secret set TEST_K" runs with "v" on stdin
    Then it exits 0 and reports "stored secret://test/k (revision 1)"
    And the secret store of the temp home holds "secret://test/k" with the value "v"
    Given the built dsh binary and a temp home that does not exist yet
    When "dsh secret set TEST_K v-in-argv" runs with nothing on stdin
    Then it exits with the usage status 64
    And no stream carries "v-in-argv"
    And the secret store of the temp home does not hold "secret://test/k"

  Scenario: Command reference
    Given the command tree of the built dsh binary
    When the reference is rendered by "dsh catalog commands"
    Then it equals the committed cli/testdata/reference.golden byte for byte
    And it equals the in-process rendering of the command tree
