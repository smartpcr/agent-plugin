# Carried tech debt register: core areas A03 to A18

| | |
|---|---|
| **Reviewer** | K1 (debt review). The reviewer wrote none of the rows it judges. |
| **Covers** | The 190 tech-debt units that the area agents of A03, A04, A05, A06, A08 to A18 marked `carried`; every not-applicable debt row of those areas; and the audit units `UP-DEBT-found.Q3.1`, `.2` and `.8`, which fall in them |
| **Ledger rows** | [../trace/fragments/K1.jsonl](../trace/fragments/K1.jsonl) (one row per unit above, plus replacing rows listed in [section 8](#8-other-rows-this-review-replaced)); the three audit units in [../trace/fragments/Q3.jsonl](../trace/fragments/Q3.jsonl) |
| **State described** | After the targeted re-sweep of 2026-11-19, which followed the debt audit [../audit/q3-debt.md](../audit/q3-debt.md) and the specification fix pass |
| **Status** | Design only. **Nothing was built, run or tested.** "Resolved" means "a specification describes a decision, a deliverable and a planned test or scenario that remove the cause", and nothing more. |

**Remaining debt in these areas: 5 units**, all `carried` `unresolved`, in two entries
([section 3](#3-unresolved)): three facts nobody has obtained, and one claim about the
sandbox backends that waits for scenarios. The re-sweep found 21; after it, the owners
added the decision sentences and the UTF-16 support the register asked for, and a final
re-check verified each in the specification text ([section 5](#5-changed-in-the-re-sweep)).

## 1. How to read this register

Upstream deepseek-harness lists its own weaknesses: each package README has a section
"Known Limitations and Deferred Work", and the source has `TODO` markers. The ledger calls
each one a tech-debt unit. A unit that is still true of the Go design is `carried` and has
one of three classes. The re-sweep applied each test strictly.

**Platform limit.** A fact of an operating system, a protocol, a file format, the Go
runtime or another company's program that no reasonable design choice of this harness
removes. Each row names the fact and where it comes from (section 6). Example: Windows
consoles have no signal aimed at one group of processes, so an interrupt is a Ctrl-C to
every process on the console (`L:subprocess.subprocess-local.5`).

**Scope boundary.** Something the product deliberately does not do, where **a decision
record states the boundary in its own words**: a sentence a reader can point at, not a
consequence derived from a general rule or a sentence in a debt table. Example: the
policy stage cannot rewrite a call's arguments, because
[DH-ADR-TOOLS-3](../areas/tools.md#dh-adr-tools-3) says "logged arguments are never
rewritten" (`L:core.tools.2`). A record marked "recommended default, not confirmed by the
owner" counts, and the row says so.

**Unresolved.** Real debt: a weakness the design could remove, a fact it still has to
obtain, or a deliberate boundary that no record yet states. Each entry says what is
missing.

**Not applicable.** The limit cannot arise in a Go implementation of what is built. Where
it cannot arise because the feature is dropped, the row now says "dropped by" the
decision and links it (section 8).

**Unit names.** `L:<rest>` abbreviates `UP-DEBT-limit.packages.<rest>`. Each of the 190
units appears exactly once in sections 3, 4, 6 and 7; section 5 repeats the units that
changed class, so that a reader can see what moved.

## 2. Summary

Counts of the 190 reviewed units, by area, after the re-sweep.

| Key | Area specification | Carried | Platform limit | Scope boundary | Unresolved | Resolved |
|---|---|---:|---:|---:|---:|---:|
| A03 | [tools.md](../areas/tools.md) | 6 | 1 | 5 | 0 | 0 |
| A04 | [llm.md](../areas/llm.md) | 4 | 1 | 2 | 1 | 0 |
| A05 | [session-store.md](../areas/session-store.md) | 6 | 2 | 3 | 0 | 1 |
| A06 | [session-views.md](../areas/session-views.md) | 5 | 0 | 4 | 0 | 1 |
| A08 | [filesystem.md](../areas/filesystem.md) | 8 | 0 | 8 | 0 | 0 |
| A09 | [execution.md](../areas/execution.md) | 22 | 10 | 10 | 2 | 0 |
| A10 | [sandbox-ssh.md](../areas/sandbox-ssh.md) | 12 | 9 | 2 | 1 | 0 |
| A11 | [ptc-workflow.md](../areas/ptc-workflow.md) | 8 | 5 | 3 | 0 | 0 |
| A12 | [subagent-team.md](../areas/subagent-team.md) | 29 | 0 | 26 | 0 | 3 |
| A13 | [jobs-schedule-goal-plan.md](../areas/jobs-schedule-goal-plan.md) | 13 | 0 | 13 | 0 | 0 |
| A14 | [context-compaction-skill.md](../areas/context-compaction-skill.md) | 30 | 4 | 26 | 0 | 0 |
| A15 | [mcp-lsp-web-connector.md](../areas/mcp-lsp-web-connector.md) | 22 | 7 | 12 | 0 | 3 |
| A16 | [document-browser-computer.md](../areas/document-browser-computer.md) | 3 | 1 | 2 | 0 | 0 |
| A17 | [interaction-credentials.md](../areas/interaction-credentials.md) | 19 | 4 | 12 | 1 | 2 |
| A18 | [host-telemetry.md](../areas/host-telemetry.md) | 3 | 0 | 2 | 0 | 1 |
| | **Total** | **190** | **44** | **130** | **5** | **11** |

Of the 11 resolved: 2 since the first pass, 8 since the convergence round, 1 since the
re-sweep.

**Outside the 190, also in these areas:** three resolved units moved to carried
(`L:ssh.ssh.1` and `L:ssh.ssh-helper-runtime.1` to scope boundaries, and
`L:session-query.session-log-export.6`, which was not applicable, to a platform limit);
38 not-applicable rows, 25 of them re-issued with the dropping decision; and the three
audit units: `UP-DEBT-found.Q3.1` is a scope boundary, `.Q3.2` and `.Q3.8` are resolved.
None of these is debt.

**What the re-sweep checked.** All 129 scope-boundary rows against the full text of the
decision records they link; all 46 platform-limit rows for a named fact and a source; all
39 not-applicable debt rows of these areas; every row the audit or the fix pass named.
The `resolved` rows were not all re-read; those the audit named were.

## 3. Unresolved

Two entries, 5 units. The stage named is the build stage of the implementation plan
that delivers the area. Two entries of the re-sweep, U-K1-13 and U-K1-14, were closed by
the final re-check; their units are scope boundaries now (section 7), and section 5 says
what closed them.

<a id="u-k1-12"></a>
### U-K1-12 Three facts nobody has obtained yet (3 units)

| Unit | The missing fact | What is still missing | Stage |
|---|---|---|---|
| `L:llm.llm-deepseek-account.4` | The shape of the field that carries reasoning effort levels in the vendor's model listing | [llm.md](../areas/llm.md) §10 says scenario `DH-E-LLM-5` keeps a real listing, but its steps and assertions do not mention the field, and no deliverable or test covers the read. The embedded catalogue supplies effort levels until then | DH-S-35 |
| `UP-DEBT-marker.packages/account/deepseek-account/src/protocol.ts:245` | The vendor's business error codes and the wording for each | The list itself, and a named step that obtains it. The message catalogue is keyed by code, so wording can be added without a code change | DH-S-35 |
| `L:shell.pwsh-local.7` | Whether Windows PowerShell 5.1 reads non-ASCII standard input correctly when its console input code page is set to UTF-8 before it starts | Scenario `DH-E-EXEC-7` makes the measurement; it has not run, and the executor does not yet set the code page. Small impact: the 5.1 fallback only, and only in-process callers that pass standard input | DH-S-13 |

<a id="u-k1-15"></a>
### U-K1-15 "No backend reports a denied operation" is a fact still to establish (2 units)

- **Units.** `L:shell.bash-sandbox.2`, `L:sandbox.sandbox.3`.
- **What the user experiences.** A denied write inside a confined command shows as the
  command's own error text, with a hint that it may have been the sandbox.
- **Why it is not a platform limit yet.** The claim that no backend gives an unprivileged
  caller a typed denial report had no source (audit finding Q3-3). Since the final
  re-check, [sandbox-ssh.md](../areas/sandbox-ssh.md) (row `sandbox.3` of its §5) holds it
  as an assumption to settle per backend by three named scenarios: `DH-E-SANDBOX-1`
  (bubblewrap and Landlock), `DH-E-SANDBOX-4` (the Windows token) and `DH-E-SANDBOX-3`
  (macOS, where the backend reads the sandbox log as a hint, kept only if that scenario
  shows the records name the process). That is the right design for an unknown fact, but
  none of the scenarios has run, so no source can be named.
- **What is missing.** The facts. When the scenarios have run, each backend's half becomes
  a platform limit with the scenario as its source, or, for macOS, the log hint narrows
  the limit. Stage DH-S-27.

## 4. Resolved

**Resolved in the convergence round** (register entries proposed by the first pass and
designed into the specifications; each checked again in the re-sweep). Where a unit of an
entry is not resolved, its current class is in section 6 or 7.

| Label | Resolved units | Decision, deliverable, proof | Remainder |
|---|---|---|---|
| <a id="u-k1-3"></a>U-K1-3 | `L:team.agent-team.2`, `L:subagent.subagent-claude-code.9`, `L:subagent.subagent-codex.9` | `isolation: worktree` on a delegation target: [DH-ADR-40](../design.md#dh-adr-40) (owner), [DH-ADR-SUBAGENT-12](../areas/subagent-team.md#dh-adr-subagent-12), `DH-D-SUBAGENT-10`, `DH-T-SUBAGENT-32`, `DH-T-XFORGE-13`, `DH-E-SUBAGENT-7` | Off by default, by the owner's decision, so in the default configuration members share one checkout and a cancelled external run leaves its edits. Effects outside the checkout are never undone (a fact) |
| <a id="u-k1-5"></a>U-K1-5 | `L:feedback.message-feedback.3` | A request table in the home database: [DH-ADR-STORE-12](../areas/session-store.md#dh-adr-store-12), `DH-D-STORE-11`, [DH-ADR-SESSION-12](../areas/core-session.md#dh-adr-session-12), `DH-D-SESSION-9`, [DH-ADR-HUMAN-13](../areas/interaction-credentials.md#dh-adr-human-13), `DH-T-STORE-17`, `DH-E-STORE-1` | None |
| <a id="u-k1-6"></a>U-K1-6 | `L:lsp.lsp.2` | Symbols of a file, a symbol by name, and diagnostics: [DH-ADR-CONNECT-7](../areas/mcp-lsp-web-connector.md#dh-adr-connect-7), `DH-D-CONNECT-5`, `DH-T-CONNECT-21`, `DH-E-CONNECT-4` | Edits and the call hierarchy stay out, stated in the same record |
| <a id="u-k1-9"></a>U-K1-9 | `L:web.web.2`, `L:web.web-deepseek-api.1` | An optional `region` that providers declare and the result reports: [DH-ADR-CONNECT-8](../areas/mcp-lsp-web-connector.md#dh-adr-connect-8), `DH-D-CONNECT-6`, `DH-D-CONNECT-8`, `DH-T-CONNECT-23` | Recency and site filters come "when a backend honours them", stated in the same record; the vendor's site and ranking fields are still not read, because nothing uses them |
| <a id="u-k1-11"></a>U-K1-11 | `L:session.session-log-deepseek.3` | An elision record in place of an oversized event: [DH-ADR-STORE-10](../areas/session-store.md#dh-adr-store-10) (recommended default), `DH-D-STORE-9`, `DH-T-STORE-15`, `DH-E-STORE-4` | Holds only if the vendor accepts such a line; if it does not, the upload component is not shipped |

The other convergence entries ended outside "resolved": **U-K1-1** (process-tree owner)
and **U-K1-2** (process-tree memory limit) removed the Linux and Windows cause and left a
macOS platform limit; **U-K1-4** (follow-up to an external child) made the `dsh` runtime
child continuable and left a scope boundary for the other products; **U-K1-7** (MCP
prompts and subscriptions) added prompt templates and left subscriptions as a scope
boundary; **U-K1-8** (a fetched PDF) stores the PDF for the document tool and leaves text
extraction out as a scope boundary; **U-K1-10** (`cd` in a persistent shell) removed the
lasting case and left a fact of shells.

**Resolved in the first pass** (the cause was already removed elsewhere):
`L:session.session-projection.2` (the host renders pages and an unknown key is an error,
`DH-D-VIEW-1`, [DH-ADR-VIEW-1](../areas/session-views.md#dh-adr-view-1));
`L:interaction.tool-ask-user.3` (a child's question goes to whoever answers for the root
session, [DH-ADR-HUMAN-2](../areas/interaction-credentials.md#dh-adr-human-2) rule 5,
[DH-ADR-SUBAGENT-6](../areas/subagent-team.md#dh-adr-subagent-6), owner decision
[DH-ADR-16](../design.md#dh-adr-16)).

**Resolved in the re-sweep:** `L:host.open-in-app.2`. A moved macOS application is now
found by one Spotlight query by bundle identifier, run through the host starter at
resolution time only ([DH-ADR-HOST-9](../areas/host-telemetry.md#dh-adr-host-9),
`DH-D-HOST-9`, `DH-T-HOST-28`, `DH-E-HOST-10`); an application Spotlight does not index is
added by an operator entry.

## 5. Changed in the re-sweep

Each unit below also appears in its current class in section 3, 4, 6 or 7.

| From | To | Units | Reason |
|---|---|---|---|
| scope boundary | unresolved | `L:session.session-title-first-prompt-llm.1`, `L:subagent.subagent-acp.3`, `L:subagent.subagent-claude-code.8`, `L:subagent.subagent-codex.8`, `L:subagent.subagent-fork-in-process.1`, `L:subagent.subagent-spawn-in-process.1`, `L:goal.tool-goal.1`, `L:goal.tool-goal.2`, `L:credentials.credentials.2`, `L:identity.anonymous-user-id.3`, `L:sandbox.sandbox.1`, `L:sandbox.sandbox-policy.2`, `L:shell.bash-sandbox.1` (13) | The linked record does not state the boundary in its own words (entry U-K1-13, closed by the final re-check below) |
| scope boundary | unresolved | `L:fs.fs.1`, `L:fs.tool-fs.3`, `L:fs.tool-str-replace-editor.1` (3) | The record states the rule, but its reason does not cover a UTF-16 file with a byte-order mark (entry U-K1-14, closed by the final re-check below) |
| platform limit | unresolved | `L:shell.bash-sandbox.2`, `L:sandbox.sandbox.3` (2) | The fact is asserted without a source and may not hold on macOS ([U-K1-15](#u-k1-15)) |
| platform limit | scope boundary | `L:subagent.subagent-claude-code.6` (1) | Follows from the chosen driver, not from the vendor; `DH-ADR-CLAUDEAGENTSDK-1` now states it as a boundary of that record (audit Q3-3) |
| scope boundary | platform limit | `L:shell.pwsh-sandbox.1` (1) | No record states that reads are unconfined; on Windows it is a fact of the write-restricted token, as for `L:sandbox.sandbox-windows-acl.8` |
| scope boundary | resolved | `L:host.open-in-app.2` (1) | `DH-ADR-HOST-9` now adds the Spotlight query (audit Q3-11) |
| resolved | scope boundary | `L:web.web-fetch-http.2`, `L:web.web.3` (2) | The PDF is stored and rendered as page images; the text decoding upstream deferred stays out, as `DH-ADR-CONNECT-8` states (audit Q3-6) |
| resolved | scope boundary | `L:ssh.ssh.1`, `L:ssh.ssh-helper-runtime.1` (2, outside the 190) | A Windows remote host is out by a scope sentence of `DH-ADR-SANDBOX-8`, and replay by `DH-ADR-SANDBOX-10` (audit Q3-2) |
| not applicable | platform limit | `L:session-query.session-log-export.6` (1, outside the 190) | A clipboard write cannot be undone in any design (audit Q3-7) |
| unresolved | scope boundary | `UP-DEBT-found.Q3.1` (audit unit) | `DH-ADR-SANDBOX-11` and `DH-ADR-SECURITY-12` state that the harness is unaudited and not a guarantee of isolation, and make a review a release gate |
| unresolved | resolved | `UP-DEBT-found.Q3.2`, `UP-DEBT-found.Q3.8` (audit units) | A real-model counter-test of the policy text (`DH-E-SANDBOX-7`, a merge gate for any change to it); a defined zero-source search result (`DH-D-CONNECT-11`, `DH-T-CONNECT-24`, `DH-E-CONNECT-8`) |

**Final re-check, after the owners answered section 9 of the re-sweep.** Each claim was
checked against the record text; all held.

| From | To | Units | What closed it |
|---|---|---|---|
| unresolved | scope boundary | `L:session.session-title-first-prompt-llm.1`, `L:subagent.subagent-acp.3`, `L:subagent.subagent-claude-code.8`, `L:subagent.subagent-codex.8`, `L:subagent.subagent-fork-in-process.1`, `L:subagent.subagent-spawn-in-process.1`, `L:goal.tool-goal.1`, `L:goal.tool-goal.2`, `L:credentials.credentials.2`, `L:identity.anonymous-user-id.3`, `L:sandbox.sandbox.1`, `L:sandbox.sandbox-policy.2`, `L:shell.bash-sandbox.1` (13, former U-K1-13) | A "Scope boundary of this record" sentence now in `DH-ADR-VIEW-6`, `DH-ADR-SUBAGENT-5`, `DH-ADR-SUBAGENT-10`, `DH-ADR-JOBS-9`, `DH-ADR-JOBS-10`, `DH-ADR-HUMAN-9`, `DH-ADR-HUMAN-12`, `DH-ADR-SANDBOX-3` and `DH-ADR-SECURITY-10` |
| unresolved | scope boundary | `L:fs.fs.1`, `L:fs.tool-fs.3`, `L:fs.tool-str-replace-editor.1` (3, former U-K1-14) | `DH-ADR-FS-11` item (a) now decodes UTF-16 with a byte-order mark and writes it back in the same form (`DH-D-FS-1`, `DH-D-FS-6`, test `DH-T-FS-27`). The units stay carried, not resolved, because the rest of the upstream limit (other encodings without a mark, audio and video) is kept and stated by the same record as its scope boundary. `DH-T-FS-27` does not exercise `str_replace_editor`, which shares the decoding |
| unresolved | unresolved | `L:shell.bash-sandbox.2`, `L:sandbox.sandbox.3` (U-K1-15) | Not closed: the specification now names the scenarios that establish the fact per backend, and none has run |

`DH-ADR-SANDBOX-11` and `DH-ADR-SECURITY-12` now refer to each other.

**Refreshed without a change of class:** the three U-K1-3 rows now link
`DH-D-SUBAGENT-10` and state that isolation is off by default; `L:lsp.lsp.2` states that
the call hierarchy is out by its record; the three model-backed search rows link the
provider's own deliverable `DH-D-CONNECT-11`; the two terminal rows that end with the host
link `DH-ADR-EXEC-9`; `L:interaction.user-approval.4` is reworded to the headless rule of
`DH-ADR-CLI-7` (section 8).

**Rows checked and kept in their class.** 111 of the 129 scope-boundary rows, whose
record states the boundary; 43 of the 46 platform-limit rows, with a named fact; 13 of
the 39 not-applicable rows, which exist only because of JavaScript, npm, Node, koffi,
React or Electron (25 more stay not applicable with a corrected reason).

## 6. Platform limits

| Platform or third party | The limit | Units | What the user sees | Where the fact comes from |
|---|---|---|---|---|
| **Go runtime** | Code inside the host cannot be stopped or memory-limited from outside | `L:core.tool-executor-native.1`, `L:interaction.commands.2`, `L:ptc-runtime.ptc-runtime-node.14` | A body that ignores cancellation is settled "outcome unknown" and listed as detached; first-party tools bound their own reads | The Go runtime has no call that stops a goroutine and no per-goroutine memory limit |
| **Windows consoles** | No signal aimed at one process group; no stop or hang-up | `L:subprocess.subprocess-local.5`, `L:shell.tool-pwsh-persistent.6` | Interrupt is Ctrl-C to the whole console; unsupported signals are refused | Windows console design; upstream `packages/subprocess/subprocess-local/README.md` |
| **Windows restricted token** | Restricts writes only; hard links alias files; trees stamped by another sandbox are unreadable; sockets cannot be restricted | `L:shell.pwsh-sandbox.1`, `L:sandbox.sandbox-windows-acl.8`, `L:shell.pwsh-sandbox.3`, `L:sandbox.sandbox-local.2`, `L:sandbox.sandbox-windows-acl.9` | Every start reports `partial` with the named effects; `require: full` is refused; network denied does not run | `WRITE_RESTRICTED` semantics; AppContainer rejected by upstream because it needs read grants on every path (`packages/sandbox/sandbox-windows-acl/README.md:125`, `:160`, `:189`) |
| **Windows named pipes** | A confined program cannot give its children named-pipe standard streams | `L:sandbox.sandbox-windows-acl.6`, `L:shell.tool-pwsh.1` | Stated in the policy text; inherited and anonymous pipes work | Default descriptor of a new named pipe; `packages/sandbox/sandbox-windows-acl/README.md:186` |
| **Windows access lists** | The first grant walks every descendant | `L:sandbox.sandbox-windows-acl.7` | A one-time wait at consent, with progress and cancel | Eager propagation of inheritable entries; `README.md:187` |
| **macOS** | No owner for a range of processes; no memory limit for a process tree | `L:subprocess.subprocess-local.2`, `L:subprocess.subprocess-local.7`, `L:ptc-runtime.ptc-runtime-node.4`, `L:ptc-runtime.ptc-runtime-node.3` | Survivors are named; a run says `limits: unavailable`. The same on a Linux host where no user service manager answers | macOS has process groups and sessions only, no job object or control group |
| **macOS** | The only confinement tool, `sandbox-exec`, is deprecated | `L:sandbox.sandbox-local.4` | If removed, the backend is unavailable at start-up and confined modes do not run | Apple; `DH-ADR-SECURITY-2` |
| **Linux kernel** | An older kernel knows fewer Landlock access kinds | `L:sandbox.sandbox-local.3` | `partial`, with the missing kinds named | The kernel's Landlock ABI version |
| **macOS and Windows** | Nothing proves that another process waits for terminal input | `L:terminal.terminal-bash.2`, `L:shell.tool-bash-persistent.2` | The call runs to its timeout; the wait reason says `inferred_idle` | Upstream `packages/terminal/terminal-bash/README.md` (exact only where the provider can prove it, Linux) |
| **Shells** | What a command does inside the shell process is invisible outside it | `L:context.agent-instructions.1` | A `cd` inside one command loads no instructions; a lasting `cd` in a persistent shell does | Shell semantics |
| **Inherited by scripts and remote commands** | As confined as the platform's backend | `L:ptc-runtime.ptc-runtime-node.2`, `L:workflow.workflow-ptc.1`, `L:ssh.sandbox-ssh.1` | Mode, backend and enforcement on every result | The rows above |
| **Remote execution** | The remote machine must be trusted | `L:ssh.sandbox-ssh.2` | A trust statement at connect | A machine that runs your commands can do anything with them |
| **Operating-system processes** | A process cannot see later changes to its parent shell's environment | `L:credentials.credentials-local.3`, `L:credentials.credentials.3` | Environment-sourced credentials change after a restart; stored values and `.env` files apply at once | Process semantics |
| **PowerShell** | The shell is the only parser of its language | `L:shell.pwsh-local.3` | A syntax error is a failed command | PowerShell |
| **Client and server** | A client cannot enforce a rule on a modified copy of itself | `L:llm.llm-deepseek-account.3` | The version gate is a compatibility guard | Only the service can enforce |
| **Acting on the outside world** | A local log cannot be atomic with an external effect | `L:session.session-checkpoint-policy.1` | After a crash a started call is "outcome unknown" | The two-party atomicity problem |
| **HTTP** | An acknowledgement and its local record cannot be atomic | `L:session.session-log-deepseek.1` | An upload accepted just before a crash may be sent again, with keys to remove duplicates | At-least-once delivery |
| **Browser clipboard** | A successful write cannot be undone | `L:session-query.session-log-export.6` | What was copied stays copied | W3C Clipboard API and events: no undo operation |
| **Text encodings** | Bytes valid in two encodings cannot be told apart | `L:web.web-fetch-http.4` | UTF-8 preferred after the page's own declaration | A detector can only guess |
| **Model providers** | The system prompt is the head of every request | `L:context.harness-dev-context.1` | A release that changes prompt text starts a new request series on some routes | Provider interfaces and prefix caches |
| **LibreOffice** | A different layout engine from Microsoft Office | `L:skill.skill-office.2`, `L:document.tool-office.9` | Layout can differ; results name the engine version | Owner decision [DH-ADR-20](../design.md#dh-adr-20) fixes the engine |
| **tmux** | Not native on Windows; a pane is found by its controlling terminal | `L:context.tmux-context.5` | The contributor does nothing on Windows | tmux and POSIX terminals |
| **Linear** | A paid workspace can require an administrator's approval | `L:connector.linear.7` | The refusal is shown in the account view | Linear's documentation |
| **Language servers** | The protocol has no "indexing finished" signal | `L:lsp.tool-lsp.2` | A result says when the server reported it was indexing | Language Server Protocol |
| **Go MCP SDK** | No support for task-based tools | `L:mcp.mcp-client.8` | Such a tool is not offered and is counted in the server status | The SDK's roadmap lists tasks as future work ([../libs/modelcontextprotocol.md](../libs/modelcontextprotocol.md)) |
| **Vendor model-backed search** | One search is a model request; no result-count parameter; snippets are whatever is returned | `L:web.web-search-deepseek.1`, `L:web.web-search-deepseek.5`, `L:web.web-search-deepseek.6` | Slower and dearer than the direct Search API; an answer with no source is flagged `unsourced` | Upstream `packages/web/web-search-deepseek/README.md` |
| **Vendor account service** | No token expiry or refresh; loopback callback only | `L:account.deepseek-account.1` | Sign-in from the local machine or a forwarded port | The vendor's protocol |

## 7. Scope boundaries

"(rd)" marks a record that is a recommended default, not confirmed by the owner.

| Decision record | The boundary it states | Units |
|---|---|---|
| [DH-ADR-TOOLS-3](../areas/tools.md#dh-adr-tools-3) | Logged arguments are never rewritten | `L:core.tools.2` |
| [DH-ADR-TOOLS-9](../areas/tools.md#dh-adr-tools-9) | Repeat chains are per session, not shared with subagents | `L:guard.repeat-tool-reminder.4` |
| [DH-ADR-TOOLS-10](../areas/tools.md#dh-adr-tools-10) | One list per session; content and three states; whole-list replacement only | `L:todo.tool-todo.1`, `L:todo.tool-todo.2`, `L:todo.tool-todo.3` |
| [DH-ADR-TOOLS-6](../areas/tools.md#dh-adr-tools-6) with [DH-ADR-PTC-3](../areas/ptc-workflow.md#dh-adr-ptc-3) | A call token is minted only for a root call, and no command started by a nested call gets one, so a script cannot start a working workflow | `L:workflow.workflow.3` |
| [DH-ADR-LLM-1](../areas/llm.md#dh-adr-llm-1) (rd for audio) | Audio and video never reach a model | `L:mcp.mcp-client.4`, `L:mcp.mcp-resources.4` |
| [DH-ADR-LLM-3](../areas/llm.md#dh-adr-llm-3) | Images are bytes the harness holds, never a URL; no assistant image output | `L:llm.llm-deepseek.5` |
| [DH-ADR-LLM-9](../areas/llm.md#dh-adr-llm-9) | Token counts stay approximate | `L:llm.token-meter.1`, `L:compaction.compaction-basic.1` |
| [DH-ADR-PERSISTENCE-10](../upstream/session-and-persistence.md#dh-adr-persistence-10), with [DH-ADR-HUMAN-1](../areas/interaction-credentials.md#dh-adr-human-1) and [-3](../areas/interaction-credentials.md#dh-adr-human-3) for approvals | An interrupted turn is closed, never continued; an unanswered call is not retried; a pending approval is lost at a restart | `L:session.session-checkpoint-policy.3`, `L:session.session-persistence.6`, `L:interaction.user-approval.5` |
| `DH-ADR-PERSISTENCE-1` with `-10` | Committed events are never rewritten, so withdrawn feedback stays in history | `L:feedback.message-feedback.2` |
| [DH-ADR-STORE-3](../areas/session-store.md#dh-adr-store-3) | Nothing is checkpointed inside a running model attempt | `L:session.session-checkpoint-policy.2` |
| [DH-ADR-VIEW-6](../areas/session-views.md#dh-adr-view-6) | The default cadence titles a session once; Refresh or another cadence is the remedy | `L:session.session-title-first-prompt-llm.1` |
| [DH-ADR-VIEW-8](../areas/session-views.md#dh-adr-view-8) | A session whose two directories are gone runs no turn until someone chooses one | `L:session.working-directory.2` |
| [DH-ADR-VIEW-9](../areas/session-views.md#dh-adr-view-9) (rd) | Exports hold committed events only; a quote is only a marked line | `L:session-query.session-log-export.3`, `L:session.message-quotes.1` |
| [DH-ADR-FS-11](../areas/filesystem.md#dh-adr-fs-11) | Text tools handle UTF-8 and UTF-16 with a byte-order mark, no other encoding, audio or video; no patch rollback; some moves are two effects; no `replace_all` in the compatibility editor; no copy or recursive operations; uncovered changes listed only when a file tool made them | `L:fs.fs.1`, `L:fs.tool-fs.3`, `L:fs.tool-str-replace-editor.1`, `L:fs.fs.2`, `L:fs.fs.3`, `L:fs.tool-fs.1`, `L:fs.tool-str-replace-editor.2`, `L:deliverables.workspace-changes.7` |
| [DH-ADR-EXEC-8](../areas/execution.md#dh-adr-exec-8) | The secret scrub is a name heuristic | `L:subprocess.subprocess-local.9` |
| [DH-ADR-EXEC-9](../areas/execution.md#dh-adr-exec-9) (rd), with [DH-ADR-EXEC-2](../areas/execution.md#dh-adr-exec-2) and [DH-ADR-WEBAPP-4](../areas/app-web.md#dh-adr-webapp-4) for the end with the host | A terminal has one owner, starts only on request, needs a pseudo-terminal, reports no bell and ends with the host | `L:terminal.terminal.1`, `L:terminal.terminal-bash.5`, `L:terminal.terminal.2`, `L:terminal.terminal.3`, `L:shell.tool-bash-persistent.1`, `L:shell.tool-pwsh-persistent.1`, `L:shell.tool-pwsh-persistent.8` |
| [DH-ADR-EXEC-7](../areas/execution.md#dh-adr-exec-7) | No translation between shell dialects | `L:shell.tool-pwsh.3` |
| [DH-ADR-JOBS-7](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-7) with [DH-ADR-WEBAPP-4](../areas/app-web.md#dh-adr-webapp-4) | With no firing host nothing fires | `L:schedule.schedule.1` |
| [DH-ADR-SANDBOX-3](../areas/sandbox-ssh.md#dh-adr-sandbox-3) with `DH-ADR-SECURITY-10` | The policy promises file effects, network, home protection and delegated execution; nothing about processes, system calls or devices | `L:sandbox.sandbox.1`, `L:sandbox.sandbox-policy.2`, `L:shell.bash-sandbox.1` |
| [DH-ADR-PTC-5](../areas/ptc-workflow.md#dh-adr-ptc-5) | Workflow limits are fixed by the operator; the model may only lower them | `L:workflow.tool-workflow.3` |
| [DH-ADR-PTC-6](../areas/ptc-workflow.md#dh-adr-ptc-6) | A run is never resumed or replayed | `L:workflow.workflow.2` |
| [DH-ADR-SUBAGENT-5](../areas/subagent-team.md#dh-adr-subagent-5) (rd for the remote and follow-up points) | One process per external run; a child never runs on a remote machine; only the `dsh` runtime child takes a follow-up; a foreign product takes no harness preset and no per-call model option | `L:subagent.subagent-acp.1`, `L:subagent.subagent-acp.2`, `L:subagent.subagent-claude-code.1`, `L:subagent.subagent-codex.1`, `L:subagent.subagent-dsh-sdk.1`, `L:subagent.subagent-dsh-sdk.4`, `L:subagent.subagent.3`, `L:subagent.subagent-acp.3`, `L:subagent.subagent-claude-code.8`, `L:subagent.subagent-codex.8` |
| [DH-ADR-SUBAGENT-7](../areas/subagent-team.md#dh-adr-subagent-7) with `DH-ADR-CLAUDEAGENTSDK-1` and `DH-ADR-CODEX-1` | External products run as installed; the user installs and logs in | `L:subagent.subagent-claude-code.4`, `L:subagent.subagent-codex.3` |
| `DH-ADR-CLAUDEAGENTSDK-1` ([../libs/anthropic-ai-claude-agent-sdk.md](../libs/anthropic-ai-claude-agent-sdk.md)) | Driving print mode gives no permission callback, so a Claude Code child cannot ask a person; a lead to check may narrow this | `L:subagent.subagent-claude-code.6` |
| [DH-ADR-SUBAGENT-9](../areas/subagent-team.md#dh-adr-subagent-9) | From an external child only the final answer, its value and usage are collected | `L:subagent.subagent-acp.4`, `L:subagent.subagent-claude-code.7`, `L:subagent.subagent-codex.7` |
| [DH-ADR-SUBAGENT-10](../areas/subagent-team.md#dh-adr-subagent-10) | A fork copies the creator's turns once and offers no route fields by default; a spawn starts empty | `L:subagent.subagent-fork-in-process.3`, `L:subagent.tool-subagent.4`, `L:subagent.subagent-fork-in-process.1`, `L:subagent.subagent-spawn-in-process.1` |
| [DH-ADR-SUBAGENT-4](../areas/subagent-team.md#dh-adr-subagent-4) | A child's name is never reused | `L:subagent.subagent.2` |
| [DH-ADR-SUBAGENT-8](../areas/subagent-team.md#dh-adr-subagent-8) | No message crosses trees; a send has a receipt; only an ancestor or owner interrupts | `L:subagent.subagent.4`, `L:subagent.tool-subagent-control.1`, `L:team.agent-team.5` |
| [DH-ADR-SUBAGENT-11](../areas/subagent-team.md#dh-adr-subagent-11), with [DH-ADR-SUBAGENT-12](../areas/subagent-team.md#dh-adr-subagent-12) as the enforced alternative | Write scopes and team policy text are advisory in a shared checkout | `L:team.agent-team.3`, `L:team.tool-agent-team.1` |
| [DH-ADR-JOBS-1](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-1) | Jobs do not survive the host | `L:jobs.jobs.1`, `L:jobs.jobs-local.1` |
| [DH-ADR-JOBS-2](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-2) | One consuming reader | `L:jobs.jobs.2`, `L:jobs.tool-jobs.5` |
| [DH-ADR-JOBS-6](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-6) with `DH-ADR-RRULE-1` | Bounded rule languages; future times follow the machine's zone data | `L:schedule.schedule.5` |
| [DH-ADR-JOBS-9](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-9) | One goal per session, worked in that session; the goal tool proves a human turn, not that the request merits a goal | `L:goal.goal.4`, `L:goal.goal-round-driver.2`, `L:goal.tool-goal.1` |
| [DH-ADR-JOBS-10](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-10) | The model judges completion and whether two blockers are the same; the runtime counts rounds; no evaluator ships | `L:goal.goal.3`, `L:goal.goal-round-driver.1`, `L:goal.tool-goal.2` |
| [DH-ADR-JOBS-11](../areas/jobs-schedule-goal-plan.md#dh-adr-jobs-11) | A subagent cannot open a plan review | `L:plan.plan-mode.4` |
| [DH-ADR-CONTEXT-1](../areas/context-compaction-skill.md#dh-adr-context-1) | Context costs history tokens until compaction | `L:context.time-context.6` |
| [DH-ADR-CONTEXT-2](../areas/context-compaction-skill.md#dh-adr-context-2) | No imports, no rule directories, no model-written summary of instructions | `L:context.agent-instructions.3`, `L:context.agent-instructions.8` |
| [DH-ADR-CONTEXT-3](../areas/context-compaction-skill.md#dh-adr-context-3) with [DH-ADR-20](../design.md#dh-adr-20) | Authoring libraries are the user's to install | `L:skill.skill-office.1` |
| [DH-ADR-CONTEXT-4](../areas/context-compaction-skill.md#dh-adr-context-4) | The system prompt is never compacted; no model-facing compaction tool | `L:compaction.compaction-basic.5`, `L:compaction.compaction.1`, `L:compaction.compaction.3` |
| [DH-ADR-CONTEXT-6](../areas/context-compaction-skill.md#dh-adr-context-6) | Offloaded images never return by themselves; pruning is syntactic, counted in characters, and cuts by stated rules | `L:compaction.compaction-image-offload.1`, `L:compaction.compaction-image-offload.3`, `L:compaction.compaction-tool-result-pruner.1`, `L:compaction.compaction-tool-result-pruner.2`, `L:compaction.compaction-tool-result-pruner.3` |
| [DH-ADR-CONTEXT-7](../areas/context-compaction-skill.md#dh-adr-context-7) | Discovery reads one level; no encrypted or macro-enabled documents | `L:skill.skill-filesystem.1`, `L:skill.skill-office.3` |
| [DH-ADR-CONTEXT-8](../areas/context-compaction-skill.md#dh-adr-context-8) | A skill body over the cap is refused with its path, never truncated or streamed | `L:skill.tool-skill.4` |
| [DH-ADR-CONTEXT-9](../areas/context-compaction-skill.md#dh-adr-context-9) | A file mention is a pointer; a session mention is a text snapshot; the index is bounded | `L:context.file-reference.2`, `L:context.file-reference-local.2`, `L:context.session-reference.4`, `L:context.session-reference.5` |
| [DH-ADR-CONTEXT-11](../areas/context-compaction-skill.md#dh-adr-context-11) (rd) | The clock and terminal location say little, on purpose | `L:context.time-context.2`, `L:context.time-context.3`, `L:context.time-context.5`, `L:context.tmux-context.1`, `L:context.tmux-context.2`, `L:context.tmux-context.3` |
| [DH-ADR-CONNECT-2](../areas/mcp-lsp-web-connector.md#dh-adr-connect-2) | A probe is capped | `L:mcp.mcp-manager.5` |
| [DH-ADR-CONNECT-4](../areas/mcp-lsp-web-connector.md#dh-adr-connect-4) | A malformed result is refused whole; subscriptions are not followed | `L:mcp.mcp-client.5`, `L:mcp.mcp-client.1`, `L:mcp.mcp-resources.3` |
| [DH-ADR-CONNECT-8](../areas/mcp-lsp-web-connector.md#dh-adr-connect-8) | Minimal tool arguments; no hosted page extraction; an omission marker for unsafe HTML; a fetched PDF is stored for the document tool and not decoded as text | `L:web.tool-web.3`, `L:web.tool-web.4`, `L:web.web.5`, `L:web.web-fetch-http.2`, `L:web.web.3` |
| [DH-ADR-CONNECT-10](../areas/mcp-lsp-web-connector.md#dh-adr-connect-10) | The harness changes no command environment | `L:connector.cli-account.6` |
| [DH-ADR-MEDIA-1](../areas/document-browser-computer.md#dh-adr-media-1) | No tool argument selects a provider | `L:browser-use.browser-use.2`, `L:computer-use.computer-use.2` |
| [DH-ADR-9](../design.md#dh-adr-9) with [DH-ADR-HUMAN-11](../areas/interaction-credentials.md#dh-adr-human-11) | No unasked vendor traffic, so no background key check | `L:account.deepseek-auth.3` |
| [DH-ADR-HUMAN-1](../areas/interaction-credentials.md#dh-adr-human-1) | Four closed interaction kinds; drafts stay in the tab | `L:interaction.user-questions.2`, `L:interaction.user-questions.3` |
| [DH-ADR-HUMAN-6](../areas/interaction-credentials.md#dh-adr-human-6) | Two knobs per preset; `auto` never a default; the default must name a preset | `L:interaction.permission-presets.1`, `L:interaction.permission-presets.4`, `L:interaction.permission-presets.5` |
| [DH-ADR-HUMAN-9](../areas/interaction-credentials.md#dh-adr-human-9) | A credential reference is an environment-shaped name in one namespace; structure belongs in a record | `L:credentials.credentials.2` |
| [DH-ADR-HUMAN-12](../areas/interaction-credentials.md#dh-adr-human-12) | The identifier belongs to one home: a reset one cannot be joined to the old one, and two homes are not linked | `L:identity.anonymous-user-id.1`, `L:identity.anonymous-user-id.3` |
| [DH-ADR-HUMAN-13](../areas/interaction-credentials.md#dh-adr-human-13) | Feedback has a category and no severity | `L:feedback.command-feedback.2` |
| [DH-ADR-HOST-9](../areas/host-telemetry.md#dh-adr-host-9) | No icon extraction | `L:host.open-in-app.3` |
| [DH-ADR-HOST-7](../areas/host-telemetry.md#dh-adr-host-7) | Telemetry waits in memory only | `L:host.product-telemetry-otel.2` |

Outside the 190: [DH-ADR-SANDBOX-8](../areas/sandbox-ssh.md#dh-adr-sandbox-8) (rd) and
[DH-ADR-SANDBOX-10](../areas/sandbox-ssh.md#dh-adr-sandbox-10) state that the remote host
is Linux or macOS and that nothing is replayed (`L:ssh.ssh.1`,
`L:ssh.ssh-helper-runtime.1`); [DH-ADR-SANDBOX-11](../areas/sandbox-ssh.md#dh-adr-sandbox-11)
(rd) and `DH-ADR-SECURITY-12` state that the harness is unaudited
(`UP-DEBT-found.Q3.1`).

**Boundaries that have a record but deserve a second look.** Jobs and terminals end with
the host, so every restart of `dsh`, an update included, ends the user's builds and dev
servers (`DH-ADR-JOBS-1`, `DH-ADR-EXEC-9`); Codex and Claude Code children keep no
session although both products can; worktree isolation is off by default, so a default
team still collides (`DH-ADR-40`).

## 8. Other rows this review replaced

**Not-applicable rows re-issued with the decision that drops the feature** (25). Each
reason now says "dropped by" and links the decision:

- The JavaScript guest runtime, dropped by [DH-ADR-8](../design.md#dh-adr-8) (with
  `DH-ADR-PTC-1` or `DH-ADR-PTC-3` where the seam is meant): `L:core.tool-executor-ptc.3`,
  `L:core.tool-executor-ptc.6`, `UP-DEBT-marker.packages/core/tool-executor-ptc/src/ptc.ts:465`,
  `UP-DEBT-marker.packages/core/tool-executor-ptc/src/ts-types.ts:315`,
  `L:ptc-runtime.ptc-runtime-node.1`, `.5`, `.6`, `.7`, `.8`, `.9`, `.10`, `.12`, `.13`,
  `.16`, `L:ptc-runtime.ptc-runtime.2`, `.3`, `L:workflow.workflow-ptc.5`, `.6`,
  `UP-DEBT-marker.packages/ptc-runtime/ptc-runtime-node/src/evaluators/scoped-repl-evaluator.ts:109`.
- Reading upstream's earlier formats, dropped by [DH-ADR-11](../design.md#dh-adr-11) and
  [DH-ADR-2](../design.md#dh-adr-2): `L:team.agent-team.8`, `L:feedback.message-feedback.1`.
- The workspace-dependencies tool, dropped by `DH-ADR-CONTEXT-3`:
  `L:skill.tool-workspace-dependencies.1`, `.2`.
- The browser-worker preview runtime, dropped by [DH-ADR-17](../design.md#dh-adr-17):
  `L:connector.upstream-skills.5`.
- The desktop application, dropped by [DH-ADR-18](../design.md#dh-adr-18):
  `L:host.authentication-desktop.1`.

**Not-applicable rows kept as written** (13; they exist only because of npm, Node, koffi,
React or Electron): `UP-DEBT-marker.packages/guard/timeout-policy/src/index.ts:6`,
`L:llm.plugin-package-inventory-deepseek.1`, `.3` and its marker at `src/index.ts:101`,
`L:session.session-persistence-jsonl.8`, `L:session.session-projection.5`,
`L:fs.tool-fs.6`, `L:subprocess.win32-process.1`, `.3`, `.6`,
`L:ssh.ssh-helper-runtime.3`, `L:connector.cli-account.5`, `L:document.office-kit.5`.

**Resolved rows re-worded** (stale names or rules): `L:interaction.user-approval.4` (the
headless command asks only on a terminal, [DH-ADR-CLI-7](../areas/app-cli.md#dh-adr-cli-7));
`L:llm.llm.6` (the leaf package `llm/vocab`, `DH-D-LLM-11`);
`L:interaction.permission-presets.3` (`permission.CatalogChanged`).

**Audit units resolved:** `UP-DEBT-found.Q3.2` (the policy-text counter-test
`DH-E-SANDBOX-7`, a merge gate on any change to the text) and `UP-DEBT-found.Q3.8` (the
zero-source result of `DH-D-CONNECT-11`, `DH-T-CONNECT-24`, `DH-E-CONNECT-8`).

## 9. For the specification owners

The re-sweep's list (U-K1-13, U-K1-14, the per-backend scenarios of U-K1-15, the overlap
of `DH-ADR-SANDBOX-11` and `DH-ADR-SECURITY-12`) has been answered and checked. What is
still open:

- **U-K1-15:** run `DH-E-SANDBOX-1`, `-3` and `-4` and record, per backend, whether a
  denied operation is reported anywhere an unprivileged caller can read.
- **U-K1-12:** a step in `DH-E-LLM-5` that records the effort field, with a deliverable
  for the read; a named step to obtain the vendor's business codes; the run of
  `DH-E-EXEC-7` for PowerShell 5.1.
- **Small:** `DH-T-FS-27` could add `str_replace_editor` to the tools it round-trips.

## 10. Limits of this review

- It is a document review. "Resolved" says that a specification describes a mechanism;
  it does not say the mechanism works. Nothing was built or run.
- The strict test for a scope boundary was applied to the record text as found during the
  re-sweep; other agents were editing specifications at the same time.
- Facts about other companies' products are taken from the library specifications and
  upstream READMEs, not checked against the vendors. Sentences marked "Inference" are the
  reviewer's reasoning.
