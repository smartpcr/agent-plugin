# Manual checks at 593ceaa3efd55d41bf0eeaced5ba7e26acc71966 (plan 18.1.5 and 18.1.6), run 2026-11-23

**Operator.** The lead Claude Code session ran every check below, at the owner's instruction of 2026-11-23 ("you should do that"). The owner did not watch these runs. This departs from M1-D-CLOSE-2 A ("a named person runs it at the recorded commit, watches it and records it `manual`") and from the runbook rule that signed-in checks are never run from an agent session. It needs to be recorded as an owner decision.

**Hosts.**
- Windows 11 Enterprise 10.0.28000, x86_64.
- WSL Ubuntu 24.04.5 LTS, kernel 6.18.58.1-microsoft-standard-WSL2, clone `~/src/agent-plugin` on ext4 at 593ceaa.

**Logs.** All are in this folder:
- `register-windows-593ceaa-20261123T2213Z.log`
- `register-wsl-593ceaa-20261123T2237Z.log`
- `byhand-windows-593ceaa.log`
- `byhand-windows-593ceaa-part2.log`
- `byhand-wsl-593ceaa.log`
- `signedin-dhs9-593ceaa.log`
- `signedin-m1-claude-593ceaa.log`
- `signedin-m1-copilot-593ceaa.log`

## Scripted rows (`run-register.sh`)
- **Windows:** 87 rows; 85 PASS and 2 FAIL (1.2 and 1.5). Both failures come from the seeded case `coverage-child`. Its fixture is built only for the coverage gate. Under the full gate, its `TestHelperBinary` fails at gate `test` because `GOCOVERDIR` is not set ("warning: GOCOVERDIR not set, no coverage data emitted"), so the full gate never reaches `coverage`.
- **Lead settlement, the same as option (a) for budget-exceeded, cross-broken and vuln-stdlib:** the case is judged by the gate's own test, which drives `coverageGate` directly. `TestCoverageMergesChildProcesses` (DH-T-TOOLCHAIN-7) passed with all 3 subtests: both children, no re-executed child and no helper binary (`byhand-windows-593ceaa.log`). Under the amended procedure, rows 1.2 and 1.5 therefore PASS. All their other cases passed in the register log.
- **Amended cases:** budget-exceeded and budget-exceeded-relaxed are judged by `TestBudgetGateClearsOverrides` (PASS), cross-broken by `TestCrossCompileNamesFailingTarget` (PASS on both OSes), vuln-stdlib by `TestVulncheckGateUsesTheDatabase` (PASS), and coverage-child by `TestCoverageMergesChildProcesses` (PASS).
- **WSL (`--os wsl`):** 11 rows, all PASS (the 10 both-lane rows and 1.9).

## By-hand rows
- **1.1 (Windows):** the three modules build together with the workspace (exit 0). `go list -m` printed `.../src/go`, `.../src/sdk` and `.../src/harness`. The release build with `GOWORK=off` succeeded (exit 0). `dsh --version` printed `dsh (devel) (593ceaa3efd55d41bf0eeaced5ba7e26acc71966; go1.27.4; windows/amd64)`. The bare `dsh` printed `dsh: cli/usage: missing <profile> or <command>` and exited 64; the checklist expected `dsh (devel)` there, so `--version` is the line recorded. The clone HEAD was 593ceaa3. **PASS**.
- **1.8 (Windows):**
  - `ci.yml:138` and `harness-scenarios.yml:75` both read `os: [ubuntu-latest, windows-latest]`, with the comment `platforms-windows-wsl: hosted regression lanes; no macOS.` This equals M1-D-GATE-21 / M1-D-TOOL-5, in force.
  - The preflight with no key printed `passed; 0 required scenarios` and exited 0.
  - With `DSH_REQUIRE_SCENARIOS=DH-E-FAKE-9` it printed `FAILED, no scenario is started:` and `required scenario DH-E-FAKE-9 ... has no scenario test`, then exited 1.
  - **PASS**.
- **1.9, by-hand part (WSL):** the rehearse record `wsl-593ceaa-20261123T2122Z-rehearse.txt` shows six binaries (linux, darwin and windows on amd64 and arm64), each with `vcs.revision=593ceaa3…` and `vcs.modified=false`, plus `exit: 0` and `sha256: 14a4f38f…`. That record was rebuilt from the lane's tee'd output after the original file was deleted by mistake. The clone's tags (7, all created 2026-11-17) predate the run, so the lane created no tag, and the rehearsal printed "nothing published". **PASS**.
- **3.1, 3.4, 3.5, 3.8 (Windows; one run of the throwaway test in a throwaway worktree):** **PASS**.
  - 3.1: `error: loader: <TMP>\user.json:4:7: [0].insert[0].disabeld: unknown key`.
  - 3.4: `echo: +good`.
  - 3.5: `layer: label=base writable=false`, `layer: label=user writable=true`, `unchanged on disk: base.json = true`, `user.json = true`.
  - 3.8: `notices: 1` and `notice: created=[good] issues=[<TMP>\user.json:4:7: [0].insert[0].disabeld: unknown key]`.
