# DH-S-18 manual runbook: the SMB share (DH-E-FS-1, DH-E-FS-2, DH-E-CHOKIDAR-2)

Status: **runbook written (implementation-plan Stage 8.1), not run.** Nothing in this file
is evidence until the record below is filled from a real run. The results go to the
`linux-wsl:smb` and `windows-host:smb` rows of [DH-S-18.md](DH-S-18.md); the variants are
listed in [variants.md](variants.md).

This is the provisioning and run of the share variants (tech-spec sec. 3.4 item 7,
M2-EN-4, M2-EN-5). The scenarios are `TestFenceHoldsUnderALinkSwap`
(`src/harness/internal/testkit/scenarios/fs_fence_scenario_test.go`, DH-E-FS-1 on
`DSH_SCENARIO_FS1_SHARE`), DH-E-FS-2 (a long signed-in Claude Code session whose working
directory is on the share, Windows host only) and DH-E-CHOKIDAR-2 (the watcher polls
network and cross-OS directories).

## What the check proves, and what it does not

- **FS-1.** The write fence holds while a link in the path is being swapped, for whole
  cycles of three and at least 10,000 of them, on a directory of the SMB share.
- **FS-2.** A long real-model session with its working directory on the share keeps every
  write inside its roots.
- **CHOKIDAR-2.** On SMB, NFS and a Windows directory seen from WSL, the watcher reports
  Poll mode and sees a change within two poll intervals.

They prove the behaviour on this share server, protocol version and mount options. They do
not prove other servers or options; record them so a later run can be compared.

## Who, where, and with what

- **Who.** The tester (WSL) or a team member who keeps the Windows host and the share
  server. Never an agent session.
- **Where.** The tester's WSL machine (`[self-hosted, linux, dsh-wsl]`) and the Windows host
  (`[self-hosted, windows, dsh-windows-host]`). macOS is out of scope
  (`platforms-windows-wsl`).
- **Share.** A team-kept SMB 3 share with a scratch directory the runner's account can
  write; an NFS export for CHOKIDAR-2; on WSL, a directory under `/mnt/c`.
- **Credential.** The share account only. FS-2 uses the Windows host's own signed-in
  Claude Code (`claude auth login` done as that user); no model key.

## Steps

1. **Mount the shares** (the "Share mounts checked" preflight: each directory exists and
   is writable):

   ```bash
   sudo mount -t cifs //share-host/dsh-s18 /mnt/dsh-smb -o vers=3.1.1,uid=$(id -u),gid=$(id -g),credentials=$HOME/.dsh-smb-cred
   sudo mount -t nfs share-host:/dsh-s18 /mnt/dsh-nfs
   mkdir -p /mnt/c/Users/$USER/dsh-s18 && test -w /mnt/dsh-smb && test -w /mnt/dsh-nfs
   ```

   ```powershell
   New-SmbMapping -LocalPath S: -RemotePath \\share-host\dsh-s18 -Persistent $false
   Test-Path S:\ ; New-Item -ItemType Directory S:\scratch -Force
   ```

2. **Set the inputs:**

   | Variable | Value |
   |---|---|
   | `DSH_SCENARIO_FS1_SHARE` | a base directory on the SMB share (`/mnt/dsh-smb/scratch`, `S:\scratch`) |
   | `DSH_SCENARIO_WATCH_SHARES` | the SMB and NFS directories and, on WSL, the `/mnt/c` directory, `;`-separated |

3. **Run** from the repository root (FS-2 on the Windows host only):

   ```bash
   GOWORK=off go -C src/harness test -tags scenario -p 1 -count=1 -v -run 'TestFenceHoldsUnderALinkSwap' ./internal/testkit/scenarios/
   ```

   or dispatch `harness-scenarios.yml` with `stage: DH-S-18` and let the `*:smb` s18 jobs
   run every scenario `harness-gate require --stage DH-S-18 --variant <label>` prints.
4. **Clean up.** Remove the scratch directories from the share and unmount
   (`sudo umount /mnt/dsh-smb /mnt/dsh-nfs`; `Remove-SmbMapping S: -Force`).

## Record

| Field | Value |
|---|---|
| Date | |
| Operator | |
| Commit | |
| Share server, SMB dialect (`Get-SmbConnection` or `/proc/fs/cifs/DebugData`), NFS version | |
| Mount options | |
| `claude --version` (Windows host, FS-2) | |

| Variant | Scenario | Expected | Printed lines (paste unchanged) | Result |
|---|---|---|---|---|
| `linux-wsl:smb` | DH-E-FS-1 | fence held for every cycle on the SMB directory | | |
| `linux-wsl:smb` | DH-E-CHOKIDAR-2 | Poll mode, change seen within two intervals on SMB, NFS and `/mnt/c` | | |
| `windows-host:smb` | DH-E-FS-1 | as on Linux | | |
| `windows-host:smb` | DH-E-FS-2 | every write of the session inside its roots | | |
| `windows-host:smb` | DH-E-CHOKIDAR-2 | Poll mode, change seen within two intervals | | |

A result is `passed`, `failed` or `not run (share preflight failed)`; a missing share is
never a pass.
