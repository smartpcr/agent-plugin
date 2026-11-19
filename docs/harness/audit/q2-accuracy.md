# Audit Q2: accuracy against upstream

Audit agent Q2, 2026-11-19. This report changes no other document. Nothing in it was
built, run or tested; the only commands run were file reads, searches, and
`go list -m` / `go mod download` for the module check.

## 1. What was audited, how, and what was not

**The question.** Are the statements the documents make about upstream true, and does
each disposition in the ledger lead to something in the Go design that covers the
upstream unit? The traceability tool proves that every one of the 3,669 units has a row
and that links resolve. It cannot prove that a row is right. This audit measures how
often rows are wrong, by sampling.

**Sources read.** The merged ledger `trace/ledger.jsonl`, the inventory
`trace/inventory/*.jsonl`, the specification each sampled row names (`areas/`, `libs/`,
`upstream/`, `design.md`), and upstream at `e:\work\github\ai\deepseek-harness`
(commit `2e39e704`, confirmed with `git log -1`). Upstream files were read as data only.

**The main sample: 172 disposition rows.** Rows were taken from `ledger.jsonl` in file
order, filtered by unit type, every Nth row from a stated offset (1-based row number
within the filtered list). The rule is repeatable with
`grep '"unit":"UP-<TYPE>' ledger.jsonl | awk 'NR>=OFFSET && (NR-OFFSET)%STEP==0'`.

| Type | Rows in ledger | Rule (offset, step) | Sampled |
|---|---|---|---|
| `UP-PKG` | 420 | 7, 15 | 28 |
| `UP-DEP` | 242 | 5, 12 | 20 |
| `UP-DEC` | 464 | 9, 16 | 29 |
| `UP-DEBT` `resolved` | 1,362 | 11, 68 | 20 |
| `UP-DEBT` `carried` | 384 | 13, 64 | 6 |
| `UP-DEBT` `not-applicable` | 245 | 8, 40 | 6 |
| `UP-TOOL` | 77 | 3, 6 | 13 |
| `UP-EVT` | 111 | 4, 9 | 12 |
| `UP-CFG` | 174 | 6, 14 | 13 |
| `UP-SVC` | 119 | 2, 12 | 10 |
| `UP-CLI` | 7 | all | 7 |
| `UP-PERSIST` | 64 | 1, 8 | 8 |
| **Total** | 3,669 | | **172** |

The sample holds rows from **52 different agents**, 9 `dropped` rows and 18
`not-applicable` rows. No row was replaced. The sampled units are listed in section 6.

**The three checks made on each row.**

1. *The unit is real and described correctly.* The package directory, dependency
   declaration, decision note, README limitation bullet, catalogue entry or source line
   was opened upstream and compared with the row's `reason` and with what the
   specification says about it. Specific claims in a reason (counts, option names,
   defaults, dispatch modes, line references) were checked where the reason made them.
2. *The disposition is justified.* The linked `DH-` item was read in its specification
   (heading, decision or implementation text, and the row of the specification's own
   unit table where one exists) to see whether it carries the unit's behaviour, says the
   unit is dropped, or contains the mechanism that removes a debt's cause.
3. *Nothing material is lost.* The upstream unit's own description (README summary,
   generated schema, help text) was compared with what the row and the specification
   carry or record as dropped.

