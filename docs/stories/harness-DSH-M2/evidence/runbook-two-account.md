# DH-S-18 manual runbook: the two-account machine (DH-E-STORAGE-6)

Status: **runbook written (implementation-plan Stage 8.1), not run.** Nothing in this file
is evidence until the record below is filled from a real run. Stage 8.8 took it up on
`7b5133d` and could not run it: see [Stage 8.8 blockers](DH-S-18.md#stage-88-blockers)
(no dsh path seeds an attachment or a spill file; the home has no `cache/`). The result goes to the
`linux-wsl:two-account` and `windows-host:two-account` rows of [DH-S-18.md](DH-S-18.md);
the variants are listed in [variants.md](variants.md).

This is the provisioning and run of the STORAGE-6 machine (tech-spec sec. 3.4 item 7,
M2-EN-4, M2-EN-5). The scenario is `TestAnotherLocalUserCannotReadStoredData`
(`src/harness/internal/testkit/scenarios/storage_private_scenario_test.go`, build tag
`scenario`).

## What the check proves, and what it does not

dsh seeds an attachment and a spill file as the runner's account. A **second ordinary
local account** then lists and reads the blobs, spill and cache directories: every request
must be denied. On POSIX the spill directory is then made group-writable, and the spill
component must refuse to start naming the directory while the rest of the host runs.

It proves the file modes and ACLs dsh sets keep another ordinary account out on this OS.
It does not prove anything against an administrator or root, who can read any file, nor
against a second account that shares the first one's groups by mistake (step 1 checks it
does not).

## Who, where, and with what

- **Who.** The tester (Linux under WSL) or a team member who keeps the Windows host. Never
  an agent session.
- **Where.** The tester's WSL machine (`[self-hosted, linux, dsh-wsl]`) and the Windows host
  (`[self-hosted, windows, dsh-windows-host]`). macOS is out of scope
  (`platforms-windows-wsl`).
- **Credential.** The second account's password, from `DSH_LAB_SECOND_USER_CRED`; it is
  used by the list and read commands only, never written to the record.
- **Kept account.** The second account is kept between runs; the run removes only the data
  it seeded.

## Steps

1. **Create, or check, the second account** (the "Second account checked" preflight). It
   must be an ordinary account, not the runner's own, and share no group with it:

   ```bash
   sudo useradd --create-home --user-group dsh-s18-other   # once
   id dsh-s18-other; id "$USER"                             # no shared group
   ```

   ```powershell
   New-LocalUser -Name dsh-s18-other -Password (Read-Host -AsSecureString)   # once
   Get-LocalGroupMember Administrators | Where-Object Name -like '*dsh-s18-other'   # must print nothing
   ```

2. **Set the inputs** (`{dsh}`, `{home}`, `{path}`, `{user}` and `{log}` are filled in by the
   test):

   | Variable | Value |
   |---|---|
   | `DSH_SCENARIO_STORAGE6_USER` | `dsh-s18-other` |
   | `DSH_SCENARIO_STORAGE6_PUBLIC` | a directory both accounts can traverse and list |
   | `DSH_SCENARIO_STORAGE6_SEED` | command storing, through `{dsh}` on `{home}`, an attachment and a spill file |
   | `DSH_SCENARIO_STORAGE6_LIST` | command listing `{path}` as `{user}`, non-zero when denied (`sudo -n -u {user} ls {path}`; on Windows a `Start-Process -Credential` wrapper) |
   | `DSH_SCENARIO_STORAGE6_READ` | command printing `{path}` as `{user}`, non-zero when denied |
   | `DSH_SCENARIO_STORAGE6_START` | POSIX only: command starting the host on `{home}` in the background, log to `{log}` |
   | `DSH_SCENARIO_STORAGE6_STATUS` | POSIX only: command exiting 0 when the host on `{home}` runs |
   | `DSH_SCENARIO_STORAGE6_STOP` | POSIX only: command stopping the host on `{home}` |

3. **Run** from the repository root:

   ```bash
   GOWORK=off go -C src/harness test -tags scenario -p 1 -count=1 -v -run 'TestAnotherLocalUserCannotReadStoredData' ./internal/testkit/scenarios/
   ```

   or dispatch `harness-scenarios.yml` with `stage: DH-S-18` and let the `*:two-account`
   s18 jobs run.
4. **Clean up.** The test removes the seeded data and restores the spill directory mode.
   Check that no file of the run is left under the scratch home, and keep the account.

## Record

| Field | Value |
|---|---|
| Date | |
| Operator | |
| Commit | |
| Machine and OS build | |
| Second account and its groups (`id`, or `Get-LocalUser` and group memberships) | |

| Variant | Expected | Printed lines (paste unchanged) | Result |
|---|---|---|---|
| `linux-wsl:two-account` | every list and read denied; the group-writable spill directory refused by name while the host runs | | |
| `windows-host:two-account` | every list and read denied | | |

A result is `passed`, `failed` or `not run (second-account preflight failed)`; a machine
without a second account is never a pass.
