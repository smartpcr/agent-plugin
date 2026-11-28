# DH-S-18 evidence (fixture: s18)

Closure status: open

Fixture of the DH-S-18 acceptance close (stageclose_test.go, `TestStageClosureS18Golden`): the live
record's Scenarios rows with every in-scope variant passed and every macOS variant out of scope by
decision `platforms-windows-wsl`. The test runs it against the committed acceptance index and
variants.md; the not-ready states set the `linux-e2:no-user-manager` row of DH-E-EXEC-10 to not run.

## Steps

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 18.1 | scenarios of step 18.1 | 0a1b2c3 |  |
| 18.2 | scenarios of step 18.2 | 0a1b2c3 |  |
| 18.3 | scenarios of step 18.3 | 0a1b2c3 |  |
| 18.4 | scenarios of step 18.4 | 0a1b2c3 |  |
| 18.5 | scenarios of step 18.5 | 0a1b2c3 |  |
| 18.6 | scenarios of step 18.6 | 0a1b2c3 |  |
| 18.7 | scenarios of step 18.7 | 0a1b2c3 |  |
| 18.8 | scenarios of step 18.8 | 0a1b2c3 |  |
| 18.9 | scenarios of step 18.9 | 0a1b2c3 |  |
| 18.10 | scenarios of step 18.10 | 0a1b2c3 |  |
| 18.11 | scenarios of step 18.11 | 0a1b2c3 |  |

