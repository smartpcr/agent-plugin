# DH-S-13 evidence

Closure status: open

Evidence record of [impl.md DH-S-13](../../../harness/impl.md#dh-s-13) (Sandbox launch, Linux backends and shell tools) for story
`harness:DSH-M2`, in the impl.md sec. 7.3 form of the M1 records
([../../harness-DSH-M1/evidence/README.md](../../harness-DSH-M1/evidence/README.md)). Plan:
[implementation-plan.md](../implementation-plan.md) Phase 3. Decisions of
[../../harness-DSH-M1/decisions.md](../../harness-DSH-M1/decisions.md) stay in force.

**Kind.** Build, P0. Paired acceptance stage: DH-S-18.

**How this record closes** (C57; M2-C-2; [the gate's README](../../../../tools/harness-gate/README.md#stage-closure)):

1. While the line above reads `open`, fill the tables below until `harness-gate ready --stage DH-S-13 --built`
   exits 0. `ready` does not read the status line.
2. Only then does the closing PR change the line to `Closure status: closed`.
3. That PR's gate runs `harness-gate close --stage DH-S-13 --built`: every `ready` condition again, and the
   line reading `closed`. A `closed` line on a record that is not ready is refused `closed-but-not-ready`.

No M2 scenario run and no M2 stage closure happens before M1 is closed (M2-DEC-1; `harness-gate entry`,
implementation-plan Stage 1.2). The four tables below are read by the gate: keep their headings and columns.
A result is `passed`, `failed`, `not run (<reason>)`, `skipped`, `blocked` or `out of scope (<decision>)`;
skipped, blocked, not run and an empty cell count as open (impl.md sec. 7.3, M2-DEC-3), and only a
variant out of scope by a named decision is not a blocker (M2-C-2). A `recorded` leg must run; its failure
is kept and does not block (M2-DEC-4). A commit is a SHA of 7 to 40 lower-case hex digits.

## Steps

Each step's merge commit; `--built` refuses a step without one.

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 13.1 | DH-D-SANDBOX-2 | | |
| 13.2 | DH-D-EXEC-4 | | |
| 13.3 | DH-D-SANDBOX-3 | | |
| 13.4 | DH-D-SANDBOX-4 | | `world/sandbox/local`: bubblewrap backend (`bwrap` resolved once by absolute path; argv from the Policy, golden `local/testdata/bwrap_argv.golden`, `TestBwrapArgvFromPolicy`) and pure-Go Landlock backend through go-landlock v0.11.2 (`launch/landlock.go`, `launch/confine_linux.go`: `NewRulesetFD`, locked thread, `no_new_privs`, `LandlockRestrictSelf`; ABI probe, rules from the Policy, applied by the stage to itself; `TestLandlockRulesFromPolicy`, `TestLandlockStageConfinesForReal`); Prepare refuses a root that would make the harness binary or home writable where the backend cannot overlay it, judging each root, the home and the binary as written and with every symbolic link resolved, and refusing a path it cannot resolve (`local/protect.go`, `TestPrepareKeepsTheHarnessUnwritable`, `TestPrepareSeesThroughSymlinkedAncestors`); order bwrap then Landlock, both failing refuses a confined policy `sandbox/unavailable` (`TestSelectionOrder`). I2 Q4 default applied, see Owner defaults. |
| 13.5 | DH-D-EXEC-3 | | |
| 13.6 | DH-D-EXEC-8 | | |
| 13.7 | DH-D-EXEC-9 | | |
| 13.8 | DH-D-CONTEXT-2 | | |

## Scenarios

None executed here. Authored here: DH-E-NATIVESYSTEM-1 to -4 and DH-E-SANDBOX-1 (run in DH-S-18); DH-E-SANDBOX-2, DH-E-EXEC-7 and DH-E-EXEC-8 (run in DH-S-39).

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |

## Manual checks register

The manual checks impl.md DH-S-13 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 13.4 | on a Linux host, a confined command that writes outside its roots: the denial and the reported enforcement | | | | `manual` |
| 13.3 | both backends removed: the command did not run | | | | `manual` |

## Owner defaults (M2-C-4)

The defaults of [impl.md sec. 9.1](../../../harness/impl.md#91-defaults-the-owner-has-not-confirmed) whose
"Confirm before" is DH-S-13. The owner confirms each, or changes the document that holds it, before this
stage closes; `--built` refuses a row with no confirmation or no name in By.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |
| `partial` when delegated execution cannot be stopped | I2 Q4 | A sandbox that cannot stop delegated execution is reported `partial` | | | |

Applied, unconfirmed, in step 13.4. Both Linux confiners (`launch/observe.go` `confineBwrap` and
`launch/confine_linux.go` `confineLandlock`) end in the shared `delegated()`, which claims
`delegated-execution` governed only when the stage's bus observation (`launch/bus.go` `busProbe`, fed by
`launch/bus_linux.go`) shows every session service absent: the address in `DBUS_SESSION_BUS_ADDRESS` and
`bus` and `systemd/private` under `/run/user/<uid>` and `$XDG_RUNTIME_DIR`, each dial failing with "no
such file", "not a directory" or "connection refused". Under bubblewrap the stage looks after the
runtime directory is hidden by `--tmpfs`; under Landlock it looks just before restricting itself, since
this reference set does not scope sockets, so on a desktop session with a live bus Landlock does not
claim it. `delegated-execution` is missing, and `sandbox.Enforce` reports the start `partial` naming it,
whenever a service answers, the address names any other transport (`tcp:`, `nonce-tcp:`, `unixexec:`,
`launchd:`, `systemd:`, `autolaunch:`, an abstract socket, `runtime=yes` with no runtime directory) or is
malformed, or a dial fails for any other reason (a time out, a permission denial); off Linux it is
always missing. Tests: `TestBusProbeCountsTheUnknownReachable`, `TestSessionBusObservedForReal`,
`TestBwrapStageClaimsOnlyWhatItSees`, `TestLandlockRulesFromPolicy`.

Decisions taken from the design in step 13.4 (these are not impl.md sec. 9.1 defaults, so they have no row above):

- **fs-write version gate.** Landlock reports `fs-write` governed only at the reference version 5;
  below it `fs-write` is in `Missing`. This is DH-ADR-NATIVESYSTEM-2 ("a named reference version (5 at
  first, as upstream)", `full` only "when the kernel's version is at least the reference")
  (`launch/landlock.go`, `TestLandlockRulesFromPolicy`).
- **Private `/tmp` under bubblewrap.** Under `workspace-write`, bubblewrap mounts `--tmpfs /tmp`, which
  goes away when the start ends. Under `read-only` the host `/tmp` stays read-only. This follows
  DH-ADR-SANDBOX-4 (only Landlock and Seatbelt "keep upstream's shared temp areas") and the bubblewrap
  row of sandbox-ssh.md sec. 3.2. It comes before every bind, so a root, the canary or the binary under
  `/tmp` is still the host's (`local/bwrap.go`, `testdata/bwrap_argv.golden`, `TestBwrapArgvFromPolicy`).
  Landlock grants writes only beneath the policy's roots, as TC-04a requires, and does not grant the
  shared host `/tmp`.
- **Where `Config.Home` comes from.** Whoever builds the sandbox service passes the resolved harness
  home. No production code builds `world/sandbox/local` yet; every caller is a test. Until a caller
  exists, an empty `Home` means no start claims `harness-home`, and the inside-home refusal has nothing
  to compare against (`local/local.go` `Config.Home`).

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Not yet recorded. At closure: the commit of the three modules (one commit), the Go version (`go1.27.4`),
the binary's SHA-256 and embedded version.

### Environment and fixture

Not yet recorded. Gate venue: both local lanes, `tools/harness-gate/lanes/win.ps1` (Windows native) and
`lanes/wsl.sh` (Linux under WSL); macOS out of scope (`platforms-windows-wsl`).

### Exact tests and commands

Not yet recorded: the gate runner's output, the `go test` command lines and `DSH_REQUIRE_SCENARIOS`
(`harness-gate require --stage DH-S-13`).

### Run identifiers

Not yet recorded: CI run and job ids, or the local run-record id and its SHA-256 (M1 runbook template);
session ids and provider request ids for model calls.

### Results

The Scenarios table above, and the local test results per step at closure. Not yet recorded.

### Manual checks

The Manual checks register above.

### Cleanup evidence

Not yet recorded: the before-and-after comparison the stage names, or what was retained.

### Open items

Unresolved debt `UP-DEBT-limit.packages.shell.pwsh-local.7` (non-ASCII standard input under Windows PowerShell 5.1) closes with DH-E-EXEC-7 in DH-S-39. Built-not-done until DH-S-39: SANDBOX-2, SANDBOX-3, EXEC-4, EXEC-8, EXEC-9; CONTEXT-2 until DH-S-32 (DH-E-CONTEXT-1).
