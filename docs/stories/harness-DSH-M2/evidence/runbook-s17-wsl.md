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

Run the whole block below in one go, from the repository root, exactly as written. It is the
job's steps in the job's order, and it fails closed as the job does (GitHub runs a
`shell: bash` step as `bash -eo pipefail`; the block adds `-u`): the first failing
command ends the run with a non-zero status, and nothing after it starts. The one command
allowed to fail and carry on is the copilot-cli route preflight of step 4, which is
recorded (M2-DEC-4). Do not paste the steps one by one into an interactive shell; a step
that failed there would leave the next one running on an empty plan.

```bash
bash -euo pipefail <<'EOF'
mkdir -p artifacts/harness

# 1. Name the machine and the commit; a dirty tree is not the commit under test.
echo "E1 wsl:$(cmd.exe /c hostname | tr -d '\r')/$WSL_DISTRO_NAME"
grep PRETTY_NAME /etc/os-release; uname -r; go version; bwrap --version
[ -z "$(git status --porcelain)" ] || { echo "the tree is not clean"; exit 1; }
sha="$(git rev-parse HEAD)"; echo "commit $sha"

# 2. Entry (M2-DEC-1).
GOWORK=off go -C tools/harness-gate run . entry --root ../..

# 3. Signed-in route preflight, claude-code (required): dsh built once, its checksum,
#    one probe turn. A failure ends the run here; no scenario starts.
GOWORK=off go -C src/harness build -o "$PWD/artifacts/harness/dsh" ./cmd/dsh
export DSH_BIN="$PWD/artifacts/harness/dsh"
sha256sum "$DSH_BIN" | tee artifacts/harness/s17-dsh.sha256
claude --version | tee artifacts/harness/s17-clients.txt
DSH_REQUIRE_SCENARIOS= GOWORK=off go -C src/harness test -tags scenario -count=1 -v \
  -run '^TestSignedInRoutePreflight$/^claude-code$' ./internal/testkit/scenarios/ 2>&1 \
  | tee artifacts/harness/s17-preflight-claude-code.txt

# 4. Signed-in route preflight, copilot-cli (recorded): the only step allowed to fail.
log=artifacts/harness/s17-preflight-copilot-cli.txt
copilot --version | tee -a artifacts/harness/s17-clients.txt || true
if DSH_REQUIRE_SCENARIOS= GOWORK=off go -C src/harness test -tags scenario -count=1 -v \
  -run '^TestSignedInRoutePreflight$/^copilot-cli$' ./internal/testkit/scenarios/ 2>&1 | tee "$log"; then
  result=passed
else
  result="failed (route preflight)"
fi
echo "copilot-cli route preflight: $result" | tee -a "$log"

# 5. The required list from the DH-S-17 index entry (M2-DEC-3). A failing require
#    ends the run (a plain assignment keeps its status under -e; export "$(...)"
#    would not).
list="$(GOWORK=off go -C tools/harness-gate run . require --root ../.. --stage DH-S-17)"
export DSH_REQUIRE_SCENARIOS="$list"
echo "stage=DH-S-17 DSH_REQUIRE_SCENARIOS=$list"

# 6. Preflight: the index, the authored tests, built-through and the Claude sign-in.
#    The plan must hold all 14 scenarios.
export GITHUB_OUTPUT="$PWD/artifacts/harness/s17-plan.txt"; : > "$GITHUB_OUTPUT"
GOWORK=off go -C tools/harness-gate run . preflight --root ../..
count="$(sed -n 's/^count=//p' "$GITHUB_OUTPUT" | tail -n 1)"
[ "$count" = 14 ] || { echo "DH-S-17 plans $count scenarios, want all 14 (M2-DEC-3)"; exit 1; }

# 7. The 14 scenarios, through the same command as the job: TOOLCHAIN-1 first, the
#    single-process ones, the host-killing SESSION-2 and AGENT-3, SANDBOX-7 last, one go test per phase (-p 1) of the claude-code legs, which
#    a skip, a failure or a non-zero exit fails, then a recorded go test of the
#    copilot-cli legs, whose pass or failure is kept and whose skip or missing result
#    fails; all within the 100 min budget of the 2 h job. It writes the evidence
#    record and exits non-zero on any required failure or recorded leg not run.
export DSH_SCENARIO_REPORT="$PWD/artifacts/harness/s17-report.txt"
GOWORK=off go -C tools/harness-gate run . scenarios --root ../.. --stage DH-S-17 \
  --evidence "../../artifacts/harness/s17-$sha.txt"
echo "S17 run on $sha: passed"
EOF
echo "exit $?"
```

The run passed only if the block's last lines read `S17 run on <sha>: passed` and
`exit 0`. Any other exit is a failed run, named by the last command's output; record it as
`failed (<reason>)`. The record `artifacts/harness/s17-<sha>.txt` holds each phase's
`go test` command and output, and one `scenarios: recorded leg: <Test>/copilot-cli passed`
(or `failed`, `skipped`, `not run (...)`) line per routed scenario; the copilot-cli rows
are in `artifacts/harness/s17-report.txt.copilot-cli`.
## The record

Fill [DH-S-17.md](DH-S-17.md) while it reads `Closure status: open`:

- each Scenarios row: Environment as named above, the commit of step 1, the result as seen
  (`passed`, `failed (<reason>)`, `not run (<reason>)`), and in Artifacts the `dsh`
  SHA-256, the model id and the scenario's report row;
- the copilot-cli rows kept whatever their result (M2-DEC-4);
- Exact tests and commands: the block of Steps as run, its exit status, and
  `DSH_REQUIRE_SCENARIOS`.

