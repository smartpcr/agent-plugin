# Request context, compaction and skills: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". Sections 6 to 9 are design throughout. Nothing here has been built, run or tested. IDs in this document use the area key `CONTEXT`.

| | |
|---|---|
| **Area key** | `CONTEXT` (agent `A14`) |
| **Upstream packages** | 17 in three groups: `context` (7), `compaction` (5), `skill` (5) |
| **Serves outcomes** | [DH-O-4](../design.md#dh-o-4) (governs everything here), [DH-O-3](../design.md#dh-o-3), [DH-O-8](../design.md#dh-o-8), [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7), [DH-O-1](../design.md#dh-o-1), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A01 (surface, replace, rewrites, auxiliary requests), A02 (`PreStep`, `AttemptFailed`, maintenance), A03 (tool pipeline, prompt sections), A04 (LLM runtime, meter, catalogue, message sources), A06 (projections, query, working directory), A07 (spill), A08 (filesystem seam, ignore rules), A09 (shell seam), A16 (office tools), A17 (commands), A22 (plugin capability), A23 (`watch`, glob, `schema`) |
| **Priority** | P0 for the context pipeline and workspace instructions; P1 for compaction, skills and references; P2 for terminal location and recall |

## 1. Purpose

A model answers from what it is sent. Beyond the conversation itself, the harness sends three more kinds of text, and this area owns all three.

- **Request context**: facts the user never typed but the model needs, such as the repository's instruction files, the current time, and a snapshot of another session the user pointed at.
- **Compaction**: when a conversation outgrows the model's window, older history is shortened (by trimming oversized tool output, by omitting old images, and by replacing a span with a model-written summary) so the session can continue.
- **Skills**: packaged instructions on disk that the model can find in a catalogue and load when a task calls for them.

Without this area an agent ignores `AGENTS.md`, a long session ends at the first "context too long" error, and reusable instructions must be pasted by hand. The governing promise is [DH-O-4](../design.md#dh-o-4): each of these texts is in the session log before the model sees it, and a replay reproduces it from the log without reading a file, a clock or another session again.

## 2. Vocabulary

Terms from [../design.md §2](../design.md#2-vocabulary) (surface, derived history, projection, seam, scope, lease) and from [agent-loop.md §2](agent-loop.md#2-vocabulary) (step, admission, request series, maintenance task) keep their meaning.

| Term | What it is | Why it exists |
|---|---|---|
| **Contributor** | A component that may add context messages when a step is proposed. | Each kind of context has one owner and one place in a fixed order. |
| **Observation** | What a contributor read from outside the log: file bytes, a clock reading, another session's surface. | It is recorded inside the message it produced, so a replay never observes again. |
| **Sourced message** | A user-role message whose `source` names the contributor and carries its typed record (paths, digests, times). | Readers and folds recognise context without parsing prose. |
| **Visible state** | What a contributor's own messages on the *current* surface say; shadowed messages do not count. | A contributor adds only what changed, and re-adds what compaction removed. |
| **Instruction file, chain** | A Markdown file of standing guidance (`AGENTS.md`, `CLAUDE.md`). The *user-global chain* is the user's own files; the *project chain* runs from the project root down to the working directory. | Broad rules and specific rules are budgeted and ordered separately. |
| **Mention** | Text the user typed to point at something: `@path` for a file, a `dsh-session:` link for a session. | A pointer is cheap; content enters only through a logged read or snapshot. |
| **Context window (`W`), output reservation (`O`), headroom (`B`)** | The most tokens one route accepts; the tokens reserved for the answer; spare room kept free. | Together they decide when history must shrink. |
| **Pressure, threshold, overflow** | Pressure is the estimated size of the next request. The threshold is the size at which compaction starts. Overflow is a provider's refusal because the request exceeds its window. | Shrinking before the provider refuses is cheaper than after; overflow is the one failure compaction can repair. |
| **Pruning** | Replacing the middle of an oversized tool result with a marker, with no model call. | Often removes the need to summarise at all. |
| **Image offload** | Replacing an old input image with a text placeholder for all later requests. | A route's image budget is separate from its token window. |
| **Summariser** | The model call that condenses a span of history. | It is a model request like any other: logged, attributed, bounded. |
| **Checkpoint** (here: *compaction checkpoint*) | The user-role message that replaces a span with its summary. | The model continues from it as if it had always been there. |
| **Balanced edge, retained tail** | A balanced edge is a cut in history that no unanswered tool call crosses. The retained tail is the newest part of history, which compaction never touches. | A provider rejects a tool call without its result; the model keeps its recent work word for word. |
| **Skill, bundle** | A named Markdown instruction file with a short description; a bundle is its directory with helper files. | Instructions that cost tokens only when a task needs them. |
| **Skill provider, source class** | A provider lists and loads skills from one place. Its source class is `embedded`, `user`, `custom`, `project` or `plugin`. | The registry merges places; the class says who wrote the text. |
| **Catalogue** | The list of skill names and descriptions the model is shown. | The model chooses by summary and loads the body on demand. |
| **Invocation policy** | Two flags per skill: may the model load it, may the user invoke it. | Some skills are for people only. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.context.agent-instructions` | `dsh-agent-instructions` | consumer | Loads the user-global and project instruction chains as a durable user-role message and reports later changes (`packages/context/agent-instructions/README.md:12`). |
| `UP-PKG-packages.context.time-context` | `dsh-time-context` | consumer | Adds a clock reading: time, browser zone policy, elapsed time (`packages/context/time-context/README.md:12`). |
| `UP-PKG-packages.context.tmux-context` | `dsh-tmux-context` | consumer | Opt-in: tells the model which tmux pane its host process runs in (`packages/context/tmux-context/README.md:12`). |
| `UP-PKG-packages.context.harness-dev-context` | `dsh-harness-dev-context` | consumer | Tells an agent behind the Web GUI where the harness checkout is and which URL serves the GUI (`packages/context/harness-dev-context/README.md:12`). |
| `UP-PKG-packages.context.file-reference` | `dsh-file-reference` | definition | `ctx.fileReferences`: path candidates for `@file` completion, and the mention grammar (`packages/context/file-reference/README.md:12`). |
| `UP-PKG-packages.context.file-reference-local` | `dsh-file-reference-local` | provider | A bounded fuzzy index of the host filesystem per agent (`packages/context/file-reference-local/README.md:68`). |
| `UP-PKG-packages.context.session-reference` | `dsh-session-reference` | provider (core) | `ctx.sessionReferenceResolver`: turns a session mention into a bounded, untrusted snapshot (`packages/context/session-reference/README.md:12`). |
| `UP-PKG-packages.compaction.compaction` | `dsh-compaction` | definition | Abstract `ctx.compaction` with three operations, the `compaction/*` events and tool-pairing helpers (`packages/compaction/compaction/src/index.ts:119-187`). |
| `UP-PKG-packages.compaction.compaction-basic` | `dsh-compaction-basic` | provider | Threshold policy and the model-written summary (`packages/compaction/compaction-basic/README.md:32`). |
| `UP-PKG-packages.compaction.compaction-tool-result-pruner` | `dsh-compaction-tool-result-pruner` | provider (core) | `ctx.toolResultPruner`: head, marker, tail (`packages/compaction/compaction-tool-result-pruner/README.md:44`). |
| `UP-PKG-packages.compaction.compaction-image-offload` | `dsh-compaction-image-offload` | consumer | Records which images to omit after a route rejects a request (`packages/compaction/compaction-image-offload/README.md:38-42`). |
| `UP-PKG-packages.compaction.command-compact` | `dsh-command-compact` | consumer | The human `/compact` command (`packages/compaction/command-compact/README.md:34-36`). |
| `UP-PKG-packages.skill.skill` | `dsh-skill` | definition and registry | `ctx.skills`: merges provider catalogues, resolves duplicates, loads a body (`packages/skill/skill/README.md:50-53`). |
| `UP-PKG-packages.skill.skill-filesystem` | `dsh-skill-filesystem` | provider | Finds skills under project, custom and user directories and watches them (`packages/skill/skill-filesystem/README.md:36`, `:48-54`). |
| `UP-PKG-packages.skill.skill-office` | `dsh-skill-office` | provider | Four bundled Office skills: a router and three authoring guides (`packages/skill/skill-office/src/index.ts:11-12`). |
| `UP-PKG-packages.skill.tool-skill` | `dsh-tool-skill` | consumer | The model-facing catalogue, the `skill` tool and the `/name` gesture (`packages/skill/tool-skill/README.md:54-57`). |
| `UP-PKG-packages.skill.tool-workspace-dependencies` | `dsh-tool-workspace-dependencies` | consumer | `load_workspace_dependencies`: paths into a bundled Python, Node.js and pnpm payload (`packages/skill/tool-workspace-dependencies/README.md:12`). |

### 3.2 Behaviour

**Request context.** Every context plugin listens on `agent/pre-step` and hands the loop a user-role message with a typed source; the message is an ordinary logged event (`packages/context/agent-instructions/README.md:90`).

- **Instruction files.** The first request gets one baseline message: `$DSH_HOME/AGENTS.md`, then `~/.agents/AGENTS.md`, then every candidate from the project root (nearest `.git`) down to the working directory, broad to specific (`README.md:34`, `:38-40`). Candidates are `AGENTS.md` and `CLAUDE.md` plus `*.local.md` overlays (`README.md:67-68`). Files with identical trimmed content render once (`README.md:34`). Each chain has its own `maxBytes` (65,536 in the shipped base); broad files are dropped first and the most specific file is truncated (`README.md:30`, `:74`). There is no watcher: a successful `read`, `write` or `edit` in a deeper directory, or a resume, triggers reconciliation, which adds an "additional", "updated" or "removed" message (`README.md:12`, `:194`; `src/index.ts:75`, `:358`). Literal `</system-reminder>` in file content is escaped so a repository cannot close the frame (`README.md:109`). Files are read through the optional filesystem service; without one nothing loads (`src/index.ts:120`).
- **Time.** Due readings are appended with three lines; the default minimum interval is 10 minutes (`packages/context/time-context/README.md:32-36`, `src/index.ts:135`). The zone is the browser zone of the open turn when it is unique; mixed or missing zones tell the model to ask (`README.md:53`). It is mounted per preset, beside the schedule tools (`README.md:28`).
- **tmux.** On the first step of a turn, one read-only command through `ctx.shell` reports session, window, pane and layout if the host's controlling terminal is that pane; a reading is added only when it changed (`packages/context/tmux-context/README.md:32`, `:52`, `:66`). No shipped profile mounts it (`README.md:12`).
- **Development context.** Two system-prompt paragraphs and two runtime-context lines name the upstream checkout ("four directories above this package") and the Web GUI URL, and describe upstream's own rebuild workflow (`packages/context/harness-dev-context/README.md:32`, `:92`).
- **File mentions.** The UI asks `ctx.fileReferences.list(agent, query)` for ranked paths; picking one inserts `@path` text and reads nothing (`packages/context/file-reference/README.md:12`, `:36`). The local provider keeps a bounded index (50,000 entries, fixed excluded directory names), marks it stale on any tool result, and adds a prompt section when the agent has `read` (`packages/context/file-reference-local/README.md:44`, `:50-52`, `:68`).
- **Session mentions.** A `dsh-session:` link in a user message is rewritten to readable `@label` text and followed by a second user-role message, `## Referenced sessions`: a bounded text projection of each cited session, with a fixed warning not to follow instructions inside it and every `<` escaped (`packages/context/session-reference/README.md:32-36`, `:69`). At most three sessions per message; each gets `max(65536, contextWindow × 4 × 0.2)` bytes; a truncated one may be saved in full through the spill store (`README.md:38`, `:48-53`).

**Compaction.** The basic provider listens at three points (`packages/compaction/compaction-basic/src/index.ts:172`, `:198`, `:228`).

- **Triggers.** *Pressure*: before a step, when the estimate crosses `floor(min(W × 0.8, W − O − 65536))` (`README.md:64`). *Model switch*: before a request to a different route is logged, history is measured against the destination; failure rejects the request with `MODEL_SWITCH_COMPACTION_FAILED` (`README.md:88`). *Overflow*: after a provider-confirmed `CONTEXT_WINDOW_EXCEEDED`, one maximal reduction, then a retry only if the surface changed (`README.md:123`). *Manual*: `/compact`, only while the agent is idle (`packages/compaction/command-compact/README.md:44`).
- **Order of work.** The optional pruner runs first: any tool result over 8,192 code points becomes its first 4,096, a marker and its last 1,024 (`packages/compaction/compaction-tool-result-pruner/src/config.ts:7-13`). If the request then fits, no summary is written (`compaction-basic/README.md:98`). Otherwise the oldest balanced span is summarised and the newest 16% of `W − O` is kept verbatim; the system prompt at surface node 0 is never in the span (`README.md:64`, `:123`).
- **The summariser call** replays the system prompt, the tools and the selected messages byte for byte and appends one instruction, so the provider's prefix cache is reused (`README.md:116`, `:129`). It goes to the model that produced the latest assistant message unless a target is configured (`README.md:88`). Only returned text is kept; it is framed in `<compacted-summary>` tags (`src/summarizer.ts:25-26`, `:158`).
- **How it is logged.** `compaction/start`, then `compaction/summary` (summary, shadowed sequence numbers, token estimate, route, usage), then one `user/message` with a `replace` operation over the span, then `compaction/end`. Only the `user/message` is on the surface; the replaced events stay in the log (`packages/compaction/compaction/README.md:95-97`; `docs/subsystems/compaction.md:15-17`). The unmatched `compaction/start` is the lock; one older than the newest `session/end-seed` is stale (`compaction/README.md:111`).
- **Image offload.** On `IMAGE_OFFLOAD_REQUIRED` the plugin appends `image/offload` naming the oldest image occurrences and asks for a retry; a registered pure function turns those images into placeholder text in every later request (`packages/compaction/compaction-image-offload/src/index.ts:26-35`; `README.md:38`).

**Skills.** The registry asks each provider for candidates, one provider after another, validates them and keeps the winner per name: nearest scope layer, then rank, then registration order (`packages/skill/skill/README.md:84`, `:94`; `src/index.ts:602`). The filesystem provider recognises `<root>/<name>/SKILL.md` and `<root>/<name>.md` with YAML front matter (`name`, `description`, optional `whenToUse`, `disable-model-invocation`, `user-invocable`), in root order `<project>/.dsh/skills`, `<project>/.agents/skills`, custom, `<home>/skills`, `~/.agents/skills`, bundled (`packages/skill/skill-filesystem/README.md:36`, `:48-56`). It watches existing roots with chokidar at depth 1 and polls for a missing root (`src/index.ts:467`, `:499-504`). The tool package publishes a catalogue message before the first request and a whole replacement when a digest of names, descriptions and completeness changes (`packages/skill/tool-skill/README.md:85`). The `skill` tool takes one argument, the exact name, and returns the body in a `<skill_content>` block with a resource hint (`README.md:181-189`). A `/name` token anywhere in a user message injects the same block for a user-invocable skill (`README.md:89`). The Office provider serves four `SKILL.md` files and appends a Python-environment section that points at `load_workspace_dependencies` or at the user's Python (`packages/skill/skill-office/README.md:41`). **Contract units.** Two tools (`skill`, `load_workspace_dependencies`), two live events (`compaction/summary-error`, a waterfall at `packages/compaction/compaction/src/index.ts:106`; `skills/change`, an emit at `packages/skill/skill/src/index.ts:299`), twelve config schemas and five services. Section 6.2 gives each its Go counterpart.

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | Left behind | Source |
|---|---|---|---|
| An instruction file cannot be read | Its whole candidate group keeps its last-good state for that pass | A change notice delayed until a later pass | `packages/context/agent-instructions/README.md:225` |
| A tmux or clock contributor fails | tmux: warning, no reading. Clock: a registration failure stops the request | Nothing | `packages/context/tmux-context/README.md:52`; `packages/context/time-context/README.md:79` |
| The summariser call fails or is cut at its output cap | Pressure: a warning, and the step continues with full history. Switch: the request is rejected. Manual: an error code. Earlier prunes and checkpoints stay | An attempt recorded only as `compaction/end` with an error. Inference: the request that was sent is not in the log, because its route, cap and usage are fields of `compaction/summary`, which is appended only on success | `packages/compaction/compaction-basic/README.md:261`; `src/region.ts:246`, `:492`; `docs/subsystems/compaction.md:16` |
| History changed while summarising | The attempt is closed as `changed`; no replacement | A closed bracket | `docs/subsystems/compaction.md:99` |
| The closing append fails, or the host dies inside the bracket | The unmatched start blocks every later compaction in that process lifetime | **Uncertain:** `commit` "may follow partial mutation" | `packages/compaction/compaction-basic/src/region.ts:161-163`; `docs/subsystems/compaction.md:99` |
| A prune replacement is rejected | The run fails; earlier replacements stay | A partly pruned surface, which is valid | `packages/compaction/compaction-tool-result-pruner/README.md:82` |
| A skill provider fails to list | Logged, skipped, and the observation is marked incomplete and not cached | A lower-ranked skill of the same name may win meanwhile | `packages/skill/skill/README.md:66`; `packages/skill/tool-skill/README.md:119` |
| One skill file is unreadable | Its whole root is omitted for that observation | Readable siblings hidden | `packages/skill/skill-filesystem/README.md:150` |

## 4. Upstream decisions dissected

The first twelve rows are the notes handed to this area (paths under `.agents/notes/`); the ledger rows hold the longer reasoning. The rest are visible only in code or READMEs. `ADR-n` means `DH-ADR-CONTEXT-n` in [section 7](#7-decisions).

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented/architecture/2026-07-20-routed-model-context-and-compaction-policy.md` | Capacity is a fact of the exact route; measurement is model-agnostic; thresholds are compaction's per-route policy | keep | One process talks to models of different sizes | [ADR-4](#dh-adr-context-4) |
| `implemented/architecture/2026-09-10-image-offload-events.md` | A logged `image/offload` event names omitted image occurrences; a pure function applies it forever | keep, plan first | Otherwise the visible image set depends on unlogged request preparation | [ADR-6](#dh-adr-context-6) |
| `implemented/architecture/2026-09-24-preset-scoped-time-context.md` | The clock is mounted only in presets whose tools use it | keep | A context message with no consumer is pure cost | [ADR-1](#dh-adr-context-1) |
| `implemented/architecture/2026-09-27-progressive-office-skills.md` | A short router skill plus three authoring skills loaded by name | keep the structure | Ordinary edits do not pay for rendering guidance; the shell-execution half is superseded upstream | [ADR-7](#dh-adr-context-7) |
| `implemented/architecture/2026-10-05-shared-agents-root-instructions.md` | Two ordered user-global files, de-duplicated by content | keep | People share one file across agent tools | [ADR-2](#dh-adr-context-2) |
| `implemented/architecture/2026-10-16-separate-instruction-chain-budgets.md` | Each chain has its own byte budget | keep, extended | A large project chain must not evict the user's own rules; unused bytes may now be lent | [ADR-2](#dh-adr-context-2) |
| `implemented/architecture/2026-10-26-cross-deployment-office-kit.md` | Office skills everywhere, read from one asset directory; a bundled Python only where a payload ships; a missing engine degrades | change | Skills are embedded in the binary; no payload ships; the engine rule is A16's | [ADR-3](#dh-adr-context-3), [ADR-7](#dh-adr-context-7) |
| `implemented/bug-fix/2026-10-11-process-specific-prompt-values-in-runtime-context.md` | Per-process values leave the system prompt so a new port cannot break a session | keep the rule, drop the package | The rule holds for every contributor; the two values are development-only | [ADR-1](#dh-adr-context-1), [ADR-3](#dh-adr-context-3) |
| `proposed/feature/2026-07-06-recallable-compaction.md` | Proposal: frozen index checkpoints, one state checkpoint, tools to read compacted spans back | adopt in part, P2 | The log already holds every byte; recall needs a footer and two read-only tools | [ADR-10](#dh-adr-context-10) |
| `proposed/simplification/2026-09-19-provider-only-instruction-reads.md` | Proposal: read instruction files only through the filesystem provider | adopt | One read path works for local, sandboxed and remote workspaces | [ADR-2](#dh-adr-context-2) |
| `rejected/simplification/2026-07-12-prune-unused-skill-registry-api.md` | Rejected: remove direct run-time skill registration; kept as an extension path | change | The path stays (a static provider, a plugin call); the second code path goes | [ADR-7](#dh-adr-context-7) |
| `rejected/simplification/2026-07-19-fold-compaction-package-split.md` | Rejected: merge the contract into its one provider | keep the rejection | Three reducers already sit behind the contract | [ADR-4](#dh-adr-context-4) |
| Context is an ordinary sourced `user/message` (`agent-instructions/README.md:90`) | It replays, compacts and resumes like any history | keep | This is DH-O-4 for context | [ADR-1](#dh-adr-context-1) |
| Each context plugin has its own pre-step listener, ordered by registration and `prepend` (`time-context/README.md:67`; `tool-skill/README.md:89`) | Plugins compose independently | change | Message order is model-visible and must not depend on load order | [ADR-1](#dh-adr-context-1) |
| The filesystem service is optional; without it nothing loads (`agent-instructions/src/index.ts:120`) | Providerless products still start | change | A silent no-op hides a composition mistake; the seam is a required injection | [ADR-2](#dh-adr-context-2) |
| The log bracket is the lock; a failed close blocks on purpose (`compaction/README.md:73`) | A crash is detectable | change | In Go the driver goroutine and the session lease already exclude a second compaction | [ADR-4](#dh-adr-context-4) |
| The summary rides on a `user/message` replacement; `compaction/*` stay log-only (`compaction/README.md:74`) | Only message events reach the model | keep | It is the surface rule of area A01 | [ADR-4](#dh-adr-context-4) |
| The summariser replays the request prefix byte for byte (`compaction-basic/README.md:116`) | Reuse the provider's cache | keep | Cost, and it is what makes the request rebuildable from the log | [ADR-5](#dh-adr-context-5) |
| A summariser request is recorded only when it succeeds (`docs/subsystems/compaction.md:16`) | The record sits on `compaction/summary` | change | A failed call also sent user text and cost money | [ADR-5](#dh-adr-context-5) |
| Default headroom is 65,536 tokens whatever the window (`compaction-basic/README.md:80`, `:90`) | Room for a full output | change | A small-window route silently loses automatic compaction | [ADR-4](#dh-adr-context-4) |
| Project skill roots outrank user roots (`skill-filesystem/README.md:48-54`) | The repository's version of a skill wins | keep, labelled | Same rule as nested instruction files; the source class is shown | [ADR-7](#dh-adr-context-7) |
| `/name` anywhere in a user message loads a skill (`tool-skill/README.md:89`) | A lightweight gesture | change | A path such as `/office` in prose must not load instructions | [ADR-8](#dh-adr-context-8) |
| A session snapshot is framed as untrusted and tag-escaped (`session-reference/README.md:36`, `:69`) | Another session's text is data | keep | The same rule as tool output | [ADR-9](#dh-adr-context-9) |

## 5. Tech debt and its resolution

85 units: 82 README limits and 3 source markers. They are grouped by cause; each unit has its own ledger row in `trace/fragments/A14.jsonl` with the per-unit reasoning. Totals: 53 resolved, 30 carried, 2 not applicable.

| Group (cause) | Units | How the Go design answers it |
|---|---|---|
| **A. Files on disk are discovered, refreshed and parsed by private, fragile code** (21) | `agent-instructions` 1, 2, 4, 5, 6 and both markers; `file-reference-local` 3, 4; `tmux-context` 4; `skill-filesystem` 2 to 5 and its marker; `skill-office` 4 to 6; `skill` 1 to 3 | One `watch` service (area A23) replaces touch-only refresh, the private chokidar code and the polling of a missing root. Failures are contained per file and per skill, with a kept last-good value and a diagnostic, never per group or per root. Dropped candidates stay tracked and return when they fit. The project root is found once by shared, configurable markers and held. Providers are asked concurrently under a deadline and may declare a maximum age. Embedded skills cannot be missing. 20 resolved; 1 carried: `cd` inside a shell command still triggers no discovery, narrowed by counting a shell call's working-directory argument ([ADR-2](#dh-adr-context-2), [ADR-7](#dh-adr-context-7)). |
| **B. A boundary of trust or namespace is assumed, not enforced** (5) | `agent-instructions` 7; `file-reference` 1; `file-reference-local` 1; `session-reference` 3; `time-context` 4 | A project instruction file or project skill whose link resolves outside the project root is not read. Path candidates come from the filesystem seam the `read` tool uses. Session discovery and capture take the caller's principal and apply the access filter. A zone the user set is authority; the process zone is not. 5 resolved ([ADR-2](#dh-adr-context-2), [ADR-9](#dh-adr-context-9)). |
| **C. A budget is approximate, unshared or unbounded** (10) | `agent-instructions` 9; `tool-skill` 2, 5; `time-context` 1, 6; `file-reference-local` 2; `session-reference` 6; `compaction-basic` 1; pruner 1, 3 | Chains lend unused bytes above a guaranteed floor. A skill body has a size cap. The catalogue changes by delta. Clock state is fixed-size. A full transcript is plain text behind the spill store's read. 5 resolved; 5 carried and narrowed: the meter stays a calibrated estimate, prune budgets stay characters, a cut may still split a rare grapheme, the file index stays bounded but says when it is truncated, and clock readings still accumulate between compactions. |
| **D. Compaction fails, stalls or cannot shrink** (13) | `command-compact` 1 to 3; `compaction-basic` 2 to 8; `compaction` 2, 3; image offload 2 | The summariser falls back, on record, when its route cannot serve; a span too large for it is summarised in chunks; an oversized single unit is summarised from a pruned view; the same price basis selects and validates; repeated failure backs off; a region works on an idle session; images are planned before dispatch; the command waits for idle, takes a focus and a keep count, and is reachable from every carrier. 11 resolved; 2 carried: a system prompt plus tool list that alone exceeds the window is reported, not compacted ([ADR-4](#dh-adr-context-4), [ADR-5](#dh-adr-context-5), [ADR-6](#dh-adr-context-6)). |
| **E. The model or the user cannot tell what exists or what changed** (7) | `session-reference` 1, 2; `skill-filesystem` 6; `skill` 4; `tool-skill` 1, 3, 6 | The catalogue shows `whenToUse` and names skipped invalid skills; a loaded body's digest is recorded and a later change is announced; resources are listed; shadowed definitions are inspectable; sessions are found by body text and always have a title. 7 resolved ([ADR-8](#dh-adr-context-8), [ADR-9](#dh-adr-context-9)). |
| **F. Deliberate boundaries, kept on purpose** (21) | `agent-instructions` 3, 8; `time-context` 2, 3, 5; `tmux-context` 1, 2, 3, 5; `file-reference` 2; `session-reference` 4, 5; `compaction` 1; image offload 1, 3; pruner 2; `skill-filesystem` 1; `skill-office` 1 to 3; `tool-skill` 4 | Each is a guarantee or a scope limit the Go design keeps: no `@path` imports in instruction files, no model-written compression of instructions, no model-facing compaction tool, snapshots not live links, sibling panes never read, offloaded images never silently return, no installing of libraries. 21 carried, each with its justification in the ledger and a test that pins the behaviour where one applies. |
| **G. Limits of packages that are not built** (8) | `harness-dev-context` 1 to 6; `tool-workspace-dependencies` 1, 2 | [ADR-3](#dh-adr-context-3). 5 resolved (no earlier-format sessions, no such sections); 1 carried (a release that changes prompt text still starts a new request series on routes without in-history updates, a rule owned by A02); 2 not applicable (they describe upstream's payload build). |

Carried rows name stage [DH-S-47](../impl.md#dh-s-47), the known limits and scope boundaries of the core areas.

## 6. Go design

### 6.1 Shape

Packages sit under `src/harness/internal/context/` ([../design.md §4.2](../design.md#42-go-module-layout)). The directory itself holds no Go package, so nothing shadows the standard `context` package.

| Package | Component | Provides | Injects (required unless marked) | Why it exists |
|---|---|---|---|---|
| `context/inject` | `context.inject` | `inject.Key` (`Pipeline`) | agents (A02), projections (A06), `compact.Key` (optional) | The one `PreStep` listener of this area: compaction check, then contributors in a fixed order |
| `context/instructions` | `context.instructions` | a contributor | `inject.Key`, `fs.Key` (A08), `watch` (A23), working directory (A06) | Workspace instruction files |
| `context/clock`, `context/tmux` | `context.clock`, `context.tmux` | a contributor each | `inject.Key`; tmux also the shell seam (A09) | Time; terminal location (off by default) |
| `context/fileref` | `context.fileref` | `fileref.Key` (`Service`), the mention grammar, a prompt section | `fs.Key`, prompt (A03) | Path candidates for `@file` |
| `context/sessionref` | `context.sessionref` | `sessionref.Key` (`Resolver`), a contributor | `inject.Key`, query (A06), spill (A07, optional) | Session mentions and snapshots |
| `context/compact` | none (contract) | key `compact.Key` (`Engine`), event types, `compact.SummaryFailed`, edge helpers | | What every reducer and trigger shares |
| `context/compact/basic` | `compact.basic` | fills `compact.Key` | LLM runtime, meter and catalogue (A04), sessions (A01), `prune.Key` (optional) | Threshold policy and the summariser |
| `context/compact/prune` | `compact.prune` | `prune.Key` (`Pruner`) | meter | Model-free trimming of tool results |
| `context/compact/imageoffload` | `compact.imageoffload` | the `image/offload` type and its rewrite | agents, catalogue | Stable omission of old images |
| `context/compact/command` | `compact.command` | the `/compact` command | commands (A17), `compact.Key` | Manual compaction |
| `context/skill` | `skill.registry` | `skill.Key` (`Registry`), `skill.Changed`, `skill.Static` | | Merge, precedence, validation, diagnostics |
| `context/skill/fsprovider` | `skill.filesystem` | a provider | `skill.Key`, `fs.Key`, `watch` | Skills on disk |
| `context/skill/office` | `skill.office` | a provider over embedded files | `skill.Key` | The four Office skills |
| `context/skill/tool` | `skill.tool` | the `skill` tool, two contributors | `skill.Key`, tools (A03), `inject.Key` | What the model sees of skills |

A consumer never imports a provider; an import-direction test enforces it ([DH-ADR-ARCH-6](../upstream/architecture.md#dh-adr-arch-6)). Each contributor component is mounted globally or in a preset; mounted in a preset it registers through that preset's scope and serves only its agents.

```go
// inject: a contributor observes the world once and returns messages; it never appends.
type Contributor interface {
    Name() string; Required() bool // the source kind; whether a failure rejects the step or is skipped and recorded
    Contribute(ctx context.Context, in Input) (Output, error)
}
type Input struct {      // values and read-only views; Visible excludes shadowed messages
    Agent agent.Agent; Turn, Step int; Claimed []llm.UserMessage
    Selection agent.ModelSelection; Previous *agent.ModelSelection; Visible surface.HistoryView
}
type Output struct {     // Messages follow the claimed messages; After follows one of them; Reword makes a mention readable
    Messages []llm.UserMessage; After map[llm.MessageID][]llm.UserMessage; Reword map[llm.MessageID]string
}
type Pipeline interface { Register(owner *cordis.Context, c Contributor) (*cordis.EffectHandle, error) }

// compact: the seam, one provider per scope. Triggers: pressure, model-switch, overflow.
type Engine interface {
    Consider(ctx context.Context, a agent.Agent, t Trigger) (*Result, error)
    Now(ctx context.Context, a agent.Agent, o ManualOptions) (*Result, error) // Focus, KeepTurns, WhenBusy
    Region(ctx context.Context, a agent.Agent, first, last event.Seq, o ManualOptions) (*Result, error)
}

// skill: one provider path. skill.Static(defs...) wraps in-memory definitions as a provider.
type Registry interface {
    RegisterProvider(owner *cordis.Context, p Provider, o ProviderOptions) (*cordis.EffectHandle, error)
    Snapshot(ctx context.Context, v View) (Catalogue, error) // winners, shadowed, per-provider status
    Load(ctx context.Context, v View, name string, by Invoker) (Definition, error) // by: model | user
}
type Provider interface {
    List(ctx context.Context, l Lookup) (Observation, error)
    Load(ctx context.Context, l Lookup, c Candidate) (Body, error)
}
```

`ProviderOptions` holds the name, source class, rank, `MaxAge` (zero for a provider that invalidates itself) and a deadline. `Result` holds the compaction id, the summary and checkpoint sequence numbers, the shadowed sequence numbers, the estimated tokens freed and the outcome. `nil` with no error means nothing could be reduced and nothing was logged.

### 6.2 Contracts

**What reaches the model from this area.** One row per kind of text. "Recorded" names the session event; "Replay" says why no second observation is needed.

| Kind | What is injected, and when | Recorded as | Replay reproduces it because |
|---|---|---|---|
| Instruction baseline | Both chains in one framed message, at the first step of a session and again in the step after compaction shadowed it | `user/message`, source `instructions{form: baseline, root, files: [{path, scope, digest, bytes, op}]}` | The file text is in the message; the fold reads only the source record |
| Instruction change | "Additional", "updated" or "removed" for one file, at the step after a watch notice, a file-tool touch, a working-directory change or a resume | `user/message`, source `instructions{form: delta, ...}` | As above; digests say which version the model saw |
| Clock reading | Time, zone policy and elapsed time when the interval has passed | `user/message`, source `clock{at, zone, policy, elapsedMs}` | The sampled instant is in the record; replay calls no clock |
| Terminal location | tmux session, window, pane and layout at the first step of a turn, if changed | `user/message`, source `tmux{...}` | The command's parsed output is in the record |
| Session snapshot | Directly after the citing message: bounded text of each cited session, framed as untrusted | The claimed message with readable mention text, then `user/message`, source `session-reference{session, capturedThrough, bytes, omitted, locator}` | The snapshot text is in the message; the source session is never read again |
| File-mention guidance | A fixed system-prompt section while the agent has `read` | The rendered prompt, logged by A02 | It is part of prompt assembly (A03); no file is read |
| Skill catalogue | Names and capped descriptions before the first step; later a delta, or a whole list after compaction shadowed the baseline | `user/message`, source `skill-catalog{form, entries, removed, skipped, incomplete, digest}` | The entries are in the record; the fold is the catalogue state |
| Skill body | The `<skill_content>` block, when the model calls `skill` or the user invokes a skill | `tool/result` (fields: name, provider, class, digest, path), or `user/message`, source `skill-invocation{...}` | The body is in the event; a later edit of the file changes nothing already logged |
| Compaction checkpoint | The summary in place of a span | `user/message` with a `replace` operation and the replaced events as sources | Area A01 applies the replacement from the log |
| Pruned tool result | Head, marker (count and locator), tail | `tool/result` with a `replace` operation over one node | As above; the full result stays in the log |
| Image placeholder | Text naming an omitted image | `image/offload` (log-only) and its pure rewrite | Area A01 applies the versioned rewrite |

**Contributor order** is one table in package `inject`, not load order: instruction baseline, instruction changes, skill catalogue, clock, terminal location, then explicit skill invocations last, so background comes first and the text to act on comes last. A session snapshot is placed directly after the message that cited it.

**Instruction files.** User-global chain: `<harness home>/AGENTS.md`, then `<agents home>/AGENTS.md` (default `~/.agents`). Project chain: for each directory from the project root down to the session's working directory, the base candidates and then the local overlays. A more specific file is rendered later and the frame text says it takes precedence; no instruction file overrides system, developer or direct user messages. Files with the same content digest render once, from the first in order. A file is tracked as `visible`, `omitted` (did not fit), `duplicate`, `unreadable` (last-good kept) or `refused` (its link leaves the project root). When a file changes mid-session nothing in history is rewritten: the next step gets an "updated" message with the new content, and the earlier text stays in history as superseded.

**Compaction triggers.** `pressure` (the estimate crosses the threshold at a proposed step); `model-switch` (`PreStepRequest.Previous` names another route, and history is measured against the new one); `overflow` (area A04's repairable failure code, or, once per step, a rejected request while the estimate is at least 95% of the window, logged as `suspected-overflow`); `manual`. A system prompt and tool list that alone exceed the threshold are reported as `compact/envelope-too-large` with both sizes: compaction cannot shrink them.

**Compaction: one transaction.** It runs on the agent's driver goroutine, inside the area's `PreStep` or `AttemptFailed` listener or inside `RunMaintenance`.

| # | Step | Appended to the log |
|---|---|---|
| 1 | Qualify: measure through the meter (A04) against the route's window from the catalogue. Threshold `floor(min(W × thresholdRatio, W − O − B))`; `B` defaults to `min(65536, floor(W × 0.2))` | nothing |
| 2 | Plan, purely: tool results to prune, images to offload, and the span (first non-system node up to the retained tail, both cuts on balanced edges). An empty plan returns `nil` | nothing |
| 3 | Open | `compaction/start{id, trigger, origin, turn, route, pressure, threshold}` |
| 4 | Reduce without a model: prune, offload; measure again. If the request now fits, go to 7 with outcome `reduced` | per result `compaction/prune` then `tool/result` (replace); `image/offload` |
| 5 | Summarise: the request event is appended and flushed before the call is sent; each attempt settles as one event. A failure goes to `compact.SummaryFailed`; a repair listener may record a change and ask for one more request | `compaction/request{id, selection, instructionVersion, focus, route, cap, effort}`; `compaction/attempt{outcome, usage, failure, requestDigest}` |
| 6 | Validate: the selected nodes are still the current nodes; the summary is non-empty, complete and smaller than its source under the same price basis as step 2 | nothing |
| 7 | Commit as one group, then flush | `compaction/summary{id, text, shadowed, tokens, route}`, `user/message` (replace, source `compaction-checkpoint{id}`), `compaction/end{id, outcome}` |
| 8 | On any failure after step 3 | `compaction/end{id, outcome: failed, cancelled or changed, failure}`; reductions from step 4 stay |

**What is kept verbatim.** The system prompt node; the retained tail (`retainRatio`, 0.16 of `W − O`, or `retainTokens`); everything queued in the inbox; every message outside the span. Nothing is deleted: a replaced event stays in the log and can be read back.

**What happens when the summariser fails.**

| Trigger | Result | Model sees |
|---|---|---|
| Pressure | The step proceeds with full history. After `maxConsecutiveFailures` (3) on one route the engine stops trying until history shrinks or the route changes | Nothing; the failure is a log event and a UI notice |
| Model switch | `PreStep` returns `Reject{by: compaction, reason}`; the turn ends `blocked`; no header for the new route is logged | Nothing was sent |
| Overflow | `AttemptFailed` returns `GiveUp`; the turn ends `error` with the provider's original failure | Nothing more |
| Manual | The command returns one of `compact/busy`, `compact/cancelled`, `compact/changed`, `compact/summary-failed`, `compact/persistence` | Nothing; command events are log-only |

**Events.**

| Upstream unit | Go key and form | Request and result |
|---|---|---|
| `UP-EVT-compaction/summary-error` | `compact.SummaryFailed`, around | `{Session, Compaction, Selection, Failure}` to `GiveUp{}` or `Repaired{}` (a durable change was recorded; build a new request) |
| `UP-EVT-skills/change` | `skill.Changed`, notify | `{Scope, Revision}`; sent after a provider invalidation or registration commits |

**Config.** Go structs with defaults as a Go value, decoded strictly ([DH-ADR-SCHEMA-1](../libs/zod.md#dh-adr-schema-1), [DH-ADR-SCHEMA-3](../libs/zod.md#dh-adr-schema-3)).

| Upstream unit | Go struct | Fields (default) |
|---|---|---|
| `UP-CFG-dsh-agent-instructions` | `instructions.Config` | `maxBytes` per chain, a floor (65536); `maxSourceBytes` (1 MiB); `maxTotalSourceBytes` (4 MiB); `projectRootMarkers` (`.git`); `candidates` (`AGENTS.md`, `CLAUDE.md`); `localCandidates`; `agentsHome` |
| `UP-CFG-dsh-time-context` | `clock.Config` | `timeZone` (the user's setting, else the process zone); `refreshInterval` (10m; 0 means every step); `maxZonesPerTurn` (4) |
| `UP-CFG-dsh-tmux-context` | `tmux.Config` | `refreshInterval` (0: whenever changed) |
| `UP-CFG-dsh-file-reference-local` | `fileref.Config` | `maxResults` (20); `maxEntries` (50000); `excludedDirectories` (upstream's list); `respectIgnoreFiles` (true) |
| `UP-CFG-dsh-session-reference` | `sessionref.Config` | `maxReferences` (3, at most 3); `candidateLimit` (50); `maxReferenceBytes`; `referenceContextFraction` (0.2) |
| `UP-CFG-dsh-compaction-basic` | `basic.Config` | `auto` (true); `thresholdRatio` (0.8); `headroomTokens` (auto); `retainRatio` (0.16) or `retainTokens`; `summarizer.provider`, `.model`, `.maxTokens`, `.reasoningEffort` (lowest); `compactionRetries` (1); `maxOverflowRetries` (1); `maxConsecutiveFailures` (3); `modelPolicies[]` |
| `UP-CFG-dsh-compaction-tool-result-pruner` | `prune.Config` | `thresholdChars` (8192); `headChars` (4096); `tailChars` (1024); head, marker and tail must fit the threshold |
| `UP-CFG-dsh-skill` | `skill.Config` | `cacheEntries` (128); `providerTimeout` (5s) |
| `UP-CFG-dsh-skill-filesystem` | `fsprovider.Config` | `includeProjectRoots` (true); `includeUserRoots` (true); `customDirs`; `agentsHome`; `projectRootMarkers` (`.git`); `watch` (true); `maxProjects` |
| `UP-CFG-dsh-skill-office` | `office.Config` | `assetRoot` (the embedded set); `python` (`user`, or an absolute interpreter path) |
| `UP-CFG-dsh-tool-skill` | `skilltool.Config` | `descriptionMaxLength` (500); `maxBodyBytes` (262144); `maxResourceEntries` (50) |
| `UP-CFG-dsh-tool-workspace-dependencies` | none | Not built; `office.Config.python` carries the one choice it expressed ([ADR-3](#dh-adr-context-3)) |

**Services.**

| Upstream unit | Go replacement |
|---|---|
| `UP-SVC-compaction` | Key `compact.Key`, interface `Engine` |
| `UP-SVC-toolResultPruner` | Key `prune.Key`: `Measure(blocks) int`, `PruneContent(blocks) ([]Block, bool)`, `PruneSession(ctx, session) (PruneResult, error)` |
| `UP-SVC-fileReferences` | Key `fileref.Key`: `List(ctx, agent, query) ([]Candidate, Truncated, error)` |
| `UP-SVC-sessionReferenceResolver` | Key `sessionref.Key`: `Candidates(ctx, principal, agent, query, mode)`, `Prepare(ctx, principal, agent, refs, route)` |
| `UP-SVC-skills` | Key `skill.Key`, interface `Registry` |

**Persisted data.** Log-only event types, each with a payload version: `compaction/start`, `compaction/request`, `compaction/attempt`, `compaction/summary`, `compaction/prune`, `compaction/end`, `image/offload` (a rewrite type, [core-session.md ADR-6](core-session.md#dh-adr-session-6)) and the ignorable `context/skipped{contributor, error}`. `compaction/request` declares the pure builder that [core-session.md ADR-7](core-session.md#dh-adr-session-7) needs for `RequestFrom`. Registered folds, all bounded: `instructions.visible`, `clock.state`, `tmux.last`, `skills.catalogue`, `skills.loaded` and `compaction.state` (the answering route, consecutive failures, an unclosed bracket).

**Tools exposed to the model.**

| Upstream unit | Go tool | Contract |
|---|---|---|
| `UP-TOOL-dsh-tool-skill.skill` | `skill` | Input `{name}`. Result: the `<skill_content>` block with name, source class, a listing of at most `maxResourceEntries` bundle files, and the body. `Effects: none`; may overlap. Errors: `skill/invalid-name`, `skill/not-found`, `skill/not-model-invocable`, `skill/too-large` (names the size and the path to read in parts), `skill/read-failed` |
| `UP-TOOL-dsh-tool-workspace-dependencies.load_workspace_dependencies` | none | Not built ([ADR-3](#dh-adr-context-3)) |
| (new, P2) | `history_read`, `history_search` | Read-only: render or search the events one checkpoint replaced, paged; results are ordinary tool results ([ADR-10](#dh-adr-context-10)) |

**Skill trust, by source class.**

| Class | Where it comes from | Shadowed by | Extra rule |
|---|---|---|---|
| `project` | `<project>/.dsh/skills`, `<project>/.agents/skills`: arrives with a repository | nothing | A link that leaves the project root is refused; the loaded block says the text comes from the workspace, not from the user |
| `custom` | Directories the operator configured | `project` | None |
| `user` | `<harness home>/skills`, `<agents home>/skills` | `project`, `custom` | Links are followed: it is the user's own configuration |
| `plugin` | A plugin process, over the plugin protocol | by rank | Needs a capability grant (A22); labelled like `project` |
| `embedded` | Compiled into the binary | all | Cannot be missing or edited |

Whatever its class, a skill is text. Loading one reads one file and starts nothing. It cannot register a tool, change a tool's schema, widen the sandbox, answer an approval, create a session grant, add a prompt section or select a model, and front matter that claims any of these (for example an `allowed-tools` key) is ignored and reported as a diagnostic. A script in a bundle runs only if the model calls the shell tool, and then under the same pipeline, sandbox and approvals as any command ([../design.md §6](../design.md#6-security-model)).

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership.** Each component's fiber owns its registrations: contributors, providers, the tool, the command, watches. An agent's scope owns per-agent state (a file index, watch subscriptions) and releases it when the agent is disposed. The log owns every injected message once it is committed; a contributor that unloads leaves its messages in history, which is correct, and nothing "un-injects". **Access:** any component may read a skill or list file candidates; only the agent's driver appends, through the `PreStep` decision or the compaction transaction.

| Situation | What happens | Undone, and what stays uncertain |
|---|---|---|
| An optional contributor fails or panics | It is skipped for this step; `context/skipped` is logged; the others run | Nothing; it runs again at the next step |
| A required contributor fails (a cited session cannot be captured) | `PreStep` returns `Reject`; the turn ends `blocked` with the reason | The claimed message is recorded as claimed and unrun (A02) |
| A later listener rejects the step after contributors ran | The messages are not entered | Their observations are discarded; nothing was logged |
| An instruction file or skill is unreadable | That one item keeps its last-good value and gets a diagnostic; siblings proceed | Nothing |
| The watch service reports polling mode, or the workspace is remote | Loaded files are checked by metadata at each proposed step | A change is seen one step late at worst |
| The host dies inside a compaction | On write-open the session core closes the turn; the compaction component then appends `compaction/end{interrupted}` for an unclosed bracket | The surface is whole: step 7 is one group. Prunes from step 4 stay. **Uncertain:** whether the provider billed the unsettled summariser attempt |
| The commit group is refused (lease lost, disk) | Nothing of step 7 is applied; the error is returned | Resume closes the bracket as above |
| The flush after a manual compaction fails | `compact/persistence`; the in-memory session holds the result | **Uncertain:** durability, until the next flush succeeds |
| A summariser call is cancelled | The attempt settles as `cancelled`; `compaction/end{cancelled}` | Recorded reductions stay |
| A skill provider times out or fails | Its last-good catalogue is kept and marked stale; the catalogue says discovery was incomplete | A lower-ranked skill never takes over a name because its owner flickered |
| A skill file changes after it was loaded | The next step's catalogue contributor adds a one-line notice naming the skill | The logged body is unchanged |
| A plugin process that provided skills exits | Its provider is unregistered with its fiber; a delta removes its names | Bodies already loaded stay in history |

No operation in this area is retried without a record: a summariser retry is a new `compaction/attempt`, bounded by the LLM retry policy (A04) and by `compactionRetries`.

## 7. Decisions

<a id="dh-adr-context-1"></a>
### DH-ADR-CONTEXT-1 Context is a logged, sourced message produced by one ordered pipeline
- **Decision.** This area registers one `PreStep` listener. It runs the compaction check, then the contributors in a fixed order from one table. A contributor returns user-role messages whose source carries what it observed; the driver enters them in the step's admission batch. A contributor decides what to add by comparing its observation with its *visible state*, a fold over its own messages on the current surface. No per-process value (a port, a checkout path, a process id) may enter the system prompt; changing facts are context. A contributor mounted in a preset serves only that preset's agents.
- **Why.** [DH-O-4](../design.md#dh-o-4). Recording the observation in the message is what lets a replay skip the file, the clock and the other session. Comparing with the current surface means a baseline that compaction shadowed is restored in the same step, with no special case. One listener makes model-visible order independent of load order, and puts compaction before the comparison.
- **Tradeoff.** Context costs history tokens until compaction, unlike a system-prompt section. Prompt assembly (A03) stays a pure function, so anything that needs input/output must be a contributor. A new contributor needs a line in the order table.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-3](../design.md#dh-o-3), [DH-O-1](../design.md#dh-o-1). **Verified by.** `DH-T-CONTEXT-1` to `-3`, [DH-E-CONTEXT-1](#dh-e-context-1).

<a id="dh-adr-context-2"></a>
### DH-ADR-CONTEXT-2 Instruction files: one read path, two chains with a floor, contained links, watched
- **Decision.** Files are found and read only through the filesystem seam, which is a required injection. Two chains with the order of section 6.2. Each chain is guaranteed `maxBytes`; bytes one chain does not use are lent to the other; a total source budget bounds reading before rendering. Within a chain the broadest files are dropped first and the most specific is truncated last. A project-chain file whose resolved path leaves the project root is refused and reported; user-global files may link anywhere. The project root is found once per working directory and recorded in the baseline. Loaded files are watched; file-tool touches, a working-directory change and a resume also trigger reconciliation. So does a changed `cwd` in the result of a persistent shell tool ([execution.md](execution.md), `DH-ADR-EXEC-6`): a `cd` in a persistent shell loads the instruction files of the new directory at the next step (register entry U-K1-10). A `cd` that begins and ends inside one command is seen by no observer outside the shell; that remainder is a platform limit. No `@path` imports, no rule directories, no summarising of instruction text.
- **Why.** One read path gives local, sandboxed and remote workspaces the same behaviour. A cloned repository must not be able to surface a file outside itself as guidance. A floor keeps the user's own rules; lending removes the waste upstream documents.
- **Tradeoff.** A project that legitimately links its `AGENTS.md` outside the repository is refused and must copy the file. Watching costs one watch per loaded file. Instruction text is still injected text: it can steer the model inside what the sandbox and approvals allow.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-CONTEXT-4` to `-6`, [DH-E-CONTEXT-2](#dh-e-context-2).

<a id="dh-adr-context-3"></a>
### DH-ADR-CONTEXT-3 Two packages are not built: the development context and the bundled-runtime locator
- **Decision.** `harness-dev-context` and `tool-workspace-dependencies` (with its tool `load_workspace_dependencies`) are dropped. `tmux-context` is **not** dropped: it is kept as an off-by-default contributor at P2 (recommended default, not confirmed by the owner; if the owner drops terminal location too, its rows become a drop on this record).
- **Why.** Inference from the package's own text: the development context exists only for working on upstream itself. Its text names upstream's checkout, its `pnpm` watcher and its client reload, and most of its code keeps earlier-format upstream sessions byte-stable (`packages/context/harness-dev-context/README.md:36`, `:92`). The Go store reads no upstream session ([DH-ADR-11](../design.md#dh-adr-11)). The locator reports paths into a Python, Node.js and pnpm payload that upstream's desktop application and SDK wheel carry; the Go product is one pure-Go binary with nothing bundled or downloaded ([DH-ADR-15](../design.md#dh-adr-15), [DH-ADR-18](../design.md#dh-adr-18), [DH-ADR-20](../design.md#dh-adr-20)), so the tool would describe an artifact nobody builds. Inference: tmux location is an opt-in user feature for a command-line session inside tmux, not a development aid; no shipped upstream profile mounts it, and the command-line application lists it as a dependency (`apps/cli/package.json:76`).
- **Tradeoff.** An agent asked to modify the harness gets no built-in hint about its own checkout. The Office skills can only point at the user's Python, or at one interpreter path the operator configures; authoring libraries are the user's to install. Two recorded exceptions to [DH-ADR-1](../design.md#dh-adr-1).
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-16](../design.md#dh-o-16). **Verified by.** The ledger rows for both packages, the tool and its config link here; `DH-T-CONTEXT-21`.

<a id="dh-adr-context-4"></a>
### DH-ADR-CONTEXT-4 Compaction is a seam whose result commits as one group; the bracket is a record, not a lock
- **Decision.** Contract and providers stay separate. Capacity comes from the exact route (A04's catalogue); thresholds are per-route policy of the provider; headroom defaults to `min(65536, 20% of W)`. All work runs on the agent's driver goroutine, so two compactions of one session cannot overlap, and the session lease excludes another process. The summary record, the replacing message and the closing event are one `AppendGroup`. An unclosed bracket found on resume is closed as `interrupted`. The summary rides on a `user/message` replacement and the system prompt node is never in a span. There is no model-facing compaction tool.
- **Why.** Upstream needs a log-recorded lock because nothing else serialises its entry points, and pays for it with a "commit may follow partial mutation" outcome and an orphan that blocks. A group append makes "all or nothing" a property of the store. A model that could compact at will could discard the history a user relies on.
- **Tradeoff.** A summariser call holds the driver: prompts queue until it settles or is cancelled. `Region` on a running agent is only possible from inside its own turn.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-3](../design.md#dh-o-3), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-CONTEXT-11` to `-13`, [DH-E-CONTEXT-3](#dh-e-context-3).

<a id="dh-adr-context-5"></a>
### DH-ADR-CONTEXT-5 The summarising call is a logged, attributed model request with bounded failure
- **Decision.** Before a summariser request is sent, `compaction/request` is appended and flushed; it holds everything needed to rebuild the request from the log (the selection, the instruction version, the focus text, the route, the cap). Each attempt settles as exactly one `compaction/attempt` with usage or failure and the request digest. The call carries the session, the agent reference, the purpose `compaction` and the compaction id. The request replays the logged prefix byte for byte and uses the lowest reasoning effort. Route order: configured; the route of the latest assistant message; the latest request header; the session's selected route. The engine moves to the next candidate only on a permanent failure of the earlier one, only to the route the user selected for this session, and records why. A span too large for the summariser is cut at balanced edges and summarised left to right, each call given the previous partial summary; a single unit too large is summarised from a pruned view of it. Nothing commits until the last call succeeds. Failure per trigger is the table in section 6.2.
- **Why.** The summariser sees the user's conversation and costs money: [DH-O-4](../design.md#dh-o-4) and [DH-O-8](../design.md#dh-o-8) apply to it as to any request. The selected route is about to receive the whole history anyway, so using it crosses no new trust boundary, and the fallback is on record, not silent.
- **Tradeoff.** Two or more extra log events per compaction. Chunked summaries are lossier than one pass and cost more calls. A summary of a pruned view says so in its checkpoint.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-8](../design.md#dh-o-8), [DH-O-3](../design.md#dh-o-3). **Verified by.** `DH-T-CONTEXT-14`, `-15`, [DH-E-CONTEXT-3](#dh-e-context-3), [DH-E-CONTEXT-4](#dh-e-context-4).

<a id="dh-adr-context-6"></a>
### DH-ADR-CONTEXT-6 Model-free reductions run first, and each is a logged replacement or a versioned rewrite
- **Decision.** Pruning runs only after a trigger qualifies. A pruned result keeps head and tail, cut at line boundaries where one lies inside the budget and otherwise never before a combining mark, a variation selector or a joiner; its marker states how much was removed and, when area A07 spilled the result, where the full text is. Image offload is a log-only event with a pure rewrite bound to its payload version. Where the route's catalogue entry declares an image budget, the offload is planned before the request is sent; the provider's rejection remains the signal for an undeclared budget. An offloaded image is never restored automatically.
- **Why.** A reduction that needs no model call cannot fail for a model's reasons and often makes the summary unnecessary. Planning removes one failed attempt per offload. Automatic restore would make the visible image set depend on unlogged request preparation, which the upstream note rejects for the same reason.
- **Tradeoff.** Pruning is syntactic: an important middle line is lost from context, though not from the log. Prune budgets are counted in characters, not tokens, by this decision: a character count is the same for every route and needs no model-specific estimate, at the price of being only roughly proportional to tokens. A brief small-budget route offloads for good; the placeholder's path lets the model read the image again as a new occurrence.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-8](../design.md#dh-o-8). **Verified by.** `DH-T-CONTEXT-16`, `-17`.

<a id="dh-adr-context-7"></a>
### DH-ADR-CONTEXT-7 A skill is data with the trust of tool output, served through one provider path
- **Decision.** The registry has one way in, `RegisterProvider`. `skill.Static` wraps in-memory definitions; a plugin process provides skills through a granted protocol call. Providers are asked concurrently under a deadline. The registry keeps each provider's last-good catalogue and status, lists shadowed definitions, and re-lists a provider past its `MaxAge`. Precedence is upstream's (nearest scope, then rank, with project roots first), and every entry carries its source class. The rules of the trust table in section 6.2 hold for every class. The four Office skills are embedded in the binary; their text names the Go office tools of area A16 and is written for this project, not copied from upstream, whose licence for that prose was not checked (recommended default, not confirmed by the owner). `skill.Config.Disabled` is a live list of skill names that `Snapshot` leaves out, whatever their source; the settings page edits it, and a disabled skill is listed there with that reason. Two boundaries belong to this decision: discovery reads one directory level under each root, so a nested bundle is found only when its parent is a root; and the Office skills do not cover encrypted or macro-enabled documents, which the office tools refuse.
- **Why.** Upstream keeps a second registration path only so that a plugin can contribute a skill; a provider helper and a protocol call keep that ability with one code path. A skill arrives from a repository or a third party, so it gets what [../design.md §6](../design.md#6-security-model) gives fetched content: it is read, never obeyed by the host. Embedding removes the "file missing after install" class of failure.
- **Tradeoff.** A project skill can shadow a user or embedded skill by name, as upstream; the label and the shadow list make it visible but do not stop it. Changing an embedded skill needs a release, or a same-name skill in a user directory.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7), [DH-O-1](../design.md#dh-o-1). **Verified by.** `DH-T-CONTEXT-19` to `-21`, `-23`, [DH-E-CONTEXT-5](#dh-e-context-5).

<a id="dh-adr-context-8"></a>
### DH-ADR-CONTEXT-8 The catalogue is a fold with deltas; the tool enforces invocation policy; explicit invocation is structured
- **Decision.** The catalogue the model was shown is a fold over catalogue messages on the current surface. A change publishes a delta (added, changed, removed) when that is smaller than the list, and the whole list otherwise. Entries show name, capped description and `whenToUse`; skipped invalid skills are named; incomplete discovery is stated. The `skill` tool checks the invocation policy itself, in the pipeline, not by hiding names. A body over `maxBodyBytes` is refused with its size and path, never truncated. The loaded digest is recorded, and a later change of that body is announced once. A user invokes a skill through a structured reference on the message (the composer's chip, an SDK field); in plain text only a `/name` that is the first token of the message counts, and a registered command of the same name wins.
- **Why.** Hiding is not enforcement ([../design.md §6](../design.md#6-security-model), rule 2). Half an instruction file is worse than none. A whole-list replacement costs tokens in proportion to the catalogue for a one-word change.
- **Tradeoff.** Deltas make catalogue state depend on a fold, which a golden test pins. A user who types `/name` mid-sentence gets no injection.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-CONTEXT-22`, `-23`, [DH-E-CONTEXT-5](#dh-e-context-5).

<a id="dh-adr-context-9"></a>
### DH-ADR-CONTEXT-9 A reference is a pointer or a snapshot, never a live link
- **Decision.** A file mention stays text: candidates come from the filesystem seam of the session's workspace, honour ignore files, and are updated from `fs.Observed`; content enters only when the model reads the file. The local provider is merged into one component over that seam. A session mention is captured once when its message enters a step: text of direct user messages, assistant text and compaction checkpoints only, bounded per source, framed as untrusted with the frame characters escaped. Discovery and capture take the caller's principal and return only sessions it may read. A truncated capture is saved in full through the spill store as plain text with a per-capture boundary token in each header.
- **Why.** A snapshot in the log is what keeps the target's replay independent of the source. Using the workspace's own filesystem seam removes the "host namespace differs from the tool's" class of mistake.
- **Tradeoff.** A referenced session that moves on is not followed; the user mentions it again. Images and tool results do not cross sessions. The file-mention index is bounded by this decision: it holds at most a fixed number of paths per workspace and says when it is incomplete, so a huge repository costs bounded memory and a mention of an unindexed path still works as plain text.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-CONTEXT-9`, `-10`.

<a id="dh-adr-context-10"></a>
### DH-ADR-CONTEXT-10 Compacted history stays reachable; recall tools are later scope
- **Decision.** From the first build, a checkpoint ends with a footer written by code (the compaction id and the number of messages replaced) and `compaction/summary` lists the replaced sequence numbers. At P2 two read-only tools, `history_read` and `history_search`, render or search the events a checkpoint replaced in the caller's own session. The proposal's split into frozen index checkpoints and one state checkpoint is a second provider on the same seam and is not designed here.
- **Why.** The footer and the list cost nothing now and are what recall needs later. The split is a prompt-cache optimisation whose value is unmeasured.
- **Tradeoff.** Until P2 the model cannot read a compacted span back; the user can, through the session views of area A06.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-CONTEXT-24`.

<a id="dh-adr-context-11"></a>
### DH-ADR-CONTEXT-11 The clock and the terminal location say little, on purpose
- **Decision.** Recommended default, not confirmed by the owner. The clock reading is the time in whole seconds, the elapsed time since the previous reading, and one zone. The zone is the one the carrier supplied for the message: the browser's zone from the composer, or the `timeZone` an SDK program gave at `initialize` or at session creation ([sdk-acp-typert.md](sdk-acp-typert.md); register entry U-K2-5), then the user's setting. When the messages of one turn carry different zones, or none is known, the reading says so and tells the model to ask; the host process's zone is never used silently, and no tool takes a hidden zone argument. An ACP session has no zone field and keeps "unavailable". The terminal location is read on the first step of a turn only, for the host's own pane only, and reports session, window, pane and layout, not sizes.
- **Why.** Each reading costs history tokens on every session that mounts it, and a wrong zone is worse than none: "tomorrow at nine" and a schedule created from it would be off by hours.
- **Tradeoff.** No sub-second time, no pane sizes, no other panes. A user in two zones is asked once per turn in which they differ.
- **Serves.** [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-CONTEXT-7`, `DH-T-CONTEXT-8`.

## 8. Deliverables

<a id="dh-d-context-1"></a>
### DH-D-CONTEXT-1 Context reaches the model through one logged, ordered pipeline
- **Result and why it matters.** Any component can add context to the next request, and every such message is in the log with its observation. Without it each feature would invent its own injection and DH-O-4 could not be checked in one place.
- **How it is implemented.** One around listener on `agent.PreStep` that calls downstream first and, if the step is entered, runs the compaction check and then the contributors against a read-only view of the current surface; outputs are merged by the order table into `Enter.Messages`; failures are contained per contributor and recorded as `context/skipped`. Source kinds are registered with payload structs.
- **Components and files.** `context/inject`. **Depends on.** [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3). [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4): `PreStep` with `Enter{Messages}`; a listener may append before `step/start`. [DH-D-LLM-11](llm.md#dh-d-llm-11): the open message source with a typed payload. [DH-D-SESSION-3](core-session.md#dh-d-session-3): `History()` is the current-surface view (shadowed nodes are not in it) and `surface.Classify` answers for any event.
- **Priority.** P0. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-3](../design.md#dh-o-3), [DH-O-1](../design.md#dh-o-1). **Done when.** `DH-T-CONTEXT-1` to `-3` and `-25` pass and [DH-E-CONTEXT-1](#dh-e-context-1) has passed.

<a id="dh-d-context-2"></a>
### DH-D-CONTEXT-2 The agent follows the workspace's and the user's instruction files, and notices when they change
- **Result and why it matters.** A repository tells the agent its build and test commands once, in a file, and the user's own rules apply everywhere.
- **How it is implemented.** Discovery and bounded reads through the filesystem seam; digests; the `instructions.visible` fold; a pure renderer with the frame, the budget rules and frame-escape; reconciliation driven by watch notices, `tools.Settled` touches, working-directory changes and resume.
- **Components and files.** `context/instructions`. **Depends on.** [DH-D-CONTEXT-1](#dh-d-context-1). [DH-D-FS-4](filesystem.md#dh-d-fs-4) (filesystem seam with a rooted resolve), [DH-D-UTIL-3](util-test-support.md#dh-d-util-3) (`watch`), [DH-D-VIEW-10](session-views.md#dh-d-view-10) (working directory), [DH-D-EXEC-9](execution.md#dh-d-exec-9) (a shell call's working-directory argument as a touch).
- **Priority.** P0. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Done when.** `DH-T-CONTEXT-4` to `-6` pass and [DH-E-CONTEXT-1](#dh-e-context-1) and [DH-E-CONTEXT-2](#dh-e-context-2) have passed.

<a id="dh-d-context-3"></a>
### DH-D-CONTEXT-3 The model knows the time
- **Result and why it matters.** "Tomorrow at nine" is interpreted in the user's zone, and the model asks when zones conflict.
- **How it is implemented.** A contributor with a fixed-size fold ([ADR-11](#dh-adr-context-11)). The clock uses the injected clock and the standard library's zone database; the zone comes from the message's client record, then the user's setting.
- **Components and files.** `context/clock`. **Depends on.** [DH-D-CONTEXT-1](#dh-d-context-1). [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4) (preset mounting). **Used by.** The composer, whose browser zone arrives on the user message ([DH-D-UICHAT-1](ui-chat.md#dh-d-uichat-1)).
- **Priority.** P1. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-CONTEXT-7` passes. The clock needs no real environment: virtual time is the only input that matters.

<a id="dh-d-context-11"></a>
### DH-D-CONTEXT-11 The model can be told where its terminal is
- **Result and why it matters.** A command-line session inside tmux can name its own pane to the model; off by default.
- **How it is implemented.** A contributor with a fixed-size fold ([ADR-11](#dh-adr-context-11)) that runs one read-only command through the shell seam with a separator a tmux name cannot contain.
- **Components and files.** `context/tmux`. **Depends on.** [DH-D-CONTEXT-1](#dh-d-context-1), [DH-D-EXEC-8](execution.md#dh-d-exec-8) (shell seam).
- **Priority.** P2. **Outcome.** [DH-O-4](../design.md#dh-o-4). **Done when.** `DH-T-CONTEXT-8` passes and [DH-E-CONTEXT-6](#dh-e-context-6) has passed.

<a id="dh-d-context-4"></a>
### DH-D-CONTEXT-4 A user can point at a file or at another session
- **Result and why it matters.** Completion offers real paths of the workspace the tools act on, and a mention of another session brings its gist without copying its instructions' authority.
- **How it is implemented.** [ADR-9](#dh-adr-context-9): a per-agent bounded index over the filesystem seam with tiered fuzzy ranking ([../libs/fuzzysort.md](../libs/fuzzysort.md)); the mention grammar as a pure package; the resolver as a required contributor with capture, retention, framing and the optional spill.
- **Components and files.** `context/fileref`, `context/sessionref`. **Depends on.** [DH-D-CONTEXT-1](#dh-d-context-1). [DH-D-FS-4](filesystem.md#dh-d-fs-4) and [DH-D-FS-5](filesystem.md#dh-d-fs-5) (seam, ignore rules, `fs.Observed`), [DH-D-VIEW-5](session-views.md#dh-d-view-5) (query and titles), [DH-D-STORAGE-5](storage.md#dh-d-storage-5) (spill with read). The caller's principal comes from the carrier. Session candidates are found by title here. **Used by.** Candidates and chips are shown by [DH-D-UICHAT-3](ui-chat.md#dh-d-uichat-3); search by words of messages extends it in [DH-D-CONTEXT-12](#dh-d-context-12).
- **Priority.** P1. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Done when.** `DH-T-CONTEXT-9`, `-10` pass.

<a id="dh-d-context-12"></a>
### DH-D-CONTEXT-12 A session can be mentioned by words of its messages
- **Result and why it matters.** A user who remembers what was said, not what the session was called, still finds it in the `@` candidates.
- **How it is implemented.** The session candidate source asks A06's search for message bodies in ranked mode, within the caller's visibility, and merges the hits below title matches, one row per session.
- **Components and files.** `context/sessionref`. **Depends on.** [DH-D-CONTEXT-4](#dh-d-context-4), [DH-D-VIEW-6](session-views.md#dh-d-view-6) (full-text search).
- **Priority.** P2. **Outcome.** [DH-O-4](../design.md#dh-o-4). **Done when.** `DH-T-CONTEXT-26` passes.

<a id="dh-d-context-5"></a>
### DH-D-CONTEXT-5 A long session keeps working: history is summarised under pressure, on a model switch and after an overflow
- **Result and why it matters.** A session does not end at the provider's limit, and what was summarised can be audited.
- **How it is implemented.** The contract package; the basic provider with the transaction of section 6.2; per-route policy; the summariser as an auxiliary request built by `RequestFrom`; chunking; route fallback; back-off; the `AttemptFailed` listener for overflow; the `compaction.state` fold; closing an interrupted bracket on resume.
- **Components and files.** `context/compact`, `context/compact/basic`. **Depends on.** [DH-D-CONTEXT-1](#dh-d-context-1). [DH-D-SESSION-2](core-session.md#dh-d-session-2), [DH-D-SESSION-3](core-session.md#dh-d-session-3) and [DH-D-SESSION-4](core-session.md#dh-d-session-4) (`AppendGroup` over log-only and surface drafts, replace, `RequestFrom`); [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4) and [DH-D-AGENT-1](agent-loop.md#dh-d-agent-1) (`AttemptFailed`, `PreStepRequest.Previous`, `RunMaintenance`); [DH-D-LLM-6](llm.md#dh-d-llm-6), [DH-D-LLM-4](llm.md#dh-d-llm-4) and [DH-D-LLM-3](llm.md#dh-d-llm-3) (meter, catalogue, failure classes, `Runtime.Complete` on a request whose `Meta` carries the purpose). Runtime prerequisite: a route whose window is known.
- **Priority.** P1. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-3](../design.md#dh-o-3), [DH-O-8](../design.md#dh-o-8), [DH-O-10](../design.md#dh-o-10). **Done when.** `DH-T-CONTEXT-11` to `-15` pass and [DH-E-CONTEXT-3](#dh-e-context-3) and [DH-E-CONTEXT-4](#dh-e-context-4) have passed.

<a id="dh-d-context-6"></a>
### DH-D-CONTEXT-6 Oversized tool output and old images are reduced without a model call
- **Result and why it matters.** Most pressure comes from a few huge results; trimming them is free and often enough.
- **How it is implemented.** [ADR-6](#dh-adr-context-6): a pure prune function and a session pass that appends replacements; the `image/offload` type with its rewrite; the planner and the two repair listeners.
- **Components and files.** `context/compact/prune`, `context/compact/imageoffload`. **Depends on.** [DH-D-CONTEXT-5](#dh-d-context-5). [DH-D-SESSION-6](core-session.md#dh-d-session-6) (rewrite types; an oversized node is replaced by a node of its own role, as the surface rules require), [DH-D-LLM-4](llm.md#dh-d-llm-4) (image budget in the catalogue, the repairable failure), [DH-D-STORAGE-6](storage.md#dh-d-storage-6) (the spill locator on a result).
- **Priority.** P1. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-8](../design.md#dh-o-8). **Done when.** `DH-T-CONTEXT-16`, `-17` pass and [DH-E-CONTEXT-3](#dh-e-context-3) has passed.

<a id="dh-d-context-7"></a>
### DH-D-CONTEXT-7 A person can compact a session on demand
- **Result and why it matters.** Before a long task the user frees context deliberately, with a say in what is kept.
- **How it is implemented.** A host-side command `/compact [focus text] [keep=N]` that calls `Engine.Now` under `RunMaintenance`; while a turn runs it waits for idle unless told not to; a closed table of error codes; log-only command events that name the compaction id.
- **Components and files.** `context/compact/command`. **Depends on.** [DH-D-CONTEXT-5](#dh-d-context-5). [DH-D-HUMAN-6](interaction-credentials.md#dh-d-human-6) (the command registry, reached from the HTML UI, the SDK protocol and the command line).
- **Priority.** P1. **Outcome.** [DH-O-3](../design.md#dh-o-3), [DH-O-13](../design.md#dh-o-13). **Done when.** `DH-T-CONTEXT-18` passes.

<a id="dh-d-context-8"></a>
### DH-D-CONTEXT-8 Skills from the project, the user and the binary are found, merged and kept current
- **Result and why it matters.** One lookup answers "which skills exist here, from where, and why is this one hidden".
- **How it is implemented.** [ADR-7](#dh-adr-context-7): the registry; the filesystem provider (one level deep, front matter through the YAML module of [../libs/yaml.md](../libs/yaml.md), per-entry containment, watch at depth 1); the embedded Office provider over `embed.FS` with a start-up check that all four skills parse.
- **Components and files.** `context/skill`, `context/skill/fsprovider`, `context/skill/office`. **Depends on.** [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3). [DH-D-FS-4](filesystem.md#dh-d-fs-4), [DH-D-UTIL-3](util-test-support.md#dh-d-util-3), [DH-D-MEDIA-4](document-browser-computer.md#dh-d-media-4) (tool names in the Office text), [DH-D-EXT-5](extensions.md#dh-d-ext-5) (plugin-provided skills under a capability grant). **Used by.** Diagnostics are shown by [DH-D-UISET-9](ui-settings-plugins.md#dh-d-uiset-9).
- **Priority.** P1. **Outcome.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7), [DH-O-1](../design.md#dh-o-1). **Done when.** `DH-T-CONTEXT-19` to `-21` and `DH-T-CONTEXT-23` pass and [DH-E-CONTEXT-5](#dh-e-context-5) has passed.

<a id="dh-d-context-9"></a>
### DH-D-CONTEXT-9 The model discovers skills in a catalogue and loads one with a tool; the user can invoke one directly
- **Result and why it matters.** Instructions cost tokens only when a task needs them, and a person can force one.
- **How it is implemented.** [ADR-8](#dh-adr-context-8): a catalogue contributor over the `skills.catalogue` fold; the `skill` tool declared once (A03) with policy, size and digest checks; the invocation contributor; one renderer for both load paths.
- **Components and files.** `context/skill/tool`. **Depends on.** [DH-D-CONTEXT-1](#dh-d-context-1), [DH-D-CONTEXT-8](#dh-d-context-8). [DH-D-TOOLS-1](tools.md#dh-d-tools-1) and [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (tool declaration and pipeline). **Used by.** The skill chip, [DH-D-UICHAT-3](ui-chat.md#dh-d-uichat-3).
- **Priority.** P1. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7). **Done when.** `DH-T-CONTEXT-22`, `-23` pass and [DH-E-CONTEXT-1](#dh-e-context-1) and [DH-E-CONTEXT-5](#dh-e-context-5) have passed.

<a id="dh-d-context-10"></a>
### DH-D-CONTEXT-10 The model can read back what compaction replaced
- **Result and why it matters.** A detail the summary dropped is still one tool call away.
- **How it is implemented.** [ADR-10](#dh-adr-context-10): two read-only tools over the session reader, limited to the calling agent's own session, paged, rendering surface events only.
- **Components and files.** `context/compact/recall`. **Depends on.** [DH-D-CONTEXT-5](#dh-d-context-5). [DH-D-TOOLS-2](tools.md#dh-d-tools-2), [DH-D-VIEW-5](session-views.md#dh-d-view-5).
- **Priority.** P2. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-CONTEXT-24` passes.

## 9. Acceptance

### 9.1 Local tests

All use fakes for the model, the filesystem seam, the watch service and the clock unless a row says otherwise. In this section `D-n` means `DH-D-CONTEXT-n`.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-CONTEXT-1` | D-1 | Replaying a recorded log rebuilds every request with fakes that fail on any file, clock, shell or session read; the rebuilt requests equal the recorded digests |
| `DH-T-CONTEXT-2` | D-1 | Messages enter in table order whatever the registration order; an unchanged observation adds nothing; a baseline shadowed by compaction is re-entered in the same step |
| `DH-T-CONTEXT-3` | D-1 | An optional contributor's error or panic is skipped and logged as `context/skipped`; a required one rejects the step; a downstream reject enters nothing |
| `DH-T-CONTEXT-4` | D-2 | Golden: chain order, overlays, digest de-duplication, the three message forms; frame-closing text in a file is escaped; no per-process value appears in any prompt section |
| `DH-T-CONTEXT-5` | D-2 | Each chain keeps its floor; unused bytes are lent; the total source budget stops reading; an omitted file returns when room appears |
| `DH-T-CONTEXT-6` | D-2 | A watch notice, a file-tool touch, a shell working-directory argument, a changed `cwd` in a persistent shell result, a working-directory change and a resume each reconcile once; an unreadable file keeps its own last-good value; the project root is held; in a real temporary directory a link leaving the root is refused |
| `DH-T-CONTEXT-7` | D-3 | Under virtual time: interval, elapsed time, unique, mixed and missing zones, a user-set zone; fold state does not grow with history |
| `DH-T-CONTEXT-8` | D-11 | tmux output parsing with hostile names; a failed or denied command adds nothing; unchanged location adds nothing |
| `DH-T-CONTEXT-9` | D-4 | The mention grammar (fuzzed); ranking; ignore files; incremental update from `fs.Observed`; the truncated flag at `maxEntries` |
| `DH-T-CONTEXT-26` | D-12 | With a recorded session corpus: a word that occurs only in one session's messages finds that session; title matches rank above body matches; a session the caller may not see is never offered; one row per session |
| `DH-T-CONTEXT-10` | D-4 | The link codec (fuzzed); retention under the byte budget; frame escape; the access filter hides another principal's session; a later change of the source does not change the capture; the spill notice |
| `DH-T-CONTEXT-11` | D-5 | A table of windows, caps and policies gives the expected threshold and tail, including automatic headroom on a small window; invalid policy fails load |
| `DH-T-CONTEXT-12` | D-5 | Property: for generated histories every selected span has balanced edges, excludes the system node and leaves the tail byte-identical |
| `DH-T-CONTEXT-13` | D-5 | Step 7 is all or nothing under an injected append failure; a log cut after `compaction/start` resumes with one `interrupted` close and an unchanged surface; a changed selection closes as `changed` |
| `DH-T-CONTEXT-14` | D-5 | `compaction/request` is flushed before the fake model is called; one `compaction/attempt` per attempt; `RequestFrom` rebuilds the sent request; attribution fields are set; each trigger's failure result; back-off after three failures; a route fallback is recorded with its reason and never goes to an unselected route |
| `DH-T-CONTEXT-15` | D-5 | A span larger than the summariser's window is summarised in chunks and commits once; a single oversized unit is summarised from a pruned view and the checkpoint says so |
| `DH-T-CONTEXT-16` | D-6 | Property: pruned text never exceeds the threshold and pruning twice changes nothing; cuts fall on line boundaries or outside a combining sequence; the marker carries the count and locator; the original event is cited and unchanged |
| `DH-T-CONTEXT-17` | D-6 | The rewrite is pure and versioned; a declared image budget offloads before dispatch; an undeclared one after the failure; selections survive a route change and a failed summary |
| `DH-T-CONTEXT-18` | D-7 | Arguments parse; a busy agent is waited for or refused as asked; every error code; cancellation; command events are log-only and name the compaction |
| `DH-T-CONTEXT-19` | D-8 | Precedence across scope, rank and class; the shadowed list; concurrent providers under a deadline; last-good retention; `MaxAge`; invalid definitions rejected |
| `DH-T-CONTEXT-20` | D-8 | Front-matter grammar including the strict booleans; one level only; one bad entry does not hide its siblings; a missing root appears later; a watch notice invalidates once; authority-claiming keys are ignored with a diagnostic |
| `DH-T-CONTEXT-21` | D-8 | The embedded set holds four parseable skills; an incomplete override directory fails load; the Python section follows config; no tool named `load_workspace_dependencies` exists |
| `DH-T-CONTEXT-22` | D-9 | Golden: first catalogue, delta, whole list after compaction, incomplete notice, skipped names, body-changed notice; the tool's five errors; the size cap; the resource listing; the first-token rule and command precedence |
| `DH-T-CONTEXT-23` | D-8, D-9 | Through the real tool pipeline: a call for a skill that is not model-invocable is denied although its name is known; loading a skill registers no tool and changes no policy; a plugin provider without the grant is refused (fake plugin host) |
| `DH-T-CONTEXT-24` | D-10 | Recall renders and searches only spans replaced in the caller's session, pages, and returns ordinary tool results |
| `DH-T-CONTEXT-25` | D-1 | Import direction: no consumer package imports a provider package |

### 9.2 Real-environment scenarios

<a id="dh-e-context-1"></a>
#### DH-E-CONTEXT-1 Every injected message of a real session is in the log and rebuilds the request
- **Proves.** [DH-D-CONTEXT-1](#dh-d-context-1), [DH-D-CONTEXT-2](#dh-d-context-2), [DH-D-CONTEXT-9](#dh-d-context-9).
- **Given.** The built binary, a new temporary home, a real model API key supplied at run time, a temporary git repository with `AGENTS.md`, a nested `AGENTS.md` and one project skill. Areas A01 to A04 delivered.
- **When.** (1) Start a session in the repository and send a prompt that makes the model read a file in the nested directory and load the skill. (2) Export the session. (3) Run the replay check over the export on a machine without the repository.
- **Then.** The export holds the baseline, one "additional instructions" message, the catalogue and the skill result, each with its source record. Each request rebuilt from the export equals the `requestDigest` stored on its settlement. The replay opens no file.
- **Cleanup.** Stop the host; delete the home and the repository. On failure keep the export and the host log.

<a id="dh-e-context-2"></a>
#### DH-E-CONTEXT-2 An instruction file edited outside the harness reaches the model, and a link out of the project does not
- **Proves.** [DH-D-CONTEXT-2](#dh-d-context-2).
- **Given.** A running host on each of Linux, macOS and Windows, a session in a temporary repository, a real editor and `git`, and a secret-looking file outside the repository.
- **When.** (1) Save a change to `AGENTS.md` from the editor. (2) Switch branches with `git` so the file changes again. (3) Add `sub/AGENTS.md` as a link to the outside file. (4) Send one prompt after each action.
- **Then.** After 1 and 2 the next request holds one "updated" message with the new text. After 3 no request holds the outside file's text, and the diagnostics name the refused link. No turn fails.
- **Cleanup.** Delete the repository and the outside file; stop the host. Keep the session export if any assertion failed.

<a id="dh-e-context-3"></a>
#### DH-E-CONTEXT-3 A real conversation crosses the threshold, is summarised, and continues
- **Proves.** [DH-D-CONTEXT-5](#dh-d-context-5), [DH-D-CONTEXT-6](#dh-d-context-6).
- **Given.** The built binary, a real model route with a window small enough to reach in minutes (or a per-route policy that lowers the threshold), and a workspace with files large enough to produce oversized tool results.
- **When.** (1) Drive turns that read large files until pressure crosses the threshold. (2) Ask a question whose answer lies in the summarised span. (3) Kill the host during a later summariser call, restart and resume. (4) Send another prompt.
- **Then.** The log shows prunes before any summary; one flushed `compaction/request` followed by exactly one `compaction/attempt` per call; one group of summary, replacing message and end. The provider accepts the next request. The answer to 2 reflects the summary. After 3 the bracket is closed as `interrupted`, the surface equals the one before the call, and 4 succeeds.
- **Cleanup.** Stop the host; delete the home. Keep exports from before and after the kill on failure.

<a id="dh-e-context-4"></a>
#### DH-E-CONTEXT-4 A provider's overflow error is repaired once; a summariser that cannot serve fails closed
- **Proves.** [DH-D-CONTEXT-5](#dh-d-context-5).
- **Given.** A real route with automatic pressure turned off and a history built past its window; a second configured summariser route whose key is then revoked.
- **When.** (1) Send a prompt so the provider refuses the request as too long. (2) Repeat with the summariser pointed at the revoked route.
- **Then.** (1) One `assistant/attempt` with the overflow code, one compaction with trigger `overflow`, one retry in the same step that succeeds. (2) One failed `compaction/attempt` with the provider's authentication failure, `compaction/end{failed}`, and the turn ends `error` with the original overflow; a recording proxy shows no request to any route the user did not select.
- **Cleanup.** Restore the key; delete the home.

<a id="dh-e-context-5"></a>
#### DH-E-CONTEXT-5 A project skill is found, loaded and bounded by the sandbox
- **Proves.** [DH-D-CONTEXT-8](#dh-d-context-8), [DH-D-CONTEXT-9](#dh-d-context-9).
- **Given.** A real model, a real operating-system sandbox in `workspace-write` mode (area A10), and a repository whose `.agents/skills/build/` holds a `SKILL.md` and a script that writes outside the workspace.
- **When.** (1) Ask for the task the skill describes. (2) While the session runs, add a second skill directory from a real shell. (3) Send another prompt.
- **Then.** The model calls `skill`; the result is labelled as workspace content. Running the script happens only as a shell tool call, and its write outside the write roots is refused by the sandbox. The second skill appears as a catalogue delta in the request after 2.
- **Cleanup.** Delete the repository and home; confirm no file was written outside them.

<a id="dh-e-context-6"></a>
#### DH-E-CONTEXT-6 Terminal location is reported inside a real tmux pane and nowhere else
- **Proves.** [DH-D-CONTEXT-11](#dh-d-context-11).
- **Given.** Linux or macOS with tmux installed; the built `dsh` binary and an empty temporary home; the tmux contributor enabled; one real provider whose key is supplied at run time, or the replay provider where only the context message is checked.
- **When.** Run a command-line session (1) inside a tmux pane, (2) in a terminal started from that pane that only inherited tmux's environment, and (3) move the pane to another window between two turns.
- **Then.** (1) One reading naming the pane. (2) No reading. (3) One new reading on the next turn and none on unchanged turns.
- **Cleanup.** Kill the tmux session; delete the home.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed. These scenarios show the stated contracts for the tested build, inputs and machines; they do not show that a summary keeps everything a later task needs.

## 10. Open questions and limitations

**Open.** The window, output cap and image budget per route must be in A04's catalogue for every route a summariser may use; a route without them cannot be chosen, and the embedded catalogue's coverage is known only when it is written.

**Closed from other areas' documents.** The filesystem seam refuses a resolved path that leaves its root, evaluates ignore rules in process and notifies `fs.Observed` ([filesystem.md](filesystem.md)); a persistent shell result carries `cwd` ([execution.md](execution.md)). The office tools are `office_render`, `office_convert` and `office_recalculate` ([document-browser-computer.md](document-browser-computer.md), `DH-D-MEDIA-4`). A plugin provides skills under the capability `skills.provide`, with `skills/list`, `skills/read` and `skills/changed` ([extensions.md](extensions.md)). The skill chip and the browser zone are [ui-chat.md](ui-chat.md)'s; the diagnostics view and the per-skill switch, which edits `skill.Config.Disabled`, are [ui-settings-plugins.md](ui-settings-plugins.md)'s (`DH-D-UISET-9`). The mention grammar is package `context/fileref`, and session candidates take the caller's principal ([ADR-9](#dh-adr-context-9)).

**Recommended defaults, not confirmed by the owner.** Terminal location is kept at P2 ([ADR-3](#dh-adr-context-3)); the Office skill text is rewritten, not copied ([ADR-7](#dh-adr-context-7)); the clock and terminal-location boundaries of [ADR-11](#dh-adr-context-11).

**Settled with other areas, for the record.** `PreStep` listeners may append before `step/start`, a retry after a surface replacement starts a new request series, and `RunMaintenance` reports `agent/busy` at once ([agent-loop.md §6.2](agent-loop.md#62-contracts)). `History()` is the current-surface view, `AppendGroup` takes log-only and surface drafts together, `compaction/request` declares its request builder, and `image/offload` is the first rewrite type ([core-session.md](core-session.md)). The summariser's call is `Runtime.Complete` on a `session.Request`; the two repairable failures and the lowest reasoning effort are in A04's vocabulary. The command registry is host-side (A17) and reachable from the HTML UI, the SDK protocol (`session/query`) and the command line (`dsh headless --command`), not over ACP, which stays automation-only. Candidates for skills, files and sessions are served by `GET /sessions/{sid}/completions/{kind}` (api.md). `watch` (A23) covers local paths only; behind a non-local filesystem provider this area polls metadata once per step. A successful skill load is durable before the next step resolves its tools: it is the `skill` tool's result, or the sourced message of an explicit invocation, and `skills.loaded` folds both.

**Limitations.** The 30 carried units of section 5 stand, 21 of them on purpose. Token figures are estimates (A04), so the threshold is approximate and overflow recovery is the backstop. Defaults marked as new (`maxConsecutiveFailures`, `maxBodyBytes`, automatic headroom, `maxTotalSourceBytes`, `providerTimeout`) are judgements, not measurements. Instruction files, skills and session snapshots are text the model reads: injected text can steer any allowed action, as [../design.md §6](../design.md#6-security-model) states. A summary loses detail by design; the log does not. The design names its package directory `internal/context` only as a parent, because a Go package of that name would shadow the standard library's `context`.

## 11. Units dispositioned by this spec

Rows are in `trace/fragments/A14.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 17 | 14 ported, 1 merged (`file-reference-local`), 2 dropped (`harness-dev-context`, `tool-workspace-dependencies`) |
| `UP-DEBT` | 85 | 53 resolved, 30 carried, 2 not applicable |
| `UP-TOOL` | 2 | 1 ported (`skill`), 1 dropped |
| `UP-EVT` | 2 | 2 ported |
| `UP-CFG` | 12 | 11 ported, 1 replaced |
| `UP-SVC` | 5 | 5 ported |
| `UP-DEC` (handed over) | 12 | 10 ported, 2 replaced (the Office kit note, the rejected skill-registry pruning) |