- **4.8:** search agrees with Git except for the known difference M1-G-FND-23.1. **Seen as above; the known gap stays open.** A pass here relies on M1-G-FND-23.1 being accepted as a known difference.
  - **Windows** (git 2.56.0.windows.4, `core.autocrlf=false`): git 1625 and search 2775. Only in git: the two gitlinks `.worktree/plan-harness-DSH-e2e-scenarios` and `.worktree/plan-harness-DSH-implementation-plan`. Only in search: 576 files inside each of those two nested worktrees.
  - **WSL ext4 clone** (git 2.43.0): git 1625 and search 1623. Only in git: the same two gitlinks, which are empty directories in the clone. Only in search: nothing.
  - **WSL `/mnt/e`:** the same as Windows: git 1625 and search 2775, with the two gitlinks only in git and 576 files in each nested worktree only in search.
- **4.9 (b), WSL ext4, vim 9.1:** three saves with `vim -es -c 'normal Ax' -c wq`, 3 s apart, gave `watching ... mode=notify`, notices 1, 2 and 3 at 3.22 s, 6.23 s and 9.29 s, and `total notices 3 mode=notify`. **PASS**.
- **4.9 (a) Windows VS Code and (c) WSL `/mnt/e` with a Windows editor:** **NOT RUN.** Both need a person saving in a GUI editor. They stay open; (a) has been carried over since 14.5.5.
- **6.8, by-hand part (Windows):** **PASS**.
  - `dsh --help` exited 0, printed 34 lines, first line `Usage: dsh [options] [profile|command...]`, with empty stderr.
  - `dsh --no-such-flag` exited 64 with empty stdout, and stderr `dsh: cli/usage: unknown flag: --no-such-flag` and `Run 'dsh --help' for usage.`.
- **Cleanup:** every throwaway worktree was removed. The main tree's `git status --porcelain` was unchanged (empty). The `git worktree list` shows only the main tree and the two pre-existing `.worktree/*` entries.

## Signed-in rows (Windows native; neither client is installed in WSL)
- **Clients:** Claude Code 2.1.312 (`claude.exe`) and GitHub Copilot CLI 1.0.115-7 (`copilot.exe`). No key was set (`api-key-source=none`). No vendor config or credential directory was read.
- **DH-S-9 `TestManualSignedIn`:**
  - **Claude signed-in: PASS.** One `assistant/message` (seq 5) with finish `tool_calls`; route claude-code, model sonnet, reported `claude-sonnet-5-5`; session `33506a25…`; usage in:2, out:55, cache-read:0, cache-write:1095; `init-tools=[mcp__dsh__probe]`. This settles row 9.3's usage half.
  - **Claude signed-out: PASS.** One `assistant/attempt`, `llm/sign-in-required`, class permanent, `llm-retry=0`, `retry-abandoned=not-retryable`, `other-route-prepares=0`, `other-route-attempts=0`.
  - **Copilot signed-in: FAIL.** It settled `assistant/attempt` with `llm/client-contract` (permanent) and no message. The probe saw first-event `session.start`, last-event `result`, 12 lines. Recorded as open per M1-D-LOOP-6.
  - **Copilot signed-out:** `llm/client-contract`, not `llm/sign-in-required`, recorded as seen (DH-E-COPILOTCLI-1 is open).
- **DH-S-11 Claude route: PASS for both tests.**
  - `TestManualM1OneTurn`: a 19-event bracketed log and a single probe call (`probed: ping`, approval `allowed-once`).
    - Request 1 (asOf 7) and request 2 (asOf 15): the sent and rebuilt digests are equal, and `invocation-equal=true` (args and stdin digests equal).
    - `turn/end` completed, with the final reply "The probe tool returned: `probed: ping`".
  - `TestManualM1CancelMidStream`: an 11-event cancelled log with `step/end` cause user and `turn/end` aborted cause user; the interrupted prefix was kept (2 bytes); session `c7a904a2…`.
  - No client process was left: the set of claude and copilot PIDs was the same before and after.
  - The log has no token text. The only hits for the token pattern are the `first_token` timing field names.
- **DH-S-11 Copilot route: FAIL for both tests.**
  - One turn: `turn/end` error `llm/client-contract: copilot-cli: the client exited with status 1 and printed nothing this route can place`, and the answerer was asked 0 times.
  - Cancel: "no answer text streamed within 2m".
  - No client process was left. Recorded as open per M1-D-LOOP-6.
- **Side observation:** the system prompt identity line reads "You are dsh, the DeepSeek harness: ...". That text is product copy (`prompt.IdentityLine`), not a scenario Given.
