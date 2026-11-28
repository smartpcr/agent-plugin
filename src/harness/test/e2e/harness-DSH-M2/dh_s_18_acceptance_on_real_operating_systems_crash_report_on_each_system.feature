@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-crash-report-on-each-system @setup-inline
Feature: Crash report on each system (DH-E-HOST-4)
  Stage 8.3 of harness:DSH-M2 runs DH-E-HOST-4 on Linux and Windows (impl.md
  18.2, TC-21s3): a host killed with no grace (SIGKILL on Linux, TerminateProcess
  on Windows) leaves its runtime record behind; the next start of dsh writes a
  crash report for it under <home>/reports, owner-only, and prints the
  unclean-exit line naming that report and "--safe". Until DH-S-22 builds the UI,
  "a start in safe mode reaches the UI" is the listener answering: "dsh status"
  names the --safe host live (M2-DEC-6, Q m2-host4-ui-clause).

  # proof: service:os-process (Linux) and service:windows-host (Windows). dsh and a
  # small driver (testdata/host4drive) are built from this checkout for the
  # variant and the driver runs the whole sequence ON the variant, so the kill and
  # the owner-only check are that system's own. The Linux variant is this host when
  # it is Linux, else the default WSL distribution through wsl.exe; the Windows host
  # is this host when it is Windows, else the Windows side of WSL interop. A variant
  # that cannot be reached fails the scenario; it is never skipped.

  @TC-21s3 @DH-E-HOST-4 @platform:linux-wsl
  Scenario: DH-E-HOST-4 Linux
    Given a Linux variant
    When on the variant a host started on a fresh short home answers, is killed with no grace and dsh starts again on that home
    Then the driver ran on the variant's operating system and killed the host with its forced kill
    And the first start answered its readiness probe and printed no unclean-exit line
    And the killed host exited
    And the next start prints the unclean-exit line naming the killed process, a report under <home>/reports and "--safe"
    And the named report is readable, owner-only and records the unclean exit of the killed process
    And "dsh crash" prints the same report path
    And a "--safe" start on the same home makes the listener answer

  @TC-21s3 @DH-E-HOST-4 @platform:windows
  Scenario: DH-E-HOST-4 Windows
    Given the Windows host
    When on the variant a host started on a fresh short home answers, is killed with no grace and dsh starts again on that home
    Then the driver ran on the variant's operating system and killed the host with its forced kill
    And the first start answered its readiness probe and printed no unclean-exit line
    And the killed host exited
    And the next start prints the unclean-exit line naming the killed process, a report under <home>/reports and "--safe"
    And the named report is readable, owner-only and records the unclean exit of the killed process
    And "dsh crash" prints the same report path
    And a "--safe" start on the same home makes the listener answer
