@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-search-and-file-watching-on-real-systems @setup-inline
Feature: Search and file watching on real systems (DH-E-RIPGREP-1, -2, DH-E-CHOKIDAR-1, -2)
  Stage 8.7 of harness:DSH-M2 (impl.md 18.7 and 18.8) runs the search and watch
  scenarios on real operating systems: the in-process search against rg and Git
  on real checkouts with no external rg spawned, and the watch service against
  real editor saves, network shares and cross-OS directories (TC-21s3).

  # proof: service:filesystem. Each scenario runs the product's own, unmodified
  # scenario tests (internal/testkit/scenarios, -tags scenario) on each variant:
  # this host when its OS matches, else the default WSL distribution through
  # wsl.exe from a Windows host. A variant that cannot be reached, or lacks a
  # dependency, fails the scenario; it is never skipped. When the operator sets a
  # scenario's inputs (DSH_SCENARIO_REPOS, DSH_SCENARIO_EDITORS,
  # DSH_SCENARIO_REMOTE_DIRS with DSH_SCENARIO_REMOTE_CHANGE, ...; a
  # DSH_S18_<LINUX|WINDOWS>_<NAME without DSH_> overrides one per variant) they are
  # the provided instance. Else the suite stands up an ephemeral instance on the
  # variant's real volumes: a Git clone of this repository (a real public
  # checkout) on the variant's own filesystem, read through once so it is at rest
  # as a working checkout is; rg for the comparison only, the
  # variant's own or a pinned, checksum-verified ripgrep release; saves by vim, by
  # rename, in place and backup-then-write; and, for the network and cross-OS
  # directories, this host's loopback SMB share and a WSL directory seen from
  # Windows on the Windows variant and a Windows directory seen from WSL on the
  # Linux variant, each changed from its other side. The verdict is the scenario
  # tests' own rows (DSH_SCENARIO_REPORT) read back, plus the timing report and
  # the log lines that show each clause ran; each leg keeps an evidence row
  # (DSH_S18_EVIDENCE_DIR).

  @TC-21s3 @DH-E-RIPGREP-1 @DH-E-RIPGREP-2 @platform:linux-wsl @platform:windows
  Scenario: Search on real trees
    Given the Linux and Windows variants, each with real Git checkouts on its own filesystem and rg for comparison only
    When RIPGREP-1 and RIPGREP-2 run on each variant with the search budget "3s"
    Then each variant's "DH-E-RIPGREP-1" and "DH-E-RIPGREP-2" rows read passed on its OS
    And on each variant the timing report has every search of the set, its first matches within the budget and its files equal to rg's
    And on each variant the file listing equals Git's with no unexplained difference
    And no external rg was spawned on any variant

  @TC-21s3 @DH-E-CHOKIDAR-1 @DH-E-CHOKIDAR-2 @platform:linux-wsl @platform:windows
  Scenario: Watching on real trees
    Given the same variants, each with editor saves by vim, rename, in place and backup-then-write
    And on each variant network or cross-OS directories that are changed from their other side
    When CHOKIDAR-1 and CHOKIDAR-2 run on each variant
    Then each variant's "DH-E-CHOKIDAR-1" and "DH-E-CHOKIDAR-2" rows read passed on its OS
    And on each variant every save was reported exactly once within "1s"
    And on each variant the create, burst, rename and delete of a watched tree were each reported exactly once within the bound
    And on each variant every network or cross-OS directory was watched in poll mode