## Scenarios

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DH-E-TOOLCHAIN-2 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-toolchain-2-linux-wsl |
| DH-E-TOOLCHAIN-2 | Windows | acceptance | required | E2 windows | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-toolchain-2-windows |
| DH-E-TOOLCHAIN-2 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-HOST-4 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-host-4-linux-wsl |
| DH-E-HOST-4 | Windows | acceptance | required | E2 windows | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-host-4-windows |
| DH-E-HOST-4 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-EXEC-1 | Linux (E2) | acceptance | required | E2 linux-e2 | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-exec-1-linux-e2 |
| DH-E-EXEC-1 | linux-e2:systemd | acceptance | required | E2 linux-e2-systemd | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-exec-1-linux-e2-systemd |
| DH-E-EXEC-1 | linux-e2:no-user-manager | acceptance | required | E2 linux-e2-no-user-manager | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-exec-1-linux-e2-no-user-manager |
| DH-E-EXEC-10 | Linux (E2, over SSH) | acceptance | required | E2 linux-e2-over-ssh | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-exec-10-linux-e2-over-ssh |
| DH-E-EXEC-10 | linux-e2:systemd | acceptance | required | E2 linux-e2-systemd | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-exec-10-linux-e2-systemd |
| DH-E-EXEC-10 | linux-e2:no-user-manager | acceptance | required | E2 linux-e2-no-user-manager | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-exec-10-linux-e2-no-user-manager |
| DH-E-NATIVESYSTEM-1 | Linux (E2) | acceptance | required | E2 linux-e2 | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-1-linux-e2; enforcement copied (Full) |
| DH-E-NATIVESYSTEM-1 | linux-e2:landlock | acceptance | required | E2 linux-e2-landlock | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-1-linux-e2-landlock; enforcement copied (Full) |
| DH-E-NATIVESYSTEM-2 | Linux (E2) | acceptance | required | E2 linux-e2 | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-2-linux-e2; enforcement copied (Full) |
| DH-E-NATIVESYSTEM-2 | linux-e2:landlock-abi1-4 | acceptance | required | E2 linux-e2-landlock-abi1-4 | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-2-linux-e2-landlock-abi1-4; enforcement copied (Full) |
| DH-E-NATIVESYSTEM-3 | Linux (E2) | acceptance | required | E2 linux-e2 | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-3-linux-e2; enforcement copied (Full) |
| DH-E-NATIVESYSTEM-3 | linux-e2:unconfined | acceptance | required | E2 linux-e2-unconfined | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-3-linux-e2-unconfined; enforcement copied (Full) |
| DH-E-NATIVESYSTEM-3 | linux-e2:landlock | acceptance | required | E2 linux-e2-landlock | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-3-linux-e2-landlock; enforcement copied (Full) |
| DH-E-NATIVESYSTEM-4 | Linux (E2) | acceptance | required | E2 linux-e2 | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-4-linux-e2; enforcement copied (Full) |
| DH-E-NATIVESYSTEM-4 | linux-e2:landlock | acceptance | required | E2 linux-e2-landlock | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-4-linux-e2-landlock; enforcement copied (Full) |
| DH-E-NATIVESYSTEM-4 | linux-e2:musl | acceptance | required | E2 linux-e2-musl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nativesystem-4-linux-e2-musl; enforcement copied (Full) |
| DH-E-SANDBOX-1 | Linux (E2) | acceptance | required | E2 linux-e2 | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-sandbox-1-linux-e2; enforcement copied (Full) |
| DH-E-SANDBOX-1 | linux-e2:bwrap | acceptance | required | E2 linux-e2-bwrap | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-sandbox-1-linux-e2-bwrap; enforcement copied (Full) |
| DH-E-SANDBOX-1 | linux-e2:landlock | acceptance | required | E2 linux-e2-landlock | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-sandbox-1-linux-e2-landlock; enforcement copied (Full) |
| DH-E-SANDBOX-1 | linux-e2:unconfined | acceptance | required | E2 linux-e2-unconfined | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-sandbox-1-linux-e2-unconfined; enforcement copied (Full) |
| DH-E-FS-1 | Linux ext4 | acceptance | required | E2 linux-ext4 | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-fs-1-linux-ext4 |
| DH-E-FS-1 | Windows NTFS | acceptance | required | E2 windows-ntfs | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-fs-1-windows-ntfs |
| DH-E-FS-1 | linux-wsl:smb | acceptance | required | E2 linux-wsl-smb | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-fs-1-linux-wsl-smb |
| DH-E-FS-1 | windows-host:smb | acceptance | required | E2 windows-host-smb | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-fs-1-windows-host-smb |
| DH-E-FS-1 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-FS-2 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-fs-2-linux-wsl |
| DH-E-FS-2 | Windows | acceptance | required | E2 windows | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-fs-2-windows |
| DH-E-FS-2 | windows-host:smb | acceptance | required | E2 windows-host-smb | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-fs-2-windows-host-smb |
| DH-E-FS-2 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-VIEW-5 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-view-5-linux-wsl |
| DH-E-RIPGREP-1 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-ripgrep-1-linux-wsl |
| DH-E-RIPGREP-1 | Windows | acceptance | required | E2 windows | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-ripgrep-1-windows |
| DH-E-RIPGREP-1 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-RIPGREP-2 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-ripgrep-2-linux-wsl |
| DH-E-RIPGREP-2 | Windows | acceptance | required | E2 windows | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-ripgrep-2-windows |
| DH-E-RIPGREP-2 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-CHOKIDAR-1 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-chokidar-1-linux-wsl |
| DH-E-CHOKIDAR-1 | Windows | acceptance | required | E2 windows | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-chokidar-1-windows |
| DH-E-CHOKIDAR-1 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-CHOKIDAR-2 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-chokidar-2-linux-wsl |
| DH-E-CHOKIDAR-2 | Windows | acceptance | required | E2 windows | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-chokidar-2-windows |
| DH-E-CHOKIDAR-2 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-CONTEXT-2 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-context-2-linux-wsl |
| DH-E-CONTEXT-2 | Windows | acceptance | required | E2 windows | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-context-2-windows |
| DH-E-CONTEXT-2 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-STORAGE-6 | Linux (WSL) | acceptance | required | E2 linux-wsl | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-storage-6-linux-wsl |
| DH-E-STORAGE-6 | Windows | acceptance | required | E2 windows | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-storage-6-windows |
| DH-E-STORAGE-6 | macOS | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |
| DH-E-NODEFS-1 | Linux guest | acceptance | required | E2 linux-guest | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nodefs-1-linux-guest |
| DH-E-NODEFS-1 | Windows guest | acceptance | required | E2 windows-guest | 0a1b2c3 | passed | dsh sha256 5f2c0e9a; run run-dh-e-nodefs-1-windows-guest |
| DH-E-NODEFS-1 | vm:macos-guest | acceptance | required |  |  | out of scope (platforms-windows-wsl) |  |

## Manual checks register

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |

## Owner defaults (M2-C-4)

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Fixture.

### Environment and fixture

Fixture.

### Exact tests and commands

Fixture.

### Run identifiers

Fixture.

### Results

Fixture.

### Manual checks

None.

### Cleanup evidence

Fixture.

### Open items

None.
