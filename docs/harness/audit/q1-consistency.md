# Audit Q1: consistency and documentation rules

> **Status: audit report, 2026-11-19.** Written by audit agent Q1, which wrote none of
> the documents it judges. Nothing was built, run or tested for this audit. It changes
> no document; every finding names the document that needs the fix.

## 1. What was audited, how, and what was not

**The question.** Do the design documents agree with each other and with the owner's
decisions, and do they follow the owner's documentation rules: outcomes (A), decisions
and deliverables (B) and proof (C) linked by stable IDs with no orphan; plain English
with terms defined before use; five parts per decision; six fields per deliverable;
stages of at most 20 steps with five labelled parts; real-environment scenarios written
Given / When / Then / Cleanup and kept apart from local tests; no claim that anything
ran; a skipped scenario never a pass.

**What was read in full.** `design.md` sections 1 to 4, 7.1, 9, 10 and 11;
`impl.md` sections 1 to 5 and 7 to 10 and sixteen stages (1, 2, 6, 7, 8,
9, 16, 17, 18, 23, 24, 27, 28, 46, 47, 48; also 29, 34 and 59 where a finding led
there); the owner's decisions; the five integration question files `I1` to `I5`; both
debt registers' summaries and their sections on unresolved entries and boundaries.

**What was checked over every item, by read-only queries** (`grep` and `awk` printing to
the terminal; the queries are repeatable):

- all 363 deliverable rows of `trace/ledger.jsonl` against the plan's steps (one build
  stage each, priority, dependency order);
- all 361 anchored deliverable blocks for the six field labels, and their "Depends on"
  text against the ledger's `deps`;
- all 575 anchored decision records for the five labelled parts;
- all 59 stages for step count and the labelled parts; every `#dh-` link in the plan
  for a matching anchor;
