# Audit Q3: is the tech-debt list complete, and are its dispositions honest?

| | |
|---|---|
| **Auditor** | Q3 (wave 7). The auditor wrote none of the documents or ledger rows it judges. |
| **Date** | 2026-11-19 |
| **Upstream** | `e:\work\github\ai\deepseek-harness` at commit `2e39e70`, read only |
| **Wrote** | This report, and the ledger fragment `trace/fragments/Q3.jsonl` (8 new units) |
| **Status** | Design only. Nothing was built, run or tested. Every "resolved" below means "a specification describes a mechanism"; none means "works". |

## 1. What was audited, how, and what was not

**The claim under audit.** Upstream has 1,991 tech-debt units. The ledger says 1,362 are
resolved by the Go design, 245 cannot arise in Go, and 384 are carried: 110 platform
limits, 271 scope boundaries and 3 unresolved. Under the owner's rule only the 3 count as
remaining debt.

**Half 1, completeness.** I read the inventory rule in `tools/harness-trace/inventory.go`
(lines 555 to 657) and then counted the same things in upstream with read-only searches
(`git grep`, `awk` to the terminal), plus kinds the rule does not look for.

- *README bullets.* For all 389 package READMEs that have the heading "Known Limitations
  and Deferred Work" I counted the top-level bullets of that section and compared the
  count per README with the inventory. The 25-README sample the task asks for is the
  subset "sort the 389 paths, take every 15th starting at the 8th" (26 READMEs, from
  `packages/api/session-controller` to `packages/web/web-deepseek-api`); the comparison
  was run on all 389.
- *Markers.* Counted `TODO`, `FIXME`, `XXX`, `HACK` and `@deprecated` in all tracked text
  files, excluding `pnpm-lock.yaml`, `snapshots/`, Chinese translations and (for the
  comparison) Markdown, `.agents/` and `docs/`. Each line missing from the inventory was
  read.
- *Other kinds.* Counted skipped tests, suppressions, workaround wording, pins,
  limitation headings outside package READMEs, "deferred" sections of decision notes,
  postmortems and baselines; read the instances that could matter to a Go design.

**Half 2, honesty.** Samples and their rules (each can be repeated):

| Sample | Population | Rule | Size |
|---|---|---|---:|
| Resolved | 1,362 `resolved` debt rows of `trace/ledger.jsonl`, in file order | every 17th, starting at the 9th | 80 (41 agents) |
| Not applicable | 245 rows, in file order | every 6th, starting at the 3rd | 41 |
| Scope boundary | `"class":"scope-boundary"` rows of `fragments/K1.jsonl` (129) and `K2.jsonl` (142), in file order | K1: every 5th from the 2nd; K2: every 6th from the 4th | 26 + 24 = 50 |
| Platform limit | `"class":"platform-limit"` rows of K1 (46) and K2 (64) | K1: every 4th from the 2nd; K2: every 5th from the 3rd | 12 + 13 = 25 |
| Convergence round | the 22 rows the two registers list as resolved in the round, plus the 4 they reclassified as resolved | all | 26 |
| Unresolved | the 3 rows | all | 3 |

The carried samples were drawn from the K1 and K2 fragments because the merged ledger
has no `class` field (finding Q3-1).

For each row I read the full upstream bullet, the row's reason or justification, and the
heading and opening text of each linked item. **Depth differs and matters:** for 38 of
the 80 resolved rows, all 50 scope rows and all 26 convergence rows I also searched the
specification for the specific mechanism or boundary the row claims. The other 42
resolved rows were judged from the row's reason against the linked item's title and
first few hundred characters only.

**Not audited.** Whether any mechanism would work when built. The other 1,282 resolved
rows, 204 not-applicable rows and 309 carried rows. Limitations written as prose in the
upstream documents under `docs/subsystems` that have no "limits" heading. Whether the
`UP-DEC` dispositions cover the "Deferred" sections inside decision notes item by item.
Upstream issues on the code host (not in the checkout).

## 2. Headline numbers

### 2.1 Debt kinds: covered and not covered by the inventory

