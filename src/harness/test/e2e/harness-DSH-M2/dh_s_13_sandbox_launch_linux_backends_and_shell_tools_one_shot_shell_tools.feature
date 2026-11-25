@story-harness:DSH-M2 @phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools @stage-one-shot-shell-tools @setup-inline
Feature: One-shot shell tools (DH-S-13, TC-06b)
  The bash and pwsh tools take named arguments only (command, timeout,
  description and the escalation pair); strict decoding refuses any other
  argument before anything starts. A command that runs returns a structured
  result, logged as the tool/result event, that carries its exit code and the
  confinement its launch plan was confirmed under.

  Every call goes through the real tool pipeline (tools/core ExecuteBatch) with
  the shell tools mounted, into a real SQLite session log.

  A @lane-linux row runs on Linux natively, and on a Windows host inside WSL
  (this suite cross-compiled for linux). It never skips: a missing bash or an
  unreachable Linux lane fails it.

  Scenario Outline: Unknown argument rejected
    Given the bash tool mounted in the tool pipeline over a seam that counts plans
    And a tool call with arguments "<arguments>"
    When the tool validates input
    Then it returns a validation error naming "<unknown>"
    And the logged result names "<unknown>"
    And nothing is started

    Examples:
      | arguments      | unknown |
      | command, stdin | stdin   |
      | command, env   | env     |

  @lane-linux
  Scenario: Tool result carries confinement
    Given the Linux lane with bash found by preflight
    And a danger-full-access layer
    When the bash tool runs "true" through the tool pipeline
    Then the logged result contains exit_code 0
    And the logged result contains confinement.state "Unconfined"
    And the logged result contains confinement.mode "danger-full-access"
