# Carried tech debt register: host surface, user interface and experimental areas

| | |
|---|---|
| **Reviewer** | K2 (wave 5, debt review). The reviewer wrote none of the rows it judges. |
| **Covers** | The 223 tech-debt units that the area agents of A19 to A23, A25, A27, C1 to C7 and X1 to X6 marked `carried` |
| **Ledger rows** | [../trace/fragments/K2.jsonl](../trace/fragments/K2.jsonl), one row per unit; they replace the area agents' rows |
| **Sibling register** | [carried-core.md](carried-core.md) covers areas A03 to A18 (reviewer K1) |
| **State described** | After the targeted re-sweep that followed the debt audit ([../audit/q3-debt.md](../audit/q3-debt.md)) and the fix pass of the area specifications, 2026-11-19. Earlier passes: the review, then the convergence round and the owner's decisions [DH-ADR-27](../design.md#dh-adr-27) to [DH-ADR-40](../design.md#dh-adr-40) |
| **Status** | Design only. **Nothing here was built, run or tested.** "Resolved" means only that the design, as the specifications now stand, removes the cause, and that the specification names a test or scenario that would show it. |

**Where things stand.** Of the 223 units: 58 are platform limits, 149 are scope boundaries
whose linked decision record states the boundary in its own words, **none is unresolved**,
12 were resolved by redesigns written in the convergence round, and 5 had been
reclassified at the review. **The plain count of remaining debt in these areas is 0.**
The re-sweep applied the audit's strict tests to every scope-boundary, platform-limit and
not-applicable row in these areas and found 23 units that did not pass; in the final
re-check the owners of the specifications added a "Scope boundary of this record"
sentence for each, and each sentence was read against the row it serves
([section 3.1](#31-still-unresolved)). [Section 9](#9-changed-in-the-re-sweep) lists what
moved. Section numbers 4, 5.1, 5.2, 5.3 and 6 are kept as they were at the review, because
the specifications refer to them.

## 1. What this register is and how to read it

Upstream deepseek-harness lists its own weaknesses: each package README has a section
"Known Limitations and Deferred Work". The traceability ledger calls each bullet a
tech-debt unit. The repository owner asked that the Go design resolve all of them. The
area agents resolved most, and marked 223 units in these twenty areas as **carried**:
still true of the Go design.

The owner then decided that "carried" is too coarse. Each carried unit is sorted here
into one of three classes, and only the last class remains debt.

**Platform limit.** A fact of an operating system, a protocol, a browser, a file format,
the Go runtime or another company's program. No design of this harness removes it; the
design can only bound it and report it honestly. Example: a terminal can be typed into and
resized from one browser window at a time, because a pseudo-terminal has exactly one
window size (`L:api.terminal-controller.4`). The test applied: if a different design could
remove the limit at reasonable cost, it is not a platform limit.

**Scope boundary.** Something the product deliberately does not do. The test applied: a
numbered decision record that the row links must state the boundary in its own words (a
sentence a reader can point at), so that a reader can find who decided it and why. Since
the re-sweep the test is strict: a boundary that follows only from a general rule, or that
is written only in a specification's debt table or implementation text, does not pass.
Where the record is marked "recommended default, not confirmed by the owner", the row
says so. Example: a hook can deny a tool call but can never rewrite its arguments,
because [DH-ADR-XHOOKS-1](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-1) gives a hook
a closed set of answers (`L:experimental.hook-protocol.1`). At the review sixteen
boundaries were clearly deliberate but had no decision record; each has one now, and
[section 5.2](#52-boundaries-that-had-no-complete-decision-record-at-the-review) lists
the record each gained.

**Unresolved.** Real debt: a weakness the Go design still has and could remove. At the
review sixteen units were in this class. Example: a person on a company network who set
the proxy in the Windows or macOS system settings had to export environment variables by
hand, although the static system setting can be read without breaking the pure-Go rule
(`L:util.http-proxy.1`, entry [U-K2-2](#u-k2-2)). The owner then decided that every
unresolved entry gets its recommended redesign ([DH-ADR-40](../design.md#dh-adr-40)), and
the convergence round wrote those redesigns into the specifications. This pass checked
each one against the specification as it stands: a unit is `resolved` only where a
decision, a deliverable and a test or scenario can be pointed at. None of those sixteen
is unresolved now; [section 3](#3-the-fourteen-entries-and-what-became-of-each) gives
the result per entry. The re-sweep then found 23 other units that do not pass the strict
tests; they are the unresolved units of section 3.1.

Five units were **wrongly carried** at the review. Two were removed by another part of
the design and are `resolved`; three were marked `not-applicable`. The re-sweep took one
of the three back (the status mark, which the Go design builds with the same limit); the
other two are features dropped by owner decision DH-ADR-24. They are listed in
[section 6](#6-rows-reclassified-at-the-review-as-resolved-or-not-applicable).

**Not applicable, strictly.** A `not-applicable` row holds only if the limit cannot arise
in a Go implementation of what is built. Where the feature is not built because a decision
dropped it, the row's reason now says "dropped by DH-ADR-n" and links that decision. The
re-sweep rewrote 79 such rows of the area agents in these areas
([section 8](#8-rows-replaced-outside-the-223)).

**Unit names.** `L:<rest>` abbreviates `UP-DEBT-limit.packages.<rest>`; for example
`L:api.gateway.9` is `UP-DEBT-limit.packages.api.gateway.9`, the ninth bullet under
"Known Limitations" of upstream's `packages/api/gateway/README.md`. The one unit an area
agent defined itself is written in full. Every unit has exactly one home: one row of the
table in section 3.1, the "Unit" line of one entry of section 3.3, or one table row of
sections 4, 5.1, 5.2 or 6. The sixteen units of the entries are named a second time in
the outcome table of section 3.2, the units that moved in the re-sweep are named a second
time in section 9, and the examples in this section repeat four units.

**What the classes do not say.** A class is a judgement about cause, not about
importance. A platform limit can hurt more than an unresolved item.

## 2. Summary

The state after the re-sweep. "Carried" is the number of units the area agent had marked
carried, which is what this register covers. "Resolved in the convergence round" counts
units whose cause a redesign written in that round removes. "Reclassified at the review"
counts the units of section 6 in their present disposition.

| Key | Area specification | Carried | Platform limit | Scope boundary | Unresolved | Resolved in the convergence round | Reclassified at the review |
|---|---|---:|---:|---:|---:|---:|---:|
| A19 | [api.md](../areas/api.md) | 12 | 4 | 7 | 0 | 1 | 0 |
| A20 | [sdk-acp-typert.md](../areas/sdk-acp-typert.md) | 5 | 1 | 4 | 0 | 0 | 0 |
| A21 | [boot.md](../areas/boot.md) | 3 | 0 | 3 | 0 | 0 | 0 |
| A22 | [extensions.md](../areas/extensions.md) | 1 | 0 | 1 | 0 | 0 | 0 |
| A23 | [util-test-support.md](../areas/util-test-support.md) | 18 | 6 | 10 | 0 | 1 | 1 resolved |
| A25 | [app-web.md](../areas/app-web.md) | 1 | 1 | 0 | 0 | 0 | 0 |
| A27 | [bundle-preset.md](../areas/bundle-preset.md) | 22 | 3 | 15 | 0 | 3 | 1 resolved |
| C1 | [ui-core.md](../areas/ui-core.md) | 3 | 1 | 2 | 0 | 0 | 0 |
| C2 | [ui-kit.md](../areas/ui-kit.md) | 9 | 0 | 9 | 0 | 0 | 0 |
| C3 | [ui-chat.md](../areas/ui-chat.md) | 5 | 2 | 3 | 0 | 0 | 0 |
| C4 | [ui-conversation.md](../areas/ui-conversation.md) | 3 | 0 | 3 | 0 | 0 | 0 |
| C5 | [ui-work.md](../areas/ui-work.md) | 5 | 1 | 3 | 0 | 1 | 0 |
| C6 | [ui-sidebar-workspace.md](../areas/ui-sidebar-workspace.md) | 22 | 4 | 15 | 0 | 3 | 0 |
| C7 | [ui-settings-plugins.md](../areas/ui-settings-plugins.md) | 7 | 2 | 4 | 0 | 1 | 0 |
| X1 | [x-forge-worktree.md](../areas/x-forge-worktree.md) | 22 | 4 | 17 | 0 | 1 | 0 |
| X2 | [x-liveshare-webworker.md](../areas/x-liveshare-webworker.md) | 1 | 0 | 1 | 0 | 0 | 0 |
| X3 | [x-inspector-timeline.md](../areas/x-inspector-timeline.md) | 7 | 2 | 5 | 0 | 0 | 0 |
| X4 | [x-cua-browser-voice.md](../areas/x-cua-browser-voice.md) | 32 | 20 | 10 | 0 | 0 | 2 not applicable |
| X5 | [x-hooks-mods-terminal.md](../areas/x-hooks-mods-terminal.md) | 35 | 1 | 34 | 0 | 0 | 0 |
| X6 | [x-recommendations-misc.md](../areas/x-recommendations-misc.md) | 10 | 6 | 3 | 0 | 1 | 0 |
| | **Total** | **223** | **58** | **149** | **0** | **12** | **4** |

The 5 units reclassified at the review are 2 resolved, 2 not applicable and the status
mark, which is now a scope boundary. Totals: 58 + 149 + 0 + 12 + 4 = 223.

**Remaining debt: 0 units.** Before the re-sweep the register said 0 with 64 platform
limits and 142 scope boundaries; the re-sweep found 23 unresolved; the final re-check
closed all 23 with record sentences ([section 3.1](#31-still-unresolved);
[section 9](#9-changed-in-the-re-sweep) lists every move). Some of the 149 scope
boundaries rest on a record marked "recommended default, not confirmed by the owner"; each
such row says so in its justification.

Outside the 223, this pass and the one before it replaced 87 rows of the area agents in
these areas: 7 resolved rows whose wording named things that no longer exist, 1 resolved
row whose reason described an open question that is now settled, and 79 not-applicable
rows that now name the decision that dropped their feature
([section 8](#8-rows-replaced-outside-the-223)). The audit's own unit in these areas,
`UP-DEBT-found.Q3.3`, is resolved (section 8).

### What the review found

The four findings below are the review's, written before the convergence round and the
re-sweep. The first two are settled now; they are kept because they explain where the
redesigns came from. Counts in them are the review's.

- **Most unresolved items are requests one area made of another that nobody answered.**
  Ten of the 16 unresolved units are carried only because the fix belongs to a
  different area, and that area's specification neither accepts nor refuses it: a limit
  on directory reads (asked of the filesystem area), a progress line and a reasoning
  switch for `dsh headless` (the command-line area), a time-zone field (the SDK area),
  vendor display names (the model area), palette entries in the terminal snapshot (the
  execution area), arrow-key movement (the design-system area), a job's final output
  (the jobs area), an append to a session nobody holds (the session areas), and MCP
  prompt templates (the MCP area, entry U-K1-7 of the sibling register). Each is small.
  Most need no owner decision; they need the two specifications to agree.
- **Two justifications did not survive checking.** The area specification says that
  reading the operating system's proxy setting needs cgo, and that a static check cannot
  see inside a dependency. Neither holds for a Go program
  ([U-K2-2](#u-k2-2), [U-K2-3](#u-k2-3)).
- **Experimental areas hold 107 of the 223 units and only two unresolved ones.**
  Upstream marks these packages "under evaluation, no support commitment". The owner's
  decisions changed what their limits are: with computer use coming only from an external
  server ([DH-ADR-19](../design.md#dh-adr-19)), thirteen limits of upstream's own native
  helpers became facts of the operating system and of a third-party driver; with
  Stagehand dropped ([DH-ADR-24](../design.md#dh-adr-24)), two of its three limits have no
  subject and the third is kept because the replacement provider has it too.
- **User-interface rows.** One row was judged a gap in a React component's interface
  that cannot arise in host-rendered HTML (the status mark; the re-sweep reversed this,
  see section 6). Every other
  user-interface row is something a person can feel and has a class. Five of them exist
  because the product is a web page and not a desktop application
  ([DH-ADR-18](../design.md#dh-adr-18)): reserved key chords (two units), no Save as, no
  drag to another application, no reveal after download.

## 3. The fourteen entries and what became of each

At the review this section was called "Unresolved" and held fourteen entries for sixteen
units. The labels are stable; the specifications cite them.

### 3.1 Still unresolved

**None.** The re-sweep found 23 units that did not pass the strict tests: 21 deliberate
product choices that no decision record stated in its own words, and 2 choices that were
filed as platform limits but follow from this design. In the final re-check the owners
of the specifications added a "Scope boundary of this record" sentence for each. Each
sentence was read against the row it serves; every one states the boundary the row
relies on, all its clauses included, and each row is now a scope boundary linked to that
record, at stage `DH-S-59`. Nothing was built or run; the design did not change. The
tables below are the home of these 23 units.

**Record gaps closed (21 units).** The second column is the boundary; the last column is
the record whose own sentence now states it.

| Unit | The boundary | The record that states it now (final re-check) |
|---|---|---|
| `L:acp.acp.2` | An ACP prompt carries text, resource links and raster images only | Scope sentence in [DH-ADR-SDK-9](../areas/sdk-acp-typert.md#dh-adr-sdk-9) |
| `L:util.timeout.4` | An idle timer on a response body is not a total limit | Scope sentence in [DH-ADR-UTIL-4](../areas/util-test-support.md#dh-adr-util-4) |
| `L:bundle.headless.1` | `dsh headless` runs one task and exits | Scope sentence in [DH-ADR-BUNDLE-9](../areas/bundle-preset.md#dh-adr-bundle-9) (not CLI-3, whose sentence covers `headless.8`) |
| `L:bundle.headless.9` | A headless run refuses a session from another working directory and a subagent's session; a held session is refused by DH-ADR-12 | Scope sentence in DH-ADR-BUNDLE-9, which states all three clauses |
| `L:bundle.sdk-app.1` | The stdio profiles mount no web listener | Scope sentence in [DH-ADR-BUNDLE-1](../areas/bundle-preset.md#dh-adr-bundle-1) |
| `L:preset.persona.1` | The persona component mounts only inside an agent preset | Scope sentence in [DH-ADR-BUNDLE-4](../areas/bundle-preset.md#dh-adr-bundle-4) |
| `L:client.ui-context-menu.3` | An open menu is view state of one tab and is never recorded | Scope sentence in [DH-ADR-UIKIT-5](../areas/ui-kit.md#dh-adr-uikit-5) |
| `L:client.ui-primitives.15` | The status mark has five states | Scope sentence in [DH-ADR-UIKIT-1](../areas/ui-kit.md#dh-adr-uikit-1) |
| `L:client.ui-goal.1` | A goal belongs to the session and survives a preset switch | Scope sentence in [DH-ADR-JOBS-9](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-9) |
| `L:client.ui-entry-customization.4` | Menu entries can be hidden, not reordered | Scope sentence in [DH-ADR-UISIDE-9](../areas/ui-sidebar-workspace.md#dh-adr-uiside-9) |
| `L:client.ui-sidebar-documentpreview.6` | A scripted HTML preview gets only its directly referenced files and no bridge to the workspace | Scope sentence in [DH-ADR-UISIDE-5](../areas/ui-sidebar-workspace.md#dh-adr-uiside-5) |
| `L:client.ui-workspace.1` | Session search does not tolerate typing errors | Scope sentence in [DH-ADR-VIEW-5](../areas/session-views.md#dh-adr-view-5) |
| `L:client.ui-account-notifications.1` | A vendor notice is plain text with two actions | Scope sentence in [DH-ADR-UISET-7](../areas/ui-settings-plugins.md#dh-adr-uiset-7) |
| `L:experimental.deepseek-chat-import.14` | The chat importer keeps one branch and leaves listed records out | Scope sentence in [DH-ADR-XINSPECT-7](../areas/x-inspector-timeline.md#dh-adr-xinspect-7) |
| `L:experimental.timeline.3` | Pause and Continue are requests to the agent; Stop is the loop's cancel | Scope sentence in [DH-ADR-XINSPECT-5](../areas/x-inspector-timeline.md#dh-adr-xinspect-5) |
| `L:experimental.api-speech-to-text.1` | Dictation has no file upload and no stored history of transcripts (no streaming is stated) | Scope sentence in [DH-ADR-XCUA-5](../areas/x-cua-browser-voice.md#dh-adr-xcua-5) |
| `L:experimental.cua-macos.15` | The evidence does not measure task success with a real model | Scope sentence in [DH-ADR-XCUA-1](../areas/x-cua-browser-voice.md#dh-adr-xcua-1) |
| `L:experimental.hooks-claude-code.8` | `suppressOutput` and `terminalSequence` are ignored; a transcript file only on opt-in | Scope sentence in [DH-ADR-XHOOKS-2](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-2) |
| `L:experimental.webhook-github.4` | The GitHub endpoint accepts JSON bodies only | Scope sentence in [DH-ADR-XHOOKS-5](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-5) |
| `L:experimental.cot-translation-bundle.1` | Translation starts when the reader expands the block; the collapsed summary stays original | Scope sentence in [DH-ADR-XMISC-4](../areas/x-recommendations-misc.md#dh-adr-xmisc-4) |
| `L:experimental.plugin-recommendations.2` | No background call looks for features to suggest and no user profile is kept; a suggestion is made only inside a turn the user started (not "only when asked") | Scope sentence in [DH-ADR-XMISC-3](../areas/x-recommendations-misc.md#dh-adr-xmisc-3) |

**Choices that could have been designed away, now kept on purpose (2 units).** Each was a
platform limit until the re-sweep and is the consequence of a choice this design made;
both are now stated as choices.

| Unit | What a person sees | Why it is not a platform limit | The record that keeps it on purpose (final re-check) |
|---|---|---|---|
| `L:client.ui-primitives.8` | A very long source line is left uncoloured, and a construct that opens on it can mis-colour the lines after it | The chosen tokeniser (chroma, a backtracking engine with a per-rule time limit, `DH-LIB-SHIKI`) causes it (audit Q3-3) | Scope sentence in [DH-ADR-UICONV-4](../areas/ui-conversation.md#dh-adr-uiconv-4): the long-line and block cut-offs are kept on purpose because host highlighting must be bounded per render; a linear-time native engine is ruled out by [DH-ADR-15](../design.md#dh-adr-15); the loss is cosmetic |
| `L:client.ui-sidebar-documentpreview.4` | A very large PDF page, or a high zoom on a dense display, looks soft | The 16,777,216-pixel cap is a chosen bound; a page could be drawn in tiles | Scope sentence in [DH-ADR-UISIDE-5](../areas/ui-sidebar-workspace.md#dh-adr-uiside-5): the cap is kept for tab memory (one bitmap is already 64 MiB), tiles would add a second drawing path, and Download is the full-quality route |

### 3.1a Loose ends from the convergence round

Four loose ends remain. None is a weakness of the design that a redesign would remove,
so none is debt, but each needs someone to act:

1. **A fact nobody has checked ([U-K2-2](#u-k2-2)).** The Windows half of the system-proxy
   reader rests on reading registry values in a build without cgo. The specification
   records this as an open fact and is written to hold either way: if the reader cannot
   be built, Windows has none and keeps the old limit. Until someone builds it, "the
   system proxy is read on Windows" is a design statement only.
2. **Two specifications disagree ([U-K2-13](#u-k2-13)).** When another `dsh` process
   holds a session, [x-recommendations-misc.md](../areas/x-recommendations-misc.md)
   refuses a new translation with `translate/session-held-elsewhere`, while
   [core-session.md](../areas/core-session.md) (`DH-D-SESSION-9`) says a translation
   "succeeds whether the session is idle, live here or running in another `dsh`
   process". The first is the more specific and gives its reason (a provider call may
   only follow a committed request). One of the two sentences must change.
3. **One stale sentence ([U-K2-7](#u-k2-7)).** The decision text of `DH-ADR-UISIDE-9`
   now docks the session panel on laptop widths; the tradeoff bullet of the same record
   still says "on laptop widths the panel is still an overlay".
4. **A boundary stated by two records but not in the area's own words
   ([U-K2-14](#u-k2-14)).** An MCP server's prompt templates cannot be used from an ACP
   session. That follows from `DH-ADR-CONNECT-4` (a prompt is a human command) and
   `DH-ADR-SDK-9` (ACP offers no commands), and
   [sdk-acp-typert.md](../areas/sdk-acp-typert.md) does not say it.

### 3.2 Resolved in the convergence round

Each former entry by label, with the decision, deliverable and proof that the
specification now holds. "Resolved" means the design removes the cause; nothing was built
or run. Where the outcome is "scope", the owner's instruction or the integration agents
chose the option that records a boundary, and the unit is a scope boundary under the
record named.

| Entry | Units | Outcome | What resolves it or states it | Proof named in the specification |
|---|---|---|---|---|
| [U-K2-1](#u-k2-1) | `L:api.workspace-files.8` | Resolved | `ReadDir` takes a limit and reports truncation ([filesystem.md](../areas/filesystem.md) section 6.2); the files route asks for its cap plus one (`DH-D-API-9`) | `DH-T-FS-8`, `DH-T-API-24` |
| [U-K2-2](#u-k2-2) | `L:util.http-proxy.1` | Resolved in part; the rest is scope | [DH-ADR-UTIL-10](../areas/util-test-support.md#dh-adr-util-10): the static system proxy is read once at launch, below every environment variable; a PAC address is reported and never evaluated, which is the boundary the unit keeps (`DH-D-UTIL-9`) | `DH-T-UTIL-21`, `DH-E-UTIL-4`; the Windows reader still rests on an unverified registry read |
| [U-K2-3](#u-k2-3) | `L:util.http-proxy.6` | Resolved | The import-graph gate over the whole binary, dependencies included ([util-test-support.md](../areas/util-test-support.md) section 6.2, `DH-D-UTIL-10` since the fix pass split it from `DH-D-UTIL-2`) | `DH-T-UTIL-22` |
| [U-K2-4](#u-k2-4) | `L:bundle.headless.6`, `L:bundle.headless.7` | Resolved, both | [DH-ADR-CLI-3](../areas/app-cli.md#dh-adr-cli-3): a progress line at the start of the run and of each step; `--reasoning auto\|show\|hide`, where `auto` writes reasoning only to a terminal (`DH-D-CLI-2`) | `DH-T-CLI-12` |
| [U-K2-5](#u-k2-5) | `L:bundle.sdk-app.2` | Resolved | `timeZone` on `initialize` and `session/create` ([sdk-acp-typert.md](../areas/sdk-acp-typert.md) section 6.2, `DH-D-SDK-4`, `DH-D-SDK-8`). An ACP session keeps the limit and the specification says so | `DH-T-SDK-25` |
| [U-K2-6](#u-k2-6) | `L:client.ui-agent-team.1` | Resolved | A read-only message timeline (`DH-D-UIWORK-5`) over the new query `Query.TreeMessages` ([session-views.md](../areas/session-views.md), `DH-D-VIEW-5`) | `DH-T-UIWORK-24`, `DH-T-VIEW-25` |
| | `L:client.ui-agent-team.2` | Scope | [DH-ADR-UIWORK-5](../areas/ui-work.md#dh-adr-uiwork-5) and [DH-ADR-SUBAGENT-11](../areas/subagent-team.md#dh-adr-subagent-11): starting and renaming a member stay the model's; a write scope in a shared checkout is a label marked "not enforced", and a member that must not collide is started with `isolation: worktree` ([DH-ADR-SUBAGENT-12](../areas/subagent-team.md#dh-adr-subagent-12), [DH-ADR-40](../design.md#dh-adr-40)) | `DH-T-UIWORK-24` checks that no start or rename control is offered and that the label is drawn |
| [U-K2-7](#u-k2-7) | `L:client.ui-session-panel.1` | Resolved | [DH-ADR-UISIDE-9](../areas/ui-sidebar-workspace.md#dh-adr-uiside-9): the panel docks whenever the transcript keeps its minimum readable width (`DH-D-UIKIT-5` owns the token) | `DH-T-UIKIT-12`, `DH-E-UIKIT-3` |
| [U-K2-8](#u-k2-8) | `L:client.ui-sidebar-terminal.3` | Resolved | A bounded palette section in the terminal snapshot, recorded from the byte stream ([execution.md](../areas/execution.md) section 6.2, `DH-D-EXEC-7`); the island applies it before it draws (`DH-D-UISIDE-7`) | `DH-T-EXEC-12`, `DH-T-UISIDE-16`, `DH-E-UISIDE-3` |
| [U-K2-9](#u-k2-9) | `L:client.ui-workspace.4` | Resolved | The navigable-list island `k-list` in the design-system widget table ([DH-ADR-UIKIT-3](../areas/ui-kit.md#dh-adr-uikit-3)), used by the session list and the search results (`DH-D-UISIDE-1`) | `DH-T-UISIDE-1`, `DH-E-UISIDE-1` |
| [U-K2-10](#u-k2-10) | `L:client.ui-settings-general.1` | Scope | [DH-ADR-UIKIT-3](../areas/ui-kit.md#dh-adr-uikit-3): desktop browsers are the supported target; a phone may work and is not tested. Supported browsers are the owner's [DH-ADR-39](../design.md#dh-adr-39) | None needed: the boundary is the absence of a guarantee |
| [U-K2-11](#u-k2-11) | `L:client.ui-settings-models.4` | Resolved | [DH-ADR-LLM-6](../areas/llm.md#dh-adr-llm-6): every vendor entry of the embedded catalogue has a display name and optional aliases; the picker shows and filters on them (`DH-D-UISET-4`) | `DH-T-LLM-9`, `DH-T-UISET-11` |
| [U-K2-12](#u-k2-12) | `L:experimental.forge-session.5` | Resolved | The binding listener also reads the jobs area's settle record, which names the originating call and command (`DH-D-XFORGE-5`, `DH-D-JOBS-2`) | `DH-T-XFORGE-10` |
| [U-K2-13](#u-k2-13) | `L:experimental.client-ui-cot-translation.3` | Resolved | A translation of a session nobody drives is written with `AppendLogOnly` ([core-session.md](../areas/core-session.md), `DH-D-SESSION-9`; [DH-ADR-XMISC-4](../areas/x-recommendations-misc.md#dh-adr-xmisc-4)) | `DH-T-XMISC-2`, `DH-T-SESSION-20` |
| [U-K2-14](#u-k2-14) | `L:acp.acp.3` | Scope | [DH-ADR-CONNECT-4](../areas/mcp-lsp-web-connector.md#dh-adr-connect-4) makes a server prompt a human command and records "subscriptions are not followed"; [DH-ADR-SDK-9](../areas/sdk-acp-typert.md#dh-adr-sdk-9) offers no commands over ACP | `DH-T-CONNECT-22` for the MCP side |

Four of these entries end in "recommended defaults, not confirmed by the owner", as the
specifications say where they record them: the default of `--reasoning`
([U-K2-4](#u-k2-4)), the timeline and the scope of start and rename
([U-K2-6](#u-k2-6)), the dock threshold ([U-K2-7](#u-k2-7)) and the device floor
([U-K2-10](#u-k2-10)). The owner can overturn any of them.

### 3.3 The entries as written at the review

The entries below are the review's text: what a user experienced, why the design did not
remove it then, and what was proposed. They are kept so that a reader can see what each
label meant. Each ends with an **Outcome** line that states what the specification says
now. In these entries "does not" and "today" refer to the specifications before the
convergence round.

<a id="u-k2-1"></a>
#### U-K2-1 A huge directory is read whole before the listing is cut

- **Unit.** `L:api.workspace-files.8`.
- **What the user experiences.** Opening a directory with hundreds of thousands of entries
  in the files view is slow and costs the host memory in proportion to the directory,
  although the page shows only the first entries up to a cap.
- **Why the design does not remove it.** The cap is applied by the route in
  [api.md](../areas/api.md) (`DH-D-API-9`) after the filesystem seam returned everything:
  [filesystem.md](../areas/filesystem.md) declares
  `ReadDir(ctx, ref) ([]DirEntry, error)`. The route area asks for a limit in its open
  question 7; the filesystem specification does not answer. The Go design did already
  remove the larger cost, one `stat` per child.
- **Proposed redesign.** In [filesystem.md](../areas/filesystem.md) section 6.2, `ReadDir`
  takes a limit and reports whether more entries exist (or returns an iterator). The local
  provider reads the directory in batches and stops; a remote provider passes the limit to
  its helper; the search walker passes "no limit". In [api.md](../areas/api.md),
  `DH-D-API-9` asks for the cap plus one and draws "more entries not shown". The
  specification must state the consequence: a limited listing is in the operating
  system's directory order, so the page sorts what it received and says that the listing
  is partial. Proof: a local test with a counting fake provider shows that no more than
  cap plus one entries are requested.
- **Cost.** Small: one interface change that touches each filesystem provider and its
  conformance suite. Priority: with the files view.
- **Outcome.** Resolved in the convergence round, as proposed. `ReadDir` is declared with
  a limit and a truncation result, the specification states that a limited listing is in
  directory order and is drawn as partial, and `DH-T-FS-8` and `DH-T-API-24` use the
  counting fake provider.

<a id="u-k2-2"></a>
#### U-K2-2 The system proxy setting is not read

- **Unit.** `L:util.http-proxy.1`.
- **What the user experiences.** On a network where the proxy is set in the Windows or
  macOS system settings, or by a proxy auto-configuration (PAC) file, every model request
  fails to connect until the user finds out that `HTTPS_PROXY` must be exported. SOCKS5
  proxy addresses now work, which removes a third of upstream's limit.
- **Why the design does not remove it.** [util-test-support.md](../areas/util-test-support.md)
  and [libs/undici.md](../libs/undici.md) (`DH-ADR-UNDICI-2`, section 7) say that reading
  system settings "needs WinHTTP calls on Windows and a cgo framework on macOS", which
  the pure-Go rule ([DH-ADR-15](../design.md#dh-adr-15)) forbids. Inference, not verified
  by running anything: the static Windows setting is a registry value under the user's
  Internet Settings key, which a Go program reads through system calls without cgo, and
  macOS prints its setting through the system program `scutil --proxy`, which the host can
  run as a child process at launch. Only PAC needs more: evaluating a PAC file means
  running JavaScript, and the design has no script engine
  ([DH-ADR-8](../design.md#dh-adr-8)).
- **Options for the owner.**
  1. **Keep environment variables only.** Record it as scope in `DH-ADR-UNDICI-2` and
     correct the cgo sentence. Nothing is built.
  2. **Read the static system setting at launch** on Windows and macOS, as the source of
     lowest precedence (below every environment variable), frozen with the other launch
     facts ([DH-ADR-BOOT-4](../areas/boot.md#dh-adr-boot-4)) and shown in the launch
     diagnostics with its origin. A PAC address is detected and reported as "not
     supported; export the variables", never evaluated.
  3. **Option 2 plus PAC evaluation.** Needs a JavaScript engine in the binary.
- **Recommendation.** Option 2. It removes the common case and turns the PAC case from a
  silent connection failure into a named diagnostic. Option 3 is not recommended.
- **Cost.** Option 2 is two small platform-specific readers, tests with recorded registry
  values and recorded `scutil` output, and one real-environment scenario per operating
  system with a real system proxy. The Windows registry access must be verified as a
  library choice before it is named.
- **Outcome.** Option 2 was written, as [DH-ADR-40](../design.md#dh-adr-40) directs:
  [DH-ADR-UTIL-10](../areas/util-test-support.md#dh-adr-util-10), the leaf package
  `util/sysproxy`, test `DH-T-UTIL-21` and scenario `DH-E-UTIL-4`. The cgo sentence in
  [libs/undici.md](../libs/undici.md) is corrected. **Resolved in part.** The unit stays
  carried as a scope boundary for what the decision keeps out on purpose: a PAC address
  is named in a diagnostic and never evaluated. One fact is recorded as open in
  section 10 of the area specification and was not checked by anyone: that the Windows
  registry values can be read in a build without cgo. The design is written to hold
  either way; if the reader cannot be built, Windows keeps the old limit.

<a id="u-k2-3"></a>
#### U-K2-3 The outbound-traffic check does not look inside dependencies

- **Unit.** `L:util.http-proxy.6`.
- **What the operator experiences.** The promise is that every outbound connection goes
  through one policy (proxy, trusted authorities, the sharing permit). A third-party
  module that builds its own HTTP client or dials directly bypasses it, and the design
  notices only if a composition test happens to exercise that code path.
- **Why the design does not remove it.** The area ported upstream's rule: a static check
  over this repository's source, backed by a test recorder. Its justification, "a source
  analyzer cannot follow calls into third-party modules", was true of npm packages. In Go
  every dependency is source in the build.
- **Proposed redesign.** In [util-test-support.md](../areas/util-test-support.md)
  (`DH-D-UTIL-2`) and [libs/undici.md](../libs/undici.md) (`DH-ADR-UNDICI-1`), add a gate
  of the same shape as the cgo gate of [DH-ADR-15](../design.md#dh-adr-15): a Go test
  lists every package in the `dsh` binary's import graph, dependencies included, that
  imports a package able to open a connection (`net`, `net/http`, `crypto/tls` and the
  like), and compares the list with a reviewed table in which each entry says how its
  client is supplied by `egress`. A dependency update that adds a new such package fails
  the gate until someone reviews it. The specification must say what the gate proves:
  which packages *can* dial, not that they do, so the test recorder stays.
- **Cost.** Small: one test and one table kept with dependency updates. The table is a
  list of reviewed facts, not a baseline of tolerated violations; the specification should
  say so, because [DH-ADR-TOOLCHAIN-8](../upstream/toolchain.md#dh-adr-toolchain-8)
  forbids baseline files.
- **Outcome.** Resolved in the convergence round, as proposed: the gate is `DH-T-UTIL-22`,
  described in section 6.2 of the area specification, which also says that the table is a
  list of reviewed facts and that an entry without a stated supply path fails the test.

<a id="u-k2-4"></a>
#### U-K2-4 `dsh headless` is silent before the first token and writes reasoning to logs

- **Units.** `L:bundle.headless.6`, `L:bundle.headless.7`.
- **What the user experiences.** (a) A headless run prints nothing until the model's
  first token arrives; a person or a supervisor cannot tell a slow first token from a
  hang. (b) The model's reasoning goes to standard error by default, so a continuous
  integration system or log collector that keeps standard error keeps model output that
  may be sensitive.
- **Why the design does not remove it.** The runner belongs to the command-line area.
  [app-cli.md](../areas/app-cli.md) lists "reasoning, tool progress, the approval prompt"
  as the diagnostic stream of `dsh headless` and offers no option.
  [bundle-preset.md](../areas/bundle-preset.md) asks for both changes in its section 10,
  item 3, and nothing answers.
- **Proposed redesign.** In [app-cli.md](../areas/app-cli.md), the `dsh headless` row of
  the command table and [DH-ADR-CLI-3](../areas/app-cli.md#dh-adr-cli-3): (a) one
  progress line on the diagnostic stream when the run starts and at each step start,
  before any token, naming the step and the model route; (b) an option that decides
  whether reasoning is written to the diagnostic stream. For the default of (b) there are
  two choices: keep upstream's (reasoning shown), or show reasoning only when the
  diagnostic stream is a terminal and require the option otherwise. The reviewer
  recommends the second, in the spirit of [DH-ADR-9](../design.md#dh-adr-9): model output
  should not land in a log file by default. Proof: two local tests on the runner's
  streams, and one line added to the headless real-environment scenario.
- **Cost.** Small. It changes the documented output contract of one command, so it should
  be settled before scripts depend on it.
- **Outcome.** Resolved in the convergence round, both units, with the reviewer's second
  choice for the default: `--reasoning auto|show|hide`, where `auto` writes reasoning only
  when the diagnostic stream is a terminal, and a progress line at the start of the run
  and of each step. `DH-T-CLI-12` covers both. A recommended default the owner has not
  confirmed.

<a id="u-k2-5"></a>
#### U-K2-5 An SDK session does not know the user's time zone

- **Unit.** `L:bundle.sdk-app.2`.
- **What the user experiences.** In a session driven by an embedding program the model is
  told that the user's time zone is unavailable, or works from the zone of the host
  process, which in a container is usually UTC. "Today", "tomorrow at nine" and a
  schedule created in that session can be wrong by hours.
- **Why the design does not remove it.** The SDK protocol carries no time zone.
  [bundle-preset.md](../areas/bundle-preset.md) asks the SDK area for a field (section 10,
  item 6); [sdk-acp-typert.md](../areas/sdk-acp-typert.md) defines none.
- **Proposed redesign.** In [sdk-acp-typert.md](../areas/sdk-acp-typert.md): an optional
  `timeZone` (an IANA zone name) on `initialize`, with an override when a session is
  created; the host validates the name and refuses an unknown one; the time-context
  component uses it as the user's zone for that connection's sessions and says
  "unavailable" only when none was given. The Go and Python clients expose it as an
  option and send nothing by default. It is an additive optional field. ACP has no such
  field in its own protocol, so an ACP session keeps the limit; the specification should
  say so.
- **Cost.** Small: one protocol field, two client options, one local test, one assertion
  in the SDK scenario.
- **Outcome.** Resolved in the convergence round, as proposed: `timeZone` on `initialize`
  and on `session/create`, validated by the host, read by the clock context, exposed by
  both clients; test `DH-T-SDK-25`. The specification states that a session driven over
  ACP keeps the limit.

<a id="u-k2-6"></a>
#### U-K2-6 The team view has no message timeline and cannot start or rename a member

- **Units.** `L:client.ui-agent-team.1`, `L:client.ui-agent-team.2`.
- **What the user experiences.** A person supervising a team of agents must open each
  member's conversation to see what the agents told each other. From the team view they
  can interrupt a running member, dismiss a settled one and manage tasks, but cannot
  start a member or rename one. A member's "write scope" is shown, labelled as advisory,
  and nothing enforces it.
- **Why the design does not remove it.** [ui-work.md](../areas/ui-work.md) is a read
  model of the subagent area's ledger
  ([DH-ADR-UIWORK-5](../areas/ui-work.md#dh-adr-uiwork-5)). The session-query area offers
  no query for "messages between agents across one tree"; the subagent area's start
  operation needs a creator agent and its ledger has no rename; enforced write scopes are
  "not designed". No decision record states any of the three as scope. The Go design did
  remove upstream's separate mailbox: a message between agents is an ordinary logged
  message in the recipient's session.
- **Options for the owner.**
  1. **Record all three as scope** in `DH-ADR-UIWORK-5` and in
     [DH-ADR-SUBAGENT-11](../areas/subagent-team.md#dh-adr-subagent-11) ("a team is the
     tree plus a task board").
  2. **Build the timeline only.** The session-query area adds one bounded query: messages
     with source `agent-message` in the sessions of one tree, in time order. The work area
     adds a read-only timeline page. Start and rename are recorded as scope.
  3. **Option 2 plus a host operation** by which a person starts or renames a member. It
     touches the subagent area's rules for names and bounds.
- **Recommendation.** Option 2: the timeline is a read model, which is what this area is
  for, and it is the part a supervisor misses. Enforcing write scopes is not decided here:
  it is entry U-K1-3 of [carried-core.md](carried-core.md), and until it is settled the
  label must keep saying "not enforced".
- **Cost.** Option 2 is one query, one page and their tests. Option 3 is a change to a
  P0 area's invariants.
- **Outcome.** Option 2 was written. `L:client.ui-agent-team.1` is **resolved**: the
  read-only timeline of `DH-D-UIWORK-5` over `Query.TreeMessages`, with tests
  `DH-T-UIWORK-24` and `DH-T-VIEW-25`. `L:client.ui-agent-team.2` is a **scope boundary**:
  `DH-ADR-UIWORK-5` and `DH-ADR-SUBAGENT-11` record that starting and renaming a member
  stay the model's, and write scopes stay advisory in a shared checkout, with
  `isolation: worktree` ([DH-ADR-40](../design.md#dh-adr-40), the owner's resolution of
  U-K1-3) as the way to keep members apart. A recommended default the owner has not
  confirmed.

<a id="u-k2-7"></a>
#### U-K2-7 The session panel covers the page on laptop widths

- **Unit.** `L:client.ui-session-panel.1`.
- **What the user experiences.** On a window narrower than about 1608 px with the
  default transcript width, which includes most laptops, the session panel opens as an
  overlay on top of the page instead of docking beside the transcript.
- **Why the design does not remove it.**
  [DH-ADR-UISIDE-9](../areas/ui-sidebar-workspace.md#dh-adr-uiside-9) says a stylesheet
  rule docks the panel "when there is room" and the area leaves "docking by moving the
  transcript aside" to a layout decision that was not made. Upstream left the same
  decision open.
- **Options.**
  1. **Keep the overlay** and say so in `DH-ADR-UISIDE-9`: the panel never takes width
     from the transcript.
  2. **Dock down to a minimum transcript width.** "Room" is measured against the
     transcript's minimum readable width, not its default width, so the panel docks on
     laptop widths by narrowing the transcript, and overlays only below that.
  3. **A per-user preference** between the two.
- **Recommendation.** Option 2. The dock is already one stylesheet rule, so the change is
  a different threshold, and a real-browser scenario at a laptop width proves it. The
  design-system area ([ui-kit.md](../areas/ui-kit.md)) owns the width tokens and should
  make the call.
- **Cost.** Small for options 1 and 2. Option 3 adds a stored preference and a control.
- **Outcome.** Option 2 was written: `DH-ADR-UISIDE-9` measures "room" against the token
  `--frame-centre-min` (400 px), `DH-D-UIKIT-5` owns the token, `DH-T-UIKIT-12` fails a
  dock rule that names the default width, and `DH-E-UIKIT-3` expects a docked panel at
  1,600 and 1,100 px. **Resolved.** A recommended default the owner has not confirmed.
  The tradeoff bullet of `DH-ADR-UISIDE-9` still carries the old sentence and should be
  corrected.

<a id="u-k2-8"></a>
#### U-K2-8 Program-set terminal colours are lost on every re-attach

- **Unit.** `L:client.ui-sidebar-terminal.3`.
- **What the user experiences.** A program that sets its own colours in a terminal (a
  shell theme, an editor colour scheme) shows wrong colours after the user switches tabs
  or reloads the page. In upstream this happened only when a new terminal widget was
  opened; in the Go UI every tab switch and reload draws from the host's screen snapshot,
  so it happens more often.
- **Why the design does not remove it.** The snapshot is written by the host-side
  terminal emulator of the execution area and carries the screen, not the palette entries
  a program set through escape sequences.
  [ui-sidebar-workspace.md](../areas/ui-sidebar-workspace.md) and the browser-terminal
  library specification both name the gap and point at the execution area, whose
  specification does not take it up.
- **Proposed redesign.** In [execution.md](../areas/execution.md), the screen snapshot
  contract gains a bounded palette section: the entries set by the colour escape
  sequences (indexed colours, foreground, background, cursor) and their resets, recorded
  per terminal. In [ui-sidebar-workspace.md](../areas/ui-sidebar-workspace.md)
  (`DH-D-UISIDE-7`) the terminal island applies that section on attach, before it draws.
  Open question for the execution area: whether the chosen emulator exposes those
  sequences, or the terminal service must scan the byte stream for them itself.
- **Cost.** Small to moderate: one more section in a snapshot format, tests with recorded
  byte streams, and one step in the terminal real-browser scenario.
- **Outcome.** Resolved in the convergence round, as proposed, and the open question is
  answered: the host records the palette entries from the byte stream itself, so the
  snapshot's `palette` section does not depend on what the emulator exposes. Tests
  `DH-T-EXEC-12` and `DH-T-UISIDE-16`; scenario `DH-E-UISIDE-3`.

<a id="u-k2-9"></a>
#### U-K2-9 No arrow-key movement in the session list

- **Unit.** `L:client.ui-workspace.4`.
- **What the user experiences.** In the session list and in search results the arrow
  keys do nothing; a keyboard user walks every row with Tab. The Go design already
  removed the worse half: every row is a native link and takes focus.
- **Why the design does not remove it.** Arrow-key movement needs a small script (a
  "roving focus": one row is in the Tab order and the arrows move between rows).
  [ui-sidebar-workspace.md](../areas/ui-sidebar-workspace.md) was written before the
  design-system specification and says that area "is not written". It now is, and
  [DH-ADR-UIKIT-3](../areas/ui-kit.md#dh-adr-uikit-3) lists roving focus among the kit's
  scripts.
- **Proposed redesign.** In [ui-sidebar-workspace.md](../areas/ui-sidebar-workspace.md),
  `DH-D-UISIDE-1` declares the kit's roving-focus island on the list: Up and Down move,
  Home and End jump, Right and Left open and close a workspace group. Without script the
  list stays as it is today, reachable by Tab, which keeps the rule that a page is
  complete without script. [ui-kit.md](../areas/ui-kit.md) adds the list to the consumers
  in its widget table. Proof: a keyboard-only step in the navigation real-browser
  scenario.
- **Cost.** Small; the island is already designed.
- **Outcome.** Resolved in the convergence round: the design-system widget table has a
  navigable-list island, `k-list`, and `DH-D-UISIDE-1` marks the session list and the
  search results for it. `DH-T-UISIDE-1` checks the marking and `DH-E-UISIDE-1` walks the
  list with the keyboard alone.

<a id="u-k2-10"></a>
#### U-K2-10 Behaviour under a phone keyboard is unvalidated

- **Unit.** `L:client.ui-settings-general.1`.
- **What the user experiences.** Nobody knows whether a settings field stays visible when
  a phone's on-screen keyboard opens. The risk is lower than upstream's, because settings
  is an ordinary scrolling page and not a fixed-height panel.
- **Why the design does not remove it.** The planned real-browser scenarios use desktop
  browsers with a narrow window, which does not reproduce a phone keyboard, and no
  decision says whether phones are a supported target:
  [DH-ADR-UIKIT-3](../areas/ui-kit.md#dh-adr-uikit-3) states a floor of browser features,
  not of devices. A required scenario that does not exist stays open; it is not a pass.
- **Options for the owner.**
  1. **Phones are best effort.** `DH-ADR-UIKIT-3` gains a device floor: desktop browsers
     are supported, narrow layouts are for narrow desktop windows, phones are not tested.
     The unit becomes a scope boundary.
  2. **A recorded manual check** on one iOS and one Android phone per release, labelled
     `manual` in the evidence report and never counted as a scenario pass.
  3. **An automated real-device scenario** through a device service, which needs an
     account and sends test traffic off the machine.
- **Recommendation.** Option 1 now, because the product is a local tool opened on the
  machine that runs it; option 2 if remote access from a phone becomes a stated use.
- **Cost.** Option 1 is one sentence. Option 2 is a recurring manual step.
- **Outcome.** Option 1 was written: `DH-ADR-UIKIT-3` now states a device floor (desktop
  browsers are the supported target; a phone may work and is not tested), beside the
  owner's decision on supported browsers ([DH-ADR-39](../design.md#dh-adr-39)). The unit
  is a **scope boundary**. A recommended default the owner has not confirmed.

<a id="u-k2-11"></a>
#### U-K2-11 The provider picker shows internal identifiers

- **Unit.** `L:client.ui-settings-models.4`.
- **What the user experiences.** In the model settings some vendors appear under their
  internal route identifier (`moonshotai`, `zai`) instead of a product name, and a search
  by product name does not find them. The Go design already removed the other half: the
  picker is one host-filtered search with the custom endpoint pinned.
- **Why the design does not remove it.** A display name is a field of the model area's
  embedded catalogue. [ui-settings-plugins.md](../areas/ui-settings-plugins.md) requests
  it; [llm.md](../areas/llm.md) does not define it.
- **Proposed redesign.** In [llm.md](../areas/llm.md), each vendor entry of the embedded
  catalogue gains a display name and optional search aliases, and a catalogue test fails
  when an entry has none. In [ui-settings-plugins.md](../areas/ui-settings-plugins.md)
  (`DH-D-UISET-4`) the picker shows the name with the identifier as secondary text and
  filters on both.
- **Cost.** Small: one field, its data, one test.
- **Outcome.** Resolved in the convergence round, as proposed:
  [DH-ADR-LLM-6](../areas/llm.md#dh-adr-llm-6) gives each vendor entry a display name and
  optional aliases, `DH-T-LLM-9` fails an entry without a name, and the picker of
  `DH-D-UISET-4` shows and filters on them (`DH-T-UISET-11`).

<a id="u-k2-12"></a>
#### U-K2-12 A pull request created by a background command is not linked

- **Unit.** `L:experimental.forge-session.5`.
- **What the user experiences.** When the agent creates a pull request with a command it
  moved to the background, the session's panel does not show that pull request when the
  job finishes. It appears later, after the next push or inspection, or when the user
  adds it by link.
- **Why the design does not remove it.** The binding listener of
  [DH-ADR-XFORGE-4](../areas/x-forge-worktree.md#dh-adr-xforge-4) reads settled tool
  calls. A background command's tool call settles with a job id and no output, and the
  specification leaves open whether the job's final output is ever delivered as a settled
  outcome.
- **Proposed redesign.** The jobs area already writes a durable record when a job settles
  ([DH-ADR-JOBS-4](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-4)). In
  [x-forge-worktree.md](../areas/x-forge-worktree.md) (`DH-D-XFORGE-5`), the listener also
  reads that record for jobs started by a shell tool call and scans the job's output with
  the same rules and the same byte bound. It needs two things stated in
  [jobs-schedule-goal-plan.md](../areas/jobs-schedule-goal-plan.md): the settle record
  names the originating tool call and its command, and the job's output can be read at
  that moment.
- **Cost.** Small. Low priority: the feature is experimental and branch discovery already
  covers the common case.
- **Outcome.** Resolved in the convergence round, as proposed. `DH-D-XFORGE-5` has the
  listener read the jobs area's settle record; the jobs specification states that the
  `job/settled` record names the originating tool call and its command and that the
  output is still readable at that moment; `DH-T-XFORGE-10` covers a settled background
  job and the case that binds nothing.

<a id="u-k2-13"></a>
#### U-K2-13 No new translation in a session nobody holds

- **Unit.** `L:experimental.client-ui-cot-translation.3`.
- **What the user experiences.** When reading a finished subagent's session, a reasoning
  block that was translated before shows its translation, but a block that was not
  cannot be translated now. The UI states the reason, where upstream reported a
  translation failure.
- **Why the design does not remove it.** A new translation appends two events to the
  session, and only the holder of the session's lease may write
  ([DH-ADR-12](../design.md#dh-adr-12)). Upstream "activated" the session to write, which
  ran preset start-up hooks and could add model-visible context just so that a reader
  could see a translation;
  [DH-ADR-XMISC-4](../areas/x-recommendations-misc.md#dh-adr-xmisc-4) removed that on
  purpose. The area asks the session areas whether a host component may append log-only
  events to a session nobody holds; nothing answers.
- **Options.**
  1. **Keep the limit** and record it in `DH-ADR-XMISC-4`.
  2. **A maintenance append.** [core-session.md](../areas/core-session.md) and
     [session-store.md](../areas/session-store.md) define one host operation that takes a
     free session's lease for the length of one append of events the model never sees,
     without creating an agent or running any hook, and that is refused when another
     process holds the lease. Translation uses it.
  3. **Store translations outside the log**, keyed by event. This breaks the rule that a
     translation is read from the log and complicates export.
- **Recommendation.** Option 2, if the session areas accept the operation. Other writers
  of log-only records on idle sessions would use the same path. Option 3 is not
  recommended.
- **Cost.** Option 2 is a new write path with lease rules and a two-process contention
  test. Low priority: the feature is experimental.
- **Outcome.** Option 2 was written, together with the owner's resolution of U-K1-5
  ([DH-ADR-40](../design.md#dh-adr-40)): `AppendLogOnly` in
  [core-session.md](../areas/core-session.md) (`DH-D-SESSION-9`, test `DH-T-SESSION-20`)
  and its use by the translation service (`DH-ADR-XMISC-4`, test `DH-T-XMISC-2`).
  **Resolved**: upstream's case, a session nobody drives, can be translated without
  creating an agent or running a hook. A narrower case is new and is not upstream's: a
  session held by another `dsh` process is refused with
  `translate/session-held-elsewhere`. The two specifications disagree about that case
  (section 3.1, item 2).

<a id="u-k2-14"></a>
#### U-K2-14 MCP prompt templates have no consumer (same cause as U-K1-7)

- **Unit.** `L:acp.acp.3`.
- **What the user experiences.** An MCP server that an ACP client supplies is used for
  its tools and, new in the Go design, for reading resources on demand. Its prompt
  templates are not offered and resource subscriptions are not followed.
- **Why the design does not remove it.** The ACP carrier adds no limit of its own:
  [DH-ADR-SDK-9](../areas/sdk-acp-typert.md#dh-adr-sdk-9) mounts the server through the
  harness's MCP client. The gap is that client's, and it is entry **U-K1-7** of
  [carried-core.md](carried-core.md), which gives the options and a recommendation.
- **Proposed redesign.** None in this area. When U-K1-7 is settled, ACP sessions gain the
  result with no change to the SDK specification. If the owner records it as scope there,
  this unit becomes a scope boundary under the same record.
- **Cost.** None here.
- **Outcome.** U-K1-7 was settled with its first option: each prompt a connected server
  offers is a human command, and "subscriptions are not followed" is recorded as scope
  ([DH-ADR-CONNECT-4](../areas/mcp-lsp-web-connector.md#dh-adr-connect-4), test
  `DH-T-CONNECT-22`). An ACP session does not gain it, contrary to what this entry
  expected: ACP offers no commands (`DH-ADR-SDK-9`), so a person cannot run a prompt
  command from an ACP client. The unit is a **scope boundary** under those two records.
  The SDK specification does not state the consequence in its own words (section 3.1,
  item 4).

## 4. Platform limits

Sixty-four units. "Where the fact comes from" names the owner of the fact. "What the
design does about it" is the bound or the honest report the Go design adds; it is design,
not a tested result.

### 4.1 Operating-system processes, pipes, terminals and paths

| The limit | Units | What the user sees | Where the fact comes from | What the design does about it |
|---|---|---|---|---|
| Output and error streams cannot be interleaved exactly | `L:api.job-controller.1` | A job's output is labelled by channel; order across channels is approximate | Two operating-system pipes share no clock or sequence | Labels each chunk; upstream joined them blindly |
| A pseudo-terminal can be proven only by opening one | `L:api.terminal-controller.2`, `L:client.ui-sidebar-terminal.1` | Opening a terminal can fail; the tab shows the error and a retry and starts no other shell on its own | Unix device limits, the Windows console host; no probe reserves a terminal | Nothing is published before the backend is set up, so a failure leaves nothing behind |
| A pseudo-terminal has one window size | `L:api.terminal-controller.4` | One browser window at a time types and resizes; a late window gets the screen and bounded scrollback | One size value per pseudo-terminal on every system | Scrollback bound is configurable |
| A program's readiness is sometimes a guess | `L:experimental.terminal-panel.3` | After sending a command the agent may be told "inferred idle" instead of "done" | Only Linux exposes that a foreign program is blocked reading the terminal | Readiness tiers say which answer is exact |
| Completion comes from the shell | `L:client.ui-sidebar-terminal.2` | Completion menus are whatever the user's shell provides | Completion is a feature of the shell program | Keys reach the program unmodified |
| Windows opens nothing from a non-interactive session | `L:util.native-command.3` | Started from a service or over SSH, the browser hand-off may report success and open nothing | Windows session isolation | The URL is always printed; the hand-off has a deadline |
| A launcher does not report what the browser did | `L:bundle.web-app.4` | The host knows it asked for a browser, not that one appeared or closed | `xdg-open`, `open` and the Windows shell return at once | The token exchange shows that some browser signed in |
| A remote machine cannot open a browser on the user's screen | `L:bundle.web-app.5` | Under SSH the URL is printed and the user's client must forward and open it | The nature of a remote session | Prints the URL |
| An absolute path is a place, not a file | `L:client.ui-deliverables.8` | A changed file outside the workspace that was moved later is reported as "source missing" | No portable file identity follows a move | The comparison and the kept copy no longer depend on the path |

### 4.2 The Go runtime

| The limit | Units | What the user sees | Where the fact comes from | What the design does about it |
|---|---|---|---|---|
| A goroutine cannot be stopped from outside | `L:sdk.client.4`, `L:util.timeout.1` | An application handler or a tool body that ignores cancellation keeps running | The Go runtime has no such operation | A late answer is never sent and the request is already settled as refused; process owners end the process tree; a conformance helper fails a tool body that does not return |

### 4.3 Browsers and the web platform

| The limit | Units | What the user sees | Where the fact comes from | What the design does about it |
|---|---|---|---|---|
| The event stream is one-way and a proxy answers for the browser | `L:api.gateway.9` | Behind a buffering reverse proxy a closed tab holds a stream slot until the stream is rotated (30 minutes by default) | Server-sent events have no reply channel; a proxy accepts bytes on the browser's behalf | Rotation time is configurable; the oldest stream is parked first |
| The browser keeps some key chords | `L:client.shortcuts.1` | Web defaults use longer chords and a few commands have no default key | A page never receives a reserved chord; the desktop application that intercepted keys is gone ([DH-ADR-18](../design.md#dh-adr-18)) | Every command has a visible control |
| A page is not told where a download went | `L:client.ui-file-actions.3` | No "show in folder" after a download | No browser reports a download's destination | The browser's own downloads list |
| Drag to another application is Chromium-only | `L:client.ui-file-drag.1` | Elsewhere the user downloads the file | Only Chromium implements the download drag type | Download works everywhere; the scenario records unsupported browsers |
| A sandboxed frame hides its scroll position | `L:client.ui-sidebar-documentpreview.5` | An HTML preview can return to the top on reload | Same-origin isolation of the frame, which is also the security control | Host-rendered previews, images and PDF keep their position |

### 4.4 Desktop automation: the operating system and the installed driver

With [DH-ADR-19](../design.md#dh-adr-19) the harness ships no native helper. These limits
are the operating system's and the third-party driver's.

| The limit | Units | What the user sees | Where the fact comes from | What the design does about it |
|---|---|---|---|---|
| Desktop control needs permissions and a graphical session | `L:experimental.computer-use-cua-driver-native.1`, `L:experimental.cua-bundle.2`, `L:experimental.cua-windows.9` | Computer use works only on a logged-in, unlocked desktop after the user granted permissions by hand | macOS privacy permissions, Windows session and capture rules | Status view shows the driver's permission state before the first call |
| A desktop is shared with its user | `L:experimental.computer-use-cua-driver-native.3`, `L:experimental.cua.5`, `L:experimental.cua-macos.6` | A window can move or change between an observation and an input; the pointer is not restored | Nothing can reserve a desktop against its own user | A turn-length lease between harness sessions; a changed window is an error |
| Delivered input cannot be taken back, and may not take effect | `L:experimental.computer-use-cua-driver-native.4`, `L:experimental.cua-macos.10` | A cancelled call may already have acted; a success means "delivered", not "worked" | Applications decide how they treat synthetic input | The result says "outcome unknown, observe first"; nothing is replayed |
| Element references live until the next observation | `L:experimental.cua-macos.5`, `L:experimental.cua.6` | A stale reference is refused; every action names a window | An accessibility tree is a snapshot with no lasting identities | A stale reference is an error, never a guess |
| An application is identified only as well as the system exposes it | `L:experimental.cua-macos.14` | A grant may be keyed by process id and end when the application exits | What the operating system and the driver report | Fails closed: one more prompt |
| A window manager can shrink background windows | `L:experimental.cua-macos.7` | Observation and input on a background window can fail | macOS Stage Manager | The driver's degraded flag reaches the model; no window is forced forward |
| Windows input may move the real pointer | `L:experimental.cua-windows.2` | The user may see their pointer move | The driver and the Windows input model | Background delivery is always requested; the scenario records what happened |

### 4.5 A real browser driven by the harness

| The limit | Units | What the user sees | Where the fact comes from | What the design does about it |
|---|---|---|---|---|
| An attached browser is still the user's | `L:experimental.browser-use-chrome-devtools-mcp.3`, `L:experimental.browser-use-playwright-mcp.3`, `L:experimental.browser-use-runtime.2` | The user and other programs can change pages the agent works on | A browser's debugging interface accepts any client | Launching a private browser is the default; attach needs an approval |
| A delivered action cannot be undone | `L:experimental.browser-use-chrome-devtools-mcp.5`, `L:experimental.browser-use-playwright-mcp.5`, `L:experimental.browser-use-runtime.3` | Cancel does not reverse a click or a navigation | A real browser has no undo | "Outcome unknown, observe first"; a launched browser's processes are stopped by a bounded ladder |
| A browser cannot be rebuilt from a log | `L:experimental.browser-use-stagehand-native.2` | After a resume or a fork, earlier pages and logins are gone | A live process is not in the session log | The first browser result after a resume says so |

### 4.6 Other companies' services and programs

| Third party | The limit | Units | What the user sees | What the design does about it |
|---|---|---|---|---|
| Model providers and models | Real failure rates are unknown to a fixture | `L:test-support.llm-mock-server.1` | The fault server's mix is test pressure, not a forecast | Seeded and repeatable |
| | A text-only model cannot read images | `L:bundle.sdk-minimal.2` | The image tool returns an error on such a route | Stored through the attachment store |
| | A model's wording is not deterministic | `L:experimental.session-title-all-prompts-llm.3`, `L:experimental.client-ui-cot-translation.2` | An adequate title may still be reworded; a translation may change inline formatting | An unchanged title writes no event; code, math and tables are never sent to a translator; the original is one click away |
| The vendor's account service | It offers only these web endpoints and only loopback sign-in callbacks | `L:client.ui-settings-account.1`, `L:client.ui-settings-account.4` | Profile and balance are read when the panel opens and can fail singly; sign-in works only at a loopback address | Nothing is fetched before the panel opens; a non-loopback address gets an explanation, not a retry loop |
| Free web translation endpoints | Unofficial, unguaranteed, regionally unreachable | `L:experimental.client-ui-cot-translation.1`, `L:experimental.client-ui-cot-translation.5`, `L:experimental.cot-translation-bundle.2`, `L:experimental.translator.1` | A translation through these endpoints can fail or stop working | By the owner's decision [DH-ADR-36](../design.md#dh-adr-36) the web provider is specified but not in the shipped bundle, which translates with the user's own model; where an operator adds it, it is off until chosen and counts as a sender; one logged failure with Retry |
| Code hosts and Git | No count of unresolved threads; metered interfaces; no push to a local tool | `L:experimental.forge-github.2`, `L:experimental.forge-github.6`, `L:experimental.forge-gitlab.7` | A thread count may read "N+"; every poll spends the user's rate limit | Polls only while a panel is shown; backs off until the reset time |
| | Git keeps only what was committed | `L:experimental.client-ui-forge.8` | Restoring a removed worktree loses uncommitted changes | The card says so first; an existing directory is never overwritten |
| LibreOffice | Fidelity and font diagnostics are the program's | `L:client.ui-sidebar-documentpreview.2` | An office preview is as good as the installed LibreOffice | The engine version is reported ([DH-ADR-20](../design.md#dh-adr-20)) |
| Programs the host starts | A child follows the proxy only if its runtime reads the variables | `L:util.http-proxy.3` | A Node-based MCP server may connect directly | A trusted child is given the user's and the resolved values |
| Apple and Microsoft code signing | A publisher signature needs the publisher's certificates | `UP-DEBT-found.A25.2` | Release signing cannot be rehearsed outside the protected release environment | The update path is qualified with a test key; certificates are an open organisational matter |
| The vendor's chat export | An unspecified format without the bytes of most files | `L:experimental.deepseek-chat-import.7`, `L:experimental.deepseek-chat-import.10` | One export structure is supported; a missing file becomes a note with its name and size | A version check and a coded skip per conversation |

### 4.7 Formats and arithmetic

| The limit | Units | What the user sees | Where the fact comes from | What the design does about it |
|---|---|---|---|---|
| Random identifiers are unique with overwhelming probability only | `L:util.crypto.2` | Nothing in practice | 122 random bits (RFC 9562, version 4) | The store's key constraint turns a collision into a refused insert |
| A recording is finite | `L:test-support.llm-replay.2` | A model call that never returns must be authored in an override file | A session export has no event for a wait that never ended | Every attempt, failed ones included, is a logged event |

## 5. Scope boundaries

### 5.1 Boundaries stated by a decision record

106 units after the re-sweep (122 at the review, minus the 20 that moved to section 3.1,
plus the 4 that moved here from the platform tables). With the 16 units of section 5.2,
which have their record now, and the 4 units of section 3 that ended as scope (`U-K2-2`,
the second unit of `U-K2-6`, `U-K2-10`, `U-K2-14`), that makes the 126 scope boundaries
of the summary. Every row below was re-checked in the re-sweep against the text of the
record it names; where the strict reading failed, the unit moved to section 3.1. Where two
records state one boundary, the first is the one that says it most directly.

**Owner decisions** ([design.md section 7.1](../design.md#71-owner-decisions))

| Decision record | The boundary it states | Units |
|---|---|---|
| [DH-ADR-UIKIT-6](../areas/ui-kit.md#dh-adr-uikit-6) and [DH-ADR-9](../design.md#dh-adr-9) | Site marks are a fixed list; no site icon is fetched (both records have a "Scope boundary of this record" sentence since the fix pass) | `L:client.ui-primitives.3` |
| [DH-ADR-13](../design.md#dh-adr-13) with [DH-ADR-UISIDE-9](../areas/ui-sidebar-workspace.md#dh-adr-uiside-9) | No plugin discovery; the More menu holds one link to the plugins page | `L:client.ui-entry-customization.6` |
| [DH-ADR-16](../design.md#dh-adr-16) and [DH-ADR-33](../design.md#dh-adr-33) | No "always allow"; a grant ends with its session | `L:experimental.cua-windows.10` |
| [DH-ADR-18](../design.md#dh-adr-18) with [DH-ADR-UICHAT-9](../areas/ui-chat.md#dh-adr-uichat-9) | No desktop application, so no Save as | `L:client.ui-file-actions.1` |
| [DH-ADR-19](../design.md#dh-adr-19) with [DH-ADR-XCUA-1](../areas/x-cua-browser-voice.md#dh-adr-xcua-1) | The harness ships no computer-use driver, and neither pins nor audits the one installed: installing it, what it supports and how well it was validated are the operator's and the vendor's | `L:experimental.computer-use-cua-driver-mcp.1`, `L:experimental.cua.2` |
| [DH-ADR-23](../design.md#dh-adr-23) with [DH-ADR-OAR-1](../libs/botiverse-oar.md#dh-adr-oar-1) | External agent runtimes are operator-installed ("The harness installs none of these"), and one is exercised by a real scenario; five of seven are not verified | `L:experimental.subagent-oar.1`, `L:experimental.subagent-oar.2` |
| [DH-ADR-OAR-1](../libs/botiverse-oar.md#dh-adr-oar-1) with [DH-ADR-SUBAGENT-7](../areas/subagent-team.md#dh-adr-subagent-7) | The bridge answers a native agent's request for approval or input by ending the run, and keeps a one-shot contract (moved here from the platform tables in the re-sweep) | `L:experimental.subagent-oar.3`, `L:experimental.subagent-oar.6` |

**Foundation decisions**

| Decision record | The boundary it states | Units |
|---|---|---|
| [DH-ADR-WIRE-3](../upstream/wire-and-api.md#dh-adr-wire-3) | Small state is resent whole after a reconnect; only the session journal has a cursor | `L:api.workspace-controller.1` |
| [DH-ADR-WIRE-4](../upstream/wire-and-api.md#dh-adr-wire-4) | A pending question or approval does not survive a host restart; it fails closed | `L:sdk.protocol.3` |
| [DH-ADR-WIRE-12](../upstream/wire-and-api.md#dh-adr-wire-12) with [DH-ADR-XHOOKS-5](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-5) | A webhook is verified, answered 202 and not queued; no completion report | `L:experimental.webhook-github.3`, `L:experimental.webhook.1`, `L:experimental.webhook.3` |
| [DH-ADR-12](../design.md#dh-adr-12) with [DH-ADR-CLI-2](../areas/app-cli.md#dh-adr-cli-2) | One writer per session: a session another process holds is refused with `session/owned` | the held-session clause of the unit headless.9; the unit itself is in section 3.1, because its other two clauses have no record |
| [DH-ADR-SECURITY-5](../upstream/security.md#dh-adr-security-5) | Only the owner's answerer settles an approval or a question; with no answerer the answer is no | `L:api.remote-access.5`, `L:bundle.sdk-app.3` |
| [DH-ADR-ARCH-3](../upstream/architecture.md#dh-adr-arch-3) | One recorded cause per cancellation; the first wins | `L:util.timeout.3` |

**Host, protocol and composition areas**

| Decision record | The boundary it states | Units |
|---|---|---|
| [DH-ADR-API-6](../areas/api.md#dh-adr-api-6) | A wildcard bind is refused | `L:bundle.web-app.7` |
| [DH-ADR-API-7](../areas/api.md#dh-adr-api-7) | A person's terminal is owned by a session, ends with the host, feeds nothing to the model, and is reclaimed only on confirmed idleness | `L:api.terminal-controller.1`, `L:api.terminal-controller.3`, `L:client.ui-sidebar-terminal.4` |
| [DH-ADR-API-8](../areas/api.md#dh-adr-api-8) | File bytes are plain bounded HTTP (no resumable upload); listing and watching stay inside the workspace | `L:api.session-controller.4`, `L:client.file-upload.1`, `L:api.workspace-files.3`, `L:client.ui-sidebar-files.7` |
| [DH-ADR-JOBS-1](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-1) and [DH-ADR-EXEC-2](../areas/execution.md#dh-adr-exec-2) | Jobs do not survive the host | `L:api.job-controller.2` |
| [DH-ADR-JOBS-7](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-7) | A schedule fires only while a host process runs; missed occurrences are reported, not replayed | `L:bundle.sdk-app.4` |
| [DH-ADR-SDK-9](../areas/sdk-acp-typert.md#dh-adr-sdk-9) | ACP is automation-only: no replay, fork, modes, commands, terminals or elicitation | `L:acp.acp.4` |
| [DH-ADR-BOOT-2](../areas/boot.md#dh-adr-boot-2) | The profile layer is the one writable layer, and it is shared by everything on that profile | `L:boot.config-editor.1`, `L:bundle.sdk-app.7` |
| [DH-ADR-BOOT-4](../areas/boot.md#dh-adr-boot-4) | Launch facts are frozen at launch; a discovered file cannot name a program to start | `L:boot.app-boot.6`, `L:util.launch-environment.2`, `L:bundle.web-app.6` |
| [DH-ADR-BOOT-10](../areas/boot.md#dh-adr-boot-10) with the owner's [DH-ADR-30](../design.md#dh-adr-30) | The plugin manager refuses protected entries and manages its own profile only; the model's tool only attaches a plugin to its session | `L:boot.plugin-manager.11` |
| [DH-ADR-EXT-9](../areas/extensions.md#dh-adr-ext-9) | Inspection is read-only | `L:extensions.tool-cordis.1` |
| [DH-ADR-RRULE-1](../libs/rrule.md#dh-adr-rrule-1) | Recurrence is a bounded subset | `L:util.time.2` |
| [DH-ADR-BUNDLE-1](../areas/bundle-preset.md#dh-adr-bundle-1) | Only the web profile follows configuration edits; stdio and one-shot profiles need a restart | `L:bundle.acp-app.3`, `L:bundle.sdk-app.8` |
| [DH-ADR-BUNDLE-2](../areas/bundle-preset.md#dh-adr-bundle-2) | The minimal profile omits product services on purpose | `L:bundle.sdk-minimal.1` |
| [DH-ADR-BUNDLE-6](../areas/bundle-preset.md#dh-adr-bundle-6) | A resumed session binds the current definition, and the change is logged (that a preset is not a security boundary is stated only in the area's section 10) | `L:preset.agent-preset-registry.1` |
| [DH-ADR-BUNDLE-1](../areas/bundle-preset.md#dh-adr-bundle-1) and [DH-ADR-BUNDLE-10](../areas/bundle-preset.md#dh-adr-bundle-10) | An experimental view is an optional bundle, off by default | `L:experimental.client-ui-stats.1` |
| [DH-ADR-CLI-3](../areas/app-cli.md#dh-adr-cli-3) | Standard output carries only the answer ("Scope boundary of this record" sentence since the fix pass) | `L:bundle.headless.8` |
| [DH-ADR-HOST-7](../areas/host-telemetry.md#dh-adr-host-7) | Reporting channels are memory-only; a loss is counted and shown | `L:client.product-analytics.4` |

**User-interface areas**

| Decision record | The boundary it states | Units |
|---|---|---|
| [DH-ADR-UICONV-7](../areas/ui-conversation.md#dh-adr-uiconv-7) with [DH-ADR-REACT-1](../libs/react.md#dh-adr-react-1) | A half-typed answer to a question stays in its tab | `L:client.ui-user-questions.1` |
| [DH-ADR-UICHAT-8](../areas/ui-chat.md#dh-adr-uichat-8) | No reading script: where a browser lacks scroll anchoring, a reflow may move the reader's line (moved here from the platform tables in the re-sweep) | `L:client.ui-layout.4` |
| [DH-ADR-REACT-2](../libs/react.md#dh-adr-react-2) | Finished blocks are frozen while a reply streams; a late link definition is resolved when the reply settles | `L:client.ui-primitives.4` |
| [DH-ADR-DIFF-1](../libs/diff.md#dh-adr-diff-1) | A diff is bounded and says when it is coarse | `L:client.ui-primitives.2` |
| [DH-ADR-UICORE-6](../areas/ui-core.md#dh-adr-uicore-6) | Shortcuts bind physical keys; the browser's keys stay with the browser | `L:client.ui-shortcuts.2` |
| [DH-ADR-UICHAT-2](../areas/ui-chat.md#dh-adr-uichat-2) | A mention is its text, checked once at admission: it does not follow the file later, and a typed name equals a picked one | `L:client.ui-reference.3`, `L:client.ui-skill.2` |
| [DH-ADR-UICHAT-7](../areas/ui-chat.md#dh-adr-uichat-7) | Secrets are masked by published format and by store-released value, not by name | `L:client.ui-secret-mask.1` |
| [DH-ADR-UIWORK-7](../areas/ui-work.md#dh-adr-uiwork-7) | A comparison is drawn from stored hunks, coloured hunk by hunk | `L:client.ui-deliverables.6` |
| [DH-ADR-UIWORK-8](../areas/ui-work.md#dh-adr-uiwork-8) | A preset is inspected, not edited, in the web view | `L:client.ui-agent-preset.1` |
| [DH-ADR-UISIDE-2](../areas/ui-sidebar-workspace.md#dh-adr-uiside-2) | Reading state is one person's own; no counts or receipts for others | `L:client.ui-session-status.8` |
| [DH-ADR-UISIDE-3](../areas/ui-sidebar-workspace.md#dh-adr-uiside-3) | The side panel belongs to a session and keeps no operation log, so no undo | `L:client.ui-entry-customization.5`, `L:client.ui-sidebar-right.2`, `L:client.ui-sidebar-right.5` |
| [DH-ADR-UISIDE-5](../areas/ui-sidebar-workspace.md#dh-adr-uiside-5) | A preview never edits ("Scope boundary of this record" sentence since the fix pass) | `L:client.ui-sidebar-documentpreview.1` |
| [DH-ADR-UISIDE-8](../areas/ui-sidebar-workspace.md#dh-adr-uiside-8) and [DH-ADR-HOST-8](../areas/host-telemetry.md#dh-adr-host-8) | The directory picker lists and creates, nothing else (scope sentences in both since the fix pass) | `L:client.ui-directory-picker-browse.1` |
| [DH-ADR-UISIDE-9](../areas/ui-sidebar-workspace.md#dh-adr-uiside-9) | The panel's sections cannot be hidden or reordered | `L:client.ui-session-panel.3` |
| [DH-ADR-HUMAN-6](../areas/interaction-credentials.md#dh-adr-human-6) with [DH-ADR-UISET-8](../areas/ui-settings-plugins.md#dh-adr-uiset-8) | Automatic review is chosen per session, never as a default | `L:client.ui-permission-presets.2` |

**Experimental areas**

| Decision record | The boundary it states | Units |
|---|---|---|
| [DH-ADR-XFORGE-1](../areas/x-forge-worktree.md#dh-adr-xforge-1) with the owner's [DH-ADR-38](../design.md#dh-adr-38) | The code-host UI is status only and the forge seam is read-only and host-side; the model uses `gh` or `glab` in the shell | `L:experimental.forge-bundle.2`, `L:experimental.forge-session.4` |
| [DH-ADR-XFORGE-2](../areas/x-forge-worktree.md#dh-adr-xforge-2) | The harness holds no forge token: `gh` or `glab` must be installed and signed in, for listed hosts only | `L:experimental.forge-github.1`, `L:experimental.forge-gitlab.1` |
| [DH-ADR-XFORGE-3](../areas/x-forge-worktree.md#dh-adr-xforge-3) | A code host is contacted only while someone is looking: no background polling, no notifications, no webhook intake | `L:experimental.client-ui-forge.2`, `L:experimental.forge-session.3`, `L:experimental.forge.1` |
| [DH-ADR-XFORGE-4](../areas/x-forge-worktree.md#dh-adr-xforge-4) | Binding rules never guess and never execute | `L:experimental.forge-session.1` |
| [DH-ADR-XFORGE-6](../areas/x-forge-worktree.md#dh-adr-xforge-6) with [DH-ADR-XFORGE-8](../areas/x-forge-worktree.md#dh-adr-xforge-8) | A worktree move never widens what a session may write; worktrees are created explicitly and entered only inside the write roots | `L:experimental.worktree.1`, `L:experimental.tool-worktree.2` |
| [DH-ADR-XFORGE-7](../areas/x-forge-worktree.md#dh-adr-xforge-7) | Cleanup never deletes a branch or a checkout with uncommitted files | `L:experimental.worktree.2` |
| [DH-ADR-XSHARE-3](../areas/x-liveshare-webworker.md#dh-adr-xshare-3) with the owner's [DH-ADR-37](../design.md#dh-adr-37) | A viewer watches and cannot act: no files, no terminal | `L:experimental.liveshare.4` |
| [DH-ADR-XINSPECT-4](../areas/x-inspector-timeline.md#dh-adr-xinspect-4) | Statistics are one fold over the log, not a sampler | `L:experimental.client-ui-stats.8` |
| [DH-ADR-XINSPECT-5](../areas/x-inspector-timeline.md#dh-adr-xinspect-5) | A task update is a whole-tree event, kept on purpose | `L:experimental.timeline.2` |
| [DH-ADR-XCUA-1](../areas/x-cua-browser-voice.md#dh-adr-xcua-1) | Launching an application is unavailable: the model cannot start one | `L:experimental.cua-windows.7` |
| [DH-ADR-XCUA-2](../areas/x-cua-browser-voice.md#dh-adr-xcua-2) | The browser provider speaks the Chrome protocol: "Chromium family only" | `L:experimental.browser-use-chrome-devtools-mcp.1`, `L:experimental.browser-use-playwright-mcp.1` |
| [DH-ADR-XCUA-5](../areas/x-cua-browser-voice.md#dh-adr-xcua-5) with [DH-ADR-XCUA-4](../areas/x-cua-browser-voice.md#dh-adr-xcua-4) | Voice only proposes text for the draft: no automatic send, no always-on microphone, no streaming; recognition runs on the host | `L:experimental.client-ui-voice-input.1`, `L:experimental.speech-to-text.1` |
| [DH-ADR-XHOOKS-1](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-1) | Hooks are operator-configured commands with a closed set of answers: they restrict and add context, never rewrite, pre-approve or replace; other vendors' events and handler kinds outside that set are not offered | `L:experimental.hook-protocol.1`, `L:experimental.hook-protocol.3`, `L:experimental.hooks-claude-code.1`, `L:experimental.hooks-claude-code.2`, `L:experimental.hooks-claude-code.3`, `L:experimental.hooks-claude-code.4`, `L:experimental.hooks-claude-code.5`, `L:experimental.hooks-claude-code.6`, `L:experimental.hooks-claude-code.9`, `L:experimental.hooks-codex.1`, `L:experimental.hooks-codex.4` |
| [DH-ADR-XHOOKS-2](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-2) | A hook source is the shared JSON file: no inline TOML form, synchronous command handlers only ("Scope boundary of this record" sentence since the fix pass) | `L:experimental.hooks-codex.8` |
| [DH-ADR-XHOOKS-4](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-4) | Automatic review is optional, means full access gated by a fallible reviewer, has a fixed ceiling, and does not reach inside another vendor's agent (the last moved here from the platform tables in the re-sweep) | `L:experimental.auto-review.1`, `L:experimental.auto-review.3`, `L:experimental.auto-review.4`, `L:experimental.auto-review.5` |
| [DH-ADR-XHOOKS-6](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-6) | Ralph is a foreground loop of fresh children from one provider | `L:experimental.tool-ralph.2`, `L:experimental.tool-ralph.3`, `L:experimental.tool-ralph.4` |
| [DH-ADR-XHOOKS-7](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-7) | Delegation to an external runtime is one-shot, in an operator-fixed permission mode | `L:experimental.tool-subagent-oar.1` |
| [DH-ADR-SUBAGENT-7](../areas/subagent-team.md#dh-adr-subagent-7) | An external product runs in its own mode, not under the harness sandbox | `L:experimental.subagent-oar.5` |
| [DH-ADR-XHOOKS-8](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-8) | Control of a terminal passes to the person and does not come back; a persistent shell cannot be widened; reading a person's terminal never waits | `L:experimental.terminal-panel.1`, `L:experimental.terminal-panel.2`, `L:experimental.terminal-panel.4` |
| [DH-ADR-XMISC-6](../areas/x-recommendations-misc.md#dh-adr-xmisc-6) | The all-prompts title provider has no weighting setting ("Scope boundary of this record" sentence since the fix pass) | `L:experimental.session-title-all-prompts-llm.2` |

### 5.2 Boundaries that had no complete decision record at the review

**None is without a record now.** At the review each of these 16 units was deliberate, by
the area's own words, but no numbered decision said so, and
[DH-ADR-1](../design.md#dh-adr-1) lets a part be left out only by a recorded decision.
The owner directed that each gets a record ([DH-ADR-40](../design.md#dh-adr-40)). The
last column names the record that the convergence round wrote; each was read in the
specification as it stands. Most are marked "recommended default, not confirmed by the
owner" in the record itself, so the owner can still overturn them.

| Boundary | Units | Where it was stated at the review | The record that states it now |
|---|---|---|---|
| **Small helpers stay narrow.** A file's language and an export's name come from the name, never from content; only `~` and `~/` expand in a path; the pick-list filter matches in order and tolerates no typing errors; the file-reference ranker is a separate package | `L:util.code-language.2`, `L:util.file-drag.1`, `L:util.home-paths.1`, `L:util.text-search.1`, `L:util.text-search.3` | The deliverable `DH-D-UTIL-5` and the debt table | New record [DH-ADR-UTIL-9](../areas/util-test-support.md#dh-adr-util-9) "The small helpers stay narrow on purpose", items (a) to (d), with the reason for each rule and what would reopen it |
| **The command-output frame is not a terminal emulator.** Cursor addressing and screen switches are stripped; an interactive program belongs in the terminal island | `L:client.ui-primitives.17` | Requirement R2 of the ANSI library specification | A paragraph "Command output" in the tradeoff of [DH-ADR-UIKIT-3](../areas/ui-kit.md#dh-adr-uikit-3) |
| **Continue is offered after a fixed list of six failure codes**, independent of the provider's retry settings | `L:client.ui-conversation.3` | The implementation text of `DH-D-API-5` | A sentence in the decision of [DH-ADR-API-3](../areas/api.md#dh-adr-api-3): the list is fixed, and a new code joins it by a change to the specification, not by configuration |
| **The files view has no search inside file contents**, no artifact filter, no drag-and-drop and no rename | `L:client.ui-sidebar-files.1` | The debt table; `DH-ADR-UISIDE-6` covers the tree but not this list | A sentence in the tradeoff of [DH-ADR-UISIDE-6](../areas/ui-sidebar-workspace.md#dh-adr-uiside-6) that names all four absences as scope |
| **The harness obtains and renews no TLS certificate.** The operator supplies it | `L:bundle.web-app.2` | The ledger row; `DH-ADR-HOST-2` requires TLS but does not say who obtains it | A sentence in the tradeoff of [DH-ADR-HOST-2](../areas/host-telemetry.md#dh-adr-host-2): the harness obtains and renews no certificate, and the operator supplies the pair |
| **No repository-configured program runs and nothing is fetched implicitly** when the harness inspects or creates a checkout: Git filters are skipped (so a Git LFS file can count as changed and stays a pointer), and only the one named branch is fetched | `L:experimental.client-ui-forge.1`, `L:experimental.worktree.3`, `L:experimental.worktree.4` | The "upstream decisions" table of the area (verdict: keep) | Item (a) of the decision of [DH-ADR-XFORGE-6](../areas/x-forge-worktree.md#dh-adr-xforge-6), which states the rule and its cost: hooks, the filesystem monitor and the filters are switched off for the call, and only the one named branch is fetched |
| **What a status read does not cover.** Working-tree changes made outside the session are seen at the next trigger, not watched; a pull request whose head is in a fork is not discovered by branch; GitLab paid-tier fields are not read | `L:experimental.client-ui-forge.4`, `L:experimental.forge-github.5`, `L:experimental.forge-gitlab.6` | The ledger rows ("deferred") | New record [DH-ADR-XFORGE-9](../areas/x-forge-worktree.md#dh-adr-xforge-9) "What a status read does not cover is deferred, with what would reopen each", items (a) to (c) |
| **Ralph has no evaluator.** The worker declares completion | `L:experimental.tool-ralph.1` | The ledger row; the goals area records the same for goals in `DH-ADR-JOBS-10` | A sentence in the tradeoff of [DH-ADR-XHOOKS-6](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-6): ralph has no evaluator; the bound on rounds and the person reading the result are the control |

### 5.3 Boundaries that need an owner decision or a second look

These units are classified above. They were named again here at the review because a
question about them was open, or because the reviewer thinks the recorded reason is
thinner than the cost to users. Each bullet now says where the question stands. The
owner was asked about the first only; for the others the owner's instruction was to apply
the recommended option and list it as "not confirmed by the owner". They are described by subject; no unit is listed a second time.

- **The free web translation endpoints: decided.** At the review this register
  recommended dropping them. The owner decided otherwise
  ([DH-ADR-36](../design.md#dh-adr-36)): the web provider is specified and tested but is
  not in the shipped bundle, which translates with the user's own model; an operator may
  add the provider in a layer, where it is off by default and counts as a sender. The
  four platform limits in section 4.6 therefore stay, and reach only an installation
  whose operator added the provider.
- **The external-runtime bridge runs outside the harness sandbox, and five of its
  runtimes are untested** (`DH-ADR-SUBAGENT-7`, [DH-ADR-23](../design.md#dh-adr-23)).
  The decisions say so honestly. The owner kept the bridge without saying whether
  "kept" covers runtimes nobody has run, and no document designs a sandbox policy that
  would grant an external agent the network and its own sign-in files and nothing else.
  Inference: such a policy is expressible with the write roots and the network dimension
  the sandbox already has; whether a given native agent works under it can only be
  learned by running it. Still open after the convergence round:
  [subagent-team.md](../areas/subagent-team.md) section 10 records it as a fact that
  needs a run, and an external run reports `confined: by-product` until then.
- **Transcript replay over ACP** (`DH-ADR-SDK-9`). Editors that speak ACP use
  `session/load` to reopen a conversation. After the convergence round the area
  specification keeps it not offered, as a recommended default the owner has not
  confirmed (its section 10): a replay would be cheap, but it moves ACP toward a user
  interface.
- **Jobs and terminals end with the host** (`DH-ADR-JOBS-1`, `DH-ADR-API-7`). The sibling
  register raises the same point in its section 5.3: with no desktop application there is
  no tray process that keeps the host alive, so every restart of `dsh` ends the user's
  terminals and background commands. Three units in this register rest on it.
- **The question tool is offered to the model when nobody can answer**
  (`DH-ADR-SECURITY-5`). The call then fails closed, which is right, but the model spends
  a turn finding out. The bundle area asks the interaction area whether the tool should
  be hidden when no answerer is bound; the same question is open for the image tool on a
  text-only model. After the convergence round the bundle area still lists "the question
  tool hidden when no answerer is bound" among the clauses its owners have not confirmed
  in their own text; the `acp` and `headless` profiles offer no question tool at all.
- **A half-typed answer to a question is lost with its tab, while a half-typed message
  is kept on the host** (`DH-ADR-REACT-1` against `DH-ADR-UICHAT-4`). Upstream kept the
  answer across a browser restart. The two drafts are the same kind of thing for a user;
  the conversation area should say why they are stored differently, or store them the
  same way.

## 6. Rows reclassified at the review as resolved or not applicable

These were reclassified by the review itself, before the convergence round. The twelve
units resolved in that round are in [section 3.2](#32-resolved-in-the-convergence-round).
A fifth, `L:client.ui-primitives.15` (the status mark), was marked not applicable at the
review; the re-sweep took that back, because the Go design builds the same five-state
mark, and it is now a record gap in section 3.1. The two Stagehand rows stay not
applicable: their feature is dropped by owner decision DH-ADR-24, which their reasons say
and their links include.

| Unit | Now | The mechanism, or the reason it cannot arise |
|---|---|---|
| `L:test-support.session-snapshot.3` | resolved | Upstream's limit was a leftover of a migration: some snapshot behaviours could not move off the ACP adapter. The Go replay runner has one driver per surface behind one interface and each scenario names its driver (`DH-D-UTIL-7` in [util-test-support.md](../areas/util-test-support.md)), so a behaviour whose stimulus is an ACP client is driven through ACP by design and no adapter is a remainder. |
| `L:bundle.base.3` | resolved | Upstream's bullet described a Windows-only difference in temporary directories. [DH-ADR-SANDBOX-4](../areas/sandbox-ssh.md#dh-adr-sandbox-4) gives every confined start on every platform the session's own scratch directory as a write root and as `TMPDIR`, `TMP` and `TEMP`, also under `read-only`, and lists the real paths to the model. The private directory is the stated contract everywhere, not a Windows exception. |
| `L:experimental.browser-use-stagehand-native.1` | not applicable | A limit of the Stagehand provider, which is not built ([DH-ADR-24](../design.md#dh-adr-24); [DH-ADR-XCUA-2](../areas/x-cua-browser-voice.md#dh-adr-xcua-2) drops the provider whole). The Chromium-only boundary of the provider that is built is carried under `DH-ADR-XCUA-2` in section 5.1. |
| `L:experimental.browser-use-stagehand-native.5` | not applicable | A limit of the same dropped provider. The fact that an attached browser can also be changed by its user is carried for the built providers in section 4.5. |

The third Stagehand unit (its second bullet) stays carried in section 4.5: its limit, that
a browser cannot be rebuilt from a log, holds for the provider that replaces Stagehand,
and no other unit in these areas records it.

## 7. Limits of this review

- It is a document review. No claim here was tested, and the proposals in section 3 are
  not designed in detail; each names the specification that would have to carry the
  detail.
- The area specifications were being revised by other agents while this review ran. Where
  a specification and a ledger row disagreed, the row's reason and justification were the
  anchor. A decision cited here may have been reworded since. This register did not read
  every specification from end to end: it read each area's decisions, its debt table and
  its open questions, the rows' own justifications, and upstream text where a claim
  looked doubtful.
- Three statements in the review's entries were inference. Two are settled by the
  specifications since: the palette is recorded from the byte stream and does not depend
  on the emulator ([U-K2-8](#u-k2-8)), and the jobs area's settle record names the
  originating command ([U-K2-12](#u-k2-12)). The third is still unverified: that the
  static Windows proxy setting is readable without cgo and that macOS prints its setting
  through `scutil` ([U-K2-2](#u-k2-2)); the area specification lists it among the facts
  that need a run.
- **The refresh pass** read, for each entry, the place in the specification where its
  label appears, the decision and deliverable there, and the test or scenario row that
  names it. It did not re-read whole specifications, and it did not check that a test as
  described would in fact catch the defect. A unit is called resolved because the
  mechanism, a deliverable and a named proof are written down, not because anything ran.
- Many of the outcomes are "recommended defaults, not confirmed by the owner". If the
  owner overturns one, the unit's class changes with it.
- Facts about other companies' products (browsers, GitHub, GitLab, LibreOffice, the Cua
  driver, OAR, the vendor's account and export formats) are taken from the library and
  area specifications and from upstream READMEs, not from those products' own
  documentation. The statement in section 4.3 that the PDF bitmap cap equals mobile
  Safari's per-canvas limit is the reviewer's recollection and should be checked before
  it is relied on.
- **Stages.** Every limit and boundary row names [DH-S-59](../impl.md#dh-s-59),
  the plan's stage for the known limits of these areas. After the final re-check no row
  is unresolved, so every carried row names it.
- **The final re-check** read each new "Scope boundary of this record" sentence against
  the row it serves. One row's reason was narrowed to match its sentence:
  `L:experimental.plugin-recommendations.2` now says that no background call looks for
  suggestions and no profile is kept, not that a suggestion is made only when asked,
  because DH-ADR-XMISC-3 lets the model suggest a feature inside a turn the user started.
- **The re-sweep** read, for each of the 206 carried rows and the 3 not-applicable rows of
  the review, and for the 185 not-applicable rows the area agents wrote in these areas,
  the text of the decision records the row links, and searched the other decision records
  of the area for the boundary when the linked one did not state it. It applied the tests
  of the audit strictly: a boundary written only in a debt table, a section 10 or an
  implementation paragraph does not count as stated by a record. Judgement still enters:
  where a record states the main clause of a several-clause boundary and only a minor
  clause is missing, the row stays a scope boundary and names the missing clause
  (`L:boot.plugin-manager.11`, `L:preset.agent-preset-registry.1`,
  `L:client.ui-sidebar-terminal.4`, `L:experimental.tool-worktree.2`,
  `L:experimental.client-ui-voice-input.1`, `L:experimental.speech-to-text.1`,
  `L:experimental.tool-ralph.4`); where the missing clause is a main one, the row is
  unresolved (`L:bundle.headless.9`, `L:experimental.api-speech-to-text.1`).
- The figure of 16,777,216 pixels as mobile Safari's per-canvas limit is the reviewer's
  recollection, not checked; it no longer carries a classification.

## 8. Rows replaced outside the 223

The integration agents could not edit tech-debt rows, so some rows that this review had
not judged named things that were renamed or changed, or carried a label the audit found
wrong. All replacements are in the K2 fragment, which is a later wave and wins. None is
part of the 223.

### 8.1 Stale resolved rows (the refresh pass)

Seven rows that were `resolved` already named things the convergence round renamed or
changed. They keep their disposition with a corrected reason.

| Unit | Disposition | What was stale | What the row says now |
|---|---|---|---|
| `L:boot.plugin-manager.5` | resolved | "Replacing a plugin is a restart of its entry" | A replaced plugin takes effect when it is added again, because a grant binds to an approved copy ([DH-ADR-EXT-3](../areas/extensions.md#dh-adr-ext-3)) |
| `L:boot.plugin-manager.9` | resolved | A replaced directory takes effect by restarting the entry | The plugin runs from an approved copy; a changed directory takes effect when the owner adds it again |
| `L:boot.plugin-manager.10` | resolved | Removal "deletes no files" | Removal deletes the entry and never the operator's directory; the approved copy and the plugin's state go only with an explicit purge |
| `L:boot.plugin-manager.12` | resolved | An operation "downloads, copies and deletes nothing" | Nothing is downloaded; one approved copy is made before the entry is written and is unreferenced if the write fails |
| `UP-DEBT-found.A21.2` | resolved | The tool "declares a profile-wide change and the pipeline asks" | The tool has list, inspect, attach and detach only and never writes the profile ([DH-ADR-30](../design.md#dh-adr-30)); the one consent is asked by the extension area ([DH-ADR-EXT-8](../areas/extensions.md#dh-adr-ext-8)) |
| `L:bundle.acp-app.1` | resolved | The carrier was named `acp-server` | The carrier entry is `wire.acp` |
| `L:bundle.sdk-app.5` | resolved | The carrier was named `sdk-server` | The carrier entry is `wire.sdk` |

Two further reports named no row that exists: no tech-debt row of the SDK area mentions
an exit deadline or an exit controller, and none of the route area says that a stream
ends on a render failure. Nothing was replaced for them.

In the 223 rows of the review itself, wording was corrected where the convergence round
or an owner decision made it false: the four translation rows (the owner kept the web
provider out of the shipped bundle, [DH-ADR-36](../design.md#dh-adr-36)), the rows on
live sharing and on the code-host tool (now owner decisions
[DH-ADR-37](../design.md#dh-adr-37) and [DH-ADR-38](../design.md#dh-adr-38)), the plugin
manager row ([DH-ADR-30](../design.md#dh-adr-30)), the row on `session/load`, and the row
on external agents and the sandbox.

### 8.2 One resolved row whose reason described an old state (audit Q3-10)

| Unit | Disposition | What was stale | What the row says now |
|---|---|---|---|
| `L:experimental.client-ui-forge.6` | resolved | Recovery of a worktree removed by hand "needs the session-views area ... (open question 1b)" | Settled: [DH-ADR-VIEW-8](../areas/session-views.md#dh-adr-view-8) keeps every tool-made move inside the anchor, and recovery goes through `workdir.Access` with the reason `recover`, which the worktree area denies for a recorded worktree so that the card offers Restore |

A search of the other resolved rows in these areas for "open question", "asks of" and
"request to area" found four more that rest on a request to another area. Each request is
now met in the other specification, so the rows stay as they are: `L:bundle.acp-app.1`
(replaced in 8.1), `L:client.ui-goal.2` (the goal service's `Update` edits the round
limit, jobs-schedule-goal-plan.md section 6.2), `L:client.ui-sidebar-files.3` (a tree
entry carries a `hidden` flag, api.md section 6.2) and `L:client.ui-sidebar-files.5`
(`search` takes literal exclusions, api.md section 6.2). The resolved row
`L:client.ui-message-feedback.3`, which the audit found nominal (Q3-2), now holds:
ui-work.md places the rating control on every assistant-message record of the trajectory
and `DH-T-UIWORK-1` checks it; the area agent had already rewritten the row.

### 8.3 Not-applicable rows that belong to a feature dropped by decision (audit Q3-7)

A not-applicable row holds only if the limit cannot arise in a Go implementation of what
is built. The re-sweep read all 185 not-applicable debt rows the area agents wrote in these
areas. 106 exist only because of JavaScript, Node, npm, React, the TypeScript build or the
upstream build process, and stay as they are. 79 describe a feature that is not built because a
decision dropped it; their reasons said "is not built" or "there is no ...", and some had
no link. Each is replaced with the same disposition, a reason that opens "Dropped by
DH-ADR-n", and a link to that decision.

| Decision that drops the feature | What is dropped | Units |
|---|---|---|
| [DH-ADR-17](../design.md#dh-adr-17) | The in-browser preview runtime and its packer | `L:experimental.webworker-packer.1` to `.6`, `L:experimental.webworker-runtime.1` to `.20`, `L:util.lazy-require.2`, `L:experimental.deepseek-chat-import.6` (28) |
| [DH-ADR-13](../design.md#dh-adr-13), with [DH-ADR-BOOT-11](../areas/boot.md#dh-adr-boot-11) or [DH-ADR-WEBAPP-8](../areas/app-web.md#dh-adr-webapp-8) where named | Plugin download, discovery, auto-update and the market | `L:boot.plugin-manager.4`, `.6`, `.7`, `.8`; `L:util.plugin-detail.1`; `L:util.plugin-market-index.1`, `.2`; `UP-DEBT-found.A25.8` to `.12`; `L:client.ui-plugin-manager.1`, `.7`, `.8` (15) |
| [DH-ADR-18](../design.md#dh-adr-18) | The desktop application | `L:boot.plugin-manager.14`, `L:util.native-command.2`, `UP-DEBT-marker.apps/desktop/scripts/desktop-ci-version.mjs:13`, `UP-DEBT-found.A25.1`, `UP-DEBT-found.A25.5`, `L:client.ui-context-menu.1`, `L:client.ui-file-actions.2` (7) |
| [DH-ADR-24](../design.md#dh-adr-24) | The vendor release publisher | `UP-DEBT-found.A25.6`, `UP-DEBT-found.A25.7` (2) |
| [DH-ADR-2](../design.md#dh-adr-2) and [DH-ADR-11](../design.md#dh-adr-11) | Reading upstream's older session and browser-storage formats | `L:client.ui-tool.2`, `L:client.ui-entry-customization.2` (2) |
| [DH-ADR-XFORGE-5](../areas/x-forge-worktree.md#dh-adr-xforge-5) with [DH-ADR-38](../design.md#dh-adr-38) | The code-host changes view | `L:experimental.client-ui-forge.13`, `.14`, `.15` (3) |
| [DH-ADR-XINSPECT-1](../areas/x-inspector-timeline.md#dh-adr-xinspect-1) | The debugging target (DevTools hub, evaluation, client inspection) | `L:experimental.inspector.2`, `.3`, `.4`, `.5`, `.6`, `.9` (6) |
| [DH-ADR-19](../design.md#dh-adr-19) with [DH-ADR-XCUA-1](../areas/x-cua-browser-voice.md#dh-adr-xcua-1) | Upstream's first-party computer-use packages and the in-process driver | `L:experimental.cua-mcp.10`, `L:experimental.computer-use-cua-driver-native.2` (2) |
| [DH-ADR-XHOOKS-3](../areas/x-hooks-mods-terminal.md#dh-adr-xhooks-3) | The Claude Code mods bridge and its band | `L:experimental.claude-code-mods.1` to `.10`, `L:experimental.client-ui-claude-code-mods.1`, `.2` (12) |
| [DH-ADR-XMISC-7](../areas/x-recommendations-misc.md#dh-adr-xmisc-7), [DH-ADR-XMISC-2](../areas/x-recommendations-misc.md#dh-adr-xmisc-2) | The badge skill; personalized pages | `L:experimental.badge-skill-bundle.2`, `L:experimental.personalized-recommendations.2` (2) |

No not-applicable row in these areas turned out to be a limit the Go design still has; the
one such case the review had written itself, the status mark, is in section 3.1.

### 8.4 The audit's unit in these areas

`UP-DEBT-found.Q3.3` (upstream's model-menu browser test switched off on WebKit because
the engine crashed) is **resolved**; its row in `trace/fragments/Q3.jsonl` is replaced.
ui-chat.md now marks step (5) of scenario `DH-E-UICHAT-2`, the model picker, as
engine-sensitive: under owner decision [DH-ADR-39](../design.md#dh-adr-39) it also runs in
current Firefox and Safari, and a crash in any engine is a failed step, never a skip. What
is resolved is the skipped check; whether WebKit draws the picker correctly is what the
scenario will show when it runs.

## 9. Changed in the re-sweep

What moved, by unit, grouped by reason. "Before" is the state the refresh pass left.
Nothing was built or run; every move is a judgement about documents.

**Scope boundary to unresolved, because the linked record does not state the boundary
in its own words (20 units).** `L:acp.acp.2`, `L:util.timeout.4`, `L:bundle.headless.1`,
`L:bundle.headless.9`, `L:bundle.sdk-app.1`, `L:preset.persona.1`,
`L:client.ui-context-menu.3`, `L:client.ui-goal.1`, `L:client.ui-entry-customization.4`,
`L:client.ui-sidebar-documentpreview.6`, `L:client.ui-workspace.1`,
`L:client.ui-account-notifications.1`, `L:experimental.deepseek-chat-import.14`,
`L:experimental.timeline.3`, `L:experimental.api-speech-to-text.1`,
`L:experimental.cua-macos.15`, `L:experimental.hooks-claude-code.8`,
`L:experimental.webhook-github.4`, `L:experimental.cot-translation-bundle.1`,
`L:experimental.plugin-recommendations.2`. **Final re-check: all 20 are scope boundaries
again**, each under the record named in section 3.1, whose new "Scope boundary of this
record" sentence states the boundary with all its clauses.

**Platform limit to scope boundary, because a different design choice would remove the
limit and a record states the choice (4 units).** `L:client.ui-layout.4` (a reading
script could keep the line; DH-ADR-UICHAT-8 adds none), `L:experimental.auto-review.5`
(a product's permission prompts could be routed to the reviewer; DH-ADR-XHOOKS-4 says
foreign agents keep their own systems), `L:experimental.subagent-oar.3` (the bridge
receives the native agent's request; DH-ADR-OAR-1 answers by ending the run),
`L:experimental.subagent-oar.6` (one-shot is a kept contract of DH-ADR-OAR-1).

**Platform limit to unresolved, because the limit follows from a choice no record keeps
(2 units).** `L:client.ui-primitives.8` (the chosen tokeniser, audit Q3-3) and
`L:client.ui-sidebar-documentpreview.4` (pdf.js's default canvas cap). **Final re-check:
both are scope boundaries**, kept on purpose by DH-ADR-UICONV-4 and DH-ADR-UISIDE-5.

**Not applicable to unresolved (1 unit).** `L:client.ui-primitives.15`: the Go design
builds the same five-state mark, so the limit arises. **Final re-check: scope boundary**
under DH-ADR-UIKIT-1.

**Scope boundary, still scope, with a better link (many rows).** Where the fix pass added
a "Scope boundary of this record" sentence, or another record states the boundary more
directly, the row now quotes it and links it: for example `L:client.ui-primitives.3`
(DH-ADR-UIKIT-6), `L:client.ui-user-questions.1` (DH-ADR-UICONV-7),
`L:client.ui-permission-presets.2` and `L:experimental.auto-review.1` (DH-ADR-HUMAN-6),
`L:bundle.headless.8` (DH-ADR-CLI-3), `L:client.ui-directory-picker-browse.1`
(DH-ADR-UISIDE-8 and DH-ADR-HOST-8), `L:client.ui-sidebar-documentpreview.1`
(DH-ADR-UISIDE-5), `L:experimental.hooks-codex.8` (DH-ADR-XHOOKS-2),
`L:experimental.terminal-panel.4` (DH-ADR-XHOOKS-8),
`L:experimental.session-title-all-prompts-llm.2` (DH-ADR-XMISC-6),
`L:api.remote-access.5` (DH-ADR-37), `L:api.terminal-controller.1` and
`L:api.job-controller.2` (DH-ADR-EXEC-2), `L:experimental.subagent-oar.1` and `.2`
(DH-ADR-OAR-1). Links that named a deliverable the fix pass split now name the new one
(`DH-D-UTIL-9` to `-12`, `DH-D-BUNDLE-8`, `DH-D-BOOT-10`, `DH-D-API-10`).

**Stages.** Every carried row now names `DH-S-59`, the plan's stage for the known limits
and boundaries of these areas; no row is unresolved.

**Outside the 223.** 79 not-applicable rows relabelled as dropped by decision (section
8.3), one resolved row refreshed (section 8.2), and `UP-DEBT-found.Q3.3` resolved (section
8.4).

**Plain count of remaining debt in these areas: 0 units.** The re-sweep left 23 unresolved;
the final re-check closed all 23, because each now has a "Scope boundary of this record"
sentence that states it (section 3.1). The one fact still unverified is the Windows
registry read behind `L:util.http-proxy.1` (U-K2-2); the design holds either way.