| Kind | Upstream instances | In the inventory | Gap, and whether it matters for Go |
|---|---:|---:|---|
| Dependency patches (`patches/`) | 12 | 12 | None |
| Pins and exemptions in `pnpm-workspace.yaml` (`overrides`, `allowBuilds`, `minimumReleaseAgeExclude`) | 53 | 53 | Not read: `peerDependencyRules` (1), `engines` and `packageManager` in `package.json` (2). JavaScript only; does not matter |
| README bullets under "Known Limitations and Deferred Work" | 1,746 in 389 READMEs | 1,746 | **Exact match per README, all 389.** No nested bullets exist |
| Limitations written as prose inside those sections | about 7 paragraphs that state a limit (among 38 non-boilerplate lines in 27 READMEs) | 0 | One matters: `packages/sandbox/sandbox-policy/README.md:165` (added as `Q3.2`). Others are covered by a neighbouring bullet or concern dropped features |
| Limitation sections under other headings | 8 documents: `SAFETY.md:11`, `apps/desktop/README.md:385`, `apps/desktop-publisher/README.md:42`, `docs/subsystems/{feedback,client-resources,mcp,office-kit}.md`, `docs/user/guide/network-proxy.md:46` | 0 by rule; agents added the desktop, publisher and one proxy item by hand | `SAFETY.md` matters (added as `Q3.1`). The subsystem pages mostly repeat README bullets; `docs/subsystems/feedback.md:309` and `:312` (a short race after detach; no size bound on a session remark) have no unit |
| Source markers `TODO` / `FIXME` / `XXX` | 65 real markers outside Markdown and decision notes (80 matching lines; 15 are strings, fixtures or placeholders) | 47 | **18 missed**: 6 in `vitest.config.ts` (root file, outside the scan roots), 5 in `.github` (outside the scan roots), 5 in test files (excluded by rule), 2 in CSS (the marker is mid-comment, the pattern needs it right after the comment opener). Three matter (`Q3.3`, `Q3.4`, `Q3.5`) |
| `HACK` | 0 | not searched | None exist |
| `@deprecated` in source | 15 | 15 | None (11 further hits are copies in documents and one test fixture) |
| Baseline and exemption files | 4 files | 4 | The coverage exclusion list in `vitest.config.ts:275-430` (about 150 lines) and the Windows exclusion list (`:24-64`) are discussed in `upstream/toolchain.md` but were not units (added as `Q3.4`, `Q3.6`) |
| Postmortems (`docs/postmortem`) | 4 | 4 | None |
| Skipped or pending tests | 3 unconditional (`describe.skip` twice, `it.skip` once), 1 `test.fails`, 656 `skipIf` / `runIf` (about 270 by record or replay mode, about 215 by operating system, about 30 by credential, the rest by build artefact or tool presence), 9 in pytest. No `it.todo`, no `test.fixme` | 0 | Not a kind the inventory knows. One unconditional skip matters (`Q3.8`). The 157 tests skipped on Windows are a pointer to POSIX-only behaviour and were not examined one by one |
| Lint and type suppressions | `oxlint-disable` 448, `eslint-disable` 41, `@ts-expect-error` 190, `as unknown as` 1,089, `as any` 32, `v8 ignore` 1,081; `@ts-ignore` 0 | 2 aggregate baseline units | JavaScript only. Does not matter; the Go analogue is `DH-ADR-TOOLCHAIN-8` |
| Workaround wording in comments | "workaround" 1, "for now" 5, "not yet" 65 (62 are plain description), "until ..." 44, "legacy" 189 | 0 | Upstream writes its limits in READMEs, not comments. About 3 are debt-like; none found that matters. Legacy and migration code is outside the Go design by `DH-ADR-2` and `DH-ADR-11` |
| Feature flags that gate unfinished work | none found as a pattern; unfinished work lives in 70 directories under `packages/experimental` | covered through their READMEs | None |
| "Known issues" in `docs/` | 0 (the phrase occurs once, in a style rule) | n/a | None |
| "Deferred", "Out of scope", "Future work" sections inside current decision notes | 26 sections in `.agents/notes/implemented`, 5 in `proposed`; roughly 90 statements | 0 as debt; each note is one `UP-DEC` unit | Covered per note, not per deferred item. Of 16 sections read, one item had no debt unit and matters (`Q3.7`); most others repeat a README bullet |
| Disabled CI jobs | 1 found (`serial-macos`, `if: false`) | 0 | Added as `Q3.5` |

