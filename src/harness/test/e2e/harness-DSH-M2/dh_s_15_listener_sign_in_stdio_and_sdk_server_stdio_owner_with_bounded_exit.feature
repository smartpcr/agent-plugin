@story-harness:DSH-M2 @phase-dh-s-15-listener-sign-in-stdio-and-sdk-server @stage-stdio-owner-with-bounded-exit @setup-inline
Feature: Stdio owner with bounded exit
  The wire/stdio owner claims the real process stdin and stdout, sends stray
  prints to the log, bounds every write to the client and ends the process
  through an injected stop with 0 (input closed), 125 (client stalled) or 124
  (forced deadline). The helper process is this test binary re-exec'd through
  world/subprocess.Start, the one spawn path (M2-NG-3).

  # TC-12b
  Scenario: Stray write redirected
    Given a stdio helper process that writes one valid frame and then prints "x"
    When the parent starts it through subprocess.Start with stdout bound to a buffer and a log in a temp dir
    Then the helper exits with status 0
    And stdout holds only the one frame
    And the helper log holds "x"

  # TC-12a
  Scenario: Bounded writer logic
    Given an in-process bounded writer over a pipe whose reader never reads, with a 200 ms write deadline and a stop fake
    When a write is attempted and the write deadline elapses
    Then the writer reports ClientStalled
    And the stop fake was called exactly once with ClientStalled and status 125

  # TC-12b
  Scenario Outline: Bounded endings
    Given a stdio helper process owning real stdio with an exiting stop and a 2 s forced deadline
    When the parent <action>
    Then the helper exits with status <status>

    Examples:
      | action                                      | status |
      | closes the child's stdin                    | 0      |
      | stops reading while the child writes        | 125    |
      | waits past the deadline with stdin open     | 124    |
