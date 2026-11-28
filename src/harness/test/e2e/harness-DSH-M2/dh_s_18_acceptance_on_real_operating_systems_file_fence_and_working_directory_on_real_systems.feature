@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-file-fence-and-working-directory-on-real-systems @setup-inline
Feature: File fence and working directory on real systems (DH-E-FS-1, DH-E-FS-2, DH-E-VIEW-5)
  Stage 8.6 of harness:DSH-M2 (impl.md 18.5 and 18.6) runs the DH-S-12 filesystem
  scenarios on real operating systems: the fence under a link swap on ext4, NTFS
  and the SMB share (TC-21s3), the long FS-2 session on Linux and on Windows, and
  the VIEW-5 host move under bwrap on Linux (TC-21s4).

  # proof: each scenario runs the product's own, unmodified scenario test
  # (internal/testkit/scenarios, -tags scenario) on the variant: this host when its
  # OS matches, else the default WSL distribution through wsl.exe from a Windows
  # host. A variant that cannot be reached, or lacks a dependency the scenario
  # names, fails the scenario; it is never skipped. When the operator's inputs of a
  # scenario are all set (DSH_BIN, DSH_SCENARIO_*), they are the provided instance:
  # the signed-in Claude Code client, the share and the layers of the runbook. Else
  # the suite stands up an ephemeral instance for what it can give: the testhook
  # dsh, local git checkouts and scratch directories on the variant's own
  # filesystem. No stand-in replaces a dependency: the model route is the
  # variant's own claude, which the s18 preflight requires installed there, signed
  # in (claude auth status), answering one probe turn, on a run the tester
  # declares theirs (DSH_S18_TESTER); an input nothing provides fails its
  # preflight, and the leg reads "not run (<check> preflight failed)". FS-1's
  # directories and share are checked on every lane, the operator's included:
  # each directory must exist on a real filesystem, together they must cover ext4
  # and NTFS on Linux (NTFS on Windows), and the share must be a writable SMB
  # mount, else the filesystem preflight fails before any call runs.
  # The verdict is the scenario test's own row (DSH_SCENARIO_REPORT) read back:
  # passed, on the variant's OS, for the dsh binary the run used. Each leg also
  # keeps an evidence row (DSH_S18_EVIDENCE_DIR) with that verdict, the sandbox
  # status and the fence under test.

  @TC-21s3 @DH-E-FS-1 @platform:linux-wsl @platform:windows
  Scenario: DH-E-FS-1 fence on real file systems
    Given the "ext4" variant on Linux and the "NTFS" variant on Windows
    When 10,000 tool calls run under the link-swap helper on each variant
    Then each variant's DH-E-FS-1 row reads passed for the binary it ran
    And on each variant the helper swapped the link while the calls ran
    And on each variant the hash listing of the outside directory is unchanged before and after

  @TC-21s4 @DH-E-FS-2 @platform:linux-wsl
  Scenario: DH-E-FS-2 on Linux
    Given the tester's Linux machine on "ext4" with the signed-in Claude Code route, git and an editor save script
    When the long-running FS-2 run completes on the variant
    Then the variant's DH-E-FS-2 row reads passed for the binary it ran
    And every task of the run's shard passed
    And every model-chosen write through the six file tools lands inside the session's write roots, with zero files outside them in the before and after listings

  @TC-21s4 @DH-E-FS-2 @platform:windows
  Scenario: DH-E-FS-2 on Windows
    Given the Windows variant machine on "NTFS" with the signed-in Claude Code CLI installed natively, git and an editor save script
    And "dsh sandbox status" on the variant reads state "Unconfined", recorded in the row
    When the long-running FS-2 run completes on the variant under mode "workspace-write"
    Then the variant's evidence row records sandbox state "Unconfined" and the fence under test "C32 filesystem seam"
    And the variant's DH-E-FS-2 row reads passed for the binary it ran
    And every task of the run's shard passed
    And no file appears outside the write roots in the before and after listings

  @TC-21s4 @DH-E-VIEW-5 @platform:linux-wsl
  Scenario: DH-E-VIEW-5 on Linux
    Given the tester's Linux machine with the signed-in route, a real git repository and bwrap
    And "dsh sandbox status" on the variant reads backend "bwrap" state "Full"
    When VIEW-5 runs on the variant under "workspace-write"
    Then the variant's evidence row records sandbox state "Full" and the fence under test "bwrap Full"
    And the variant's DH-E-VIEW-5 row reads passed for the binary it ran
    And every model-chosen write lands inside the session's working directory