- the "Related areas" line of every file in `libs\`;
- about 35 search patterns for leftovers of overruled designs and for wording that
  implies something was run.

**Samples, and how they were drawn.**

- *Deliverables (40):* the ledger's deliverable IDs sorted in natural order, every 9th
  starting at the 5th. Each block was read beside its ledger row.
- *Decisions (43):* the anchored `DH-ADR-` IDs of `areas\` and `libs\` sorted, every
  12th starting at the 7th.
- *Scenarios (46):* 23 of the plan's 25 guarded scenarios (all but `DH-E-XFORGE-1`
  and `-4`); then every 12th of the sorted `DH-E-` IDs starting at the 6th (20, one of
  them already read); plus four chosen because their titles speak of deletion,
  rollback or a shared store (`DH-E-STORAGE-5`, `DH-E-HUMAN-3`, `DH-E-EXT-3`,
  `DH-E-STORE-2`). The last four are a targeted pick, not a random one.
- *Interfaces (14):* named in section 3, check 6.

**What was not done.** No upstream `path:line` citation was verified (that is audit
Q2's question). The 39 area specifications were not read end to end; about 62 KB of
each was not seen. `libs\README.md`, the `trace\matrix-*.md` files and the fragments
were not audited. The traceability tool was not re-run. "First verdict" cells in
`upstream\decisions-1.md` to `-4.md` were not compared row by row with the ledger.

**One deviation from the audit rules, disclosed.** One query wrote its output to a
temporary file in the scratchpad folder (`q1-deps.txt`) instead of the terminal. The
file was removed in the next command. No project file was touched.

## 2. Headline numbers

| Check | Items examined | Defective | Note |
|---|---|---|---|
| 1a. Outcomes to stages | 16 outcomes | 2 | Counts in the plan's table equal the ledger for all 16. `DH-O-15` and `DH-O-16` have their main implementation in steps with no deliverable ID |
| 1b. Deliverables, every one | 363 | 0 scheduling defects | Each is in exactly one build stage; 29 priority mismatches all labelled "pulled forward"; 20 late dependencies all labelled "held open" |
| 1c. Deliverable blocks, every one | 361 blocks | 313 do not name their outcome; 10 lack "Components"; 21 have a ledger dependency the prose omits; 34 state two or three priorities | `DH-D-LLM-7` to `-10` share one block |
| 1d. Deliverables, sample | 40 | 22 with at least one defect | 19 completion lines weaker than "passed"; 4 mixed priority; 2 completion lines omit a ledger proof; 2 dependency mismatches; 1 without components |
| 2. Owner decisions | 40 records, about 25 leftover patterns | 3 records contradicted (`DH-ADR-18`, `-21`, `-28`) in 4 documents | No leftover found for `launch.mint`, pairing, "Provisional, owner to confirm", the off-switch, `apply_patch`, a second `dsh web`, grants, or an embedded interpreter |
| 3. `design.md` section 9.2 | 61 rows against 48 integration questions and both registers | 1 row states the wrong default; 1 row overrides an owner decision; 6 rows cite register sections that were renumbered; 5 defaults marked in specifications are not listed | All 48 integration questions are accounted for |
| 4a. Decisions | 575 mechanically; 43 read | 44 lack a labelled part (38 foundation records have no "Serves"); 0 of the 43 read | |
| 4b. Scenarios | 46 read | 0 incomplete, 0 implying a run; 6 with a title or ID blemish | 230 of 235 have an anchor |
| 5. Plan | 59 stages mechanically; 16 read | 0 on step count or parts; 5 of 46 scenarios read cannot run in the stage they are indexed to; 3 scenarios that specifications place in the build stage are not guarded | |
| 6. Contradictions | 5 known, 14 interfaces | 4 of the 5 confirmed as defects; 3 of 14 interfaces described differently | |
| 7. "Related areas" lines | 109 library files | 1 damaged; 7 stale or wrong; 7 use another label | No lost separator or dangling bracket remains |
| 8. Overclaiming and status | all documents, by search | 10 library files report programs that were compiled and run; 3 status statements are stale | No document claims a harness test or scenario passed |
| 9. Readability | `design.md` 1 to 4, plan 1 to 3 | 13 terms used before definition; area keys have no legend | |

**Findings by severity:** 1 blocker, 13 major, 9 minor.

## 3. Findings

### Blocker

#### Q1-1 The plan places scenarios by their titles, and at least five cannot run where it puts them; two of them make stages wait for each other

- **Evidence.** The plan says how it placed scenarios: "this plan classified them from
  their titles" (section 7.4) and "Inference: judged from the scenario titles; the
  scenario's Given section governs" (section 10.4, row S-2). The Given sections were
  read for 46 scenarios. Five need something that is built in a later stage:

  | Scenario | Stage in the plan | What its Given or When needs | Where that is built |
  |---|---|---|---|
  | `DH-E-SUBAGENT-7` (`areas/subagent-team.md`) | 29, step 29.8, guarded | "the worktree entry mounted" and an external target, "Codex, installed and signed in" | `DH-D-XFORGE-1`, `-2` in stage 54; external children in stage 52 |
  | `DH-E-BOOT-3` (`areas/boot.md`) | 34, step 34.9, guarded | step 8: the model calls `plugin_manager` with `attach` | `DH-D-EXT-7` in stage 52 (the plan itself holds this part open as H-13) |
  | `DH-E-STORAGE-1` (`areas/storage.md`) | 16, step 16.8, guarded | "In A, add a workspace through the HTTP surface" of a web host | `DH-D-API-6` in stage 19 |
  | `DH-E-BUNDLE-3` (`areas/bundle-preset.md`) | 17, step 17.10 | "Start each of the five profiles", with a real ACP client for `acp` | the `acp` launcher closes at step 37.7; the `web` profile is assembled in stage 21 |
  | `DH-E-STORAGE-3` (`areas/storage.md`) | 17, step 17.8 | "One run each on Linux, macOS and Windows with the real sandbox backend" | macOS and Windows backends in stage 27 |

  Stage 52 lists stages 29 and 34 among its entry stages, and a build stage "may start
  when every stage it depends on is built (its exit evidence is recorded)" (plan
  section 1.4, rule 1). Stages 29 and 34 cannot record their exit evidence without
  steps 29.8 and 34.9. So 29 and 34 wait for 52, and 52 waits for 29 and 34. Stage 17
  cannot close before stages 27 and 37, so milestone M2 cannot be reached in the order
  the plan gives. The plan's own repeatable check ("for every `deps` edge the
  dependency's stage is not later") does not see this, because these needs are in
  scenario text and in "at run time" prose, not in `deps`.
- **Likely also affected, not confirmed.** `DH-E-SANDBOX-2` in stage 18 asserts Windows
  preparation and journal behaviour that belongs to `DH-D-SANDBOX-6` (stage 27).
  `DH-E-BOOT-2` in stage 16 changes a probe "through the configuration command", and
  the `dsh config` commands are part of `DH-D-CLI-7` (stage 38).
- **Fix.** `impl.md`, sections 6, 7.4, 7.5 and 10.4: read the Given and
  When of all 235 scenarios and re-index each to the first stage where everything it
  names exists. Where a scenario proves parts of different priority, the owning
  specification should split it (for example `DH-E-BOOT-3` without step 8, and a second
  scenario for attach; `DH-E-SUBAGENT-7` moved to the stage that builds the worktree
  seam, with the isolation option given its own deliverable ID). Then add to the
  ledger a `needs` field on scenario rows, so the tool can check the order.

### Major

#### Q1-2 Three scenarios that their specifications place in the build stage are not guarded, and the takeover scenario is deferred past the milestone that enables the feature

- **Evidence.** The plan says "the specifications do not mark scenarios as guarded"
  (section 7.4; problem G-6). Six completion lines do:

  | Specification line | What it says | The plan |
  |---|---|---|
  | `areas/app-cli.md:413` (`DH-D-CLI-4`) | "A stop can lose work, so both scenarios run in the build stage" (`DH-E-CLI-1`, `DH-E-CLI-2`) | Both in acceptance stage 17, not guarded |
  | `areas/session-store.md:491` (`DH-D-STORE-6`) | "Deletion removes data, so this scenario runs in the build stage" (the cleanup step of `DH-E-STORE-2`) | Acceptance stage 17, not guarded |
  | `areas/session-store.md:467` (`DH-D-STORE-3`) | `DH-E-STORE-1` "runs in the build stage, not in a later acceptance batch" | Indexed to stage 23; a "recorded partial run" in step 16.8 that "does not tick the scenario" |
  | `areas/app-cli.md:440`, `:449`, `areas/subagent-team.md:468` | `DH-E-CLI-4`, `DH-E-ELECTRONUPDATER-2`, `DH-E-SUBAGENT-7` | Guarded, as asked |

  Milestone M2 lists "several processes on one home" as usable and allows three
  profiles to be enabled (plan section 3.3), while the scenario in which a second
  writer is refused and a dead holder's session is taken over has not passed. The
  owner's rule is that a scenario whose defect could delete, restore over or take
  ownership of something runs in its build stage.
- **Further candidates, by this audit's reading of the scenario text** (a reviewer
  should decide): `DH-E-STORAGE-5` (a bad settings edit "changes nothing", the same
  class as the guarded `DH-E-UISET-1` and `DH-E-BOOT-2`); `DH-E-EXT-3` (the approved
  copy and `<home>/plugins/attached`, the same class as the guarded `DH-E-BOOT-3`);
  `DH-E-HUMAN-3` (writes and removes an item in the user's operating-system keychain);
  `DH-E-CONNECT-3` (creates and revokes an authorisation at a hosted service).
- **Fix.** `impl.md` section 7.4: add `DH-E-CLI-1`, `DH-E-CLI-2` and
  `DH-E-STORE-2`; correct the sentence about specifications; decide the four
  candidates. `areas/session-store.md`: split `DH-E-STORE-1` into a lease and takeover
  scenario (P0, run in stage 16) and a request-table scenario (stage 23). Until then
  plan section 3.3 must not list several processes on one home under M2.

#### Q1-3 A deliverable's outcome is recorded only in the ledger's JSON; two outcomes are implemented mostly by steps that have no deliverable

- **Evidence.** 313 of 361 deliverable blocks do not name an outcome. `areas/boot.md`,
  `libs/cordis.md` and `libs/cordis-loader.md` do ("**Outcome.** DH-O-2"); 30 area
  files do not, and `areas/_template.md` has no such field. No rendered page lists, for
  an outcome, its deliverables and their proof: the `trace/matrix-*.md` files are per
  upstream unit type, and plan section 2 gives counts and stage numbers. A reviewer
  can follow `DH-O-n` to its deliverables only by querying `trace/ledger.jsonl`.
  Separately, 46 build steps tick something that has no `DH-D-` ID (9 in stage 1, 6 in
  stage 46, 16 in stage 47, 15 in stage 59; the plan reports this as G-1 and G-7). The gate runner, coverage, CI and the
  release are the substance of `DH-O-15` and part of `DH-O-14`; the nine deliverables
  the ledger links to `DH-O-15` include three about the UI partial library
  (`DH-D-UIKIT-1`, `-2`, `-10`), and the three linked to `DH-O-16` do not include the
  traceability tool.
- **Rule.** "Every outcome must link to implementation work and its proof; every
  deliverable must link back to the outcome."
- **Fix.** Have `tools/harness-trace render` write one more matrix, outcome to
  deliverable to tests and scenarios to stage, and link it from `design.md` section 11
  and plan section 2. Add "**Outcome.**" to the template's Priority line and to the
  blocks. Give the toolchain, release, website and known-limits work `DH-D-` IDs in
  `upstream/toolchain.md` and the two registers (the plan's own recommendation).

#### Q1-4 At least 34 deliverables state two or three priorities; the ledger and the plan see one

- **Evidence.** Priority lines such as `areas/app-cli.md:440` (`DH-D-CLI-7`): "P1;
  `config show`, `config validate` and `secret set` are P0. `session import-upstream`
  is P2". The plan builds `DH-D-CLI-7` in stage 38, after M3, where it also says "Every
  P0 deliverable is built". The other Priority lines found by search (a line that names
  more than one of P0, P1, P2): `areas/api.md:460`; `areas/app-web.md:430`, `:448`;
  `areas/bundle-preset.md:474`, `:482`, `:498`;
  `areas/context-compaction-skill.md:437`, `:444`;
  `areas/document-browser-computer.md:358`; `areas/extensions.md:446`, `:476`;
  `areas/mcp-lsp-web-connector.md:410`, `:455`; `areas/sandbox-ssh.md:497`;
  `areas/sdk-acp-typert.md:475`; `areas/subagent-team.md:467`; `areas/ui-chat.md:420`;
  `areas/ui-conversation.md:357`, `:371`, `:385`; `areas/ui-core.md:439`, `:479`;
  `areas/ui-kit.md:390`, `:397`, `:404`, `:411`, `:425`, `:432`;
  `areas/ui-sidebar-workspace.md:433`; `areas/ui-work.md:368`;
  `areas/util-test-support.md:400`, `:414`, `:421`. A priority split written in a
  different sentence form would not have been found.
- **Why it matters.** The part with the other priority is unscheduled work inside one
  nominal step. The plan's count of 60 priority inversions is taken from the ledger and
  cannot include these; the case in Q1-1 (`DH-D-SUBAGENT-6`, "the isolation option is
  P2, because it needs the experimental worktree entry") is one of them.
- **Fix.** In each specification, split the deliverable so that each part has its own
  ID, priority, dependencies and proof, keeping the existing ID for the main part; then
  re-run the plan's inversion count.

#### Q1-5 Most completion lines accept a scenario that "has run" or "is recorded"; some omit a proof the ledger names

- **Evidence.** Of the 266 "Done when" lines that name a scenario, 107 say it "has
  run" or "is run", 85 say it "is recorded", and 66 say it passes. Examples:
  `DH-D-SANDBOX-5` "`DH-E-SANDBOX-3` is run"; `DH-D-HUMAN-3` "`DH-E-HUMAN-1` has run";
  `DH-D-API-4` "[DH-E-API-1] is recorded". `areas/ui-work.md` shows the wording that is
  wanted: "is recorded as passed". In the sample of 40, two completion lines leave out
  a proof of their ledger row: `DH-D-CONTEXT-8` omits `DH-T-CONTEXT-23` and
  `DH-E-CONTEXT-5`; `DH-D-VIEW-1` omits `DH-E-VIEW-2`.
- **Rule.** "A missing, skipped, blocked or failed required scenario remains open,
  never a pass." A scenario that ran and failed "has run". Plan section 1.4 rule 6
  states the rule correctly, so the specifications and the plan disagree on what
  closes a deliverable.
- **Fix.** In every area and library specification, change the scenario clause of each
  "Done when" line to "has passed", and make each line list exactly the `proof` of its
  ledger row. A check in `tools/harness-trace verify` can enforce the second part.

#### Q1-6 An applied default overrides the owner's words on attachments, and the spine still states the owner's version

- **Evidence.** The owner decided: "A rich-text JavaScript island, so that mentions and
  attachments appear as chips inside the text". `design.md` repeats it in `DH-ADR-21`
  and in outcome `DH-O-12` ("The composer shows mentions and attachments as chips
  inside the text"). `areas/ui-chat.md` `DH-ADR-UICHAT-1` decides: "Uploads are shown in
  the rail, not as chips in the text", and says so openly ("The owner's words put
  'mentions and attachments' in the text; uploads stay in the rail"). `design.md`
  section 9.2 lists it (I5 Q1). The owner's instruction for defaults was: where a
  recommendation contradicts a decision, the decision wins.
- **Fix.** Put this one question to the owner. Until answered, either
  `areas/ui-chat.md` follows `DH-ADR-21` (an attachment chip), or `design.md` `DH-O-12`
  and `DH-ADR-21` carry a visible note that the area specification departs from them.
  Today a reader of the outcome and a reader of the area build different things.

#### Q1-7 `design.md` section 9.2 does not list every default, and one row states the opposite of what the specification holds

- **Wrong row.** "host §5.3 | The question tool is offered to the model when nobody can
  answer | Kept: the call fails closed". `areas/interaction-credentials.md:362` holds
  the opposite: "The tool declares a `When` condition and is offered only while the
  agent's root has a binding ... (Recommended default, not confirmed by the owner)".
  The register bullet it was copied from
  (`debt/carried-host-ui-experimental.md`, section 5.3) is stale in the same way.
- **Defaults marked "not confirmed by the owner" in a specification and absent from
  9.2.** (1) Vendor account requests carry the client version and language during a
  sign-in the user started (`areas/interaction-credentials.md:582`, item 2; raised as
  "Related" under I2 question 8; it also needs reconciling with `DH-ADR-32`, under
  which the vendor sees the bare word `dsh`). (2) The product installs no per-user
  service (`areas/app-web.md:554`, item b). (3) The list of six failure codes for
  continuation is fixed and not configurable (`areas/api.md:386`). (4) Two export
  boundaries: committed events only, and the quote recogniser (`areas/session-views.md:431`).
  (5) Vendor command-line plugins get a contract and no first registrant
  (`areas/mcp-lsp-web-connector.md:569`, item b).
- **Catch-all rows.** Two rows stand for an unstated number of defaults ("and the rest
  of both tables"), so the claim "list each here" is not met for them.
- **Stale references.** Six rows cite "core §5.2" and "core §5.3". In
  `debt/carried-core.md` as it stands those sections are 6.2 and 6.3; section 5 is
  "Platform limits". Plan section 9.1 repeats the old numbers, and stage 47 cites
  "§4 and §5" where sections 5 and 6 are meant.
- **Not recorded.** I4 question 1 (the .NET port) is rightly under "still open", but
  its recommendation (port the four unload fixes) is nowhere in `design.md`.
- **Checked and correct.** All 48 questions of `I1` to `I5` are in 9.1, 9.2 or 9.3; the
  rows sampled against their specification (turn budget, fork fields, safe mode, the
  volatile reference, `yamux`, ACP `session/load`, the manifest, the tree limit, the
  elision record, the system proxy) state what the specification holds.
- **Fix.** `design.md` section 9.2: correct the question-tool row, add the five rows,
  replace the two catch-all rows with one row per decision, renumber the register
  references; `impl.md` section 9.1 and stage 47 follow.

#### Q1-8 Leftovers that contradict `DH-ADR-28` (no signing secret) and `DH-ADR-18` (no desktop shell)

- **`libs/node-crypto-timers-perf-hooks.md`, requirement rows.** `DH-LIB-NODECRYPTO-R2`
  still lists "cookie signing key" among the secrets the harness generates, and `-R5`
  says "The cookie signature covers the exact encoded body and is checked before the
  body is decoded". Line 195 of the same file says "There is no cookie signing key and
  no signature". The requirement rows should be rewritten to the random session id.
- **`areas/x-liveshare-webworker.md`.** Line 154 says the exchange "calls A18's
  `auth.Issue(principal, ttl)` ... so this component implements no cookie of its own",
  which is what the owner of that interface says too (`areas/host-telemetry.md:197`).
  But the ownership paragraph (line 218), the failure table (line 226) and the
  implementation of the deliverable (line 272) still describe "the signing key" and "a
  cookie signed with HMAC-SHA-256 under the in-memory key", with the component
  answering `listen.Authenticate` for its own cookie name. Rewrite the three passages
  to the `auth.Issue` form.
- **`libs/koffi.md`, table row 8.** The Go answer for the folder dialog begins
  "Desktop: the webview shell's own dialog call (owned by the UI-stack specification,
  DH-ADR-3)". There is no shell; `DH-ADR-18` makes the dialog a server-rendered
  directory picker. Replace the cell.

#### Q1-9 `core-session.md` and `x-recommendations-misc.md` disagree on a translation of a session another process holds

- **Evidence.** `areas/core-session.md:515` (`DH-D-SESSION-9`, result): "Feedback, a
  rename or a translation succeeds whether the session is idle, live here or running in
  another `dsh` process". `areas/x-recommendations-misc.md:193`: "Another `dsh` process
  holds the session | `translate/session-held-elsewhere`; saved results still show;
  nothing is sent", with the reason at line 230 (a translation is two appends with a
  provider call between them) and a test clause at line 326 ("no provider call and no
  filed request"). `design.md` section 9.2 (U-K2-13) agrees with the second.
- **Fix.** `areas/core-session.md`, `DH-ADR-SESSION-12` and `DH-D-SESSION-9`: say that a
  translation is written on a live or a free session only, and name the refusal.

#### Q1-10 Ten library specifications report programs that were compiled and run, while the spine says nothing was

- **Evidence.** `libs/yaml.md:5`, `libs/commander.md:5`, `libs/semver.md:5`,
  `libs/picomatch.md:5` and `libs/yarnpkgparsers.md:5`: "'Probe' means a throwaway Go
  program run on 2026-11-18 against the candidate modules", each with a section "What
  the probe showed". `libs/ipaddr.js.md:7`, `libs/mime.md:6`,
  `libs/node-net-builtins.md:6` and `libs/undici.md:6` cite "a throwaway probe kept
  outside this repository". `libs/opentelemetry.md:176` to `:177` reports binary sizes
  ("6.84 MB for linux/amd64 ... Same probe built on the SDK: 14.18 MB"). The agent
  brief forbids this ("Write no code at all, not even a throwaway probe"), and
  `design.md` opens with "Nothing is built, run or tested".
- **Why it matters.** Module choices rest on these results (the YAML module, the
  command-line pair, the telemetry exporter), and the probe sources are not kept, so
  the evidence cannot be repeated. The files are honest about what they did; the
  inconsistency is with the project's stated status.
- **Fix.** The coordinator decides one of two. Either each result is relabelled as
  "historical observation, source not retained, to be re-established by test
  `DH-T-...`" and `design.md` section 10 says that ten library choices rest on such
  observations; or each result is removed and becomes an open question. In both cases
  the status sentence of `design.md` should read "no harness code exists".

#### Q1-11 The spine states its own status wrongly in three places

- `design.md`, status block: "the implementation plan is not written". The plan
  exists and schedules all 363 deliverables.
- `design.md` section 10, tech-debt paragraph, and `DH-ADR-40`: "107 are platform
  facts ... 262 are deliberate product boundaries, of which 45 had no decision record
  yet; 37 units with 26 causes were genuinely unresolved" and "Until the area
  specifications carry each one ... and the ledger rows are re-issued, the registers
  are the truthful list". The registers and plan section 8 describe the state after
  that: 110 platform limits, 271 boundaries all with a record, 3 unresolved units in
  one entry, 26 resolved, 3 not applicable; 384 ledger rows `carried`, which is also
  what `traceability.md` shows.
- `README.md`: "Status: research in progress. This index is completed when the
  documents are done"; it does not list `debt/` or the audit reports.
- **Fix.** Update the three texts; state the five statuses once in `README.md` as the
  plan's section 1.2 does.

#### Q1-12 Three passages contradict the decision written beside them

- `areas/ptc-workflow.md:126` (section 5, `node.3`): "nothing replaces it ... that is
  an open question for A09". `DH-ADR-PTC-5` at line 348 gives the script's process tree
  a memory and a process-count limit.
- `areas/subagent-team.md:150` (section 5): "isolation per member is deferred".
  `DH-ADR-SUBAGENT-12` specifies `isolation: worktree`.
- `areas/ui-sidebar-workspace.md`, `DH-ADR-UISIDE-9`: the Decision says "the panel
  docks on laptop widths by narrowing the transcript"; the Tradeoff says "On laptop
  widths the panel is still an overlay".
- **Fix.** Rewrite the two table cells and the one tradeoff sentence. The first two
  were already reported by `debt/carried-core.md` section 2.

#### Q1-13 Area keys are used about 100 times in `design.md` and are explained only in a JSON file

- **Evidence.** The outcome table's "Areas" column, the package table and many
  sentences use `A03`, `C4`, `X2`, `F7`. The only legend is `trace/clusters.json`
  (`design.md` section 11). The column also holds agent keys that are not areas at
  all: `F1`, `F2`, `F3`, `F7` and "Q1 to Q3". `libs/sharp.md` refers to an area `A26`
  that does not exist.
- **Fix.** `design.md` section 2 or 11: one table of key, area name and document for
  the 39 areas; replace `F` and `Q` keys in the outcome table with the document they
  stand for. The plan avoids keys altogether and can serve as the model.

#### Q1-14 Deliverable blocks with a missing field or a dependency list that differs from the ledger

- **No "Components and files".** All ten deliverables of `areas/filesystem.md`
  (`DH-D-FS-1` to `-10`).
- **A ledger dependency that the "Depends on" text does not name** (21 blocks):
  `DH-D-CLI-2` (`HUMAN-2`), `BOOT-5` (`BOOT-2`, `BOOT-3`), `BUNDLE-2`, `-3`, `-5`, `-6`,
  `EXEC-8`, `EXT-4`, `CONNECT-3` (`HUMAN-6`), `PTC-2`, `SDK-9` (`HUMAN-1`), `UISET-8`,
  `UIWORK-5`, `XCUA-1` to `-5` (`CORDIS-1`), `XFORGE-5` (`JOBS-2`), `XFORGE-6`,
  `XMISC-1` (`SESSION-9`). Some are ranges written as "1 to 3" and are only a notation
  issue; each should be read.
- **A dependency in prose that the ledger omits, and that changes the order.**
  `DH-D-SUBAGENT-6` on `DH-D-XFORGE-1` and `-2` "at run time"; see Q1-1. 113 blocks
  name a deliverable the ledger does not; most are consumers named for orientation,
  and they were not all read.
- **Fix.** Each owning specification; then a verifier rule that compares the two lists.

### Minor

#### Q1-15 "Related areas" lines in `libs\`: one damaged, seven stale or wrong, seven under another label

All 109 library files were read for this line. No lost table separator, dangling
bracket or cut-off sentence remains.

| File | Line | Defect |
|---|---|---|
| `libs/puppeteerbrowsers.md` | 16 | Damaged: "(`../areas/` execution world)", a path cut off after the folder; should name `../areas/execution.md` |
| `libs/sharp.md` | 17 | "A26 (application icons)": no such area |
| `libs/opentelemetry.md` | 19 | "Not written yet, so not linked": the three areas exist |
| `libs/ptc-guest-runtime.md` | 29 | "None is written yet" |
| `libs/node-builtins-vm-worker-module.md` | 20 to 23 | "none is written yet"; also not a table row, and its paths lack `../` |
| `libs/js-yaml.md` | 16 | Names "desktop (update feed)" as an area |
| `libs/semver.md` | 16 | "desktop (update)" and "plugin updates" |
| `libs/tar.md` | 15 | "plugin market", which is dropped |
| `libs/node-crypto-timers-perf-hooks.md` | 17 | "desktop release tooling" |
| `libs/build-test-toolchain.md`, `cos-nodejs-sdk-v5.md`, `electron.md`, `electron-builder.md`, `electron-updater.md`, `octokit-webhooks.md`, `yao-pkg-pkg.md` | header | The row is called "Related documents"; the template says "Related areas" |

#### Q1-16 `Peer.FormToken()` is used by two areas and missing from its owner's interface

`areas/api.md:428` and `areas/ui-core.md:562` call "A18's `Peer.FormToken()`".
`areas/host-telemetry.md:158` declares `Peer` with `ID`, `Principal` and `Context`
only, and the browser-session row (line 197) lists no key, while line 194 mentions
the token in passing as "an HMAC of the peer's id under a random key kept with that
session's row". Add the method and the stored key to the contract, and one sentence
saying why a per-session key is not the "signing secret" that `DH-ADR-28` rules out.

#### Q1-17 `sdk-acp-typert.md` does not state that a prompt command cannot be run from an ACP client

`DH-ADR-SDK-9` says commands "are not offered", and line 301 lists slash commands as
absent. The consequence for MCP prompt templates, which became human commands
(`DH-ADR-CONNECT-4`), is stated only in `debt/carried-host-ui-experimental.md`
(entry U-K2-14: "The SDK specification does not state the consequence in its own
words"). Add one sentence to section 6.2 of the SDK specification.

#### Q1-18 Small errors in the plan

- Stage 8, Sequence: "Four steps are P1 in the ledger and pulled forward"; three are
  (8.3, 8.4, 8.7), as the stage index says.
- Section 8, entry U-K1-2: "memory and processor limit"; the specification adds a
  memory and a process-count limit and no processor meter.
- Section 7.1, "Stages" column: build stages that run guarded scenarios in that
  environment are missing (a real model API in stages 16, 23, 29, 43, 50, 56; a real
  browser in 39 and 56).
- Section 7.2, item 4: time limits are a rule with no numbers ("set when the helper is
  built"). The owner's rule asks for timeouts; record them per environment when known.
- Section 7: no exact command line or parameter list per environment; only "`go test`
  behind the scenario build tag" and `DSH_REQUIRE_SCENARIOS`.
- Section 10.5: G-8 is listed before G-7.

#### Q1-19 Decision records without a "Serves" part

The 38 records of `upstream/architecture.md` (12), `upstream/security.md` (11) and
`upstream/toolchain.md` (15) have Decision, Why, Tradeoff, evidence and Verified by,
and no Serves; `design.md` section 7.2 supplies the outcome as "this document's
mapping". `DH-ADR-PIAI-4` (`libs/earendil-works-pi.md`) has none either. Add the line
to the records.

#### Q1-20 Scenario blemishes

- Ledger title and section heading differ: `DH-E-BOOT-3`, `DH-E-STORE-3`,
  `DH-E-STORE-4`, `DH-E-ELECTRONUPDATER-2`, `DH-E-GOUISTACK-2`.
- `DH-E-BUNDLE-3` "Proves. D-2, D-3": a short form with no link.
- Five scenarios are table rows without an anchor (`DH-E-CHOKIDAR-1`, `-2`,
  `DH-E-NODEFS-1`, `DH-E-RIPGREP-1`, `-2`; the plan's G-8). `DH-E-LLM-7` to `-10` are
  table rows whose cells say "As above".
- Three sampled scenarios give their Given or Cleanup as "As above" (`DH-E-AGENT-4`,
  `DH-E-UICHAT-4`, `DH-E-UICORE-3`); each should be readable alone.
- `DH-D-BOOT-9` and others: see Q1-5 for "has run".

#### Q1-21 Other stale text

- `areas/app-web.md` section 10, item 3: "Two clauses relied on that area C1 has not
  yet written"; `areas/ui-core.md:452` and `:511` now hold the asset-manifest clause.
- `design.md` section 11, last row: "584 of 3,578 units"; the inventory has 3,669. It
  is labelled historical; give the current figure beside it or remove it.
- `areas/host-telemetry.md:194`, `:218` and others cite "Go 1.27.4" for standard
  library symbols. The floor is `go 1.26.0`; say that the symbol exists at the floor.

#### Q1-22 Short-form decision IDs in `design.md` sections 3 to 6 are not links

`ARCH-1`, `TOOLCHAIN-6`, `SECURITY-11`, `CF-10` and the like appear dozens of times
outside section 7.2 with no link. The status block explains the short form; the rule
asks for working links. Link each to its row in section 7.2.

#### Q1-23 Terms a newcomer meets before they are defined

`design.md` sections 1 to 4:

- "foundation documents", "library wave", "area wave", "integration": used from the
  status block on, never defined.
- "cordis component" in the product paragraph of section 1, before section 2, and
  cordis itself is explained only in `../architecture.md`.
- `CF-n` and `OD-n`: called "working labels" with no sentence on what a `CF` is.
- "ledger", "cluster keys": used in section 3.2 before any explanation.
- "SDK", "ACP" (expanded only in section 4.8), "SSE" (in the diagram of 4.1; written
  out only in `DH-ADR-3`), "WAL" (4.7, never expanded), "cgo" and "pure Go"
  (`DH-O-14`), "htmx" (never glossed), "seat" (4.3, "the factory seat").
- Passages that need Go or source knowledge: the "Go mechanism" column of 4.6
  (`AroundKey[Req, Res]`, `Notify`); the concurrency table of 4.5 ("`context.Context`
  first in every operation"); the package table of 4.2 (`yamlx`, `expr`, `go.work`,
  `replace`); item 2 of "How a script reaches tools" in 4.4 ("a private per-command
  file"); the `!expr` and `Required` sentences of 4.9. Each is correct detail; a
  one-line plain statement before the detail would be enough.

`impl.md` sections 1 to 3:

- "ledger", "gate runner", "scenario helper", "test kit", "keyless replay", "egress",
  "spill": used in 1.2 to 3.2 before the stage that introduces them; the vocabulary
  table of 1.3 is otherwise good and could take these seven.
- Section 3.4 names bubblewrap, Landlock, Seatbelt, "restricted token", "user service
  manager", "delegated scope" and "control group" with no gloss.

## 4. Checked and found correct

- **Plan arithmetic.** 59 stages, each with 1 to 20 steps (the largest is stage 58 with
  20) and each with Kind and priority, Result, Entry, Sequence, Steps, Manual checks,
  Scenarios and Exit evidence. Step counts equal the stage index. 533 steps, all open
  boxes. Scenario counts add up for the stages recounted (17: 26; 18: 29; 48: 39) and
  the rows of stage 47 add up to 175.
- **Scheduling against the ledger.** All 363 deliverables are in exactly one build
  stage; the 15 that appear again do so only in closing steps. Every one of the 29
  priority mismatches is labelled "pulled forward", and every one of the 20
  dependencies built later is labelled "held open" with a closing step. The plan's
  per-outcome deliverable counts equal the ledger's for all 16 outcomes. Every `#dh-`
  link in the plan has an anchor.
