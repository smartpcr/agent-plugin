# DH-S-18 manual runbook: the power-cut host (DH-E-NODEFS-1)

Status: **runbook written (implementation-plan Stage 8.1), not run.** Nothing in this file
is evidence until the record below is filled from a real run. The result goes to the
`vm:linux-guest` and `vm:windows-guest` rows of [DH-S-18.md](DH-S-18.md); the variants are
listed in [variants.md](variants.md).

This is the provisioning and run of `service:vm-power-cut` and `service:ack-recorder`
(architecture.md provided services; tech-spec sec. 3.4 item 7, M2-EN-4, M2-EN-5). The
scenario is `TestDurableReplaceSurvivesPowerLoss`
(`src/harness/internal/testkit/scenarios/durable_replace_scenario_test.go`, build tag
`scenario`). It runs **last** in S18.

## What the check proves, and what it does not

Per guest OS, a writer in the guest replaces one file through `fsx.Replace` in a loop and
sends each acknowledged version to the recorder, the scenario process on the hypervisor
host, which is isolated from the guest's disk. The guest is hard powered off at a random
moment and restarted, 100 times. It passes when after every restart the file read through
the guest agent is complete and never older than the last acknowledged version
(`DH-LIB-NODEFS-R1`).

It proves durability of `fsx.Replace` against a power cut of a virtual machine on this
hypervisor, its virtual disk cache mode and this guest file system. It does not prove the
same on bare metal, another disk cache mode or another file system. A hosted runner or lane
machine cannot cut its own power, so no `service:os-process` substitute exists.

## Who, where, and with what

- **Who.** A team member who keeps the hypervisor hosts (Q `m2-e2-fleet`). Never an agent
  session.
