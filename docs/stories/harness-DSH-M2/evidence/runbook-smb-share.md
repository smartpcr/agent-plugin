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

2. **Set the inputs.** `harness-gate require --root ../.. --stage DH-S-18 --variant <label>
   --inputs` (run in `tools/harness-gate`) prints every input the variant's scenario tests
   read; the s18 job fails before any scenario when one is unset. The share-specific ones:

   | Variable | Value |
   |---|---|
   | `DSH_SCENARIO_FS1_SHARE` | a base directory on the SMB share (`/mnt/dsh-smb/scratch`, `S:\scratch`) |
   | `DSH_SCENARIO_FS1_DIRS`, `_LAYER`, `_ROUTE`, `_MODEL` | FS-1's local directories and scripted route (`fs_fence_scenario_test.go`) |
   | `DSH_SCENARIO_REMOTE_DIRS` | CHOKIDAR-2: the SMB and NFS directories and, on WSL, the `/mnt/c` directory, `;`-separated, as this machine sees them |
   | `DSH_SCENARIO_REMOTE_CHANGE` | CHOKIDAR-2: a shell command changing `{name}` in `{dir}` from the other machine or side, e.g. `ssh share-host "echo {marker} >> /srv/dsh-s18/$(basename {dir})/{name}"`, or on WSL for `/mnt/c` `cmd.exe /c "echo {marker}>> C:\Users\%USERNAME%\dsh-s18\{name}"` |
   | `DSH_SCENARIO_FS_CHECKOUTS`, `DSH_SCENARIO_FS2_TASKS`, `_CHILDREN`, `_LAYER`, `_SHARD` | FS-2 (Windows host): the repositories, the 20 tasks, the descendant-process command, the layer and the shard (`fs_long_scenario_test.go`) |

   The `share` preflight checks `DSH_SCENARIO_FS1_SHARE` is writable and, for each
   `DSH_SCENARIO_REMOTE_DIRS` directory, writes a probe file, runs
   `DSH_SCENARIO_REMOTE_CHANGE` on it and fails unless the marker shows on this side.

3. **Run** from the repository root. FS-1 and CHOKIDAR-2 (both machines):

   ```bash
   GOWORK=off go -C src/harness test -tags scenario -p 1 -count=1 -v -run 'TestFenceHoldsUnderALinkSwap|TestWatchRemoteFilesystemsPoll' ./internal/testkit/scenarios/
   ```

   FS-2 on the Windows host, its checkouts on the share: FS-2 clones each task's checkout
   into `t.TempDir()`, so the run points the temporary directory at the share and keeps
   the go build local (`GOTMPDIR`); the `windows-host:smb` job does the same (`scratch:
   share`):

   ```bash
   tmp="$(cygpath -w "$DSH_SCENARIO_FS1_SHARE")\\s18-tmp"; mkdir -p "$tmp"
   TMP="$tmp" TEMP="$tmp" GOTMPDIR="$(cygpath -w "$RUNNER_TEMP")" GOWORK=off go -C src/harness test -tags scenario -p 1 -count=1 -timeout 175m -v -run 'TestLongSessionCorrectsItsOwnPatterns' ./internal/testkit/scenarios/
   ```

   (in PowerShell: `$env:TMP=$env:TEMP='S:\scratch\s18-tmp'; $env:GOTMPDIR="$env:LOCALAPPDATA\Temp"`).
   Record in the table below that the clone paths in the log are under the share.
   Or dispatch `harness-scenarios.yml` with `stage: DH-S-18` and let the `*:smb` s18 jobs
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