- **Handling of inversions and cycles, as far as the ledger goes.** Pulling forward a
  self-contained dependency and holding the rest open is sound and is reported
  honestly, including what is not done at each milestone. Building the fourteen
  members of cycle C-1 in one stage, cutting C-2 at two held-open edges, and building
  C-3 in one stage are sound; the plan's recommended corrections (split the dependent,
  state the dependency on the contract only) are the right ones. The limit is Q1-1
  and Q1-4: what is not in `deps` is not seen.
- **Owner decisions.** No remaining text was found that: offers a desktop application
  or Wails as the design (one cell excepted, Q1-8); embeds an interpreter; has a
  `launch.mint` method or any control method that returns a link; has a pairing step;
  lets the off-switch refuse `dsh update` or end a share; leaves `apply_patch` off;
  has a second `dsh web` print the link; lets a grant survive a resume or pass to a
  fork or subagent; or says "Provisional, owner to confirm". The grant rule is stated
  the same way in eleven documents. The ten sender kinds are the same in `design.md`,
  `areas/host-telemetry.md` and their consumers. The three-module layout, the update
  check as a sender, plan mode enforced, session attachment only, `confine: none`, no
  relay and status-only forge are stated consistently where they were looked up.
- **Decision records.** All 43 sampled have a real choice, reason, tradeoff, outcome
  and named verification. One correctly labels old evidence as historical
  (`DH-ADR-LOADER-10`).
