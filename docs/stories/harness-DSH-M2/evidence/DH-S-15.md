# DH-S-15 evidence

Closure status: open

Evidence record of [impl.md DH-S-15](../../../harness/impl.md#dh-s-15) (Listener, sign-in, stdio and the SDK server) for story
`harness:DSH-M2`, in the impl.md sec. 7.3 form of the M1 records
([../../harness-DSH-M1/evidence/README.md](../../harness-DSH-M1/evidence/README.md)). Plan:
[implementation-plan.md](../implementation-plan.md) Phase 5. Decisions of
[../../harness-DSH-M1/decisions.md](../../harness-DSH-M1/decisions.md) stay in force.

**Kind.** Build, P0. Paired acceptance stage: DH-S-17.

**How this record closes** (C57; M2-C-2; [the gate's README](../../../../tools/harness-gate/README.md#stage-closure)):

1. While the line above reads `open`, fill the tables below until `harness-gate ready --stage DH-S-15 --built`
   exits 0. `ready` does not read the status line.
2. Only then does the closing PR change the line to `Closure status: closed`.
3. That PR's gate runs `harness-gate close --stage DH-S-15 --built`: every `ready` condition again, and the
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
| 15.1 | DH-D-HOST-1 | | |
| 15.2 | DH-D-HOST-2 | | |
| 15.3 | DH-D-HOST-3 | | |
| 15.4 | DH-D-API-1 | | |
| 15.5 | DH-D-SDK-3 | | |
| 15.6 | DH-D-SDK-4 | | `wire/sdkserver`; H-1, H-2, H-3 not built: no method added, `schema.json` unchanged (M2-C-7) |
| 15.7 | DH-D-SDK-5 | | held open: H-4 |
| 15.8 | DH-D-SDK-6 | | |

## Scenarios

None executed here. Authored here: DH-E-SDK-1 (run in DH-S-17), DH-E-SDK-2 (run in DH-S-39); the browser sign-in and owner-takeover scenarios are indexed to DH-S-22 and DH-S-39.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |

## Manual checks register

The manual checks impl.md DH-S-15 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 15.2 | a listener refuses a request without the cookie, from another origin, and from a process the host started for the model | | | | `manual` |
| 15.6 | one session driven by hand over stdio with typed JSON lines | | | | `manual` |

## Owner defaults (M2-C-4)

The defaults of [impl.md sec. 9.1](../../../harness/impl.md#91-defaults-the-owner-has-not-confirmed) whose
"Confirm before" is DH-S-15. The owner confirms each, or changes the document that holds it, before this
stage closes; `--built` refuses a row with no confirmation or no name in By.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |
| No SDK session grant | I2 Q5 | An SDK client may not create a session grant | | | |
| Client time zone | U-K2-5 | The SDK client states its time zone | | | |

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Not yet recorded. At closure: the commit of the three modules (one commit), the Go version (`go1.27.4`),
the binary's SHA-256 and embedded version.

### Environment and fixture

Not yet recorded. Gate venue: both local lanes, `tools/harness-gate/lanes/win.ps1` (Windows native) and
`lanes/wsl.sh` (Linux under WSL); macOS out of scope (`platforms-windows-wsl`).

### Exact tests and commands

Not yet recorded: the gate runner's output, the `go test` command lines and `DSH_REQUIRE_SCENARIOS`
(`harness-gate require --stage DH-S-15`).

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

Not built (DH-D-SDK-4, M2-C-7): H-1 (schedule hook), H-2 (multi-route validation), H-3 (inline images). No method is added for them and `src/sdk/protocol/schema.json` is unchanged (TC-12g); an image block is refused `sdk/invalid-content`. Held open: H-4 (DH-D-SDK-5). Built-not-done: HOST-1, HOST-2 until DH-S-22; HOST-3, SDK-3 until DH-S-39; API-1 until DH-S-48. The exit evidence lists the SDK methods that are complete, those that wait for H-4, and H-1 to H-3 as not built.
