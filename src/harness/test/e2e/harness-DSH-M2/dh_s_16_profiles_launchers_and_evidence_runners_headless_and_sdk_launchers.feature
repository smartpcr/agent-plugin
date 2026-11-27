@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-headless-and-sdk-launchers @setup-inline
Feature: Headless and SDK launchers
  The built dsh (./cmd/dsh with the testhook tag, which adds the keyless fake
  model route llm.testkit-route) runs one prompt headless and serves the SDK on
  standard input and output, each in its own process on a temp home, and ends
  with a bounded exit status (TC-13b, TC-12b, INV-10, M2-C-10).

  Scenario: Headless prints the last assistant text
    Given the built dsh with the keyless fake route answering "hello" on a temp home
    When "dsh headless "say hello"" runs on the temp home
    Then stdout is exactly "hello" and a newline
    And the headless exit status is 0

  Scenario: JSON export lines
    Given the built dsh with the keyless fake route answering "hello" on a temp home
    When "dsh headless --json "say hello"" runs on the temp home
    Then the headless exit status is 0
    And every stdout line but the last parses as an export record
    And the export records include "assistant/message" and "turn/end"
    And the last stdout line is the run result with text "hello"

  Scenario: Admin command beside a live headless run
    Given the built dsh with the keyless fake route answering "hello" on a temp home
    And the fake route holds its reply
    And "dsh headless "say hello"" is running on the temp home, blocked on the fake-model reply
    When "dsh sandbox status" runs on the same home with the component trace on
    Then the admin command exits 0
    And its component trace lists no agent, model client or carrier
    When the fake route releases its reply
    Then the headless run completes with status 0 and prints "hello"

  Scenario Outline: SDK launcher bounded endings
    Given the built dsh with the keyless fake route answering "hello" on a temp home
    And the sdk profile layer of the temp home inserts the fake route <setup>
    When "dsh --profile sdk" starts on the temp home and <ending>
    Then the sdk launcher exits with status <status>

    Examples:
      | setup                             | ending                          | status |
      | as it is                          | the client closes stdin         | 0      |
      | with a write stall of 1s          | the client stops reading        | 125    |
      | with a release that never returns | the forced deadline elapses     | 124    |
