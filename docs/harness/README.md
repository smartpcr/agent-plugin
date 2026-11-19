# deepseek-harness in Go: design and implementation documents

This folder designs a Go reimplementation of **deepseek-harness**, an open-source agent
harness written in TypeScript: a program that takes a user message, asks a large language
model what to do, runs the tools the model asks for, and repeats. The Go version is one
binary, `dsh`, built on the cordis component framework already ported to Go in this
repository (`src/go`). These documents say what the Go version must achieve, how it is
built, and how each part is proven. **No harness code exists yet.**

Upstream, the requirements source: https://github.com/deepseek-ai/deepseek-harness at
commit `2e39e70496aa02986ca6a820c5d497e816b69d7e` (2026-11-18). Every upstream fact in
these documents was read at that commit and is cited as `path:line`.

## Status

The five statuses are kept apart. A later status is never claimed because an earlier one
is done. Nothing has been built, run or tested.

| Status | State | Where it is tracked |
|---|---|---|
| Design | **Done**, for owner review; the owner's decisions are fixed in [design.md §7.1](design.md#71-decisions-fixed-by-the-owner) | [design.md](design.md), the specifications below |
| Implementation | Not started | [impl.md](impl.md), stage by stage |
| Test authoring | Not started | The `DH-T` and `DH-E` items of each specification |
| Local passes | Not started | The evidence record of each stage in the plan |
| Real-environment acceptance | Not started | The acceptance stages of the plan; a skipped or missing scenario stays open |

