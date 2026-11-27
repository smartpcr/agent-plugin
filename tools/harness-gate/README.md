# harness-gate

`harness-gate` is the single quality gate of the repository (QG-1,
DH-ADR-TOOLCHAIN-9). It runs one fixed, ordered list of gates, stops at the first
failure, prints that gate's name and exits non-zero. The same command decides
"this commit passes" on a laptop and in CI:

```sh
GOWORK=off go -C tools/harness-gate run . --root ../..
```

`--root` is the repository root (the gates then run on the product modules
`src/go`, `src/sdk`, `src/harness`, RM-1) or a directory holding a `go.mod` (the
gates then run on that one module; the seeded modules of `testdata/` are run this
way). Exit status: `0` every gate passed, `1` a gate failed, `2` usage or setup
error. Every child runs on the go floor (a toolchain that is not the floor is a
setup error), and `--evidence FILE` records the run; see [Lanes](#lanes).

The module has its own `go.mod` and is not part of the root `go.work`; run it with
`GOWORK=off` (RM-2).

## Gates, in order

| # | gate | what fails it |
|---|------|---------------|
| 1 | `gofmt` | any `.go` file not `gofmt`-formatted (`testdata/`, `vendor/`, nested modules skipped) |
| 2 | `vet` | `go vet ./...`, then the own analyzers through `go vet -vettool=<harness-gate>` (QG-2), each with `-tags=`, `-tags=replay,scenario,bench` and `-tags=e2e` (the e2e layer and the `bench` files are vetted, never run: M1-D-GATE-25 A); then prints `exceptions: N`, the accepted `//lint:ignore` directives of the modules (see [Site exceptions](#site-exceptions-lintignore)) |
| 3 | `staticcheck` | any finding of the pinned staticcheck (default checks), run per module with `-tags=` and `-tags=replay,scenario,bench` from the binary of the tools module's `tool` line (`go tool -n staticcheck`); the output names the check, for example `SA4000` (M1-G-TOOL-1) |
| 4 | `modules` | in every product, test and tools module: `go mod tidy -diff` prints anything, or `go mod verify` fails; then a module rule of `modcheck` (RM-1 to RM-4, `src/go` requiring anything) or a pin without a reason comment (RM-10). See [Modules](#modules-modcheck) |
| 5 | `test` | `go test -tags= -race -shuffle=on -count=1 ./...` per module with `CGO_ENABLED=1` (QG-4); the `replay` and `scenario` layers are excluded |
| 6 | `coverage` | any package under 85% of statements, profiles merged across processes (QG-3); the packages of `CoverageDebt` are held to their pinned floor until they reach 85% |
| 7 | `trace` | `tools/harness-trace verify` (QG-9), then the [`trace-tests`](#test-traceability-dshtest-trace-tests-m1-g-close-9) check for DH-S-1 through the index's `built-through`: a required `DH-T` ID that no test names and no backed deferral defers, a deferral impl.md does not back, a misplaced directive or a broken deliverable link (step 18.1.4). Both checks run and both failures are named; a root without the tool, impl.md or the index fails |
| 8 | `scenario-index` | `test/harness/acceptance-index.yaml` missing or malformed; a `*_scenario_test.go` ID not indexed, indexed twice or bound to two tests; an indexed ID authored at or before `built-through` without a file (DH-ADR-TOOLCHAIN-12) |
| 9 | `policy-text` | a file under `src/harness/internal/world/sandbox/policy/text/` without a passed `DH-E-SANDBOX-7` row of `DH-S-17.md` recording that file's SHA-256 (M2-C-6); see [Sandbox policy text](#sandbox-policy-text-m2-c-6) |
| 10 | `budget` | a `TestBudget*` test over its compiled-in budget (QG-7) |
| 11 | `harness-replay` | `go test -count=1 -tags=replay ./test/replay/...` in the dsh module: a committed recording of `src/harness/test/replay/testdata` that the built `dsh` (testhook tag) replays without a key to a different normalised log, workspace or model requests, or two replays that differ (`DH-T-TOOLCHAIN-1`, `-5`; decision `replay-gate-step-dh-s-16`). `DSH_REPLAY_REFRESH` is never passed on; a dsh module without `test/replay` fails |
| 12 | `cross-compile` | `GOWORK=off CGO_ENABLED=0 go build ./cmd/dsh` for linux, darwin, windows × amd64, arm64 (QG-8) |
| 13 | `vulncheck` | `govulncheck ./...` per module reports a vulnerability the code reaches (exit 3), or the vulnerability database cannot be read or reached (exit 1): it fails and is re-run, it never skips. `Config.VulnDB` selects `-db`; the default is `https://vuln.go.dev`. It is the one gate that needs the network, so it runs after every local check (M1-G-TOOL-4, M1-D-TOOL-1 (a)) |
| 14 | `self` | the gate's own tests: `GOWORK=off go test -tags= -count=1 -skip '^TestRepositoryPassesEveryGate$' ./...` in `tools/harness-gate`, without the `e2e` tag (that test runs this list and would recurse); a root without the module fails (M1-G-TOOL-3) |

The list has the 12 gates of M1-D-GATE-23 plus `policy-text` and `harness-replay`: plan stage 13.4
added `modules` and `self`, stage 13.5 added `staticcheck` and `vulncheck`, which
run the linters pinned in the tools module (`linters.go`), and harness:DSH-M2
Stage 2.4 added `policy-text` and Stage 6.8 `harness-replay`. Both lanes run the whole list, so the rule is in
force on both. `TestGateListIsFixed` pins the order.

`gofmt` also checks the test modules' files. Every gate runs on the go floor.

There is no "skipped" state. Nothing reads a baseline or exemption file: a finding
is fixed in the code, not listed away (QG-2, QG-3, DH-ADR-14). The one way to accept
a finding is a reasoned directive at its site, below.

## Modules (`modcheck/`)

`modcheck` reads `go.mod` and `go.work` with its own line parser (standard library
only) and knows three classes of module:

| class | modules | rules |
|-------|---------|-------|
| product (RM-1) | `src/go`, `src/sdk`, `src/harness` | exactly these three, all in `go.work`, one go floor (RM-2), no `replace` (RM-4), `src/sdk` and `src/go` require nothing (RM-3, M1-D-TOOL-4), `src/harness` pins `src/go` and `src/sdk` at real revisions with their `go.sum` lines |
| test | `<product>/test/e2e`, today `src/go/test/e2e` | outside the RM-1 count and `go.work`; module path `<product path>/test/e2e`, on the floor; its one allowed `replace` points its own product module at a checkout directory |
| tools | `tools/<name>`: `tools/harness-gate`, `tools/harness-trace` | outside `go.work`, run with `GOWORK=off` |

In every class, `CheckReasons` requires a reason comment on every direct `require`
and every `replace`, `exclude`, `tool` and `toolchain` line: on the comment lines
right above it (no blank line between) or at the end of its own line. A bare
`// indirect` is not a reason, and a comment above `require (` does not count for
the entries inside (RM-10, DH-ADR-TOOLCHAIN-11). The `modules` gate runs
`go mod tidy -diff` and `go mod verify` in each module, then `CheckToolchain11` and
`CheckRepoReasons` on the checkout (a single-module root: `CheckReasons` on its
`go.mod`). `TestCordisModuleIsStandardLibraryOnly` is the `src/go` rule.

The cordis suites run in their test module, with the workspace off (the module is
not in `go.work`):

```sh
GOWORK=off go -C src/go/test/e2e vet -tags e2e ./...
GOWORK=off go -C src/go/test/e2e test -tags e2e -count=1 ./...
```

## The tools module

`tools/harness-gate/go.mod` is the repository's tools module (M1-D-TOOL-2 (a);
DC-3 note under DH-D-TOOLCHAIN-1 in `docs/harness/upstream/toolchain.md`). Besides
the gate's own requirements it pins the linters as `tool` lines, each with a reason:

| tool | module and version | run as |
|------|--------------------|--------|
| staticcheck | `honnef.co/go/tools` v0.8.2 (release 2026.2.2) | `GOWORK=off go -C tools/harness-gate tool staticcheck` |
| govulncheck | `golang.org/x/vuln` v1.10.0 | `GOWORK=off go -C tools/harness-gate tool govulncheck` |

Both are the newest releases and were checked to build and run on the go1.26.0
floor. Once the module cache holds them they resolve with `GOPROXY=off`. Moving a
pin means changing the `require` line, keeping its reason comment, and running
`go mod tidy`.

## Subcommands

```sh
GOWORK=off go -C tools/harness-gate run . rehearse  --root ../.. [--out DIR]
GOWORK=off go -C tools/harness-gate run . require   --root ../.. --stage DH-S-18 [--variant linux-e2:landlock]
GOWORK=off go -C tools/harness-gate run . variants  --root ../.. [--story harness:DSH-M2]
GOWORK=off go -C tools/harness-gate run . preflight --root ../..
GOWORK=off go -C tools/harness-gate run . scenarios --root ../.. --stage DH-S-17 --evidence FILE
GOWORK=off go -C tools/harness-gate run . ready     --root ../.. --stage DH-S-16 [--built]
GOWORK=off go -C tools/harness-gate run . close     --root ../.. --stage DH-S-16 [--built]
GOWORK=off go -C tools/harness-gate run . records   --root ../..
GOWORK=off go -C tools/harness-gate run . entry     --root ../.. --story harness:DSH-M2
GOWORK=off go -C tools/harness-gate run . policy-text --root ../..
dsh sandbox status | GOWORK=off go -C tools/harness-gate run . sandbox-preflight [--status FILE] [--service service:linux-landlock-only]
```

- `rehearse` (DH-D-TOOLCHAIN-5, DH-ADR-TOOLCHAIN-13; gap-closure step 19.1.1) runs
  `goreleaser release --snapshot --clean -f .goreleaser.dsh.yaml`, with goreleaser
  pinned in its own tools module `tools/goreleaser` and run as
  `go -C tools/goreleaser tool goreleaser` (never a goreleaser on PATH). The
  snapshot of `cmd/dsh` (`CGO_ENABLED=0`, `-trimpath`, linux, darwin, windows ×
  amd64, arm64) is written to `--out/dist` (a removed temp directory by default)
  through a temporary copy of the config with `dist:` added; a committed config
  that sets `dist:` is refused. Each of the six archives is verified against
  `checksums.txt`, its binary extracted to `--out`, and one line per binary
  printed: `<sha256>  <name>  version=… vcs.revision=… vcs.modified=…`. It
  publishes nothing: `release.disable`, the forge tokens removed from goreleaser's
  environment, and a run that changes the repository's tags fails. The keyless
  replay of the host-matching binary is printed as `OPEN, not built` until the
  replay runner of impl.md step 16.7 exists (step 19.1.2). CI runs it in the
  `harness` job on Linux; `release.yml` and `.goreleaser.yaml` are unchanged.
- `require` prints the comma-separated `DH-E-*` IDs of one stage entry of the
  acceptance index (empty for no `--stage`); an unknown stage is an error. The
  scenario workflow uses it to derive `DSH_REQUIRE_SCENARIOS`. With `--variant`
  (each `s18` job, Stage 8.1) it prints only the IDs that variant of the story's
  `evidence/variants.md` runs, in index order, after `variants`' check passes; an
  unknown or out-of-scope variant fails with exit 1.
- `variants` (Stage 8.1, M2-DOD-4) cross-references
  `docs/stories/<story>/evidence/variants.md` with the `DH-S-18` index entry
  (`variants/`): every scenario has an in-scope variant or an owner removal whose
  decision covers it, every listed ID is in the entry, labels are unique, services
  are architecture.md's, a sandbox service's preflight is its
  `sandboxstatus.Expectations` text, an in-scope row names a hosted or
  `self-hosted, linux|windows, <label>` runner and a provider, and an out-of-scope
  row's decision covers it (macOS rows read `out of scope (platforms-windows-wsl)`).
  It prints the per-scenario coverage (`variants/testdata/coverage.golden`) and
  exits 0; 1 with one `FAILED:` line per problem; 2 on an unreadable file.
  `TestWorkflowRunsEveryInScopeVariant` binds the `s18` matrix of
  `harness-scenarios.yml` to the file.
- `preflight` (DH-ADR-TOOLCHAIN-6, -12) reads `DSH_REQUIRE_SCENARIOS` from the
  environment and fails -- starting nothing -- when a required ID has no scenario
  test, when a required ID's index entry `requires` a stage past `built-through`,
  when the `scenario-index` check fails, or when the client a planned entry's
  `credential` names is not signed in on this host (M1-G-TOOL-16). On success it
  appends `count=`, `run=` (a `go test -run` expression) and `tests=` to
  `$GITHUB_OUTPUT`. It reads no model key (`models-signed-in-claude-copilot`); it
  prints one `sign-in` line per checked credential, with the status command and
  the parsed state only (no account field, no token).
- `scenarios` (M1-D-TOOL-6 (a)) runs one stage's scenarios on the development
  host, where the user is signed in, as one evidence record in the format of
  `--evidence` (12.3.5; a tree that is not clean is refused, exit 2): gate
  `preflight` (the stage's entry as `DSH_REQUIRE_SCENARIOS`, with the sign-in
  checks), then gate `scenarios`, `go test -tags scenario -count=1 -v -run <plan>
  ./...` per module, failing on any `--- SKIP` or a planned test without a
  `--- PASS` line. A failed preflight starts no scenario. `--stage` and
  `--evidence` are required.

The sign-in checks (`acceptance/signin.go`): `claude-session` runs
`claude auth status --json` (Claude Code 2.1.312) with `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, `COPILOT_GITHUB_TOKEN`,
`GH_TOKEN` and `GITHUB_TOKEN` removed from its environment, and passes only on
exit 0 with `loggedIn: true`; `copilot-session` fails closed, because the GitHub
Copilot CLI 1.0.115 has no non-interactive status command; `none` runs no check.

- `ready`, `close` and `records` are the stage-closure gate; see
  [Stage closure](#stage-closure).
- `entry` is the story entry check; see [Story entry](#story-entry-harness-gate-entry).
- `policy-text` runs the `policy-text` gate alone: exit 0 the tree may merge, 1 a
  text is refused, 2 a usage error or an unreadable tree; see
  [Sandbox policy text](#sandbox-policy-text-m2-c-6).
- `sandbox7-record` writes the DH-E-SANDBOX-7 acceptance record the
  `policy-text` gate requires; it runs no gate. See the same section.
- `sandbox-preflight` is the guarded sandbox check of `lanes/wsl.sh
  --guarded-preflight` (see [Guarded preflights](#guarded-preflights)). It reads
  a `dsh sandbox status` text report from `--status FILE`, or standard input,
  and exits 0 only when the report's `backend:` line is `bwrap` and its `state:`
  line is `Full` (`sandboxstatus/`). Any other report fails with exit 1, as does
  one with a missing or repeated line. It never skips. Exit 2 is a usage error.
  With `--service` it decides that S18 sandbox service's preflight instead
  (`sandboxstatus.Preflight`): `service:linux-sandbox-bwrap` backend `bwrap`,
  state `Full`; `service:linux-landlock-only` backend `landlock`, state `Full` or
  `Partial`; `service:linux-unconfined` state `Unconfined` with the missing
  effects named. A wrong machine, or an unknown service, exits 1 naming the
  service and the expected state.

## Story entry (`harness-gate entry`)

M2-DEC-1, as amended by M1 gap-closure step 12.1.9 (harness:DSH-M2
implementation-plan Stage 1.2): no M2 scenario run and no M2 stage closure happens
before M1 is closed. `harness-gate entry --story harness:DSH-M2` checks, under
`--root`:

- every M1 record `docs/stories/harness-DSH-M1/evidence/DH-S-1.md` ..
  `DH-S-11.md` exists and its status line reads `Closure status: closed` (or
  `closed at <sha>`). The status line is a line that starts `Closure status:`
  outside a code fence; history prose such as `**Closure status: closed.**` is
  not the line. A record must have exactly one: a second status line makes the
  record invalid, so it is not closed (`reads "Closure status: a second status
  line at line N ..."`), whatever the first line says;
- the M1 closure record `docs/stories/harness-DSH-M1/evidence/closure.md` exists
  with a `## Deliverables` table (columns Deliverable, State, Reason) that has one
  row for each of the 99 deliverables of impl.md steps 1.1 to 11.7
  (`closure/entry.go`), each `done` or `built` (built, not done), and every `built`
  row gives its Reason (the `DH-E` and stage that finish it, or the item holding
  it open);
- `built-through` in `test/harness/acceptance-index.yaml` is `DH-S-11` or later.

It exits 0 when all hold, 1 with one `  - ` line per unmet condition (records in
stage order, then the closure record, then `built-through`), and 2 on a usage
error, an unknown story, or a record or index that exists but cannot be read or
parsed. A missing record, closure record or index is an unmet condition, named
with every other one; a missing input never hides the rest.

**Where it runs (risk M2-R-04).** `harness-gate entry` is the mandatory first step
of every `guarded` and every `acceptance` run of harness:DSH-M2: a run whose
`entry` exits non-zero does not start. The gate venue (the build steps, the
default `harness-gate` gate list and the lane scripts in their default mode) does
not call it, so S12..S16 may build on the built M1 contracts while M1 is open
(Q `m2-entry-m1-closure`). The wiring lands with the runners themselves, when they
exist: the lane scripts' `guarded` mode (Stage 6.12), the `s17` job (Stage 7.1) and
the `s18` jobs (Stage 8.1, wired: each job's first step after checkout). Each of those stages adds the call; nothing here
refers forward to a runner that is not yet built.

The tests never read the live records for the fixture cases:
`testdata/entry/m1_open/` (the eleven record heads reading `open`, no closure
record, `built-through: DH-S-8`) must produce `testdata/entry/m1_open.golden`, and
`testdata/entry/m1_closed/` (eleven `closed` heads, a closure record,
`built-through: DH-S-11`) must pass. A separate test runs `entry` on the
repository and checks that its result agrees with the live records, whatever
their state. To regenerate the golden, run `go test -run TestEntryGolden . -update`
(refused when `CI` is set).

## Sandbox policy text (M2-C-6)

The prompt builder adds a sandbox policy section only from a shipped
per-platform text, `src/harness/internal/world/sandbox/policy/text/<platform>.md`
for `linux`, `darwin` or `windows` (harness:DSH-M2 implementation-plan Stage 2.4;
the texts are written in Stage 7.6). Until one exists `policy.Text` returns
`policy.ErrNoText` and the prompt has no such section. The `policy-text` gate
(`policytext.go`) holds the pre-merge rule M2-C-6 from before any text exists: a
tree with no file under that directory passes, and every file there must be
proven by a row of `docs/stories/harness-DSH-M2/evidence/DH-S-17.md`:

- the row's `Scenario` is `DH-E-SANDBOX-7` and its `Result` is `passed`;
- its `Commit` is the run's commit (7 to 40 lower-case hex digits) and its
  `Artifacts` name the run, `run=<id>`;
- its `Artifacts` hold `<platform>.md sha256=<hex> escalated=<n>/18` with `n` at
  least 10, and `<hex>` equal to the SHA-256 of the file's bytes;
- the run id is a run-record id, `<lane>-<sha7>-<UTC yyyymmddThhmmZ>`
  (`local-run-record-format`), and names a committed DH-E-SANDBOX-7 acceptance
  record `docs/stories/harness-DSH-M2/evidence/runs/<id>.txt` written by
  `harness-gate sandbox7-record` (below): intact (its final `sha256:` line is
  the SHA-256 of every byte before it), `scenario: DH-E-SANDBOX-7`, the clean-tree
  field both record producers write (`tree: clean (git status --porcelain
  --untracked-files=all is empty)`, `CleanTreeField`), `result: passed`, a
  `text:` line for the file with the file's SHA-256 and the row's count, and a
  full `commit:` that starts with the id's `<sha7>` and with the row's `Commit`.
  A quality-gate record (`harness-gate --evidence`) is refused: the quality gate
  runs `policy-text` itself and no scenario test, so it can prove no text;
- `git cat-file blob <that commit>:<path>` holds the file's bytes: the run was on
  a commit containing that same file.

The rule fails closed. `--root` must be a git checkout's top level that holds
the run's commit (a shallow CI clone must fetch it); when it is not, or git
cannot read the commit, every text is refused (`commit containment
unavailable`). A tree with no text needs no git. The fixtures are turned into
temporary git checkouts by the test (`policyTextRepo`): their first commit holds
only the text, with fixed author, committer, dates and message, so it is the
commit `41101e7b...` the committed fixture run record names. That record is
the producer's own output (`formatSandbox7Record`;
`TestPolicyTextFixtureRecordIsProduced`), not a hand-made format.

The acceptance record is written by the s17 job (Stage 7.6) after its
escalation runs, on the clean checkout of the branch commit it tested, before
the PR gate runs. It runs no gate, so a first or edited text is recorded without
first passing `policy-text`:

```
harness-gate sandbox7-record --root . --escalated linux=12,darwin=11,windows=13 [--lane s17] [--ci-run <id>]
```

It refuses (exit 2, nothing written) a dirty tree, a tree with no text, and a
missing or extra count; it writes `runs/<lane>-<sha7>-<UTC>.txt` holding the
commit, the CI run id, each text's path, SHA-256 and `escalated=<n>/18`, and
`result: passed` (exit 0) or `failed` (exit 1, any text under 10). The flow is
then: commit the text, run the scenario and `sandbox7-record` on that commit,
add the record and the `DH-E-SANDBOX-7` row naming `run=<id>` in a later commit,
and the PR gate admits the text. An edit is a new commit, refused until it is
recorded and its row updated (`TestSandbox7RecordFlows`).

Any other file there (not `<platform>.md`) is refused. A row may prove several
platforms, one `<platform>.md sha256=... escalated=.../18` entry each. Every edit
to a text changes its SHA-256, so the edited text is refused until DH-E-SANDBOX-7
passes again on it and the row records the new hash. Each refusal is one line
`policy-text: REFUSED M2-C-6: <path> (sha256 <hex>): <reason>`. The fixture trees
`testdata/policytext/{no_text,text_without_sandbox7,text_with_matching_pass,text_edited_after_pass}`
and `testdata/policytext/expected.golden` pin the behaviour
(`TestPolicyTextGolden`); `TestPolicyTextRunProvenance` refuses a tree that is
not a checkout, a missing, tampered, dirty-tree, other-scenario, other-bytes,
other-count, quality-gate or failed run record, a row whose commit is not the
run's, and a run commit without the text.

## Stage closure

The two-step closure protocol of C57 and M2-C-2 (harness:DSH-M2
implementation-plan Stage 1.1). The records live at
`docs/stories/<story>/evidence/DH-S-n.md`, where `<story>` is `harness:DSH-M2`
written as `harness-DSH-M2` (`--story` selects the story; `harness:DSH-M2` is the
default). Each record has a status line, `Closure status: open` or
`Closure status: closed`, and the impl.md sec. 7.3 fields as `### ...` headings.

1. While the record reads `open`, the PR fills its tables until
   `harness-gate ready --stage DH-S-n` exits 0. `ready` does not read the status
   line.
2. The closing PR then changes the line to `closed`. Its gate runs
   `harness-gate close --stage DH-S-n`, which checks every `ready` condition again
   and also requires the line to read `closed`. The refusals are:
   - `not-yet-closed`: the line reads `open`;
   - `closed-but-not-ready`: the line reads `closed` but a `ready` condition fails;
   - `status-invalid`: the line is missing or unreadable.

Both commands exit 0 on a pass and 1 with one `  - ` line per blocker. They exit 2
on a usage error or an unreadable acceptance index. A missing record is a blocker.
Without `--built`, the check is an acceptance close:
- the stage needs an index entry;
- every index-listed ID needs a row;
- every row of the `## Scenarios` table counts.

The mode is fixed by the story, not by the caller: DH-S-12..16 close with `--built`
and DH-S-17..18 without it. Either command given the other mode exits 2. Called as a
library, `Story.Ready` names the mismatch as a blocker and judges the stage in its
own mode, so a build stage never skips its build tables.

`--built` is a build-stage close (DH-S-12..16), checked in addition:
- only rows with venue `guarded` count, and every index-listed ID still needs a row;
- every `## Steps` row needs a merge commit (a SHA of 7 to 40 lower-case hex
  digits);
- every `## Manual checks register` row needs Seen, Operator, a commit and the
  label `manual`;
- every `## Owner defaults (M2-C-4)` row needs Confirmed and By;
- a row for an ID the index lists under the stage must have venue `guarded`. A
  different venue would leave its result unjudged while it still covers the index
  entry, so it is a blocker.

Each record also owes the rows Stage 1.1 created in it (`closure/obligations.go`):
- every step;
- each manual check, matched on its step and its check text, so a second copy of
  one check cannot stand in for another (a duplicate row is itself a blocker);
- the stage's owner defaults;
- every scenario row, matched on scenario, variant, venue and leg together.

A deleted row, or a scenario row whose venue or leg was edited, is a blocker.
`ready` and `close` check the scenario rows always and the other owed rows with
`--built`. `records` checks all of them on every record, including open ones.

The Scenarios table's columns are Scenario, Variant, Venue, Leg, Environment,
Commit, Result and Artifacts. A result reads one of:
- `passed`;
- `failed`;
- `not run (<reason>)`;
- `skipped`;
- `blocked`;
- `out of scope (<decision>)`.

Only `passed` counts as passed. A `skipped` row is not passed (M2-DEC-3). An `out
of scope` row is not a blocker only when it names its decision. The Leg column
reads `required` (or is empty) or `recorded`. A `recorded` leg must still run; a
`failed` result there is kept and does not block (M2-DEC-4).

The Venue column reads `guarded`, `acceptance` or `later`; any other value is a
blocker. A row that ran (`passed` or `failed`, including a recorded leg's failure)
must record its Environment, a commit SHA and its Artifacts (M2-DEC-5). A named
`out of scope` row need not. A table whose header is not followed by a `| --- |`
separator row with one cell per column is refused as unreadable, so a data row is
never taken for the separator.

`records` checks the shape of every stage record of the story (status line, the
sec. 7.3 headings, the four tables and their columns). A record that reads
`closed` must also pass `close`, with `--built` for a build stage. It then checks
the story's `closure.md`:
- a `## Deliverables` table with Deliverable, Priority, State, Finishing stage or
  held-open item, and Proving scenarios;
- every deliverable of tech-spec sec. 3.2 by ID with its priority (no unknown ID,
  no missing one), the count and the P0/P1 split;
- a state of `not built`, `built` or `done`, where only the done-candidates may
  read `done` and every row that is not done names a finishing stage or `H-` item;
- a `## Held-open items` table with a row per held-open item.

The story shapes (stages, build stages, deliverables, held-open items) are in
`closure/shape.go`. The golden `testdata/closure/expected.golden` covers `ready`
and `close` over the four record states. To regenerate it, run
`go test -run TestStageClosureGolden . -update`; this is refused when `CI` is set.
## Acceptance index (`test/harness/acceptance-index.yaml`)

Keyed by executing stage; each entry lists the authored `DH-E-*` IDs with their
authoring stage. `built-through` is the last closed story: IDs authored at or
before it must have a file. An optional `requires` list names the stages whose
behaviour a scenario authored ahead of them exercises; `preflight` refuses to run
it until `built-through` reaches every one, and none may come after the executing
stage. Every entry states its `credential` (`none`, `claude-session` or
`copilot-session`), the signed-in session it needs. Scenario files bind an ID to a test with a
`//dsh:scenario <ID>` line in the doc comment of a top-level `TestXxx`. The parser
(`acceptance/`) accepts only this shape, so no YAML dependency is needed:

```yaml
built-through: DH-S-1
stages:
  DH-S-16: []
  DH-S-17:
    - id: DH-E-LLM-3
      authored: DH-S-9
      credential: claude-session
      requires: [DH-S-11, DH-S-16]
  DH-S-18:
    - id: DH-E-TOOLCHAIN-2
      authored: DH-S-1
      credential: none
```

## Test traceability (`//dsh:test`, `trace-tests`; M1-G-CLOSE-9)

A test names the `DH-T` IDs it proves with a directive on the line directly
above its `func Test*`, `Fuzz*` or `Benchmark*` (the last line of its doc comment):

```go
// TestHomeIsPrivate: created directories are 0700 and files 0600.
//
//dsh:test DH-T-STORAGE-4, DH-T-STORAGE-14
func TestHomeIsPrivate(t *testing.T) { ... }
```

IDs are written in full and separated by commas. A **deferral** exists only where
impl.md defers the test, and is written in the package's `doc.go`:

```go
//dsh:test DH-T-TOOLCHAIN-1 later DH-S-16
package scenarios
```

`trace-tests` accepts it only when impl.md's DH-S-16 section names that ID and
DH-S-16 comes after the stage that requires it. This is not an exemption list
(QG-2): the plan's own text is the only thing that can defer a test. A directive
anywhere else (on a helper, a method, inside a body, a deferral on a test, a
doc.go line without `later`) is an error.

```sh
GOWORK=off go -C tools/harness-gate run . trace-tests --root ../.. [--stage DH-S-n]
```

For every step of DH-S-1 through `--stage` (default: the index's `built-through`)
it reads the `DH-T` IDs the step's own text names and those of the "Done when"
lines of the deliverables it links to, expanding the plan's shorthand (`` `-n` ``,
`to`, `through`, `..`). Each ID is listed at one step: the first whose own text
names it, else the first that links a deliverable naming it. It then scans every
`*_test.go` and `doc.go` below `src/` and `tools/` (all build tags; `testdata`,
`vendor` and hidden directories skipped) and prints, per step, the required,
tagged, deferred and missing IDs, the totals per stage and overall, and how the
tags were found. It exits 1 on a missing ID, a deferral impl.md does not back, a
misplaced directive or a broken deliverable link, and 2 when its inputs cannot be
read.

Tag forms accepted while the existing tests migrate (`acceptance/tests.go`):

| form | example | status |
|------|---------|--------|
| directive | `//dsh:test DH-T-X-1, DH-T-X-2` above the func | the convention |
| doc comment | `// TestX ... (DH-T-X-1, -2).` in the func's doc comment | legacy, migrates to the directive |
| body comment | `func TestX(t *testing.T) { // DH-T-X-1`, or a comment inside the body | legacy, migrates to the directive |

The `trace` gate runs the same check with the default stage (step 18.1.4): a
missing ID fails the gate with the lines the subcommand prints after `FAILED:`,
and the full report is printed above them. On a pass the gate prints one totals
line. The subcommand stays for the full report on a pass and for `--stage`.

The `scenario-index` gate also runs two plan checks of `acceptance/impl.go`
(13.6.4, 13.6.6). They are called through `acceptance.CheckBoundary` →
`CheckPlan`, which reads `docs/harness/impl.md` and
`docs/harness/trace/ledger.jsonl`; a root without either file fails.
- `CheckBuiltThrough`: the marker equals the largest n for which every step of
  DH-S-1..n is ticked in impl.md. Otherwise it names the first offending step.
- `CheckRule4Authoring`: a scenario whose ledger `proves` are all built by stage k,
  and which impl.md sec. 7.5 places in k's paired acceptance stage, is in the index
  once k is built.

## Own analyzers (`analyzers/`)

| analyzer | rule |
|----------|------|
| `nosleep` | no reference to `time.Sleep` in a `_test.go` file |
| `onespawn` | no `os/exec` import outside `internal/world/subprocess` (SP-12), and no `subprocess.Spec` literal without `Launch` (DH-D-EXEC-4); `_test.go` files are exempt |
| `cleanup` | a test that starts a goroutine, a listener (`net.Listen*`, `httptest.New*Server`) or a child process (`os.StartProcess`, `(*exec.Cmd).Start`) registers `t.Cleanup` (QG-5) |
| `fuzzcheck` | a package with a `//dsh:untrusted-parser` line in a non-test file has a `func FuzzXxx(f *testing.F)` in its own test files, a `seeds_test.go` (no `os` / `io/ioutil` import), every target seeds the `f` it receives through `f.Add` in `seeds_test.go` (directly or via the seeded parameter of a helper there, never through a local that shadows it), and every `f.Add` argument is compiled in -- literals of unnamed types, embed bytes, `seeds_test.go` data and pure stdlib calls (`bytes`, `strings`, `strconv`, ...; not `fmt`), never a call such as `readSeed()`, a write through a local or package-level alias (`alias := b; copy(alias, readSeed())`, `var alias = seed`, also through multi-hop chains in other test files; read-only chains, number/string copies and same-named variables of other scopes are fine) or a shadowed `true`/`nil` (QG-6, DH-ADR-TOOLCHAIN-3; rules in `analyzers/fuzzseeds.go` and `src/harness/internal/testkit/README.md`) |
| `rootcall` | no use of `(*cordis.Context).Root` -- call, method value or method expression -- outside `src/go/cordis` (DH-ADR-TOOLCHAIN-8) |
| `typeassert` | no unchecked `x.(T)` (anything but `v, ok := x.(T)` and a type switch) in a non-test file, and no `unsafe` import in any file (DH-ADR-TOOLCHAIN-8). Test files may assert unchecked: a failed assertion panics inside the test and fails it, which is what a checked one would do (owner decision, 2026-11-22) |
| `scenarioimport` | a `_test.go` file that imports the scenario helper `internal/testkit/scenario` imports no other `src/harness/internal/...` package but the test kit's `internal/testkit/launcher`: a scenario drives the built binary, the standard library and `src/sdk/...` (architecture.md sec. 3.4 rule 5; DH-D-UTIL-8). The scenarios written before the helper move to it one by one, each clean under the check as it does |
| `lintignore` | every `//lint:ignore` names a known check and gives a reason; no `//lint:file-ignore` (DH-ADR-TOOLCHAIN-8) |

Built and tested, not yet run by the vet gate (`analyzers.Pending`): each joins
`analyzers.All` in the change that leaves the tree clean under it (DH-ADR-TOOLCHAIN-8).

| analyzer | rule |
|----------|------|
| `errdiscard` | an `error` result assigned to `_` (`_ = f()`, `v, _ := f()`, `var _ = f()`) or dropped by a call, `defer` or `go` statement, in non-test code |

`errdiscard` is narrowed to where a dropped error can hide a defect (owner decision,
2026-11-22): `_test.go` files are not checked (a test fails on the behaviour it
asserts, and a teardown error has no caller); `fmt.Print*` and `fmt.Fprint*` are
exempt (they return only the writer's error, which the writer's owner sees on its
next write, `Flush` or `Close`); the `Write*` methods of `*bytes.Buffer` and
`*strings.Builder` are exempt (their error is always nil); and a deferred `Close` of
a read-only handle is exempt -- a file opened with `os.Open`, `(*os.Root).Open` or
`os.OpenFile(..., os.O_RDONLY, ...)`, or any value that can `Read` but not `Write`,
such as an `http.Response` body -- because closing what was only read loses no
data. A `Close` of anything written is still reported: there the error is where a
failed write surfaces. Narrowed, it reports 268 sites in the product modules, so it
stays pending until they are fixed or reason-annotated (plan stage 13.5).

The same binary is the vet tool: started with `HARNESS_GATE_VETTOOL=1` it runs
`unitchecker.Main` over the analyzers. Their fixtures live in
`analyzers/testdata/src` and are checked with `analysistest`.

### Site exceptions (`lintignore`)

A finding of an own analyzer or of staticcheck is accepted only by a directive at
its site that names the check and says why (DH-ADR-TOOLCHAIN-8, M1-G-TOOL-7):

```go
import (
	//lint:ignore typeassert reading an ACE's SID (GetAce) needs unsafe.Pointer at SidStart
	"unsafe"
)
```

It is staticcheck's own form, matched the same way: the directive belongs to the
statement it stands above (or trails) and suppresses the named checks on the line
where that statement starts. A directive without a reason, without a check or with
an unknown check is a `lintignore` finding and suppresses nothing;
`//lint:file-ignore` is always a finding. `analyzers.Exceptions` reads every Go
file of the modules, whatever its build tags, and the vet gate prints the total, for
example `exceptions: 3 (U1000 1, errdiscard 1, typeassert 1)`. A reasoned directive
at a site is not an exemption list: nothing outside the code records a violation.

`fuzzcheck` decides over a whole package: `go vet` hands it a package together
with its in-package `_test.go` files. A loader that also yields the package without
its tests (`go/packages` with `Tests`, as `analysistest` uses) is skipped for that
variant when test files of the package exist on disk, and the test variant is
judged instead. `TestFuzzCheckRunsInVetGate` proves the rule through the real
`go vet -vettool` path.

## Test layers and build tags (DH-ADR-TOOLCHAIN-4)

The `replay` and `scenario` build tags (`LayerTags`) mark the layers the keyless
gate never runs: `*_replay_test.go` and `*_scenario_test.go` files carry
`//go:build replay` / `//go:build scenario`. The `test` and `coverage` gates (and
the `go list` the coverage gate plans with) pass `-tags=`, an explicit empty tag
list that overrides any `-tags` in `GOFLAGS`, so neither layer can run in, fail or
pass the default gate. `testdata/layer-tagged` holds an always-failing test of each
layer; the gate passes on it, and `go test -tags scenario` (or `replay`) fails.

The `bench` tag (`BenchTag`) marks the benchmark files (`*_bench_test.go`,
`//go:build bench`) run by hand with `go test -tags bench -bench`. No gate runs
them, but the `vet` and `staticcheck` gates compile and check them in their
layer run (`-tags=replay,scenario,bench`), so a bench file that does not build,
breaks an analyzer rule or has a staticcheck finding fails the gate.

## Race detector (QG-4)

`-race` needs cgo and so a C compiler (`go env CC` on `PATH`; on Windows e.g.
MSYS2 UCRT64 `gcc`, EN-6). The test gate checks for it first; a lane where `-race`
cannot run is a **failure** of the `test` gate, never a skip.

## Virtual time in tests (`testing/synctest`)

Tests never wait on the wall clock — the `nosleep` analyzer rejects `time.Sleep`
in test files. Code that waits on timers, tickers or timeouts is tested inside a
`testing/synctest` bubble, where time is virtual and advances only when every
goroutine of the bubble is durably blocked:

```go
func TestRetryBacksOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- retry(ctx, time.Second) }()
		synctest.Wait() // every goroutine of the bubble is blocked on virtual time
		if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
}
```

The convention:

- Wrap the test body in `synctest.Test(t, func(t *testing.T) { … })`.
- Advance time by blocking: let the code under test wait on its own timer, then
  call `synctest.Wait()` to block until every goroutine of the bubble is idle, and
  assert. A whole hour of timeouts runs in microseconds and deterministically.
- Never `time.Sleep` to "give a goroutine a chance"; wait on an explicit signal
  (channel, `sync.WaitGroup`, `synctest.Wait`).
- Everything a bubble starts must finish inside it; register `t.Cleanup` for what
  outlives a step (the `cleanup` analyzer).

## Waiting and releasing in tests (M1-D-GATE-24)

The owner chose option A on 2026-11-22 (`docs/stories/harness-DSH-M1/decisions.md`,
M1-D-GATE-24). **Why it matters:** a test that sleeps passes or fails with the
host's speed, and a test that leaves a goroutine blocked holds memory, files or a
port until the test binary exits and can break a later test in a shuffled run. The
rules below make every wait deterministic and every start released, so the `vet`
gate can enforce them without exceptions.

**The rules.**

1. `nosleep` is strict: no `time.Sleep` in a `_test.go` file, inside a
   `synctest` bubble or not. The analyzer has no allow-list and is never silenced;
   where it is wrong, the analyzer is fixed with an `analysistest` fixture (QG-2).
2. No settle wait outside a bubble: `select { case <-x: ...; case <-time.After(d): }`
   used to show that something did *not* happen is a sleep under another name. Use
   P-V, so "nothing more can happen" is `synctest.Wait()`, not a guess at `d`. A
   `time.After` that only bounds a wait which should succeed (`Within`, a
   `receive` helper's timeout) is fine.
3. A cleanup must **unblock, then join**. Waiting for a goroutine that is still
   blocked on the test's gate hangs the cleanup; releasing a gate without waiting
   leaves the goroutine running past the test. Both halves are required, and the
   join is bounded so a stuck goroutine fails the test instead of hanging it.
4. One named poll helper exists, for facts with no in-process signal: a real child
   process or another process's log. Everything else waits on a signal or on
   virtual time.

**The fix patterns.**

| pattern | when | what it looks like |
|---------|------|--------------------|
| P-C (cleanup) | a test starts a goroutine, listener or child process | register the release right after the start: `t.Cleanup(func() { unblock(); <-done })`, where `unblock` closes a gate, cancels a context or disposes a root (safe to call twice: `sync.OnceFunc`), and `done` is the goroutine's own exit signal (`defer close(done)`) |
| P-V (virtual time) | the code under test waits on a timer, or the test must show that something did not happen | wrap the test in `synctest.Test`; `synctest.Wait()` replaces a settle sleep, and `<-time.After(d)` inside the bubble is virtual |
| P-S (signal) | a test polls in-process state | replace the poll with an explicit signal: a channel the code sends on, a test hook, or a process's `done` channel |
| P-F (fuzz seeds) | a `Fuzz*` target of an untrusted parser | move the `f.Add` seeds into `seeds_test.go` as a Go slice with an `add<Target>Seeds(f)` helper (the `fuzzcheck` rules above) |

```go
// P-C with P-V: the cleanup releases the gate, then joins the goroutine.
synctest.Test(t, func(t *testing.T) {
	gate, done := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(func() { unblock(); <-done })
	go func() { defer close(done); fiber.Dispose() }() // blocks on a cleanup waiting for gate
	synctest.Wait()                                     // every goroutine is blocked: Dispose has not returned
	// ... assert that Dispose is still waiting ...
	unblock()
})
```

`src/go/cordis/join_test.go` (`goJoined`, `closeOnce`) packages P-C for the cordis
tests: it starts the goroutine, registers "unblock, then join within `Timeout`"
and returns the goroutine's `done` channel.

**The poll helper** is `src/harness/internal/testkit/procwait`:

- `procwait.Until(ctx, t, fact func() bool)` checks `fact` at once and then every
  `procwait.Interval` (10 ms) until it holds, and fails `t` when `ctx` ends first;
- `procwait.Poll(ctx, fact) error` is the same wait returning the error (wrapping
  the context's cause) instead of failing the test;
- a context without a deadline is bounded by `procwait.DefaultTimeout` (30 s).

It is for facts only another process can make true -- a child has written its pid
file, another process's log holds a line. It is not for in-process state, which
has a signal (P-S). Its own tests run on virtual time and never sleep
(`procwait_test.go`, DH-T-TOOLCHAIN-8).

**The `cleanup` analyzer and fields.** A listener assigned to a field
(`cs.Server = httptest.NewServer(...)`) is recorded under the root variable `cs`,
so `t.Cleanup(cs.Close)` releases it (M1-G-GATE-5; fixture
`analyzers/testdata/src/cleanup/field_test.go`, `TestCleanupFieldAssignedListener`,
DH-T-TOOLCHAIN-8).

## Coverage across processes (QG-3)

The coverage gate owns one `GOCOVERDIR` per module and, for every package with
tests, runs

```sh
go test -c -tags= -cover -covermode=set -coverpkg=./... -o <tmp>/bin/pkgN.test <pkg>
GOCOVERDIR=<tmp>/covdata <tmp>/bin/pkgN.test -test.count=1 -test.timeout=10m -test.skip='^TestBudget' -test.gocoverdir=<tmp>/covdata   # in the package directory
```

then merges everything in that directory with

```sh
go tool covdata textfmt -i=<tmp>/covdata -o=<tmp>/covdata.out
```

`covdata` joins the profiles of *different* binaries package by package — the test
binary, every re-executed copy of it and every separately built `-cover` helper.
(`go test -cover`'s own report keeps only the test binary's profiles, so a helper
binary's statements would be lost.) A package without test files gets its 0%
entries from `go test -cover -coverprofile`, so none escapes the floor. Every
package under 85% of statements fails the gate, with one temporary exception:
`CoverageDebt` (`coverage.go`), owner-approved on 2026-11-24 for the M2 packages
that were already under 85% on `main` when the lanes first reached this gate. Each
is pinned at its measured coverage, so it cannot slip further; the gate prints every
entry (`coverage: DEBT ...`) and fails an entry whose package reaches 85%, so the
list only shrinks.

A test binary run directly has no default timeout (only `go test` applies one), so
the gate passes `-test.timeout` (`Config.TestTimeout`, default 10m, `go test`'s own
default) and backs it with a process deadline one minute later. A blocked test
fails the `coverage` gate naming the test that was running
(`test timed out: TestBlocked`, `testdata/coverage-blocked`) instead of hanging the
runner.

Children every test process starts inherit `GOCOVERDIR`. For their counters to be
collected:

- a test that re-executes its own binary runs the child through `m.Run` and passes
  `-test.gocoverdir=` + `os.Getenv("GOCOVERDIR")` (a test binary writes no counters
  on a bare `os.Exit`);
- a separately built helper binary is built with `go build -cover -covermode=set`
  (the mode must match for `covdata` to merge) and writes its counters on exit.

`testdata/coverage-child` exercises both: its package reaches 100% only with both
children merged, and 70% without either (`TestCoverageMergesChildProcesses`).

## Performance budgets (QG-7)

Budget tests are named `TestBudget*` and their budgets are constants compiled into
the test. The `test` gate runs them under `-race` with every other test, so a race
in a budget test fails there (`testdata/budget-race`); a budget test must therefore
also pass under the race detector (skip only the *measurement* assertion when
built with `-race`, e.g. through a `//go:build race` constant, never the code
path). The `coverage` gate leaves them out. The `budget` gate runs them alone,
uninstrumented, with every `DSH_*` and `GO*` variable removed from the child
environment (only the locations `GOROOT`, `GOPATH`, `GOMODCACHE`, `GOCACHE`,
`GOTMPDIR` are kept, and `GOWORK=off`, `GOTOOLCHAIN=local` are set), so no
override can relax a budget.

## Seeded-violation modules (`testdata/`)

| module | fails at |
|--------|----------|
| `race-seeded` | `test` (a data race) |
| `budget-race` | `test` (a data race inside `TestBudgetRacy`) |
| `unformatted-file` | `gofmt` (`add.go` is deliberately unformatted) |
| `untidy-module` | `modules` (a reasoned but unused `require golang.org/x/mod`; `go mod tidy -diff` prints its removal) |
| `coverage-40pct` | `coverage` (`example.com/cov40/p` at 40%) |
| `coverage-blocked` | `coverage` (`TestBlocked` never returns; the binary's `-test.timeout` fires) |
| `coverage-child` | passes `coverage` only with both child processes' profiles merged |
| `budget-exceeded` | `budget` (`TestBudgetX` honours `DSH_BUDGET_RELAX` unless cleared) |
| `cross-broken` | `cross-compile` (`windows/arm64` only) |
| `layer-tagged` | passes `test` and `coverage`: its always-failing `replay` and `scenario` tests are excluded |
| `fake-go` | not a gated module: a `go` command that answers `go version go1.99.0`, put first on `PATH` to prove the setup failure of an off-floor toolchain |
| `staticcheck-seeded` | `staticcheck` (`if x == x`, SA4000) |
| `vuln-stdlib` | the `vulncheck` gate against the fake database `vulndb/` (a file database that lists `strings.ToUpper` as vulnerable before go1.99.0, advisory `GO-2099-0001`); `linters_test.go` runs the gate on it directly, offline |

`gates_test.go` copies each into `t.TempDir()`, runs a freshly built
`harness-gate` against it and checks the named gate, the non-zero exit and that no
later gate started.

## Lanes

A commit is proved on two local lanes, Windows native and Linux under WSL, by one
script per lane (M1-G-GATE-20, M1-G-TOOL-12). The hosted CI lanes are regression
signals; the evidence of record is a lane run's evidence file.

```sh
~/src/agent-plugin/tools/harness-gate/lanes/wsl.sh      # in the WSL distribution
pwsh -File tools/harness-gate/lanes/win.ps1             # on the Windows host
```

### The go floor (M1-G-TOOL-14)

The gate reads the floor from the `go` line of `src/harness/go.mod` (a
single-module root: its own `go.mod`), runs `go version` with
`GOTOOLCHAIN=go<floor>` (Go fetches that toolchain when the default `go` on
`PATH` is another release, e.g. go1.26.0), and then runs every child with the
floor's own `go` binary, its `GOROOT`, its `bin` first on `PATH` and
`GOTOOLCHAIN=go<floor>`; the budget gate's `GOTOOLCHAIN=local` therefore also
runs the floor. It prints the toolchain before the first gate:

```text
harness-gate: toolchain go1.27.4 (the go floor of src/harness/go.mod; children run with GOTOOLCHAIN=go1.27.4, GOROOT=...)
```

When `go version` is not the floor (a `go` older than 1.21 that ignores
`GOTOOLCHAIN`, a toolchain that cannot be fetched, a wrapper), setup fails with
exit 2, naming both versions. There is no flag to accept another toolchain (QG-2).

### Evidence files (`--evidence FILE`)

```sh
GOWORK=off go -C tools/harness-gate run . --root ../.. --evidence /abs/path/run.txt
```

`FILE` is resolved against the gate's working directory (`tools/harness-gate`
under `go -C`), so the lane scripts pass an absolute path. Before anything else
the gate runs `git status --porcelain --untracked-files=all`; any change is
refused with exit 2, naming the dirty paths, and no file is written and no gate
started. Otherwise it writes, and tees to stdout:

1. the header: `commit` (`git rev-parse HEAD`), `tree: clean`, the `command`,
   `goos/goarch`, `go version`, the `go floor` and `goroot`, `cc` (`go env CC`)
   and `cc --version`, `host os` (`ver` on Windows, `uname -srvm` elsewhere), on
   Linux `wsl distro` (`WSL_DISTRO_NAME`), `os-release` (`PRETTY_NAME`) and
   `kernel` (`uname -r`), `children: CI=1`, and `started` (UTC);
2. `--- output ---`, the full gate output, `--- end of output ---`;
3. `ended` (UTC), `exit` (the gate's status) and `sha256`, the SHA-256 of every
   byte of the file before that last line (`head -n -1 FILE | sha256sum`).

Every child of an evidence run has `CI=1`, so a golden refuses `-update` on a
local lane as it does in CI (`testkit/golden/golden.go`).

### The lane scripts (`lanes/`)

Each script checks its lane and stops at the first check not met, naming it
(exit 3), before any gate runs:

| check | `wsl.sh` | `win.ps1` |
|-------|----------|-----------|
| checkout file system | ext4 (`stat -f -c %T .` prints `ext2/ext3`); `9p`/`v9fs` is refused as "checkout on 9p", `drvfs` or any `/mnt/<drive>` path as drvfs | a local fixed NTFS or ReFS volume |
| user | `id -u` is not 0 | not elevated (Administrator) |
| go floor | `GOTOOLCHAIN=local go version` is the floor, and `go` is not a `/mnt/` Windows binary | `GOTOOLCHAIN=go<floor>` set (go1.27.4), then `go version` is the floor |
| C compiler | `go env CC` resolves | `go env CC` resolves (MSYS2 UCRT64 `gcc`) |
| tree | clean, `core.autocrlf` unset or `false` | clean, `core.autocrlf` unset or `false` |
| `TMPDIR` | on ext4 | (WSL only) |
| `pwsh` | (Windows only) | `pwsh` resolves on `PATH` to an absolute path and runs (architecture.md "Shells": the pwsh executor rows run here and fail, never skip, without it) |

Then each runs `GOWORK=off go -C tools/harness-gate run . --root ../.. --evidence
<file>`, with the record at
`docs/stories/harness-DSH-M1/evidence/runs/<lane>-<sha7>-<UTC yyyymmddThhmmZ>.txt`
(`<lane>` is `wsl` or `win`). `wsl.sh` also runs `rehearse`, first, while the tree
is still clean (an untracked record would make every binary `vcs.modified=true`),
and writes its output next to the record as `...-rehearse.txt`, ending with the
same kind of `sha256` line. The script exits with the gate's status, else the
rehearsal's.

### Guarded preflights

A guarded run (implementation-plan Stages 6.10 and 6.12) needs more of each
machine than the gate does. Every check fails the run with exit 3. None is
skipped.

```sh
~/src/agent-plugin/tools/harness-gate/lanes/wsl.sh --guarded-preflight
```

`wsl.sh --guarded-preflight` runs the lane checks above, then the
`service:linux-sandbox-bwrap` checks (architecture.md I-8), and exits 0 without
running a gate:

1. `bwrap` resolves on `PATH` to an absolute, executable path. This is the
   same lookup the bubblewrap backend makes. When `bwrap` is missing and `sudo`
   needs no password, the script installs it with `sudo apt-get install -y
   bubblewrap`. Otherwise the check fails and names the package.
2. The script builds `cmd/dsh` from the checkout into a scratch directory under
   `TMPDIR`. It creates an empty `<scratch>/home` (the stage claims
   `harness-home` only when it sees an existing home read-only; an absent home
   reports `Partial`), runs `dsh --home <scratch>/home sandbox status`, a
   read-only view that creates nothing in the home, and prints the report. It then pipes the
   report to `harness-gate sandbox-preflight`. Only backend `bwrap` with state
   `Full` passes. `landlock Partial`, `Unconfined` or anything else fails, so a
   machine whose user namespaces are restricted can never pass silently.

On success it prints, for example:

```text
wsl lane: guarded preflight passed: bwrap /usr/bin/bwrap; backend bwrap, state Full
```

`win.ps1` has no sandbox check: no Windows backend exists in M2. Its `pwsh`
check (the table above) runs on every Windows lane run.

### Guarded mode (`--guarded`, `-Guarded`; Stage 6.12)

```sh
~/src/agent-plugin/tools/harness-gate/lanes/wsl.sh --guarded [--inputs FILE]
pwsh -File tools/harness-gate/lanes/win.ps1 -Guarded [-Inputs FILE]
```

impl.md step 16.8. After the lane checks (and, on WSL, the guarded preflight
above) the script runs `harness-gate guarded --lane wsl|windows --stage DH-S-16
--evidence <record>`, which is one evidence record at
`docs/stories/harness-DSH-M2/evidence/runs/guarded-<wsl|win>-<sha7>-<stamp>.txt`.
Its gates, in order, stop at the first failure:

1. `entry`: [Story entry](#story-entry-harness-gate-entry); while harness:DSH-M1
   is not closed nothing runs (M2-DEC-1).
2. `require`: the stage's IDs (`harness-gate require --stage DH-S-16`), checked
   against the index; an ID may require the stage itself (DH-E-FS-3 requires
   DH-S-16) but no later one. The Windows lane drops DH-E-STORE-2 and records
   it `out of scope (impl.md 16.8 Linux only; TC-19s2)`.
3. `inputs`: every `DSH_SCENARIO_*` input the planned tests read, found by
   reading their source, is set (the inputs file, outside the tree, supplies
   them: `set -a`-sourced on WSL, `KEY=VALUE` lines on Windows); the editor
   save script (`DSH_SCENARIO_FS3_EDITOR`) resolves, else `service:git-and-editor`
   is named; DH-E-STORE-2 probes the Claude Code sign-in.
4. `build`: `dsh` built once with `-tags testhook` into the scratch directory
   (`DSH_BIN`), its SHA-256 printed: the keyless replay provider (`llm.replay`)
   DH-E-STORE-1, -STORE-6 and -FS-3 load is registered only in that build.
5. `scenarios`: the scratch tree listed before; `go test -tags scenario -count=1
   -p 1 -run <plan> ./internal/testkit/scenarios` with `TMPDIR` in the scratch
   directory, `DSH_REQUIRE_SCENARIOS` and `DSH_SCENARIO_REPORT`; the scratch tree
   listed after; the scenarios' `evidence:` log lines (home files before and
   after, deleted sessions' rows, blobs and spill files gone, the log's `seq`
   range), observed before each removes its scratch home, under
   `cleanup evidence:`; one `row: | ... |` line per ID, the Scenarios-table row of
   `evidence/DH-S-16.md`, taken from the report (an ID with no report row is
   `not run`).

Exit 0 only when every planned scenario passed.

### Provisioning the WSL lane

- `wsl --install -d Ubuntu-24.04` under WSL2, with a **non-root** default user:
  root bypasses the permission tests (`storage/home/private_unix_test.go`,
  `loosen_unix_test.go`).
- Go **1.27.4 exactly** (decision `go-floor-1-27-4`), from the go.dev
  `linux/amd64` tarball into `/usr/local/go`, first on `PATH` (`~/.profile`;
  `wsl.sh` prepends `/usr/local/go/bin` itself for a non-login shell). In
  `/etc/wsl.conf`, set `[interop] appendWindowsPath=false`, so the Windows
  `go.exe` can never be picked up.
- `build-essential` (gcc, for `-race` with `CGO_ENABLED=1`) and `git`.
- `bubblewrap` (`sudo apt-get install bubblewrap`, giving `/usr/bin/bwrap`) and
  unprivileged user namespaces, for guarded runs
  (`service:linux-sandbox-bwrap`). `wsl.sh --guarded-preflight` installs the
  package when `sudo` needs no password, and fails otherwise.
- `GOCACHE`, `GOMODCACHE` and `TMPDIR` on ext4 (the defaults under `~` and `/tmp`
  are).
- Optional: `[boot] systemd=true` in `/etc/wsl.conf`; it enables the Linux
  user-scope path; without it the process-group fallback is reported (EN-5).
  Raise the WSL memory limit (`.wslconfig`) if the coverage gate is killed.
- Run `wsl --shutdown` before a budget run, to reset the clock drift of a
  long-running VM.

**The clone rule.** Run the WSL lane only from a WSL-native clone on ext4, never
on `/mnt/e`:

```sh
git clone /mnt/e/work/github/ai/agent-plugin ~/src/agent-plugin
git -C ~/src/agent-plugin checkout <sha>
```

Why:

- DrvFs/9P reports every file as `0777` and ignores `chmod`. This breaks the
  privacy tests and marks the tree `vcs.modified`.
- `flock`, rename and inotify are unreliable there.
- git's "dubious ownership" check breaks VCS stamping.
- The budget gate fails on its speed.

`.gitattributes` already forces `eol=lf`; do not set `core.autocrlf`.

### Provisioning the Windows lane

- The floor toolchain is selected with `GOTOOLCHAIN=go1.27.4`. The host default
  may be another release; `win.ps1` sets the variable from `src/harness/go.mod`,
  and the gate pins every child to the floor itself.
- MSYS2 UCRT64 `gcc` on `PATH` (for `-race`), as in `.github/workflows/ci.yml`.
- PowerShell 7 (`pwsh`) on `PATH`; `win.ps1` fails without it.
- A non-elevated shell, and the checkout on a local NTFS or ReFS (Dev Drive)
  volume.
