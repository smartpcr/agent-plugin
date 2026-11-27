@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-go-client-package @setup-inline
Feature: Go client package (DH-S-16, TC-15b)
  The Go client of src/sdk/sdk starts `dsh --home <dir> --profile sdk` as a
  child, drives sessions over the SDK protocol, and closes the child through
  the shutdown, terminate and kill ladder.

  Scenario: Client drives the built binary
    Given DSH_BIN set to the dsh binary built once by the test
    And the keyless fake route answering "hello from the fake" in the sdk profile of a fresh home
    When the client starts, creates a session and runs the prompt "say hello"
    Then the run returns the fake's text "hello from the fake"
    And Close exits the child at the shutdown rung with status 0
    And testkit/procwait sees the child's pid gone

  Scenario Outline: Shutdown ladder escalates
    Given a child that acknowledges shutdown but <ignores>
    When the client closes it with a grace of 500 milliseconds
    Then Close returns without error within 10 seconds
    And the child ended at the "<unix>" rung on Unix or the "<windows>" rung on Windows
    And testkit/procwait sees the child's pid gone

    Examples:
      | ignores                                      | unix      | windows |
      | ignores the end of its input                 | terminate | kill    |
      | ignores the end of its input and SIGTERM     | kill      | kill    |

  Scenario: Missing home refused
    Given Options without a home naming a child that records its own start
    When Start is called
    Then Start returns the error "Options.Home is required" and no client
    And no process was started