Ten library specifications contain observations from throwaway programs run during
library research. They are marked there as unverified research notes, each with the test
that is to measure it ([design.md §10](design.md#10-limitations)); none is a test result.

## Reading order

For a first reading, in this order:

1. [design.md](design.md) §1 to §3: purpose, vocabulary (including the table of area
   keys such as `A07` or `C3`), and the sixteen outcomes `DH-O-1` to `DH-O-16`.
2. [design.md](design.md) §4 to §6: architecture, failure and cleanup, and the
   security model with its stated limits.
3. [design.md](design.md) §7 and §9: the owner's decisions, the recommended defaults
   the owner may overturn, and what is still open.
4. [impl.md](impl.md): stages, order, priorities and the
   evidence that closes each stage.
5. The specification of the area or library you work on, from the table below.

| Document | What it holds |
|---|---|
| [design.md](design.md) | The spine: outcomes, architecture, global decisions, owner decisions, limitations |
| [impl.md](impl.md) | Every deliverable scheduled in stages, with order, priority and how completion is proven |
| [areas/](areas/) | One specification per area of the product (39); keys in [design.md §2](design.md#area-keys) |
| [libs/](libs/README.md) | One specification per upstream dependency: what it does upstream and the Go choice |
| [upstream/](upstream/) | The foundation documents: upstream's architecture, persistence, wire protocols, security and toolchain, with recommended Go decisions |
| [debt/](debt/) | The two registers that sort every carried tech-debt item into platform limit, scope boundary or unresolved: [carried-core.md](debt/carried-core.md), [carried-host-ui-experimental.md](debt/carried-host-ui-experimental.md) |
| [audit/](audit/) | Three independent audits of the finished documents (see below) |
| [traceability.md](traceability.md) | Generated coverage of every upstream unit |
| [trace/](trace/README.md) | The machine-readable ledger behind the traceability check, and its rules |

## ID scheme

Every item has a stable ID with the prefix `DH` (deepseek-harness). IDs are never
renumbered; a withdrawn item keeps its ID and says so.

| Form | What it names | Example |
|---|---|---|
| `DH-O-n` | An outcome: observable behaviour of the finished product | `DH-O-7`, confinement |
| `DH-ADR-n` | A global decision; 1 to 40 are fixed by the owner | `DH-ADR-28` |
| `DH-ADR-<AREA>-n` | A decision of one foundation, library or area document | `DH-ADR-SECURITY-12` |
| `DH-D-<AREA>-n` | A deliverable: a piece of work with an outcome, a priority, dependencies and proof | `DH-D-CORDIS-1` |
| `DH-T-<AREA>-n` | A local test: unit, composition, golden or replay | `DH-T-LOADER-2` |
| `DH-E-<AREA>-n` | A real-environment scenario, written as Given, When, Then, Cleanup | `DH-E-TOOLCHAIN-2` |
| `DH-LIB-<NAME>` | A library item: the Go answer to one upstream dependency; `-Rn` is one of its requirements | `DH-LIB-SEMVER-R1` |
| `DH-S-n` | A stage of the implementation plan | `DH-S-1` |
| `UP-<TYPE>-...` | An upstream unit in the inventory: package, dependency, decision note, tech debt, tool, event, configuration, service, command or persisted event | `UP-DEBT-found.Q3.1` |

`CF-n` (a conflict between foundation recommendations) and `OD-n` (a former open
decision) are working labels in design.md, not ledger IDs.

## Coverage

Figures as of 2026-11-19, from `tools/harness-trace verify` (which passes) and
[traceability.md](traceability.md).

**Upstream side.** 3,677 upstream units were inventoried, and all 3,677 carry a
disposition (100%):

| Unit type | Units |
|---|---:|
| Packages and directories | 420 |
| Dependencies | 242 |
| Decision notes | 464 |
| Tech-debt items | 1,999 |
| Model-facing tools | 77 |
| Events | 111 |
| Configuration schemas | 174 |
| Services | 119 |
| Commands | 7 |
| Persisted event types | 64 |
| **Total** | **3,677** |

**Go side.** 577 decisions, 395 deliverables, 152 library items, 1,231 local tests and
246 real-environment scenarios, scheduled in the 59 stages of
[impl.md](impl.md).

`tools/harness-trace verify` checks that every unit has a disposition, that every link
resolves, and that every deliverable has an outcome, a proof and a stage. It checks that
a judgement was recorded, not that the judgement is right.

## Tech debt

Upstream's 1,999 tech-debt items are dispositioned as follows:

| Class | Items | Meaning |
|---|---:|---|
| Resolved | 1,366 | The design removes the cause. This includes limits that disappear because the feature that had them is not built; such a row names the decision that drops the feature |
| Not applicable | 243 | The item exists only because of JavaScript, Node, npm or the upstream build. A row that is not applicable because a feature is dropped names the decision that drops it |
| Carried: platform limit | 103 | A fact of a platform or third party that no design removes; documented as a known limit |
| Carried: scope boundary | 282 | A deliberate product boundary, each stated in the words of a linked decision record |
| Carried: **unresolved** | **5** | Remaining debt, listed below |
| **Total** | **1,999** | 390 carried |

The five unresolved items each need a vendor fact or a real-environment run, so none can
be closed on paper:

1. The effort field of the vendor's model listing (`llm-deepseek-account.4`).
2. The vendor's business error codes (`protocol.ts:245`).
3. How Windows PowerShell 5.1 decodes non-ASCII text on standard input (`pwsh-local.7`).
4. and 5. Whether each sandbox backend reports a denied operation (`bash-sandbox.2`,
   `sandbox.3`).

The verifier checks every carried row's class, stage and decision link.

**How far to trust these figures.** After the audit, every scope-boundary,
platform-limit and not-applicable row was re-checked one by one. The 1,366 resolved rows
were **sampled, not all re-read**. The debt audit found 2 of 80 sampled resolved rows to
be resolved in name only, so roughly 17 to 70 of the 1,366 may be resolved in name only
and not yet identified. The true count of remaining debt is therefore higher than 5; how
much higher is not known until those rows are re-read.

## Audit results

Three independent audits read the finished documents. **The three reports describe the
state before the fix pass.** Every finding was then fixed in a fix pass, and **the fixes
have not been re-audited.**

| Audit | What it checked | Result |
|---|---|---|
| [audit/q2-accuracy.md](audit/q2-accuracy.md) | Ledger rows and citations against upstream source | 172 sampled ledger rows, 171 correct: mismatch **0.6%**. 40 citations checked, 38 correct. 15 Go modules confirmed |
| [audit/q1-consistency.md](audit/q1-consistency.md) | The documents against each other, the owner's decisions and the documentation rules | 1 blocker, 13 major and 9 minor findings |
| [audit/q3-debt.md](audit/q3-debt.md) | Whether the tech-debt list is complete and each disposition honest | 8 major and 3 minor findings; 8 debt items added to the inventory |

## Known deviations

- Eleven area specifications are 601 to 634 lines long, over the 600-line guideline.
- Ten library specifications reported throwaway programs being run during research. In
  the fix pass these reports were relabelled as unverified research notes, each with the
  test that is to measure it; none is a test result.

## What remains open

- **Decisions the owner has not taken**: [design.md §9.3](design.md#93-still-open),
  among them code-signing certificates and where keys are kept, where the documentation
  website is hosted, and the recommended defaults of [§9.2](design.md#92-recommended-defaults-applied-not-confirmed-by-the-owner)
  the owner may still overturn.
- **Facts that need a real run**: the observations marked as unverified research notes in
  ten library specifications; the SQLite driver's measurements; that the pseudo-terminal
  and keychain modules build without cgo; whether the tool endpoint is reachable from
  inside each sandbox backend; and every real-environment scenario. Each is named, with the
  test or scenario that settles it, in [design.md §10](design.md#10-limitations).
- **No security audit**: the harness is stated as unaudited until an independent review
  ([DH-ADR-SECURITY-12](upstream/security.md#dh-adr-security-12)).
