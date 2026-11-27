# DH-S-17 on a signed-in WSL machine (M2): the operator's runbook

Status: **runbook written (implementation-plan Stage 7.1), not run.** Nothing in this file is
evidence; a run fills the rows of the Scenarios table and the evidence record of
[DH-S-17.md](DH-S-17.md).

This is the by-hand form of the `s17` job of
[harness-scenarios.yml](../../../../.github/workflows/harness-scenarios.yml), for when no
self-hosted runner carries the job's labels
`[self-hosted, linux, dsh-e1, dsh-signed-in-claude, dsh-signed-in-copilot]` (risk M2-R-01,
M2-EN-8). The steps below are the job's steps, in the job's order, with the same commands;
only the place they run differs. It follows the form of the M1
[DH-S-11 runbook](../../harness-DSH-M1/evidence/DH-S-11-manual-runbook.md).

## What the run proves, and what it does not

The 14 DH-S-17 scenarios (the `DH-S-17` entry of `test/harness/acceptance-index.yaml`,
tech-spec sec. 4.2 rows 5 to 18) run against the built `dsh` on the user's own signed-in
Claude Code route, which must pass, with the Copilot CLI route recorded either way
(M2-DEC-4). A required scenario that skips fails the run (M2-DEC-3).

A run that did not happen is **not run**, never passed. If the job was dispatched and no
runner picked it up, every S17 row stays `not run (no runner)` until this runbook or a
runner produces a result (M2-R-01, M2-C-2).

## Who, where, and with what

- **Who.** The owner or an operator, signed in to the clients as themselves. Never an agent
  session.
- **Where.** A Linux (WSL 2) distribution on the operator's workstation (E1, M2-EN-1), from
  the repository root of a clean checkout of the commit under test. `bwrap` must be
  installed for DH-E-SDK-1.
- **Credential.** Your existing sign-in (`claude auth login`, `copilot login`) inside the
  distribution. Do not type, set or export any key or token: there is none (M2-NG-2,
  M2-C-11). The routes remove every credential variable from the client's environment
  anyway.
- **Route inputs.** `DSH_SCENARIO_CLAUDE_CODE_MODEL`, `DSH_SCENARIO_CLAUDE_CODE_EFFORT`,
  `DSH_SCENARIO_COPILOT_CLI_MODEL`, `DSH_SCENARIO_COPILOT_CLI_EFFORT`,
  `DSH_SCENARIO_PREFLIGHT_LAYER`, `DSH_SCENARIO_PREFLIGHT_PROMPT` and the per-scenario
  `DSH_SCENARIO_*` inputs each scenario's doc comment names. On a self-hosted runner they
  live in the runner's own environment; by hand, export them in the shell. None is a
  credential.
- **Go.** `GOTOOLCHAIN=go1.27.4` (the gate floor), `GOWORK=off`.

## Which machine the record must cite

A row run by this runbook cites the WSL machine, never "E1" alone and never a runner:

- **Environment cell** of each Scenarios row: `E1 wsl:<windows-host>/<distro>`, where
  `<windows-host>` is the output of `cmd.exe /c hostname` and `<distro>` is
  `$WSL_DISTRO_NAME`, both read in step 1 (for example `E1 wsl:DEVBOX7/Ubuntu-24.04`).
- **Environment and fixture** section: that same id, the distribution's `PRETTY_NAME` from
  `/etc/os-release`, the kernel (`uname -r`), the `claude --version` and
  `copilot --version` lines, `bwrap --version` and `go version`.

A row run by the `s17` job cites `E1 runner:<runner name>` and the workflow run id instead.
One stage record may mix both, row by row; a row with no machine named does not close.

## Steps

All commands are bash, from the repository root.

1. **Name the machine and the commit.** Paste each line into the record:

   ```bash
   echo "E1 wsl:$(cmd.exe /c hostname | tr -d '\r')/$WSL_DISTRO_NAME"
   grep PRETTY_NAME /etc/os-release; uname -r; go version; bwrap --version
   git status --porcelain   # must print nothing: a dirty tree is not the commit under test
   git rev-parse HEAD
   ```

