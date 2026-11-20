@story-harness:DSH @phase-boot-launch-egress-secrets-and-test-kit @stage-leaf-helpers-and-process-trees @setup-inline
Feature: Leaf helpers and process trees
  The harness has one error table keyed by "<domain>/<reason>" codes, and every
  child process is started through the subprocess package, which owns and stops
  the whole process tree and never hands a secret to a child.

  Scenario: Error kinds map to one table
    Given an in-memory error with reason "subprocess/start-failed" carrying the secret "sk-e2e-secret-value"
    And the error is wrapped three levels deep by "boot/launch-failed", a fmt %w wrap and "cli/command-failed"
    When herr.Kind is read
    Then it returns the innermost reason "subprocess/start-failed"
    And the reason is a valid "<domain>/<reason>" code with domain "subprocess"
    And no formatting of the error includes the secret value

  Scenario: Process tree stopped and awaited
    Given the test binary re-exec'd as a child that spawns a grandchild ignoring SIGTERM
    And both the child and the grandchild are running
    When StopTree runs with a 2 s grace
    Then StopTree returns without error
    And both processes are gone when StopTree returns
    And the tree is reported gone and a second StopTree is a no-op

  Scenario: Child environment holds no secret
    Given a parent environment containing "DSH_DEEPSEEK_API_KEY" set to a dummy value
    When a child is started through subprocess with a given env list
    Then the child reports "DSH_DEEPSEEK_API_KEY" as absent
    When a child is started through subprocess with an env built from the parent environment
    Then the child reports "DSH_DEEPSEEK_API_KEY" as absent
    And the child reports "PLAIN_SETTING" as present
