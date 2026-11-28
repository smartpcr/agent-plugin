@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-e2-variant-fleet-and-runbooks @setup-inline
Feature: DH-S-18 E2 variant fleet and runbooks
  The S18 acceptance runs on a named fleet of machines listed in
  docs/stories/harness-DSH-M2/evidence/variants.md. The gate cross-references
  that list with the DH-S-18 entry of the acceptance index (M2-DOD-4), and each
  variant's job refuses to run on a machine that does not provide the sandbox
  service the variant names (architecture.md provided services table).

  Scenario: Variant list covers the index
    Given the committed variants file and the "DH-S-18" entry of the acceptance index
    When the index check cross-references them
    Then the check reports no problem
    And every S18 scenario has at least one in-scope variant or an owner decision removing it
    And every macOS row reads "out of scope (platforms-windows-wsl)"
    And the coverage equals the committed golden "coverage.golden"
    And the harness-gate variants command exits 0 and lists every S18 scenario
    And without the "vm:linux-guest" and "vm:windows-guest" rows the check fails naming "DH-E-NODEFS-1" and "M2-DOD-4"

  Scenario: Preflight refuses a wrong machine
    Given the recorded "dsh sandbox status" outputs
    When the "service:linux-landlock-only" preflight evaluates the "bwrap Full" output
    Then it fails for every "service:linux-landlock-only" machine kind
    And each failure names "service:linux-landlock-only" and "want backend landlock"
    And the harness-gate sandbox-preflight command exits 1 for it for every machine kind
    And the "service:linux-landlock-only" preflight passes only the output of its own machine kind