### 2.2 Units added by this audit

Eight, in `trace/fragments/Q3.jsonl`. `verify -partial` reports no failure for `[Q3]`.

| Unit | What | Evidence | Disposition |
|---|---|---|---|
| `UP-DEBT-found.Q3.1` | No security audit; sandbox and approvals are not a guarantee of isolation | `SAFETY.md:7`, `:13` | carried, **unresolved**, `DH-S-13` |
| `UP-DEBT-found.Q3.2` | New policy text must be counter-tested with a real model (it once stopped the model calling tools in 5 of 12 runs) | `packages/sandbox/sandbox-policy/README.md:165` | carried, **unresolved**, `DH-S-12` |
| `UP-DEBT-found.Q3.3` | Model menu scenario skipped on WebKit: the engine crashes | `apps/web/tests/model-menu-observer.e2e.ts:146` | carried, **unresolved**, `DH-S-41` |
| `UP-DEBT-found.Q3.8` | Live test of vendor web search disabled: an answer can carry no structured sources | `packages/web/web-search-deepseek/tests/deepseek.e2e.ts:18-26` | carried, **unresolved**, `DH-S-35` |
| `UP-DEBT-found.Q3.4` | About 150 lines of coverage exclusions, six marked TODO | `vitest.config.ts:275-430` | resolved, `DH-ADR-TOOLCHAIN-7` |
| `UP-DEBT-found.Q3.5` | macOS reference job switched off | `.github/workflows/ci-master.yml:194` | resolved, `DH-ADR-TOOLCHAIN-12`, `-6` |
| `UP-DEBT-found.Q3.6` | Whole packages excluded from tests on Windows by a list | `vitest.config.ts:24-64` | resolved, `DH-ADR-TOOLCHAIN-12`, `-6` |
| `UP-DEBT-found.Q3.7` | Time-limit design for model-written programs left open | `.agents/notes/implemented/architecture/2026-09-11-sandboxed-node-ptc-runtime.md:54-58` | resolved, `DH-D-TOOLS-3`, `DH-T-TOOLS-8`, `DH-ADR-PTC-5` |

The four unresolved rows mean "found by the audit and not yet examined by the owning
area", not "proved to be a design weakness"; an owner may sort some into a limit or a
boundary. **The headline changes to 1,999 units: 1,366 resolved, 245 not applicable, 388
carried, of which 7 unresolved.** No further material unit was left out for the cap of
40; what was not added is counted by kind in 2.1.

### 2.3 Sample results

| Sample | Size | Holds | Does not hold | Could not verify |
|---|---:|---|---|---|
| Resolved | 80 | **78 real** (36 confirmed by finding the mechanism in the specification, 42 judged at heading depth; 14 of the 78 are "resolved by not building the feature", each under a decision) | **1 nominal** (`ui-message-feedback.3`), **1 nominal in part** (`ssh.ssh.1`); 0 wrong | 3 of the 36 searches found the deliverable but not the exact sentence claimed (`ui-settings-subagent.1`, `launch-environment.1`, `telemetry.otel.3`); counted as real |
| Not applicable | 41 | **29** exist only because of JavaScript, Node, npm or React. **11** cannot arise only because a feature was dropped by decision (true, but the wrong label; Q3-7) | **1 questionable**: `session-query.session-log-export.6` (a clipboard write cannot be undone in any design; it is a platform limit). 0 where the Go design clearly suffers the same | 0 |
| Scope boundary | 50 | **40** stated by the linked decision record | **9** where the linked record does not state the boundary (Q3-4); **1** stated but arguably removable debt (`host.open-in-app.2`, which the register itself flags) | 0 |
| Platform limit | 25 | **22** facts no design of this harness removes | **1** that follows from the design's own choice of driver (`subagent-claude-code.6`), **1** that follows from the design's library choice (`client.ui-primitives.8`), **1** asserted without a source and open to doubt (`sandbox.sandbox.3`) | 0 |
| Convergence round | 26 | **26** have a decision, a deliverable and a named test that exists in the specification; **17** remove the whole limit | **9** remove part of it or only on a condition (Q3-6); 0 nominal, 0 wrong | 2 test rows (`DH-T-UIKIT-12`, `DH-T-EXEC-17`) exist but the part I read does not show the claimed assertion |
| Unresolved | 3 | **3** stated accurately; stages `DH-S-13` and `DH-S-35` (twice) are the build stages of the pwsh executor (step 13.6), the vendor provider (35.3) and the vendor account (35.2); the plan admits they have no deliverable or test (gap G-4) | 0 | 0 |

