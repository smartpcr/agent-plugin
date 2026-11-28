@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-linux-process-tree-ownership @setup-inline
Feature: Linux process tree ownership (DH-S-18 Stage 8.4, impl.md 18.3, TC-21s2)
  The host owns every command's whole process tree. On a Linux login whose own
  control group is not writable it asks the user service manager once for a
  delegated scope and makes each tree's control group inside it; with no user
  manager it falls back to process groups, says so, and applies no limits.
  Each machine is an account reached over SSH. It is either the provided one
  (DSH_SCENARIO_EXEC10_SSH, DSH_SCENARIO_EXEC10_SSH_NOMANAGER) or an ephemeral
  scratch account behind a private sshd on this host's systemd Linux. On the
  ephemeral machine the no-user-manager account's user@<uid>.service is stopped
  and masked, and the account does not linger.

  @TC-21s2 @DH-E-EXEC-1 @proof:service:linux-systemd-cgroup
  Scenario: DH-E-EXEC-1 tree ended
    Given the "linux-e2:systemd" machine, an account reached over SSH, whose preflight records the user manager "running"
    When a command forks a tree 5 levels deep with a setsid grandchild and an orphan that ignore SIGTERM, and is stopped
    Then the tree's owner is reported as "control-group", exact, with no fallback
    And the owner is the delegated scope the user service manager granted, holding every member
    And no descendant survives the stop
    And the tree's control group is removed
    And the delegated scope is gone once the host exits

  @TC-21s2 @DH-E-EXEC-10 @proof:service:linux-systemd-cgroup
  Scenario: DH-E-EXEC-10 memory limit enforced in the delegated scope
    Given the "linux-e2:systemd" machine, an account reached over SSH, whose preflight records the user manager "running"
    When a command forks a tree 5 levels deep with a setsid grandchild and an orphan that ignore SIGTERM, under a 256 MiB memory limit, and is stopped
    Then the tree's owner is reported as "control-group", exact, with no fallback
    And the owner is the delegated scope the user service manager granted, holding every member
    And the memory limit is reported "enforced"
    And the tree's control group has memory.max 268435456
    And no descendant survives the stop
    And the tree's control group is removed
    And the delegated scope is gone once the host exits

  @TC-21s2 @DH-E-EXEC-10 @proof:service:linux-systemd-cgroup
  Scenario: DH-E-EXEC-10 fallback reported
    Given the "linux-e2:no-user-manager" machine, an account reached over SSH, whose preflight records the user manager "stopped"
    When a command forks a tree 5 levels deep with a setsid grandchild and an orphan that ignore SIGTERM, under a 256 MiB memory limit, and is stopped
    Then the tree's owner is reported as "process-group", not exact, with a fallback naming the user service manager
    And the fallback is announced exactly once and no delegated scope is reported
    And the memory limit is reported "unavailable"
    And every member left in the process group is ended and the setsid grandchild is reported as a survivor
