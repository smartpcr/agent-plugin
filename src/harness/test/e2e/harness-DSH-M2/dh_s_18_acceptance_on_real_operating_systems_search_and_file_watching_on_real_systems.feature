@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-search-and-file-watching-on-real-systems @setup-inline
Feature: Search and file watching on real systems (DH-E-RIPGREP-1, -2, DH-E-CHOKIDAR-1, -2)
  Stage 8.7 of harness:DSH-M2 (impl.md 18.7 and 18.8) runs the search and watch
  scenarios on real operating systems: the in-process search against rg and Git
  on real checkouts with no external rg spawned, and the watch service against
  real editor saves, network filesystems and cross-OS directories (TC-21s3).

  # proof: service:filesystem. Each scenario runs the product's own, unmodified
  # scenario tests (internal/testkit/scenarios, -tags scenario) on each variant:
  # this host when its OS matches, else the default WSL distribution through
  # wsl.exe from a Windows host. The variant of this host's OS must run, and
  # the other one too wherever this host reaches it (a Windows host reaches
  # Linux through WSL). A variant this host cannot reach at all (native Linux
  # has no Windows; a Windows runner without WSL has no Linux) runs this same
  # suite on its own variant runner (variants.md: windows-hosted, linux-wsl);
  # DSH_S18_BOTH=1 makes this host run both. Nothing is skipped or stood in
  # for. When the operator sets a scenario's inputs
  # (DSH_SCENARIO_REPOS, DSH_SCENARIO_EDITORS, DSH_SCENARIO_REMOTE_DIRS with
  # DSH_SCENARIO_REMOTE_CHANGE, ...; a DSH_S18_<LINUX|WINDOWS>_<NAME without
  # DSH_> overrides one per variant) they are the provided instance. Else the
  # suite stands up an ephemeral instance on the variant's real volumes, private
  # to the run, as the run's own user: it installs nothing, runs nothing as
  # root and starts, stops or reconfigures no system service. For search: three
  # Git checkouts on the variant's own filesystem, each read through once so it
  # is at rest as a working checkout is -- small, fsnotify v1.10.1 (pinned
  # commit, under 1,000 files); medium, this repository at HEAD; very large, one
  # commit holding side-by-side copies of this repository's HEAD tree, at least
  # 100,000 files (the size the budget is agreed on) -- and rg for the
  # comparison only, the variant's own or a pinned, checksum-verified ripgrep
  # release. For watching: saves by vim, by rename, in place and
  # backup-then-write. The SMB share, and on Linux the NFS mount, are the
  # operator's, kept per evidence/runbook-smb-share.md on the self-hosted
  # linux-wsl:smb and windows-host:smb variants (variants.md): mounted by the
  # operator, whose sudo grant covers mount and umount of those shares only,
  # and handed to the suite as DSH_SCENARIO_REMOTE_DIRS with
  # DSH_SCENARIO_REMOTE_CHANGE. The suite serves and mounts no share. Each
  # operator directory is classified by the filesystem its OS reports, and the
  # variant must watch every kind it holds (Linux: SMB, NFS and, under WSL, the
  # Windows directory; Windows: SMB). On any other variant the suite provides
  # the cross-OS directory alone (a Windows one seen from WSL under /mnt, a WSL
  # one seen from Windows through \\wsl.localhost), changed from its other side
  # across WSL interop; a variant labelled *:smb (DSH_S18_<LINUX|WINDOWS>_VARIANT)
  # without its shares fails the share preflight. A runner with neither (native
  # Linux, Windows without WSL) runs CHOKIDAR-1, and CHOKIDAR-2 runs on the
  # *:smb variants variants.md assigns it to. The verdict is the scenario
  # tests' own rows (DSH_SCENARIO_REPORT) read back, plus the timing report and
  # the log lines that show each clause ran; each leg keeps an evidence row
  # (DSH_S18_EVIDENCE_DIR).

  @TC-21s3 @DH-E-RIPGREP-1 @DH-E-RIPGREP-2 @platform:linux-wsl @platform:windows
  Scenario: Search on real trees
    Given the Linux and Windows variants, each with real Git checkouts of a small, a medium and a very large public repository on its own filesystem and rg for comparison only
    When RIPGREP-1 and RIPGREP-2 run on each variant with the search budget "3s"
    Then each variant's "DH-E-RIPGREP-1" and "DH-E-RIPGREP-2" rows read passed on its OS
    And on each variant the searched checkouts were a small one under 1000 files, a medium one and a very large one of at least 100000 files
    And on each variant the timing report has every search of the set, its first matches within the budget and its files equal to rg's
    And on each variant the file listing equals Git's with no unexplained difference
    And no external rg was spawned on any variant
    And the variant of this host's OS ran, and the other variant too wherever this host reaches it

  @TC-21s3 @DH-E-CHOKIDAR-1 @DH-E-CHOKIDAR-2 @platform:linux-wsl @platform:windows
  Scenario: Watching on real trees
    Given the same variants, each with editor saves by vim, rename, in place and backup-then-write
    And on each variant its remote directories, the operator's SMB share and on Linux NFS mount on the smb variants and a cross-OS directory, each changed from its other side
    When CHOKIDAR-1 and CHOKIDAR-2 run on each variant
    Then each variant's "DH-E-CHOKIDAR-1" and "DH-E-CHOKIDAR-2" rows read passed on its OS
    And on each variant every save was reported exactly once within "1s"
    And on each variant the create, burst, rename and delete of a watched tree were each reported exactly once within the bound
    And on each variant every network and cross-OS directory was watched in poll mode
    And the variant of this host's OS ran, and the other variant too wherever this host reaches it