### 2.4 The honest count of remaining debt

The documents say **3**. What this audit supports:

| Component | Count | Basis |
|---|---:|---|
| Recorded unresolved | 3 | all checked |
| Found by this audit, unexamined | 4 | `Q3.1`, `Q3.2`, `Q3.3`, `Q3.8` |
| Named in the samples as not really closed | 4 | `ui-message-feedback.3`, the remote half of `ssh.ssh.1`, `open-in-app.2`, `subagent-claude-code.6` |
| **Named, countable today** | **11** | |
| Resolved rows that are probably nominal, not yet identified | about 17 to 70 of 1,362 | 2 of 80 is 2.5% (34 rows; 95% interval 9 to 118). Both nominal rows were found among the 38 searched in depth, which is 5.3% (72 rows; interval 20 to 230). The heading-depth check could not have found either |
| Not-applicable rows that are really limits | about 6 of 245 | 1 of 41 (interval 1 to 31) |
| Scope boundaries that are removable debt | about 5 of 271 | 1 of 50 (interval 1 to 29) |
| Platform limits that a different design choice removes | about 9 to 13 of 110 | 2 to 3 of 25 (interval 2 to 34) |

**Plain answer: the honest count of remaining debt is not 3. It is 11 that can be named
today, and most likely between 50 and 110 units in all (about 2.5% to 5.5% of 1,999),
with roughly 65 as the central figure** (11 named, plus about 34 resolved, 6 not
applicable, 5 scope and 11 platform rows not yet identified; the four named sample rows
are counted once more inside those estimates, which is within the error). The samples
are small, so a true figure as low as about 25 or as high as about 330 cannot be
excluded. The claim that survives intact is
the broad one: in the samples, 96% or more of resolved rows point at a mechanism that
addresses the limit, and no row was found that states something false about upstream or
about the design.

Three further groups are not debt under the owner's rule but are not "closed" in the
plain sense either: 9 of the 26 convergence resolutions are partial or conditional
(Q3-6); 31 of the 271 scope boundaries rest on a record marked "recommended default, not
confirmed by the owner" (counted over all rows, 17 in K1 and 14 in K2); and about 49 of
271 scope rows (9 of 50) link a record that does not state the boundary.

## 3. Findings

Counts: 0 blocker, 7 major, 4 minor.

### Q3-1 (major) The class of a carried row is not in the ledger and is not checked

- **Evidence.** `trace/ledger.jsonl` has 384 carried debt rows and none has a `class`
  field (`grep -c '"class"' trace/ledger.jsonl` gives 0). The field exists only in
  `fragments/K1.jsonl` (178 rows) and `fragments/K2.jsonl` (206 rows). The `Row` type in
  `tools/harness-trace/ledger.go:15-48` has no such field, so `merge` drops it and
  `verify` never sees it. `trace/README.md` does not define it.
- **Why it matters.** The owner's rule is "only unresolved counts". The one number that
  rule produces (3) cannot be derived from the merged ledger, and a carried row with no
  class, or with a misspelt one, passes the gate.
- **Fix.** Add `Class` to `Row`; make `verify` require one of `platform-limit`,
  `scope-boundary`, `unresolved` on every carried debt row and print the three totals;
  document the field in `trace/README.md` under "Fragment rows"; show it in
  `trace/matrix-debt.md`.

### Q3-2 (major) Two sampled `resolved` rows do not remove the limit they name

1. `UP-DEBT-limit.packages.client.ui-message-feedback.3` (agent C3). Upstream: "the
   trajectory and waterfall views render no feedback controls"
   (`packages/client/ui-message-feedback/README.md`). The row says the control "is a
   partial ... so any page ... can place the same control", and `areas/ui-chat.md:122`
   says "a partial any view can include". The trajectory page is specified in
   `areas/ui-work.md` (`DH-ADR-UIWORK-2`, line 271) and nowhere includes it; no test
   places it there. The limit is unchanged. **Fix:** either `ui-work.md` adds the rating
   control to the trajectory row with a test, or the row becomes carried as a scope
   boundary under a record that says the trajectory has no rating.
