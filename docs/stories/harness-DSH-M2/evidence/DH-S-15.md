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

Gate venue: both local lanes, `tools/harness-gate/lanes/win.ps1` (Windows native) and
`lanes/wsl.sh` (Linux under WSL); macOS out of scope (`platforms-windows-wsl`). Lane run at 6422ca4:
Windows, windows/amd64, NTFS checkout, not elevated, go1.27.4, CC=gcc, clean tree (a detached git worktree);
WSL Ubuntu-24.04, ext4 clone, uid 1000, go1.27.4, CC=gcc, TMPDIR=/tmp, clean tree. Both preflights passed.

### Exact tests and commands

Lane gate at 6422ca4: `pwsh -NoProfile -File tools\harness-gate\lanes\win.ps1` and
`bash tools/harness-gate/lanes/wsl.sh`. Each runs `harness-gate --root ../.. --evidence <record>`, which runs all 13
gates (`gofmt`, `vet`, `staticcheck`, `modules`, `test` (`go test -race -shuffle=on -count=1` per module),
`coverage`, `trace`, `scenario-index`, `policy-text`, `budget`, `cross-compile`, `vulncheck`, `self`); WSL first
runs the release rehearsal. At closure: the per-step `go test` command lines and `DSH_REQUIRE_SCENARIOS`
(`harness-gate require --stage DH-S-15`).

### Run identifiers

