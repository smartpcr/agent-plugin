@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-search-and-file-watching-on-real-systems @setup-inline
Feature: Search and file watching on real systems (DH-E-RIPGREP-1, -2, DH-E-CHOKIDAR-1, -2)
  Stage 8.7 of harness:DSH-M2 (impl.md 18.7 and 18.8) runs the search and watch
  scenarios on real operating systems: the in-process search against rg and Git
  on real checkouts with no external rg spawned, and the watch service against
  real editor saves, network filesystems and cross-OS directories (TC-21s3).

  # proof: service:filesystem. Each scenario runs the product's own, unmodified
  # scenario tests (internal/testkit/scenarios, -tags scenario) on each variant:
  # this host when its OS matches, else the default WSL distribution through
  # wsl.exe from a Windows host. The variants this host reaches run and are
  # asserted; a variant it cannot reach fails the scenario in its last step, and
  # a missing dependency fails the step that needs it. Nothing is skipped. When the operator sets a
  # scenario's inputs (DSH_SCENARIO_REPOS, DSH_SCENARIO_EDITORS,
  # DSH_SCENARIO_REMOTE_DIRS with DSH_SCENARIO_REMOTE_CHANGE, ...; a
  # DSH_S18_<LINUX|WINDOWS>_<NAME without DSH_> overrides one per variant) they are
  # the provided instance, e.g. an SMB share or NFS mount per
  # evidence/runbook-smb-share.md. Else the suite stands up an ephemeral instance
  # on the variant's real volumes, private to the run: it installs nothing and
  # starts, stops or reconfigures no system service. For search: three Git checkouts on the variant's own
  # filesystem, each read through once so it is at rest as a working checkout
  # is -- small, fsnotify v1.10.1 (pinned commit, under 1,000 files); medium,
  # this repository at HEAD; very large, one commit holding side-by-side copies
  # of this repository's HEAD tree, at least 100,000 files (the size the budget
  # is agreed on) -- and rg for the comparison only, the variant's own or a
  # pinned, checksum-verified ripgrep release. For watching: saves by vim, by
  # rename, in place and backup-then-write; a real SMB share, served by Samba's
  # smbd run as the Linux user with its own configuration, user database and
  # share directory on a free port (the Windows variant maps it from WSL with
  # New-SmbMapping as the signed-in user, no administrator); on Linux a real
  # NFSv3 export the suite serves on loopback; the Linux variant mounts both with
  # the kernel's CIFS and NFS clients, which mount only as root, so mount and
  # umount alone run as root (wsl.exe -u root, or sudo -n), on the run's own
  # mount points; and a cross-OS directory (a Windows one seen from WSL, a WSL
  # one seen from Windows through \\wsl.localhost). Each change is written from
  # the other side: behind the server, on its own filesystem, or across WSL
  # interop. The verdict is the
  # scenario tests' own rows (DSH_SCENARIO_REPORT) read back, plus the timing
  # report and the log lines that show each clause ran; each leg keeps an
  # evidence row (DSH_S18_EVIDENCE_DIR).

  @TC-21s3 @DH-E-RIPGREP-1 @DH-E-RIPGREP-2 @platform:linux-wsl @platform:windows
  Scenario: Search on real trees
    Given the Linux and Windows variants, each with real Git checkouts of a small, a medium and a very large public repository on its own filesystem and rg for comparison only
    When RIPGREP-1 and RIPGREP-2 run on each variant with the search budget "3s"
    Then each variant's "DH-E-RIPGREP-1" and "DH-E-RIPGREP-2" rows read passed on its OS
    And on each variant the searched checkouts were a small one under 1000 files, a medium one and a very large one of at least 100000 files
    And on each variant the timing report has every search of the set, its first matches within the budget and its files equal to rg's
    And on each variant the file listing equals Git's with no unexplained difference
    And no external rg was spawned on any variant
    And both the Linux and the Windows variant were reached and ran

  @TC-21s3 @DH-E-CHOKIDAR-1 @DH-E-CHOKIDAR-2 @platform:linux-wsl @platform:windows
  Scenario: Watching on real trees
    Given the same variants, each with editor saves by vim, rename, in place and backup-then-write
    And on each variant a real SMB share, on Linux a real NFS mount too, and a cross-OS directory, each served by the suite and changed from its other side
    When CHOKIDAR-1 and CHOKIDAR-2 run on each variant
    Then each variant's "DH-E-CHOKIDAR-1" and "DH-E-CHOKIDAR-2" rows read passed on its OS
    And on each variant every save was reported exactly once within "1s"
    And on each variant the create, burst, rename and delete of a watched tree were each reported exactly once within the bound
    And on each variant every network and cross-OS directory was watched in poll mode
    And both the Linux and the Windows variant were reached and ran
