# Human interaction, approvals, credentials, account, identity and feedback: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". IDs in this document use the area key `HUMAN`. Nothing here has
> been compiled, run or tested.

| | |
|---|---|
| **Area key** | `HUMAN` (cluster A17) |
| **Upstream packages** | 13: `interaction` (5), `credentials` (3), `account` (2), `identity` (1), `feedback` (2) |
| **Serves outcomes** | [DH-O-7](../design.md#dh-o-7), [DH-O-13](../design.md#dh-o-13), [DH-O-14](../design.md#dh-o-14); also [DH-O-6](../design.md#dh-o-6), [DH-O-8](../design.md#dh-o-8), [DH-O-12](../design.md#dh-o-12), [DH-O-4](../design.md#dh-o-4) |
| **Depends on areas** | A01 and A05 (event registration, append, flush, resume closers, the lock helper), A02 (agent, `Send`), A03 (stage 4 of the pipeline, tool declaration, prompt context), A04 (routes that name a credential), A06 (session upload, projections, `Visibility`), A07 (`kv`, home layout, settings form), A08 (path canonicalisation, durable file write), A09 (child environment), A10 (sandbox mode, write roots, secret mask), A18 (off-switch), A19 and A20 (routes, SDK and ACP methods, peers), A23 (`schema`, `egress`, `watch`), A24 (commands), C4 and C7 (views) |
| **Priority** | P0 for the registry, approvals and the secret type and store: without them every `Ask` is "no" and no provider has a key. P1 for grants, presets, questions, commands, keychain, account, feedback |

## 1. Purpose

This area is every place where the harness needs a person, or needs to know who or what it is acting for. A person approves an action a static rule cannot decide, answers a question the model asks, runs a command that must not cost a model turn, hands over a key or signs in, and may choose to send feedback. Without this area nothing could be approved (so every guarded action would be refused), no model provider could be authenticated, and nothing would stand between "the user typed a remark" and "the whole session was uploaded".

Three pieces here are shared by other areas and are specified once: the **interaction registry**, the rule for **who may answer** when several clients are connected, and **session grants** ([DH-ADR-16](../design.md#dh-adr-16)). The rule that binds all of it is [DH-O-7](../design.md#dh-o-7): with nobody to ask, the answer is no.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Interaction** | One pending thing a person must settle, held in the host: an approval, a question, a sign-in prompt or a consent. It has an id, an audience, a payload and exactly one settlement. | One set of rules (answered once, fail closed, late answers ignored) for every kind and every client. |
| **Channel, answerer** | A channel is one kind of route to a decision maker: `ui` (browser tabs), `sdk`, `acp`, `cli`. An answerer is the component a carrier registers for its channel; it shows an interaction and brings back the answer. | The registry must not know HTTP, JSON-RPC or a terminal. |
| **Binding** | The fact "this agent's interactions go to this channel and this peer". An agent has at most one. | Decides who answers when a browser and an SDK program are both connected. |
| **Actor** | Who settled something: principal, channel and peer, taken from the admitted connection. | An audit record without it cannot say who approved. |
| **Approval, outcome** | A yes or no about one action. The outcome is one of a closed set (section 6.2). | A caller can switch over every case; a missing channel has its own value. |
| **Approval policy** | A session's setting `ask` or `never`. `never` rejects before anyone is asked. | The deterministic stance for unattended runs. |
| **Subject** | What an approval is about in a form a grant can name: a directory, an application or, for browser use, a website. Declared by the asker from validated arguments. | A grant must match on a fact, never on display text. |
| **Session grant** | An owner's standing yes for one subject scope, held in memory for one live agent, logged, listed and revocable. | Fewer repeated prompts without a rule that outlives the work. |
| **Permission preset** | A named pair: sandbox mode and approval policy. | One "Permissions" choice for a person; the two mechanisms keep their own values. |
| **Human command** | `/name input` typed by a person; it runs host code against one agent and never becomes a model message. | Direct control that costs no model turn. |
| **Credential reference, record** | A reference is the name of one secret value (`DEEPSEEK_API_KEY`). A record is a structured credential one component owns under `<owner>/<id>`, such as a sign-in grant. | Configuration names a secret without holding it; a token document has no environment-variable form. |
| **Secret value, lease** | The value type whose every printed form is `[redacted]`. A lease is one acquired value plus a signal that fires when it is revoked. | A value cannot leak by formatting; in-flight work ends when the credential goes. |
| **Authorisation flow** | Code that obtains a record by talking to a person (open a page, paste a code). | Some credentials can only be obtained, not configured. |
| **Anonymous identifier** | One random UUID per harness home. | Lets a collector group what one installation chose to send. |

Ownership and access differ here as elsewhere. A session's agent **owns** its grants and its pending interactions: they end with it. An answerer has **access** to settle an interaction; it owns nothing. The credential store **owns** secret values; a route that names a reference has access for one request.

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.interaction.user-approval` | `dsh-user-approval` | definition and provider | `ctx.approval`: one-shot decisions, session policy, the audit pair |
| `UP-PKG-packages.interaction.permission-presets` | `dsh-permission-presets` | consumer of two seams | `ctx.permissionPresets`: named sandbox and approval bundles, `/permission` |
| `UP-PKG-packages.interaction.user-questions` | `dsh-user-questions` | definition and provider | `ctx.userQuestions`: `ask`, `askTimed`, late answers, the `userQuestions` projection |
| `UP-PKG-packages.interaction.tool-ask-user` | `dsh-tool-ask-user` | consumer (tool) | The model-facing `ask_user_question` tool |
| `UP-PKG-packages.interaction.commands` | `dsh-commands` | definition and provider | `ctx.commands`: registry and dispatch of human commands |
| `UP-PKG-packages.credentials.credentials` | `dsh-credentials` | definition | `ctx.credentials`: references and records, UI-safe description |
| `UP-PKG-packages.credentials.credentials-local` | `dsh-credentials-local` | provider | Owner-only YAML file over the launch environment and two `.env` files |
| `UP-PKG-packages.credentials.authorization` | `dsh-authorization` | definition and provider | `ctx.authorization`: flows that obtain a record from a person |
| `UP-PKG-packages.account.deepseek-account` | `dsh-deepseek-account` | provider | Vendor sign-in, profile, balance, notifications, request credentials |
| `UP-PKG-packages.account.deepseek-auth` | `dsh-deepseek-auth` | definition and provider | `ctx.deepseekAuth`: picks API key or sign-in for one vendor request |
| `UP-PKG-packages.identity.anonymous-user-id` | `dsh-anonymous-user-id` | library | One random UUID per home, minted on first use |
| `UP-PKG-packages.feedback.command-feedback` | `dsh-command-feedback` | consumer | `/feedback`, the `sessionFeedback` remote, the category list |
| `UP-PKG-packages.feedback.message-feedback` | `dsh-message-feedback` | provider | Ratings and notes on assistant messages, versioned edits |

### 3.2 Behaviour

**Approvals.** `request()` refuses outside an open turn, appends `approval/asked`, decides, appends `approval/decided` and returns one of four outcomes; only `allowed-once` proceeds (`packages/interaction/user-approval/src/index.ts:57`, `:243-262`). Policy `never` rejects inside the service before any answerer runs; a throwing answerer, an unknown return value or no answerer becomes `unavailable`; an abort becomes `cancelled` and a late answer is discarded (`:296-333`). Answerers are listeners of the `approval/request` waterfall, registered by the ACP bridge, the SDK server and the web remotes (`docs/event-producer-consumer.md:32`); a deployment is told to compose one terminal answerer because sibling order is not a priority (`packages/interaction/user-approval/README.md:32`). The request carries the tool name, a reason and an optional call id, not the arguments (`README.md:166`). The decided record holds the id and outcome only (`src/index.ts:260`). A child agent may get the policy `never` at delegation (`src/index.ts:38-44`).

**Presets.** A preset names one sandbox mode and one approval policy; the base bundle ships `read-only`, `workspace-write` and `danger-full-access` (`packages/bundle/base/cordis.patch.yml:271-275`). `set()` appends `permission/preset` and writes each changed knob through its own setter; `current()` is the last selection that still matches, else the first matching entry, else the derived label `custom` (`packages/interaction/permission-presets/README.md:85`, `:89`). A fresh session is pinned to the default; a resumed one keeps its values (`:64`). `auto` is reserved for the experimental review layer (`:32`).

**Questions.** `ask()` blocks; `askTimed()` returns "pending" at a deadline while the question stays answerable, and a later answer is steered into the agent as a user message (`packages/interaction/user-questions/README.md:27`, `:35`). A subagent's session is refused (`src/index.ts:144-148`). Open, continued and settled questions are derived from logged tool calls and results, with no separate state (`README.md:70`). A question may carry a presentation intent; the `approval` intent turns a question into a permission card with reject, allow and an optional "always" (`src/types.ts:36-51`), which the experimental computer-use package uses for per-application access, saved to a profile store when "Always allow" is chosen (`packages/experimental/cua/src/application-access.ts:146-163`). The tool has two schemas selected by configuration, blocking (`legacy`, the default) and `timed` (`packages/interaction/tool-ask-user/src/index.ts:24-25`).

**Commands.** A line `/name rest` is dispatched to a registered handler for one agent; unknown names are rejected, never sent to the model (`packages/interaction/commands/README.md:52`). A registration is global or scoped to one agent (`:56`). Each run appends `command/run` and `command/done`, outside any turn (`:93`; `src/types.ts:105`, `:112`). Only the web client and the terminal UI have an adapter (`README.md:12`).

**Credentials.** Consumers resolve a reference per operation and never cache (`packages/credentials/credentials/README.md:116`). `describe` reports configured, source, writable and a masked preview, never the value (`:56`). The local provider resolves in a fixed order: its file, the launch environment snapshot, the project `.env`, the home `.env` (`packages/credentials/credentials-local/README.md:73-80`). The file is never loaded into the process environment and diagnostics never quote a value (`:115`, `:163`). A spawned child starts from the parent environment minus names matching `KEY|PASSWORD|SECRET|TOKEN` and every `DSH_*` name (`packages/subprocess/subprocess/src/index.ts:54`, `:73`). Upstream states the limit: the model's tools run as the same user and can read the file (`packages/credentials/credentials-local/README.md:115`). An authorisation flow is registered under the record it writes; one attempt per record; prompts travel with the request to the page that started it; `authorized` is reported only after the record's commit was observed (`packages/credentials/authorization/README.md:65`, `:69`, `:113`).

**Account.** Sign-in opens the vendor page in the system browser, receives `/oauth/callback` on the host's web server, checks state, exchanges a PKCE code once and stores a grant record before redirecting (`packages/account/deepseek-account/README.md:51`). Local cancellation is final (`:20`). The token is released only as headers for one allowed origin, with `reject()` and a `revoked` signal (`:16`). Sign-out removes the local grant first, then retries the vendor's logout up to five times in the background (`:105`). `deepseekAuth` orders the credential kinds for a vendor request; with `auto` billing it takes the first kind that has a credential (`packages/account/deepseek-auth/README.md:55-65`).

**Identity and feedback.** The identifier is minted on first use by any caller and stored as one line (`packages/identity/anonymous-user-id/src/index.ts:68-98`); every vendor model request carries it (`packages/llm/llm-deepseek/src/adapter.ts:158`) and the first `/feedback` mints it to print it (`packages/feedback/command-feedback/src/index.ts:81`). Feedback is three log-only event types in the session log (`packages/feedback/message-feedback/README.md:52`). Recording it triggers the upload of the whole session log by default ([../libs/opentelemetry.md](../libs/opentelemetry.md) §1.1).

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | Upstream behaviour |
|---|---|
| No answerer, or the answerer throws | `unavailable`; the tool call is denied (`packages/interaction/user-approval/src/index.ts:310-318`) |
| Host restarts with an approval pending | Not restored; the audit pair is left open (`packages/interaction/user-approval/README.md:168`) |
| A timed question's process ends | The question becomes "continued" and can still be answered as a new user message (`packages/interaction/user-questions/README.md:35`) |
| Credential file unreadable, malformed or readable by others | Start-up fails; on a live reload the last good content keeps serving (`packages/credentials/credentials-local/README.md:121`) |
| Page reload during sign-in | The attempt is abandoned (`packages/credentials/authorization/README.md:148`) |
| Vendor logout fails | Local sign-out stands; the old token may stay valid at the vendor (`packages/account/deepseek-account/README.md:105`) |

## 4. Upstream decisions dissected

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `2026-07-06-approval-seam` | One service, closed outcomes, answerers as waterfall listeners, fail closed | keep the vocabulary, change the routing | An ordered listener chain cannot say who answers for an agent | [HUMAN-1](#dh-adr-human-1), [-2](#dh-adr-human-2), [-3](#dh-adr-human-3) |
| `2026-09-19-timed-user-question-two-settlements` | A timeout returns "pending"; a late answer is a user message | keep; change the claim stream | The host owns one deadline and a hold, so no claim protocol is needed | [HUMAN-7](#dh-adr-human-7) |
| `2026-07-30-credential-boundaries-and-atomic-registration` | The stored document is never loaded into the environment; the gap against the model is stated | keep, and close part of the gap | The keychain it names as "the real answer" is built | [HUMAN-9](#dh-adr-human-9) |
| `2026-10-09-stored-credential-precedence` | A stored value wins over the environment for every provider | keep | One precedence; a save always takes effect | [HUMAN-9](#dh-adr-human-9) |
| `2026-08-13-credential-records-and-authorization-flows` | Records under `<owner>/<id>`; flows own the write; prompts travel with the request | keep, change two parts | Prompts become registry items so a reload re-attaches; a flow can declare revocation | [HUMAN-10](#dh-adr-human-10) |
| `2026-09-14-deepseek-account-login` | System-browser sign-in, local cancel is final, success only after storage | keep; drop the desktop embedding | No desktop application ([DH-ADR-18](../design.md#dh-adr-18)) | [HUMAN-11](#dh-adr-human-11) |
| `2026-11-10-deepseek-request-authentication` | One service picks key or sign-in and releases headers bound to an origin | keep the release, drop presence-ordered `auto` | Taking "whichever exists" changes who is billed without a record ([DH-O-8](../design.md#dh-o-8)) | [HUMAN-11](#dh-adr-human-11) |
| `2026-09-05-canonical-feedback-log` | Feedback lives in the session log; delivery rides on default-on senders | keep the log, drop default delivery | [DH-ADR-9](../design.md#dh-adr-9) | [HUMAN-13](#dh-adr-human-13) |
| Code: requests only inside an open turn (`user-approval/src/index.ts:245`) | A bare event between turns looked like a crash tail | drop | The Go store is transactional; there is no tail to confuse | [HUMAN-3](#dh-adr-human-3) |
| Code: the `approval` question intent with "always" (`user-questions/src/types.ts:36-51`) | Computer use needed a permission card | change | A permission decision asked as a question skips policy, audit and fail-closed routing | [HUMAN-7](#dh-adr-human-7), [HUMAN-4](#dh-adr-human-4) |
| Code: `custom` is a derived label (`permission-presets/README.md:89`) | Knobs can be set separately | keep | The preset is a convenience, not the authority | [HUMAN-6](#dh-adr-human-6) |
| Code: commands exist only where an adapter exists (`commands/README.md:12`) | Commands were a UI feature | change | A command is host behaviour; every carrier can dispatch it | [HUMAN-8](#dh-adr-human-8) |
| Code: the id is minted by any first caller (`anonymous-user-id/src/index.ts:68`) | Records should always correlate | change | It must not exist before the user opts in to a sender | [HUMAN-12](#dh-adr-human-12) |

## 5. Tech debt and its resolution

54 units. Each has its own ledger row with the reason; they are grouped here by cause.

| Group (units) | Cause | Answer in Go | Resolved / carried / n.a. |
|---|---|---|---|
| Approvals (`user-approval.1` to `.5`) | One-shot only; a request without arguments; tied to an open turn; no shipped answerer | Session grants; the item resolves the call's presentation from the log; requests need no turn; each interactive profile mounts the answerer of its carrier, and `dsh headless` asks on a controlling terminal through the `cli` answerer and refuses when there is none or `--json` is set ([app-cli.md](app-cli.md), `DH-ADR-CLI-7`). Carried: a pending prompt is not re-asked after a restart, because the interrupted turn is closed, never continued | 4 / 1 / 0 |
| Presets (`permission-presets.1` to `.5`) | A fixed table with a derived label | The table is live configuration, so a named combination is a config entry. Carried: two knobs only, `auto` cannot be a default, a default must name a preset | 2 / 3 / 0 |
| Questions (`tool-ask-user.1` to `.4`, `user-questions.1` to `.3`) | Two tool modes, one web answerer, state lost in script sub-calls, a subagent's question refused | One schema; every call is in the projection; nested calls are logged at admission; peer-addressed items; a child's question reaches the root's answerer ("Who may answer", rule 5). Carried: the form vocabulary, browser-local drafts | 5 / 2 / 0 |
| Commands (`commands.1`, `.2`) | Text-only input; cooperative cancellation | Typed arguments from a Go struct. Carried: a goroutine cannot be killed | 1 / 1 / 0 |
| Credentials (`credentials.1` to `.4`, `credentials-local.1` to `.4`, `authorization.1` to `.3`) | A same-user readable file; no enumeration, revision, durability, revocation or orphan check | Keychain envelope and sandbox mask; `References`; a revision on `Set`; a durable write; flows declare `Revoke`; the service joins records with flows. Carried: environment-shaped names, and a running process cannot see a later export (two units) | 8 / 3 / 0 |
| Vendor account (`deepseek-account.1`, the `protocol.ts:245` marker, `deepseek-auth.1` to `.3`) | Vendor protocol limits; one deployment per kind; route kind known only while a plugin is connected | Route kind is read from configuration and the request header. Carried: no token refresh and loopback redirect, undefined business-code copy, presence-only check | 2 / 3 / 0 |
| Identity (`anonymous-user-id.1` to `.5`) | Always minted, sent to every gateway, racy, memoised | Created in one transaction at the first opted-in use; read fresh; off per route. Carried by design: no recovery, no cross-home link | 3 / 2 / 0 |
| Feedback (`command-feedback.1` to `.7`, `message-feedback.1` to `.7`) | No list, withdraw, actor, flush or bound; a full scan per call; upload on feedback | A projection, a withdraw event, the actor on every event, flush before acknowledgement, a cap, explicit sending; a change made from a process that does not hold the session is handed to the holder through the request table (U-K1-5). Carried: no severity field, history is not erased. Not applicable: the old sidecar format | 11 / 2 / 1 |

## 6. Go design

### 6.1 Shape

All packages sit under `src/harness/internal/human` ([../design.md §4.2](../design.md#42-go-module-layout)). A contract package holds types and the service key; the component sits beside it. The code sketch after the table is illustrative, not final.

| Package | Component (config row) | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `human/interact` | `human.interactions` | `interact.Registry` under `interact.Key` | nothing | The one place a pending item lives ([DH-ADR-WIRE-4](../upstream/wire-and-api.md#dh-adr-wire-4)) |
| `human/approval` | `human.approval` | `approval.Service` (requests, policy, grants) | registry, session store, projections; optional prompt assembly | Stage 4 of the tool pipeline and every other asker |
| `human/permission` | `permission.presets` | `permission.Service` | approval, sandbox policy (A10), agents, commands | The one "Permissions" choice |
| `human/question`, `human/question/tool` | `human.questions`, `tool.ask-user` | `question.Service`; the tool | registry, agents, projections; `tools.Key` | Service and tool are separate components ([../design.md §4.2](../design.md#42-go-module-layout)) |
| `human/command` | `human.commands` | `command.Registry` | session store, attachments (A07) | Human commands on every carrier |
| `human/secret`, `secret/file`, `secret/keychain`, `secret/redact` | `human.secrets`, `human.secrets.redact` | `secret.Store` under `secret.Key`; a `tools.Shape` listener | `kv`, `watch`, `home` (A07, A23) | Values behind one interface; masking of released values |
| `human/authz` | `human.authorization` | `authz.Service` | secrets, registry | Flows that ask a person |
| `human/account` | `account.deepseek` (default off) | `account.Service`, `account.Authorizer` | secrets, authz, `egress` | The vendor account |
| `human/identity` | `human.identity` | `identity.Service` | `kv` | The anonymous identifier |
| `human/feedback` | `human.feedback` | `feedback.Service` | session store, projections, commands; optional upload (A06) | Capture, preview and export |

```go
// package interact
type Audience struct{ Agent agent.Ref; Peer PeerRef }        // exactly one is set
type Item struct{ ID ID; Kind Kind; To Audience; Session session.ID; Call tools.CallID; Payload any; Deadline time.Time }
type Registry interface {
    RegisterAnswerer(owner *cordis.Context, ch Channel, a Answerer) (*cordis.EffectHandle, error) // one per channel
    Bind(scope *cordis.Context, a agent.Ref, b Binding) (*cordis.EffectHandle, error)             // one per agent
    Ask(ctx context.Context, it Item) (Answer, Actor, error)     // blocks; settles exactly once
    Answer(ctx context.Context, by Actor, id ID, a Answer) error // interaction/settled, /unknown, /not-yours
    Pending(v Visibility) []Item                                 // every baseline includes these
}
type Answerer interface{ Deliver(ctx context.Context, it Item) error; Withdraw(id ID, why Reason) }
// package approval
type Service interface {
    Request(ctx context.Context, r Request) (Decision, error)    // Decision{Outcome, Detail, Grant, By}
    Policy(s session.ID) Policy                                  // SetPolicy(ctx, agent, policy, by) switches it
    Grants() Grants
}
type Grants interface {
    List(s session.ID) []Grant
    Revoke(ctx context.Context, s session.ID, g GrantID, by Actor) error
    Roots(s session.ID) []string                                 // directory grants in force, read by area A10
}
// package secret
type Value struct{ /* unexported */ }                            // every text, JSON and log form is "[redacted]"
type Store interface {
    Acquire(ctx context.Context, r Ref) (Lease, error)           // Lease: Reveal(), Source(), Revoked(), Release()
    Describe(ctx context.Context, r Ref) (Info, error)           // configured, source, protection, masked preview, revision
    Set(ctx context.Context, r Ref, v Value, ifRevision string) error // Unset(ctx, r) removes
    References(ctx context.Context) ([]Ref, error)               // named by configuration or stored
    Record(ctx context.Context, k Key) (Record, bool, error)     // also ListRecords, DeleteRecord
    ModifyRecord(ctx context.Context, k Key, fn func(Record, bool) (Record, bool, error)) error
    Mask(p []byte) []byte                                        // replaces every value released by this process
}
```

`Visibility` is area A06's type; `PeerRef` and `Actor` are built by a carrier from an admitted peer ([DH-ADR-SECURITY-8](../upstream/security.md#dh-adr-security-8)) and never from request data.

### 6.2 Contracts

**Interaction kinds** (closed and complete: four, and one typed intent of a question; a new kind is a code change with a payload type, a fragment and a protocol mapping). The registry's `Pending` reports the kind per session without waking it, which is what a session list shows.

| Kind | Asked by | Audience | Answer | With nobody to answer |
|---|---|---|---|---|
| `approval` | `approval.Service` | the agent | allow, allow and grant, reject | `unavailable` |
| `consent` | `approval.Service` for a change to the user's own machine (A10's "prepare directory") or for attaching a plugin, whose inspection report is the display (A22) | the agent, or the peer running a command | yes, no; never grantable; no selection | `unavailable` |
| `question`, intent `plan-review` | `question.Service` for A13's `exit_plan_mode`; the typed payload is the plan text (Markdown) | the agent | exactly one of three settlements: approve; keep planning, with feedback text; dismiss. It is a blocking question with no timeout and no grant, and the approval policy does not apply to it | an error, which A13 reports as `plan/no-reviewer`; a cancelled item is `plan/review-interrupted` |
| `question` | `question.Service` | the agent | one item per question, or skip | error for a blocking call, "pending" for a timed one |
| `authorization` | `authz.Service` | the peer that began the attempt | text, secret text, a selection, or decline | the attempt settles `cancelled` |

**Who may answer.** (1) A carrier registers one answerer per channel; a second registration for a channel fails at load. Each interactive profile mounts the answerer of its carrier. `dsh headless` registers the `cli` answerer only when a controlling terminal can be opened and `--json` is not set ([app-cli.md](app-cli.md), `DH-ADR-CLI-7`); otherwise that run has no answerer and every approval it raises is `unavailable`. The context an answerer is given with `Deliver` is cancelled when the item is withdrawn, and an answerer that returns an error settles the item `unavailable`. (2) The carrier that creates or resumes an agent binds it to its own channel and peer as an effect in the agent's scope, so the binding dies with the agent. (3) Only the bound channel may settle that agent's items. Several browser tabs are views of the one `ui` answerer: all show the item, the first valid answer wins, the others are told who answered. (4) When the bound answerer unloads or its peer disconnects for good, the agent is unbound and its items settle `unavailable`; an owner on the `ui` channel may then claim it explicitly, which is logged. Nothing is transferred automatically. (5) An agent created by another agent is never bound by itself. Every item it raises (approval, consent, question, plan review) is registered for its own session and offered to whoever answers for the root session of its tree, labelled with the child's path; if the root has no binding it settles `unavailable` (owner decision of 2026-11-19; [DH-ADR-SUBAGENT-6](subagent-team.md#dh-adr-subagent-6)). Delegation no longer seeds the approval policy `never` on a child; A12's `childApprovals: deny` restores upstream's automatic refusal. (6) A non-owner peer needs an `interactions: answer` role on the session and can never create a grant. (7) A plugin process, the tool endpoint, the host's control endpoint and the model have no path to `Answer`, to a grant, to a preset or to a secret value. (8) An answer is accepted only from a peer the carrier admitted: a browser session that A18 admitted, which it never does for a process the host itself started for the model, or the stdio client that started the host. An owner cookie is enough; nothing readable from the home makes one ([DH-ADR-HOST-12](host-telemetry.md#dh-adr-host-12)).

**The approval request and its outcomes.** `Request{Agent, Kind, Tool, Call, Parent, Origin, Reason, Display, Subject, Mode}`. `Call`, `Parent` and `Origin` are the nested-call identity [tools.md](tools.md) asks for; `Mode` is the sandbox mode an escalation asks for. Stage 4 of the pipeline is one call to `Request`, built from the listener's `Ask{reason, display, subject}`; the optional `subject` (kind, name, label) is what a grant can match. The pipeline holds no grant state and writes no approval or grant event: `approval/asked`, `approval/decided`, `grant/created` and `grant/ended` are written here and nowhere else, and the three failure outcomes are the codes [tools.md](tools.md) puts on the tool result.

| Outcome | Meaning | Proceeds | Code in [tools.md](tools.md) |
|---|---|---|---|
| `allowed-once` | A person or an automation client allowed this call | yes | n/a |
| `allowed-by-grant` | A session grant in force covers the subject; nobody was asked | yes | n/a |
| `rejected` | A person said no, or the policy is `never` | no | `approval/rejected` |
| `cancelled` | The request's context ended first | no | `approval/cancelled` |
| `unavailable` | No binding, no answerer, a failed or panicking answerer, a timeout, or the host is stopping; `Detail` says which | no | `approval/unavailable` |

**What the person is shown.** The agent and, for a child, its lineage; the tool and its arguments, rendered from the logged call by the tool's `PresentCall`; the origin (the model, or a script under which outer call); the asker's reason; the sandbox mode asked for; the subject; and the choices. "Allow for this session" appears only when the request has a grantable subject and the viewer is an owner, and its label states the exact scope ("writes under `/repo/build` until this session ends"). **Timeouts.** `approval.Config.Timeout` is 0 by default: a prompt waits until answered or cancelled, and the tool budget is paused meanwhile ([tools.md](tools.md)). A set timeout settles `unavailable` with detail `timeout`.

**Session grants.**

| Scope kind | Names | A later request matches when | Offered by |
|---|---|---|---|
| `directory` | One canonical absolute directory, access `write` | Its subject is a directory whose path, canonicalised again at request time (A08), is that directory or inside it, and its `Mode` is no wider than the mode the creating approval allowed | Sandbox escalation and file tools (A10, A08) |
| `application` | Provider id and application id | Its subject has the same two ids, taken from validated tool arguments. The id must be a stable identity the provider reports (a bundle identifier, an executable path); a process id is refused as a grant subject, and such a call asks each time | Computer use (X4), only where arguments identify the application ([DH-ADR-19](../design.md#dh-adr-19)) |
| `website` | One origin (scheme, host and port), for browser use only | Its subject is that exact origin and the asker is the browser service | Browser use (A16). The owner counts one website as "one application" ([DH-ADR-33](../design.md#dh-adr-33)); web fetch to a private address is never grantable and asks every time |

A request with no subject can never be granted or matched. Matching runs at stage 4 only, after policy listeners and guards, and after the `never` check. A grant is created only by an owner's "allow and grant" answer on the `ui` or `cli` channel, where a person answers. An `sdk` client cannot create one; `sdk` and `acp` stay one-shot (Recommended default, not confirmed by the owner). A grant lives and dies with the one live session it was given to ([DH-ADR-HUMAN-5](#dh-adr-human-5)). The grant table is in memory and is the only authority; the log is the audit. While a directory grant is in force A10 reads it as a write root through `Grants.Roots`. Listing and revoking: a panel per session (the routes `grant.list` and `grant.revoke` are registered by C4, [ui-conversation.md](ui-conversation.md) §6.2; views by C4 and C7), the command `/grants`, and SDK methods (A20). Grants are shown to an owner only: a non-owner with the answer role sees no grants strip, and no role offered to a share viewer may contain `interactions: answer` ([DH-ADR-37](../design.md#dh-adr-37)). Revocation applies to the next stage-4 check; a call already allowed, and a process already started, keep what they had. A change of preset or policy revokes every grant of the session.

**Presets shipped** (live configuration; `custom` is a derived label and `auto` is reserved for the experimental review layer).

| Preset | Sandbox mode | Approval policy | What it allows |
|---|---|---|---|
| `read-only` | `read-only` | `ask` | Reads; every write needs an escalation a person approves |
| `workspace-write` (default) | `workspace-write` | `ask` | Writes inside the write roots; anything wider is asked |
| `danger-full-access` | `danger-full-access` | `never` | Everything the user can do; nothing is asked and nothing can be escalated |

**Events.** Live events are notify-form keys ([../design.md §4.6](../design.md#46-event-model)), dispatched through the agent's scope when they concern one agent.

| Upstream event | Go | Note |
|---|---|---|
| `approval/request` | none: `Registry.Ask` routes to the bound answerer | An ordered chain is replaced by ownership |
| `user-questions/request` | none: the same registry | As above |
| `authorization/settled` | `authz.Settled{Key, Outcome}` | Sent after the record key is released |
| `commands/change` | `command.Changed{Scope}` | After a registration commits |
| `credentials/reference-updated` | `secret.ReferenceChanged{Ref, Origin}` | `Origin` is `local` or `foreign` (another process) |
| `credentials/record-updated` | `secret.RecordChanged{Key, Op}` | Also closes leases of a deleted record |
| `deepseek-account/signed-out` | `account.SignedOut` | After local removal, before leases close |
| `deepseek-account/session-expired` | `account.Expired` | After a 401 cleared the grant |
| `deepseek-account/model-sign-in-required` | none | The attempt record's `llm/sign-in-required` already says it ([llm.md](llm.md)) |
| `feedback/committed` | none | Nothing is sent because feedback was recorded |
| `permission-presets/catalog-changed` | `permission.CatalogChanged` | Readers re-read the catalogue |
| new | `interact.Changed{Audience}`, `grant.Changed{Session}` | A19 pushes the `interaction` fragment; A10 re-reads roots |

**Config.** Go structs with defaults as Go values; decoding is strict.

| Component | Replaces | Fields (default) |
|---|---|---|
| `human.approval` | `UP-CFG-dsh-user-approval` | `Policy` (`ask`), `Timeout` (0), `Grants.Kinds` (`directory`, `application`, `website`), `Grants.MaxPerSession` (32) |
| `permission.presets` | `UP-CFG-dsh-permission-presets` | `Presets` (the three above), `DefaultPreset` (`workspace-write`); live |
| `tool.ask-user` | `UP-CFG-dsh-tool-ask-user` | `DefaultTimeout` (-1, wait), `MaxTimeout` (as upstream); the `mode` field is gone |
| `human.secrets` | `UP-CFG-dsh-credentials-local` | `Backend` (`auto`, `keychain`, `file`), `ProjectEnv` (true), `HomeEnv` (true), `Watch` (true), `Debounce` (100 ms), `LockWait` (30 s). The path is fixed by the home layout (A07) |
| `account.deepseek` | `UP-CFG-dsh-deepseek-account` | The three origins, timeouts and logout retries as upstream (`packages/account/deepseek-account/src/index.ts:73-86`); `RequestHeaders` values are text or a reference; `Notifications` (false). `desktopPlatform` is gone |
| `human.feedback` | `UP-CFG-dsh-message-feedback` | `MaxNoteBytes` (8192), `MaxTextBytes` (8192), `MaxEventsPerSession` (1000) |

**Services.** `ctx.approval` to `approval.Key`; `ctx.authorization` to `authz.Key`; `ctx.commands` to `command.Key`; `ctx.credentials` to `secret.Key`; `ctx.deepseekAccount` to `account.Key`; `ctx.deepseekAuth` to `account.AuthKey` (merged into the account package); `ctx.messageFeedback` and `ctx.sessionFeedback` to `feedback.Key` (merged); `ctx.permissionPresets` to `permission.Key`; `ctx.userQuestions` to `question.Key`.

**Persisted data.** All are log-only session events registered with area A01's registry: the nine types [session-store.md](session-store.md) lists as kept, and three new ones marked below.

| Event | Data |
|---|---|
| `approval/asked` | id, kind (`approval` or `consent`), call, parent, origin, tool, reason, subject, mode |
| `approval/decided` | id, outcome, detail, `by` (principal, channel, peer), grant id if one was used or created |
| `approval/policy`, `permission/preset` | The whole new value and `by` |
| `grant/created`, `grant/ended` (new) | Grant id, scope, `by`, the approval that created it; the reason `revoked`, `session-ended`, `host-stopped`, `policy-changed`, and `by` for a revocation |
| `command/run`, `command/done` | Command id, name, typed arguments unless the command opts out, `by`; outcome, text, an optional source event `seq` |
| `feedback/record`, `feedback/withdrawn` (new), `feedback/message-put`, `feedback/message-delete` | Item id, category, text or rating and note, an optional referenced `seq`, version, `by` |

Projections: `approval` (policy), `grants` (for display only), `permissions` (current preset), `questions` (open, continued, settled), `feedback` (current items). Machine state in `kv` (A07): namespace `identity`. Files: `<home>/secrets/` (the store, its lock, the fallback key file).

**Tool.** `ask_user_question` (`UP-TOOL-dsh-tool-ask-user.ask_user_question`): the same name, one schema. `questions` as upstream; `timeout` in seconds is optional, `-1` waits. A call returns the answers, or `{pending, callId}` at its deadline; a later answer arrives as a user message that names the call. The model-facing text is one line per question; the structured value stays in the log. **Commands owned here.** `/permission [preset]`, `/grants [revoke <id>]`, `/feedback [text]`. A command declares a Go argument struct (or raw text) and whether it needs an owner. `/permission` and `/grants revoke` do.

**Secrets: how each rule is enforced.**

| A secret must not be | Enforced by | Proof |
|---|---|---|
| Printed | `secret.Value` implements every formatting, text and JSON interface as `[redacted]`; `Reveal()` is the only way out and a source check lists its permitted callers. No route, SDK method or plugin call returns a value: writing is one-way and `Describe` gives a masked preview. Parse errors carry a position and a name, never the line | `DH-T-HUMAN-10`, `-11` |
| Logged | The type's `slog` form is redacted. The component `human.secrets.redact` masks every released value in tool results at the `tools.Shape` stage, before they are logged or shown to the model; area A06's baseline redactor calls `Store.Mask` | `DH-T-HUMAN-10`, `-12` |
| Inherited by a child process | The harness never writes a stored value or a `.env` entry into its own environment (a source check forbids `os.Setenv` outside the starter). Area A09's `BuildEnv` removes every name in `Store.References()` ([DH-ADR-EXEC-8](execution.md#dh-adr-exec-8)). A child that needs a secret gets it as an explicit entry built from a lease at spawn | `DH-T-HUMAN-12`, `DH-E-HUMAN-3` |

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Owner (who releases it) | Who merely has access |
|---|---|---|
| A pending interaction | The registry, for the asker's context; settled once | The bound answerer and its views |
| An answerer registration; a binding | The carrier's fiber; the agent's scope | The registry |
| A grant | The live agent of its session; ended at disposal, host stop, revocation or policy change | Stage 4; A10 through `Roots` |
| A secret value | The store; a lease is released by its holder | A route or flow that names the reference or record |
| The keychain item (the data key) | Created by the harness for one home; removed only by `dsh secret reset` | Any process of the same user that asks the keychain |
| A sign-in attempt | The `authz` component, in memory | The peer that began it |
| A vendor grant record | Shared: the vendor can revoke it; the harness deletes only its local copy | The account service |
| The anonymous identifier | The `identity` namespace; removed by reset | Senders the user opted in to |

| Situation | What happens | Undone, or left uncertain |
|---|---|---|
| No binding or no answerer | `unavailable`; the call is denied | Nothing ran |
| An answer for a settled, cancelled or unknown item | `interaction/settled` or `interaction/unknown`; the view is replaced by the settlement | Nothing changes |
| The host stops with items pending | Each settles `unavailable` with detail `host-stopping` before the session flushes | Not restored |
| The host dies with an approval asked and not decided | On resume the lease holder appends `approval/decided` (`unavailable`, `host-stopped`) and `grant/ended` (`host-stopped`) for every grant without an end, before the session accepts work | Grants are not restored; the next call asks |
| Either audit append fails | The request returns an error and the call is denied; no decision exists without its record | Nothing ran |
| A grant's directory is replaced by a link | The subject is canonicalised per request, so the request no longer matches and asks | The grant stays listed until revoked |
| The keychain is missing or locked | `auto` uses the owner-only key file and reports `protection: file`; `keychain` set explicitly fails the component | Nothing is written in a weaker mode silently |
| The keychain item is lost | Stored values cannot be decrypted: each reads as absent with reason `key-lost`; environment fallbacks still resolve | The user enters keys again |
| A store write is interrupted | Temporary file, sync, rename, directory sync, under a cross-process lock; the old file stays whole | Nothing partial |
| A flow returns without committing its record | `authz/not-committed`; nothing is reported as authorised | A record the flow did write stays and is listed |
| Sign-out | Local delete must succeed; then leases close and in-flight requests end with cause `revoked`; issuer revocation is retried a bounded number of times in memory | **Uncertain:** whether the issuer still honours the old token; the UI says so |
| A command handler ignores cancellation | After the grace period the run settles `command/aborted` and the handler is tracked as detached | **Uncertain:** its side effects |
| An upload is declined or the switch is off | Feedback stays recorded locally; nothing is sent | Nothing |
| A feedback change is handed to another process and its holder dies between reading the request and appending | The request stays unapplied and expires; the next holder of the lease applies a request that has not expired, once, because a request is marked applied in the same transaction as the append | **Uncertain** until it is applied or expires; the form shows "pending" and then the outcome, never a silent loss |

## 7. Decisions

<a id="dh-adr-human-1"></a>
### DH-ADR-HUMAN-1 One interaction registry, four closed kinds, settled exactly once

- **Decision.** Every pending approval, consent, question and sign-in prompt is an item in `interact.Registry`. An item has one audience (an agent or a peer), settles once by compare-and-swap, and reports a late or foreign answer as an error that changes nothing. Pending approvals live in memory only.
- **Why.** Upstream has three delivery mechanisms and a fourth for sign-in prompts that deliberately avoided a registry (`packages/credentials/authorization/README.md:93`). Peer-addressed items keep "the page that started it" and let a reload re-attach.
- **Tradeoff.** A registry is a place where an item can leak if the asker's context never ends, so every `Ask` takes a context and the registry settles everything when it unloads. A half-typed answer is not an item's state: it stays in the browser tab that holds it and is lost with it, because a draft is not a decision and storing keystrokes on the host would add a second copy to keep consistent across tabs (a scope boundary, recorded here).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-13](../design.md#dh-o-13); applies [DH-ADR-WIRE-4](../upstream/wire-and-api.md#dh-adr-wire-4). **Verified by.** `DH-T-HUMAN-1`, `DH-E-HUMAN-2`.

<a id="dh-adr-human-2"></a>
### DH-ADR-HUMAN-2 Answerers register per channel; the creating carrier binds the agent; nothing transfers automatically

- **Decision.** The eight rules of "Who may answer" in section 6.2.
- **Why.** This is the rule [DH-ADR-WIRE-4](../upstream/wire-and-api.md#dh-adr-wire-4) leaves open and [DH-ADR-SECURITY-5](../upstream/security.md#dh-adr-security-5) (b) states. Creator-binds keeps upstream's "the SDK answers only for agents it created" and extends it to every carrier.
- **Tradeoff.** An agent whose client went away cannot be approved from another channel until an owner claims it, so unattended work stops at its first prompt. That is the fail-closed reading; automatic hand-over to "whoever is connected" would let a second client approve work it did not start.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-13](../design.md#dh-o-13). **Verified by.** `DH-T-HUMAN-2`, `DH-E-HUMAN-2`.

<a id="dh-adr-human-3"></a>
### DH-ADR-HUMAN-3 Approvals: five closed outcomes, policy before people, an attributed audit pair, no turn requirement

- **Decision.** The outcome table of section 6.2. `never` is checked inside the service before grants and answerers. `approval/decided` names the actor and any grant. A request may be made outside a turn. No decision is returned unless both records were appended; an open pair found on resume is closed.
- **Why.** The closed vocabulary is what makes a missing channel safe. `allowed-by-grant` is a separate outcome so the log shows that nobody was asked. The turn requirement existed to tell an audit event from a crash tail (`packages/interaction/user-approval/src/index.ts:245-250`), which a transactional store does not have.
- **Tradeoff.** A pending prompt is lost at a restart and its call is denied ([../design.md §5](../design.md#5-failure-retry-rollback-and-cleanup)).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-HUMAN-3`, `DH-T-HUMAN-4`, `DH-E-HUMAN-1`.

<a id="dh-adr-human-4"></a>
### DH-ADR-HUMAN-4 A session grant names a subject scope from a closed set and is matched on facts

- **Decision.** Three scope kinds, `directory`, `application` and, for browser use only, `website` ([DH-ADR-33](../design.md#dh-adr-33)), with the matching rules of section 6.2. Only an owner's answer creates a grant. The in-memory table is the authority; the log and the `grants` projection are for audit and display. A grant never allows a mode wider than the approval that created it.
- **Why.** [DH-ADR-16](../design.md#dh-adr-16) names "one application, one directory". A subject built from validated arguments cannot be forged by a reason string, and re-canonicalising a path per request stops a link swap from moving the scope.
- **Tradeoff.** A call whose asker declares no subject is asked every time. Inside a granted directory the model acts unprompted, which the grant label says. A "one named tool" kind was considered and left out: a shell tool granted by name is not a narrow scope.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-HUMAN-5`, `DH-T-HUMAN-6`, `DH-E-HUMAN-7`.

<a id="dh-adr-human-5"></a>
### DH-ADR-HUMAN-5 A grant lives and dies with the one live session it was given to and is never copied

- **Decision.** Settled by the owner on 2026-11-19 and kept when the scope was widened to one website ([DH-ADR-33](../design.md#dh-adr-33)); this is the one place the rule is written. (a) **Resume.** A grant does not survive a session resume: it ends when its host stops or its agent is disposed, and resume closes it in the log. (b) **Fork.** A grant does not pass to a fork: grant events in a fork's inherited prefix are ignored and the fork starts with none. (c) **Subagent.** A grant does not pass to a subagent, in either direction. A child's request goes to whoever answers for the root session ("Who may answer", rule 5) and is denied if nobody does; a grant given in answer to it belongs to the child's own session and ends with it. (d) **Scripts.** A nested call through the tool command line belongs to the same session, so that session's grants apply by scope, never by entry ([tools.md](tools.md)).
- **Why.** The owner chose the strict reading. Authority replayed from a log could outlive the component and the person that gave it; area A10 applies the same rule to registered write roots.
- **Tradeoff.** One more prompt after every resume, fork and delegation, and the person who answers for a root session sees the prompts of its whole tree.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-HUMAN-6`.

<a id="dh-adr-human-6"></a>
### DH-ADR-HUMAN-6 A preset bundles two knobs; switching is an owner action that is logged and revokes grants

- **Decision.** Keep the preset as a named pair with `custom` derived. The table is live configuration. Only an owner switches a permission preset or policy, through the UI, a command or the SDK; the model, a plugin and a script cannot. A compiled-in component may tighten a session, never widen it, and that change is logged with the component as its actor. A switch appends `permission/preset`, writes the knobs through their owners, revokes the session's grants and tells the model through the runtime context.
- **Why.** The enforcing services keep their own values, so removing the preset component changes nothing in force (`packages/interaction/permission-presets/README.md:28`). Revoking on a switch means a tightened session cannot keep a looser standing yes.
- **Tradeoff.** A user who switches to a wider preset and back gives their grants again. Scope boundaries of this record: a permission preset bundles exactly two knobs, the sandbox mode and the approval policy, and nothing else; the reserved `auto` preset of the experimental review layer can never be the default for new sessions, because a default must work in a composition that does not mount that layer, and `DefaultPreset` must name a preset of the table.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-HUMAN-7`.

<a id="dh-adr-human-7"></a>
### DH-ADR-HUMAN-7 One question tool with two settlements; a permission decision is never a question

- **Decision.** One `ask_user_question` schema with an optional timeout. A timeout returns "pending"; a later answer is a logged user message. The host owns the one deadline; a view may place a hold that lasts while its connection lives. Every call, including a nested one, is in the `questions` projection. The `approval` intent is removed: computer use asks through `approval.Service` with an `application` subject. The `plan-review` intent stays a question, with a typed payload and three settlements (section 6.2).
- **Why.** Two schemas made half the calls invisible to the projection (`packages/interaction/tool-ask-user/README.md:155`). A permission asked as a question bypasses policy, the audit pair and binding, and its "always" stores a rule across sessions, which [DH-ADR-16](../design.md#dh-adr-16) rules out.
- **Tradeoff.** Persistent per-application access is gone; a session grant replaces it. A timeout is never an approval, and the tool text says so. The tool declares a `When` condition and is offered only while the agent's root has a binding, so the model does not spend a turn asking when nobody can answer (Recommended default, not confirmed by the owner); a binding lost in mid-step still fails the call closed.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-HUMAN-8`, `DH-E-HUMAN-6`.

<a id="dh-adr-human-8"></a>
### DH-ADR-HUMAN-8 Commands are typed host actions that any carrier can dispatch and the model never sees

- **Decision.** Keep the registry, scoping and the logged run and done pair. A command declares a Go argument struct, from which the form, the completion and the SDK schema are derived, or raw text. Dispatch is one service call, so the UI, the command line and the SDK all reach it. A command that changes authority requires an owner.
- **Why.** Upstream commands exist only where an adapter was written, which is why `/feedback` is web-only (`packages/feedback/command-feedback/README.md:143`).
- **Tradeoff.** A handler still has to honour cancellation.
- **Serves.** [DH-O-13](../design.md#dh-o-13), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-HUMAN-9`.

<a id="dh-adr-human-9"></a>
### DH-ADR-HUMAN-9 Secrets: a redacting type, one store, a keychain-held key over an owner-only file, stored wins, never in the environment

- **Decision.** `secret.Value` and `secret.Store` as in section 6. Resolution order: the managed store, the launch environment snapshot, the project `.env`, the home `.env`; the last three are read-only. The managed store is one file under `<home>/secrets/`, encrypted with a random key (AES-256-GCM from the standard library). The key is one item in the operating-system keychain, reached through `DH-LIB-HUMANKEYRING`; where no keychain works the key is an owner-only file beside the store and `Describe` reports `protection: file`.
- **Why.** [DH-ADR-SECURITY-6](../upstream/security.md#dh-adr-security-6) asks for a keychain by default. One keychain item per secret does not hold a sign-in grant: the module's own limits are 2,560 bytes on Windows and about 3,000 on macOS (`keyring.go:13-16` of the module). An envelope gives one size-free store, one lock, one enumeration and one watched file on every system.
- **Tradeoff.** A keychain is not a boundary against an unconfined process of the same user ([../design.md §6](../design.md#6-security-model)); it stops a plain file read and, where A10 hides the secret directory and the keychain service, a confined command ([DH-ADR-SANDBOX-5](sandbox-ssh.md#dh-adr-sandbox-5)). The store therefore holds model and service keys and never an owner credential: browser sessions are hashed rows elsewhere ([DH-ADR-HOST-12](host-telemetry.md#dh-adr-host-12)). Losing the key item loses the stored values. The file is no longer hand-editable; `dsh secret set|unset|list` replaces that. The module is used on Windows and Linux. On macOS it would start `/usr/bin/security` itself with `os/exec`, so there the backend is own code that runs that program through A09's host starter as a trusted launch, and the module is not compiled in. Scope boundary of this record (`credentials.2`): a credential reference is an environment-shaped name (`DEEPSEEK_API_KEY`) in one flat namespace, never a path or a structured key; a credential that has structure, such as a sign-in grant, is a record under `<owner>/<id>` instead. This is a product choice: one namespace lets the same name be set in the launch environment, a `.env` file or the store and resolve the same way, and lets `BuildEnv` remove every name exactly.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-8](../design.md#dh-o-8). **Verified by.** `DH-T-HUMAN-10` to `-13`, `DH-E-HUMAN-3`.

<a id="dh-adr-human-10"></a>
### DH-ADR-HUMAN-10 A sign-in is an interaction addressed to whoever started it; sign-out is local first and then revokes

- **Decision.** Keep: flows keyed by the record they write, one attempt per key, `authorized` only after the commit was observed, a decline is an outcome. Change: prompts and notices are registry items addressed to the starting peer, so a reload re-attaches; a flow may declare `Revoke`; sign-out deletes the record, closes its leases, then calls `Revoke` with bounded retries held in memory; the service lists each record with whether a flow still claims it.
- **Why.** These are the three limits upstream records (`packages/credentials/authorization/README.md:148-150`). Pages open in the system browser ([DH-ADR-18](../design.md#dh-adr-18), [../libs/open.md](../libs/open.md)).
- **Tradeoff.** A host restart still abandons an attempt. A revocation that never succeeds is not retried after a restart, because that would mean keeping the old token.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-8](../design.md#dh-o-8). **Verified by.** `DH-T-HUMAN-14`, `DH-E-HUMAN-4`.

<a id="dh-adr-human-11"></a>
### DH-ADR-HUMAN-11 The vendor account: host-only release bound to an origin, no presence-ordered billing, no unasked vendor traffic

- **Decision.** Keep the system-browser sign-in with state and PKCE, local cancel as final, and release of the token only as headers for the configured inference origin with redirects refused. No surface returns the token. `account.Authorizer` replaces `deepseekAuth`: a request names the kinds its API accepts and gets the kind of the conversation's route, read from configuration and the logged request header, or else the configured default (`api-key`). It never tries a second kind. Profile and balance are fetched when the user opens the panel. Notification polling and its event reports are one sender of kind `account-notices` at area A18's gate: a setting, off by default, under the off-switch. Desktop embedding is dropped.
- **Why.** Upstream's `auto` takes whichever credential exists, so signing out silently moves a search call to the API key's bill. Polling and "displayed, clicked, dismissed" reports are data leaving the machine ([DH-ADR-9](../design.md#dh-adr-9)).
- **Tradeoff.** A user with only a sign-in must set the default kind for tools outside a vendor conversation. Vendor notices are unseen until enabled.
- **Serves.** [DH-O-8](../design.md#dh-o-8), [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-HUMAN-15`, `DH-E-HUMAN-4`.

<a id="dh-adr-human-12"></a>
### DH-ADR-HUMAN-12 The anonymous identifier does not exist until the user opts in to something that sends it

- **Decision.** A fresh home has no identifier. `identity.Current()` reads without creating. `identity.Ensure(ctx, purpose)` creates it, in one `kv` transaction, only when the named purpose is a setting the user turned on and the off-switch is not set: an authorised session upload (A06), a route's `Installation` switch ([DH-ADR-LLM-7](llm.md#dh-adr-llm-7)) or telemetry (A18). Otherwise it fails with `identity/not-enabled`. `Reset` deletes it for every process on the home at once. Recording feedback neither creates nor prints it.
- **Why.** [DH-ADR-9](../design.md#dh-adr-9). An identifier that exists is one a later change could send by mistake; one that does not exist cannot leak.
- **Tradeoff.** Records sent before and after a reset cannot be joined, by design. Scope boundary of this record (`anonymous-user-id.3`): the identifier belongs to one harness home; two homes of the same person, on one machine or two, have two identifiers and nothing links them, and the identifier is not recovered after a reset or a lost home. This is a product choice: a link between homes would need an account or a shared secret, which is exactly the identity the anonymous identifier exists to avoid.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-HUMAN-16`.

<a id="dh-adr-human-13"></a>
### DH-ADR-HUMAN-13 Feedback is recorded locally; sending is a separate step that shows what will be sent; local export is always offered

- **Decision.** Recording appends a log-only event with the actor and flushes before acknowledging. It sends nothing. When this process does not hold the session's lease (the session runs in another `dsh` on the same home), the feedback service is the first user of the request table that [session-store.md](session-store.md) adds ([DH-ADR-40](../design.md#dh-adr-40), U-K1-5): its four feedback event types are declared `Detached`, and every feedback write is one call of `session.Service.AppendLogOnly(ctx, id, drafts)` ([DH-ADR-SESSION-12](core-session.md#dh-adr-session-12)), whose drafts are the events it would have appended (record, withdraw, rate or delete, with the item's version and the actor). The core takes one of three paths and says which: an ordinary append when the session is live here; a maintenance append when nobody holds it; or, when another process holds it, one row filed with `Store.FileRequest` ([DH-ADR-STORE-12](session-store.md#dh-adr-store-12)), outcome `queued{request}`, which this service follows with `Store.WatchRequest`. The holder's core applies the row as its own group append, with the same validation and cap, and the row becomes `applied` or `refused`; a row nobody applies becomes `expired` and the user is told "not recorded: the session's host did not answer". An entry has a category and no severity, because a severity nobody triages is a field nobody trusts (a scope boundary, recorded here). The feedback form then offers: keep it local (the default), export, or send. "Send" exists only when area A06's `upload.Status()` is ready; it shows `upload.Preview` (destination, event count, bytes, time range, identity attached or "will be created", what was masked, that sent data cannot be recalled) and a link to the exact redacted records, and calls `upload.Authorise` only on confirmation. "Export" writes those same redacted records to a file the user chooses, for sharing by their own means.
- **Why.** [DH-ADR-9](../design.md#dh-adr-9), and upstream's acknowledgement that confirms recording while an upload starts ([../libs/opentelemetry.md](../libs/opentelemetry.md) §1.1). The upload itself is area A06's ([DH-ADR-VIEW-7](session-views.md#dh-adr-view-7)).
- **Tradeoff.** The publisher receives feedback only from users who take a second step. Redaction masks released values and known shapes; it does not understand content, and the preview says so.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-HUMAN-17`, `DH-T-HUMAN-18`, `DH-E-HUMAN-5`.

## 8. Deliverables

<a id="dh-d-human-1"></a>
### DH-D-HUMAN-1 A pending approval or question lives in one registry and only the bound channel can settle it

- **Result and why it matters.** Every client sees the same pending items and exactly one answer counts; with nobody bound the asker gets "no" at once.
- **How it is implemented.** A table of items guarded by one mutex, each with a state word settled by compare-and-swap and a channel the asker waits on. Registrations and bindings are cordis effects, so unloading a carrier or disposing an agent settles its items. `Pending` filters by A06's `Visibility`.
- **Components and files.** `internal/human/interact`. **Depends on.** [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3), [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4); [DH-D-AGENT-1](agent-loop.md#dh-d-agent-1) (`agent.Ref`, and the root of an agent's tree). The carriers depend on this deliverable, each with one answerer and the binding at agent creation and resume: the browser ([DH-D-API-5](api.md#dh-d-api-5): `POST /interactions/{id}`, a hold route, the `interaction` fragment), the SDK and ACP ([DH-D-SDK-6](sdk-acp-typert.md#dh-d-sdk-6), [DH-D-SDK-9](sdk-acp-typert.md#dh-d-sdk-9)) and the terminal ([DH-D-CLI-2](app-cli.md#dh-d-cli-2)).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-13](../design.md#dh-o-13). **Priority.** P0. **Done when.** `DH-T-HUMAN-1` and `DH-T-HUMAN-2` pass and [DH-E-HUMAN-2](#dh-e-human-2) has passed.

<a id="dh-d-human-2"></a>
### DH-D-HUMAN-2 An approval returns one closed outcome and leaves an attributed audit pair

- **Result and why it matters.** Stage 4 of the tool pipeline, sandbox escalation and A10's directory consent share one decision path that fails closed and records who decided.
- **How it is implemented.** `approval.Service.Request`: append asked, check policy, consult grants, ask the registry, append decided. Policy is a registered fold of `approval/policy`. A recovered panic in any step is `unavailable`. A resume hook closes open pairs. A prompt-context section states the policy to the model, as upstream.
- **Components and files.** `internal/human/approval`. **Depends on.** [DH-D-HUMAN-1](#dh-d-human-1); [DH-D-SESSION-1](core-session.md#dh-d-session-1) and [DH-D-SESSION-2](core-session.md#dh-d-session-2) (event registration, append, and `session.Created` with `How: resumed`, on which the closers for an open pair and an unended grant are appended). Stage 4 of the pipeline ([DH-D-TOOLS-2](tools.md#dh-d-tools-2)) calls this with call, parent and origin, and depends on it.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-6](../design.md#dh-o-6). **Priority.** P0. **Done when.** `DH-T-HUMAN-3` and `DH-T-HUMAN-4` pass and [DH-E-HUMAN-1](#dh-e-human-1) has passed.

<a id="dh-d-human-3"></a>
### DH-D-HUMAN-3 A user can grant one directory or one application for the session, see the grants and revoke them

- **Result and why it matters.** [DH-ADR-16](../design.md#dh-adr-16): fewer repeated prompts, with a list and an off button, and nothing that outlives the work.
- **How it is implemented.** An in-memory table per live agent; creation inside the answer path under the owner check; matching as pure functions per scope kind; `grant/created` and `grant/ended` events; the display fold; `Roots` for A10; the `/grants` command.
- **Components and files.** `internal/human/approval` (files for grants and scopes). **Depends on.** [DH-D-HUMAN-2](#dh-d-human-2), [DH-D-HUMAN-6](#dh-d-human-6); [DH-D-FS-1](filesystem.md#dh-d-fs-1) (canonical paths). Its readers depend on it: the policy resolver through `Roots` ([DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1)), and the grants panel and its routes ([DH-D-UICONV-6](ui-conversation.md#dh-d-uiconv-6), [DH-D-UISET-8](ui-settings-plugins.md#dh-d-uiset-8)).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** `DH-T-HUMAN-5` and `DH-T-HUMAN-6` pass and [DH-E-HUMAN-7](#dh-e-human-7) has passed.

<a id="dh-d-human-4"></a>
### DH-D-HUMAN-4 One Permissions choice sets sandbox mode and approval policy together

- **Result and why it matters.** A person picks "read only", "workspace" or "full access" once; new sessions start from the configured default.
- **How it is implemented.** A component that folds the two knob events and the selection into `permissions`, pins a fresh session at agent creation, switches through A10's `SetMode` and `approval.SetPolicy`, and registers `/permission`.
- **Components and files.** `internal/human/permission`. **Depends on.** [DH-D-HUMAN-2](#dh-d-human-2), [DH-D-HUMAN-3](#dh-d-human-3), [DH-D-HUMAN-6](#dh-d-human-6); [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) (`SetMode` and the `sandbox/mode` event).
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Priority.** P1. **Done when.** `DH-T-HUMAN-7` passes.

<a id="dh-d-human-5"></a>
### DH-D-HUMAN-5 The model can ask the user a question, wait or move on, and still get a late answer

- **Result and why it matters.** A question does not block work that does not depend on it, and survives a restart.
- **How it is implemented.** `question.Service` asks through the registry with a deadline; the `questions` fold reads tool calls, results and reply messages; `Reply` validates one answer per question and sends a steering user message through `agent.Send`, resuming the root agent of a closed session through the carrier. The tool is one `tools.Define` declaration.
- **Components and files.** `internal/human/question`, `internal/human/question/tool`. **Depends on.** [DH-D-HUMAN-1](#dh-d-human-1); [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2) (`Send` with a message source); [DH-D-TOOLS-1](tools.md#dh-d-tools-1) (tool declaration; nested calls are logged by [DH-D-TOOLS-4](tools.md#dh-d-tools-4)); [DH-D-VIEW-1](session-views.md#dh-d-view-1) (projection registration).
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7). **Priority.** P1. **Done when.** `DH-T-HUMAN-8` passes and [DH-E-HUMAN-6](#dh-e-human-6) has passed.

<a id="dh-d-human-6"></a>
### DH-D-HUMAN-6 A person runs a command against an agent from any client without a model turn

- **Result and why it matters.** Direct controls work in the browser, the terminal and the SDK alike and are in the log.
- **How it is implemented.** A scoped registry on area A01's layered tables; `Execute` parses the line, decodes typed arguments through `schema`, checks the actor, appends run, calls the handler with a deadline and appends done.
- **Components and files.** `internal/human/command`. **Depends on.** [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3); [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) (`schema`); [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (attachment receipts); [DH-D-SESSION-8](core-session.md#dh-d-session-8) (scoped registration tables). Each carrier adds a dispatch entry and depends on this deliverable.
- **Serves.** [DH-O-13](../design.md#dh-o-13), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** `DH-T-HUMAN-9` passes.

<a id="dh-d-human-7"></a>
### DH-D-HUMAN-7 Configuration names a secret; the value sits behind one store and cannot be printed, logged or inherited

- **Result and why it matters.** A provider gets its key per request, a rotated key reaches the next request, and no log, page or child process carries a value.
- **How it is implemented.** The `secret` contract; the environment and `.env` providers with their own strict parser; the managed file store in `file` protection mode with lock, revision and durable write; leases with a revocation signal; `Mask`; the `human.secrets.redact` listener; the source checks.
- **Components and files.** `internal/human/secret`, `secret/file`, `secret/redact`. **Depends on.** [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4); [DH-D-STORAGE-2](storage.md#dh-d-storage-2) (the home layout and permissions check); [DH-D-FS-1](filesystem.md#dh-d-fs-1) (durable write and the cross-process file lock with a wait limit); [DH-D-UTIL-3](util-test-support.md#dh-d-util-3) (`watch`). `BuildEnv` reads `References` and so depends on this deliverable ([DH-D-EXEC-8](execution.md#dh-d-exec-8)).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-8](../design.md#dh-o-8). **Priority.** P0. **Done when.** `DH-T-HUMAN-10` to `DH-T-HUMAN-12` pass.

<a id="dh-d-human-8"></a>
### DH-D-HUMAN-8 Stored secrets are encrypted under a key in the operating-system keychain, and a person can sign in and out

- **Result and why it matters.** A file read no longer yields a key, and credentials that must be obtained from a person have one flow, one prompt surface and a real sign-out.
- **How it is implemented.** The keychain backend behind a three-method interface (get, set, delete the data key); backend choice at start with the result reported; `authz.Service` with attempts as registry items; the loopback callback route; `Revoke`; the orphan join.
- **Components and files.** `internal/human/secret/keychain`, `internal/human/authz`. **Depends on.** [DH-D-HUMAN-7](#dh-d-human-7), [DH-D-HUMAN-1](#dh-d-human-1), `DH-LIB-HUMANKEYRING`, [DH-LIB-OPEN](../libs/open.md); [DH-D-API-1](api.md#dh-d-api-1) (the callback route, declared with A18's `callback` class); [DH-D-EXEC-1](execution.md#dh-d-exec-1) (the host starter, for the macOS backend). Hiding `<home>/secrets` from confined commands is [DH-D-SANDBOX-7](sandbox-ssh.md#dh-d-sandbox-7). Runtime prerequisite: a keychain service, or the fallback is reported.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-8](../design.md#dh-o-8). **Priority.** P1. **Done when.** `DH-T-HUMAN-13` and `DH-T-HUMAN-14` pass, [DH-E-HUMAN-3](#dh-e-human-3) has passed on each of the three systems, and [DH-E-HUMAN-4](#dh-e-human-4) has passed.

<a id="dh-d-human-9"></a>
### DH-D-HUMAN-9 A user can sign in to the vendor account, and an installation has an identifier only after opting in

- **Result and why it matters.** The account can be used instead of a key without the token leaving the host, and a fresh installation identifies itself to nobody.
- **How it is implemented.** The account flow registered with `authz`; `Release` and `Authorizer`; on-demand profile and balance; optional polling behind the setting; `identity.Service` over a `kv` namespace.
- **Components and files.** `internal/human/account`, `internal/human/identity`. **Depends on.** [DH-D-HUMAN-8](#dh-d-human-8); [DH-D-UTIL-2](util-test-support.md#dh-d-util-2) (`egress`); [DH-D-HOST-4](host-telemetry.md#dh-d-host-4) (the sharing gate, kind `account-notices`, and the off-switch); [DH-D-STORAGE-1](storage.md#dh-d-storage-1) (`kv`). [DH-D-LLM-5](llm.md#dh-d-llm-5) (the vendor provider, which writes the route kind and the request header record this reads from configuration and the log). Runtime prerequisite: a vendor account.
- **Serves.** [DH-O-8](../design.md#dh-o-8), [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Priority.** P1. **Done when.** `DH-T-HUMAN-15` and `DH-T-HUMAN-16` pass and [DH-E-HUMAN-4](#dh-e-human-4) has passed.

<a id="dh-d-human-10"></a>
### DH-D-HUMAN-10 Feedback is saved locally, and nothing is sent until the user has seen exactly what will be

- **Result and why it matters.** A remark or a rating is never the trigger of an upload the user did not see.
- **How it is implemented.** `feedback.Service` (record, withdraw, rate, list) over a registered fold with per-item versions; the form's three exits; the preview and export built from area A06's redacted records.
- **Components and files.** `internal/human/feedback`. **Depends on.** [DH-D-HUMAN-6](#dh-d-human-6), [DH-D-HUMAN-7](#dh-d-human-7); [DH-D-VIEW-9](session-views.md#dh-d-view-9) (`upload.Status`, `Preview`, `Authorise` and paged access to the redacted records); [DH-D-STORE-7](session-store.md#dh-d-store-7) (the export format); [DH-D-STORE-11](session-store.md#dh-d-store-11) (the request table through which a non-holder hands the holder a log-only change). The form is [DH-D-UICHAT-8](ui-chat.md#dh-d-uichat-8).
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Priority.** P1. **Done when.** `DH-T-HUMAN-17` and `DH-T-HUMAN-18` pass and [DH-E-HUMAN-5](#dh-e-human-5) has passed.

## 9. Acceptance

### 9.1 Local tests

Unit and composition tests with fakes for the store, the answerers, the keychain and the vendor. They run under the race detector.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-HUMAN-1` | `DH-D-HUMAN-1` | Two concurrent answers: one wins, the other gets `interaction/settled`; an answer after cancel and after unload changes nothing; unloading the registry settles every item; no goroutine is left |
| `DH-T-HUMAN-2` | `DH-D-HUMAN-1` | With `ui` and `sdk` answerers registered, an SDK-created agent's item cannot be settled from `ui`; an unbound agent gets `unavailable` without delivery; a child's item goes to the root's answerer and is `unavailable` when the root is unbound; an answer from the tool endpoint, the control endpoint or a plugin has no route; a claim is logged; a second answerer for one channel fails to load; a non-owner without the role is refused |
| `DH-T-HUMAN-3` | `DH-D-HUMAN-2` | Each of the five outcomes; `never` rejects with no delivery and no grant lookup; a panicking answerer and an unknown answer are `unavailable`; a timeout under virtual time; a failed append returns an error and no decision |
| `DH-T-HUMAN-4` | `DH-D-HUMAN-2` | The decided record names principal, channel and peer; a request outside a turn is accepted; a log with an open pair is closed on resume before any append |
| `DH-T-HUMAN-5` | `DH-D-HUMAN-3` | A directory grant covers the directory and paths inside it and nothing beside it, including a prefix sibling and a link that points out; an application grant matches both ids only and a process id is refused as a subject; a website grant matches one exact origin for the browser service and never a web fetch; no subject never matches; a wider mode asks |
| `DH-T-HUMAN-6` | `DH-D-HUMAN-3` | Revocation, agent disposal and a policy switch each stop a grant and log the reason; a resumed session and a fork have no grants although the log holds `grant/created`; a child agent has none of its parent's, and a grant given on a child's prompt does not apply to its parent or siblings; a non-owner, an `sdk` or `acp` answer, a plugin and the tool endpoint cannot create one; the cap is enforced |
| `DH-T-HUMAN-7` | `DH-D-HUMAN-4` | A fresh session is pinned to the default; a resumed one keeps its values; a switch writes only changed knobs, revokes grants and is refused for a non-owner; an unmatched pair reads `custom`; a default naming no preset fails the config load |
| `DH-T-HUMAN-8` | `DH-D-HUMAN-5` | A blocking call with no binding fails; a timed call returns pending at its deadline; a hold extends it and its loss restores it; a late answer becomes one user message and a second is `question/reply-queued`; a nested call is in the projection; a child agent's question is offered to the root's answerer with the child's path, and fails when the root has no binding |
| `DH-T-HUMAN-9` | `DH-D-HUMAN-6` | An unknown name logs nothing and is never a model message; a scoped command shadows a global one; typed arguments are validated; run and done are paired; an owner-only command is refused for others; a handler that ignores cancellation settles `command/aborted` |
| `DH-T-HUMAN-10` | `DH-D-HUMAN-7` | Golden file: `secret.Value` through every `fmt` verb, `encoding/json`, `log/slog` text and JSON, `text/template` and `html/template` prints `[redacted]`; parse errors of the store and of `.env` contain no value |
| `DH-T-HUMAN-11` | `DH-D-HUMAN-7` | Precedence over the four layers; a stored value wins and `Unset` reveals the fallback; an empty value is refused; a stale revision is refused; two processes modifying one record lose no write; a kill between write and rename leaves the old file; every registered remote handler is enumerated and none returns a value |
| `DH-T-HUMAN-12` | `DH-D-HUMAN-7` | The child environment built with a store holding three references contains none of them nor any `.env` entry; the process environment is unchanged after `Set`; a tool result containing a released value reaches the log masked; unsetting a reference fires the lease's signal |
| `DH-T-HUMAN-13` | `DH-D-HUMAN-8` | With a fake keychain: round trip, a missing service falls back and reports `file`, an explicit `keychain` fails closed, a lost key reads as absent with `key-lost`, a tampered file is rejected whole |
| `DH-T-HUMAN-14` | `DH-D-HUMAN-8` | One attempt per key; a decline settles `cancelled`; a flow that returns without committing is `authz/not-committed`; a reconnecting peer receives the pending prompt again; sign-out deletes, signals leases and retries `Revoke` within its bound; an orphan record is listed as such |
| `DH-T-HUMAN-15` | `DH-D-HUMAN-9` | A callback with a wrong state or after cancel stores nothing; the token is released only for the configured origin and never after a redirect; the authoriser never tries a second kind; a fresh home makes no request to a recording vendor endpoint; the off-switch stops polling |
| `DH-T-HUMAN-16` | `DH-D-HUMAN-9` | `Current` on a fresh home reports none and creates nothing; `Ensure` fails for a purpose that is off or under the off-switch; two concurrent `Ensure` calls on one database return one value; `Reset` is seen by a second store instance |
| `DH-T-HUMAN-17` | `DH-D-HUMAN-10` | Record, withdraw, rate, edit with a stale version, list; the actor is on every event; the acknowledgement follows a flush; the cap is enforced; a fork starts with no items; with a fake request table, a change from a non-holder is applied once by the holder, a refused one reports its reason, and one nobody applies expires with a message |
| `DH-T-HUMAN-18` | `DH-D-HUMAN-10` | Recording calls neither `Preview` nor `Authorise`; "send" is absent when upload is not ready; `Authorise` is called only after confirmation with the previewed value; the export's records equal the previewed records |

### 9.2 Real-environment scenarios

<a id="dh-e-human-1"></a>
#### DH-E-HUMAN-1 A real browser approves and rejects one call at a time

- **Proves.** `DH-D-HUMAN-2`. Grants are [DH-E-HUMAN-7](#dh-e-human-7), because they are a later deliverable.
- **Given.** The built `dsh` with the `web` profile on an empty home, a real model API key supplied at run time, a temporary workspace with a sibling directory `S`, preset `workspace-write`, two tabs of a real browser driven by the controller.
- **When.** 1. Prompt the model to write a file in `S`. 2. Reject in tab A. 3. Prompt again and choose "allow once" in tab B, then submit the same answer in tab A. 4. Prompt a second write in `S`.
- **Then.** 2: the file is absent and the result is `approval/rejected`. 3: the write happens; tab A is told the item was settled; the decided record names the owner and the `ui` channel. 4: a prompt appears again.
- **Cleanup.** Delete the home and the workspace. If a step failed, keep the session export.

<a id="dh-e-human-2"></a>
#### DH-E-HUMAN-2 With an SDK client and a browser connected, only the creator answers; with nobody, the answer is no

- **Proves.** `DH-D-HUMAN-1`.
- **Given.** A real SDK program that starts `dsh` on an empty home and creates a session whose one tool always asks; a browser attached to the same home through a second `dsh` web host; a real model API key.
- **When.** 1. The model calls the tool; the browser tries to answer; then the SDK program allows. 2. The SDK program stops replying and the turn is cancelled. 3. The same session is run with `dsh headless --json` and no controlling terminal (standard input, output and error redirected, run from a scheduler with no terminal). 4. It is run again with `dsh headless` in a real terminal, and the prompt there is answered "deny".
- **Then.** 1: the browser's answer is refused as not its own and the call waits; the SDK answer lets it run; the decided record names the `sdk` channel. 2: the outcome is `cancelled`. 3: no answerer is registered, the outcome is `unavailable` and the tool body never ran. 4: the `cli` answerer shows the prompt, the outcome is `rejected` with the `cli` channel, and the tool body never ran.
- **Cleanup.** Stop both hosts, delete the home.

<a id="dh-e-human-3"></a>
#### DH-E-HUMAN-3 A stored key sits behind a real keychain and reaches neither a child process nor a confined read

- **Proves.** `DH-D-HUMAN-8`. It writes and removes an item in the user's operating-system keychain, so it runs in that deliverable's build stage. The file store of `DH-D-HUMAN-7` is proved by its local tests.
- **Given.** One machine each for Windows, macOS and Linux with a desktop keychain service, and one headless Linux machine without; the built binary; an empty home; a planted value `K`.
- **When.** 1. `dsh secret set TEST_KEY` with `K` on standard input. 2. Restart and resolve it through a provider request to a local recording server. 3. Run a shell tool call that prints its environment and reads every file under `<home>/secrets`. 4. Run the same read under the sandbox.
- **Then.** 1: the keychain holds one item for the home; no file under the home contains `K`. 2: the server receives `K` in the header. 3: the output contains neither `K` nor `TEST_KEY`, and any occurrence of `K` would be masked. 4: where area A10 reports the mask, the read fails; elsewhere the result reports the directory as unmasked. On the headless machine `Describe` reports `protection: file` and step 1 warns.
- **Cleanup.** `dsh secret reset`, confirm the keychain item is gone, delete the home. If cleanup fails, record the item name.

<a id="dh-e-human-4"></a>
#### DH-E-HUMAN-4 Vendor sign-in through the system browser: cancel, reload, complete, sign out

- **Proves.** `DH-D-HUMAN-9`, `DH-D-HUMAN-8`.
- **Given.** A real vendor account operated by a person; the `web` profile on an empty home on loopback; a recording proxy that logs request hosts only.
- **When.** 1. Start a sign-in and cancel it locally, then finish the vendor page anyway. 2. Start again, reload the harness tab while the vendor page is open, then finish. 3. Open the account panel. 4. Sign out during a running turn on the account route.
- **Then.** Before step 1 the proxy saw no vendor host. 1: the home holds no grant and the late callback is refused. 2: the reloaded tab shows the pending sign-in and then "signed in"; the record exists. 3: profile and balance requests happen now and not earlier. 4: the turn ends with cause `revoked`, the record is gone, a logout request reached the vendor, and no request used an API key.
- **Cleanup.** Sign out at the vendor if step 4 did not run; delete the home.

<a id="dh-e-human-5"></a>
#### DH-E-HUMAN-5 The feedback form shows its payload before anything is sent, and offers export

- **Proves.** `DH-D-HUMAN-10`.
- **Given.** The `web` profile on an empty home, a real browser, a local recording collector, a session of several turns in which a tool printed a planted secret the store had released.
- **When.** 1. Submit feedback with default settings. 2. Choose "export". 3. Turn session upload on with the collector as destination, submit feedback, open the preview, cancel. 4. Submit again and confirm.
- **Then.** 1: the collector received nothing; the form says nothing was sent; no identifier exists. 2: the file's records have the planted value masked. 3: the preview names the collector, the event count and "identifier will be created"; nothing arrives. 4: what arrives equals the previewed records and count; the identifier now exists.
- **Cleanup.** Delete the home and the collector file.

<a id="dh-e-human-6"></a>
#### DH-E-HUMAN-6 A timed question returns pending, and its late answer becomes a user message after a restart

- **Proves.** `DH-D-HUMAN-5`.
- **Given.** A real model API, the `web` profile, a prompt that makes the model ask one question with a ten-second timeout and continue other work.
- **When.** Let the deadline pass; kill the host; start it; open the session in a browser and answer.
- **Then.** The tool result is pending and the turn continued; after the restart the question is listed as continued; the answer appears as one user message naming the call and starts a turn; a second answer is refused.
- **Cleanup.** Delete the home.

<a id="dh-e-human-7"></a>
#### DH-E-HUMAN-7 A real browser grants a directory for the session, lists it, revokes it, and finds nothing after a restart

- **Proves.** `DH-D-HUMAN-3`.
- **Given.** The setup of [DH-E-HUMAN-1](#dh-e-human-1): the `web` profile on an empty home, a real model API key supplied at run time, a temporary workspace with a sibling directory `S`, preset `workspace-write`, two tabs of a real browser.
- **When.** 1. Prompt the model to write a file in `S` and choose "allow for this session" in tab B. 2. Prompt a second write in `S` and one outside it. 3. Open the grants panel and revoke. 4. Prompt a third write in `S`. 5. Stop the host, start it, resume the session and prompt once more.
- **Then.** 1: the write happens; the log has `grant/created` naming the owner and the `ui` channel. 2: the write in `S` shows no prompt and is `allowed-by-grant`; the other prompts. 3: the panel lists one grant, then none, and the log has `grant/ended` with `revoked`. 4 and 5: a prompt appears each time.
- **Cleanup.** Delete the home and the workspace. If a step failed, keep the session export.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed. The scenarios show the stated contracts for the tested build, inputs and machines only.

## 10. Open questions and limitations

**Decided by the owner.** A grant may name one website for browser use only, and web fetch asks every time; a grant is never copied ([DH-ADR-33](../design.md#dh-adr-33)). **Recommended defaults, not confirmed by the owner.** (1) An `sdk` client cannot create a session grant; only a person in the UI or the terminal can. The ACP option of kind `allow_always` is therefore not offered; if the owner allows it, its label is "for this session". (2) A vendor account request names the client as the bare word `dsh`; the client version is added only when the user turned the version on, as for every model request ([DH-ADR-32](../design.md#dh-adr-32)). The user's language is sent only during a sign-in the user started, so that the vendor page is shown in it, and never otherwise.

**Settled with other areas.** A stored key is tried with the on-demand route check of [DH-D-LLM-4](llm.md#dh-d-llm-4), and a route's kind comes from `llm.Runtime.Routes()`; this area adds no "test this key" call. Closers for an open approval pair and an unended grant are appended on `session.Created` with `How: resumed` ([core-session.md](core-session.md) §6.2), before any agent accepts work; the cross-process lock is `fsx` ([DH-D-FS-1](filesystem.md#dh-d-fs-1)). The feedback form pages the redacted records through `upload.Uploads.Records`, and the store hands released values to A06's baseline redactor through `Store.Mask`, which prints nothing. Computer use (X4) declares `application` subjects from validated arguments, and its stored permission list has no counterpart. Each kind's payload is a Go struct in `interact` (`ApprovalPayload`, `ConsentPayload`, `QuestionPayload` with its optional plan text, `AuthorizationPayload`), and `interact.Changed` names the actor of a settlement, for "answered by". `account.Service` offers `Notices(ctx)` and `ReportNotice(ctx, id, event)`; the poll interval is the config field `NotificationsInterval` beside `Notifications`. A command line is `/name` followed by raw text or by the fields of the command's argument struct, and a command's title is a message-catalogue key (C1). A compiled-in component may tighten a session (a stricter mode or policy, logged with the actor `component:<name>`), for example an automatic reviewer that unloads; only an owner widens.

**Unverified.** Whether `github.com/zalando/go-keyring` works with cgo off on all three targets was checked by reading (no file imports `C`), not by building. The vendor's allowed redirect addresses, required client headers and business codes are not in the pinned source. **Carried limits** are listed by group in section 5. They are owned by stage `DH-S-47` of [../implementation-plan.md](../implementation-plan.md#dh-s-47), except the vendor's business-code wording, which waits for the vendor's list and is owned by `DH-S-35`.

**An upstream inconsistency, for the record.** Upstream's preset README lists two default presets (`packages/interaction/permission-presets/README.md:49`) while the base bundle ships three (`packages/bundle/base/cordis.patch.yml:271-275`); this design follows the bundle.

## 11. Units dispositioned by this spec

13 packages (11 ported, 2 merged), 54 tech-debt units (34 resolved, 19 carried, 1 not applicable), 1 tool (ported, changed), 11 events (7 ported, 4 replaced), 6 config schemas (ported), 10 services (7 ported, 3 merged), and 8 decision notes (5 ported, 3 replaced). Go items: 13 decisions, 10 deliverables, 18 tests, 6 scenarios, 1 library (`DH-LIB-HUMANKEYRING`: `github.com/zalando/go-keyring` v0.2.8, MIT, verified with `go list -m` and the `LICENSE` file in the module cache on 2026-11-18). Rows are in `trace/fragments/A17.jsonl`.