Local run records of the lane gate at 6422ca4 (`docs/stories/harness-DSH-M1/evidence/runs/` in each lane's
checkout; copies kept in `E:\work\lanes\` and WSL `~/src/lane-records/`), all exit 0. The SHA-256 is each record's
own final `sha256:` line, the digest of every line before it (`tools/harness-gate/evidence.go`); both were rechecked
with `head -n -1 <record> | sha256sum`:

| Lane | Record | SHA-256 | Ended (UTC) | Result |
| --- | --- | --- | --- | --- |
| Windows | `win-6422ca4-20261127T0146Z.txt` | `82bbad948a12722fa9e0f55f0a5fd97d9020ce878f130e53f6719e84bb224f9d` | 2026-11-27T02:20:25Z | passed, 13/13 gates |
| WSL | `wsl-6422ca4-20261127T0145Z.txt` | `abcad3846907adf9cbdcee338b2ae23377387a4ba34e67792dad0dbeb04d37e7` | 2026-11-27T01:56:59Z | passed, 13/13 gates |
| WSL rehearsal | `wsl-6422ca4-20261127T0145Z-rehearse.txt` | | 2026-11-27T01:46:00Z | exit 0 |

No CI run; no model calls (the HOST scenarios are compile only here).

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

Lane results: the S15 gate on both lanes runs once per commit, outside an iteration, in a temporary worktree (product policy "Full harness gate"). Attempt at 78ef01a (clean detached checkouts; `E:\work\lanes\dhs15-78ef01a` and `~/src/dhs15-78ef01a` under WSL, both removed after the run): **failed on both lanes at gate 1/13 `gofmt`**, `not gofmt-formatted: src/harness/internal/host/launch/component.go` (win `win-78ef01a-20261126T1019Z.txt`, wsl `wsl-78ef01a-20261126T1020Z.txt`; the WSL rehearsal exited 0). That file has no final newline on `origin/main` (f7d7462, #165) and this stage does not touch it, so no later gate ran. Second attempt at 6025ffb, started by operator answer `s15-lane-runs` (`E:\work\lanes\dhs15-6025ffb` and `~/src/dhs15-6025ffb`, both removed after the run): **failed on both lanes at gate 1/13 `gofmt`** on the same file (win `win-6025ffb-20261126T1029Z.txt`, wsl `wsl-6025ffb-20261126T1030Z.txt`; the WSL rehearsal exited 0). `origin/main` (835063c, #171) still carries it unformatted. By operator answer `s15-baseline-gofmt` this branch formats that file as a separate one-line change (final newline only). Third attempt at 2abd753 (`E:\work\lanes\dhs15-2abd753` and `~/src/dhs15-2abd753`, both removed after the run; logs `E:\work\lanes\dhs15-2abd753-win.log` and `~/src/dhs15-2abd753-wsl.log`): gate 1/13 `gofmt` **passed** on both lanes (1100 files); **failed on both lanes at gate 2/13 `vet`** (win `win-2abd753-20261126T1044Z.txt`, wsl `wsl-2abd753-20261126T1045Z.txt`; the WSL rehearsal exited 0). Findings in this stage's files, fixed after 2abd753: `time.Sleep` polls (QG-4) in `host_network_scenario_test.go`, `takeover_test.go` and `takeover_check_test.go`, now on tickers or the proxy's exit signal; the other-origin listener of `host_signin_scenario_test.go` now released by its own `t.Cleanup` (QG-5); the `unsafe` imports of `tty_windows_test.go` and `tty_darwin_test.go` now carry a `//lint:ignore typeassert` reason (DH-ADR-TOOLCHAIN-8). Findings in files this stage does not touch, unchanged on `origin/main`: `internal/host/web/web_test.go:131` (`peer` lacks `Principal` of `listen.Peer`, #161), `time.Sleep` at `internal/host/launch/launch_test.go:138` (#165), `internal/wire/sdkserver/forward_test.go:289` (#166) and `test/e2e/harness-DSH-M2/dh_s_15_listener_sign_in_stdio_and_sdk_server_host_launch_and_control_endpoint_test.go:151`. By operator answer `s15-baseline-vet` this branch repairs them as separate test-only changes: `peer` gains `Principal` (the owner, as the admit-all fake admits everyone), the two goroutines of `web_test.go` (`TestStreamDecisionPinnedAcrossSwap`'s request and `TestConcurrentPublication`'s reader) are waited for by a `t.Cleanup`, and the three sleeps become a ticker or `<-time.After` with unchanged intervals and assertions. With them, the analyzers pass module-wide (`go vet -vettool=<harness-gate>` over `src/harness/...`). A diagnostic run at 6868562 with those findings suppressed (not lane evidence) passed gates 1-4 under WSL and **failed at gate 5/13 `test`** on three tests outside this stage's scenarios, each also failing on `origin/main` 835063c: `internal/cli/output` `TestEveryReasonHasAStatus` (the `launch/*` and `web/*` reasons have no exit status in `output.codes`), `internal/host/launch` `TestOpenFallback` (`launch_test.go:390`, `launch/not-ready`), and `internal/session/store/allevents` `TestInventory` (`agent/inbox/spliced` shape changed at version 1 without a bump). `TestOpenFallback` is a stale test fixture, not a product defect: both hosts got a `Ready` channel that never closed, so `Open` correctly refused with `launch/not-ready` (the contract at `launch.go:166` and `:375-380`) before reaching the fallback or no-channel path. This branch now closes `Ready` before those calls, first asserts the `launch/not-ready` refusal before readiness, and counts the printed links so the background announcement cannot satisfy the fallback check (test-only; the readiness guard is unchanged). The other two need product decisions (an exit status per reason; a version bump or a `compatible` note for a persisted event) and are raised as open question `s15-baseline-test`. The lanes rerun on the commit carrying the vet repairs. Fourth attempt at 5c1f590 (the vet repairs, without the `TestOpenFallback` fixture fix; `E:\work\lanes\dhs15-5c1f590` and `~/src/dhs15-5c1f590`; logs `E:\work\lanes\dhs15-5c1f590-win.log` and `~/src/dhs15-5c1f590-wsl.log`; win record `win-5c1f590-20261126T1117Z.txt`, wsl record `wsl-5c1f590-20261126T1117Z.txt`): gates 1/13 `gofmt`, 2/13 `vet`, 3/13 `staticcheck` and 4/13 `modules` **passed on both lanes with nothing suppressed** (win modules 6m4.806s). Gate 5/13 `test` **failed on both lanes** on exactly `TestEveryReasonHasAStatus, TestOpenFallback, TestInventory` (`go test -tags= -race -shuffle=on -count=1 ./...`; win 5m47.554s, `harness-gate: FAILED at gate test`, exit 1), the three tests above and no others. Both temporary checkouts are removed. By operator answer `s15-baseline-test` this branch then repairs the other two as a separate change: `internal/cli/output/status.go` gives each of the ten reasons an exit status (`launch/no-channel` Unavailable = 4 and `launch/taken` Busy = 6 per `app-cli.md:167`; `launch/config` Config; `launch/io` and `launch/print-failed` Failed; `launch/no-stop` and `launch/not-ready` Unavailable; `web/undeclared-effect` and `web/route-invalid` Invalid and `web/route-taken` Busy, as `listen/mount-invalid` and `listen/mount-taken`), and `internal/session/store/allevents/testdata/persisted.notes` gains one `agent/inbox/spliced 1->1 ... compatible:` line (the `client` and `timeZone` fields #169 added are optional, so version 1 readers ignore them), with `persisted.golden` regenerated. That is persisted-format metadata only: no event version, stored record or decoder changes. With it, the three tests pass on Windows (`go test -count=1`) and under WSL (`-race -shuffle=on`). Fifth attempt at eff3750 (that repair; `E:\work\lanes\dhs15-eff3750` and `~/src/dhs15-eff3750`, both removed after the run; logs `E:\work\lanes\dhs15-eff3750-win.log` and `~/src/dhs15-eff3750-wsl.log`; win record `win-eff3750-20261126T1235Z.txt`): gates 1/13 to 5/13 (`gofmt`, `vet`, `staticcheck`, `modules`, `test`) **passed on both lanes with nothing suppressed** (test: win 5m51.631s, wsl 3m1.623s), so the three gate-5 failures are gone. Gate 6/13 `coverage` **failed on both lanes** on packages from `main` this stage does not own: `internal/host/launch` (win 77.8%, wsl 78.2% < 85%; #165), `internal/wire/sdkserver` (80.3% < 85% on both; #166), and on Windows also `internal/world/sandbox/local` (77.4% < 85%). `main` cleared exactly these in #172 (3e631e9, test-only: `launch/refusal_test.go`, `sdkserver/answer_paths_test.go`, `forward_paths_test.go`, `methods_test.go`, `sandbox/local/edges*_test.go`), merged into this branch as bd1260b. With it, `go vet` (tags `''` and `replay,scenario,bench`) and `go test -cover` pass on those three packages. The lanes rerun at bd1260b. Sixth attempt at bd1260b (`E:\work\lanes\dhs15-bd1260b` and `~/src/dhs15-bd1260b`, both removed after the run; logs `E:\work\lanes\dhs15-bd1260b-win.log` and `~/src/dhs15-bd1260b-wsl.log`; win record `win-bd1260b-20261126T2359Z.txt`, wsl record `wsl-bd1260b-20261126T2359Z.txt`, WSL rehearsal exit 0): gates 1/13 to 12/13 (`gofmt` to `vulncheck`, including 6/13 `coverage`) **passed on both lanes with nothing suppressed**. Gate 13/13 `self` **failed on both lanes** (exit 1) on one test, `tools/harness-gate` `TestRequireSubcommand` (`scenarios_test.go:228`). Its expected `require --stage` lists for DH-S-22 and DH-S-39 predated this stage's index entries (`DH-E-HOST-1,DH-E-HOST-2` and `DH-E-HOST-7,DH-E-HOST-8,DH-E-HOST-9`). That failure comes from this stage's own index change, and the expectations are now updated (`GOWORK=off go test -run '^TestRequireSubcommand$' .` ok). The same commit tightens two scenario checks without changing any index entry: the DH-E-HOST-1 sign-out step returns the normal window to the host before it captures and revokes its cookie, and a confined effect counts as reported only when the result's `exposed` list names it, not when only the status text mentions it (`TestListedErrorRequiresTheResult`). The lanes rerun on the commit carrying these. Seventh attempt at c7562fb (that commit; `E:\work\lanes\dhs15-c7562fb` and `~/src/dhs15-c7562fb`; logs `E:\work\lanes\dhs15-c7562fb-win.log` and `~/src/dhs15-c7562fb-wsl.log`): started detached 2026-11-27T00:55Z, WSL preflight passed. The first Windows run stopped during gate 4/13 `modules` (after gates 1/13 to 3/13 passed) with no exit status. Its process was ended from outside, and that is not a gate result. It was restarted at 2026-11-27T00:59Z on the same clean checkout (log `E:\work\lanes\dhs15-c7562fb-win2.log`); its partial record was moved out of the tree. WSL result (record `wsl-c7562fb-20261127T0054Z.txt`, sha256 `4d9236f4…3031`, ended 2026-11-27T00:58:25Z, rehearsal exit 0, clone removed): gates 1/13 to 4/13 **passed**. Gate 5/13 `test` **failed** (exit 1) on one test, `internal/world/subprocess` `TestHardLimitEndsTheTree/past_a_polite_stop_in_grace` (`collect_test.go:329: the tree child said "": unexpected EOF`; `go test -tags= -race -shuffle=on -count=1 ./...`, shuffle seed 1795740937831312523). This stage does not touch that package: the branch diff against `origin/main` covers only `testkit/scenarios`, the acceptance index, `tools/harness-gate/scenarios_test.go` and this file. The same test passed at gate 5 on both lanes at eff3750 and bd1260b. By product policy "Full harness gate" it is recorded and raised as open question `s15-subprocess-hardlimit`, not re-run or investigated here. Windows (second run; record `win-c7562fb-20261127T0059Z.txt`, sha256 `12dccb74…858d`, ended 2026-11-27T01:45:08Z, exit 0, worktree removed): **passed all 13 gates** (`gofmt` to `self`, including 5/13 `test` 5m44.846s and 6/13 `coverage`), nothing suppressed. So at c7562fb Windows passes every gate, and WSL fails only gate 5/13 on the unrelated subprocess test above. The record stays open: both lanes must pass on the final commit. The next WSL run follows the operator's answer to `s15-subprocess-hardlimit`. That answer is to treat the WSL failure as a flake and re-run both lanes on the next commit; the c7562fb WSL record above stays as evidence. Eighth attempt at 6422ca4 (its only change from c7562fb is this file; `E:\work\lanes\dhs15-6422ca4` and `~/src/dhs15-6422ca4`; logs `E:\work\lanes\dhs15-6422ca4-win.log` and `~/src/dhs15-6422ca4-wsl.log`): started detached 2026-11-27T01:46Z, and both preflights passed. **Passed all 13 gates on both lanes, nothing suppressed.**

- **WSL:** record `wsl-6422ca4-20261127T0145Z.txt` (sha256 `abcad384…37e7`), ended 2026-11-27T01:56:59Z, exit 0 (`lane-exit: 0`). The rehearsal (`wsl-6422ca4-20261127T0145Z-rehearse.txt`) also exited 0. Gate 5/13 `test` passed in 3m2.845s, including `TestHardLimitEndsTheTree`.
- **Windows:** record `win-6422ca4-20261127T0146Z.txt` (sha256 `82bbad94…f9d`), ended 2026-11-27T02:20:25Z, exit 0. Gate 5/13 `test` passed in 4m33.129s and 6/13 `coverage` in 19m49.013s.
- **TC-12g:** `git diff origin/main...6422ca4 -- src/sdk/protocol/` is empty, so `schema.json` is unchanged.

Both temporary checkouts are removed, and the records are kept outside the tree (`E:\work\lanes\` and WSL `~/src/lane-records/`). The S15 gate record (M2-DOD-1) **passes** at 6422ca4. Commits after it change only this file. The two `manual` rows stay open, as decided above. No attempt before the eighth counts as a pass.

Where the repairs live: the formatting, vet and gate-5 repairs named above (`host/launch/component.go`, `host/web/web_test.go`, `host/launch/launch_test.go`, `wire/sdkserver/forward_test.go`, the DH-S-15 e2e test, `cli/output/status.go`, `allevents/testdata/persisted.notes` and `persisted.golden`) were made on this branch in earlier iterations and then landed on `main` in #172 (3e631e9), together with the coverage tests. That commit was merged back here as bd1260b. They are history already in the merge target, so the branch diff against `origin/main` no longer contains them.
