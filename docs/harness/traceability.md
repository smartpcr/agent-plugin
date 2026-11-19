# Traceability: deepseek-harness to the Go design

> **Generated** by `tools/harness-trace render` from the inventory and the ledger
> fragments under [trace/](trace/README.md). Do not edit by hand.

Upstream: https://github.com/deepseek-ai/deepseek-harness at commit `2e39e70496aa02986ca6a820c5d497e816b69d7e`.

Every upstream unit the inventory found carries one disposition. A unit links to the
Go decision, deliverable or library that accounts for it, and each deliverable links
to the tests and scenarios that prove it. `tools/harness-trace verify` fails if any
unit, link or proof is missing.

## Coverage

| Unit type | Units | With disposition | Dispositions | Matrix |
|---|---|---|---|---|
| Packages and directories (`UP-PKG`) | 420 | 420 | dropped 47, merged 57, not-applicable 3, ported 187, replaced 126 | [matrix-pkg.md](trace/matrix-pkg.md) |
| Dependencies (`UP-DEP`) | 242 | 242 | dropped 12, merged 5, not-applicable 56, ported 9, replaced 160 | [matrix-dep.md](trace/matrix-dep.md) |
| Upstream decisions (`UP-DEC`) | 464 | 464 | dropped 41, merged 5, not-applicable 48, ported 216, replaced 154 | [matrix-dec.md](trace/matrix-dec.md) |
| Tech debt (`UP-DEBT`) | 1999 | 1999 | carried 390, not-applicable 243, resolved 1366 | [matrix-debt.md](trace/matrix-debt.md) |
| Model-facing tools (`UP-TOOL`) | 77 | 77 | dropped 6, merged 1, ported 66, replaced 4 | [matrix-tool.md](trace/matrix-tool.md) |
| Events (`UP-EVT`) | 111 | 111 | dropped 12, merged 6, ported 50, replaced 43 | [matrix-evt.md](trace/matrix-evt.md) |
| Plugin config (`UP-CFG`) | 174 | 174 | dropped 14, merged 12, not-applicable 1, ported 100, replaced 47 | [matrix-cfg.md](trace/matrix-cfg.md) |
| Services (`UP-SVC`) | 119 | 119 | dropped 10, merged 17, ported 65, replaced 27 | [matrix-svc.md](trace/matrix-svc.md) |
| CLI commands (`UP-CLI`) | 7 | 7 | ported 6, replaced 1 | [matrix-cli.md](trace/matrix-cli.md) |
| Persisted session events (`UP-PERSIST`) | 64 | 64 | dropped 6, ported 54, replaced 4 | [matrix-persist.md](trace/matrix-persist.md) |
| **Total** | **3677** | **3677** | | |

## Carried tech debt by class

Only `unresolved` is remaining debt. A `platform-limit` is a fact no design removes; a
`scope-boundary` is a deliberate boundary stated by a decision record.

| Class | Units |
|---|---|
| platform-limit | 103 |
| scope-boundary | 282 |
| unresolved | 5 |

## Go-side items

| Kind | Count |
|---|---|
| decision | 577 |
| deliverable | 395 |
| library | 152 |
| scenario | 246 |
| test | 1231 |

## How to read a row

- **ported**: the behaviour exists in the Go design, in Go form.
- **replaced**: a different mechanism gives the same result; the link names the decision.
- **merged**: folded into another Go component; the link names it.
- **dropped**: deliberately not built; the linked decision record says why.
- **not-applicable**: exists only because of JavaScript, Node, npm or the upstream build; the reason says which.
- **resolved** (tech debt): the Go design removes the cause; the link shows where.
- **carried** (tech debt): still present, with a written justification and an owning stage.