2. `UP-DEBT-limit.packages.ssh.ssh.1` (agent A10). Upstream: "No Windows endpoint,
   automatic provisioning, reconnect or replay" (`packages/ssh/ssh/README.md:99`). The
   design gives a Windows *local* end, provisioning and reconnect. For a Windows *remote*
   host the only text is "a Windows remote is P2" in a table cell
   (`areas/sandbox-ssh.md:184`); no deliverable, test or scenario covers it. **Fix:**
   add the P2 deliverable with its proof, or record "the remote host is Linux or macOS"
   as a scope boundary in `DH-ADR-SANDBOX-8` and split the row's reason accordingly.

Extrapolation: see 2.4. A second sample of resolved rows, all at mechanism depth and
weighted to rows whose resolution lives in another area's specification, would narrow
the interval; both nominal rows are of that cross-area kind.

### Q3-3 (major) Three sampled platform limits are not facts that no design can remove

1. `UP-DEBT-limit.packages.subagent.subagent-claude-code.6` (K1). The justification
   calls "no human interaction path" a third-party fact. `libs/anthropic-ai-claude-agent-sdk.md:164`
   lists "no permission callback" as a tradeoff of the option the design chose ("own
   driver over the installed executable's print mode"), and line 46 shows the vendor's
   SDK has the callbacks (`canUseTool`, `onElicitation`, `onUserDialog`). The limit is a
   consequence of the design's choice. (From the auditor's general knowledge and not
   verifiable in this task: the vendor's command line also documents a flag that hands
   permission prompts to an MCP tool in non-interactive mode. Treat as a lead to check.)
   **Fix:** reclass as scope boundary under `DH-ADR-CLAUDEAGENTSDK-1`, or as unresolved
   until the prompt channel is checked.
2. `UP-DEBT-limit.packages.client.ui-primitives.8` (K2): overlong lines stay uncoloured
   because of the chosen grammar format and the pure-Go rule. That is a library and
   policy choice. **Fix:** reclass as scope boundary under the highlighting library
   decision.
3. `UP-DEBT-limit.packages.sandbox.sandbox.3` (K1): "no backend offers an unprivileged,
   typed report of a denied operation" is stated with no source. **Fix:** cite the
   source per backend, or mark the row as an assumption to verify.

More generally, by a crude pattern only 7 of the 110 platform-limit justifications carry
a path, a URL or a named document; the rest name the kind of fact in words. **Fix:** the
registers' platform tables gain a "source" column (vendor document, kernel or API
reference, or the upstream `path:line` that measured it).

### Q3-4 (major) "Every scope boundary is stated by a decision record" is not true of the records' own text