**Three separate samples**, each with its own count: 40 upstream citations from 20
documents, 15 Go module choices, and 25 inventory lookups (13 workspace packages from
the upstream tree, 12 entries from upstream's generated catalogues). Their rules are in
sections 2.3 to 2.5.

**What was not audited.** The other 3,497 rows; the implementation plan and stage
ordering; whether the Go design is good; whether tests and scenarios prove their
deliverables; Go-side items that no sampled row links to. For check 3 the upstream
package's README, catalogue entry or note was read, not every source file of the
package, so a behaviour that upstream itself does not document could be missed.

## 2. Headline numbers

### 2.1 Main sample

| | Count |
|---|---|
| Rows sampled | 172 |
| Correct | 171 |
| Incorrect | 1 |
| Could not verify | 0 |
| **Mismatch rate** (incorrect / (correct + incorrect)) | **0.6%** (1 of 172) |

The rate is below 5%, so no second sample was drawn.

A stricter count is given for transparency. Three further rows pass all three checks
(the unit is real, the disposition is right, nothing is lost) but their `reason` says
something about the Go design that the specifications contradict or do not contain
(one row in Q2-2, two in Q2-4). Counting those as wrong as well gives 4 of 172,
**2.3%**, still below 5%.

**By unit type.**

| Type | Sampled | Correct | Incorrect | Correct, but the reason disagrees with a specification |
|---|---|---|---|---|
| `UP-PKG` | 28 | 28 | 0 | 1 (session-checkpoint-policy) |
| `UP-DEP` | 20 | 20 | 0 | 0 |
| `UP-DEC` | 29 | 29 | 0 | 0 |
| `UP-DEBT` (20 resolved, 6 carried, 6 not-applicable) | 32 | 32 | 0 | 2 (user-approval.4, session-inspector.5) |
| `UP-TOOL` | 13 | 12 | 1 | 0 |
| `UP-EVT` | 12 | 12 | 0 | 0 |
| `UP-CFG` | 13 | 13 | 0 | 0 |
| `UP-SVC` | 10 | 10 | 0 | 0 |
| `UP-CLI` | 7 | 7 | 0 | 0 |
| `UP-PERSIST` | 8 | 8 | 0 | 0 |

**By which check failed.** Check 1 (unit real and described correctly): 0. Check 2
(disposition justified): 0. Check 3 (nothing material lost): 1, the `session_search`
tool (Q2-1).

**By agent.** No agent stands out on numbers this small, with one note: both sampled
rows written by agent X3 are flagged (the one incorrect row, and one reason that states
a mechanism its specification does not contain). X3 wrote 63 rows; two rows are too few
to call a pattern, but a re-read of X3's tool and debt rows is cheap. The four sibling
session-query tools were compared argument by argument as a follow-up and match.

### 2.2 What the rate means

With 1 failure in 172 rows drawn across all types, the sample is consistent with a
true error rate of well under 5%; it is not evidence that the rate is zero. The
strata are small (7 to 32 rows), so a per-type rate cannot be stated.

### 2.3 Citations: 40 checked

Rule: 20 documents. Areas: every 4th file of `areas\` in name order from the 2nd
(`api`, `bundle-preset`, `execution`, `interaction-credentials`, `ptc-workflow`,
`session-views`, `ui-chat`, `ui-settings-plugins`, `x-cua-browser-voice`,
`x-liveshare-webworker`). Libraries: every 15th file of `libs\` in name order from the
5th (`anthropic-aisdk`, `cosmokit`, `exceljs`, `lexical`, `node-process-builtins`,
`react`, `trycuacua-driver`). Upstream: `decisions-1`, `security`, `wire-and-api`. In
each, the first `path:line` citation at or after one third of the file and the first at
or after two thirds; where a document gave fewer than two that way, the first citation
at or after line 50, then the next one, until 40 were reached.

| | Count |
|---|---|
| File exists and the cited line says what the sentence claims | 38 |
| Claim true and in the cited file, but the line is 17 lines off | 1 (`areas/api.md:386`) |
| The cited file does not say it at all | 1 (`areas/x-cua-browser-voice.md:273`, Q2-3) |

### 2.4 Go modules: 15 checked

Rule: the distinct modules of `libs\README.md` in first-appearance order (41 of them),
every 3rd from the 1st (14 modules), plus the last one, the only MPL-licensed choice.
Each was checked with `go list -m -json <module>@<version>` and
`go mod download -json`, then the LICENSE file in the module cache was read. The
checksum database and proxy were at their defaults (`GOSUMDB=sum.golang.org`,
`GOPROXY=https://proxy.golang.org,direct`, `GOFLAGS` empty). No command was blocked.

| Module and version | Licence stated | Result |
|---|---|---|
| `github.com/expr-lang/expr` v1.17.8 | MIT | version exists; MIT |
| `github.com/gohugoio/hugo` v0.170.1 | Apache-2.0 | version exists; Apache-2.0 |
| `honnef.co/go/tools` v0.8.2 | MIT | version exists; MIT text |
| `github.com/goreleaser/goreleaser/v2` v2.20.2 | MIT | version exists; MIT |
| `github.com/spf13/pflag` v1.1.0 | BSD-3-Clause | version exists; three-clause BSD |
| `github.com/Masterminds/semver/v3` v3.5.0 | MIT | version exists; MIT text |
| `golang.org/x/sys` v0.50.0 | BSD-3-Clause | version exists; three-clause BSD |
| `github.com/landlock-lsm/go-landlock` v0.11.2 | MIT | version exists; MIT |
| `github.com/fsnotify/fsnotify` v1.10.1 | BSD-3-Clause | version exists; three-clause BSD |
| `golang.org/x/sync` v0.25.0 | BSD-3-Clause | version exists; three-clause BSD |
| `github.com/JohannesKaufmann/html-to-markdown/v2` v2.5.2 | MIT | version exists; MIT |
| `github.com/microcosm-cc/bluemonday` v1.0.27 | BSD-3-Clause | version exists; three-clause BSD |
| `github.com/gohugoio/hugo-goldmark-extensions/passthrough` v0.6.0 | Apache-2.0 | version exists; Apache-2.0 |
| `github.com/zalando/go-keyring` v0.2.8 | MIT | version exists; MIT |
| `github.com/hashicorp/yamux` v0.1.2 | MPL-2.0 | version exists; MPL 2.0 |

**15 of 15 confirmed**, version and licence. Only the module's top-level licence file was
read; licences of a module's own dependencies were not.

### 2.5 The inventory: 25 lookups

- **Workspace packages, 13 of 13 have a `UP-PKG` unit.** Rule: every directory matching
  `packages/*/*`, `apps/*` or `vendor/*` that holds a `package.json` in the upstream
  tree (406), sorted, every 32nd from the 11th: `packages/api/job-controller`,
  `client/script-realm-iframe`, `client/ui-primitives`, `client/ui-workspace`,
  `deliverables/deliverable-file-references`, `experimental/forge`,
  `experimental/translator`, `host/directory-picker-native`, `preset/agent-preset`,
  `session/session-title-first-prompt-llm`, `storage/storage`, `typert/protocol`,
  `workflow/workflow`. Each also has a disposition row.
- **Catalogue entries, 12 of 12 have a unit.** Tools (`docs/tool-catalog.md`, every 20th
  `###` heading from the 5th): `read_mcp_resource`, `apply_patch`, `schedule_list`,
  `job_list`. Events (`docs/event-producer-consumer.md`, every 30th table row from the
  10th): `agent/pre-step`, `fs/delete-intent`, `subagent/provider-added`,
  `worktree/cleanup`. Config schemas (`docs/config-catalog.md`, every 50th `##` heading
  from the 20th): `dsh-client-connection`, `dsh-host-directory-picker-browse`, `dsh-ssh`,
  `dsh-web-search-deepseek`.
- **Totals agree.** The catalogues hold 77 tool headings, 111 event rows and 174 config
  headings; the inventory holds 77, 111 and 174 units.
- One observation outside the sample is recorded as Q2-8.

## 3. Findings

Counts by severity: blocker 0, major 3, minor 5.

### Q2-1 (major) `session_search` loses four upstream arguments and no document says so

- **Where.** Ledger row `UP-TOOL-dsh-tool-session-query.session_search` (agent X3,
  `ported`); `areas/x-inspector-timeline.md:193`.
- **What the documents say.** The row: the tool is exposed "changed: ... adds a mode
  argument ... and a cursor ...; the availability filter is dropped". The specification
  lists the Go arguments `query`, `mode`, `session_ids`, `created_from`, `created_to`,
  `parent_session_ids`, `include_root_sessions`, `event_types`, `surfaces`, `cursor` and
  summarises the unit as "changed: cursor and mode added".
- **What upstream has.** `docs/tool-catalog.md:2048-2140`: `query`, `session_ids`,
  `created_at_from`, `created_at_to`, `parent_session_ids`, `include_root_sessions`,
  `availability` (`:2086`), `event_seq_from` (`:2097`), `event_seq_to` (`:2101`),
  `event_time_from` (`:2105`), `event_time_to` (`:2109`), `event_types`,
  `event_surfaces`.
- **The defect.** Four filters (`event_seq_from`, `event_seq_to`, `event_time_from`,
  `event_time_to`) are absent from the Go tool and recorded nowhere as dropped; three
  arguments are renamed (`created_at_*` to `created_*`, `event_surfaces` to `surfaces`)
  without a note. The model can no longer restrict a cross-session search to a time
  window of events, only to a window of session creation. Check 3 fails.
- **Fix.** In `areas/x-inspector-timeline.md` section "Tools exposed to the model",
  either add `event_time_from`/`event_time_to` (and the sequence pair) to
  `session_search`, or state that they are dropped and why, and list the renames. Update
  the ledger row's `reason` to match. The four sibling tools (`session_event_search`,
  `session_trace`, `session_event_trace`, `session_event_read`) were compared and carry
  every upstream argument.