Keep `artifacts/harness/` outside the commit; copy only the lines the record quotes. Never
paste a token, a cookie or an account e-mail.

## DH-E-SANDBOX-7 on s17/policy-text (implementation-plan Stage 7.6)

DH-E-SANDBOX-7 (impl.md 17.9) runs last in S17 (architecture.md R-03) and is the one scenario
that does not run on `main`: no shipped sandbox policy text may exist there until this
scenario has passed on its exact bytes (M2-C-6). `main` carries the reviewed drafts in
`src/harness/internal/world/sandbox/policy/testdata/draft_text/{linux,darwin,windows}.md`
(test data only: no build embeds them, `TestDraftTextsLoad` checks that each loads and
renders) and the loader `loadTexts`; the PR branch `s17/policy-text` ships them. Run this
only after the 13 other scenarios have run (step 7 above), by the same operator, on the same
WSL machine.

### A. Prepare the branch (one commit: texts, goldens and the loader together)

```bash
bash -euo pipefail <<'EOF'
p=src/harness/internal/world/sandbox/policy
git switch -c s17/policy-text origin/main
git mv "$p/testdata/draft_text" "$p/text"
cat > "$p/text_embed.go" <<'GO'
package policy

import (
	"embed"
	"io/fs"
)

// shippedFS is the shipped policy texts, world/sandbox/policy/text/*.md
// (Stage 7.6; M2-C-6: each merged only with a passed DH-E-SANDBOX-7 run on its
// exact bytes, harness-gate policy-text).
//
//go:embed text/*.md
var shippedFS embed.FS

func init() {
	dir, err := fs.Sub(shippedFS, "text")
	if err == nil {
		texts, err = loadTexts(dir)
	}
	if err != nil {
		panic(err)
	}
}
GO
# Record policy_section_{linux,darwin,windows}.golden from the shipped texts (CI must be unset).
GOWORK=off go -C src/harness test ./internal/world/sandbox/policy/ -run '^TestShippedTextGolden$' -update
GOWORK=off go -C src/harness test ./internal/world/sandbox/policy/ ./internal/tools/prompt/...
sha256sum "$p"/text/*.md
git add "$p" && git commit -m "Stage 7.6: ship the sandbox policy texts (DH-E-SANDBOX-7 pending)"
git push -u origin s17/policy-text
EOF
```

On this branch `harness-gate policy-text` refuses all three texts (no passed row yet); that is
expected, and is why the branch is not merged before step C.

### B. Run the 108 runs on the branch head

From a clean checkout of the branch head, with steps 1 to 6 of the block above already passed
on that commit, and these inputs (none is a credential, none is committed):
`DSH_SCENARIO_SANDBOX7_TASKS` (the twelve tasks, `escalate<TAB>prompt` or `local<TAB>prompt`,
six of each, over `{workspace}` and `{outside}`), `DSH_SCENARIO_SANDBOX7_LAYER` (a layer over
`{platform}` setting `sandbox.policy` `platform: {platform}`, no title route),
`DSH_SCENARIO_SANDBOX7_PREVIOUS=none` (the first text), `DSH_SCENARIO_CLAUDE_CODE_MODEL`.

```bash
export DSH_SCENARIO_REPORT="$PWD/artifacts/harness/s17-sandbox7-report.txt"
GOWORK=off go -C src/harness test -tags scenario -count=1 -p 1 -timeout 110m -v \
  -run '^TestPolicyTextMakesTheModelEscalate$' ./internal/testkit/scenarios/ 2>&1 \
  | tee artifacts/harness/s17-go-test-sandbox7.txt
```

If the 2 h job limit is hit (M2-R-06), shard per text on the same commit: run with
`-run '^TestPolicyTextMakesTheModelEscalate$/^linux$'` (then `darwin`, `windows`), each with its
own `DSH_SCENARIO_REPORT` (`s17-sandbox7-report-<text>.txt`); each shard reports and judges only its text, and the three lines
are joined (`linux=n,darwin=n,windows=n`) for step C. The transcripts of every failed run are
kept (TC-20s).

### C. Record, and merge only on pass

```bash
counts="$(paste -sd, artifacts/harness/s17-sandbox7-report*.txt)"   # linux=n,darwin=n,windows=n
GOWORK=off go -C tools/harness-gate run . sandbox7-record --root ../.. --escalated "$counts" --lane s17
```

It writes `docs/stories/harness-DSH-M2/evidence/runs/s17-<sha7>-<UTC>.txt` (commit, the SHA-256
of each text, its count; exit 0 only when every text has at least 10 of 18).

- **Pass.** On the same branch, in [DH-S-17.md](DH-S-17.md): the `DH-E-SANDBOX-7` row reads
  `E1 wsl:<host>/<distro>` | `<sha7>` | `passed` | `run=<id>; dsh sha256=<hex>; model=<id>;
  linux.md sha256=<hex> escalated=<n>/18; darwin.md sha256=<hex> escalated=<n>/18;
  windows.md sha256=<hex> escalated=<n>/18`; step 17.9 takes the commit; `UP-DEBT-found.Q3.2`
  is marked closed. Commit the record and the row, then
  `GOWORK=off go -C tools/harness-gate run . policy-text --root ../..` must print `ok` for the
  three texts; the PR merges only then (M2-C-6, no exception).
- **Fail.** Commit nothing to `main`. The row on `main` reads `failed (<text>=<n>/18 ...)`
  with the run id, the branch stays unmerged, no shipped text exists on `main`, and S17 stays
  open. A re-worded draft is reviewed on `main` and the branch is cut again.