2. **Entry** (the job's first step; M2-DEC-1). It must exit 0:

   ```bash
   GOWORK=off go -C tools/harness-gate run . entry --root ../..
   ```

3. **Signed-in route preflight, claude-code (required).** Build `dsh` once, record its
   checksum, then one probe turn on the Claude Code route. A failure here stops the run;
   no scenario starts:

   ```bash
   set -o pipefail; mkdir -p artifacts/harness
   GOWORK=off go -C src/harness build -o "$PWD/artifacts/harness/dsh" ./cmd/dsh
   export DSH_BIN="$PWD/artifacts/harness/dsh"
   sha256sum "$DSH_BIN" | tee artifacts/harness/s17-dsh.sha256
   claude --version | tee artifacts/harness/s17-clients.txt
   DSH_REQUIRE_SCENARIOS= GOWORK=off go -C src/harness test -tags scenario -count=1 -v \
     -run '^TestSignedInRoutePreflight$/^claude-code$' ./internal/testkit/scenarios/ 2>&1 \
     | tee artifacts/harness/s17-preflight-claude-code.txt
   ```

4. **Signed-in route preflight, copilot-cli (recorded).** Its result is kept either way; a
   failure makes the copilot-cli legs read `failed (route preflight)`:

   ```bash
   copilot --version | tee -a artifacts/harness/s17-clients.txt
   DSH_REQUIRE_SCENARIOS= GOWORK=off go -C src/harness test -tags scenario -count=1 -v \
     -run '^TestSignedInRoutePreflight$/^copilot-cli$' ./internal/testkit/scenarios/ 2>&1 \
     | tee artifacts/harness/s17-preflight-copilot-cli.txt
   ```

5. **Derive the required list** from the `DH-S-17` index entry (M2-DEC-3). It prints the 14
   IDs of `tools/harness-gate/testdata/s17_require.golden`:

   ```bash
   export DSH_REQUIRE_SCENARIOS="$(GOWORK=off go -C tools/harness-gate run . require --root ../.. --stage DH-S-17)"
   echo "$DSH_REQUIRE_SCENARIOS"
   ```

6. **Preflight.** The index, the authored tests, built-through and the Claude sign-in; it
   writes the test plan to the file `GITHUB_OUTPUT` names, as on a runner. `count` must
   read 14:

   ```bash
   export GITHUB_OUTPUT="$PWD/artifacts/harness/s17-plan.txt"; : > "$GITHUB_OUTPUT"
   GOWORK=off go -C tools/harness-gate run . preflight --root ../..
   cat "$GITHUB_OUTPUT"
   ```

7. **Run the 14 scenarios** with the plan of step 6, one package at a time:

   ```bash
   RUN="$(sed -n 's/^run=//p' "$GITHUB_OUTPUT")"
   export DSH_SCENARIO_REPORT="$PWD/artifacts/harness/s17-report.txt"
   GOWORK=off go -C src/harness test -tags scenario -count=1 -p 1 -timeout 110m -v -run "$RUN" ./... 2>&1 \
     | tee artifacts/harness/s17-scenarios.txt
   ```

   The run fails if any line reads `--- SKIP`, or if a test of the plan's `tests=` line has
   no `--- PASS:` line.

## The record

Fill [DH-S-17.md](DH-S-17.md) while it reads `Closure status: open`:

- each Scenarios row: Environment as named above, the commit of step 1, the result as seen
  (`passed`, `failed (<reason>)`, `not run (<reason>)`), and in Artifacts the `dsh`
  SHA-256, the model id and the scenario's report row;
- the copilot-cli rows kept whatever their result (M2-DEC-4);
- Exact tests and commands: steps 2 to 7 as run, and `DSH_REQUIRE_SCENARIOS`.

Keep `artifacts/harness/` outside the commit; copy only the lines the record quotes. Never
paste a token, a cookie or an account e-mail.
