# harness:DSH-M3 -- Architecture: the web UI, a first working harness

> **Story.** "clone deepseek harness, given context in docs\harness of this repo, and
> design and impl doc, i want you to follow impl. This story is milestone M3 of dsh
> (impl.md stages DH-S-19 to DH-S-22), following harness:DSH-M1. Its tech spec is
> docs/stories/harness-DSH-M3/tech-spec.md."
>
> **Authority.** [`docs/harness/impl.md`](../../harness/impl.md) sec. 3.3 (milestone M3),
> [DH-S-19](../../harness/impl.md#dh-s-19) to [DH-S-22](../../harness/impl.md#dh-s-22),
> sec. 7.1 (environments E1, E5) and sec. 10.3 (held open H-5, H-6, H-8, H-9, H-22);
> [`docs/harness/design.md`](../../harness/design.md) sec. 4.2 (package layout),
> DH-O-12 (live HTML UI) and DH-ADR-39 (supported browsers); the area specifications
> [api.md](../../harness/areas/api.md) (A19), [ui-core.md](../../harness/areas/ui-core.md) (C1),
> [ui-kit.md](../../harness/areas/ui-kit.md) (C2), [ui-chat.md](../../harness/areas/ui-chat.md) (C3),
> [ui-conversation.md](../../harness/areas/ui-conversation.md) (C4),
> [ui-sidebar-workspace.md](../../harness/areas/ui-sidebar-workspace.md) (C6),
> [app-web.md](../../harness/areas/app-web.md), [host-telemetry.md](../../harness/areas/host-telemetry.md) (A18);
> the library specifications [go-ui-stack.md](../../harness/libs/go-ui-stack.md),
> [playwright.md](../../harness/libs/playwright.md) and [micromark.md](../../harness/libs/micromark.md).
> Where this document and those disagree, impl.md and the area specification win and
> this document is corrected.
>
> **Sibling plan documents** (written in parallel; this document does not repeat them):
> [tech-spec.md](tech-spec.md) (requirements, decisions, environments),
> [implementation-plan.md](implementation-plan.md) (step order and commits),
> [e2e-scenarios.md](e2e-scenarios.md) (scenario texts and run schedule).
> The milestones before this one are described in
> [harness-DSH-M1/architecture.md](../harness-DSH-M1/architecture.md) (C01 to C30) and
> [harness-DSH-M2/architecture.md](../harness-DSH-M2/architecture.md) (C31 to C57);
> component ids here continue that numbering (M3 adds C58 to C86).

---

## 0. Situation report

| Item | State at the start of M3 |
|---|---|
| What M2 left | `dsh headless` and `dsh --profile sdk` run real turns with tools, approvals and the Linux sandbox. The `web` profile (`boot/profile/bundles/web.yml`) already mounts the host plane: `host.listen`, `storage.db`, `host.auth`, `host.assets`, `host.web`, `host.launch`, `host.access`, `host.stream`, `ui.shell`. `dsh web` boots it, prints or opens a one-time sign-in link, writes the runtime record and is reused by a second `dsh web`. **It serves no page a person can use**: the route table is the interim declared-effect table of step 15.4, access is a constant owner-only stub, the stream hub is a topic table with no connection, and the shell is a region registry with no page |
| What M3 adds | The first product a person can use without a terminal: `dsh web` serves a signed-in page with the session list, a chat composer, a live transcript that streams the reply, tool calls as cards with honest states, approvals and questions answered inline, session grants offered, listed and revoked, a directory chooser to add a workspace, in English or Chinese, light or dark, keyboard operable; and a keyless real-browser lane that proves it in CI |
| What M3 proves | S19, S20 and S21 close on gate rows (in-process, golden, ephemeral services), the browser-lane row, and their guarded scenarios (step 19.7: DH-E-STORAGE-1; step 21.13: DH-E-HUMAN-2, DH-E-BOOT-2; real signed-in model where the scenario requires one). S22 runs 20 real-browser scenarios in E1 and E5; steps 22.1 to 22.11 can close on automated Chromium evidence, while step 22.12 (Firefox, Safari) stays open until automated evidence exists, so **S22 and M3 are not claimed closed by this plan** (R-06) |
| What M3 enables | **Nothing for users.** "The team uses `dsh web` on Linux" (impl.md sec. 3.3). Every P0 deliverable is built; the held-open P0 parts and the 44 deliverables whose last scenario runs later (stopping: DH-E-CLI-1/-2 in DH-S-27; every macOS and Windows behaviour) stay disabled |
| Entry prerequisite | M2 closed (DH-S-17, DH-S-18 records `closed`; the 16.8 guarded batch passed). Today `test/harness/acceptance-index.yaml` says `built-through: DH-S-11`; see R-01 |
| Owner decisions in force | `platforms-windows-wsl` (gate lanes Windows native and Linux under WSL; macOS out of scope), `models-signed-in-claude-copilot` (E1 is the signed-in Claude Code or Copilot CLI route; no API key), `go-floor-1-27-4`; DH-ADR-39 (Chromium-family fully supported; current Firefox and Safari for the engine-sensitive set) |

---

## 1. Scope

### 1.1 Stages and deliverables

| Stage | Theme | Deliverables built (P0 unless marked) | Closes on |
|---|---|---|---|
| [DH-S-19](../../harness/impl.md#dh-s-19) | Routes, access, event stream, conversation API | API-2, API-3, API-4, API-5 (H-5, H-6 held open), HOST-8, API-6 (P1, pulled forward); completes the interim contract of API-1 (step 15.4) | DH-T-API-1..-7, -9, -11..-21, -25, -26; DH-T-HOST for dirbrowse; guarded DH-E-STORAGE-1 (step 19.7); manual SSE-reconnect and forgery checks |
| [DH-S-20](../../harness/impl.md#dh-s-20) | UI shell and design system | UICORE-7, UICORE-1, UICORE-3, UICORE-4, HOST-10 (P1, pulled forward; H-9 held open), UICORE-2, UICORE-5 (P1), UICORE-6 (P1), UIKIT-1, UIKIT-2, UIKIT-3 (P1; H-22 held open), UIKIT-4, UIKIT-5, UIKIT-9, UIKIT-10 | DH-T-UICORE-1..-13, -16; DH-T-UIKIT-1..-12, -18..-21; the gallery golden files; the single-CSP-writer check; manual gallery walk in Chromium (Firefox and Safari as a written checklist) |
| [DH-S-21](../../harness/impl.md#dh-s-21) | Chat, transcript, approvals, session list, assembled product | UICONV-2, UICHAT-1, UICHAT-6, UICONV-1 (H-8 held open), UICONV-3, UICONV-4, UICONV-6, UISIDE-1, UISIDE-8, WEBAPP-2, WEBAPP-1, WEBAPP-6 | DH-T-UICONV-*, DH-T-UICHAT-1/-7/-8/-13/-14, DH-T-UISIDE-1..-3/-17, DH-T-WEBAPP-1..-4/-12; keyless browser lane passing on a clean runner; guarded DH-E-HUMAN-2, DH-E-BOOT-2 (step 21.13); the open list for M3 published |
| [DH-S-22](../../harness/impl.md#dh-s-22) | Acceptance of the web UI in real browsers | none built | 20 scenarios: WEBAPP-5 (first); HOST-1, HOST-2, API-2; BOOT-1; API-1, GOUISTACK-1, GOUISTACK-2; UICORE-1, -2, -3; UIKIT-1, -2; UICONV-1, UICHAT-5; HUMAN-1, -6, -7; BUNDLE-3; UIKIT-5 and the engine-sensitive repeats (last) |

M3 is reached when S22 is closed and the guarded scenarios of steps 19.7 and 21.13 have
passed (impl.md). Step 22.12 stays open while its Firefox and Safari runs are `manual`
(impl.md DH-S-22 "Manual checks"), so this plan delivers "22.1 to 22.11 passed, 22.12
open" and does **not** claim S22 or M3 closed until qualifying automated Firefox and
Safari evidence exists (Test & Environment Contract, TC-25m; R-06).

### 1.2 Held open at M3

| Id | Deliverable built | What waits | Closing step | Effect on this architecture |
|---|---|---|---|---|
| H-5 | API-5 (`session.create`, `session.model`) | The model catalogue (LLM-4) | 23.13 | `PUT /sessions/{sid}/model` accepts only the one provider's models that M1/M2 routes offer; the picker is a single-provider list |
| H-6 | API-5 (`session.complete`) | The skill catalogue (CONTEXT-9) | 25.8 | `completions/skill` answers an empty candidate list; `file`, `session`, `command` work |
| H-8 | UICONV-1 | The user-text view with chips and masked credentials (UICHAT-5) | 41.10 | A sent user message is drawn by C79 as plain escaped text (`kit.Data`), never through C76's Markdown path |
| H-9 | HOST-10 | Plugin rows of the inventory (EXT-4) | 34.8 | C69 lists compiled-in components only |
| H-22 | UIKIT-3 | Appearance controls in the General settings section (UISET-2) | 43.2 | C73's appearance preference is set from the header menu and the gallery only |

### 1.3 Out of scope

- Jobs, settings, terminals and files routes (API-7, API-8, API-9; P1, DH-S-24 onward),
  the WebSocket (`host/wsconn`), and `DH-E-API-3`/`-4`/`-5` (API-5 scenario runs in
  DH-S-48 with the jobs routes of DH-S-40).
- Non-owner principals (API-10, P1): no policy is registered in M3, so every
  non-owner is denied with `access/no-policy`; the `RegisterKind`/`RegisterPolicy`
  surface exists but carries no policy.
- `ui/setup`, `ui/product` routes (WEBAPP-3, -4, -5, -10), the web app manifest
  (WEBAPP-4), the budget gate (WEBAPP-7, `test/bench/web`).
- Scenarios authored in S19 to S21 whose run belongs later: DH-E-UICONV-2 (DH-S-48),
  DH-E-UICONV-4 (DH-S-39), DH-E-UICONV-5 (DH-S-32), DH-E-API-5 (DH-S-48).
- macOS rows (decision `platforms-windows-wsl`); Safari therefore has no M3 machine
  (Q `m3-firefox-safari`).

---

## 2. Starting point in the repository

Names in this table are taken from the tree at the start of the story.

| Existing package or file | State today | What M3 does with it |
|---|---|---|
| `src/harness/internal/host/web` (`web.go`, `component.go`; component `host.web`, `web.Key` = `*Routes`) | Interim contract of step 15.4: `Route{Name, Method, Pattern, Effect(Read/Change/Stream), Params, Serve http.Handler}`; atomic table swap; unload cancels handlers; `Streams(req)` for the listener | C58 replaces `Effect`/`Params`/`Serve` with the full `Route{Access, Activation, Kind, Policy, Form, Limits, Handle}` of api.md sec. 6.1, the closed `Response` set, the header writer, the CSP composer and the forgery check. The swap, the group lifetime and `Streams` are kept as written |
| `src/harness/internal/host/access` (`access.go`, `component.go`) | `Principal{Kind, ID}` with `IsOwner()`, `KindOwner`, `Owner()` (returns the owner `Principal`), `Resource{Kind, ID}`, `Decision`, `ReasonNoPolicy`; `ownerOnly` service (`Decide`, `Allows`) | C59 adds `Rule` (`Owner`, `On`, `Admitted`, `Public`), `From` (`Session`, `Workspace`, `Opaque`), `Filter`, `RegisterKind`, `RegisterPolicy`, `Invalidate` and `access.Changed`; the owner-only decision stays the only path. `Principal` keeps its shape; the principal constructor is renamed `OwnerPrincipal()` so `Owner()` can be the rule (sec. 4.2) |
| `src/harness/internal/host/stream/stream.go` | `Source{Topic, Class(State/Notice)}`, `Hub{Register, Topics}`; no connection | C60 builds connections, tabs, interests, the journal class over `query.Follow`, parking, attention, `SetRenderer`; `Source` gains `Access` and `Open` |
| `src/harness/internal/ui/shell/shell.go` | `Region{Name, Kind, Doc}`, `Contribution{Region, ID, Order}`, `Declare`, `Contribute`, `Epoch` (owner effects; region-instance-safe cleanup) | C67 adds `Env`, `Page`, `Render`, `Island`, `Touch`, phases, keyed scope, the layout and `#ui-state`. The effect and epoch mechanics are kept |
| `src/harness/internal/host/assets` (`/assets/`, embedded FS, hashed immutable names, ETag) | Holds `files/favicon.svg` only | Stays the mount; C68 supplies the `ui/assets` tree (htmx 2.0.11 vendored, stream script, island loader, stylesheet, sprites) and its manifest |
| `src/harness/internal/host/{listen,auth,launch,diag}` | Listener with admission, `listen.Streaming(ctx)`, `PeerFrom`; `auth.Service` (`Mint`, `Issue`, `SignOut`, `RevokeAll`); runtime record and control endpoint | Used unchanged. `Peer.FormToken()` (HOST-2) is the forgery check's token source |
| `src/harness/internal/cli` (`cli.go:115` registers `web`) and `cli/hostclient` | `dsh web` starts the host and opens the link | C84 adds only the composition and order assertions (WEBAPP-1); no new command |
| `src/harness/internal/boot/profile/bundles/web.yml`, golden `profile/testdata/composed.golden` | Mounts the nine host-plane entries; the comment lists the entries later deliverables add | Each M3 component adds its entry in the same change; `composed.golden` is regenerated in that step and reviewed as a diff |
| `src/harness/internal/session/query` (`Observe`, `List`, `ListByProject`, `As(Caller)`, `Follow` -> `*Follower`) | Built in M1/M2 | The journal source of C60 and the reads of C61, C62, C82 |
| `src/harness/internal/human` (`Registry`: `Ask`, `Answer`, `Pending`, `PendingAll`, `RegisterAnswerer(ch)`), `human/approval/grants.go` (`Grants.List`, `Match`, `Revoke`, `EndAll`) | Built in M1/M2; SDK and CLI answerers registered | C81 registers the `ui` channel answerer and renders `Pending`; grant strip over `Grants.List`/`Revoke` |
| `src/harness/internal/agent` (`events.go`: `Created`, `Disposed`, `StatusChanged`, `Failed`, `StepDecided`; `Inbox.Send`) | **No `agent.StreamFrame` notice exists** (grep of `src/harness/internal/agent` finds no stream-frame type) | C65 adds the observe-only frame notice that api.md DH-D-API-4 and ui-conversation.md DH-D-UICONV-3 depend on (Q `m3-streamframe-gap`) |
| `src/harness/internal/storage/workspace` (`Registry.Add`, `List`) | Built in M1/M2 | C64 routes over it; `workspace.Changed` re-renders the `shell` source |
| `src/harness/internal/testkit/{model,dshbin,launcher,temphome,golden,faultserver,egresstest,scenario}`, `testkit/scenarios/*_scenario_test.go` (`//go:build scenario`) | `host_signin_scenario_test.go` (DH-E-HOST-1), `host_network_scenario_test.go` (HOST-2), `boot_first_page_scenario_test.go` (BOOT-1), `boot_two_process_scenario_test.go` (BOOT-2), `human_question_scenario_test.go` (HUMAN-6), `bundle_egress_scenario_test.go` (BUNDLE-3) exist and drive "the browser" through **operator-step commands** (`operatorStep`, `operatorJSON`, `DSH_SCENARIO_*` inputs) | C85 adds a real driver so these become automated; the operator-step form stays only for the Firefox/Safari checklist |
| `src/harness/go.mod` | No browser driver, no Markdown or sanitizer module | Adds `github.com/chromedp/chromedp` (test-only import, DH-ADR-PLAYWRIGHT-1), `github.com/yuin/goldmark` and `github.com/microcosm-cc/bluemonday` (product, micromark.md), each with the go.mod comment convention |
| `src/harness/internal/archtest` | Import-rule tests | Gains the M3 rules of sec. 3.4 |
| `test/harness/acceptance-index.yaml` (entries under `DH-S-19`, `DH-S-21`, `DH-S-22` already exist for STORAGE-1, BOOT-2, BOOT-1, HUMAN-6, HOST-1, HOST-2, BUNDLE-3), `tools/harness-gate` (`lanes/win.ps1`, `lanes/wsl.sh`) | No browser preflight | C86 adds the missing DH-S-22 entries and a Chromium preflight |
| `src/harness/test/e2e/harness-DSH-M2` (godog) | M2's Gherkin suites | M3's suites go to `src/harness/test/e2e/harness-DSH-M3` |

---

## 3. Components

### 3.1 Package map

All paths are under `src/harness/`. `(new)` marks packages M3 creates; `(ext)` marks
existing packages M3 extends.

```
internal/
  agent/                      (ext)  C65 StreamFrame notice (observe-only)
  host/
    web/                      (ext)  C58 Route, Request, Response, header writer, CSP, forgery
    access/                   (ext)  C59 Rule, From, Filter, kinds, policies (none registered)
    stream/                   (ext)  C60 Hub, Tab, Interest, connections, journal class, park
    conversation/             (new)  C61 Commands{Create, Prompt, Continue, Fork}
    routes/
      sessions/               (new)  C62 session.* routes
      workspaces/             (new)  C64 workspace.*, directory.*, interaction.answer, grant.*
    dirbrowse/                (new)  C63 Browser contract, Window, roots
      local/                  (new)  C63 dirbrowse.local (os filesystem)
    inventory/                (new)  C69 host.inventory, inventory.Changed
  ui/                         (new)  C67 Req, SafeHTML, View[T], Register, Render
    msg/                      (new)  C66 Catalog (en, zh), keys, T()
    shell/                    (ext)  C67 Env, Page, Render, Island, Touch, phases, layout
    assets/                   (new)  C68 embedded tree, manifest, stream script, island loader
    prefs/                    (new)  C70 Store, Pref[T]
    keys/                     (new)  C71 Registry, Command, keymap, #ui-keys
    kit/                      (new)  C72 Text, Action, Button, Field, List, Data ... (Validate)
    frame/ dock/ menu/ keyhelp/ (new) C72 widgets over kit
    theme/                    (new)  C73 Appearance, Themes, CSS layers
    brand/                    (new)  C74 mark, product name, favicon set
    gallery/                  (new)  C75 /gallery page (dev and test only)
    render/                   (new)  C76 ui.render: goldmark -> bluemonday -> FromSanitizer
    composer/                 (new)  C77 Draft, Drafts
    chat/                     (new)  C78 Row, Planner
    transcript/               (new)  C79 NodeDef, Service, stream.Renderer
      live/                   (new)  C79 per-attempt live tail state
    toolcard/                 (new)  C80 Facts, Card, state machine
    interaction/              (new)  C81 inline approvals, questions, grant strip, ui answerer
    nav/                      (new)  C82 live session list, reading.Record
    dirpick/                  (new)  C83 directory chooser island
    product/                  (new)  C84 composition checks, embedded-asset audit (tests)
  boot/profile/bundles/web.yml (ext) C84 one entry per new component
  testkit/
    web/                      (new)  C85 web.Start(t, opts): built dsh, temp home, replay, port 0
    browser/                  (new)  C85 the only chromedp importer; Chromium provider
    scenarios/                (ext)  C86 browser-driven DH-S-22 scenarios (//go:build scenario)
  archtest/                   (ext)  sec. 3.4 rules
test/e2e/harness-DSH-M3/      (new)  godog suites mirroring e2e-scenarios.md
```

### 3.2 Process picture

```
 browser tab (Chromium automated; Firefox and Safari = step 22.12, open)
   |  GET /            HTML page (server-rendered, works without script)
   |  GET /events?tab=                    stream for a tab that shows no session
   |  GET /sessions/{sid}/live?after=&tab= stream for a tab that shows a session
   |  hx-get fragments + X-Dsh-Tab header  -> server-side rq.Tab().Watch(...)
   |  POST /sessions/{sid}/prompt   hx-post + _token, idempotent request id
   v
 +--------------------------- dsh web (one process, profile "web") ----------------+
 | host.listen  -- admission (A18), TLS, Peer, FormToken --+                        |
 |                                                         v                        |
 | host.web (C58): match -> forgery -> access (C59) -> form -> activation -> Handle |
 |    |  Response{View|NoContent|Go|Problem|Bytes|Hijack} -> header writer + CSP    |
 |    |                                                                            |
 |    +-- routes/sessions (C62) --> host.conversation (C61) --> agent.Inbox.Send    |
 |    +-- routes/workspaces (C64) --> storage.workspace, dirbrowse.local (C63),     |
 |    |                               human.Registry.Answer, approval.Grants       |
 |    +-- ui pages: ui.shell (C67) + views (C72..C83) rendered via ui.Render        |
 |    +-- /events, /sessions/{sid}/live  --> host.stream (C60)                       |
 |                        | journal class: session/query.Follow(sid, cursor)        |
 |                        | state class:   Source.Open(sub) -> Replace/Notify       |
 |                        | live notices:  agent.StreamFrame (C65)                  |
 |                        +-- Renderer = ui/transcript (C79) -> SSE named events    |
 | host.assets  /assets/<hash>  (ui/assets tree C68, immutable)                     |
 | agent runtime, human.Registry (ui answerer C81), session store (sqlite)          |
 +---------------------------------------------------------------------------------+
            | provider call (replay layer in the keyless lane; signed-in route in E1)
            v
       model route
```

### 3.3 Component table

| Id | Component (package) | Deliverable | Responsibility | Depends on | Stage |
|---|---|---|---|---|---|
| C58 | `host.web` (`internal/host/web`) | API-2 (+ completes API-1) | The full route contract; one request pipeline; the only response header writer; CSP composed at table swap; forgery check; strict form decode; Problem rendering with request id | host.listen, host.auth, host.access, ui (for View) | S19 |
| C59 | `host.access` (`internal/host/access`) | API-3 | Every route's `Rule`; resolves `From` to a `Resource`; owner-only decision; `Filter` for list routes; `access.Changed` when a policy or kind changes | host.listen (Principal), storage.workspace, session.query | S19 |
| C60 | `host.stream` (`internal/host/stream`) | API-4 | One SSE connection per tab; tabs and interests (memory only); journal class pulled from `query.Follow`; state and notice sources; live tail from C65; resume by Last-Event-ID or `after`; reset; parking at StreamsPerPeer; reconnect burst 429; attention stream; renderer fallback | host.web, host.access, session.query, agent (C65) | S19 |
| C61 | `host.conversation` (`internal/host/conversation`) | API-5 | `Commands{Create, Prompt, Continue, Fork}`; blank-session reuse under lease; resume before prompt (`MayResume`); request id = message id; first receipt on `agent/duplicate-message`; the `continuation` fold | agent (Inbox), session.store, session.query, boot.agentpreset, session.title | S19 |
| C62 | `host.routes.sessions` (`internal/host/routes/sessions`) | API-5 | `session.list/create/open/history/follow/prompt/cancel/queue/rename/model/fork/delete/upload/attachment/complete` (sec. 5.3); `session.queue` is `PUT`/`DELETE /sessions/{sid}/inbox/{mid}` with `MayResume` | C58, C59, C60, C61 | S19 |
| C63 | `dirbrowse`, `dirbrowse.local` (`internal/host/dirbrowse`, `.../local`) | HOST-8 | Bounded, sorted window over a directory under configured roots; create a directory; no symlink escape; typed errors | os filesystem | S19 |
| C64 | `host.routes.workspaces` (`internal/host/routes/workspaces`) | API-6 (P1) | Workspace routes (`GET`/`POST /workspaces`, `POST /workspaces/default`, `PUT /workspaces/{wid}/title`, `DELETE /workspaces/{wid}`, `PUT /workspaces/order`, `PUT /workspaces/{wid}/sessions/order`); session archive and pin (`POST`/`DELETE /sessions/{sid}/archive` with 409 on running work or `stop=1` to stop it first; `PUT`/`DELETE /sessions/{sid}/pin`); `directory.browse/create`; `interaction.answer`; `grant.list/revoke` | C58, C59, C63, storage.workspace, human.Registry, approval.Grants | S19 |
| C65 | `agent.StreamFrame` (`internal/agent`) | pulled-forward part of AGENT-1 (DH-D-AGENT-1) | Observe-only notice `StreamFrame{Session, Turn, Attempt, Kind(Start/Chunk/End), Seq, Text, Reasoning}` from the driver; never journaled; dropped when nobody listens | agent driver | S19 |
| C66 | `ui.msg` (`internal/ui/msg`) | UICORE-7 | Message catalogue (en, zh); typed keys; `T(req, key, args)`; missing-key test | -- | S20 |
| C67 | `ui`, `ui.shell` (`internal/ui`, `internal/ui/shell`) | UICORE-1, UICORE-2 | `ui.Req` resolved on the host; `SafeHTML` (constructors only from registered views and `FromSanitizer`); `View[T]` registry; buffered `Render`; shell layout with landmarks, regions, contributions, phases, keyed scope, `#ui-state` epoch, islands | C66, host.web, host.stream | S20 |
| C68 | `ui.assets` (`internal/ui/assets`) | UICORE-3, UICORE-4 | Embedded asset tree; manifest (source, version, licence, hash); htmx 2.0.11 vendored with eval off; the stream script on `EventSource` (seven connection states, park, resume, reset); island loader (custom elements, ES modules, import map hashed into CSP) | host.assets, C67 | S20 |
| C69 | `host.inventory` (`internal/host/inventory`) | HOST-10 (P1; H-9) | Lists the composed components, routes, regions, sources; publishes `inventory.Changed` | boot, host.web, ui.shell | S20 |
| C70 | `ui.prefs` (`internal/ui/prefs`) | UICORE-5 (P1) | Per-principal preference store; `Pref[T]` typed handles; change re-renders through `shell` source | storage.db, C67 | S20 |
| C71 | `ui.keys` (`internal/ui/keys`) | UICORE-6 (P1) | Command registry and keymap; `#ui-keys` JSON for the stream script; conflict detection | C67 | S20 |
| C72 | `ui.kit` + `ui/frame`, `ui/dock`, `ui/menu`, `ui/keyhelp` | UIKIT-1, UIKIT-4, UIKIT-5 | Typed controls with `Validate`; accessible names; focus order; dialogs and menus with keyboard; dock layout `Intent.Apply` | C67, C66 | S20 |
| C73 | `ui.theme` (`internal/ui/theme`) | UIKIT-2, UIKIT-3 (P1; H-22) | Tokens and layers (reset, tokens, base, kit, feature, user); light, dark, system through `light-dark()`; Appearance preference | C68, C70 | S20 |
| C74 | `ui.brand` (`internal/ui/brand`) | UIKIT-9 | Product mark, name, favicon set | C68 | S20 |
| C75 | `ui.gallery` (`internal/ui/gallery`) | UIKIT-10 | `/gallery` page drawing every kit control in every state; the golden source | C72, C73 | S20 |
| C76 | `ui.render` (`internal/ui/render`) | UICONV-2 | The sole sanitizer: goldmark (+ passthrough for math) -> bluemonday allow-list -> `ui.FromSanitizer` (adds `hx-disable`); strips `id`, `style`, `hx-*`, `data-*`, `on*`, `script`, `form`, `iframe`, bad URL schemes | goldmark, bluemonday, C67 | S21 |
| C77 | `ui.composer` (`internal/ui/composer`) | UICHAT-1 | Draft persistence with `Rev` (stale save -> 409); attachments; the request id minted per send | C67, C72, C62 | S21 |
| C78 | `ui.chat` (`internal/ui/chat`) | UICHAT-6 | Row planner: turns, groups, hidden rows; stable positions for streamed inserts | C79 | S21 |
| C79 | `ui.transcript`, `ui/transcript/live` | UICONV-1 (H-8), UICONV-3 | Node definitions and the `transcript` projection keyed by `(session, node key)`; `Page`/`Body` for first paint; implements `stream.Renderer` (`Events`, `Live`); the live tail from C65; page-vs-stream equivalence | C60, C65, C76, C67 | S21 |
| C80 | `ui.toolcard` (`internal/ui/toolcard`) | UICONV-4 | Tool call facts to one card per call; honest state machine (sec. 4.6) | C79 | S21 |
| C81 | `ui.interaction` (`internal/ui/interaction`) | UICONV-6 | Inline approvals and questions from `Registry.Pending`; registers the `ui` answerer; "Allow once" default; "Allow for this session" for one directory, app or origin; grant strip; `grant/created`, `grant/ended` notices; late-question card and the `question.read`/`question.reply` routes delegating to `question.Service.Reply`; wires `question.Service.Restore` into session resume | human.Registry, human/question, approval.Grants, C64, C79 | S21 |
| C82 | `ui.nav` (`internal/ui/nav`) | UISIDE-1 | Live session list in `#nav`; status precedence Pending > Running > Unread; `reading.Record` | session.query, C60, C67 | S21 |
| C83 | `ui.dirpick` (`internal/ui/dirpick`) | UISIDE-8 | Directory chooser over `directory.browse/create`; add workspace | C63, C64, C72 | S21 |
| C84 | web product (`web.yml` entries, `internal/ui/product` tests) | WEBAPP-1, WEBAPP-2 | The composed `web` profile equals the inventory; readiness before the URL is printed; every embedded file referenced, manifest complete, total under the byte budget | boot.profile, C68, C69 | S21 |
| C85 | keyless browser lane (`internal/testkit/web`, `internal/testkit/browser`) | WEBAPP-6 | `web.Start(t, opts)`: built dsh, temp home, `--port 0`, `--no-open`, replay layer in place of the provider, fresh browser profile; tripwires (console error, leftover recording, unexpected reset) | testkit.dshbin, testkit.model, chromedp | S21 |
| C86 | acceptance plumbing (`testkit/scenarios`, `acceptance-index.yaml`, `tools/harness-gate`) | S22 (no deliverable) | Browser-driven scenario files; index entries; Chromium preflight in the lanes; the M3 open list | C85 | S22 |

### 3.4 Boundary rules

Each rule is an `archtest` check or a named test; the test id is given.

1. **One sanitizer.** Only `ui/render` imports `bluemonday` and only `ui/render` calls
   `ui.FromSanitizer` (archtest; DH-T-UICONV for the corpus).
2. **SafeHTML has no public constructor.** No package outside `ui` builds `SafeHTML`
   from a string; `html/template.HTML` conversions are forbidden outside `ui`
   (archtest; DH-T-UICORE-1).
3. **One header writer.** Only `host/web` sets `Content-Security-Policy`,
   `X-Content-Type-Options`, `Referrer-Policy`, COOP, CORP, `Permissions-Policy`
   (archtest grep for the header names; the single-CSP-writer check of S20).
4. **Every route declares access.** `Routes.Handle` rejects a route whose `Access` is
   the zero `Rule` (DH-T-API-2); `Public` requires a class and a reason.
5. **One writer per region.** A region has one owner component; a second `Contribute`
   with kind `One` fails at load (DH-T-UICORE).
6. **The stream renders through the renderer only.** `host/stream` never imports
   `ui/*` packages other than through `SetRenderer` (archtest).
7. **Feature packages import the host contracts, not each other.** `routes/sessions`
   and `routes/workspaces` do not import each other; `ui/*` feature packages do not
   import `host/routes/*` (archtest).
8. **No Node, no bundler.** No `package.json`, no generated JS; `ui/assets` files are
   committed sources with manifest entries (WEBAPP-2 test DH-T-WEBAPP-3).
9. **chromedp only in tests.** `github.com/chromedp/*` is imported only by
   `internal/testkit/browser` (DH-LIB-PLAYWRIGHT-R6; archtest), and the product
   binary's `go list -deps` does not contain it (DH-T-WEBAPP-4).
10. **The browser is the only client** (DH-ADR-GOUISTACK-9). No JSON API is added for
    the UI; fragments are HTML; `Accept: application/json` is answered only for Problems.

## 4. Data model

Only the `continuation` fold, drafts, preferences, reading records and the
`transcript` projection are persisted (sqlite, under the profile's `storage.db`).
Tabs, interests, connections and live-tail state are memory only and are rebuilt
from the cursor after a restart.

### 4.1 Request and response (C58)

| Entity | Key fields | Notes |
|---|---|---|
| `web.Route` | `Name` (dotted, unique), `Method`, `Pattern` (Go 1.22 mux syntax), `Access access.Rule`, `Activation` (`None` / `LiveOnly` / `MayResume`), `Kind` (`Page` / `Fragment` / `List` / `Action` / `Download` / `Upload` / `Stream` / `Socket` / `Frame`), `Policy` (frame policy for `Frame`), `Form` (schema: field name -> type, required, max), `Limits` (`MaxFormBytes`, `HandlerTimeout` overrides), `Handle func(*Request) Response` | Replaces the interim `Effect`, `Params`, `Serve`. `Kind` implies the method rule: `Page`/`List`/`Fragment`/`Download`/`Stream` are GET; `Action`/`Upload` are POST, PUT or DELETE and require the forgery check |
| `web.Request` | `Context`, `Principal access.Principal`, `Session` (resolved `{sid}`), `Param(name)`, `Filter access.Filter`, `Tab() stream.Tab` (from the tab header or the `tab=` query), `Env ui.Req`, `NewRequestID()`, `Peer listen.Peer`, decoded form | Built by the pipeline; a handler never sees an undecoded body |
| `web.Response` | closed set: `View(view, data)`, `NoContent()`, `Go(url)` (303 or `HX-Location`), `Problem(code, detail)`, `Bytes(type, r)`, `Hijack(fn)` | Unexported interface method keeps the set closed |
| `web.Problem` | `Status`, `Code` (for example `web/forgery`, `access/denied`, `session/writer-held`), `Detail` (catalogue key), `RequestID`, `RetryAfter` | Rendered as a fragment into `#notices` for htmx, as a page for navigation, as `application/problem+json` for `Accept: application/json` |
| `web.Config` | `Heartbeat 15s`, `WriteTimeout 10s`, `MaxAge 30m`, `ReplayLimit 2000`, `PageEvents 100`, `StreamsPerPeer 4`, `InterestsPerTab 128`, `TabGrace 2m`, `ReconnectBurst 10/10s`, `ReconnectGiveUp 8`, `LiveMinInterval 50ms`, `MaxFormBytes 1MiB`, `HandlerTimeout 60s` | Defaults of api.md sec. 6.3 |

### 4.2 Access (C59)

| Entity | Key fields | Notes |
|---|---|---|
| `access.Principal` (existing, unchanged) | `Kind string` (`KindOwner` = `"owner"` is built in and has no `ID`; other kinds match `[a-z][a-z0-9-]*`), `ID string`; method `IsOwner()` | Put on every admitted peer by host.auth (`auth.go`), read by host.listen and host.web through `Peer.Principal()`. Same shape as api.md sec. 6.1 `Principal{Kind, ID}` |
| `access.Resource` | `Kind` (`session`, `workspace`, `directory`, `interaction`, `grant`), `ID` | Existing type |
| `access.Rule` | one of `Owner()`, `On(from ...From)` (none = global), `Admitted()`, `Public(class, reason)` (class is A18's `anonymous` or `callback`) | Zero value is rejected at `Handle` |
| `access.From` | `Session(param)`, `Workspace(param)`, `Opaque(param)` | Resolves the resource from the request; an `Opaque` param is readable only through `rq.Param` |
| `access.Decision` | `Allowed`, `Reason` (`access/owner`, `access/no-policy`, ...) | Denial -> 403 `access/denied` |

**Principal migration (explicit).** Today `access.Owner()` returns the owner
`Principal`; api.md sec. 6.1 gives the name `Owner()` to the owner-only `Rule`. C59
renames the existing constructor to `access.OwnerPrincipal()` and updates its callers in
the same change: `host/auth/auth.go` (`a.create(..., access.Owner(), ...)`) and the test
fakes in `host/auth/auth_test.go`, `host/web/web_test.go`, `host/listen/listen_test.go`,
`host/access/component_test.go`. `Principal{Kind, ID}`, `KindOwner` and `IsOwner()` keep
their shape, so host.auth's cookie model and host.listen's `Peer` are otherwise
unchanged. `Service.Decide(ctx, p, action, rs)` and `Allows(ctx, p, action, r)` keep
their M2 signatures; `Invalidate(p *Principal)` (raises `access.Changed`) is added.

### 4.3 Stream (C60)

| Entity | Key fields | Notes |
|---|---|---|
| `stream.Source` | `Topic`, `Class` (`Journal` / `State` / `Notice`), `Access access.Rule`, `Open(sub) error` | Existing `Source` gains `Access`, `Open`, the `Journal` class |
| `stream.Subscription` | `Replace(region, render)`, `Notify(event)` | Given to `Open`; one writer per region |
| `stream.Tab` | `ID` (minted into the page layout at first render), `Principal`, `Interests` (max `InterestsPerTab` 128), `LastStreamClosed`, `Parked bool` | Memory only; dropped `TabGrace` 2m after its last stream closed. An unknown tab id on a stream gets `reset` first |
| `stream.Interest` | `Topic`, `Key` (for example a directory or a roster) | Added **server side** when a fragment route renders a live region and calls `rq.Tab().Watch(topic, key)`; removed by a collapse/close request calling `Unwatch`. The browser never calls `Watch` |
| tab header | `X-Dsh-Tab: <tab id>` (name proposed, I-6) | Put by the page layout on every htmx request (`hx-headers` on `<body>`) so a fragment request reaches its `stream.Tab`; the same id is the `tab=` query of the stream URL |
| `stream.Connection` | `Tab`, `Peer`, `Started`, `Writer` (flusher), `Heartbeat` timer | Max `StreamsPerPeer` (4) session and shell streams per peer on plain HTTP (unlimited under TLS); one more sends `park` to the longest-open stream and closes it; the attention stream counts but is never parked |
| `stream.Cursor` | `Session`, `Seq` | Event id on the wire is `<sid>:<seq>`; the page carries it in `#stream-cursor` |
| `stream.Event` | `Name` (`session`, `live`, `interaction`, `shell`, `view`, `notice`, `reset`, `park`, `attention`), `ID`, `Data SafeHTML` (OOB fragments) | Named SSE events carrying HTML (DH-ADR-GOUISTACK-4) |
| `stream.Renderer` | `Events(env, []Event) SafeHTML`, `Live(env, sid, live.TailPosition) SafeHTML` | Set once by C79 via `Hub.SetRenderer`; fallback placeholder on error never stalls the cursor |

### 4.4 Conversation (C61, C65)

| Entity | Key fields | Notes |
|---|---|---|
| `conversation.Receipt` | `Session`, `Message` (= request id), `Seq`, `Duplicate bool` | A repeat with the same request id returns the first receipt (`agent/duplicate-message`) |
| `continuation` fold (persisted) | `Session`, `Workspace`, `Preset`, `Model`, `LastSeq`, `Blank bool`, `Lease` | The only persisted state C61 owns; blank-session reuse under lease |
| `agent.StreamFrame` (C65) | `Session`, `Turn`, `Attempt`, `Kind` (`Start` / `Chunk` / `End`), `Seq` (per attempt), `Text`, `Reasoning` | Observe-only, never journaled; the settled message comes from the journal |
| `conversation.Config` | `PageMinMessages 50`, `PageMinTurns 2`, `PageMaxMessages 500` | History paging |

### 4.5 UI core (C66 to C75)

| Entity | Key fields | Notes |
|---|---|---|
| `ui.Req` | `Lang` (`en`/`zh`), `Dir`, `Zone`, `Theme`, `Principal`, `Tab`, `Platform`, `Partial bool` | Resolved on the host (DH-ADR-GOUISTACK-8) |
| `ui.View[T]` | `Name`, template, data type `T` | Registered once; `Render` buffers and writes nothing on error |
| `shell.Region` | `Name`, `Kind` (`One` / `List` / `Keyed` / `Wrap`), `Phases`, `Doc` | Landmarks: `#notices #nav #header #main #aside #dialogs #announce #ui-state #ui-keys` |
| `shell.Contribution` | `Region`, `ID`, `Key`, `Phase`, `Order`, `Priority`, `Match`, `Render` | Removed with the owner's effects; bumps `Epoch` |
| `shell.Island` | `Tag` (custom element), `Module`, `Stylesheet`, `Policy web.FramePolicy`, `Fallback` | Module hash enters the import map and CSP at table swap |
| `shell.Page` | `Title`, `Main`, `Session`, `Cursor` | Input to `shell.Render` |
| `assets.Entry` | `Path`, `Hash`, `Bytes`, `Source`, `Version`, `Licence` | Manifest row; WEBAPP-2 audits it |
| `msg` key | `Key`, `en`, `zh` | Missing translation fails DH-T-UICORE |
| `prefs.Pref[T]` | `Name`, default, `Principal` scope | Appearance, density, keymap |
| `keys.Command` | `ID`, `Label` key, `Default` chord, `Scope` | Emitted as `#ui-keys` JSON |
| `theme.Appearance` | `light` / `dark` / `system` | CSS `light-dark()` |
| `inventory.Entry` | `Component`, `Kind` (route / region / source / island), `Name`, `Bundle` | Plugin rows held open (H-9) |

### 4.6 Conversation UI (C76 to C83)

| Entity | Key fields | Notes |
|---|---|---|
| `transcript.NodeDef` | `Kind`, `Types` (event types folded), `Place`, `Match`, `Fold`, `View`, `Body` | Registered by C79, C80, C81 |
| `transcript` projection row (persisted) | `Session`, `NodeKey`, `Kind`, `Seq`, `Turn`, `Body` (folded state) | Same rows serve the page and the stream (equivalence test) |
| `live.TailPosition` (internal) | `Session`, `Turn`, `Attempt`, `FrameSeq` | **Not a wire cursor.** Orders `StreamFrame` chunks (C65) inside one model attempt so C60 can coalesce them and `Renderer.Live` can draw the tail; it lives in memory only and is reset when the attempt settles. It never appears in a URL, in `Last-Event-ID` or in `#stream-cursor`. The only cursor a client sends is `stream.Cursor` (journal `Seq`, see sec. 4.3): `?after=` on `/sessions/{sid}/live` and the `<sid>:<seq>` event id. On reconnect the tail is rebuilt from the current attempt's state baseline, never replayed by `FrameSeq` |
| question item | `Call` (opaque in the URL), `ID` (question id from `question/asked`), `Session`, `Text`, `Choices`, `State` (`open` / `pending` (timed out, still answerable) / `answered`) | From the `questions` fold over `question/asked` and `question/answered`; `{call}` resolves to `ID` through `Asked.Call` |
| `toolcard.Facts` | `Call`, `Parent`, `Name`, `Args`, `State`, `Result`, `Cwd`, `Home` | `Card{Call, Result, Body}` |
| tool card state | `preparing` -> `waiting-approval` -> `running` -> `finished` / `failed` / `denied` / `cancelled`; `not-started`; `outcome-unknown` (host restarted while running) | No state is drawn that the journal has not recorded |
| interaction item | `ID` (iid), `Session`, `Kind` (`approval` / `question`), `Subject`, `Options`, `Default` ("Allow once") | From `human.Registry.Pending` |
| grant | `ID`, `Session`, `Scope` (one directory, app or origin), `Created`, `Ended`, `Reason` | Per live session; never copied by resume, fork or subagent |
| `composer.Draft` (persisted) | `Session`, `Text`, `Attachments`, `RequestID`, `Rev` | Stale `Rev` -> 409 |
| `chat.Row` | `Seq`, `Turn`, `Pos`, `Kind`, `Hidden`, `Group` | Planner output |
| `reading.Record` (persisted) | `Principal`, `Session`, `Seq` | Unread status in the list |

### 4.7 Test artefacts (C85, C86)

| Entity | Key fields | Notes |
|---|---|---|
| `web.Options` (testkit) | `Binary`, `Home`, `Replay` (recording dir), `Browser`, `Profile`, `Env` | `web.Start(t, opts)` returns `{URL, SignIn, Browser, Stop}` |
| tripwire record | `ConsoleErrors`, `UnusedRecordings`, `Resets` | Any non-empty field fails the test |
| scenario artefacts | screenshots, HAR-like request log, `dsh` logs, journal dump | Written under the test's temp dir; uploaded by CI on failure |

---

## 5. Interfaces

### 5.1 Go contracts (shape, not final signatures)

```go
// host/web (C58)
type Routes interface {
    Handle(owner *cordis.Context, rs ...Route) error // atomic swap; rejects zero Access
    List() []RouteInfo
    Streams(r *http.Request) bool
}
type Response interface{ isResponse() }
func View[T any](v ui.View[T], data T) Response
func Problem(status int, code, detail string) Response

// host/access (C59)
type Service interface { // existing Decide and Allows keep their M2 signatures
    Decide(ctx context.Context, p Principal, action string, rs []Resource) Decision
    Allows(ctx context.Context, p Principal, action string, r Resource) bool
    Filter(ctx context.Context, p Principal, kind string) Filter
    RegisterKind(k Kind) error
    RegisterPolicy(p Policy) error // none registered in M3
    Invalidate(p *Principal)       // raises access.Changed
}
func Owner() Rule                // was the Principal constructor; now OwnerPrincipal()
func OwnerPrincipal() Principal

// host/stream (C60)
type Hub interface {
    Register(owner *cordis.Context, s Source) error
    SetRenderer(r Renderer)
    Serve(w http.ResponseWriter, req *web.Request) // GET /events, /sessions/{sid}/live, /events/attention
}
type Tab interface { // reached only through web.Request.Tab() inside a server-side handler
    ID() string
    Watch(topic, key string) // a fragment route that renders a live region
    Unwatch(topic, key string)
}

// host/conversation (C61)
type Commands interface {
    Create(ctx context.Context, in CreateIn) (session.ID, error)
    Prompt(ctx context.Context, sid session.ID, in PromptIn) (Receipt, error)
    Continue(ctx context.Context, sid session.ID) (Receipt, error)
    Fork(ctx context.Context, sid session.ID, at event.Seq) (session.ID, error)
}

// dirbrowse (C63)
type Browser interface {
    List(ctx context.Context, path string, w Window) (Listing, error)
    Create(ctx context.Context, parent, name string) (string, error)
    Roots() []string
}

// testkit/web (C85)
func Start(t testing.TB, o Options) *Instance
```

### 5.2 Interface table

| From | To | Interface | Carries |
|---|---|---|---|
| host.listen | C58 | `http.Handler` + `listen.PeerFrom` | Admitted request, peer, form token |
| C58 | C59 | `access.Service.Decide` | Principal, route name as action, resources resolved from the rule's `From` -> decision |
| C58 | handlers (C62, C64, ui pages) | `Route.Handle(*Request) Response` | Decoded form, resolved session, `ui.Req` |
| C62 | C61 | `conversation.Commands` | Prompt text, attachments, request id |
| C61 | agent | `agent.Inbox.Send`, resume | Message; `agent/duplicate-message` |
| C60 | session.query | `Service.Follow(ctx, sid, from)` -> `*Follower` | Journal records from the cursor |
| agent driver | C60 | `agent.StreamFrame` notice (C65) | Live text chunks |
| C60 | C79 | `stream.Renderer` | Events -> OOB HTML fragments |
| C79, C80, C81 | C76 | `render.Markdown(env, text) SafeHTML` | Model text |
| C81 | human | `Registry.RegisterAnswerer("ui")`, `Pending`, `Answer` | Live interaction answers (`interaction.answer`) |
| C81 | human/question | `question.Service.Reply(ctx, id, by human.Actor, text) (agent.Receipt, error)`; `Open() []string` | Late answers (`question.reply`): one steering user message through the inbox; errors `question/reply-queued`, `question/unknown`, `question/invalid` |
| C62 resume hook | human/question | `question.Service.Restore(ctx, log, inbox) ([]string, error)` | Reopens every `question/asked` without `question/answered` when a session resumes; today only tests call it, so C81 wires it into the MayResume path |
| C64 | approval | `Grants.List`, `Grants.Revoke` | Grants per live session |
| C64, C83 | C63 | `dirbrowse.Browser` | Directory windows |
| C67 | C60 | `shell` state source | Region re-render after `Epoch` change |
| C84 | boot.profile | `composed.golden`, inventory | Composition equality |
| C85 | chromedp | `browser.New(ctx, profile)` | DevTools protocol over a pipe |

### 5.3 HTTP routes added in M3

Patterns, names and activations follow api.md sec. 6.2; where api.md gives no name
(workspace routes, archive, pin) the name here is proposed and marked `*` (I-6).
Access notation is api.md's: `Owner()`; `On(from...)` with no `From` meaning a global
resource; `S` = `On(Session("sid"))`; `W` = `On(Workspace("wid"))`.

| Name | Method and pattern | Kind | Access | Activation |
|---|---|---|---|---|
| `home`* | `GET /` | Page | `Owner()` | None |
| `session.list` | `GET /sessions?q=` | List | `On()` (global) + `rq.Filter()` | None |
| `session.create` | `POST /sessions` (workspace, directory, preset, model, request id; workspace is a checked typed field) | Action | `On()` (global) | None |
| `session.open` | `GET /sessions/{sid}` | Page | `S` | None |
| `session.history` | `GET /sessions/{sid}/events?before={seq}` (`at=`, `around=`) | Fragment | `S` | None |
| `session.follow` | `GET /sessions/{sid}/live?after={seq}&tab={tab}` | Stream | `S` | None |
| `session.prompt` | `POST /sessions/{sid}/prompt` (multipart; `continue=1`) | Action | `S` | MayResume |
| `session.cancel` | `POST /sessions/{sid}/cancel` | Action | `S` | LiveOnly |
| `session.queue` | `PUT /sessions/{sid}/inbox/{mid}` (edit a queued message or move it to steering), `DELETE /sessions/{sid}/inbox/{mid}` (withdraw it) | Action | `On(Session("sid"), Opaque("mid"))`: `{mid}` is opaque, read only through `rq.Param("mid")`, and must name a queued message of that session | MayResume |
| `session.rename` | `PUT /sessions/{sid}/title` | Action | `S` | MayResume |
| `session.model` | `PUT /sessions/{sid}/model` (H-5) | Action | `S` | MayResume |
| `session.fork` | `POST /sessions/{sid}/fork` (`seq`, default last completed turn) | Action | `S` | None |
| `session.delete` | `DELETE /sessions/{sid}` | Action | `S` (owner only) | None |
| `session.upload` | `POST /sessions/{sid}/uploads` | Upload | `S` | None |
| `session.attachment` | `GET /sessions/{sid}/attachments/{ref}` | Download | `On(Session("sid"), Opaque("ref"))` (`ref` referenced by the log or the caller's draft) | None |
| `session.complete` | `GET /sessions/{sid}/completions/{kind}?q=` (H-6) | Fragment | `S` | None (never wakes a session) |
| `session.archive`* | `POST /sessions/{sid}/archive` (archive; with running work answers 409 listing that work; with `stop=1` stops the work first, then archives), `DELETE /sessions/{sid}/archive` (unarchive) | Action | `Owner()` | None |
| `session.pin`* | `PUT /sessions/{sid}/pin`, `DELETE /sessions/{sid}/pin` | Action | `Owner()` | None |
| `stream.events`* | `GET /events?tab={tab}` (a tab that shows no session) | Stream | `Owner()` | None |
| `stream.attention`* | `GET /events/attention` | Stream | `Owner()` | None |
| `workspace.list`*, `workspace.add`* | `GET /workspaces`, `POST /workspaces` | List, Action | `On()` (global) | None |
| `workspace.default`* | `POST /workspaces/default` (first-use workspace) | Action | `On()` (global) | None |
| `workspace.title`* | `PUT /workspaces/{wid}/title` | Action | `W` | None |
| `workspace.remove`* | `DELETE /workspaces/{wid}` | Action | `W` | None |
| `workspace.order`* | `PUT /workspaces/order` | Action | `On()` (global) | None |
| `workspace.sessions-order`* | `PUT /workspaces/{wid}/sessions/order` (session ids are checked typed fields) | Action | `W` | None |
| `directory.browse` | `GET /directories?path=` | Fragment | `Owner()` | None |
| `directory.create` | `POST /directories` | Action | `Owner()` | None |
| `interaction.answer`* | `POST /interactions/{iid}` (A17's four kinds and `plan-review`) | Action | `On(Opaque("iid"))` | None (answers reach the live registry; a settled item answers 409). A timed question past its deadline is no longer in the registry: its card posts to `question.reply` instead |
| `question.read` | `GET /sessions/{sid}/questions/{call}` (ui-conversation.md sec. 6.2) | Fragment | `On(Session("sid"), Opaque("call"))` | None (read-only review of an open, pending or settled question) |
| `question.reply` | `POST /sessions/{sid}/questions/{call}` (form: `text`, request id) | Action | `On(Session("sid"), Opaque("call"))` | MayResume (resumes a closed session's root agent, then calls `question.Service.Reply`) |
| `grant.list`, `grant.revoke` | `GET /sessions/{sid}/grants`, `DELETE /sessions/{sid}/grants/{gid}` (area C4) | Fragment, Action | `S`; revoke adds `Opaque("gid")` | None |
| `gallery`* | `GET /gallery` | Page | `Owner()` | None (mounted only with `ui.gallery`) |

`/assets/` and `/auth/` stay A18's routes (registered in M2) and are migrated to the
full `Route` by C58 without changing their patterns.

Every response carries the header set of api.md sec. 6.3: CSP (`ui.BasePolicy` plus
the import-map and island hashes, built at table swap, no nonce),
`X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, COOP and CORP
`same-origin`, `Permissions-Policy` denying every feature.

Error mapping: 400 `web/bad-request`; 401 and 403 from admission (A18); 403
`access/denied`; 403 `web/forgery`; 404 (including `question/unknown`); 409
(`session/writer-held`, stale draft `Rev`, `question/reply-queued` for a second
answer); 422 also for `question/invalid` (empty answer); 413 and 422 (form limits and schema); 429 and 503 with `Retry-After`; 500
`web/internal` with the request id.

### 5.4 SSE events

| Event | Id | Payload | Sent when |
|---|---|---|---|
| `session` | `<sid>:<seq>` | OOB fragments for the folded transcript nodes | A journal record past the cursor |
| `live` | none | Replace of the live tail region | `StreamFrame` (rate-limited by `LiveMinInterval`) |
| `interaction` | none | Inline approval or question | `Registry` pending set changes |
| `shell` | none | Region replace or `#ui-state` epoch | Contributions, prefs, workspaces change |
| `view` | none | A state-class source replace | Source `Replace` |
| `notice` | none | `#notices` item | Notice-class sources, grant notices |
| `reset` | none | Instruction to refetch | Cursor older than `ReplayLimit`, epoch change |
| `park` | none | Park notice | The tab is parked (StreamsPerPeer) |
| `attention` | none | Count and target | On `/events/attention` only |

### 5.5 CLI surface

No new command. `dsh web [--port N] [--no-open] [--profile web]` is the M2 command;
M3 changes what it serves. The keyless lane calls the built binary as
`dsh web --port 0 --no-open` with `DSH_HOME` pointing at a temp home and the replay
layer selected by the test configuration (C85).

---

## 6. Flows

Each flow names the stage that builds it and the scenario or test that proves it.

### F1 First launch and sign-in (S19, S21; DH-E-HOST-1, DH-E-BOOT-1)

1. The operator runs `dsh web`. Boot composes the `web` profile; C84's check
   (DH-T-WEBAPP-1) has already asserted the profile equals the inventory (C69).
2. `host.launch` waits until every route group is registered (C58 swap), the asset
   manifest is loaded (C68) and the stream hub accepts sources (C60). **Only then** is
   the URL printed (readiness before the URL; DH-T-WEBAPP-2).
3. The browser opens the one-time link; `host.auth` sets the cookie and answers 303 to
   `/`.
4. `home` (C58 -> C59 `Owner()` -> handler) renders `shell.Page` through C67. The page
   is complete HTML without script: session list (C82), empty main with the composer
   (C77), `#ui-state` with the epoch. The layout mints a tab id and puts it on
   `<body hx-headers>` as `X-Dsh-Tab`, so every later htmx request names the tab.
5. The header writer adds the CSP composed at the last table swap. Because this page
   shows no session, the stream script (C68) opens `GET /events?tab=<id>`.

### F2 Open a session and connect the stream (S19, S21; DH-E-API-1, DH-E-UICONV-1)

1. `GET /sessions/{sid}` (a navigation, or an htmx swap carrying `X-Dsh-Tab`): C59
   resolves `Session("sid")`; C79 `Page` reads the `transcript` projection and the
   state regions at one cut (at least `PageMinMessages` across `PageMinTurns`, at most
   `PageMaxMessages`) and writes that cut into `#stream-cursor`.
2. Interests are registered **on the server, during rendering**: each fragment that
   draws a live region (for example the grants strip or an open directory) calls
   `rq.Tab().Watch(topic, key)` inside its handler. The browser never sends a
   "watch" message; `EventSource` is server-to-client only.
3. Because the page shows a session, the stream script closes any `/events` stream
   of this tab and opens `GET /sessions/{sid}/live?after=<seq from #stream-cursor>&tab=<id>`
   (`session.follow`, access `S`). A tab id the hub does not know gets `reset` first.
4. C60 runs the connect order: check the rule; attach to every source (**listeners
   first**); send the state baselines (`live` tail, `interaction`, `shell`, `view` for
   the tab's interests); then journal records after the cursor via `store.Follow`
   (pulled, `PageEvents` per write); then live changes. Nothing between the page read
   and the attach is lost.
5. Each journal batch goes through `Renderer.Events` (C79) to OOB fragments; the event
   id `<sid>:<seq>` advances only after the write completed.
6. Collapsing a region is an htmx request that calls `rq.Tab().Unwatch(topic, key)`.
   Leaving the session for a page without one switches the tab back to `/events`.

### F3 Send a prompt, live tail, settle (S19, S21; DH-E-UICONV-1, DH-E-HUMAN-1)

1. The composer (C77) posts `POST /sessions/{sid}/prompt` with `_token` and a request
   id minted at draft time.
2. C58: same-origin check and form token (else 403 `web/forgery`), C59 rule, strict
   decode against the form schema, activation `MayResume` (resume a parked session
   first).
3. C62 calls `Commands.Prompt`; C61 sends with the request id as message id. A repeat
   (double click, retry after cut) returns the first receipt; the UI shows one message.
4. The driver emits `StreamFrame` Start/Chunk/End (C65). C60 forwards to
   `Renderer.Live` at most every `LiveMinInterval`; the `live` event replaces the
   tail region.
5. The settled assistant message arrives as a journal record; **the settled message
   and the emptied tail ship in one `session` event** so the text never shows twice.
6. The draft is cleared with its `Rev`; the session row in `#nav` (C82) moves to
   `Running` then back.

### F4 Tool card, inline approval, session grant (S21; DH-E-HUMAN-1, DH-E-HUMAN-7)

1. A tool call is journaled; C80 folds `Facts` into a card in `preparing`.
2. The approval rule asks `human.Registry.Ask`; the `ui` answerer (C81) is the
   registered channel. An `interaction` event draws the inline approval with
   "Allow once" focused.
3. The operator chooses "Allow for this session" for one directory. `POST
   /interactions/{iid}` (`interaction.answer`, activation None, forgery-checked; a
   settled item answers 409) -> `Registry.Answer` -> the grant
   is created; a `grant/created` notice goes into the transcript; the card moves to
   `running` then `finished`.
4. `GET /sessions/{sid}/grants` lists it; `DELETE .../grants/{gid}` revokes it
   (`Grants.Revoke`); `grant/ended` is journaled. Resume, fork and subagents start
   with no grant.

### F4b Timed question, restart, late answer (S21, S22; DH-E-HUMAN-6, DH-D-HUMAN-5)

1. The model calls the ask-user tool with a ten-second timeout and `Wait` false.
   `question.Service.Ask` journals `question/asked{ID, Call}` and asks through
   `Registry.Ask`; the `ui` answerer (C81) draws an inline question card via an
   `interaction` event. The tool result is **pending** at once and the turn carries on.
2. The deadline passes: the service withdraws the registry interaction. The card
   re-renders from the `questions` fold as `pending` (still answerable) and its form
   now targets `POST /sessions/{sid}/questions/{call}`. It no longer targets
   `/interactions/{iid}`, because that route serves only live registry items and
   would answer 409.
3. The host is killed and started again. Registry state is memory only, so nothing
   about the question survives there. The journal still holds `question/asked`
   with no `question/answered`.
4. The operator opens `GET /sessions/{sid}`. C79 renders the transcript; the
   `questions` fold lists the question as continued (pending).
   `GET /sessions/{sid}/questions/{call}` (`question.read`) returns the card.
5. The operator answers: `POST /sessions/{sid}/questions/{call}` (`question.reply`;
   forgery check, access `On(Session("sid"), Opaque("call"))`). Activation
   **MayResume** resumes the session's root agent. The resume hook runs
   `question.Service.Restore(ctx, log, inbox)`, which reopens the question. C81
   resolves `{call}` to the question `ID` via `Asked.Call` and calls
   `question.Service.Reply(ctx, id, actor, text)`.
6. `Reply` journals `question/answered` and sends **one** steering user message
   (`agent.InboxMessage{ID: "ans-<id>", Source: SourceQuestion, Call}`), which
   names the call and starts a turn. That message reaches the transcript through
   the normal journal stream, so the browser draws exactly one user message.
7. A second answer, from the same tab or another, gets `question/reply-queued` ->
   409, and the card shows the settled answer. A `{call}` the session never asked
   gets `question/unknown` -> 404. The registry answer route is never used for a
   restored question.
8. Proof: TC-13s (in-tree route to `Reply` on the ephemeral services) and TC-24s
   (real model and real browser, DH-E-HUMAN-6).

### F5 Reload, network cut, host restart (S19, S21; DH-E-API-1, DH-E-UICONV-1, DH-E-GOUISTACK-2)

1. **Reload.** The page re-renders from the projection with a new cursor; no event is
   replayed twice.
2. **Network cut.** `EventSource` reconnects with `Last-Event-ID`; C60 resumes from
   that cursor if it is within `ReplayLimit`, else sends `reset` and the script
   refetches the page. More than `ReconnectBurst` per window -> 429 with
   `Retry-After`; after `ReconnectGiveUp` the display state is `stalled`.
3. **Host restart.** Tab records are memory only, so the hub does not know the tab id:
   the reconnecting stream gets `reset` first, every live region re-requests itself
   (re-registering its interests server side), and the session page reconnects
   `/sessions/{sid}/live?after=<cursor>`; `store.Follow` gives every missed record. A
   card that was `running` when the host died shows `outcome-unknown`.
4. **Seven tabs** (GOUISTACK-2): a stream beyond `StreamsPerPeer` (4 on plain HTTP)
   sends `park` to the longest-open one and closes it; showing that tab again
   reconnects and resumes from its cursor.

### F6 Add a workspace, create, archive and unarchive a session (S19, S21; DH-E-STORAGE-1, DT-UISIDE)

1. The chooser island (C83) asks `GET /directories?path=` -> C63 window under the
   configured roots (bounded, sorted; no escape by `..` or symlink).
2. `POST /directories` creates a folder; `POST /workspaces` adds it
   (`storage.workspace.Registry.Add`); the `shell` source re-renders `#nav`.
3. `POST /sessions` with the workspace reuses a blank session under lease or creates
   one; 303 (or `HX-Location`) to `/sessions/{sid}`.
4. **Archive.** `POST /sessions/{sid}/archive` on an idle session archives it; on a
   session with running work it answers **409** listing that work; the row offers
   "stop and archive", which posts again with `stop=1`: the host cancels the work,
   then archives. `DELETE /sessions/{sid}/archive` **unarchives**. `PUT`/`DELETE
   /sessions/{sid}/pin` pin and unpin. Each change re-renders `#nav` through `shell`.
   DH-E-STORAGE-1 drives this lifecycle from web host A while command-line process B
   runs a session on the same home (TC-07b).

### F7 Hostile model output stays inert (S21; DH-T-UICONV corpus, DH-E-UICONV-1)

1. Model text with `<script>`, `onerror=`, `javascript:` links, `hx-post`, `data-*`,
   forms and iframes reaches C79.
2. C76 renders Markdown, sanitizes with the allow-list and wraps the subtree with
   `hx-disable`; no CSP violation is reported (the tripwire in C85 counts console
   errors including CSP reports).

### F8 Feature unload removes routes and UI (S19, S20; DH-T-API, DH-T-UICORE)

1. A component's owner context is disposed. Its route group leaves the table in one
   swap (in-flight handlers are cancelled), its sources unregister (connections get a
   `shell` update), its contributions leave (epoch bump -> `dsh:reset` in open tabs),
   its island hashes leave the CSP at the swap.

### F9 Keyless browser lane (S21; DH-T-WEBAPP-12, DH-E-WEBAPP-5)

1. `web.Start(t, opts)` builds or uses the built `dsh`, makes a temp home, selects the
   replay layer, starts `dsh web --port 0 --no-open`, reads the URL from the readiness
   line, starts Chromium with a fresh profile (C85 `browser`), signs in with the link.
2. The test drives F1..F7 against recorded model replies.
3. On stop, tripwires fail the test on a console error, an unused recording or an
   unexpected `reset`. Three control builds (a CSP-breaking asset, an unsanitized
   view, a missing recording) must make the lane fail (DH-E-WEBAPP-5).

### F10 Acceptance in S22 (S22; 20 scenarios; closure conditional)

1. Step 22.1: WEBAPP-5 runs first on a clean hosted Linux runner: Go and Chromium,
   no Node, no key, loopback only (TC-19s).
2. Steps 22.2 to 22.8, Chromium, automated through C85/C86: HOST-1 (TC-21s), HOST-2
   and API-2 (TC-22s), BOOT-1 (TC-23s), and the replay-driven API-1, GOUISTACK-1/-2,
   UICORE-1/-2/-3, UIKIT-1/-2, UICONV-1, UICHAT-5 (TC-20s).
3. Steps 22.9 to 22.11, Chromium with the **real signed-in model** (TC-24s): HUMAN-1,
   HUMAN-6, HUMAN-7, BUNDLE-3. A replay run of the same flows (TC-16s) is kept as a
   separate record and is not evidence for these steps.
4. Step 22.12, last: UIKIT-5 and the engine-sensitive repeats of 22.4 and 22.6. Until
   the second driver (W3C WebDriver, DH-ADR-PLAYWRIGHT-2) exists, the Firefox run is
   executed from the written checklist and recorded `manual` with its evidence
   (browser version, console and policy-violation log, session export); Safari has no
   machine under `platforms-windows-wsl` and is recorded "not run". **Both leave step
   22.12 open** (TC-25m).
5. Each pass is recorded in `acceptance-index.yaml`. Because 22.12 is open, S22 is
   **not closed** and M3 is **not reached**; the published M3 open list says "22.1 to
   22.11 passed; 22.12 open (Firefox manual, Safari not run)" (R-06, Q
   `m3-firefox-safari`).

---

## 7. Invariants

| Id | Invariant | Checked by |
|---|---|---|
| INV-1 | Every byte of model or user text in a page passes through C76 or is escaped by `html/template` | archtest rule 1, 2; DH-T-UICONV corpus |
| INV-2 | Exactly one CSP per response, written by `host/web`, no `unsafe-inline`, no nonce | single-CSP-writer check; CSP golden |
| INV-3 | A page rendered from the projection equals the same nodes delivered by the stream | page-vs-stream golden (DH-T-UICONV) |
| INV-4 | The cursor advances only after a successful write; a renderer error yields a placeholder and still advances | DH-T-API stream tests |
| INV-5 | Submits are idempotent: same request id, one message, first receipt | DH-T-API-5 class tests |
| INV-6 | No route without a rule; no non-owner allowed in M3 | DH-T-API-2, DH-T-API access tests |
| INV-7 | Unload leaves no route, source, contribution or CSP hash of the component | DH-T-API, DH-T-UICORE unload tests |
| INV-8 | A grant never outlives its live session and is never copied | DH-T-UICONV grant tests |
| INV-9 | The product binary contains no chromedp and no Node artefact | DH-T-WEBAPP-4; archtest rule 8, 9 |
| INV-10 | Every page works without script (forms post, links navigate) | no-script golden |

---

## Test & Environment Contract

Two independent axes classify every acceptance area.

**Proof strategy** -- what kind of evidence, defined strictly:

- `in-process` -- pure logic with **zero external I/O**: no socket (not even
  loopback), no file opened at test time, no database, no subprocess, no Go toolchain
  call. Allowed: in-memory fakes, `httptest.NewRecorder` (an in-memory
  `ResponseWriter`, not a listener), `fstest.MapFS`, fake clocks, a fake `Follower`,
  data compiled in through `embed.FS`.
- `golden` -- a committed snapshot compared byte for byte (rendered partials, CSP
  text, sanitizer corpus, `composed.golden`). The producer runs in memory; only the
  committed file is read.
- `service:<name>` -- needs a live backing service. Each is named below with how it
  is provided. **Ephemeral** services are created by the test itself on the gate host
  (no docker, nothing pre-installed beyond Go); **provided** services are never on the
  gate host and are reached only through a preflight that fails the run, never skips.
- `lab` -- physical hardware. M3 has **no `lab` rows**.

**Venue** -- where the row runs:

- `gate` -- every lane on every commit (`win.ps1`, `wsl.sh`, hosted CI): `in-process`,
  `golden` and **ephemeral** `service:*` rows only.
- `browser-lane` -- the hosted CI jobs on `ubuntu-latest` and `windows-latest` that
  have Chromium (`service:chromium`). A required check for closing S21; **not** a gate
  row, because the gate host has no browser.
- `guarded` -- the batch at step 19.7 or 21.13, run once with its own preflight.
- `acceptance` -- the S22 runs (and the manual checks of S19 and S20).

`inline` means "proved by the implementing step's own unit run with no named
dependency". **Only `in-process` and `golden` rows may be `inline`.** A `service:*` row,
ephemeral or provided, is never `inline`: it is a named test whose helper provisions
the service and fails when it cannot.

### Ephemeral services (self-provisioned on the gate host)

| Name | How the test provisions it | Why embedded/ephemeral is honest | Used by |
|---|---|---|---|
| `service:sqlite-embedded` | `modernc.org/sqlite` (pure Go, already in `go.mod`) on a file in `t.TempDir()` via `testkit/temphome` | The session store, projections, drafts, prefs and the `continuation` fold use plain SQL; `git grep -i fts5` finds no extension use, so the embedded build lacks nothing the schema needs | TC-03s, TC-04s, TC-05s, TC-13s |
| `service:loopback-http` | `httptest.NewServer` or `host.listen` on `127.0.0.1:0` | Real sockets on loopback; no external host | TC-01s, TC-02s, TC-04s, TC-13s |
| `service:tempfs` | `t.TempDir()` with created directories and symlinks | Local disk only | TC-06s |
| `service:dsh-process` | The built binary or a stub binary via `testkit/dshbin`, `testkit/launcher` | A child process on the same host | TC-14s, TC-15s, TC-18b |
| `service:go-toolchain` | The `go` command already on every lane (`go list -deps`, package loading) | Required to build anyway | TC-10s, TC-14s |

### Provided services (never on the gate host; each has a preflight that fails the run)

| Name | Provider | Preflight |
|---|---|---|
| `service:chromium` | Hosted CI `ubuntu-latest` and `windows-latest` runners (Chrome preinstalled) for `browser-lane`; on the WSL and Windows lane machines a pinned Chrome for Testing path in `DSH_BROWSER` for guarded and acceptance runs (Q `m3-chromium-provisioning`) | `browser.Preflight`: binary found, version at or above the pinned major, headless start within 10s |
| `service:model-route-claude-code` | The operator's signed-in Claude Code (or Copilot CLI) session (decision `models-signed-in-claude-copilot`). It replaces the **API key** of E1, not the **real model**: these rows run real model turns and a replay recording never satisfies them | The M2 signed-in preflight (`llm/manual_signedin_test.go`): the route answers one real prompt |
| `service:linux-scratch-home` | The Linux lane under WSL (`wsl.sh`) with a disposable home on a Linux filesystem | Home writable, on ext4 (not `/mnt/c`), empty before the run |
| `service:windows-host` | The Windows lane machine (`win.ps1`) | `dsh web` builds and starts natively |
| `service:second-machine` | A second machine on the LAN (E5) | The host URL is reachable from it |
| `service:trusted-tls-proxy` | Operator-provided reverse proxy with a certificate the second machine trusts, stripping a path prefix (E5) | TLS handshake succeeds; `X-Forwarded-*` reach the host |
| `service:firefox-geckodriver` | Current Firefox (plus geckodriver once the WebDriver provider of DH-ADR-PLAYWRIGHT-2 exists) on a lane machine | `firefox --version`; until the provider exists the run is a written checklist recorded `manual` |

### Contract rows

Suffix: `a` = in-process, `g` = golden, `s` = service, `b` = guarded batch, `m` =
manual record (never closes a step on its own).

| Row | Acceptance area | Strategy | Dependency and how it is provided | Venue |
|---|---|---|---|---|
| TC-01a | Route declaration rules: zero `Access` rejected, method by `Kind`, closed `Response`, `Public` needs class and reason (DH-T-API-1, -2) | in-process | none; routes built in memory, handlers driven with `httptest.NewRecorder` | gate (inline) |
| TC-01s | Table swap and unload cancel an in-flight handler; old M2 groups (listen, launch, auth, assets) still answer after the C58 migration (DH-T-API-3) | service:loopback-http | ephemeral | gate |
| TC-02a | Pipeline decisions: forgery decision, strict form decode against the schema, activation choice, error to `Problem` mapping, `HX-Request` variants (DH-T-API-4, -6, -9) | in-process | none; recorder only | gate (inline) |
| TC-02s | Forgery and limits on a real socket: cross-origin form, missing `_token`, oversize body 413, `HandlerTimeout` | service:loopback-http | ephemeral | gate |
| TC-02g | Header set and CSP text after a table swap with islands | golden | committed `host/web/testdata/csp.golden` | gate (inline) |
| TC-03a | Rules, `From` resolution with fake resolvers, no-policy denial, the `Owner()` rule vs `OwnerPrincipal()` split (DH-T-API-11, -12) | in-process | none | gate (inline) |
| TC-03s | `Filter` over a stored session list (DH-T-API-13) | service:sqlite-embedded | ephemeral | gate |
| TC-04a | Stream logic: connect order, `seq` ordering, slot replacement, renderer fallback placeholder, park choice, burst counter, resume-or-reset decision, interest cap and `TabGrace` (DH-T-API-14..-19) | in-process | none; fake `Follower`, fake clock, in-memory writer | gate (inline) |
| TC-04s | SSE on a socket: heartbeat, `Last-Event-ID` resume through `store.Follow`, 429 with `Retry-After`, `WriteTimeout` close, resume after host restart (DH-T-API-20, -21) | service:loopback-http + service:sqlite-embedded | ephemeral | gate |
| TC-05s | Conversation commands: blank reuse under lease, idempotent receipt (concurrent, after claim, after store reopen), fork, `continuation` fold, `MayResume` (DH-T-API-5, -7, -25, -26) | service:sqlite-embedded | ephemeral; replay recordings via `embed.FS` | gate |
| TC-06a | dirbrowse window bounding and sort over `fstest.MapFS` | in-process | none | gate (inline) |
| TC-06s | dirbrowse roots, `..` and symlink escape refusal, create on disk | service:tempfs | ephemeral | gate |
| TC-07b | DH-E-STORAGE-1 (step 19.7): web host A and command-line B on one home; archive, archive with `stop=1`, unarchive | service:linux-scratch-home + service:model-route-claude-code | provided; real model turns | guarded |
| TC-08a | `ui.Req`, `SafeHTML` constructors, view registry, buffered `Render`, catalogue completeness (DH-T-UICORE-1..-7) | in-process | none | gate (inline) |
| TC-08g | Shell partials, the no-script page, gallery pages in en/zh, light/dark (DH-T-UICORE-8..-13, -16; DH-T-UIKIT-1, -12, -20) | golden | committed `testdata/*.golden` per `ui/*` package | gate (inline) |
| TC-09a | Kit validation, accessibility lint, keymap conflicts, dock intents, prefs typing (DH-T-UIKIT-2..-11, -18, -19, -21) | in-process | none | gate (inline) |
| TC-10s | Import rules and the single-CSP-writer check (sec. 3.4) | service:go-toolchain | ephemeral (`archtest` loads packages) | gate |
| TC-11g | Sanitizer corpus: hostile inputs to expected HTML | golden | committed `ui/render/testdata/corpus/*` | gate (inline) |
| TC-12g | Page-vs-stream equivalence of transcript nodes; tool card states | golden | committed `ui/transcript/testdata`, `ui/toolcard/testdata` | gate (inline) |
| TC-13a | Chat planner, nav status precedence, interaction option set ("Allow once" default), grant scope rules, composer request-id minting | in-process | none | gate (inline) |
| TC-13s | Draft `Rev` 409, reading records, prefs persistence, `POST /interactions/{iid}` to `Registry.Answer`, `POST /sessions/{sid}/questions/{call}` to `question.Service.Reply` after `Restore` (one inbox message; second answer 409), grant list and revoke (DH-T-UICHAT-1/-7/-8/-13/-14, DH-T-UISIDE-1..-3/-17, DH-T-UICONV-*) | service:sqlite-embedded + service:loopback-http | ephemeral | gate |
| TC-14g | Composed `web` profile | golden | `boot/profile/testdata/composed.golden` | gate (inline) |
| TC-14a | Inventory equals composition; asset manifest complete and total under the byte budget over the embedded tree (DH-T-WEBAPP-1, -3) | in-process | none; `embed.FS` | gate (inline) |
| TC-14s | Readiness before the URL (DH-T-WEBAPP-2); no chromedp in the product's `go list -deps` (DH-T-WEBAPP-4) | service:dsh-process + service:go-toolchain | ephemeral | gate |
| TC-15s | The WEBAPP-6 harness itself with a stub binary and a stub browser (DH-T-WEBAPP-12) | service:dsh-process | ephemeral | gate |
| TC-16s | Keyless browser lane F1..F7 with tripwires (WEBAPP-6) | service:chromium | provided by hosted CI runners | browser-lane |
| TC-17b | DH-E-HUMAN-2 (step 21.13): SDK program and browser on one home | service:model-route-claude-code + service:chromium + service:dsh-process | provided; real model; SDK program built on the lane | guarded |
| TC-18b | DH-E-BOOT-2 (step 21.13): scenario build with probe entries | service:dsh-process | ephemeral; scenario build tag | guarded |
| TC-19s | DH-E-WEBAPP-5: clean runner, no Node, no key, loopback only; three control builds fail | service:chromium | fresh hosted `ubuntu-latest` runner | acceptance |
| TC-20s | Replay-driven browser scenarios, Chromium runs: DH-E-API-1, DH-E-GOUISTACK-1/-2, DH-E-UICORE-1/-2/-3, DH-E-UIKIT-1/-2, DH-E-UICONV-1, DH-E-UICHAT-5 (their "Given" names the replay provider or no model) | service:chromium | provided; replay recordings | acceptance |
| TC-21s | DH-E-HOST-1 (Windows and Linux desktops) | service:chromium + service:windows-host | provided | acceptance |
| TC-22s | DH-E-HOST-2, DH-E-API-2 | service:second-machine + service:trusted-tls-proxy + service:chromium | provided (E5) | acceptance |
| TC-23s | DH-E-BOOT-1 (Windows 11 and Linux) | service:windows-host + service:linux-scratch-home + service:chromium | provided | acceptance |
| TC-24s | **Real-model** browser scenarios: DH-E-HUMAN-1, DH-E-HUMAN-6 (flow F4b: timed question, host kill and restart, late answer through `question.reply`), DH-E-HUMAN-7, DH-E-BUNDLE-3 | service:model-route-claude-code + service:chromium | provided; real model turns; a replay run is recorded separately and never counts | acceptance |
| TC-25m | Step 22.12: DH-E-UIKIT-5 and the engine-sensitive repeats of 22.4 and 22.6 in Firefox and Safari | service:firefox-geckodriver (written checklist); Safari has no machine | recorded `manual`; **leaves 22.12 open** (impl.md DH-S-22 "Manual checks") | acceptance (open) |
| TC-26m | Manual SSE-reconnect, forgery and gallery walks of S19 and S20 | service:chromium | operator on a lane machine; recorded `manual` | acceptance |

**Replay and real-model evidence are separate records.** TC-16s and TC-20s use the
replay layer; TC-07b, TC-17b and TC-24s use the real signed-in model. A row of one kind
is never offered as evidence for a row of the other kind.

**Closure.** S19 and S20 close on their gate rows and manual checks; S21 closes on its
gate rows, TC-16s and the guarded TC-17b and TC-18b. **S22 cannot close while TC-25m is
manual**: impl.md says the checklist runs "leave step 22.12 open", and M3 is reached
only when S22 is closed. M3 is therefore reported as "S22 steps 22.1 to 22.11 passed;
22.12 open" until automated Firefox and Safari evidence exists (R-06, Q
`m3-firefox-safari`).

### Rules for downstream documents

1. `implementation-plan.md` may mark only `in-process` and `golden` rows `inline`.
   Ephemeral `service:*` rows run in the gate as named tests; provided `service:*`
   rows never run in the gate, and `browser-lane` rows are a separate required CI job.
2. A golden row is regenerated only in the step that changes its producer, with the
   diff reviewed in the same commit.
3. `e2e-scenarios.md` gives each scenario its TC row and provided service; a missing
   service fails the run with the preflight message.
4. A real-model row (TC-07b, TC-17b, TC-24s) is never satisfied by replay.
5. No document may claim S22 or M3 closed while TC-25m is `manual`.
6. No row may depend on docker, a database server, Node, or an API key.

---

## 8. Risks, held open, inconsistencies

| Id | Kind | Item | Handling |
|---|---|---|---|
| R-01 | risk | `acceptance-index.yaml` says `built-through: DH-S-11` while M3 assumes M2 closed | Entry prerequisite; S19 step 1 checks the M2 records |
| R-02 | risk | Replacing the interim `web.Route` breaks M2 registrants (listen, launch, auth, assets routes) | C58 migrates them in the same change; DH-T-API-1 runs over all groups |
| R-03 | risk | `agent.StreamFrame` does not exist | C65 adds it in S19 (Q `m3-streamframe-gap`) |
| R-04 | risk | Chromium is not provisioned on the WSL lane | Provided service with preflight (Q `m3-chromium-provisioning`) |
| R-05 | risk | SSE behaviour differs across proxies (buffering) | `X-Accel-Buffering: no`, heartbeat; proved by TC-22s |
| R-06 | risk | Step 22.12 cannot close on a manual checklist, and Safari has no machine under `platforms-windows-wsl`; S22 and M3 therefore stay open | Record Firefox `manual` with evidence, Safari "not run"; closure needs the WebDriver provider (DH-ADR-PLAYWRIGHT-2) and an owner decision on Safari (Q `m3-firefox-safari`) |
| R-07 | risk | Real-model scenarios (STORAGE-1, HUMAN-1/-2/-6/-7, BUNDLE-3) depend on the operator's signed-in session and are non-deterministic | Run only in guarded/acceptance venues with the signed-in preflight; keep replay evidence separate (TC-16s, TC-20s) |
| I-1 | inconsistency | app-web.md calls the driver "DH-LIB-PLAYWRIGHT" while playwright.md chooses `chromedp` v0.24.0 | Follow playwright.md DH-ADR-PLAYWRIGHT-1 |
| I-2 | inconsistency | app-web.md places the scaffold in `test/scenario/web`; the repo keeps scenarios in `internal/testkit/scenarios` (M2 I-2) | Scaffold in `internal/testkit/web` and `internal/testkit/browser` |
| I-3 | inconsistency | impl.md E1 names a real model API key; the owner decision is the signed-in route | `service:model-route-claude-code` |
| I-4 | inconsistency | DH-ADR-39 asks for Safari; `platforms-windows-wsl` excludes macOS | Safari rows stay open (Q `m3-firefox-safari`) |
| I-5 | inconsistency | Sibling docs did not exist when this document was written | Next iteration aligns component ids and TC rows with them |
| I-6 | gap | api.md sec. 6.2 names no route for the workspace, archive, pin, home and stream routes, and does not name the tab header | Names marked `*` in sec. 5.3 and `X-Dsh-Tab` are proposed here; area spec to confirm |
