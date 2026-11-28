@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-linux-confinement-and-sandbox-status @setup-inline
Feature: Linux confinement and sandbox status (DH-S-18 Stage 8.5, TC-21s1)
  The DH-E-SANDBOX-1 and DH-E-NATIVESYSTEM rows on the three Linux machine roles
  of variants.md: bwrap, Landlock only and unconfined. On each machine
  "dsh sandbox status" (the dsh binary built from this checkout) gives its
  verdict, read from the head of the report as the evidence record copies it,
  never from a backend line; and shell commands run through the real bash tool
  pipeline over the real Linux sandbox chain (bubblewrap, then Landlock) and the
  real policy resolver, with the real launch stage.

  Each machine is the provided one when DSH_SCENARIO_SANDBOX1_MACHINES names an
  SSH alias for its role. Otherwise it is an ephemeral one on this host's Linux
  (natively, or through WSL from Windows): the Landlock-only machine is a PATH
  without bwrap, and the unconfined machine adds a seccomp filter under which
  the Landlock system calls fail as on a kernel booted with Landlock disabled.
  A machine whose preflight does not hold fails its scenario; none is skipped.

  Scenario: bwrap machine (TC-21s1)
    Given the "service:linux-sandbox-bwrap" machine whose preflight reads "bwrap Full"
    When the SANDBOX-1 and NATIVESYSTEM rows run on it in a "workspace-write" session with require "any"
    Then "dsh sandbox status" reports "Full" under backend "bwrap" with no missing effect
    And every command's result reports backend "bwrap" in state "full"
    And the writes inside the write roots succeed
    And the writes outside the write roots are denied and leave nothing behind
    And the command's own exit code and stderr reach the caller

  Scenario: Landlock-only machine (TC-21s1)
    Given the "service:linux-landlock-only" machine whose preflight reads "landlock"
    When the SANDBOX-1 and NATIVESYSTEM rows run on it in a "workspace-write" session with require "any"
    Then "dsh sandbox status" reports "Partial" under backend "landlock" naming the missing effects
    And every command's result reports backend "landlock" in state "partial" naming the missing effects
    And the writes inside the write roots succeed
    And the writes outside the write roots are denied and leave nothing behind
    And the command's own exit code and stderr reach the caller

  Scenario: Unconfined machine refuses (TC-21s1)
    Given the "service:linux-unconfined" machine whose preflight reads "Unconfined"
    When a command creating a marker is started on it in a "workspace-write" session with require "full"
    Then "dsh sandbox status" reports "Unconfined" with every backend of the chain unavailable
    And the start is refused with "sandbox/unavailable" or "sandbox/enforcement-insufficient"
    And no command ran: the marker does not exist