### Q2-2 (major) Two documents disagree on whether `dsh headless` can answer an approval

- **Where.** Ledger row `UP-DEBT-limit.packages.interaction.user-approval.4` (agent A17,
  `resolved`); scenario `DH-E-HUMAN-2` in `areas/interaction-credentials.md:538-539`;
  against `DH-ADR-CLI-7` in `areas/app-cli.md:360` and ledger row `UP-CLI-dsh.headless`
  (agent A24).
- **What they say.** A17's row: "the headless profile mounts none [no answerer] on
  purpose, so the answer there is no by design". `DH-E-HUMAN-2` step 3: "The same
  session is run with the `headless` profile" then "the outcome is `unavailable` and the
  tool body never ran". `DH-ADR-CLI-7`: "`cli.headless` registers the one `cli` answerer
  ... when a controlling terminal can be opened and `--json` is not set", and the
  prompt offers allow once, deny, or allow with a session grant.
  `interaction-credentials.md:27` and `:214` themselves name the `cli` channel.
- **The defect.** The debt disposition is still right (the upstream limit, "no built-in
  answerer", is removed), so the row is counted correct. But its reason is false for a
  run on a terminal, and the scenario as written passes or fails depending on whether
  the tester has a terminal attached.
- **Fix.** Reword the ledger reason: "each interactive profile mounts the answerer of
  its carrier; `dsh headless` asks on a controlling terminal through the `cli` answerer
  and refuses when there is none or `--json` is set (DH-ADR-CLI-7)". In `DH-E-HUMAN-2`
  step 3 state the precondition: run with `--json`, or with no controlling terminal.

### Q2-3 (major) A citation attributes to upstream a statement the cited file does not contain

- **Where.** `areas/x-cua-browser-voice.md:273`, tradeoff of `DH-ADR-XCUA-5`.
- **What it says.** "Browsers give the microphone only to a secure context, so a host
  reached over plain HTTP on a network address cannot record
  (`client-ui-voice-input/README.md:28`)."
- **What upstream says.** `packages/experimental/client-ui-voice-input/README.md:28`
  describes the set-up prompt, the guidance dialog and the click-to-record flow. Neither
  that line nor any other line of the README mentions a secure context, HTTPS or a
  loopback exception (searched for `secure`, `insecure`, `getUserMedia`, `plain HTTP`,
  `loopback`: no match).
- **The defect.** The statement is general browser behaviour, not an upstream fact read
  at that line. The writing rule requires such a sentence to open with "Inference".
- **Fix.** Remove the citation and mark the sentence as inference, or cite the browser
  platform rule as an outside reference. One of 40 citations failed this way, so a
  wider pass over citations in tradeoff paragraphs is worth an hour.

### Q2-4 (minor) Ledger reasons that disagree with, or go beyond, the specification they name

The disposition and links of each row are right; the wording of the reason is not.

| Row | Reason says | Specification says |
|---|---|---|
| `UP-PKG-packages.session.session-checkpoint-policy` (A05) | the checkpoints are "kept as a listener-only Go component over the store's flush barrier" | `areas/session-store.md`, `DH-D-STORE-4`: "`checkpoint.Policy` is called by the owners of three points, never attached as an optional listener" (and `areas/agent-loop.md:216`, `areas/tools.md:203` call it directly) |
| `UP-DEBT-limit.packages.experimental.session-inspector.5` (X3, `not-applicable`) | "The Go page offers the registered event types as a static list in a plain form field" | `areas/x-inspector-timeline.md:164` says only "filters by type and class"; no list of types in a form field is specified. Line 99 gives the real ground: the limit is React keyboard state |

A third case is wording only and is not counted: the row `UP-CFG-dsh-client-connection`
(C1) says "`maxRequestBodyBytes` has no counterpart" and then names A19's form bound,
while `areas/ui-core.md:321` says it "is A19's `web.Config.MaxFormBytes` and the
attachment limit". Same substance, opposite phrasing.

**Fix.** Edit the reasons to repeat the specification. For the checkpoint row:
"kept as `checkpoint.Policy`, called by the driver and the tool pipeline at the three
points, failing closed". For the inspector row: "the limit is the keyboard state of a
React suggestion list; the Go page has no client-side list state (DH-ADR-3)".

### Q2-5 (minor) `areas/ui-work.md` still files a resolved limit under "kept on purpose"

- **Where.** `areas/ui-work.md:122`, debt group F: "**Limits kept on purpose.** No
  tree-wide message timeline, ... `L:ui-agent-team.1`, `.2`, ... (5) | Carried, each
  narrowed", while the same cell then says the tree-wide timeline is built
  (`DH-D-UIWORK-5`, register entry U-K2-6), and the ledger row
  `UP-DEBT-limit.packages.client.ui-agent-team.1` (K2) is `resolved`.
- A second leftover in `DH-D-UIWORK-5` ("How it is implemented"): the timeline "reads
  area A06's bounded tree query when that exists; until then it reads each member's
  log", while `areas/session-views.md:259` says of `Query.TreeMessages` "this is the
  design, not a fallback".
- **Fix.** In group F drop "No tree-wide message timeline" and `L:ui-agent-team.1`, make
  the count 4, and list `.1` with the resolved limits. In `DH-D-UIWORK-5` delete the
  "until then" fallback sentence.

### Q2-6 (minor) `resolved` also covers limits that disappear because the feature is not built

- **Where.** Sampled row `UP-DEBT-limit.packages.experimental.forge-gitlab.5` (X1,
  `resolved`: "Comments and draft notes are not read or shown"). The specifications use
  the same convention openly: `areas/x-forge-worktree.md:104` ("Limits of surfaces that
  are not built (17: 14 resolved, 3 not applicable)") and `areas/boot.md:125` ("The
  plugin market (12) ... Resolved by not building it").
- **The defect.** Each such row is defensible, because the linked decision
  (`DH-ADR-XFORGE-5`, `DH-ADR-BOOT-11`) records the drop. But the headline "1,362 debt
  items resolved" reads as "1,362 limitations fixed", and some part of it is "the
  feature that had the limitation was dropped". One of 20 sampled `resolved` rows is of
  this kind; the two groups named above are 26 rows on their own.
- **Fix.** In `traceability.md` (or the debt summary in `design.md`) split the resolved
  count into "cause removed by a mechanism" and "feature not built, see the dropping
  decision", or add a sentence saying the count includes both.

### Q2-7 (minor) Two citations are loose

- `areas/api.md:386` cites `packages/api/session-controller/src/continuation.ts:27` for
  "a fixed list of six failure codes". The list is at `:9-10` (six codes, as claimed);
  line 27 is the projection definition. **Fix:** cite `:9-10`.
- `libs/exceljs.md:106` puts "saved values, approximate formatting" in quotation marks
  "as upstream does (`README.md:89`)". Upstream's line 89 says that column-width and
  theme-tint approximation "can affect fidelity"; the quoted words are not upstream's.
  **Fix:** drop the quotation marks or quote upstream's own words.

### Q2-8 (minor) Four workspace packages have no unit of their own

`pnpm-workspace.yaml:7` includes `native/system/packages/*`, which holds five packages
(`darwin-arm64`, `darwin-x64`, `entry`, `linux-arm64`, `linux-x64`). Only `entry` is a
lockfile importer, so only `UP-PKG-native.system` and
`UP-PKG-native.system.packages.entry` exist. The four platform packages are covered in
words by the reason of `UP-PKG-native.system` ("the platform packages ... are not
carried"). Nothing is lost; **fix** is one sentence in `trace/README.md` under `UP-PKG`
saying that workspace members that are not lockfile importers are covered by their
parent unit.

## 4. What was checked and found correct

- **Packages (28).** Every directory exists at the pinned commit and its README summary
  matches the row. Specific claims held: the SDK application's patch disables the title
  model call and carries office-kit and client rows (`packages/bundle/sdk-app/cordis.patch.yml:8-62`);
  the Stagehand provider runs a second model inside a browser extension and owns its
  Chromium and profile (`.../browser-use-stagehand-native/README.md:45`, `:90`); the
  storage hub "performs no IO" (`packages/storage/storage/src/index.ts:3`); `cosmokit`
  exports 22 names used upstream, 6 of them the volatile reference.
- **Dependencies (20).** Each is declared where the library document says. Checked
  counts held: 249 files import Playwright; `rrule.ts` is 689 lines; `supports-color` is
  used once at `vendor/logger-console/src/index.ts:23`; `cytoscape` is declared by the
  website with no import; one HMAC and no cipher or key derivation in product source.
- **Decision notes (29).** Every note exists under the stated status and category, and
  says what the row says it decides. Spot facts held: descriptor 7 for the control pipe,
  five voice packages and the rejection of browser-native recognition, eight build
  speed-ups, the 500 ms hold and 200 ms fade, undo reachable only through internal
  methods.
- **Tech debt (32).** Every limitation bullet, patch, deprecation and agent-found item
  exists with the stated text. For each `resolved` row the mechanism is written in the
  specification (for example recursive glob sampling in `DH-D-FS-3`, `session/close` in
  `DH-D-SDK-4`, the calibration factor in `DH-ADR-LLM-9`, the three OAR patch fixes as
  requirements). Each `carried` row restates the upstream limit accurately and has a
  justification and a stage. Each `not-applicable` row concerns pnpm, TypeScript types,
  the JavaScript guest, the bundled payload or React state.
- **Tools (12 of 13).** Names and argument lists match `docs/tool-catalog.md`
  (`team_task_update` has the eight actions; `job_output`, `schedule_view`,
  `send_message`, `terminal_open`, `web_search`, `glob`, `write`, `exit_worktree`,
  persistent `pwsh`). `exit_worktree` upstream does return to the source checkout's root
  (`packages/experimental/worktree/src/index.ts:279`), as the row says.
- **Events (12).** Dispatch modes match upstream's matrix (`agent/created` serial,
  `llm/stream` waterfall, `forge/providers-changed` parallel), and each Go key is in the
  owning specification's event table.
- **Config schemas (13).** Field names and defaults match `docs/config-catalog.md`
  (for example `compressionLevel` 0 to 9 default 6; `cleanupPeriodDays` 30;
  `waitTimeoutMs` 30 s, `maxWaitTimeoutMs` 10 min, `inlineOutputBytes` 2048).
- **Services (10), commands (7), persisted events (8).** Each exists upstream as
  described. `dsh web` has exactly seven options; `dsh headless` has the five named;
  `schedule/change` is inert upstream (`packages/schedule/schedule/README.md:179`). For
  each persisted type marked kept, the owning area declares the Go type.
- **Citations, modules, inventory.** As counted in sections 2.3 to 2.5.

## 5. Limits of this audit

- A sample of 172 of 3,669 rows (4.7%) estimates a rate; it does not clear the other
  rows. Per-type strata are too small to rank types or agents.
- "Correct" means the three checks passed at the depth stated in section 1. Check 2 read
  the linked item's own text, not every deliverable it depends on. Check 3 relied on
  what upstream documents about the unit.
- The audit says nothing about whether the Go design can be built as written, whether
  the plan's stages are ordered soundly, or whether tests prove what they claim.
- The module check confirms that a version exists and what its top-level licence file
  says on 2026-11-19. It does not check transitive licences, maintenance or fitness.
- A row judged correct can still carry a reason with loose wording; only contradictions
  with a specification were recorded (Q2-2, Q2-4).

## 6. The sampled rows

Verdict is "correct" unless marked. `[R]` marks a correct row whose reason disagrees
with a specification (Q2-2, Q2-4). `[X]` marks the incorrect row (Q2-1).

**UP-PKG (28).** `apps.plugin-market-generator`, `packages.api.terminal-controller`,
`bundle.sdk-app`, `client.ui-account-notifications`, `client.ui-entry-customization`,
`client.ui-reference`, `client.ui-settings-web-search`, `client.ui-workflow-run`,
`context.file-reference-local`, `credentials.authorization`,
`experimental.browser-use-stagehand-native`, `experimental.cua-bundle`,
`experimental.liveshare`, `experimental.terminal-panel`,
`extensions.cordis-host-runner`, `goal.goal-round-driver`,
`identity.anonymous-user-id`, `llm.llm-retry`, `ptc-runtime.ptc-runtime-node`,
`session.session-checkpoint-policy` `[R]`, `session.session-telemetry-otel`,
`shell.tool-pwsh`, `storage.storage`, `subprocess.win32-process`, `typert.generator`,
`util.lazy-require`, `web.web-deepseek-api`, `vendor.cosmokit`.

**UP-DEP (20).** `@botiverse/oar`, `@jridgewell/gen-mapping`,
`@modelcontextprotocol/server-everything`, `@panzoom/panzoom`, `@types/compression`,
`@types/react`, `@vscode/ripgrep`, `app-builder-lib`, `cytoscape`, `eslint-scope`,
`http-server`, `mdast-util-from-markdown`, `micromark-util-symbol`, `node:crypto`,
`node:path`, `node:url`, `playwright`, `rrule`, `supports-color`, `vitepress`.

**UP-DEC (29).** implemented/architecture: `2026-07-02-tool-render-intent-union`,
`2026-07-28-api-browser-trust-boundary`, `2026-08-10-session-log-version-mechanism`,
`2026-08-28-ctx-remote-failure-vocabulary`,
`2026-09-08-desktop-bundled-runtime-and-external-plugins`,
`2026-09-11-subprocess-control-pipe`, `2026-09-16-experimental-voice-input`,
`2026-09-19-profile-resolution-lookup-order`,
`2026-09-23-desktop-close-to-background-and-quit-confirmation`,
`2026-10-01-replace-changed-package-modules-on-recomposition`,
`2026-10-10-user-text-json-nodes`, `2026-10-16-steering-batch-start-in-definition-state`,
`2026-10-21-session-history-stream-and-snapshot`, `2026-10-31-native-office-tool`,
`2026-11-09-explicit-ptc-output`. implemented/bug-fix:
`2026-09-30-session-list-host-fairness`. implemented/feature:
`2026-07-12-subagent-persona-tool-filter-and-depth`,
`2026-09-04-right-sidebar-docking-infrastructure`,
`2026-09-22-archive-admission-reads-stored-tasks`, `2026-10-19-sdk-native-user-questions`,
`2026-11-17-sidebar-scrollbar-fade-in-css`. implemented/process:
`2026-09-09-parallel-macos-notarization`, `2026-10-11-lossless-build-speedups`.
implemented/simplification: `2026-09-11-remove-e2b-providers`. implemented/testing:
`2026-09-06-backend-continuation-performance`. proposed:
`architecture.2026-07-29-durable-last-activity-index`,
`feature.2026-07-06-recallable-compaction`,
`process.2026-08-20-audience-first-documentation-quality`. rejected:
`architecture.2026-06-16-typed-event-schemas`.

**UP-DEBT resolved (20).** `deprecated.packages/core/session/src/index.ts:740`,
`found.L3.3`, `limit...attachment.attachment-local.3`, `client.ui-agent-team.1`,
`client.ui-plugin-manager.4`, `client.ui-sidebar-right.1`, `connector.notion.6`,
`deliverables.tool-present.1`, `experimental.client-ui-forge.9`,
`experimental.forge-gitlab.5`, `experimental.tool-subagent-oar.2`,
`fs.tool-fs-search.5`, `interaction.user-approval.4` `[R]`, `llm.token-meter.4`,
`sdk.server.1`, `session.session-projection.6`, `skill.tool-skill.3`,
`subprocess.subprocess.1`, `web.web-deepseek-api.4`, `patch.@botiverse__oar@0.67.0`.

**UP-DEBT carried (6).** `api.terminal-controller.2`, `client.ui-sidebar-files.7`,
`experimental.client-ui-forge.8`, `experimental.terminal-panel.3`, `plan.plan-mode.4`,
`subagent.subagent-dsh-sdk.4`.

**UP-DEBT not-applicable (6).** `found.A25.5`, `boot.plugin-manager.3`,
`client.ui-slots.2`, `experimental.session-inspector.5` `[R]`,
`ptc-runtime.ptc-runtime-node.13`, `typert.loader.3`.

**UP-TOOL (13).** `stagehand_navigate`, `exit_worktree`, `plugin_market`,
`team_task_update`, `glob`, `write`, `job_output`, `pwsh` (persistent), `schedule_view`,
`session_search` `[X]`, `send_message`, `terminal_open`, `web_search`.

**UP-EVT (12).** `agent/created`, `agent/status`, `authorization/settled`,
`cordis/request-run`, `forge/providers-changed`, `hmr/reload`, `llm/stream`,
`plugin-manager/update-state`, `settings/document-updated`, `theme/change`,
`web/providers-changed`, `workflow/log`.

**UP-CFG (13).** `dsh-agent-preset-registry`, `dsh-client-connection`,
`dsh-cordis-host-runner`, `dsh-experimental-computer-use-cua-driver-mcp`,
`dsh-fs-sandbox`, `dsh-linear`, `dsh-plan-mode`, `dsh-session-log-export`,
`dsh-spill-local`, `dsh-terminal-bash`, `dsh-tool-jobs`, `dsh-tool-web`,
`dsh-workspace-changes`.

**UP-SVC (10).** `agentDefaultModel`, `commands`, `cuaPrograms`, `forgeSession`,
`mcpClientHost`, `productAnalytics`, `sessionPersistence`, `shell`, `systemPrompt`,
`webApp`.

**UP-CLI (7).** `dsh`, `dsh.acp`, `dsh.headless`, `dsh.plugin`, `dsh.sdk`,
`dsh.sdk-minimal`, `dsh.web`.

**UP-PERSIST (8).** `agent-preset/selected`, `command/run`, `feedback/message-put`,
`llm/retry`, `schedule/change`, `subagent/descriptor`, `todo/write`, `tool/result`.
