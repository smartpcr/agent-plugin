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
status=$?; echo "exit $status"; echo "exit $status $(git rev-parse HEAD)" > artifacts/harness/s17-exit.txt
```

The run passed only if the block's last lines read `S17 run on <sha>: passed` and
`exit 0` (also saved, with the commit, in `artifacts/harness/s17-exit.txt`). Any other exit is a failed run, named by the last command's output; record it as
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

DH-E-SANDBOX-7 (impl.md 17.9) runs last in S17 (architecture.md R-03), on the binary built
from the head of the unmerged PR branch `s17/policy-text`, the only commit that carries
shipped sandbox policy text: none may exist on `main` until the scenario has passed on its
exact bytes (M2-C-6). `main` already carries every byte the branch ships, kept out of
`policy/text/` so nothing embeds them:

- `src/harness/internal/world/sandbox/policy/testdata/draft_text/{linux,darwin,windows}.md`,
  the texts (`TestDraftTextsLoad`: each loads and renders);
- `.../policy/testdata/s17_branch/policy_section_{linux,darwin,windows}.golden`, their goldens
  (`TestDraftTextGolden`: each equals the render of its draft);
- `.../policy/testdata/s17_branch/text_embed.go.txt`, the loader that embeds `text/*.md`.

The branch only moves them, so its commit carries the reviewed bytes, the texts and their
goldens together. S17 therefore runs once, on the branch head: steps 1 to 7 above run all 14
scenarios with SANDBOX-7 last, and SANDBOX-7 runs nowhere else.

### A. Cut the branch (moves only, one commit)

```bash
bash -euo pipefail <<'EOF'
p=src/harness/internal/world/sandbox/policy
git switch -c s17/policy-text origin/main
git mv "$p/testdata/draft_text" "$p/text"
git mv "$p"/testdata/s17_branch/policy_section_*.golden "$p/testdata/"
git mv "$p/testdata/s17_branch/text_embed.go.txt" "$p/text_embed.go"
# The goldens are moved, not re-recorded: TestShippedTextGolden must pass as is.
GOWORK=off go -C src/harness test -count=1 ./internal/world/sandbox/policy/ ./internal/tools/prompt/...
sha256sum "$p"/text/*.md
git commit -m "Stage 7.6: ship the sandbox policy texts (DH-E-SANDBOX-7 pending)"
git push -u origin s17/policy-text
EOF
echo "exit $?"
```

On this commit `harness-gate policy-text` refuses all three texts (no passed row yet); that is
expected, and is why the branch is not merged before step C.

### B. Run S17 once, on the branch head

From a clean checkout of the branch head, export the SANDBOX-7 inputs (none is a credential,
none is committed): `DSH_SCENARIO_SANDBOX7_TASKS` (the twelve tasks, `escalate<TAB>prompt` or
`local<TAB>prompt`, six of each, over `{workspace}` and `{outside}`),
`DSH_SCENARIO_SANDBOX7_LAYER` (a layer over `{platform}` setting `sandbox.policy`
`platform: {platform}`, no title route), `DSH_SCENARIO_SANDBOX7_PREVIOUS=none`. Then run the
block of steps 1 to 7 above, as written, on that commit. Step 7 runs the 13 other scenarios
and then SANDBOX-7, once, inside the 100 min budget; SANDBOX-7 writes its counts last to
`artifacts/harness/s17-report.txt` (`linux=n,darwin=n,windows=n`). The block passed only if it
ends `S17 run on <sha>: passed` and `exit 0`, which it saves with the commit in
`artifacts/harness/s17-exit.txt` for step C.

**Shard (M2-R-06), only when the budget ran out before SANDBOX-7.** If the block failed and
its record names only `DH-E-SANDBOX-7 not run: the ... scenario budget is spent` (every other
scenario passed), SANDBOX-7 never started; run it per text on the same commit, each shard its
own fail-closed block and its own job slot. Never shard after SANDBOX-7 started: that would
run a text twice.

```bash
for text in linux darwin windows; do
bash -euo pipefail -s "$text" <<'EOF'
text="$1"
[ -z "$(git status --porcelain)" ] || { echo "the tree is not clean"; exit 1; }
export DSH_BIN="$PWD/artifacts/harness/dsh"
export DSH_SCENARIO_REPORT="$PWD/artifacts/harness/s17-sandbox7-report-$text.txt"
rm -f "$DSH_SCENARIO_REPORT"
GOWORK=off go -C src/harness test -tags scenario -count=1 -p 1 -timeout 110m -v \
  -run "^TestPolicyTextMakesTheModelEscalate\$/^$text\$" ./internal/testkit/scenarios/ 2>&1 \
  | tee "artifacts/harness/s17-go-test-sandbox7-$text.txt"
EOF
echo "shard $text: exit $?"
done
```

With `pipefail` a failing `go test` fails the block through `tee`. Each shard reports and
judges only its text; a run that fails alone keeps its transcript, and a text below the floor
keeps the transcript of every run (TC-20s).

### C. Record, and merge only on pass

Record only from passed runs, naming each report file explicitly (never a glob: a stale
report of an earlier run must not be joined). `sandbox7-record` reads one line per
`--report`, refuses a platform named twice or a missing one, and records nothing unless every
`--go-test-log` shows the scenario passed.

```bash
bash -euo pipefail <<'EOF'
a=artifacts/harness
sha="$(git rev-parse HEAD)"
if [ -f "$a/s17-sandbox7-report-linux.txt" ]; then   # sharded
  set -- --report "$a/s17-sandbox7-report-linux.txt" --report "$a/s17-sandbox7-report-darwin.txt" \
    --report "$a/s17-sandbox7-report-windows.txt" --go-test-log "$a/s17-go-test-sandbox7-linux.txt" \
    --go-test-log "$a/s17-go-test-sandbox7-darwin.txt" --go-test-log "$a/s17-go-test-sandbox7-windows.txt"
else
  [ "$(cat "$a/s17-exit.txt")" = "exit 0 $sha" ] || { echo "S17 did not pass on $sha"; exit 1; }
  set -- --report "$a/s17-report.txt"
fi
GOWORK=off go -C tools/harness-gate run . sandbox7-record --root ../.. --lane s17 "$@"
EOF
echo "exit $?"
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
  with the failing log, the branch stays unmerged, no shipped text exists on `main`, and S17
  stays open. A re-worded draft is reviewed on `main` (its golden re-recorded there with
  `-run '^TestDraftTextGolden$' -update`) and the branch is cut again.
