@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-durable-replace-under-power-loss @setup-inline
Feature: Durable replace under power loss (DH-S-18 Stage 8.9, impl.md 18.11, TC-21s5)
  A durable fsx.Replace is complete and never older than its acknowledgement
  after a hard power cut. Each guest OS runs the writer role of
  TestDurableReplaceSurvivesPowerLoss in a guest made fresh from a disposable
  overlay; this host, the hypervisor host, runs its controller and the ack
  recorder, which appends every acknowledged version to an append-only version
  log. The guest is powered off (not shut down) 100 times at a random moment
  and restarted, and the file it holds is judged after every restart.
  The hypervisor (proof service:vm-power-cut) is team-kept infrastructure,
  bound by the vm:* s18 jobs' inputs: DSH_S18_KVM_* for the Linux guest and
  DSH_S18_HYPERV_* for the Windows guest (runbook-power-cut.md step 2). The
  run's guest is made fresh from an overlay with PROBE_CREATE and deleted with
  PROBE_DELETE once it passes. A guest OS whose inputs are not set fails its
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
