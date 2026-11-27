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
- **Credential.** The scratch account of each host (`DSH_LAB_HYPERV_CRED` for Hyper-V).
  No model key.
- **Base images.** One per guest OS, holding the built `dsh` test binary and the writer
  loop; record each image's SHA-256.
- **Time.** At most 3 h per variant (M2-EN-7); beyond that, shard the 100 cuts (M2-R-06).

## Steps

1. **Record the hosts.** Paste into the record: `virsh version` and the kernel of the KVM
   host; `Get-VMHost | Select-Object Name, VirtualMachineMigrationEnabled` and the build
   of the Hyper-V host; each base image's SHA-256 and the guest disk cache mode.
2. **Preflight the controller** (the `service:vm-power-cut` preflight). Per host, create
   a throw-away guest from a copy-on-write overlay of the base image, power it off hard
   and delete it:

   ```bash
   qemu-img create -f qcow2 -F qcow2 -b base-linux.qcow2 /var/lib/libvirt/images/s18-probe.qcow2
   virt-install --import --name s18-probe --memory 1024 --disk /var/lib/libvirt/images/s18-probe.qcow2 --noautoconsole --os-variant generic
   virsh destroy s18-probe && virsh undefine s18-probe --remove-all-storage
   ```

   ```powershell
   New-VHD -Path C:\VMs\s18-probe.vhdx -ParentPath C:\VMs\base-windows.vhdx -Differencing
   New-VM -Name s18-probe -Generation 2 -MemoryStartupBytes 2GB -VHDPath C:\VMs\s18-probe.vhdx; Start-VM s18-probe
   Stop-VM s18-probe -TurnOff -Force; Remove-VM s18-probe -Force; Remove-Item C:\VMs\s18-probe.vhdx
   ```

   A host where any of these fails is recorded `not run (hyperv preflight failed)` or
   `not run (libvirt preflight failed)`, and the stage stays open (M2-C-2).
3. **Create the run's guests.** One fresh overlay per guest OS, named in `DSH_SCENARIO_VMS`.
4. **Set the inputs** on the controller (`{vm}` is replaced by each guest's name):

   | Variable | Value |
   |---|---|
   | `DSH_SCENARIO_VMS` | the guests, `;`-separated |
   | `DSH_SCENARIO_NODEFS_LISTEN` | the recorder's `host:port` on the hypervisor host (the `service:ack-recorder` preflight: local and free) |
   | `DSH_SCENARIO_NODEFS_START` | command starting the writer role in `{vm}` in the background |
   | `DSH_SCENARIO_NODEFS_POWEROFF` | `virsh destroy {vm}`, or `Stop-VM {vm} -TurnOff -Force` through PowerShell remoting |
   | `DSH_SCENARIO_NODEFS_POWERON` | command starting `{vm}` and waiting until its guest agent answers |
   | `DSH_SCENARIO_NODEFS_READ` | command printing the replaced file of `{vm}` through the guest agent |
   | `DSH_SCENARIO_REPORT` | the file the recorder's version log is written to |

5. **Run** from the repository root on the controller:

   ```bash
   GOWORK=off go -C src/harness test -tags scenario -p 1 -count=1 -timeout 175m -v -run 'TestDurableReplaceSurvivesPowerLoss' ./internal/testkit/scenarios/
   ```

   or dispatch `harness-scenarios.yml` with `stage: DH-S-18` and let the `vm:*` s18 jobs run.
6. **Clean up.** Delete each passing overlay, its guest definition and disk. Keep the
   recorder log under `DSH_SCENARIO_REPORT`. On a failure keep the failing overlay and name
   it in the record and in [DH-S-18.md](DH-S-18.md).

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
| Recorder log path | |

| Variant | Guest | Expected | Cuts run | Last acknowledged version / read version (last cut) | Result |
|---|---|---|---|---|---|
| `vm:linux-guest` | Linux on KVM | 100 cuts, file complete and never older than the acknowledged version | | | |
| `vm:windows-guest` | Windows on Hyper-V | as for Linux | | | |

A result is `passed`, `failed` (with the failing iteration and the kept overlay) or
`not run (<check> preflight failed)`; a missing host is never a pass.
