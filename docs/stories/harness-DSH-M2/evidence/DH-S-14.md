# DH-S-14 evidence

Closure status: open

Evidence record of [impl.md DH-S-14](../../../harness/impl.md#dh-s-14) (Agent presets and bundles) for story
`harness:DSH-M2`, in the impl.md sec. 7.3 form of the M1 records
([../../harness-DSH-M1/evidence/README.md](../../harness-DSH-M1/evidence/README.md)). Plan:
[implementation-plan.md](../implementation-plan.md) Phase 4. Decisions of
[../../harness-DSH-M1/decisions.md](../../harness-DSH-M1/decisions.md) stay in force.

**Kind.** Build, P0. Paired acceptance stage: DH-S-17.

**How this record closes** (C57; M2-C-2; [the gate's README](../../../../tools/harness-gate/README.md#stage-closure)):

1. While the line above reads `open`, fill the tables below until `harness-gate ready --stage DH-S-14 --built`
   exits 0. `ready` does not read the status line.
2. Only then does the closing PR change the line to `Closure status: closed`.
3. That PR's gate runs `harness-gate close --stage DH-S-14 --built`: every `ready` condition again, and the
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
| 14.1 | DH-D-BUNDLE-1 | 4ad9da7 | squash-merge of #103; the five definitions in `boot/profile/profile.go` (`Definitions`), `bundles/headless.yml` and `bundles/sdk.yml` filled, `composed.golden` regenerated |
| 14.2 | DH-D-BUNDLE-2 | 9f59581 | squash-merge of #107; `bundles/admin.yml`, `bundles/base.yml`, `core.yml` extended; `Insert`, `Merge`, `Remove` and `Preview` (the owning layer of each entry and value) in `boot/profile/stack.go` |
| 14.3 | DH-D-BUNDLE-4 | 4a45471 | squash-merge of #112; `boot/agentpreset`: declarations, revisions with lease counts, `Extends` checks, `Select`, session binding and its fold |
| 14.4 | DH-D-BUNDLE-5 | e08b33f | squash-merge of #142; `agentpreset/presets/standard.yml` and `agentpreset/persona` |
| 14.5 | DH-D-BUNDLE-6 | 9a634aa | squash-merge of #144; Coverage check `profile.Coverage` and `bundles/optional.yml`; optional list reviewed below; `composed.golden` unchanged (the list is not a layer) |

## Scenarios

None executed here. The bundle scenarios are authored in DH-S-16: DH-E-BUNDLE-3 runs in DH-S-22, DH-E-BUNDLE-1, -2 and -4 in DH-S-48.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |

## Manual checks register

The manual checks impl.md DH-S-14 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 14.2 | the composed tree of each profile previewed: which layer supplied three chosen values | Seen on Windows 11 and on WSL Ubuntu, for each of `admin`, `core`, `web`, `headless` and `sdk` stacked by `profile.Stack` over a scratch home with a profile layer merging `logging.level: debug` and a user layer merging `sharing.off: true`, previewed by `profile.Preview` (tree and ownership index dumped): `logging config.destination` `"stderr"` supplied by `bundle:admin` (`bundles/admin.yml:10:7`), `logging config.level` `"debug"` by `profile` (`profiles/<name>/layer.yml:1:3`), `sharing config.off` `true` by `user` (`config/layer.yml:1:3`). Every profile but `admin` also reports its Required entries not yet built (`profile/missing-required`, as `composed.golden` lists them) | Forge engineer agent run, unattended (operator acceptance asked, question `dh-s-14-manual-checks`) | fb4b97d | `manual` |
| 14.3 | a preset edited while a test session runs: the session keeps its revision | Seen on Windows 11 and on WSL Ubuntu, real `fs.tools` and `shellexec.tools` over an embedded SQLite file: session `s1` bound to the shipped `standard` rev 1 (`sha256:1b86830f...`, tools `apply_patch bash edit glob grep pwsh read write`); with `s1` open, the scratch `standard.yml` edited (persona prefix rewritten, `applyPatch: false`) and reloaded: rev 1 retired with 1 lease, rev 2 (`sha256:ab4e5427...`) current; `s1`'s fold, registry binding, tool list and persona prefix stay rev 1's; a new `s2` binds rev 2 (no `apply_patch`, the edited prefix); after closing and reopening the database `s1`'s fold still reads rev 1's digest; closing `s1`'s scope unloads rev 1 | Forge engineer agent run, unattended (operator acceptance asked, question `dh-s-14-manual-checks`) | fb4b97d | `manual` |

Logs and the throwaway test sources: [runs/manual-fb4b97d/](runs/manual-fb4b97d/README.md).

## Owner defaults (M2-C-4)

None: impl.md sec. 9.1 names no default to confirm before DH-S-14.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Built record at `fb4b97d` (main, after #145; every step above merged). Go `go1.27.4` (the floor
of `src/harness/go.mod`) on both lanes. No binary built for this record. At closure: the commit
of the three modules (one commit), the binary's SHA-256 and embedded version.

### Environment and fixture

Gate venue: both local lanes, `tools/harness-gate/lanes/win.ps1` (Windows native) and
`lanes/wsl.sh` (Linux under WSL); macOS out of scope (`platforms-windows-wsl`). Manual checks and
package tests: Windows 11 (10.0.28000), go1.27.4 windows/amd64, gcc 16.1.0 (MSYS2); WSL2 Ubuntu
(Linux 6.18.58.1-microsoft-standard-WSL2), go1.27.4 linux/amd64. Fixture: scratch homes,
workspaces, preset files and SQLite databases in `t.TempDir()`, in a throwaway worktree at
`fb4b97d`, removed afterwards.

### Exact tests and commands

At `fb4b97d`, on both platforms (WSL: `GOTOOLCHAIN=local GOFLAGS=-buildvcs=false`):

- `go -C src/harness test -count=1 -race -shuffle=on ./internal/boot/profile/... ./internal/boot/agentpreset/...`:
  `ok` for `boot/profile`, `boot/agentpreset` and `boot/agentpreset/persona` on both.
- `go -C src/harness test -count=1 -v -run 'TestShippedBuildIsCovered|TestDefaultProfilesSendNothing' ./internal/boot/profile/`:
  both `PASS` (Windows).
- The manual checks: `go -C src/harness test -count=1 -v -run TestManualS14 ./internal/boot/profile/ ./internal/boot/agentpreset/`
  with the two throwaway files of [runs/manual-fb4b97d/](runs/manual-fb4b97d/README.md) copied in: `PASS` on both.
- Windows lane: `tools/harness-gate/lanes/win.ps1` at `fb4b97d`, see Results.

`DSH_REQUIRE_SCENARIOS` (`harness-gate require --stage DH-S-14`): not yet recorded.

### Run identifiers

Windows lane at `fb4b97d`: local run record
[`win-fb4b97d-20261126T0016Z.txt`](../../harness-DSH-M1/evidence/runs/win-fb4b97d-20261126T0016Z.txt),
SHA-256 `ce89c874fc45aaf32b6a8878c1fa77ea12ef18318e2f37c38d636262cf90aadc`. WSL lane: not run (see
Results). No session ids or provider request ids: no model call.

### Results

The Scenarios table above (none here), the package tests and manual checks above, and:

**Lanes at `fb4b97d`: failed at gate 1, in code outside DH-S-14.** The Windows lane stops at
`harness-gate: FAIL gofmt (1.397s)`, `not gofmt-formatted:
src/harness/test/e2e/harness-DSH-M2/dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_seam_probe_select_confirm_test.go`
(one struct field, `before int`, not aligned; from #135, DH-S-13). gofmt does not depend on the
platform, so the WSL lane was not started for this commit. The S14 gate scenario (M2-DOD-1)
stays open until that file is formatted on `main` and both lanes pass (Open items).

**Step 14.5: coverage over the components that exist so far, and the optional list
reviewed** (DH-D-BUNDLE-6, DH-ADR-BUNDLE-10). `TestShippedBuildIsCovered`
(`internal/boot/profile/coverage_test.go`) passes on the nine components of the build
catalogue (`internal/boot/catalog/testdata/catalog.golden`):

- Mounted by a shipped layer: `boot.logging` and `host.sharing` (`admin.yml`), `util.egress`
  and `util.watch` (`core.yml`); in every profile through the kernel.
- On `internal/boot/profile/bundles/optional.yml`, each with an owner and a reason:
  `fs.local` (A08), `fs.ops` (A08), `sandbox.policy` (A10), `session.workdir` (A01) — the
  cycle C-3 unit of DH-S-12, members of `core.yml` by design — and `permission.presets`
  (A17), a member of `base.yml` by design. Each waits on inject for a service no catalogue
  item provides yet (`session.core`, `session.views`, `human.approval`, `human.command`);
  mounted now, every launch would wait out its `StartupTimeout`. Each row moves to its
  layer in the change that registers the provider. Reviewed: no row is also mounted, and
  every row names a catalogue component (both enforced by the check).
- Defaults send nothing (NG-7): `TestDefaultProfilesSendNothing` composes `admin`, `core`,
  `web`, `headless`, `sdk`, `sdk-minimal` and `acp` from their bundles with no route and no
  home layer; `profile.Egress` of each is empty (`admin.yml` ships `host.sharing` with every
  kind off).
- `composed.golden` is unchanged: `optional.yml` is not a bundle layer (`BundleNames`
  leaves it out, `Bundle("optional")` is `profile/bad-bundle`).

Command: `go -C src/harness test -race -shuffle=on ./internal/boot/profile/... ./internal/boot/agentpreset/...`.

### Manual checks

The Manual checks register above.

### Cleanup evidence

The throwaway worktree at `fb4b97d` (manual checks and Windows lane) was removed with
`git worktree remove`; the two manual test files were never committed as Go source (kept as
`.txt` under [runs/manual-fb4b97d/dhs14/](runs/manual-fb4b97d/README.md)); every scratch path
was a `t.TempDir()`, removed by the test.

### Open items

The lanes at `fb4b97d` fail gofmt on a DH-S-13 e2e file (Results); both lanes are to be run again
once it is formatted on `main` (question `dh-s-14-gofmt-lane-blocker`). Operator acceptance of the
two manual checks (question `dh-s-14-manual-checks`).

Built-not-done: BUNDLE-2, BUNDLE-6 until DH-S-22; BUNDLE-4, BUNDLE-5 until DH-S-48 (impl.md problem S-1).
