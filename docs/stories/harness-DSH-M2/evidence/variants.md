# DH-S-18 variant fleet (E2)

Status: **fleet listed (implementation-plan Stage 8.1), not run.** The rows below are the
machines the S18 acceptance runs on. Nothing here is evidence: each variant's result is
recorded in [DH-S-18.md](DH-S-18.md) from a real run.

This file lists every S18 variant of impl.md sec. 7.1 E2 (M2-EN-2, M2-EN-3): the Linux
distributions, kernels with and without Landlock, cgroup v2 delegated and not,
`systemd --user` present and absent, unprivileged user namespaces allowed and denied, and
Windows native. Per Q `m2-e2-fleet` (M2-Q-3) the **team** provides the machines and keeps
them; a variant without hardware is recorded `not run (<check> preflight failed)` and S18
stays open (M2-C-2). The E2 variants are named machines, never "whatever the runner is"
(architecture.md R-02). macOS rows are out of scope by owner decision
`platforms-windows-wsl` (M1 OWNER-1, Q `m2-macos-rows`).

`harness-gate variants` cross-references this file with the `DH-S-18` entry of
`test/harness/acceptance-index.yaml`: every scenario has at least one in-scope variant or
an owner decision removing it (M2-DOD-4). Each in-scope variant is one `s18` job of
`.github/workflows/harness-scenarios.yml`, which runs `harness-gate entry`, the variant's
preflight, and the scenarios `harness-gate require --stage DH-S-18 --variant <label>`
prints (this row's Scenarios, in index order).

Scenarios that compare machines (DH-E-SANDBOX-1, DH-E-NATIVESYSTEM-3 and -4, DH-E-EXEC-1,
DH-E-EXEC-10, DH-E-NODEFS-1) read every machine from one variable
(`DSH_SCENARIO_SANDBOX1_MACHINES`, `DSH_SCENARIO_EXEC1_*`, `DSH_SCENARIO_EXEC10_SSH*`,
`DSH_SCENARIO_VMS`), so they are listed under each variant they touch. A variant's job
preflights only its own machine, but the scenario test it then runs is the whole test
against every machine in that variable. So:

- **Shared machine sets.** DH-E-SANDBOX-1 needs the `bwrap=`, `landlock=` and
  `unconfined=` machines of `DSH_SCENARIO_SANDBOX1_MACHINES` in one test, and
  DH-E-EXEC-1 and DH-E-EXEC-10 need both the `DSH_SCENARIO_EXEC10_SSH` (systemd) and
  `DSH_SCENARIO_EXEC10_SSH_NOMANAGER` machines. If one machine of a set is down, the
  scenario fails in **every** variant that lists it (`linux-e2:bwrap`, `:landlock` and
  `:unconfined` for SANDBOX-1; `linux-e2:systemd` and `:no-user-manager` for EXEC-1/10),
  not only in the variant whose machine is down.
- **Repeated and overlapping runs.** The same multi-machine test runs once per listing
  variant (three times for SANDBOX-1, twice for EXEC-1/10). These `linux-e2:*` jobs have
  no `lock` (only the hypervisor variants share `lock: hypervisor`), and with
  `max-parallel: 4` the repeats can run at the same time against the same remote
  machines.

Recording rule for these scenarios (M2-C-2): a variant whose own preflight fails is
recorded `not run (<check> preflight failed)`. A shared-set scenario that fails because
another machine of its set is down, or because repeats overlapped, is recorded
`failed (<machine> unavailable)` or `failed (overlapping run)` against **each** variant
that ran it, naming the machine, never `not run` for that variant alone and never a pass.
A shared-set scenario counts as passed only when every listing variant's run of it passed
in the same workflow run; otherwise it stays open and S18 stays open.

The manual variants have runbooks: [power-cut host](runbook-power-cut.md),
[two-account machine](runbook-two-account.md) and [SMB share](runbook-smb-share.md).

## Variants

| Variant | Platform | Runner | Provider | Service | Preflight | Scenarios | Scope |
|---|---|---|---|---|---|---|---|
| `linux-wsl` | Ubuntu 24.04 under WSL2, bwrap installed, user namespaces allowed | `self-hosted, linux, dsh-wsl` | the tester's own machine, signed in to Claude Code | `service:linux-sandbox-bwrap` + `service:model-route-claude-code` + `service:git-and-editor` + `service:go-toolchain` | bwrap Full | DH-E-TOOLCHAIN-2, DH-E-CHOKIDAR-1, DH-E-RIPGREP-1, DH-E-RIPGREP-2, DH-E-HOST-4, DH-E-FS-1, DH-E-FS-2, DH-E-VIEW-5, DH-E-CONTEXT-2 | in scope |
| `linux-wsl:two-account` | Ubuntu 24.04 under WSL2 with a second local account | `self-hosted, linux, dsh-wsl` | the tester's own machine, second account per the two-account runbook | `service:os-process` | Second account checked | DH-E-STORAGE-6 | in scope |
| `linux-wsl:smb` | Ubuntu 24.04 under WSL2 with an SMB and an NFS share mounted, and a Windows directory | `self-hosted, linux, dsh-wsl` | the tester's own machine, shares per the SMB share runbook | `service:filesystem` | Share mounts checked; remote change seen | DH-E-FS-1, DH-E-CHOKIDAR-2 | in scope |
| `linux-e2:bwrap` | Debian 12, kernel 6.1, bwrap installed, user namespaces allowed | `self-hosted, linux, dsh-e2-controller` | team-kept Linux VM over SSH | `service:linux-sandbox-bwrap` | bwrap Full | DH-E-SANDBOX-1 | in scope |
| `linux-e2:landlock` | Fedora 40, kernel 6.8, Landlock ABI >= 5, no bwrap, user namespaces denied | `self-hosted, linux, dsh-e2-controller` | team-kept Linux VM over SSH | `service:linux-landlock-only` | landlock | DH-E-SANDBOX-1, DH-E-NATIVESYSTEM-1, DH-E-NATIVESYSTEM-3, DH-E-NATIVESYSTEM-4 | in scope |
| `linux-e2:landlock-abi1-4` | Ubuntu 22.04, kernel 5.15, Landlock ABI between 1 and 4, no bwrap | `self-hosted, linux, dsh-e2-controller` | team-kept Linux VM over SSH | `service:linux-landlock-only` | landlock | DH-E-NATIVESYSTEM-2 | in scope |
| `linux-e2:unconfined` | Debian 12, kernel booted with Landlock disabled, no bwrap | `self-hosted, linux, dsh-e2-controller` | team-kept Linux VM over SSH | `service:linux-unconfined` | Unconfined | DH-E-SANDBOX-1, DH-E-NATIVESYSTEM-3 | in scope |
| `linux-e2:musl` | Alpine 3.20 (musl), Landlock ABI >= 5, no bwrap | `self-hosted, linux, dsh-e2-controller` | team-kept Linux VM over SSH | `service:linux-landlock-only` | landlock | DH-E-NATIVESYSTEM-4 | in scope |
| `linux-e2:systemd` | Debian 12, `systemd --user` running, cgroup v2 delegated to the user manager | `self-hosted, linux, dsh-e2-controller` | team-kept Linux VM over SSH, scratch account | `service:linux-systemd-cgroup` | `systemctl --user` state recorded | DH-E-EXEC-1, DH-E-EXEC-10 | in scope |
| `linux-e2:no-user-manager` | Debian 12, no `systemd --user` for the account, cgroup v2 not delegated | `self-hosted, linux, dsh-e2-controller` | team-kept Linux VM over SSH, scratch account without lingering | `service:os-process` | `systemctl --user` state recorded | DH-E-EXEC-1, DH-E-EXEC-10 | in scope |
| `windows-hosted` | Windows Server 2022, native | `windows-latest` | GitHub-hosted runner | `service:windows-host` + `service:git-and-editor` + `service:go-toolchain` | dsh checksum recorded | DH-E-TOOLCHAIN-2, DH-E-CHOKIDAR-1, DH-E-RIPGREP-1, DH-E-RIPGREP-2, DH-E-HOST-4, DH-E-FS-1, DH-E-CONTEXT-2 | in scope |
| `windows-host` | Windows 11, native, Claude Code installed and signed in | `self-hosted, windows, dsh-windows-host` | team-kept Windows machine | `service:windows-host` + `service:model-route-claude-code` + `service:git-and-editor` | dsh checksum recorded | DH-E-FS-2 | in scope |
| `windows-host:two-account` | Windows 11, native, with a second local account | `self-hosted, windows, dsh-windows-host` | team-kept Windows machine, second account per the two-account runbook | `service:windows-host` + `service:os-process` | Second account checked | DH-E-STORAGE-6 | in scope |
| `windows-host:smb` | Windows 11, native, with an SMB share mapped; FS-2's checkouts on the share (`scratch: share`) | `self-hosted, windows, dsh-windows-host` | team-kept Windows machine, share per the SMB share runbook | `service:windows-host` + `service:filesystem` + `service:model-route-claude-code` | Share mounts checked; remote change seen; temporary directory on the share | DH-E-FS-1, DH-E-FS-2, DH-E-CHOKIDAR-2 | in scope |
| `vm:linux-guest` | Ubuntu 24.04 guest on KVM/libvirt | `self-hosted, linux, dsh-hypervisor` | team-kept KVM host per the power-cut runbook | `service:vm-power-cut` + `service:ack-recorder` | Throw-away guest created from an overlay, cut hard, restarted and deleted, base images unchanged; test acknowledgment recorded and read back (`ack-probe`) | DH-E-NODEFS-1 | in scope |
| `vm:windows-guest` | Windows 11 guest on Hyper-V | `self-hosted, linux, dsh-hypervisor` | team-kept Hyper-V host per the power-cut runbook | `service:vm-power-cut` + `service:ack-recorder` | Throw-away guest created from an overlay, cut hard, restarted and deleted, base images unchanged; test acknowledgment recorded and read back (`ack-probe`) | DH-E-NODEFS-1 | in scope |
| `macos` | macOS | - | - | - | - | DH-E-TOOLCHAIN-2, DH-E-CHOKIDAR-1, DH-E-CHOKIDAR-2, DH-E-RIPGREP-1, DH-E-RIPGREP-2, DH-E-HOST-4, DH-E-STORAGE-6, DH-E-FS-1, DH-E-FS-2, DH-E-CONTEXT-2 | out of scope (platforms-windows-wsl) |
| `vm:macos-guest` | macOS guest | - | - | - | - | DH-E-NODEFS-1 | out of scope (platforms-windows-wsl) |

## Owner removals

No owner decision removes an S18 scenario: every one runs on at least one variant above.

| Scenario | Decision |
|---|---|