- **Where.** The hypervisor hosts: a Linux KVM/libvirt host for the Linux guest, a Hyper-V
  host for the Windows guest, reached from the `[self-hosted, linux, dsh-hypervisor]`
  runner (or the operator's shell on it) over SSH (libvirt) and PowerShell remoting
  (Hyper-V). macOS guests are out of scope (`platforms-windows-wsl`).
- **Credentials.** No model key. Two kinds, kept apart:
  - *Host credential*, controller to host. KVM: the libvirt host's scratch account
    (`DSH_LAB_SSH_KEY`). Hyper-V: the SSH private key of the Hyper-V host's scratch
    account `s18lab` (`DSH_LAB_HYPERV_CRED`). That account is a member of *Hyper-V
    Administrators*, and the host runs OpenSSH Server with the PowerShell 7 `powershell`
    subsystem, so `pwsh` on the Linux controller reaches it noninteractively with
    `New-PSSession -HostName … -UserName s18lab -KeyFilePath …`.
  - *Guest credential*, host to guest. The Windows guest's local account, which is baked
    into the base image. PowerShell Direct (`Invoke-Command -VMName`) works only on the
    Hyper-V host itself and needs credentials of the guest, so it is always run inside
    the host session. The guest credential is stored once on the host, as `s18lab`:
    `Get-Credential | Export-Clixml C:\s18\guest-cred.xml`. The file is DPAPI-encrypted,
    readable only by `s18lab` on that host, and never leaves the host.
- **Base images.** One per guest OS, holding the built `dsh` test binary and the writer
  loop; record each image's SHA-256.
- **Time.** At most 3 h per variant (M2-EN-7); beyond that, shard the 100 cuts (M2-R-06).

## Steps

1. **Record the hosts.** Paste into the record: `virsh version` and the kernel of the KVM
   host; `Get-VMHost | Select-Object Name, VirtualMachineMigrationEnabled` and the build
   of the Hyper-V host; each base image's SHA-256 and the guest disk cache mode.
2. **Preflight the controller** (the `service:vm-power-cut` and `service:ack-recorder`
   preflights, before iteration 1). Per host, from the controller: record the base images'
   checksums, create a throw-away guest from a copy-on-write overlay, power it off hard,
   restart it, have it send a test acknowledgment that the recorder writes to its log and
   reads back, stop it and delete it, then check the base images are unchanged. Run it
   only while no `vm:*` s18 job runs (they hold the `hypervisor` lock for the same guests
   and recorder address). Linux guest on KVM/libvirt (`{addr}` is
   `DSH_SCENARIO_NODEFS_LISTEN`, here `192.168.122.1:47000`):

   ```bash
   sha256sum base-linux.qcow2 | tee s18-base.before
   qemu-img create -f qcow2 -F qcow2 -b base-linux.qcow2 /var/lib/libvirt/images/s18-probe.qcow2
   virt-install --import --name s18-probe --memory 1024 --disk /var/lib/libvirt/images/s18-probe.qcow2 --noautoconsole --os-variant generic
   virsh destroy s18-probe                       # hard power-off, no shutdown
   virsh start s18-probe                         # restart from the cut
   GOWORK=off go -C tools/harness-gate run . ack-probe --listen 192.168.122.1:47000 \
     --token s18-probe-ack --log s18-ack.log --timeout 2m \
     --send 'ssh s18-probe "echo s18-probe s18-probe-ack | nc -N 192.168.122.1 47000"'
   grep -F s18-probe-ack s18-ack.log             # the acknowledgment, read back
   virsh destroy s18-probe; virsh undefine s18-probe --remove-all-storage
   sha256sum -c s18-base.before                  # base image unchanged
   ```

   Windows guest on Hyper-V. Every step runs from the Linux controller in `pwsh` 7:
   - Hyper-V cmdlets run in an SSH remoting session on the host (host credential).
   - The guest is reached from inside that session over PowerShell Direct (guest
     credential).
   - The recorder runs on the controller at `10.20.0.5:47000`, the controller's address
     on the guest network (`DSH_SCENARIO_NODEFS_LISTEN` of the Windows variant).

   Save the acknowledgment sender on the controller as
   `/opt/dsh-s18/s18-probe-ack.ps1`. It takes the guest, the token and the recorder's
   `host:port` as parameters, so the manual cycle below and the `vm:*` job
   (`DSH_S18_PROBE_ACK`) run the same script:

   ```powershell
   param(
     [Parameter(Mandatory)][string] $Probe,   # guest name ({probe})
     [Parameter(Mandatory)][string] $Token,   # acknowledgment token ({token})
     [Parameter(Mandatory)][string] $Addr     # recorder host:port ({addr})
   )
   $ErrorActionPreference = 'Stop'
   $i = $Addr.LastIndexOf(':'); $rhost = $Addr.Substring(0, $i); $rport = [int]$Addr.Substring($i + 1)
   $hv = New-PSSession -HostName hyperv-host -UserName s18lab -KeyFilePath ~/.ssh/s18-hyperv
   try {
     Invoke-Command -Session $hv -ArgumentList $Probe, $Token, $rhost, $rport -ScriptBlock {
       param($probe, $token, $rhost, $rport)
       $guest = Import-Clixml C:\s18\guest-cred.xml        # guest credential, host-local
       Invoke-Command -VMName $probe -Credential $guest -ErrorAction Stop `
         -ArgumentList $probe, $token, $rhost, $rport -ScriptBlock {
           param($probe, $token, $rhost, $rport)
           $c = [Net.Sockets.TcpClient]::new($rhost, $rport)
           $w = [IO.StreamWriter]::new($c.GetStream()); $w.WriteLine("$probe $token"); $w.Close()
         }
     }
   } finally { Remove-PSSession $hv }
   ```

   Then run the cycle. Every failure stops it, and the probe guest is deleted in any case:

   ```powershell
   $ErrorActionPreference = 'Stop'
   $hv = New-PSSession -HostName hyperv-host -UserName s18lab -KeyFilePath ~/.ssh/s18-hyperv
   $before = Invoke-Command $hv { (Get-FileHash C:\VMs\base-windows.vhdx).Hash }
   try {
     Invoke-Command $hv {
       New-VHD -Path C:\VMs\s18-probe.vhdx -ParentPath C:\VMs\base-windows.vhdx -Differencing
       New-VM -Name s18-probe -Generation 2 -MemoryStartupBytes 2GB -VHDPath C:\VMs\s18-probe.vhdx
       Start-VM s18-probe
       Stop-VM s18-probe -TurnOff -Force                # hard power-off, no shutdown
       Start-VM s18-probe                               # restart from the cut
       Wait-VM s18-probe -For Heartbeat -Timeout 300    # guest up again
     }
     Remove-Item s18-ack.log -ErrorAction SilentlyContinue   # no stale line can pass the read-back
     $env:GOWORK = 'off'
     go -C tools/harness-gate run . ack-probe --listen 10.20.0.5:47000 --token s18-probe-ack `
       --log s18-ack.log --timeout 5m `
       --send 'pwsh -NoProfile -File /opt/dsh-s18/s18-probe-ack.ps1 -Probe s18-probe -Token s18-probe-ack -Addr 10.20.0.5:47000'
     if ($LASTEXITCODE -ne 0) { throw "ack-probe failed (exit $LASTEXITCODE): hyperv preflight failed" }
     Select-String -SimpleMatch 's18-probe s18-probe-ack' s18-ack.log   # the acknowledgment, read back
   } finally {
     Invoke-Command $hv {
       if (Get-VM s18-probe -ErrorAction SilentlyContinue) { Stop-VM s18-probe -TurnOff -Force; Remove-VM s18-probe -Force }
       Remove-Item C:\VMs\s18-probe.vhdx -ErrorAction SilentlyContinue
     }
   }
   $after = Invoke-Command $hv { (Get-FileHash C:\VMs\base-windows.vhdx).Hash }
   Remove-PSSession $hv
   if ($after -ne $before) { throw "base image changed: $after" }   # base image unchanged
   ```

   A wrong host key or a missing Hyper-V Administrators membership fails `New-PSSession`.
   A wrong guest credential, or a guest without the Hyper-V integration services, fails
   the inner `Invoke-Command -VMName`. Either way `pwsh -File` exits non-zero, and so
   `ack-probe` fails the preflight.

   `ack-probe` exits 0 only when the acknowledgment is in the log and read back and its
   `--send` command exited 0; it fails when none arrives within the timeout, when the
   sender fails, or when the sender is still running at the timeout. A host where any step
   fails is recorded `not run (libvirt preflight failed)` or `not run (hyperv preflight
   failed)`, and the stage stays open (M2-C-2). Paste the commands' output, the log line and
   both checksums into the record.

   The `vm:*` s18 jobs run this same cycle themselves (`check: power-cut`) from these
   environment variables, `{probe}` (also `{vm}`) being a fresh guest name per run:

   | Variable | Value |
   |---|---|
   | `DSH_S18_BASE_IMAGES` | the base images as paths the controller can read, `;`-separated; their SHA-256 before and after must match. For Hyper-V, the host's `C:\VMs` mounted read-only on the controller, e.g. `/mnt/hyperv-vms/base-windows.vhdx` |
   | `DSH_S18_PROBE_CREATE` | creates and starts `{probe}` from an overlay (the `qemu-img` and `virt-install` lines above) |
   | `DSH_S18_PROBE_DELETE` | stops `{probe}` hard if it runs, then deletes it and its overlay: `virsh destroy {probe} 2>/dev/null; virsh undefine {probe} --remove-all-storage` |
   | `DSH_S18_PROBE_ACK` | makes `{probe}` send one line holding `{token}` to `{addr}`, e.g. `ssh {probe} "echo {probe} {token} \| nc -N 192.168.122.1 47000"` for `{addr}` `192.168.122.1:47000`; on Hyper-V, `pwsh -NoProfile -File /opt/dsh-s18/s18-probe-ack.ps1 -Probe {probe} -Token {token} -Addr {addr}` (host session, then PowerShell Direct, as above) |

   The job checksums the base images, creates the probe, cuts it with
   `DSH_SCENARIO_NODEFS_POWEROFF`, restarts it with `DSH_SCENARIO_NODEFS_POWERON`, runs
   `harness-gate ack-probe --listen $DSH_SCENARIO_NODEFS_LISTEN` with `DSH_S18_PROBE_ACK` as
   `--send`, deletes the probe with `DSH_S18_PROBE_DELETE` and compares the checksums. Any
   failure is `not run (power-cut preflight failed)`.
3. **Create the run's guests.** One fresh overlay per guest OS, named in `DSH_SCENARIO_VMS`.
4. **Set the inputs** on the controller (`{vm}` is replaced by each guest's name):

   | Variable | Value |
   |---|---|
   | `DSH_SCENARIO_VMS` | the guests, `;`-separated |
   | `DSH_SCENARIO_NODEFS_LISTEN` | the recorder's `host:port` on the hypervisor host (the `service:ack-recorder` preflight: local and free) |
   | `DSH_SCENARIO_NODEFS_START` | command starting the writer role in `{vm}` in the background (`DSH_SCENARIO_NODEFS_ROLE=writer`, `DSH_SCENARIO_NODEFS_FILE`, `DSH_SCENARIO_NODEFS_ACK`, `DSH_SCENARIO_NODEFS_VM={vm}`). The writer must **not** start on its own at boot: every version it acknowledges after a restart, before the round's read, would be missed by the judge |
   | `DSH_SCENARIO_NODEFS_POWEROFF` | `virsh destroy {vm}`, or on Hyper-V `Stop-VM {vm} -TurnOff -Force` run in the host's SSH remoting session (`pwsh -c 'Invoke-Command (New-PSSession -HostName hyperv-host -UserName s18lab -KeyFilePath ~/.ssh/s18-hyperv) { Stop-VM {vm} -TurnOff -Force }'`) |
   | `DSH_SCENARIO_NODEFS_POWERON` | command starting `{vm}` and waiting until its guest agent answers |
   | `DSH_SCENARIO_NODEFS_READ` | command printing the replaced file of `{vm}` through the guest agent on its **standard output** only (diagnostics go to standard error); the bytes must equal one version exactly, one trailing line ending allowed |
   | `DSH_SCENARIO_REPORT` | the report `<report>`. The package's `TestMain` (`scenario.Main`) keeps `<report>` for the scenario rows and points the variable, as the test reads it, at `<report>.detail` (`scenario.DetailSuffix`): **the recorder's version log is `<report>.detail`**, appended to and kept whatever the result. Keep both files |
   | `DSH_SCENARIO_NODEFS_ROUNDS` | optional: `i-j` runs rounds i to j of 1-100 (a shard, M2-R-06); empty or `all` runs every round |

   Each round: the controller starts the writer, waits (at most 5 min) for its first new
   acknowledgement, cuts the power at a random moment within 5 s after it, restarts the
   guest, reads the file, and judges it against every version the recorder acknowledged.
   A version is acknowledged once the recorder has appended it to the log and replied
   `ok <version>` to the writer, which writes no next version before that reply; so at a
   cut at most the one version in flight is unacknowledged, and no wait decides the
   bound. The judgement ties each writer connection then open to the version judged: an
   acknowledgement read on it later, however late, is logged `late` if no newer, and
   otherwise logged `fail` and fails the run (keep that guest's overlay). The log has one line per
   event, `<sequence> <UTC time> <event> key=value...`, the events being `start`, `seed`,
   `round`, `ack`, `cut`, `read`, `late`, `fail`, `unconfirmed` (a reply the writer's
   connection did not take) and `junk`.

5. **Run** from the repository root on the controller:

   ```bash
   GOWORK=off go -C src/harness test -tags scenario -p 1 -count=1 -timeout 175m -v -run 'TestDurableReplaceSurvivesPowerLoss' ./internal/testkit/scenarios/
   ```

   or dispatch `harness-scenarios.yml` with `stage: DH-S-18` and let the `vm:*` s18 jobs run
   (they run all 100 rounds; they do not bind `DSH_SCENARIO_NODEFS_ROUNDS`). Within a run
   `harness-gate variant-run` puts `DH-E-NODEFS-1` last (impl.md 18.11).

   **Sharding.** If 100 rounds would pass 3 h, run shards, e.g. `1-40`, `41-80`, `81-100`,
   each a separate run with the same `DSH_SCENARIO_VMS` guests (their overlays kept between
   shards) and the same `DSH_SCENARIO_REPORT`, so every shard appends to the same
   `<report>.detail` version log. A shard past round 1 refuses to start unless that log ends
   with every guest's round i-1 read and judged; it takes each guest's acknowledged version
   from the log and continues its sequence. A shard stopped after a round's `round` or `cut`
   line, before its `read`, cannot be continued: that round's file is unjudged evidence a
   new writer would replace, so keep the overlay, judge its file by hand against the log's
   last `ack`, record it, and rerun all rounds on a fresh overlay. A run with any `fail`
   line, even one logged after a later `read` (an acknowledgement read late), cannot be
   continued either: keep the overlay of the guest it names and rerun all rounds on a fresh
   overlay (a new `start rounds=1-...` line). Record each shard's run.
6. **Clean up.** After the last shard, delete each passing overlay, its guest definition
   and disk. Keep the recorder's version log `<report>.detail` and the report `<report>`
   (both under `DSH_SCENARIO_REPORT`; deleting the `.detail` file discards the
   acknowledgement evidence). On a failure (a `fail` line
   in the log, naming the guest and round) keep the failing overlay and name it in the
   record and in [DH-S-18.md](DH-S-18.md).

### Through the Gherkin suite

The Stage 8.9 E2E suite
(`src/harness/test/e2e/harness-DSH-M2/dh_s_18_acceptance_on_real_operating_systems_durable_replace_under_power_loss.feature`)
runs steps 2 to 6 for each guest OS in one go. It reads the step 2 inputs directly as
`DSH_S18_KVM_*` (Linux guest) and `DSH_S18_HYPERV_*` (Windows guest): `NODEFS_LISTEN`,
`NODEFS_START`, `NODEFS_POWEROFF`, `NODEFS_POWERON`, `NODEFS_READ`, `BASE_IMAGES`,
`PROBE_CREATE`, `PROBE_SHOW`, `PROBE_ACK` and `PROBE_DELETE`. It does not read `VMS`. Then,
for each guest OS, it:

1. runs the step 2 preflight with a throw-away probe;
2. creates the run's guest fresh with `PROBE_CREATE` (`{probe}` is the generated guest name);
3. cuts the guest and powers it back on;
4. runs a prebuilt `TestDurableReplaceSurvivesPowerLoss` binary against that guest (100 rounds,
   never a cached result);
5. judges the version log on its own: 100 cuts, every read complete, and the version read
   never older than the last acknowledged version;
6. deletes the guest with `PROBE_DELETE` if it passes, and keeps it if it fails.

If a guest OS's inputs are unset, or its preflight fails, that scenario fails with
`not run (power-cut preflight failed)`. It never skips. The report, the version log and the
child output are written to `$DSH_E2E_NODEFS_ARTIFACTS/<linux|windows>/`; when that is
unset, they go to a temp directory.

```bash
GOWORK=off go -C src/harness test -tags e2e -v -timeout 355m \
  -run '^TestE2E_dh_s_18_acceptance_on_real_operating_systems_durable_replace_under_power_loss$' \
  ./test/e2e/harness-DSH-M2/
```

`.github/workflows/e2e-dh-s-18-acceptance-on-real-operating-systems.yml` runs this command on
the `dsh-hypervisor` runner. It runs in environment `dsh-m2-s18` and holds the
`dsh-s18-lock-hypervisor` lock. Record its result here in the same way as for step 5.

## Record

| Field | Value |
|---|---|
| Date | |
| Operator | |
| Commit | |
| KVM host (`virsh version`, kernel, disk cache mode) | |
| Hyper-V host (build, disk cache mode) | |
| Base image SHA-256 (Linux; Windows) | |
| Preflight (step 2) per host | |
| Recorder version log path (`<report>.detail`) and its SHA-256 | |
| Report path (`<report>`, the scenario rows) | |

| Variant | Guest | Expected | Cuts run | Last acknowledged version / read version (last cut) | Result |
|---|---|---|---|---|---|
| `vm:linux-guest` | Linux on KVM | 100 cuts, file complete and never older than the acknowledged version | | | |
| `vm:windows-guest` | Windows on Hyper-V | as for Linux | | | |

A result is `passed`, `failed` (with the failing iteration and the kept overlay) or
`not run (<check> preflight failed)`; a missing host is never a pass.