- **Scenarios.** All 46 read have Given, When, Then and Cleanup, name a real
  environment (a real model API, operating system, SSH host, browser, plugin, external
  program), assert things an observer can see, say what to keep if cleanup fails, and
  do not suggest they ran. Real-environment scenarios are in their own subsection in
  every file looked at.
- **Interfaces described the same way by owner and consumers (11 of 14).** The
  pipeline entries `ExecuteBatch` and `Call`; `ScriptLimits` and `AllowScriptCalls`;
  `tools.Settled`; `config.Service` (`Tree`, `Preview`, `Apply`, `Status`);
  `OpenWrite(IfFree)`; the control endpoint's three methods; `listen.Authenticate`;
  `SetForCall`; `Spec.Limits`; `policy.Resolver`; the grants interface. The three that
  differ are `AppendLogOnly` (Q1-9), `auth.Issue` (Q1-8) and `Peer.FormToken` (Q1-16).
- **"First verdict" tables.** Each of `upstream/decisions-1.md` to `-4.md` says in its
  status block, its column legend and a bold paragraph that the ledger governs;
  `design.md` section 10 says it again. The reader is told clearly.
- **Status lines.** All 148 area and library specifications and the nine upstream
  documents open with a design-only status; both registers say "Nothing was built, run
  or tested" and define "resolved" narrowly. The plan keeps design, implementation,
  test authoring, local passes and acceptance apart and marks all but the first "Not
  started".

## 5. Limits of this audit

- A finding count is not a defect rate for the whole set. Checks marked "every one"
  are complete for the property they test (a label is present, a link resolves, a
  count agrees). They do not show that the text behind a label is right.
- The samples are small: 40 of 363 deliverables, 43 of 575 decisions, 46 of 235
  scenarios. Q1-1 found 5 misplaced scenarios among 46; the other 189 were not read,
  so the true number is unknown and is probably higher.
- "No leftover found" means the searched wording was not found. A leftover phrased
  differently would have been missed.
- Whether an upstream fact is true, whether a debt item is really resolved, and
  whether a library choice is sound were not examined here.
- The guarded-scenario candidates in Q1-2 and the "likely also affected" items in Q1-1
  are this audit's reading of scenario text and need a decision by the owning area.
- Nothing in this report is evidence that any part of the design works. No code
  exists, and no test or scenario has run.
