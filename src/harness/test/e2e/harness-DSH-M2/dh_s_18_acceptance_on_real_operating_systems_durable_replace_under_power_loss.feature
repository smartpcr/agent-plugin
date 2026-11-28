@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-durable-replace-under-power-loss @setup-inline
Feature: Durable replace under power loss (DH-S-18 Stage 8.9, impl.md 18.11, TC-21s5)
  A durable fsx.Replace is complete and never older than its acknowledgement
  after a hard power cut. Each guest OS runs the writer role of
  TestDurableReplaceSurvivesPowerLoss in a guest made fresh from a disposable
  overlay; this host, the hypervisor host, runs its controller and the ack
  recorder, which appends every acknowledged version to an append-only version
  log. The guest is powered off (not shut down) 100 times at a random moment
  and restarted, and the file it holds is judged after every restart.
  The hypervisor (proof service:vm-power-cut) is team-kept infrastructure: a
  guest's controller is the one named by DSH_E2E_NODEFS_LINUX_GUEST or
  DSH_E2E_NODEFS_WINDOWS_GUEST. A guest OS with no controller fails its
  preflight and its scenario; it never skips.

  @TC-21s5 @DH-E-NODEFS-1 @proof:service:vm-power-cut @proof:service:ack-recorder
  Scenario: DH-E-NODEFS-1 Linux guest
    Given a libvirt guest from a disposable overlay, whose vm-power-cut preflight creates, cuts and deletes a throw-away guest
    And the ack recorder's address on this host, the hypervisor host, is local and free
    When it is powered off 100 times during fsx.Replace loops
    Then the scenario run passed and its report row for "DH-E-NODEFS-1" reads "passed"
    And the version log records 100 rounds of the guest, each cut after a new acknowledgement and read after its restart
    And after every restart the file is complete and never older than the recorder's last acknowledged version

  @TC-21s5 @DH-E-NODEFS-1 @proof:service:vm-power-cut @proof:service:ack-recorder
  Scenario: DH-E-NODEFS-1 Windows guest
    Given a Hyper-V guest from a disposable overlay, whose vm-power-cut preflight creates, cuts and deletes a throw-away guest
    And the ack recorder's address on this host, the hypervisor host, is local and free
    When the same 100 cuts run with Stop-VM -TurnOff
    Then the scenario run passed and its report row for "DH-E-NODEFS-1" reads "passed"
    And the version log records 100 rounds of the guest, each cut after a new acknowledgement and read after its restart
    And the same holds: after every restart the file is complete and never older than the recorder's last acknowledged version
