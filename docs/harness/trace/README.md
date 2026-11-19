# The traceability ledger

This folder is the machine-readable record that connects every part of upstream
deepseek-harness to the Go design. It exists so that "nothing was forgotten" is a
check a tool runs, not a statement someone makes.

## How it works

1. **Inventory.** `tools/harness-trace inventory` reads the upstream checkout and writes
   one row per upstream **unit** into `inventory/*.jsonl`. A unit is something the Go
   design must account for.
2. **Assignment.** [clusters.json](clusters.json) names the research agents and the
   upstream paths and dependencies each one owns. `assign` writes each agent's units to
   `assignments/<agent>.jsonl`. Every unit has exactly one owner.
3. **Fragments.** Each agent writes its findings as rows in `fragments/<agent>.jsonl`.
   An agent only writes its own fragment, so agents never edit the same file.
4. **Merge and verify.** `merge` resolves one winning row per unit into `ledger.jsonl`.
   `verify` fails if any unit has no disposition, any link points at nothing, or any
   deliverable has no proof.
5. **Render.** `render` writes [../traceability.md](../traceability.md) and one
   `matrix-<type>.md` per unit type for people to read.

## Unit types

| Prefix | What one unit is | Where the inventory finds it |
|---|---|---|
| `UP-PKG` | An upstream workspace package, or a directory that is not a package but holds product content | `pnpm-lock.yaml` importers, plus a fixed list of extra directories |
| `UP-DEP` | A third-party npm dependency, a Node built-in module, or a Python dependency | `pnpm-lock.yaml`, `node:` imports in source, `pyproject.toml` |
| `UP-DEC` | An upstream decision note that is still current | `.agents/notes/{implemented,proposed,rejected}` |
| `UP-DEBT` | A piece of tech debt | `patches/`, pins and exemptions in `pnpm-workspace.yaml`, each bullet under a package README's "Known Limitations and Deferred Work", `FIXME`/`TODO`/`XXX` and `@deprecated` in source, baseline files, postmortems |
| `UP-TOOL` | A tool the model can call | `docs/tool-catalog.md` |
| `UP-EVT` | A typed event | `docs/event-producer-consumer.md` |
| `UP-CFG` | A plugin's config schema | `docs/config-catalog.md` |
| `UP-SVC` | A service on the cordis context (`ctx.<name>`) | `docs/capability-seams.md` |
| `UP-CLI` | A `dsh` command | `docs/cli-help.md` |
| `UP-PERSIST` | A persisted session event type | `docs/persistence-catalog.md` |

The granularity is the unit, not the source line. An agent may add a `UP-DEBT` or
`UP-DEC` unit the inventory could not find mechanically (see "define" below).

## Fragment rows

A fragment is JSON Lines: one JSON object per line. A row does one of three things.

**1. Give an upstream unit its disposition.**

```json
{"unit":"UP-PKG-packages.core.session","disposition":"ported","reason":"The append-only session log is kept; storage moves behind a Go interface.","links":["DH-D-SESSION-1","DH-ADR-SESSION-2"],"spec":"areas/core-session.md"}
```

| Disposition | Meaning | Required |
|---|---|---|
| `ported` | The behaviour exists in the Go design, in Go form | at least one link |
| `replaced` | A different mechanism gives the same result | at least one link |
| `merged` | Folded into another Go component | at least one link |
| `dropped` | Deliberately not built | a link to a `DH-ADR` decision that says why |
| `not-applicable` | Exists only because of JavaScript, Node, npm or the upstream build process | the reason says which |
| `resolved` (debt only) | The Go design removes the cause | at least one link |
| `carried` (debt only) | Still present in the Go design | `justification` and an owning `stage` |

`UP-DEBT` units accept only `resolved`, `not-applicable` and `carried`. Every row needs a
`reason` of real substance and a `spec`: the document under `docs/harness` that explains it.

**2. Define an upstream unit the inventory missed.**

```json
{"define":"UP-DEBT-found.A04.1","type":"DEBT","title":"Retry policy is duplicated in two adapters","evidence":"packages/llm/llm-deepseek/src/retry.ts:40"}
```

The ID must be `UP-DEBT-found.<agent>.<n>` or `UP-DEC-found.<agent>.<n>`. It then needs a
disposition row like any other unit.

**3. Declare a Go-side item.**

```json
{"go":"DH-D-SESSION-1","kind":"deliverable","title":"Append-only session log","outcomes":["DH-O-3"],"proof":["DH-T-SESSION-1","DH-E-SESSION-1"],"deps":["DH-D-CORDIS-1"],"priority":"P0","spec":"areas/core-session.md"}
{"go":"DH-T-SESSION-1","kind":"test","title":"Appended events are immutable","proves":["DH-D-SESSION-1"],"spec":"areas/core-session.md"}
{"go":"DH-E-SESSION-1","kind":"scenario","title":"A session survives a host restart","proves":["DH-D-SESSION-1"],"spec":"areas/core-session.md"}
{"go":"DH-ADR-SESSION-2","kind":"decision","title":"Storage behind one interface","serves":["DH-O-3"],"spec":"areas/core-session.md"}
{"go":"DH-LIB-YAML","kind":"library","title":"YAML parsing","choice":"module","module":"gopkg.in/yaml.v3","version":"v3.0.1","license":"MIT and Apache-2.0","verified":"go list -m and LICENSE in module cache, 2026-11-18","spec":"libs/yaml.md"}
```

| Kind | ID form | Required fields |
|---|---|---|
| `deliverable` | `DH-D-<KEY>-<n>` | `outcomes` (at least one `DH-O`), `proof` (at least one test or scenario), `spec` |
| `test` (local) | `DH-T-<KEY>-<n>` | `proves`, `spec` |
| `scenario` (real environment) | `DH-E-<KEY>-<n>` | `proves`, `spec` |
| `decision` | `DH-ADR-<KEY>-<n>` | `spec` |
| `library` | `DH-LIB-<KEY>` | `choice` (`stdlib`, `module`, `own`, `dropped`, `toolchain`, `open`); a `module` choice also needs `module`, `version`, `license`, `verified` |

`<KEY>` is upper-case letters and digits. Area agents use the `idKey` from
`clusters.json`; a library spec uses a key derived from the library name. Global items
(`DH-O-n`, `DH-ADR-n`, `DH-S-n`) are defined by anchors in
[../design.md](../design.md) and [../impl.md](../impl.md).
Every Go item must be written, by ID, in its `spec` document.

## Who wins when two agents write a row for the same unit

The row from the later wave wins. A foundation or decision agent gives a first
classification; the area agent that owns the code refines it. Two rows for one unit in
the same wave are an error. Rows in `fragments/LEAD.jsonl` override everything and are
used for review fixes.

## Commands

Run from the repository root:

```bash
go -C tools/harness-trace run . inventory        # needs ../deepseek-harness
go -C tools/harness-trace run . assign
go -C tools/harness-trace run . verify -partial  # while waves are still running
go -C tools/harness-trace run . verify           # final gate, exit code 1 on any failure
go -C tools/harness-trace run . render
```
