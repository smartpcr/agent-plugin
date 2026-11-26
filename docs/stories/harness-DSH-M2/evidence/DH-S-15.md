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
| 15.7 | DH-D-SDK-5 | | `wire/sdkserver` `forward.go`, `runs.go`: scoped ordered `session.event`, `session.run.completed` with the last assistant text (TC-12a); H-4 (goal stop signal) not built |
| 15.8 | DH-D-SDK-6 | | `wire/sdkserver` `answer.go`: one `sdk` answerer routed to the owning connection; `approval/request` and `question/ask` only when advertised; an unoffered answer is sent back to the client as `sdk/invalid-answer` (an error to the null id naming the `interactionId`, written by `sdkserver`'s own transport under `Conn.Serve` with the SDK unchanged, so release builds (`GOWORK=off`, DH-ADR-26) send it too; a transport that cannot send it is never asked, and a rejection that cannot be sent refuses the interaction and is reported; the rejection keeps the codec's bounds: over `MaxFrameBytes` it drops the quoted reply, and a write not accepted within `WriteStall` or failing at once closes the transport, so `Serve` returns and the connection's sessions are released), settles nothing and is asked again (at most 3 times, then refused); a late answer is `sdk/late-answer`; withdrawal is `$/cancel`; a subagent of an owned session is bound via its creator on `agent.Created`, before it is live; no answer creates a session grant; `timeZone` validated on `initialize` and `session/create` and carried in the client record (`agent.ClientRecord`) of every message the carrier queues (TC-12a, NG-8, U-K2-5) |

## Scenarios

None executed here. Authored here: DH-E-SDK-1 (run in DH-S-17) and DH-E-SDK-2 (run in DH-S-39), in Stage 6.11 on the Go client; and, in Stage 5.9, compile only, on the M1 scenario helpers (`dshBinary`, `startDsh`) and raw JSON-RPC from `src/sdk/jsonrpc`, never `src/sdk/sdk` (M2-DOD-5):

- DH-E-HOST-1 (`host_signin_scenario_test.go`) and DH-E-HOST-2 (`host_network_scenario_test.go`), indexed under DH-S-22 (impl.md step 22.2);
- DH-E-HOST-7 (`host_takeover_linux_scenario_test.go`), DH-E-HOST-8 (`host_takeover_macos_scenario_test.go`) and DH-E-HOST-9 (`host_takeover_windows_scenario_test.go`), indexed under DH-S-39 (impl.md step 39.4), on the shared takeover runner `takeover_test.go` (the script run once per mode: workspace-write, network denied, danger-full-access; DH-E-HOST-9 makes only the workspace-write and danger-full-access runs, as the restricted token cannot deny the network).

The files are under `src/harness/internal/testkit/scenarios/`, with their shared helpers in `host_web_test.go`. `host.launch` prints its link only to a terminal (stderr a character device), so `startWebHost` runs `dsh` under a terminal host (`tty_*_test.go`: a pseudo-terminal on Linux and macOS, a ConPTY on Windows), a re-exec of the test binary started through `startDsh`; the printed link is read from that terminal's output, and the product's credential channel is unchanged. What the product does not yet build (the browser, the conversation API, the takeover fixture) is a run-time input of each scenario, named in its doc comment; a missing input fails the scenario, it is never skipped.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |

## Manual checks register

The manual checks impl.md DH-S-15 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 15.2 | a listener refuses a request without the cookie, from another origin, and from a process the host started for the model | not run (blocked: `dsh --home H --layer layer.yml web` exits 78 before listening, `boot/required: required entry not active: session.store, agent.registry, agent.driver, tools.registry, agent.presets, host.listen, host.auth, host.assets, host.web, host.access, host.stream, ui.shell`; no request could be sent; deferred to the stage-close PR by operator answer `s15-manual-checks-operator`) | Copilot (Forge engineer agent), attempt only | c21d7f4 | `manual` |
| 15.6 | one session driven by hand over stdio with typed JSON lines | not run (blocked: `dsh --home H sdk` exits 78 before reading stdin, `boot/required: required entry not active: session.store, agent.registry, agent.driver, tools.registry, agent.presets, wire.stdio, wire.sdk`; the typed `initialize` line got no reply; deferred to the stage-close PR by operator answer `s15-manual-checks-operator`) | Copilot (Forge engineer agent), attempt only | c21d7f4 | `manual` |

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

Not built (DH-D-SDK-4, M2-C-7): H-1 (schedule hook), H-2 (multi-route validation), H-3 (inline images). No method is added for them and `src/sdk/protocol/schema.json` is unchanged (TC-12g); an image block is refused `sdk/invalid-content`. Not built (DH-D-SDK-5): H-4 (goal stop signal); a run completes at its turn's `turn/end` only, never on a goal. Built-not-done: HOST-1, HOST-2 until DH-S-22; HOST-3, SDK-3 until DH-S-39; API-1 until DH-S-48. The exit evidence lists the SDK methods that are complete, those that wait for H-4, and H-1 to H-3 as not built.

SDK methods (`wire/sdkserver` `server.go`, the registered client requests):

| Method | State |
| --- | --- |
| `initialize`, `shutdown` | complete (`timeZone` validated on `initialize`) |
| `session/create`, `session/resume`, `session/list`, `session/close` | complete |
| `session/prompt`, `session/cancel` | complete; a text block only, an image block is refused `sdk/invalid-content` until H-3 |
| `session/query`, `session/history` | complete |
| `session/wait`, `session.run.completed` | complete for a turn (`turn/end`, with the last assistant text); completion on a goal waits for H-4 |
| `session/model/set` | complete for the one route of the session; validation across routes waits for H-2 |
| `session/working-directory/get`, `session/working-directory/set` | complete |
| `approval/request`, `question/ask` (server to client) | complete, only when the client advertised them; `sdk/invalid-answer`, `sdk/late-answer`, `$/cancel` |
| a schedule hook | not built (H-1); no method |

Open list H-1..H-4: H-1 schedule hook, H-2 multi-route validation, H-3 inline images, H-4 goal stop signal: each not built in M2, none has a method or a schema entry.

Manual checks: attempted at c21d7f4 on a scratch home and recorded `not run (blocked)` in the register. The binary at that commit does not boot the `web` or `sdk` profile: the bundles that activate their required entries are not registered yet (profiles launch from DH-D-BUNDLE-3), so no listener answered and no stdio session started. The boot reports (`<home>/reports/report-*.txt`) name the missing entries quoted in the register. Nothing was observed, so nothing is recorded as seen. Both rows stay open: by operator answers `s15-manual-checks-operator` and `s15-manual-checks-blocked` they are deferred to the stage-close PR and stay open until a commit whose profiles boot (DH-D-BUNDLE-3); an operator performs them there, at the final built commit (the one the lane runs use). Until then the component tests cover the same refusals and the same wire (`go test -count=1 ./internal/host/listen/ ./internal/host/auth/ ./internal/wire/sdkserver/`: ok at c21d7f4 plus this stage's test-only changes). They do not replace the `manual` rows.

Lane results: the S15 gate on both lanes runs once per commit, outside an iteration, in a temporary worktree (product policy "Full harness gate"). Attempt at 78ef01a (clean detached checkouts; `E:\work\lanes\dhs15-78ef01a` and `~/src/dhs15-78ef01a` under WSL, both removed after the run): **failed on both lanes at gate 1/13 `gofmt`**, `not gofmt-formatted: src/harness/internal/host/launch/component.go` (win `win-78ef01a-20261126T1019Z.txt`, wsl `wsl-78ef01a-20261126T1020Z.txt`; the WSL rehearsal exited 0). That file has no final newline on `origin/main` (f7d7462, #165) and this stage does not touch it, so no later gate ran. Second attempt at 6025ffb, started by operator answer `s15-lane-runs` (`E:\work\lanes\dhs15-6025ffb` and `~/src/dhs15-6025ffb`, both removed after the run): **failed on both lanes at gate 1/13 `gofmt`** on the same file (win `win-6025ffb-20261126T1029Z.txt`, wsl `wsl-6025ffb-20261126T1030Z.txt`; the WSL rehearsal exited 0). `origin/main` (835063c, #171) still carries it unformatted. By operator answer `s15-baseline-gofmt` this branch formats that file as a separate one-line change (final newline only). Third attempt at 2abd753 (`E:\work\lanes\dhs15-2abd753` and `~/src/dhs15-2abd753`, both removed after the run; logs `E:\work\lanes\dhs15-2abd753-win.log` and `~/src/dhs15-2abd753-wsl.log`): gate 1/13 `gofmt` **passed** on both lanes (1100 files); **failed on both lanes at gate 2/13 `vet`** (win `win-2abd753-20261126T1044Z.txt`, wsl `wsl-2abd753-20261126T1045Z.txt`; the WSL rehearsal exited 0). Findings in this stage's files, fixed after 2abd753: `time.Sleep` polls (QG-4) in `host_network_scenario_test.go`, `takeover_test.go` and `takeover_check_test.go`, now on tickers or the proxy's exit signal; the other-origin listener of `host_signin_scenario_test.go` now released by its own `t.Cleanup` (QG-5); the `unsafe` imports of `tty_windows_test.go` and `tty_darwin_test.go` now carry a `//lint:ignore typeassert` reason (DH-ADR-TOOLCHAIN-8). Findings in files this stage does not touch, unchanged on `origin/main`: `internal/host/web/web_test.go:131` (`peer` lacks `Principal` of `listen.Peer`, #161), `time.Sleep` at `internal/host/launch/launch_test.go:138` (#165), `internal/wire/sdkserver/forward_test.go:289` (#166) and `test/e2e/harness-DSH-M2/dh_s_15_listener_sign_in_stdio_and_sdk_server_host_launch_and_control_endpoint_test.go:151`. No attempt counts as a pass. The record stays open until both lanes pass on the final commit.
