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
| 14.1 | DH-D-BUNDLE-1 | | |
| 14.2 | DH-D-BUNDLE-2 | | |
| 14.3 | DH-D-BUNDLE-4 | | |
| 14.4 | DH-D-BUNDLE-5 | | |
| 14.5 | DH-D-BUNDLE-6 | | Coverage check `profile.Coverage` and `bundles/optional.yml`; optional list reviewed below; `composed.golden` unchanged (the list is not a layer) |

## Scenarios

None executed here. The bundle scenarios are authored in DH-S-16: DH-E-BUNDLE-3 runs in DH-S-22, DH-E-BUNDLE-1, -2 and -4 in DH-S-48.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |

## Manual checks register

The manual checks impl.md DH-S-14 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 14.2 | the composed tree of each profile previewed: which layer supplied three chosen values | | | | `manual` |
| 14.3 | a preset edited while a test session runs: the session keeps its revision | | | | `manual` |

## Owner defaults (M2-C-4)

None: impl.md sec. 9.1 names no default to confirm before DH-S-14.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Not yet recorded. At closure: the commit of the three modules (one commit), the Go version (`go1.27.4`),
the binary's SHA-256 and embedded version.

### Environment and fixture

Not yet recorded. Gate venue: both local lanes, `tools/harness-gate/lanes/win.ps1` (Windows native) and
`lanes/wsl.sh` (Linux under WSL); macOS out of scope (`platforms-windows-wsl`).

### Exact tests and commands

Not yet recorded: the gate runner's output, the `go test` command lines and `DSH_REQUIRE_SCENARIOS`
(`harness-gate require --stage DH-S-14`).

### Run identifiers

Not yet recorded: CI run and job ids, or the local run-record id and its SHA-256 (M1 runbook template);
session ids and provider request ids for model calls.

### Results

The Scenarios table above, and the local test results per step at closure. Not yet recorded.

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

Not yet recorded: the before-and-after comparison the stage names, or what was retained.

### Open items

Built-not-done: BUNDLE-2, BUNDLE-6 until DH-S-22; BUNDLE-4, BUNDLE-5 until DH-S-48 (impl.md problem S-1).
