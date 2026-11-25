@story-harness:DSH-M2 @phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools @stage-linux-backends-bubblewrap-and-landlock @setup-inline
Feature: Linux backends -- bubblewrap and Landlock
  The Linux chain is bubblewrap, then Landlock (DH-D-SANDBOX-4). Bubblewrap is an
  outside confiner whose argv is a pure function of the policy; Landlock is a rule
  set the stage applies to itself, gated by the kernel's interface version. The
  seam selects the first backend whose start-up probe passed and refuses a
  confined policy when none did (TC-04a). Policies come from the fake resolver,
  so the write roots carry their real sources: the project root, a grant and the
  session scratch.

  Scenario: bwrap argv from policy (TC-04a)
    Given a "workspace-write" policy resolved with project root "P", grant "G" and scratch "S"
    And the bwrap host "BWRAP", harness binary "DSH", harness home "HOME", runtime directory "RUNTIME" and canary "CANARY"
    When the bwrap backend builds the argv for the policy
    Then the argv, with fixture paths written as tokens, equals the committed golden "internal/world/sandbox/local/testdata/bwrap_argv.golden"
    And the argv ends with "--" and unshares the network
    And the argv binds writable exactly "P, G, S"

  Scenario Outline: bwrap refuses what it cannot confine -- <row>
    Given a "<policy>" policy resolved with project root "P", grant "G" and scratch "S"
    And the bwrap host "<bwrap>", harness binary "DSH", harness home "HOME", runtime directory "RUNTIME" and canary "CANARY"
    When the bwrap backend builds the argv for the policy
    Then building the argv is refused "sandbox/launch-failed"

    Examples:
      | row                         | policy             | bwrap          |
      | relative bwrap program      | workspace-write    | relative:bwrap |
      | danger-full-access is never | danger-full-access | BWRAP          |

  Scenario: Landlock rule set from policy (TC-04a)
    Given a fake kernel reporting Landlock ABI 3
    And a "workspace-write" policy over real directories with project root "P", grant "G" and scratch "S"
    When the Landlock backend builds the rule set for the policy with harness home "HOME"
    Then writes are granted only beneath "P, G, S"
    And these paths are writable:
      | path      |
      | P         |
      | G/a/b     |
      | S/tmp     |
    And these paths are not writable:
      | path             |
      | .                |
      | HOME/config.json |
      | Px               |
      | other/f          |
    And the rule set handles truncate but not device ioctl
    And the rule set's Missing is exactly "fs-write"
    And the rule set governs exactly "harness-home"
    And enforcing the policy on that evidence is "partial" missing exactly "fs-write, network, delegated-execution"

  Scenario Outline: Landlock gates effects on the kernel's version -- ABI <abi>
    Given a fake kernel reporting Landlock ABI <abi>
    And a "workspace-write" policy over real directories with project root "P", grant "G" and scratch "S"
    When the Landlock backend builds the rule set for the policy with harness home "HOME"
    Then writes are granted only beneath "P, G, S"
    And the rule set's Missing is exactly "<missing>"
    And the rule set governs exactly "<governed>"

    Examples:
      | abi | missing                | governed               |
      | 1   | fs-write, harness-home |                        |
      | 2   | fs-write, harness-home |                        |
      | 3   | fs-write               | harness-home           |
      | 4   | fs-write               | harness-home           |
      | 5   |                        | fs-write, harness-home |
      | 6   |                        | fs-write, harness-home |

  Scenario: Landlock is refused on a kernel without it
    Given a fake kernel reporting Landlock ABI 0
    And a "workspace-write" policy over real directories with project root "P", grant "G" and scratch "S"
    When the Landlock backend builds the rule set for the policy with harness home "HOME"
    Then building the rule set is refused "sandbox/unavailable"

  Scenario: Selection order -- bwrap fails, Landlock passes (TC-04a)
    Given a sandbox chain of a "bwrap" backend whose probe fails with "bwrap not found" and a "landlock" backend whose probe passes
    When the service probes the chain at start-up
    Then the service reports "bwrap" unavailable because "bwrap not found"
    And the service reports "landlock" available
    And Prepare for a "workspace-write" policy gives a confined plan under backend "landlock"

  Scenario: Selection order -- the first passing backend wins
    Given a sandbox chain of a "bwrap" backend whose probe passes and a "landlock" backend whose probe passes
    When the service probes the chain at start-up
    Then the service reports "bwrap" available
    And the service reports "landlock" available
    And Prepare for a "workspace-write" policy gives a confined plan under backend "bwrap"

  Scenario: Selection order -- both failing refuses a confined policy (TC-04a)
    Given a sandbox chain of a "bwrap" backend whose probe fails with "bwrap not found" and the real Landlock backend on a fake kernel reporting ABI 0
    When the service probes the chain at start-up
    Then the service reports "bwrap" unavailable because "bwrap not found"
    And the service reports "landlock" unavailable because "no Landlock"
    And Prepare for a "workspace-write" policy is refused "sandbox/unavailable" with no plan
    And Prepare for a "read-only" policy is refused "sandbox/unavailable" with no plan
    And Prepare for a "danger-full-access" policy gives an unconfined plan under backend "none"
