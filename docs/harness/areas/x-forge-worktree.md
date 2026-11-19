# Experimental: code-host (forge) integration and git worktrees: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md) and are cited as `path:line` under the upstream root; `x/` in a citation
> stands for `packages/experimental/`. Statements that are reasoning and not read from source open with
> "Inference"; sections 6 to 9 are design and are inference throughout. IDs use the area key `XFORGE`.
> Nothing here was built, run or tested; what Git, `gh`, `glab` and a code host really do is for section 9.2.

| | |
|---|---|
| **Area key** | `XFORGE` (agent X1) |
| **Upstream packages** | 8, all under `packages/experimental`: `forge`, `forge-github`, `forge-gitlab`, `forge-session`, `forge-bundle`, `client-ui-forge`, `worktree`, `tool-worktree` |
| **Serves outcomes** | [DH-O-10](../design.md#dh-o-10) (a seam whose providers are operator-installed programs), [DH-O-7](../design.md#dh-o-7) (a worktree never widens what a session may write), [DH-O-9](../design.md#dh-o-9) (parallel sessions with owned checkouts), [DH-O-12](../design.md#dh-o-12) (panel), [DH-O-5](../design.md#dh-o-5) (bindings survive resume and fork), [DH-O-14](../design.md#dh-o-14) (nothing contacted by default) |
| **Depends on areas** | A03 ([tools.md](tools.md)), A06 ([session-views.md](session-views.md): `workdir`, folds, query), A07 ([storage.md](storage.md): `kv`, archive events, spill), A08 ([filesystem.md](filesystem.md)), A09 ([execution.md](execution.md): the one process starter, shell results), A10 ([sandbox-ssh.md](sandbox-ssh.md): policy, launch, `GrantRoot`), A02 ([agent-loop.md](agent-loop.md): maintenance, `PreStep`), A05 (session deletion, duty lease), A15 ([mcp-lsp-web-connector.md](mcp-lsp-web-connector.md): `cliaccount`), A17 (directory grants), A19 and C1 ([api.md](api.md), [ui-core.md](ui-core.md): routes, stream, regions), A23 (`egress.ChildEnv`), A27 ([bundle-preset.md](bundle-preset.md): optional bundles) |
| **Priority** | P2 throughout. Upstream ships all eight packages switched off and "under evaluation" (`x/README.md:26`). Nothing here is needed for a first working harness or for parity on the main paths |

## 1. Purpose

Two capabilities for people who use the harness on a Git repository. **Worktrees** give a session its own
checkout and branch, so that several sessions or subagents can edit one repository at the same time
without overwriting each other's files. **Forge integration** shows, beside a conversation, the pull
requests (GitHub) and merge requests (GitLab) the session created or pushed to, with their checks, review
state and mergeability. Without this area an agent can still run `git worktree`, `gh` and `glab` through
the shell tool. What is missing is a session that *moves into* an isolated checkout with one call, cleanup
of that checkout when the session goes away, and any view of change-request status.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Forge (code host), change request** | A forge hosts Git repositories and reviews: GitHub or GitLab, including self-hosted installations. A change request is a pull request or a merge request. | One provider-neutral word for each, as upstream uses on every surface (`x/forge/README.md:12`). |
| **Provider, family** | A provider is the component that talks to one family of forge (`github`, `gitlab`) on the hosts it lists. | Consumers stay independent of one code host. |
| **Snapshot** | One polled status of a change request: state, checks, review decision, mergeability, changed lines, unresolved thread count. | What a card shows. |
| **Binding** | The fact "this session works on this change request", with an origin: `created`, `referenced`, `discovered`, `tracked`. | A session's list of change requests without the user maintaining it. |
| **Command rule** | Declarative data describing a forge's command-line tool (CLI): which subcommands create or name a change request and how its page address looks. | Lets the harness recognise `gh pr create` in a shell result without running anything. |
| **Worktree, linked checkout** | A second working directory of one Git repository, on its own branch, sharing the repository's object store. Git's own feature (`git worktree`). | Isolation without a second clone. |
| **Pool** | The directory that holds a checkout's worktrees, by default `<checkout root>/.agents/worktrees`. | A model-chosen name can never leave it. |
| **Common Git directory** | Where a repository keeps commits and branches. A linked worktree's files live outside it. | `git commit` in a worktree must write there, which matters for the sandbox. |
| **Ownership record, journal, receipt** | The *ownership record* is the logged event that says which session created a worktree. The *journal* is a small table with one row per worktree operation in flight. The *receipt* is the tool result or cleanup event that states the final outcome. | Ownership decides who may remove; the journal lets a crash be recognised; the receipt tells the caller what happened. |

## 3. What upstream does

### 3.1 Packages

All eight are complete and tested upstream, listed as experimental, and mounted only by a bundle that ships disabled (`x/forge-bundle/README.md:12`). Eight of the nine decision notes handed to this area are dated between 2026-10-09 and 2026-11-11, so the surface was still changing weekly at the pinned commit.

| Upstream unit | Package | Role | What it does (non-test source lines; test files) |
|---|---|---|---|
| `UP-PKG-packages.experimental.forge` | `dsh-experimental-forge` | definition | `ctx.forge`: provider registry, one shared polling loop per change request, 27 provider operations of which 8 are writes (`x/forge/src/types.ts:636-757`). 1,658 lines; 5 |
| `UP-PKG-packages.experimental.forge-github` | `dsh-experimental-forge-github` | provider | Reads and writes pull requests by running the host's `gh` (`x/forge-github/src/gh.ts:52-62`). 3,053 lines; 14 |
| `UP-PKG-packages.experimental.forge-gitlab` | `dsh-forge-gitlab` | provider | The same for merge requests through `glab` (`x/forge-gitlab/src/glab.ts:64-82`). 2,876 lines; 14 |
| `UP-PKG-packages.experimental.forge-session` | `dsh-experimental-forge-session` | consumer | `ctx.forgeSession`: a projection that derives bindings from shell results (`x/forge-session/README.md:41`). 1,314 lines; 6 |
| `UP-PKG-packages.experimental.client-ui-forge` | `dsh-experimental-client-ui-forge` | consumer (host and browser) | `ctx.forgePanel` plus a React client: 6,362 host lines and 23,457 client lines in 97 files, 1.37 MB of source, the largest client package. 59 |
| `UP-PKG-packages.experimental.forge-bundle` | `dsh-experimental-forge-bundle` | other (composition) | Seven rows of configuration and an icon (`x/forge-bundle/cordis.patch.yml:2-15`) |
| `UP-PKG-packages.experimental.worktree` | `dsh-experimental-worktree` | definition and provider | `ctx.worktrees`: create, enter, exit, restore, and cleanup on archive (`x/worktree/src/index.ts:178-290`). 1,433 lines; 7 |
| `UP-PKG-packages.experimental.tool-worktree` | `dsh-experimental-tool-worktree` | consumer | The tools `enter_worktree` and `exit_worktree` (`x/tool-worktree/src/index.ts:45-85`). 86 lines; 1 |

**What `client-ui-forge` offers the user** (`x/client-ui-forge/README.md:32-47`). (a) In the session panel: a checkout row, a local-changes row and one card per bound change request, with actions to merge, mark ready, rerun failed checks, update the branch and request reviewers. (b) A "New worktree" option on the new-session screen, a creation card and a restore card. (c) Glyphs and a hover card on session-list rows. (d) A changes view: a diff reader for local changes in four scopes and for a change request's diff, with a file tree, "review layers", viewed marks, a commit picker, review threads with replies, a pending review and review submission, and an extension interface for other plugins. (e) A Code review page that lists the change requests awaiting the user across hosts and reviews one without a session. Inference: (d) and (e) are a code-review client that repeats the code host's own pages; they account for most of the package and for 11 of its 21 listed limits.

### 3.2 Behaviour

- **Providers and credentials.** A provider runs the vendor CLI as a process of the host, never in the session's execution world, with the host environment minus names containing `KEY`, `PASSWORD`, `SECRET` or `TOKEN`, so the CLI authenticates from its own credential store and the harness never holds a token (`x/forge-github/src/gh.ts:52-62`, `x/forge-github/README.md:37`). The process is started with the `execa` library, not through the subprocess seam (`gh.ts:4`). A host not listed in `hosts` is never contacted (`x/forge-github/src/index.ts:43`, `x/forge-github/README.md:119`). A readiness probe reports `ready`, `unauthenticated` with the sign-in command, or `unavailable` (`x/forge-github/README.md:41`).
- **Polling.** Watchers of one change request share one loop; each family has one batched fetch in flight; an open request is polled every 30 s while checks run or mergeability is unknown and every 120 s otherwise; a merged one stops; failures back off to 10 minutes (`x/forge/src/index.ts:71-76`, `x/forge/src/schedule.ts:24-33`). Polling stops with the last watcher and results live in memory only (`x/forge/README.md:53`, `:110`).
- **Bindings.** A projection reads logged `bash` and `pwsh` calls, including calls made inside a program: `gh pr create` binds the addresses it prints, `gh pr view` and similar bind the request they name, and `git push` records pushed branches, for which the service asks the provider for open requests (`x/forge-session/README.md:41`, `x/forge-session/src/index.ts:374-379`). Caller actions are `plugin:forge-session/binding` records (`x/forge-session/src/projection.ts:23`). Status is never logged. **The model has no forge tool** (`x/forge-session/README.md:98`): it uses the CLIs through the shell tool.
- **Worktrees.** `create` resolves a local commit, creates a branch and checkout under the pool with `git worktree add --no-checkout -b` and `reset --hard`, moves the session there through the working-directory service, and appends `plugin:worktree/created` (`x/worktree/src/index.ts:331-367`). Every Git command runs through the subprocess and sandbox providers under the session's own policy; hooks, the filesystem monitor and content filters are disabled (`:339`, `:463`, `:552-560`). Creation never widens write access (`x/worktree/README.md:57`). `enter` reaches only registered worktrees inside the pool; `exit` returns to the recorded source checkout (`x/worktree/README.md:37-39`). A fork inherits its parent's records as not owned (`x/worktree/src/projection.ts:94`).
- **Who gets a worktree.** A session gets one when the user ticks the option before the first message, or when the model calls `enter_worktree`. Upstream considered and rejected a worktree created automatically for each subagent (`.agents/notes/implemented/feature/2026-09-13-explicit-worktree-creation.md:29`).
- **The panel.** The host half inspects the checkout with Git under the session's permissions after each tool result and directory change, and streams one state per session (`x/client-ui-forge/README.md:65-67`). The worktree option is recorded as a request; when the first message arrives, an inbox listener holds the agent in maintenance until the worktree exists (`x/client-ui-forge/src/index.ts:218`, `x/client-ui-forge/src/worktree.ts:481`).

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | Left behind |
|---|---|---|
| Creation fails or is cancelled after the directory was allocated | The error names the path; nothing is removed (`x/worktree/src/index.ts:368`) | A branch, a partial checkout, or both |
| The session leaves a worktree, or is archived | Leaving removes nothing. Archiving starts one background attempt for the **first** checkout the session created (`x/worktree/src/archive.ts:126-129`): removed with a non-force `git worktree remove` only if clean, unlocked and referenced by no unarchived session (`x/worktree/src/removal.ts:105`, `:137`). The branch is never deleted | The attempt's state is process-local (`docs/subsystems/worktrees.md:115`): a crash loses it, and sessions archived earlier are never swept (`x/worktree/README.md:43`) |
| The session is deleted | Upstream has no session deletion (`packages/session/session-persistence/README.md:155`) | n/a |
| The worktree directory is removed by hand | A message waits until the user restores it or continues in the project (`x/client-ui-forge/README.md:35`) | Uncommitted work is lost (`:162`) |
| A forge read fails; a forge write has no confirmed answer | A read reports `unavailable`, `unauthenticated` or `rate-limited` and the card keeps its last snapshot. A write reports `unknown`, is never retried, and the user must check the host (`x/forge/README.md:37`) | Possibly an applied change |

## 4. Upstream decisions dissected

The first nine rows are the handed-over notes; their unit IDs start `UP-DEC-implemented.` and end with the name shown.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `bug-fix.2026-10-28-forge-git-session-permissions` | Inspection uses the session's resolved sandbox mode, never retries unconfined, and disables repository-configured filters | keep | The session is the one authority for what Git may touch; a background refresh must not run repository code | [XFORGE-6](#dh-adr-xforge-6), [D-6](#dh-d-xforge-6) |
| `bug-fix.2026-11-07-forge-changes-view-responsiveness` | Draw a 366-file diff lazily; scope CSS `:has()` rules | drop | Its subject, the changes view, is not built | [XFORGE-5](#dh-adr-xforge-5) |
| `feature.2026-09-13-explicit-worktree-creation` | Explicit creation from a pinned local commit; no fetch, no copy of dirty files; no wider grants; archive cleanup of clean checkouts; no automatic subagent isolation | keep, change three points | Changed: a failed attempt is rolled back, operations are journaled, and the grant to the Git directory is retained | [XFORGE-6](#dh-adr-xforge-6), [-7](#dh-adr-xforge-7) |
| `feature.2026-10-09-forge-panel` | One streamed state per session from a host half; React surfaces; a hold on the first message; merge and ready actions | replace | Keep the host half and Git through the session's world. Three server-rendered surfaces replace React; the hold is removed; no host writes | [XFORGE-5](#dh-adr-xforge-5), [-8](#dh-adr-xforge-8), [-1](#dh-adr-xforge-1) |
| `feature.2026-10-09-session-change-request-bindings` | Bindings derive from the log, no separate store; credentials stay with the host CLI; no model tool | keep, change the mechanism | Facts stay in the log, but as explicit events written when the command settles, not as tool text parsed again on every replay | [XFORGE-4](#dh-adr-xforge-4), [-2](#dh-adr-xforge-2) |
| `feature.2026-10-20-forge-session-list` | Row glyphs from cached projection views plus one polling stream for every listed session | replace | Keep the glyphs from summary folds; rows show the last known status and start no polling | [XFORGE-3](#dh-adr-xforge-3) |
| `feature.2026-11-05-forge-pending-review-drafts` | Comment drafts live in the code host's pending review | drop | The harness writes nothing to a code host | [XFORGE-1](#dh-adr-xforge-1) |
| `feature.2026-11-11-forge-provider-command-rules` | Each provider registers a declarative rule; the projection version is a hash of the rule set | keep the rules, drop the version | Rules are compiled-in data used only when a command settles; no fold depends on them, so nothing refolds | [XFORGE-4](#dh-adr-xforge-4) |
| `feature.2026-11-11-forge-review-page` | A page of the user's review queue across hosts | drop | A second review client; the code host's page is the review surface | [XFORGE-5](#dh-adr-xforge-5) |
| Code: providers start the CLI with `execa` (`x/forge-github/src/gh.ts:4`) | Bypasses the subprocess seam | change | One process starter ([execution.md](execution.md), `DH-ADR-EXEC-1`) | [XFORGE-2](#dh-adr-xforge-2) |
| Code: a sandboxed Node child allocates the directory (`x/worktree/src/directory.ts:7-23`) | The filesystem seam had no exclusive create and no enforced policy (`x/worktree/README.md:108`) | drop | Git's own branch lock gives exclusivity; the Go file seam is fenced | [D-1](#dh-d-xforge-1) |
| Code: `enter_worktree` moves the session directory (`x/tool-worktree/src/index.ts:56-68`) | The one model tool that moves it, bounded by the pool (`.agents/notes/implemented/architecture/2026-11-03-session-workspace-root.md:17`) | keep, tighten | [session-views.md](session-views.md) `DH-ADR-VIEW-8` owns the rule: a tool call may move the directory only inside the anchor (the directory a person last chose, or the original project). The tool moves through `workdir.Service.SetForCall`, so it can narrow the write boundary and never widen it | [XFORGE-6](#dh-adr-xforge-6) |

## 5. Tech debt and its resolution

56 units: 31 resolved, 3 not applicable, 22 carried. Per-unit reasons are in the ledger rows. Units are named `<package>.<n>` after `UP-DEBT-limit.packages.experimental.<package>.<n>`.

| Group (count) | Units | Cause | Answer in Go |
|---|---|---|---|
| **A. Limits of surfaces that are not built** (17: 14 resolved, 3 not applicable) | `client-ui-forge` 10, 12 to 21; `forge-github` 4, 7; `forge-gitlab` 2 to 5 | The changes view, review threads and writes, viewed marks, the review page, host diff reads, merge and rebase | Not built by [XFORGE-1](#dh-adr-xforge-1) and [XFORGE-5](#dh-adr-xforge-5). `client-ui-forge` 13, 14 and 15 describe lazy drawing in the React view and are not applicable |
| **B. Held messages, partial artifacts, shared deadlines** (5 resolved) | `client-ui-forge` 5, 6, 7; `worktree` 5; `forge-github` 3 | A hold on the agent; no rollback; a Node helper; one deadline for a whole batch | No hold ([XFORGE-8](#dh-adr-xforge-8)); rollback and a journal ([XFORGE-7](#dh-adr-xforge-7)); no Node and a Git version floor; continuation pages get a deadline per change request |
| **C. The sandbox blocks Git inside a worktree** (2 resolved) | `client-ui-forge` 9; `worktree` 6 | The Git directory lies outside the worktree, and Windows ignored extra roots | The grant is retained ([XFORGE-6](#dh-adr-xforge-6)); Windows honours each root (A10, `DH-ADR-SANDBOX-6`) |
| **D. Derived state that was fragile or invisible** (8 resolved) | `client-ui-forge` 3, 11; `forge-session` 2, 6, 7, 8, 9; `forge` 2 | Cached summaries, rule-hashed fold versions, memory-only results, failures only in the log | Summary folds commit with their events ([DH-ADR-PERSISTENCE-7](../upstream/session-and-persistence.md#dh-adr-persistence-7)); bindings are events ([XFORGE-4](#dh-adr-xforge-4)); last snapshots are stored; lookup failures are shown; Git filters the branch list; a parent lists its children's bindings |
| **E. Composition** (2 resolved) | `forge-bundle` 1; `tool-worktree` 1 | A profile patch per extra host; a tool without a caller | `hosts` is an ordinary config field on the settings form; the pipeline attributes every call to an agent |
| **F. Carried by decision: safety, cost or Git itself** (18) | `worktree` 1 to 4; `tool-worktree` 2; `forge-github` 1, 2, 6; `forge-gitlab` 1, 6, 7; `forge` 1; `forge-session` 1, 3; `client-ui-forge` 1, 2, 4, 8 | Bounded reach of the tool; no implicit fetch or filter programs; vendor CLIs must be installed; rate limits; polling with no webhook; conservative command recognition; uncommitted work cannot be restored | Kept on purpose; each row carries its justification. Mitigations: "Add by link", discovery by branch, `Refresh` for a future webhook caller, a visible "filters skipped" note, a watch on `HEAD` |
| **G. Carried: no forge tool for the model** (4) | `forge-bundle` 2; `forge-session` 4, 5; `forge-github` 5 | A host-side read tool would use the user's credentials outside the session's network policy; background output and fork discovery need more provider work | Deferred until the feature leaves experimental; the model keeps `gh` and `glab` through the shell |

## 6. Go design

### 6.1 Shape

Packages sit under `src/harness/internal/x` ([../design.md §4.2](../design.md#42-go-module-layout)). Every component is compiled in and mounted by one of two optional bundles, each a single disabled group ([boot.md](boot.md) `DH-ADR-BOOT-1`, [bundle-preset.md](bundle-preset.md) `DH-ADR-BUNDLE-1`): the group `x.worktree` holds the first two rows and the group `x.forge` the rest. A group id may not also be a component name ([boot.md](boot.md), `DH-ADR-BOOT-13`), so the two service components are catalogued as `x.worktree.service` and `x.forge.registry`; where this document writes the component `x.worktree` or `x.forge`, those are meant. They are independent: the forge UI shows the worktree control only when `worktree.Key` is present.

| Package | Component | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `x/worktree` | `x.worktree` | `worktree.Key` (`Service`) | `workdir.Key`, `fs.Key`, `subprocess.Key`, the sandbox seam and `policy.Resolver`, fold registry, `kv`; optional: workspace registry, session store | The seam: Git operations under the session's policy, records, cleanup |
| `x/worktree/tools` | `x.worktree.tools` | nothing; registers two tools | `tools.Key`, `worktree.Key` | An agent preset that does not mount it pays no prompt tokens; the name follows the rule for tool-only components ([boot.md](boot.md), `DH-ADR-BOOT-13`) |
| `x/forge` | `x.forge` | `forge.Key` (`Service`) | `kv` | Provider registry and shared polling |
| `x/forge/github`, `x/forge/gitlab` | `x.forge.github`, `x.forge.gitlab` | nothing; each registers a provider | `forge.Key`, `subprocess.HostKey`, `egress` (child environment only), `cliaccount.Key` | One provider per family; each can be disabled alone |
| `x/forge/bind` | `x.forge.bind` | `bind.Key` (`Service`) | `forge.Key`, `tools.Key`, session store, fold registry, spill | Bindings |
| `x/forge/ui` | `x.forge.ui` | nothing; routes, a stream source, region contributions | `bind.Key`, `forge.Key`, `subprocess.Key`, sandbox, `workdir.Key`, the UI region registry (C1), routes and hub (A19), agents; optional `worktree.Key` | The three surfaces and checkout inspection |

```go
package forge // read-only: no method changes anything on a code host
type Provider interface {
    Describe() Description // Family, Label, Noun (pull-request | merge-request), Hosts
    RepositoryFromRemote(url string) (Repository, bool)
    ChangeRequestFromURL(url string) (Ref, bool); URL(r Ref) string
    Fetch(ctx context.Context, refs []Ref) map[Key]Result // Snapshot or Failure per ref; a slow ref fails alone
    FindByHead(ctx context.Context, repo Repository, branch string) ([]Ref, error)
    Readiness(ctx context.Context, host string) Readiness // ready | unauthenticated{SignIn} | unavailable{Reason}
}
type Service interface {
    Register(owner *cordis.Context, p Provider) error          // effect; one provider per family and per host
    Watch(owner *cordis.Context, r Ref, fn func(Update)) error // effect; the first watcher polls at once
    Resolve(url string) (Ref, bool); Latest(r Ref) (Update, bool) // memory, else the stored snapshot and its time
    Refresh(r Ref); FindByHead(ctx, repo, branch) ([]Ref, error); Readiness(ctx, repo) Readiness
}
package worktree
type Service interface {
    // Origin says who moves the session: an owner principal (routes, SDK: workdir.Set, which may move the anchor)
    // or a tool call id (the two tools: workdir.SetForCall, inside the anchor only).
    Create(ctx context.Context, by Origin, a agent.Agent, r CreateRequest, phase func(Phase)) (Created, error) // Name, From, Fetch
    Enter(ctx context.Context, by Origin, a agent.Agent, name string) (Entered, error); Exit(ctx context.Context, by Origin, a agent.Agent) (Left, error)
    Restore(ctx context.Context, a agent.Agent, w Created, phase func(Phase)) (Restored, error)
    // ForChild is what the delegation tool's isolation: worktree uses (subagent-team.md). It creates a branch and checkout for a child
    // that does not exist yet, as an effect of the child's activation fiber, and returns where the child must be created.
    ForChild(activation *cordis.Context, parent session.Ref, r ChildRequest) (Created, error) // ChildRequest: Run, Name; Created: Path, Branch, BaseCommit
    Of(s session.Ref) State; Pending(s session.ID) []Op // the fold; journal rows: running, failed, uncertain
}
```

`bind.Service` has `ChangeRequests(session)`, `Watch`, `Track`, `Dismiss` and `DiscoverBranch`, as upstream (`x/forge-session/src/index.ts:138-195`). Types of other areas (`agent.Agent`, `session.Ref`, `workdir.Service`, `policy.Resolver`, `subprocess.Service`, `tools.Spec`, region contributions, `api.Route`) are used as their owners define them.

**How Git and the CLIs are started.** Both go through the one process starter ([execution.md](execution.md)). Git runs in the session's execution world: `subprocess.Key`, with a `Launch` from the sandbox seam for the policy that `policy.Resolver` resolved for that session, so it runs under the session's limits or does not run. `gh` and `glab` run on the host: `subprocess.HostKey`, `Launch: Trusted("forge provider CLI")`, an environment from `BuildEnv` plus `egress.ChildEnv(Trusted)` for proxy settings, prompts and update checks off, and for `glab` the redirecting variables removed and a scratch working directory (`x/forge-gitlab/src/glab.ts:50-74`). Every start has a deadline and an output ceiling. The starter's outcomes map to a snapshot or to one of four codes: `forge/unavailable`, `forge/unauthenticated`, `forge/rate-limited`, `forge/not-found`.

### 6.2 Contracts

**Events.**

| Go event or type | Kind and form | Payload | Replaces |
|---|---|---|---|
| `forge.ProvidersChanged` | live, notify | family | `UP-EVT-forge/providers-changed` |
| none | n/a | n/a | `UP-EVT-forge/review-changed`: not built, there are no review writes ([XFORGE-1](#dh-adr-xforge-1)) |
| `worktree.Cleanup` | live, notify | session id, path, journal id, status (`scheduled`, `retained`, `removed`, `failed`) | `UP-EVT-worktree/cleanup` |
| `worktree/created`, `worktree/entered` | durable, log-only, ignorable | path, branch, base commit, repository root, and `from`, the directory the session was in | `plugin:worktree/created`, `/entered` |
| `forge/binding` | durable, log-only, ignorable | action (`created`, `referenced`, `discovered`, `tracked`, `dismissed`), canonical address, evidence call id | `plugin:forge-session/binding` and the re-parsing of tool text |

**Config.** Go structs with defaults as Go values; strict decoding; durations and sizes validated positive.

| Struct | Fields (default) | Replaces |
|---|---|---|
| `forge.Config` | `activeInterval` 30s, `idleInterval` 120s, `maxBackoff` 10m (at least `activeInterval`), `fetchTimeout` 30s | `UP-CFG-dsh-experimental-forge` |
| `github.Config` | `command` `gh`, `hosts` [`github.com`], `timeout` 30s, `maxOutputBytes` 8 MiB, `maxReviewThreads` 1000 | `UP-CFG-dsh-experimental-forge-github`; `maxCommits`, `maxViewedFiles`, `maxViewerItems` dropped with their reads |
| `gitlab.Config` | `command` `glab`, `hosts` [`gitlab.com`], `timeout` 30s, `maxOutputBytes` 8 MiB, `statusBatchSize` 2, `maxConcurrentRequests` 4 | `UP-CFG-dsh-forge-gitlab`; `maxReviewThreads`, `maxCommits`, `maxCachedVersionBytes` dropped |
| `bind.Config` | `discoveryTimeout` 30s, `maxScanBytes` 1 MiB | `UP-CFG-dsh-experimental-forge-session` |
| `worktree.Config` | `retainOnArchive` false (live), `directory` `.agents/worktrees`, `namePrefix` `dsh-`, `gitCommand` `git`, `timeout` 5m, `grace` 3s, `maxOutputBytes` 64 KiB | `UP-CFG-dsh-experimental-worktree`; `nodeCommand` dropped |
| `forgeui.Config` | `gitCommand` `git`, `gitTimeout` 10s, `grace` 1s, `maxOutputBytes` 1 MiB, `readinessTimeout` 15s, `maxBranches` 50, `maxUntrackedFiles` 100, `refreshThrottle` 10s (new) | `UP-CFG-dsh-experimental-client-ui-forge`; 15 diff, review, lens and review-list fields dropped; `worktreePrefix` merged into `namePrefix` |

**Services.** `UP-SVC-forge` becomes `forge.Key`; `UP-SVC-forgeSession` becomes `bind.Key`; `UP-SVC-worktrees` becomes `worktree.Key`; `UP-SVC-forgePanel` is replaced by the routes and stream source of `x.forge.ui`, which offers no service.

**Persisted data.** Two folds registered with A06, both carried on list rows: `worktree` (created, entered, and the worktree the session is in now) and `forge.bindings` (visible bindings, no status). Two `kv` namespaces: `worktree.ops`, authoritative, the journal (id, session, kind `create`, `restore` or `cleanup`, state, path, branch, base commit, repository root, instance, error); and `forge.snapshots`, derived, the last snapshot per change request with its time.

**Tools exposed to the model.** `enter_worktree({name?, from?})` and `exit_worktree()`, with upstream's names, arguments and result fields (`x/tool-worktree/src/index.ts:16-38`), exclusive in a batch, and present only where the `x.worktree.tools` entry is mounted. They are the one use of `workdir.Service.SetForCall` ([session-views.md](session-views.md), `DH-ADR-VIEW-8`): the service refuses with `workdir/outside-anchor` any target that is not the anchor or inside it, and a tool call never moves the anchor. This area adds a stricter check of its own, that the target lies inside the session's current write roots, and `exit` returns to `from` ([XFORGE-6](#dh-adr-xforge-6)). They need no approval of their own, because they can only narrow or restore the write boundary. There is **no forge tool**: the model reads and changes a code host only with `gh`, `glab` or `git` in the shell tool, where the sandbox's network setting and the shell tool's approval rules apply ([execution.md](execution.md), [sandbox-ssh.md](sandbox-ssh.md)).

**User interface** (routes and stream owned by A19, regions by C1 and the conversation areas). No island: every surface is a server-rendered fragment. Text comes from the message catalogue in English and Chinese, namespaces `forge` and `worktree`.

| Surface | Region | Routes (all bound to the session; mutations are owner only) | Stream |
|---|---|---|---|
| Session panel section: checkout row, local-changes count with "Ask the agent to create a change request", sign-in row with the command to copy, and one card per binding (state, title, number, checks, review decision, mergeability, changed lines, unresolved threads as "N+", linked issues, "as of" time) with "Open in browser", "Add by link", and "Remove" with undo | Area C6's regions `session.panel.section` (cards) and `session.panel.environment` (checkout and local-changes rows) ([ui-sidebar-workspace.md §6.2](ui-sidebar-workspace.md#62-contracts)) | `GET /sessions/{sid}/forge`; `POST …/forge/refresh`; `POST` and `DELETE …/forge/bindings`; `POST …/forge/ask-create` | topic `forge.panel`, key session: the whole section, re-rendered when inspection, bindings or a poll change it. A tab's interest is the watcher |
| Worktree control and card: base-branch search, optional name, progress, failure with Retry and "Continue in the project", removed-worktree notice with Restore | `composer.controls` ([ui-chat.md](ui-chat.md)) and `conversation.docks` ([ui-conversation.md](ui-conversation.md)) | `GET …/worktree/bases?q=`; `POST …/worktree`, `…/worktree/retry`, `…/worktree/dismiss`, `…/worktree/restore`, `…/worktree/leave`: a person's calls of `Create`, `Exit` and `Restore`, with an owner principal as origin. Create is offered only before the first turn; leave is refused with 409 `worktree/busy` while a turn runs | topic `conversation.docks` |
| Session-list glyph: a worktree mark and the most urgent change request, from the two folds and `forge.snapshots` | Area C6's row region `nav.session.mark` | none | the list's own |

Titles and labels of change requests are data from a code host and are escaped like any other text. Avatars are not shown, because a page loads no image from another origin ([libs/go-ui-stack.md](../libs/go-ui-stack.md)).

### 6.3 Ownership, failure, retry, rollback and cleanup

Ownership is who may remove a thing; access is who may use it. A session that *entered* a worktree has access only.

| Resource | Class | Owner | What cleanup may undo |
|---|---|---|---|
| A checkout and branch a session created | Created | The session whose own log (not its fork-inherited prefix) holds `worktree/created` | The checkout, only when clean, unlocked and unreferenced; the branch never |
| The same, seen from a fork or a session that entered it | Shared | Not this session | Nothing |
| An attempt's branch and directory before `worktree/created` is committed | Created by one journal row | That operation | Everything it created (rollback) |
| A `running` journal row whose host died | Uncertain | Nobody | Nothing automatically; it is reported with the two Git commands that remove it |
| The grant to the common Git directory | Access | The worktree component's fiber, per live session | Released on exit, unload and session end |
| A watch, a provider registration, a route, a contribution | Created | The registering fiber | Removed with the fiber. A code host's own data is shared and only read |

| Situation | What happens | Undone, and what stays uncertain |
|---|---|---|
| Creation fails or is cancelled before the event commits | The journal row drives rollback: `git worktree remove` (non-force) of the path, then deletion of the branch only if it still points at the pinned base commit | If a rollback step fails the row becomes `uncertain` and is shown; nothing is forced |
| The host dies during creation, restore or cleanup | At the next start, under a duty lease (A05), `running` rows of dead instances become `uncertain` | **Uncertain:** a partial checkout or branch. Not removed, because the session's policy is not in force |
| A UI-requested creation fails | The maintenance task ends. A `PreStep` listener rejects step 1 while a failed attempt is undecided, so the turn ends `blocked` with the reason and no model request | The user retries or continues in the project; the composer stays usable |
| A session is archived, or deleted | Each owned checkout gets a `cleanup` row, then a background inspection under the creator's policy bounded by the source checkout. `retainOnArchive` skips archive cleanup; deletion always inspects, because the ownership record is about to disappear | Dirty, locked, detached, still referenced, or under `read-only`: retained and reported. Unarchive cancels an attempt not yet started and waits for one that is |
| The worktree directory was removed by hand | The session does not silently fall back to the project (section 10, item 2); the card offers Restore, which checks out the branch again, or the base commit if the branch is gone | **Lost:** changes never committed |
| `gh` or `glab` is missing, signed out or rate-limited, or a poll is cut off | The card shows the last stored snapshot with its time and the reason; sign-out shows the sign-in command. Polls back off; nothing is retried inside a call | Nothing: reads only |
| The entry is disabled while sessions use it | Tools, routes and watches go with the fibers. The session stays in its worktree; its events stay readable because they are ignorable | Checkouts and branches stay |

## 7. Decisions

<a id="dh-adr-xforge-1"></a>
### DH-ADR-XFORGE-1 The forge seam is read-only: the harness never changes anything on a code host
- **Decision.** `forge.Provider` has six operations and a description, all reads. Merge, mark ready, rerun checks, update branch, request reviewers, review comments, pending reviews and viewed marks are not built. A change to a code host is made by the model's own CLI command in the shell tool, or by the user on the host's page, which every card links to.
- **Why.** Upstream's eight write operations run on the host with the user's credentials, outside the session's sandbox and outside the tool pipeline, and each has an "unknown outcome" that is never retried (`x/forge/README.md:37`, `:61`). One rule, "reads only", needs no approval design and cannot damage a repository.
- **Tradeoff.** One click in upstream becomes a visit to the code host or a request to the agent. A recorded exception to [DH-ADR-1](../design.md#dh-adr-1), decided by the owner: the code-host UI is status only, with no host-side writes ([DH-ADR-38](../design.md#dh-adr-38)).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-XFORGE-7`, [DH-E-XFORGE-3](#dh-e-xforge-3).

<a id="dh-adr-xforge-2"></a>
### DH-ADR-XFORGE-2 Providers drive the operator-installed `gh` and `glab`; the harness holds no forge token
- **Decision.** Keep upstream's mechanism. A provider starts the vendor CLI on the host through `subprocess.HostKey`. No token is read, stored or passed; the CLI uses its own credential store. The harness opens no HTTP connection to a code host, so `egress` builds only the child's proxy environment. Sign-in state is shown through A15's `cliaccount`, and a missing CLI is `unavailable` with the reason.
- **Why.** Token storage, device sign-in, enterprise single sign-on and refresh are security code the vendor already ships. It matches [DH-O-10](../design.md#dh-o-10): a capability whose provider is operator-installed software says so plainly where the software is missing, and nothing is downloaded ([DH-ADR-13](../design.md#dh-adr-13)).
- **Tradeoff.** A process start per read; output formats that change with CLI versions; no status without the CLI. A provider speaking HTTP through `egress` with a credential reference could be added behind the same interface.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-XFORGE-8`, `DH-T-XFORGE-9`, [DH-E-XFORGE-3](#dh-e-xforge-3).

<a id="dh-adr-xforge-3"></a>
### DH-ADR-XFORGE-3 A code host is contacted only after the user enabled the entry, only for a listed host, and only while someone is looking
- **Decision.** Both bundles ship disabled. Enabling `x.forge` is the opt-in, and the settings form says which hosts will be contacted and with whose sign-in. After that a request is made in three cases only: a tab is showing that session's panel (status polls and the readiness probe), the user pressed refresh, or the session pushed a branch (one lookup). Session-list rows show stored status and start nothing. The forge is not a sender under A18's sharing gate.
- **Why.** [DH-ADR-9](../design.md#dh-adr-9) switches off data sent to a vendor or collector by default. Inference: a read from the user's own code host, with the user's own sign-in, after the user turned the feature on, is the product doing what was asked, as a model request is. Polling for nobody would spend the user's rate limit and contact a host in the background.
- **Tradeoff.** No notification when checks finish while no panel is open; a list row can be stale and says how old it is. The one off-switch does not silence this entry: it stops senders only, and a code-host status read the user configured still runs ([DH-ADR-27](../design.md#dh-adr-27)).
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-XFORGE-7`, `DH-T-XFORGE-12`, [DH-E-XFORGE-3](#dh-e-xforge-3).

<a id="dh-adr-xforge-4"></a>
### DH-ADR-XFORGE-4 Bindings are explicit log events written when a shell call settles
- **Decision.** A listener on `tools.Settled` reads each settled shell call (the model's or a script's), classifies the command with the compiled-in command rules through `DH-LIB-SHELLQUOTE`, scans the complete output up to `maxScanBytes` (through the spill store when the result was shortened), and appends one `forge/binding` event per change request found. Pushed branches trigger a lookup whose results are appended the same way. The fold reads only `forge/binding` events. Rules never guess and never execute; a numeric selector without a literal repository does not bind.
- **Why.** Upstream folds tool text, so the fold's meaning depends on which providers are loaded: it hashes the rule set into the state version and refolds every session when a rule changes (`x/forge-session/README.md:57`, `:96`). Events make the fold independent of rules and of result shortening.
- **Tradeoff.** A command run while the entry was disabled never binds, where upstream would bind it later; branch discovery on inspection and "Add by link" cover that. A crash between a result and its binding loses the binding until the next discovery. Upstream's live lookup for unqualified selectors (`x/forge-session/README.md:43`) is not built.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-XFORGE-10`.

<a id="dh-adr-xforge-5"></a>
### DH-ADR-XFORGE-5 `client-ui-forge` becomes three server-rendered surfaces; the code-review client is dropped
- **Decision.** Built: the panel section, the worktree control with its card, and the session-list glyph (section 6.2). Dropped, as recorded exceptions to [DH-ADR-1](../design.md#dh-adr-1): the changes view for local changes and for change requests, review layers, commit selection, review threads, the Code review page, and the lens and annotation extension interface. Per-turn file changes remain visible through A08's `workspace/changes` card.
- **Why.** Sections 3.1 and 5: the dropped parts are most of the package, repeat the code host's pages, depend on the writes [XFORGE-1](#dh-adr-xforge-1) removes, and carry 11 of its listed limits. The lens interface is a client-plugin extension point, for which [DH-ADR-3](../design.md#dh-adr-3) has no place. The kept parts are what an agent harness adds: which change requests this session produced, how they stand, and an isolated checkout.
- **Tradeoff.** No diff of a branch against its fork point inside the harness, and no quoting of a review thread into the composer. Either can return later as its own deliverable on the read-only seam; until then `DH-LIB-DIFFGIT` has no consumer here ([libs/diff.md](../libs/diff.md) §7). Decided by the owner ([DH-ADR-38](../design.md#dh-adr-38)).
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-16](../design.md#dh-o-16). **Verified by.** `DH-T-XFORGE-11`, [DH-E-XFORGE-4](#dh-e-xforge-4).

<a id="dh-adr-xforge-6"></a>
### DH-ADR-XFORGE-6 A worktree move never widens what a session may write; the Git directory grant is retained, not added
- **Decision.** (a) Every Git command runs under the session's resolved policy and is never retried with less confinement. No repository-configured program runs and nothing is fetched implicitly when the harness inspects or creates a checkout: hooks, the filesystem monitor and clean, smudge and process filters are switched off for the call, and only the one named branch is fetched when a fetch was asked for. The stated cost (register section 5.2): a file kept by Git LFS stays a pointer in a new worktree and can count as changed in an inspection, which the panel marks "filters skipped". (b) The two tools move a session only through `workdir.Service.SetForCall`, which keeps every tool-made move inside the anchor ([session-views.md](session-views.md), `DH-ADR-VIEW-8`, where the rule is stated once). On top of that, `enter_worktree` accepts a target only if it lies inside the session's current write roots, and `exit_worktree` returns to the recorded `from` directory, not to the repository's top level, so after any sequence of tool calls the workspace root is inside the root the session had before its first entry. Only a person, through a carrier, can move a session elsewhere. (c) While a session works in a worktree, the component holds `policy.Resolver.GrantRoot` for the common Git directory, but only if that directory was inside the session's write roots when it entered. Otherwise the user is asked for a directory session grant (A17), and without it commits are denied.
- **Why.** [session-views.md](session-views.md) `DH-ADR-VIEW-8` makes the working directory the write boundary, so a tool that moves it must not be able to enlarge it; the anchor rule there is the guarantee, and the write-roots check here closes the case below. Inference from upstream's code: `enter` runs no writing command, so from a project that is a subdirectory of a repository it can reach the repository's pool, and `exit` then lands on the repository root, wider than where the session began (`x/worktree/src/index.ts:373-391`, `:279`). Upstream refuses any grant, so a session in `workspace-write` cannot commit in its own worktree (`x/worktree/README.md:100`), which defeats the feature in the default mode.
- **Tradeoff.** The Git directory is shared by all worktrees of the repository: the grant keeps what the session could already write, it does not isolate branches from each other. A project that is itself a linked worktree needs one approval.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-XFORGE-2`, `DH-T-XFORGE-3`, [DH-E-XFORGE-1](#dh-e-xforge-1).

<a id="dh-adr-xforge-7"></a>
### DH-ADR-XFORGE-7 The log says who owns a worktree, a journal covers each operation in flight, and cleanup removes only clean, unreferenced checkouts
- **Decision.** Section 6.3. A journal row is written before the first Git command of a create, restore or cleanup and closed after its outcome is recorded. Rollback undoes only what its own row created. Cleanup runs for every owned checkout on archive and on deletion, uses a non-force `git worktree remove`, and never deletes a branch. Rows left by a dead process are marked `uncertain` once per home and reported, never acted on.
- **Why.** Upstream leaves partial artifacts after a failure, keeps cleanup state in memory, considers only the first checkout, and never sweeps (section 3.3). Go adds session deletion and several processes on one home ([DH-ADR-12](../design.md#dh-adr-12)), which make a lost attempt routine. A branch holds commits, which are the user's work.
- **Tradeoff.** Branches accumulate until the user deletes them. An interrupted operation needs a person to run two Git commands. Ignored files are discarded with a removed checkout, as Git does (`x/worktree/README.md:43`).
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-1](../design.md#dh-o-1). **Verified by.** `DH-T-XFORGE-4`, `DH-T-XFORGE-5`, [DH-E-XFORGE-2](#dh-e-xforge-2).

<a id="dh-adr-xforge-8"></a>
### DH-ADR-XFORGE-8 Worktrees are created explicitly: before the first turn, by the tool, or for a delegated child that asked for isolation; no message is held and no subagent is isolated automatically
- **Decision.** From the UI, creation is an operation of its own that runs as a maintenance task of the session's agent, offered only before the first turn. A message sent meanwhile is latched by the driver as [agent-loop.md](agent-loop.md) describes; if the attempt failed, the turn is `blocked` by a `PreStep` listener, never run in the wrong directory. A subagent starts in its parent's directory (A06) unless its delegation target is configured with `isolation: worktree` ([DH-ADR-40](../design.md#dh-adr-40), register entry U-K1-3; the option is A12's, off by default). For such a start A12 calls `worktree.Service.ForChild` from the child's activation, before the child exists. `ForChild` pins the parent checkout's current commit, creates the branch `<namePrefix><child name>-<run>` and its checkout in the parent's pool under the parent session's policy, with the same journal row and rollback as any creation, and returns path, branch and base commit. A12 creates the child with that path as its working directory, so the worktree is the child's only write root (plus the retained Git directory grant of [XFORGE-6](#dh-adr-xforge-6)), and writes the branch into `subagent/settled`. No live session is moved, so `DH-ADR-VIEW-8` is not involved. The worktree is an effect of the activation fiber: when the run settles or the activation fails, the effect removes the checkout if it is clean and deletes the branch only if it still points at the base commit; a branch with commits is kept, and a dirty checkout is retained and named in the settled record. Nothing is merged, fetched or pushed. Where the parent's directory is not in a Git repository, or the `x.worktree` group is not enabled, `ForChild` fails with `worktree/not-a-repository` or the service is absent, and A12 fails the start: it never falls back to the shared checkout silently.
- **Why.** Upstream's hold keeps an agent idle in maintenance with no stop control and misses messages that arrive during a turn (`x/client-ui-forge/README.md:160-161`). Upstream rejected automatic subagent isolation because delegation has its own coordination model (`.agents/notes/implemented/feature/2026-09-13-explicit-worktree-creation.md:29`); a hidden checkout per child would also multiply branches nobody asked for.
- **Tradeoff.** One more click than upstream's tick box, and a blank session abandoned after creation leaves a worktree until it is archived or deleted. Parallel subagents share one checkout unless their target asks for isolation; an isolated child's work reaches the parent only when someone merges its branch, and branches accumulate. The core delegation area then depends on this experimental one for that option only.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-3](../design.md#dh-o-3). **Verified by.** `DH-T-XFORGE-6`, `DH-T-XFORGE-13`, [DH-E-XFORGE-4](#dh-e-xforge-4), [DH-E-XFORGE-1](#dh-e-xforge-1).

<a id="dh-adr-xforge-9"></a>
### DH-ADR-XFORGE-9 What a status read does not cover is deferred, with what would reopen each
- **Decision.** Recommended default, not confirmed by the owner. Three limits of the status surfaces are scope, not defects. (a) Working-tree changes made outside the session are seen at the next trigger (a settled tool call, a panel fetch, a change of `HEAD`), not watched; a watch on the whole checkout would reopen it. (b) A change request whose head branch lives in a fork is not discovered by branch; it appears when the session's own command names it or the user adds it by link. A head lookup qualified by the fork's owner would reopen it. (c) GitLab fields that exist only on paid tiers (approval rules, merge trains) are not read; a test installation on such a tier would reopen it.
- **Why.** Each needs either a standing cost for every session (a watch), a second lookup per push against the user's rate limit, or an environment the project cannot test. [DH-ADR-1](../design.md#dh-adr-1) lets a part be left out only by a recorded decision; this is that record (register section 5.2).
- **Tradeoff.** A card can be stale by one trigger, and a fork-based or paid-tier workflow shows less than the code host's own page, which every card links to.
- **Serves.** [DH-O-16](../design.md#dh-o-16), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-XFORGE-12` (no process starts without a trigger), `DH-T-XFORGE-10` (a fork head is bound only by a named address).

## 8. Deliverables

<a id="dh-d-xforge-1"></a>
### DH-D-XFORGE-1 A session can move into its own checkout and branch, and back, by a host operation or by two model tools
- **Result and why it matters.** Two sessions on one repository edit and commit without touching each other's working files, and each session's later tools resolve paths in its own checkout.
- **How it is implemented.** The `x.worktree` component. `Create` resolves the revision, checks that the branch and path are free, lets `git worktree add --no-checkout -b` claim both (Git's branch lock makes two creators of one name exclusive), writes the pool's `.gitignore` through the file seam only if absent, checks files out with filters, hooks and lazy fetch disabled, calls `workdir.Set`, then appends the event. A probe refuses Git older than 2.31, and older than the lazy-fetch fix when the repository has a promisor remote (`x/worktree/README.md:99`). `Enter`, `Exit`, `Restore`, the fold and the rules of [XFORGE-6](#dh-adr-xforge-6). A person's move calls `workdir.Set`; a tool's move calls `workdir.SetForCall` with its call id. `ForChild` creates a worktree for a child that asked for `isolation: worktree`, as an effect of the child's activation ([XFORGE-8](#dh-adr-xforge-8)); [DH-D-SUBAGENT-6](subagent-team.md#dh-d-subagent-6) builds on it and this deliverable does not depend on that one. The `x.worktree.tools` component declares the two tools as `tools.Spec` values; `enter_worktree` tries `Enter` for a name without `from` and creates on `worktree/not-found`, as upstream.
- **Components and files.** `internal/x/worktree`, `internal/x/worktree/tools`. **Depends on.** `DH-D-CORDIS-2`, [DH-D-TOOLS-1](tools.md#dh-d-tools-1) (`tools.Spec`), [DH-D-VIEW-10](session-views.md#dh-d-view-10) (`workdir.Service` with `Set` and `SetForCall`) and [DH-D-VIEW-1](session-views.md#dh-d-view-1) (fold registration), [DH-D-EXEC-1](execution.md#dh-d-exec-1) and [DH-D-EXEC-4](execution.md#dh-d-exec-4) (`subprocess.Key` under a launch plan), [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) and [DH-D-SANDBOX-3](sandbox-ssh.md#dh-d-sandbox-3) (`Resolve`, `Prepare`, `GrantRoot`, the write roots of a policy), [DH-D-FS-4](filesystem.md#dh-d-fs-4) (create-if-absent write, `Stat`), [DH-D-HUMAN-3](interaction-credentials.md#dh-d-human-3) (a directory grant). Runtime: Git on the session's machine.
- **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Priority.** P2. **Done when.** DH-T-XFORGE-1, DH-T-XFORGE-2, DH-T-XFORGE-3 and DH-T-XFORGE-13 pass and [DH-E-XFORGE-1](#dh-e-xforge-1) has passed.

<a id="dh-d-xforge-2"></a>
### DH-D-XFORGE-2 A worktree is cleaned up when its session goes away, and a crash leaves a named remainder, not a mystery
- **Result and why it matters.** Archiving or deleting a session reclaims its clean checkout; nothing with uncommitted work is ever removed; after a crash the user is told exactly what was left.
- **How it is implemented.** The `worktree.ops` journal; rollback; listeners on `workspace.SessionArchived`, `workspace.SessionUnarchiving` and session deletion; the inspection (`git status`, the worktree list, lock and reference checks) inside a per-path lock that also denies `workdir.Access` into a checkout being removed; the start-up sweep under a duty lease; `worktree.Cleanup`. Because it removes directories, this deliverable's scenario runs before it is called built.
- **Components and files.** `internal/x/worktree` (`journal.go`, `cleanup.go`). **Depends on.** [DH-D-XFORGE-1](#dh-d-xforge-1). [DH-D-STORAGE-1](storage.md#dh-d-storage-1) and [DH-D-STORAGE-7](storage.md#dh-d-storage-7) (`kv`, archive events), [DH-D-STORE-6](session-store.md#dh-d-store-6) and [DH-D-STORE-8](session-store.md#dh-d-store-8) (a call before a session is deleted; a duty lease), [DH-D-VIEW-1](session-views.md#dh-d-view-1) and [DH-D-VIEW-10](session-views.md#dh-d-view-10) (cold reads of a session's fold and directory).
- **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-1](../design.md#dh-o-1), [DH-O-5](../design.md#dh-o-5). **Priority.** P2. **Done when.** DH-T-XFORGE-4 and DH-T-XFORGE-5 pass and [DH-E-XFORGE-2](#dh-e-xforge-2) has passed.

<a id="dh-d-xforge-3"></a>
### DH-D-XFORGE-3 Change-request status is read through one provider-neutral registry with shared, bounded polling
- **Result and why it matters.** Any number of viewers of one change request cost one poll, and a restart shows the last known status at once.
- **How it is implemented.** The `x.forge` component: registration with host-collision checks, one timer, one batched fetch per family, upstream's cadence and backoff, a reset time honoured on `rate-limited`, results published per change request, and the `forge.snapshots` namespace written on change.
- **Components and files.** `internal/x/forge`. **Depends on.** `DH-D-CORDIS-2`, `DH-D-CORDIS-4`, [DH-D-STORAGE-1](storage.md#dh-d-storage-1) (`kv`).
- **Outcome.** [DH-O-10](../design.md#dh-o-10), [DH-O-14](../design.md#dh-o-14). **Priority.** P2. **Done when.** DH-T-XFORGE-7 passes and [DH-E-XFORGE-3](#dh-e-xforge-3) has passed.

<a id="dh-d-xforge-4"></a>
### DH-D-XFORGE-4 GitHub and GitLab status providers that run the user's own CLI
- **Result and why it matters.** Status for github.com, gitlab.com and any listed self-hosted installation, with no credential in the harness.
- **How it is implemented.** Address parsing for remotes and change-request pages; one GraphQL status query per host through `gh api graphql`, with an alias per change request so that one error fails one request, and thread-count continuation pages outside the batch; GitLab batches that halve when the host refuses a query as too complex (`x/forge-gitlab/README.md:43`); the readiness probe; the command rules. A conformance suite runs both providers against a scripted CLI.
- **Components and files.** `internal/x/forge/github`, `gitlab`, `forgetest`. **Depends on.** [DH-D-XFORGE-3](#dh-d-xforge-3). [DH-D-EXEC-1](execution.md#dh-d-exec-1) (`subprocess.HostKey`), [DH-D-UTIL-2](util-test-support.md#dh-d-util-2) (`egress.ChildEnv`), [DH-D-CONNECT-9](mcp-lsp-web-connector.md#dh-d-connect-9) (`cliaccount.Register`). Runtime: `gh` or `glab`, signed in.
- **Outcome.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7). **Priority.** P2. **Done when.** DH-T-XFORGE-8 and DH-T-XFORGE-9 pass and [DH-E-XFORGE-3](#dh-e-xforge-3) has passed.

<a id="dh-d-xforge-5"></a>
### DH-D-XFORGE-5 A session lists the change requests it works on, across resume and fork
- **Result and why it matters.** The list builds itself from what the agent did, and a fork starts with its parent's list.
- **How it is implemented.** [XFORGE-4](#dh-adr-xforge-4): the observer, the event type, the fold, discovery after a push (skipping names that are only tags), `Track` and `Dismiss`, watches that follow provider changes, and lookup failures kept in the panel state. A command the agent moved to the background is covered too (register entry U-K2-12): its tool call settles with a job id and no output, so the listener also reads the durable record that area A13 writes when a job settles ([DH-D-JOBS-2](jobs-schedule-goal-plan.md#dh-d-jobs-2)), for jobs started by a shell tool call, and scans the job's output with the same rules and the same `maxScanBytes`. If that record does not name the originating call and command, or the output is no longer readable, nothing is bound and branch discovery and "Add by link" remain the way. A session's list also shows the bindings of its subagent children, labelled.
- **Components and files.** `internal/x/forge/bind`. **Depends on.** [DH-D-XFORGE-3](#dh-d-xforge-3), `DH-LIB-SHELLQUOTE`, [DH-D-JOBS-2](jobs-schedule-goal-plan.md#dh-d-jobs-2) (the durable record of a settled background job). [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (`tools.Settled`, for nested calls too), [DH-D-EXEC-9](execution.md#dh-d-exec-9) (the shell result type), [DH-D-STORAGE-5](storage.md#dh-d-storage-5) (spill read), [DH-D-VIEW-1](session-views.md#dh-d-view-1) and [DH-D-VIEW-5](session-views.md#dh-d-view-5) (folds, a session's children).
- **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-4](../design.md#dh-o-4). **Priority.** P2. **Done when.** DH-T-XFORGE-10 passes and [DH-E-XFORGE-3](#dh-e-xforge-3) has passed.

<a id="dh-d-xforge-6"></a>
### DH-D-XFORGE-6 The user sees checkout and change-request status beside the conversation and can start a session in a worktree
- **Result and why it matters.** The reason to enable the feature: a glance shows whether the work landed, and isolation is one control away.
- **How it is implemented.** The `x.forge.ui` component: checkout inspection (about ten read-only Git commands under the session's policy, one run at a time with one trailing rerun, triggered by `tools.Settled`, a directory or policy change, a panel fetch and a watch on the Git directory's `HEAD`); the three surfaces of section 6.2 as templates; the `PreStep` listener; the two optional bundle files.
- **Components and files.** `internal/x/forge/ui`, templates and catalogue files, `x.worktree.yml`, `x.forge.yml`. **Depends on.** [DH-D-XFORGE-1](#dh-d-xforge-1), [DH-D-XFORGE-2](#dh-d-xforge-2), [DH-D-XFORGE-5](#dh-d-xforge-5). [DH-D-API-1](api.md#dh-d-api-1) and [DH-D-API-4](api.md#dh-d-api-4) (routes, hub), [DH-D-UICORE-2](ui-core.md#dh-d-uicore-2) and [DH-D-UICORE-7](ui-core.md#dh-d-uicore-7) (region registry, catalogue), [DH-D-UICHAT-1](ui-chat.md#dh-d-uichat-1) (`composer.controls`), [DH-D-UICONV-1](ui-conversation.md#dh-d-uiconv-1) (`conversation.docks`), [DH-D-UISIDE-9](ui-sidebar-workspace.md#dh-d-uiside-9) and [DH-D-UISIDE-3](ui-sidebar-workspace.md#dh-d-uiside-3) (the panel and row regions), [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4) (`RunMaintenance`, `PreStep`), [DH-D-BUNDLE-6](bundle-preset.md#dh-d-bundle-6) (optional bundles).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14), [DH-O-3](../design.md#dh-o-3). **Priority.** P2. **Done when.** DH-T-XFORGE-6, DH-T-XFORGE-11 and DH-T-XFORGE-12 pass and [DH-E-XFORGE-4](#dh-e-xforge-4) has passed.

## 9. Acceptance

### 9.1 Local tests

Local tests use a temporary real Git repository where Git itself is the subject, and fakes for the CLIs, the sandbox and the clock. None counts as a scenario.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-XFORGE-1` | D-1 | Create from `HEAD`, a branch, a tag and a fetched remote branch; an existing branch or path is `worktree/already-exists` and changes nothing; dirty files are not copied; a configured filter program and a hook are not run; two concurrent creators of one name give one success |
| `DH-T-XFORGE-2` | D-1, ADR-6 | With a recording policy resolver: every Git start carries the session's policy; a sandbox refusal is returned, never retried unconfined; the Git directory grant is taken only when it was inside the roots, and released on exit and unload |
| `DH-T-XFORGE-3` | D-1, ADR-6 | Tool schemas and golden results; a tool's move goes through `workdir.SetForCall` and a person's through `workdir.Set`; from a project that is a subdirectory, `enter` of a pool outside the roots is `worktree/outside-roots`, and a target outside the anchor is `workdir/outside-anchor`; after any generated sequence of enter and exit the root is inside the starting root and the anchor is unchanged; a nested exit goes one level; without the `x.worktree.tools` entry neither tool exists |
| `DH-T-XFORGE-13` | D-1, D-2, ADR-8 | `ForChild` against a temporary repository: it pins the parent's commit, creates the branch and checkout in the parent's pool under the parent's policy and returns path, branch and base commit; disposing the activation context removes a clean checkout, deletes a branch still at its base, keeps a branch with commits and retains a dirty checkout with a reported reason; a failure after each step rolls back that step's artifacts; outside a Git repository it fails with `worktree/not-a-repository` and creates nothing; it never calls `workdir.Set` or `SetForCall` |
| `DH-T-XFORGE-4` | D-2, ADR-7 | A failure injected after each creation step rolls back exactly that row's artifacts; a branch that moved is kept; a failed rollback leaves `uncertain` |
| `DH-T-XFORGE-5` | D-2, ADR-7 | Archive and delete remove a clean owned checkout and keep its branch; dirty, untracked, locked, detached, referenced by another session, fork-inherited and merely entered checkouts are retained; unarchive cancels or waits; with two processes, rows of a dead instance become `uncertain` once |
| `DH-T-XFORGE-6` | D-6, ADR-8 | A failed UI attempt blocks step 1 with its reason and no model request; after a retry the turn runs in the worktree; the control is refused once a turn has run |
| `DH-T-XFORGE-7` | D-3, ADR-1, ADR-3 | With virtual time: cadence, backoff, one fetch in flight per family, a merged request stops, no watcher means no call, unregistering settles watchers `unavailable`; stored snapshots are served after a restart with their time; the provider interface has no mutating method |
| `DH-T-XFORGE-8` | D-4, ADR-2 | Against a scripted CLI: recorded GraphQL answers map to snapshots; a partial error fails one alias; `rate-limited`, sign-out and a missing program map to their codes; the child environment holds no credential name |
| `DH-T-XFORGE-9` | D-4, ADR-2 | Remote and page addresses in every form of `x/forge-github/README.md:41` and `x/forge-gitlab/README.md:39`; an unlisted host starts no process |
| `DH-T-XFORGE-10` | D-5, ADR-4 | A table of command lines and outputs gives the expected events and excluded shell forms give none; a shortened result is scanned through the spill store; a settled background job that a shell call started is scanned once from its settle record, and one whose record lacks the call or whose output is gone binds nothing; replay and fork reproduce the list from events alone; dismiss hides and track restores; a tag-only name is not looked up |
| `DH-T-XFORGE-11` | D-6, ADR-5 | Golden fragments for every card state in English and Chinese; a hostile title is escaped; mutations refuse a non-owner |
| `DH-T-XFORGE-12` | D-6, ADR-3 | Composition test with a recording starter: with the bundles disabled no `gh`, `glab` or Git process starts; with them enabled and no tab open, none starts either |

### 9.2 Real-environment scenarios

<a id="dh-e-xforge-1"></a>
#### DH-E-XFORGE-1 Two sessions work in parallel in their own worktrees and both can commit
- **Proves.** DH-D-XFORGE-1.
- **Given.** Linux, macOS and Windows machines with real Git and the real sandbox backend in `workspace-write`; a repository with one commit; the `x.worktree` bundle enabled; two sessions with a real model API key.
- **When.** Each session calls `enter_worktree`, edits the same file path, runs `git add` and `git commit`, and tries to write a file in the source checkout. One is then asked to enter a directory outside the project. One then calls `exit_worktree`. Last, with a delegation target configured `isolation: worktree`, one session delegates an edit and a commit to a subagent, and a second delegation is cancelled before it commits.
- **Then.** Two branches each hold one commit; the source checkout is unchanged; the write to the source checkout is denied by the sandbox; the commit succeeds without an approval; the move outside the project is refused and the session stays where it was; after exit the session's directory is the one it started in. The first child's commit is on its own branch, which the settled record names; the parent's checkout is unchanged and the child's clean checkout is gone; the cancelled child's uncommitted changes are retained in its checkout and reported; nothing was merged.
- **Cleanup.** Delete both sessions, then the repository. Keep the journal and the host log if a checkout remains.

<a id="dh-e-xforge-2"></a>
#### DH-E-XFORGE-2 Cleanup on archive and delete, and a host killed mid-operation
- **Proves.** DH-D-XFORGE-2.
- **Given.** A Linux, a macOS and a Windows machine, each with real Git, the real sandbox backend in `workspace-write`, a repository with one commit, the `x.worktree` group enabled and a real model API key; three sessions with worktrees: one clean, one with an uncommitted file, one whose host will be killed.
- **When.** Archive the first; delete the second; start a creation in the third and kill the host process during checkout; restart the host.
- **Then.** The first checkout is gone and its branch remains; the second is retained and the report names the file; after the restart the third appears as `uncertain` with its path and the two commands, and nothing was removed.
- **Cleanup.** Run the reported commands; delete the repository.

<a id="dh-e-xforge-3"></a>
#### DH-E-XFORGE-3 A session's pull request and merge request appear with live status, using only the user's CLI sign-in
- **Proves.** DH-D-XFORGE-3, DH-D-XFORGE-4, DH-D-XFORGE-5.
- **Given.** A test repository on github.com and one on gitlab.com; `gh` and `glab` installed and signed in with a test account; a real browser; a recording proxy on the machine.
- **When.** (1) Start the host with the bundle disabled and open a session. (2) Enable it; the agent pushes a branch and runs `gh pr create`, and in the other repository `glab mr create`. (3) Open the panel; let a check run finish. (4) Close the tab for five minutes. (5) Sign out of `gh` and refresh.
- **Then.** (1) The proxy records no request to either host. (2) and (3) One card per request shows state and checks, and updates when the check finishes. (4) No request is recorded while no tab is open. (5) The card keeps its last status with its time and shows the sign-in command. Throughout, the proxy shows no request made by the `dsh` process itself and no write request from a provider.
- **Cleanup.** Close the requests, delete the branches, sign in again.

<a id="dh-e-xforge-4"></a>
#### DH-E-XFORGE-4 Starting a session in a worktree from the browser, including a failed attempt
- **Proves.** DH-D-XFORGE-6.
- **Given.** A real browser, a real model API key, a repository with a branch named `taken`, both bundles enabled.
- **When.** Create a worktree named `taken`; send a message at once; then Retry with a generated name; later remove the worktree directory by hand and send another message.
- **Then.** The first turn ends `blocked` with the reason and no model request; after Retry the next turn runs in the worktree and the session row shows the glyph; after the removal the card offers Restore and the turn does not run in the project until the user chooses.
- **Cleanup.** Delete the session and the repository.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Decided by the owner.** The code-host UI is status only ([DH-ADR-38](../design.md#dh-adr-38)): status cards, a link to the code host and the worktree control; no write to a code host ([XFORGE-1](#dh-adr-xforge-1)) and no diff, review or review-queue view ([XFORGE-5](#dh-adr-xforge-5)). A configured status read still runs while the off-switch is set ([DH-ADR-27](../design.md#dh-adr-27)). A delegation target may ask for `isolation: worktree` ([DH-ADR-40](../design.md#dh-adr-40), debt entry U-K1-3; [XFORGE-8](#dh-adr-xforge-8)).
2. **Settled with [session-views.md](session-views.md).** `DH-ADR-VIEW-8` states the rule once: a tool call moves the directory only inside the anchor. The two tools call `workdir.Service.SetForCall`; recovery of a missing directory is dispatched through `workdir.Access` with the reason `recover`, which this area denies for a recorded worktree so that the card can offer Restore.
3. **Assumed of [sandbox-ssh.md](sandbox-ssh.md) (request sent).** Confirm that a common Git directory inside a workspace root is writable under `workspace-write`, that a policy exposes its write roots for the containment check, and that `Resolve` works for a session no agent is running (cleanup).
4. **Settled and assumed.** Settled by their owners: `store.Deleting{Session, Reader}` is sent before a deleted session's rows go; the duty lease is `DH-D-STORE-8`; `tools.Settled` is sent for nested calls too. Still assumed of [execution.md](execution.md) (request sent): the shell result type (command, complete or spilled output, working directory).
5. **Limits kept.** A subagent shares its parent's checkout unless its delegation target asks for `isolation: worktree` ([XFORGE-8](#dh-adr-xforge-8)); nothing is merged automatically. The three deferrals of [XFORGE-9](#dh-adr-xforge-9). Assumed of [jobs-schedule-goal-plan.md](jobs-schedule-goal-plan.md): a job's settle record names the originating tool call and its command, and the job's output can be read at that moment. No forge tool for the model; no webhook intake (`Refresh` is the entry a webhook area would call); status only with the vendor CLI; no diff or review inside the harness; branches are never deleted; reads are unconfined, so a session can read another session's worktree.
6. **To verify by scenario, not known from reading.** Which Git versions honour the filter and lazy-fetch switches; `gh` and `glab` output across versions; whether `glab` honours the proxy variables `gh` does.

## 11. Units dispositioned by this spec

| Type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 8 | 6 ported, 2 replaced (`client-ui-forge`, `forge-bundle`) |
| `UP-DEBT` | 56 | 31 resolved, 3 not-applicable, 22 carried |
| `UP-TOOL` | 2 | 2 ported |
| `UP-EVT` | 3 | 2 ported, 1 dropped |
| `UP-CFG` | 6 | 6 ported, with dropped fields named |
| `UP-SVC` | 4 | 3 ported, 1 replaced |
| `UP-DEC` (handed over) | 9 | 2 ported, 4 replaced, 3 dropped |

Go items declared: 9 decisions, 6 deliverables, 13 local tests, 4 scenarios. Rows are in `trace/fragments/X1.jsonl`.