- **Claim.** `debt/carried-core.md:81` ("All 129 scope boundaries are now stated by a
  decision record") and `debt/carried-host-ui-experimental.md:104` ("Every one of the
  142").
- **Evidence.** For 9 of 50 sampled rows the linked record does not contain the
  boundary; it appears only in the row's justification or in the specification's debt
  table. Searched over the whole record text:

| Unit | Linked record | What the record lacks |
|---|---|---|
| `interaction.permission-presets.4` | `DH-ADR-HUMAN-6` | that `auto` cannot be a default |
| `subagent.subagent-acp.4` | `DH-ADR-SUBAGENT-9` | that only the final answer text is collected and the trace is withheld |
| `experimental.terminal-panel.4` | `DH-ADR-XHOOKS-8` | that a read of a person's terminal does not wait |
| `experimental.session-title-all-prompts-llm.2` | `DH-ADR-XMISC-6` | that prompts are not weighted |
| `client.ui-primitives.3` | `DH-ADR-9` | the fixed list of site marks (derived from a general rule) |
| `bundle.headless.8` | `DH-ADR-CLI-3` | "the result stream carries only the result" (it is in §6.2.3, not in the record) |
| `client.ui-directory-picker-browse.1` | `DH-ADR-HOST-8`, `DH-ADR-UISIDE-8` | no search, rename or delete |
| `client.ui-sidebar-documentpreview.1` | `DH-ADR-UISIDE-5` | that previews never edit |
| `experimental.hooks-codex.8` | `DH-ADR-XHOOKS-2` | that the other product's configuration layers and TOML form are not read |

- **Fix.** Add one sentence to each record ("Scope boundary of this record: ..."), as
  was done for `DH-ADR-CONNECT-2`, `DH-ADR-API-3` and `DH-ADR-HUMAN-13`; then re-run the
  check over all 271 rows (expect about 50 such rows). Until then the two register
  sentences should read "is linked to a decision record".

### Q3-5 (major) The inventory misses whole kinds, and four material items had no unit

- **Evidence.** Section 2.1. The rule in `inventory.go` reads one exact heading, only
  lines that start with `- `, only six scan roots, no test files, and a marker only when
  it directly follows a comment opener.
- **Consequence.** `SAFETY.md`, a required model counter-test, a browser-engine crash and
  a disabled live test had no unit; four more items were answered by the toolchain or
  tools specifications without a unit (now `Q3.4` to `Q3.7`).
- **Fix.** (a) Owners examine `Q3.1`, `Q3.2`, `Q3.3`, `Q3.8` and reclass or resolve
  them: `Q3.1` belongs in `design.md` §6 and the sandbox area as a limit with a decision
  about a security review; `Q3.2` needs a real-model scenario in `areas/sandbox-ssh.md`;
  `Q3.3` needs the model picker marked engine-sensitive in `areas/ui-chat.md` or a
  stated reason; `Q3.8` needs the zero-source result defined in
  `areas/mcp-lsp-web-connector.md` (`DH-D-CONNECT-8`). (b) If the inventory is run
  again: read prose paragraphs of the limitation section, add `SAFETY.md`,
  `docs/subsystems/*.md` "Limits" sections, root configuration files and `.github` to
  the scan, and list unconditional test skips. (c) `trace/README.md` should say that the
  "Deferred" sections of decision notes are covered per note only.

### Q3-6 (major) Nine of the 26 quick resolutions are partial or conditional but read as closed

Each has a real decision, deliverable and test; the row's disposition hides what is left.

| Unit | What is still true |
|---|---|
| `team.agent-team.2`, `subagent.subagent-claude-code.9`, `subagent.subagent-codex.9` | Isolation is `isolation: worktree`, **off by default**, Git only (`DH-ADR-SUBAGENT-12`, `DH-ADR-40`). In the default configuration members still share one checkout and a cancelled external run still leaves its edits. Changes to external systems are never restored |
| `session.session-log-deepseek.3` | Holds only if the vendor accepts an elision record (`DH-E-STORE-4`); the record is a default the owner has not confirmed |
| `web.web.2` | The bullet lists four deferred controls; one (`region`) is added. Recency, site filters and depth stay "when a backend honours them", which is upstream's position |
| `web.web-deepseek-api.1` | Localization is passed; the provider still ignores the site and ranking fields |
| `lsp.lsp.2` | Symbols and diagnostics are added at lower priority; edits and call hierarchy stay out |
| `web.web-fetch-http.2`, `web.web.3` | A PDF is stored and read through `office_render`, which makes page images (`areas/document-browser-computer.md:179`). There is still no text decoding, so a text-only model cannot read a fetched PDF |

- **Fix.** The registers already say most of this in their "Result per unit" column. Put
  it in the ledger: either a second row class ("resolved in part") or a split of each
  unit's reason into "removed" and "kept as scope under DH-ADR-x", so a count of
  resolved rows does not overstate. `areas/subagent-team.md` should state the default
  behaviour next to the option.

### Q3-7 (major) "Not applicable" is used for features dropped by decision, without a checked link

- **Evidence.** 11 of 41 sampled rows: `found.A25.10`, `found.A25.6`,
  `experimental.claude-code-mods.1`, `experimental.client-ui-forge.13`,
  `experimental.inspector.5`, `experimental.webworker-packer.1`,
  `experimental.webworker-runtime.1`, `.8`, `.15`, `.20`, `util.plugin-market-index.2`.
  `trace/README.md` defines not-applicable as "exists only because of JavaScript, Node,
  npm or the upstream build process". These limits vanish because the feature is not
  built. The outcome is honest; the label is not, and a not-applicable row needs no
  link, so the decision that drops the feature is named only in prose and the tool does
  not check it.
- **Scale.** About 65 of 245 (11 of 41; interval about 37 to 103).
- **Fix.** Either widen the definition in `trace/README.md` to "or belongs to a feature
  dropped by a linked decision" and require a `DH-ADR` link in that case, or move these
  rows to `resolved` with the link. Also reclass `session-query.session-log-export.6`
  as a platform limit.

### Q3-8 (major) Specification text that contradicts the design it now carries

- `areas/subagent-team.md:150` still says "isolation per member is deferred", against
  `DH-ADR-SUBAGENT-12` in the same file. `areas/ptc-workflow.md:126` still says of the
  process-tree limit that "nothing replaces it", against `DH-ADR-PTC-5`. The K1 register
  reported both (`debt/carried-core.md:91-96`); they are still there.
- **Fix.** Update the two §5 debt tables to the convergence-round design.

### Q3-9 (minor) Stale stage placeholder in 19 area specifications

- 19 files under `areas/` say "Carried rows name the placeholder stage `DH-S-0`" (for
  example `areas/ui-core.md:124`). No ledger row has that stage; carried rows name
  `DH-S-47`, `DH-S-59` or a build stage. **Fix:** replace the sentence with the real
  stage.

### Q3-10 (minor) Row reasons that describe a state the specification has left behind

- `UP-DEBT-limit.packages.experimental.client-ui-forge.6` says the fix "needs the
  session-views area ... (open question 1b)"; `areas/x-forge-worktree.md` §10 item 2 says
  it is settled. **Fix:** refresh the reason. A sweep of resolved rows for "open
  question" and "asks of" would find the rest.

### Q3-11 (minor) One boundary the register doubts is still counted as closed

- `UP-DEBT-limit.packages.host.open-in-app.2` (macOS detection by known paths only). The
  K1 justification itself says the stated reason "overlooks that macOS ships a
  command-line Spotlight query". **Fix:** decide it: add the query to `DH-ADR-HOST-9`,
  or keep the boundary with the real reason.

## 4. Checked and found correct

- The README-bullet inventory is exact: 1,746 of 1,746, per README, across all 389.
- All 12 patches, all 15 source `@deprecated` tags, all 4 baseline files and all 4
  postmortems are units.
- The 47 marker units are exactly what the stated rule finds; the 18 misses are outside
  the rule, not errors in applying it.
- In the 225 rows read, no reason misquotes upstream and no link points at an item on a
  different subject. Linked decisions are on topic in every case.
- All 26 named tests looked up for the convergence-round rows exist in their
  specifications, and 24 describe the behaviour the row claims (for example `DH-T-FS-8`,
  `DH-T-STORE-17`, `DH-T-CONNECT-23`, `DH-T-SUBAGENT-32`, `DH-T-CLI-12`). For
  `DH-T-UIKIT-12` and `DH-T-EXEC-17` the part of the test row I read does not show the
  claimed assertion; not checked further.
- The three unresolved rows are accurate against upstream
  (`packages/account/deepseek-account/src/protocol.ts:245` and the two README bullets),
  their stages match the plan's build steps, and the plan states that they lack a
  deliverable and a test.
- The two registers are candid: they separate "resolved" from "built", mark what the
  owner has not confirmed, list their own loose ends, and K1 flags boundaries "worth a
  second look".
- Agents did look beyond the mechanical inventory: 110 hand-added units cover
  `apps/desktop`, the publisher, `vendor/README.md`, the Python SDK and many source
  sites.

## 5. Limits of this audit

- A sample of 80 from 1,362 cannot bound a rare defect tightly. "About 17 to 70 nominal
  rows" is an estimate with wide intervals; it is not a count, and it does not say which
  rows.
- "Real" means a decision and a deliverable with a named test or scenario address the
  limit in the specification text. It does not mean the mechanism is sufficient, and 42
  of the 78 real verdicts rest on the linked item's title and opening text only.
- The classes of 309 carried rows were not read. The 9-of-50 and 3-of-25 rates are
  extrapolations.
- The completeness search used words and headings. A limitation that upstream states in
  ordinary prose, with none of the searched words, was not found by it.
- The four unresolved units added here were not examined with the owning areas; some
  will prove to be limits or boundaries.
- Nothing here is evidence that anything was built, run or tested. Nothing was.
