@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-release-rehearsal-on-every-platform @setup-inline
Feature: Release rehearsal on every platform (DH-E-TOOLCHAIN-2)
  Stage 8.2 of harness:DSH-M2 runs DH-E-TOOLCHAIN-2 first in DH-S-18 (impl.md
  18.1): "harness-gate rehearse" runs the pinned GoReleaser on
  .goreleaser.dsh.yaml in snapshot mode, nothing published, and records one line
  per binary: "<sha256>  dsh-<os>-<arch>[.exe]  version=... vcs.revision=...
  vcs.modified=...". Each archive is then unpacked again, independently of the
  rehearsal, checked against checksums.txt and the record, and its dsh runs
  "--version" on a real operating system (TC-21s3).

  # proof: service:os-process. The rehearsal is the real one (tools/harness-gate
  # rehearse, pinned GoReleaser, the tag list compared before and after), run once
  # per suite into a scratch directory. The Linux variant is this host when it is
  # Linux, else the default WSL distribution through wsl.exe; the Windows host is
  # this host when it is Windows, else the Windows side of WSL interop. A variant
  # that cannot be reached fails the scenario; it is never skipped. Each system also
  # runs one keyless headless replay scenario (TC-21s3); while the rehearsal reports
  # that clause OPEN (ReplayCheck nil) the scenario fails, so DH-E-TOOLCHAIN-2 is never
  # recorded as passing with the replay unproven.
  Background:
    Given the release rehearsal of this checkout ran with nothing published

  @TC-21s3 @DH-E-TOOLCHAIN-2 @platform:linux-wsl
  Scenario: DH-E-TOOLCHAIN-2 on Linux
    Given a Linux variant
    When the rehearsal archive for the variant is unpacked and "dsh --version" runs on it
    Then the archive's SHA-256 matches its line in checksums.txt
    And the unpacked binary's SHA-256 matches the rehearsal record
    And "dsh --version" exits 0
    And it prints the embedded version matching the record
    And it names the recorded revision of the checked-out commit and the variant's platform
    And the rehearsal reports the keyless headless replay of the variant's binary passed, not OPEN

  @TC-21s3 @DH-E-TOOLCHAIN-2 @platform:windows
  Scenario: DH-E-TOOLCHAIN-2 on Windows
    Given the Windows host
    When the rehearsal archive for the variant is unpacked and "dsh --version" runs on it
    Then the archive's SHA-256 matches its line in checksums.txt
    And the unpacked binary's SHA-256 matches the rehearsal record
    And "dsh --version" exits 0
    And it prints the embedded version matching the record
    And it names the recorded revision of the checked-out commit and the variant's platform
    And the rehearsal reports the keyless headless replay of the variant's binary passed, not OPEN

  # Negative path: the checksum preflight has teeth. A copy of an archive with one
  # byte changed is refused before anything of it runs.
  Scenario Outline: A tampered <os> archive is refused before its binary runs
    Given a copy of the rehearsal's <os> archive with one byte changed
    When the copy is checked against checksums.txt
    Then the check refuses it, naming both digests
    And no binary of the copy is unpacked

    Examples:
      | os      |
      | linux   |
      | windows |
