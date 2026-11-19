# lexical and the @lexical/* packages: Go specification

> **Status: design, not built. The owner decided that the composer is a rich-text
> JavaScript island. This document specifies it as an island that loads Lexical
> ([DH-ADR-LEXICAL-1](#dh-adr-lexical-1)).**
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The earlier
> revision recommended a plain textarea; the repository owner chose the island instead,
> so that references appear as chips inside the text, as upstream.
> Upstream facts were read at the pinned commit recorded
> in [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". No browser behaviour described here was tested, and no Lexical
> file was fetched: the package is not in the upstream checkout (no `node_modules`).

| | |
|---|---|
| **Library ID** | `DH-LIB-LEXICAL` (one family, six packages) |
| **Upstream units** | `UP-DEP-lexical`, `UP-DEP-@lexical/plain-text`, `UP-DEP-@lexical/history`, `UP-DEP-@lexical/text`, `UP-DEP-@lexical/utils`, `UP-DEP-@lexical/headless`; debt `UP-DEBT-patch.lexical@0.49.0` |
| **Upstream version** | 0.49.0 for all six; `lexical` is patched (`pnpm-workspace.yaml:121`) |
| **Kind** | bundled into the client; `@lexical/headless` is test-only |
| **Used by** | `packages/client/ui-conversation` only (the message composer) |
| **Go choice** | **own island code** that loads Lexical as vendored, unmodified browser files embedded in the binary. Lexical is not a Go module; nothing is compiled or bundled |
| **Related areas** | Composer and transcript UI areas (C3); attachments (area A07); [go-ui-stack.md](go-ui-stack.md) for the island rules (`DH-ADR-GOUISTACK-3`, `DH-ADR-GOUISTACK-6`); [react.md](react.md) for submission feedback (`DH-ADR-REACT-3`) |

| Package | What upstream uses it for | In the Go design |
|---|---|---|
| `lexical` | The editor core: document tree, selection, commands, custom node types | Vendored, loaded by the island |
| `@lexical/plain-text` | Plain-text editing behaviour (typing, deletion, line breaks, clipboard) | Vendored, loaded by the island |
| `@lexical/history` | Undo and redo with a merge window | Vendored, loaded by the island |
| `@lexical/text` | Turning matching text into an entity node (plain-text references) | Vendored, loaded by the island |
| `@lexical/utils` | `mergeRegister`: combine several unregister functions | Vendored, loaded by the island |
| `@lexical/headless` | Run the editor without a DOM in one unit test | Not applicable: there is no JavaScript test runner; the island is tested in a real browser |

## 1. What it is and why upstream needs it

The **composer** is the box where the user writes a message. Lexical is a library that
turns an ordinary page element into a text editor whose content is a tree of typed nodes,
not a plain string.

Upstream did not start with it. The composer was a `<textarea>` painted in three stacked
layers so that references could look like coloured tokens inside the text, and the draft
was held twice: as the textarea's string and as a table of "occurrences". Reconciling the
two "guessed the edit position", and a reference could be silently degraded
(`.agents/notes/archived/architecture/2026-08-20-web-composer-lexical-editor.md:12`).
Lexical replaced both: a reference became an **atomic chip**, a node that is inserted,
selected and deleted as one unit and that carries its own identity (`:19`).

So the library is not there for rich text (bold, lists). The editor is registered as
plain text (`packages/client/ui-conversation/src/client/input/editor/runtime.ts:12`). It
is there for one thing: **things inside the text that are not text**. "Rich-text island"
in the owner's decision means this: plain text with chips in it, not formatting.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-conversation/src/client/input/editor/runtime.ts:11-14` | One editor per session, created outside React and kept for the session's lifetime | `createEditor`, `registerPlainText`, `registerHistory`, `createEmptyHistoryState`, `mergeRegister` |
| `.../editor/chip-node.tsx`, `command-chip-node.tsx`, `QuoteNode.tsx` | Three atomic node types: a reference chip (file, folder, session), a claimed slash-command chip, a quote | `DecoratorNode`, `createCommand` |
| `.../editor/text-ref.ts:5` | A reference typed as plain text is recognised and styled without becoming a chip | `registerLexicalTextEntity` |
| `.../editor/projection.ts`, `span-map.ts` | One tree, three readings: for trigger detection (a chip is one placeholder character), for clipboard and draft storage (a chip is its canonical text), for the model (each chip serialised by its owner at submit) | tree traversal, `$createRangeSelection` |
| `.../editor/keymap.ts:10-13`, `:52-55` | Enter must not send while an input method is composing; Safari delivers the closing key after the composition ends, and old engines signal composition with key code 229 | `KeyboardEvent.isComposing` |
| `packages/client/ui-conversation/tests/lexical-editor-core.client.spec.tsx:9` | Drive the document in a unit test with no DOM | `createHeadlessEditor` |

The folder `packages/client/ui-conversation/src/client/input/editor/` holds 2,365 lines of
editor code written on top of the library.

**What the composer does for the user** (from the consuming packages, not from the
library):

| Behaviour | Evidence (`path:line`) |
|---|---|
| Typing `/` or `@` at the caret opens a grouped menu of commands, files and sessions; arrows, Tab, Enter, Escape and the pointer drive it while focus stays in the composer | `packages/client/ui-input-trigger/README.md:12`, `:31` |
| Trigger detection, menu reduction and exact matching are pure code with no DOM | `packages/client/ui-input-trigger/README.md:46` |
| A pick inserts a reference, or starts a command; a claimed command is one chip at the start of the draft | `packages/client/ui-conversation/README.md:107` |
| Enter sends, Shift+Enter breaks the line, Ctrl or Cmd+Enter sends with the other delivery mode | `packages/client/ui-conversation/README.md:131` |
| A paste that carries files anywhere on the page adds them to the composer as attachments, shown in an attachment rail beside the editor | `packages/client/ui-conversation/README.md:137`, `:139` |
| Drafts keep text and reference chips across session switches and reloads | `packages/client/ui-conversation/README.md:105` |
| Undo and redo; a paste is its own undo step; a programmatic insert is one undo step | `...lexical-editor.md:37`; `packages/client/ui-conversation/README.md:205` |
| A quote of selected transcript text can be inserted; on the wire a quote is ordinary prompt text | `packages/client/ui-conversation/README.md:66`; `packages/api/session-controller/README.md:117` |
| On touch devices the draft font is at least 16 px so the browser does not zoom on focus | `packages/client/ui-conversation/README.md:79` |

Upstream binds the editor without the library's React package: "`@lexical/react` is
deliberately not used" (`...lexical-editor.md:18`, `:45`). So the island needs no React.

## 3. Tech debt upstream carries

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-patch.lexical@0.49.0` | **A two-line fix that weighs 648 KB.** When an on-screen keyboard pans the page, Lexical computes the visible area in the wrong coordinates and the caret can stay hidden behind the keyboard. The fix replaces `visualViewport.offsetTop` with `visualViewport.pageTop - defaultView.scrollY`. It is applied to five files; two are minified builds on a single line, so the patch must carry each whole line twice (about 147,000 characters each) | `patches/lexical@0.49.0.patch:1-13` (the fix), `:29-36` and `:37-44` (the minified files), `:45-58` (the source file); size in the assignment row | Resolved: [DH-ADR-LEXICAL-2](#dh-adr-lexical-2) forbids patching a vendored file, so the patch does not exist in the Go design. The defect it fixed is still a requirement (`DH-LIB-LEXICAL-R7`); it is met by a Lexical release that contains the fix, or by the island's own code scrolling the caret into view outside the vendored files. Which of the two is an open question (section 7) |
| Not a unit: cost of the library | "+~70KB gzip" in the composer bundle | `...lexical-editor.md:52` | Kept: the same bytes, served as static files, loaded only on a page that shows a composer |
| Not a unit: draft attachments are lost on reload | "A reload discards draft attachments" because the bytes live in browser memory | `packages/client/ui-conversation/README.md:236` | A pasted file is uploaded to the host at once (`DH-LIB-LEXICAL-R4`), so a reload finds it. The unit itself belongs to the composer area |
| Not a unit: file drops reach every composer | "Document file drops are not routed" | `packages/client/ui-conversation/README.md:237` | One drop target per composer element. The unit belongs to the composer area |
| Not a unit: chip keyboard behaviour cannot be unit-tested | "jsdom lacks `Selection.modify`", so chip deletion is asserted only in a browser | `...lexical-editor.md:33` | Unchanged, and wider: the Go toolchain has no JavaScript test runner at all, so every editing behaviour is asserted in a real browser (`DH-E-LEXICAL-1`) |

Search performed: `patches/`, `pnpm-workspace.yaml`, the decision note above, and the
"Known Limitations" of `ui-conversation` and `ui-input-trigger`.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-LEXICAL-R1` | The user writes multi-line plain text. Enter sends, Shift+Enter breaks the line, Ctrl or Cmd+Enter sends with the other delivery mode. Enter never sends while an input method is composing, including on Safari and on engines that only signal key code 229 | `ui-conversation/README.md:131`; `keymap.ts:10-13`, `:52-55` |
| `DH-LIB-LEXICAL-R2` | Typing a trigger character at the start of a word opens a menu of candidates that narrows as the user types. The keyboard drives the menu without leaving the composer. Escape closes it and changes nothing. Candidates come from the host, which applies the same access rules as every other request | `ui-input-trigger/README.md:12`, `:31` |
| `DH-LIB-LEXICAL-R3` | A picked file, folder or session reference becomes an **atomic chip inside the text**: it shows an icon and a label, and is inserted, selected, copied and deleted as one unit. A claimed slash command is one chip at the start of the draft. What the chip shows is what the host will resolve at submit; a reference that no longer resolves is shown as such before the message is sent, never silently dropped | `...lexical-editor.md:19`, `:50`; `ui-conversation/README.md:105`, `:107` |
| `DH-LIB-LEXICAL-R4` | Pasting, dropping or choosing files adds attachments. Each file is stored by the host as soon as it is added and appears as an **attachment chip inside the text**, a chip kind like a mention, which shows the upload's progress, a retry and a failure inside the chip. It is deleted as one unit, can be removed before sending, and survives a reload. Upstream shows uploads in a rail instead; the owner decided chips ([DH-ADR-21](../design.md#dh-adr-21)) | `ui-conversation/README.md:139`, `:236`; [DH-ADR-21](../design.md#dh-adr-21) |
| `DH-LIB-LEXICAL-R5` | Undo and redo work over typing, pasting and every programmatic insertion (a picked reference, a quote); a paste is one step | `...lexical-editor.md:37`; `ui-conversation/README.md:205` |
| `DH-LIB-LEXICAL-R6` | The draft of each session, text and chips, survives switching sessions and reloading the page. The host holds it; the island never has the only copy | `ui-conversation/README.md:105`; `DH-ADR-GOUISTACK-6` in [go-ui-stack.md](go-ui-stack.md) |
| `DH-LIB-LEXICAL-R7` | On a touch device the caret stays visible above the on-screen keyboard and focusing the composer does not zoom the page | `patches/lexical@0.49.0.patch:8-13`; `ui-conversation/README.md:79` |
| `DH-LIB-LEXICAL-R8` | With scripts unavailable, or when the island fails to load, the composer still sends a text message: the element contains a plain form with a text field and a submit button, which the island takes over only after it has started | [DH-ADR-3](../design.md#dh-adr-3); `DH-LIB-REACT-R10` in [react.md](react.md) |
| `DH-LIB-LEXICAL-R9` | Any third-party browser file the composer loads is byte-identical to a published release, recorded with version, licence and hash, embedded in the binary and served by the host. No build step and no patch | The toolchain drops the bundler; `UP-DEBT-patch.lexical@0.49.0` |
| `DH-LIB-LEXICAL-R10` | The host treats everything the island sends as untrusted. A chip names its target by the canonical mention text the host put in the menu, and by nothing else; at admission the host parses the submitted text, validates every mention under the caller's access rules and ignores any label the browser supplies. There is no table of handles ([DH-ADR-UICHAT-2](../areas/ui-chat.md#dh-adr-uichat-2), which owns the rule). The island never inserts markup: pasted and quoted content enters as plain text, and labels are written as text | [go-ui-stack.md](go-ui-stack.md), `DH-ADR-GOUISTACK-3`; [DH-O-7](../design.md#dh-o-7) |

## 5. Go choice

**Decision.** The composer is a JavaScript island that loads Lexical. The repository
owner chose the island on 2026-11-18 over the earlier recommendation of a plain textarea
("a rich-text JavaScript island, so that mentions and attachments appear as chips inside
the text, as upstream"; recorded for the whole design as
[DH-ADR-21](../design.md#dh-adr-21)). [DH-ADR-3](../design.md#dh-adr-3) already names
"the rich-text input" as an example of an island.

What the owner decided and what this document adds are different things. The owner
chose **an island with chips in the text**. That the island loads **Lexical**, how the
files are delivered, what is sent to the host and the security rules are this
specification's proposals; area C3 confirms them. DH-ADR-21 lists "no composer with
scripts off" as a cost of the island. This document keeps a plain form inside the
element as a fallback (`DH-LIB-LEXICAL-R8`), because the rest of the UI must send a
prompt without script (`DH-LIB-REACT-R10`); C3 may drop the fallback if it does not
hold up.

<a id="dh-adr-lexical-1"></a>
### DH-ADR-LEXICAL-1 The composer is a JavaScript island that loads Lexical; references are atomic chips in the text

- **Decision.** Five parts.

  1. **The island.** A custom element, `<dsh-composer>`, defined in one first-party ES
     module and loaded the first time the element appears, as
     `DH-ADR-GOUISTACK-6` in [go-ui-stack.md](go-ui-stack.md) requires of every island.
     Inside the element the host renders a plain form (a `<textarea>`, a delivery
     choice, a file input, a submit button). When the module starts it creates a
     Lexical editor on a content-editable element, hides the textarea, and keeps the
     textarea's value equal to the draft's text so that the same form still submits.
     Removing the element (a swap, a session switch) runs its `disconnectedCallback`,
     which unregisters every listener.
  2. **The library it loads.** Lexical, the library upstream uses, with the same four
     add-on packages and registered the same way (plain text, history, text entities;
     no React binding). Version 0.49.0 is the one upstream pins and the only one this
     task has evidence for. The files are each package's published production ES module.
     The full set the browser must be able to load, from the lockfile:

     | Imported by the island | Pulled in by those | Evidence |
     |---|---|---|
     | `lexical`, `@lexical/plain-text`, `@lexical/history`, `@lexical/text`, `@lexical/utils` | `@lexical/clipboard`, `@lexical/dragon`, `@lexical/extension`, `@lexical/html`, `@lexical/internal`, `@lexical/list`, `@lexical/selection`, and `@preact/signals-core` 1.14.4 | `pnpm-lock.yaml:30030-30110`, `:34034-34036` |

     Thirteen packages. Licence MIT per the library's repository; **not re-verified in
     this task**, and the exact file names and hashes are not recorded because nothing
     was fetched (section 7).
  3. **How it is vendored and delivered with no bundler.** Each file is copied
     unmodified from its published release into the repository, listed in the vendor
     manifest with package, version, licence, source address and SHA-256, embedded in
     the binary and served from the host's asset route under a name that contains its
     hash ([DH-ADR-LEXICAL-2](#dh-adr-lexical-2)). The published files import one
     another by package name (`import ... from 'lexical'`), which a browser cannot
     resolve by itself. One **import map**, a small JSON block generated from the
     manifest that maps each package name to its served file, is placed in the head of
     every page. No file is concatenated, minified, rewritten or tree-shaken.
  4. **What it sends to the host.** Four kinds of request, all same-origin, all carrying
     the session cookie and passing the host's ordinary request checks.

     | When | Request | Host answer |
     |---|---|---|
     | A trigger character is typed, and on each further keystroke | The trigger and the text typed so far, for the current session | A menu fragment rendered by the host. Each option carries a kind, a display label and a **canonical text token** as data attributes |
     | The draft changes (after a short pause) and when the page is hidden | The draft as text, each chip written as its canonical token | Stored as the draft record of that session and principal; nothing is rendered |
     | A file is pasted, dropped or chosen | The file | It is stored as a pending attachment; the answer carries the attachment's canonical token, which the island inserts as an attachment chip at the caret |
     | The user sends | The form: the draft's text (each chip as its canonical token) and the delivery mode | The host parses the text with the mention grammar of area A14, validates each mention under the caller's principal and records the confirmed spans with the message; on success the pending row of `DH-ADR-REACT-3` appears and the draft is cleared |

     A picked option becomes a chip node that stores exactly the three values the option
     carried. The island draws the chip (icon class, label) itself and writes the label
     as text.
  5. **Security rules.** Section 5.1.
- **Why.** The owner wants chips inside the text. Lexical is the library upstream
  already proved for exactly this, including the input-method, selection and
  browser-engine cases upstream refused to hand-roll ("IME, selection, and engine quirks
  are exactly what Lexical already owns", `...lexical-editor.md:43`). Meeting
  `DH-LIB-LEXICAL-R3` with a plain textarea is not possible, and with an own
  `contenteditable` editor it means writing what Lexical does.
- **Verdict on upstream.** *Keep* the editor library, the three chip kinds, plain-text
  references, the trigger pipeline's behaviour and the composition guard. *Change* where
  authority sits: menu candidates, reference resolution, the draft and attachments are
  host state, and the island is a view of them. *Drop* the React bindings around the
  editor, the patch, and the test-only headless package.
- **Tradeoff or constraint.**
  - It is the largest hand-written script in the product. Upstream's editor layer is
    2,365 lines of TypeScript; the island is its counterpart in plain JavaScript, with no
    type checker and no unit-test runner, so its behaviour is proven only in a real
    browser.
  - It needs an import map, which the base page policy admits through one fixed hash
    source (section 5.1, rule 2; [DH-ADR-UICHAT-3](../areas/ui-chat.md#dh-adr-uichat-3);
    [go-ui-stack.md](go-ui-stack.md), `DH-ADR-GOUISTACK-3`). A framed composer is the
    fallback if a supported browser does not honour the hash.
  - Thirteen vendored files to track, and a defect in any of them cannot be patched
    ([DH-ADR-LEXICAL-2](#dh-adr-lexical-2)).
  - With scripts off, the composer is a textarea: references can be typed as text
    tokens but there are no chips and no menu (`DH-LIB-LEXICAL-R8`).
- **Serves.** [DH-O-12](../design.md#dh-o-12); `DH-LIB-LEXICAL-R1` to `-R8`, `-R10`.
- **Verified by.** `DH-T-LEXICAL-1` to `-5` and `-7` for the host side;
  `DH-E-LEXICAL-1` in a real browser for everything that happens inside the editor.

### 5.1 Security rules for the island

Each rule says what it prevents. They apply on top of the island rules of
[go-ui-stack.md](go-ui-stack.md).

1. **Vendored, pinned, same origin.** Every Lexical file is byte-identical to a
   published release and hash-checked by a test; nothing is loaded from another origin
   or from a content delivery network. *Prevents:* a changed or substituted library.
2. **The page policy stays as strict as the rest of the product, with one addition.**
   The island needs no evaluation, no inline script and no inline style attribute of its
   own. The import map is the exception: a browser treats it as an inline script, which
   `script-src 'self'` blocks. It is allowed by adding the SHA-256 of the import map's
   exact text to `script-src`. The text is generated from the manifest and is the same
   on every response, so no per-response nonce is needed, which is the property
   `DH-ADR-GOUISTACK-3` protects; that decision and area C3's
   [DH-ADR-UICHAT-3](../areas/ui-chat.md#dh-adr-uichat-3) both state the exception. If a
   supported browser does not honour a hash for an import map, the stack's own rule
   applies: an island that needs more than the base policy is mounted in a same-origin
   frame. Framing the main text input
   costs focus, shortcut, paste and resize handling across the frame, so it is the
   fallback, not the plan. *Prevents:* weakening the policy for the whole page to load
   one widget.
3. **No markup ever enters through the editor.** The editor is registered as plain
   text. A paste takes the clipboard's plain-text form only; a quote from the transcript
   is inserted as text; a chip's label is written with `textContent`. The island never
   assigns `innerHTML` and never builds markup from a string. *Prevents:* model output,
   a file name or a pasted page turning into elements that could imitate an approval
   prompt.
4. **The browser names, the host decides.** A chip holds the canonical mention text the
   host put in a menu answer. That text is a pointer, not a grant. At admission the host
   parses the submitted text, validates each session mention under the caller's
   principal and checks each file mention once; a mention whose target is gone or out of
   reach is "unresolved": the message is not sent, the draft is kept, and the chip is
   marked. A label sent by the browser is display only and is never read. A file mention
   gives the model nothing by itself: the model reads the file through its own guarded
   tools. This is area C3's rule
   ([DH-ADR-UICHAT-2](../areas/ui-chat.md#dh-adr-uichat-2)); the first text of this
   document used host-issued opaque handles looked up again at submit, which added
   expiry, re-issue after a host restart and a second path for the no-script form
   without adding protection. *Prevents:* a modified page referencing a session the user
   cannot reach.
5. **Bounded and validated input.** The submitted text is checked against size limits
   and parsed with one grammar before use; a submission that fails is refused with a
   stable error. *Prevents:* oversized or malformed submissions reaching the turn.
6. **No authority in the island.** Drafts and attachments are host state
   (`DH-LIB-LEXICAL-R6`, `-R4`); the send button's availability follows the host's
   access answer but the host refuses an unauthorised send regardless, as upstream does
   (`packages/client/ui-conversation/README.md:101`). *Prevents:* the page being the
   only copy of anything, or the only check on anything.
7. **Nothing leaves through the island.** It talks only to the host that served it.
   *Prevents:* a draft or a file name reaching a third party.

<a id="dh-adr-lexical-2"></a>
### DH-ADR-LEXICAL-2 A vendored browser file is byte-identical to a published release and is never patched

- **Decision.** Every third-party file the browser loads (an island's library, its style
  sheet, its fonts) is copied unmodified from a published release into the repository,
  listed in one manifest with package name, version, licence, source address and SHA-256,
  embedded in the binary and served from the host's asset route under a name that
  contains its hash. A test recomputes every hash. If a release has a defect, the choices
  are: move to a release that fixes it; work around it in the island's own code, outside
  the vendored file; or drop the library. Editing the vendored file is not a choice.
- **Why.** The lexical patch shows what editing a distributed file costs: 648 KB of
  diff, re-created at every version bump, to carry two lines. A hash-pinned unmodified
  file can be audited by comparing one number with the publisher's.
- **Verdict on upstream.** *Drop* `pnpm patch` for browser libraries.
- **Tradeoff or constraint.** A defect with no upstream fix and no outside workaround
  blocks the library. That is intended: it is the point at which the dependency is no
  longer worth having. For the composer this is a live case: Lexical 0.49.0 unpatched
  has the on-screen-keyboard defect (section 3).
- **Serves.** [DH-O-14](../design.md#dh-o-14); `DH-LIB-LEXICAL-R9`.
- **Verified by.** `DH-T-LEXICAL-6`.

This rule is written here because the lexical patch is its evidence. It applies to every
island, including the fallback named in [micromark.md](micromark.md), and the UI stack
specification adopts it for every vendored asset. The general island delivery mechanism
belongs to that specification.

### 5.2 Options considered and not taken

| Option | What it was | Why it was not taken |
|---|---|---|
| Native textarea, a small own script, host-assisted completion | The draft is one string; a reference is a text token; a rail under the text shows what each token resolves to. No library. Works with scripts off | This document's earlier recommendation, **overruled by the owner**: it removes chips inside the text (icon, label, one-keystroke deletion). It survives as the no-script fallback of `DH-LIB-LEXICAL-R8` |
| Own `contenteditable` editor | Chips without a library | Everything Lexical does would be own code; upstream rejected it for the same reason (`...lexical-editor.md:43`) |
| Lexical through a bundler | One output file, no import map | The toolchain has no JavaScript build step ([DH-ADR-3](../design.md#dh-adr-3)), and a bundled file is not byte-identical to any published release |

## 6. Design notes

**Completion round trip.**

1. The island reads the editor through upstream's "detect" projection, in which a chip
   counts as one placeholder character (`...lexical-editor.md:20`). When it sees a
   trigger character at a word start with the caret after it, it sends the trigger and
   the text typed so far to a host route for the current session.
2. The host asks every registered **completion source** (commands, files, sessions; a
   Go interface, one implementation per kind) and returns a menu fragment. A source that
   fails is left out and logged, as upstream drops a failed source silently
   (`ui-input-trigger/README.md:46`). Each candidate carries its canonical token; the
   host remembers nothing about the menu it sent.
3. A newer keystroke cancels the older request. The fragment carries the query it
   answers, and the island discards a fragment whose query is no longer under the caret.
4. A pick replaces the typed token with a chip node in one editor update, so it is one
   undo step (`DH-LIB-LEXICAL-R5`).

**Three readings of one draft.** Upstream's rule is kept (`projection.ts`): for trigger
detection a chip is one placeholder character; for the clipboard, the fallback text
field and the stored draft it is its canonical token; for the model it is whatever the
host's resolver produces for that token at admission. The third reading now happens on
the host.

**Resolution.** At admission the host parses the submitted text with the mention grammar
of area A14. Plain text and quotes pass through. A mention is validated by the source of
its kind; a command name is matched against the command catalog. One unresolved mention
refuses the whole submission with the list of unresolved mentions, which the island
marks. With and without script the host parses the same text with the same rules, so a
typed token resolves to the same target as a chip would. The confirmed spans are
recorded with the message as display facts, so what a sent mention shows is a function
of the log ([DH-ADR-UICHAT-2](../areas/ui-chat.md#dh-adr-uichat-2)).

**Attachments.** A pasted or dropped file is posted to the host at once and stored as a
pending attachment of the session (blob first, then the record, as
[design.md §4.7](../design.md#47-storage) requires). While the upload runs the island
shows a placeholder chip at the caret with its progress; the response carries the
attachment's canonical token and the placeholder becomes an attachment chip. Deleting
the chip removes the attachment from the draft. The attachment is owned by the
session's pending-input state: sending moves it into the message; removing it or
deleting the session releases it; an attachment never sent is collected by the
retention rule for unreferenced blobs. Upstream shows uploads in a rail beside the
editor (`ui-conversation/README.md:137`); the owner decided that they are chips inside
the text ([DH-ADR-21](../design.md#dh-adr-21)), so this design departs from upstream
here on purpose.

**Drafts.** The island sends the draft to the host after a short pause in typing and
when the page is hidden. The host stores one draft record per session and principal,
holding the text and the staged upload references, and renders it into the fallback text
field and into a data attribute the island reads when it starts
([DH-ADR-UICHAT-4](../areas/ui-chat.md#dh-adr-uichat-4)). A typed or pasted token
becomes a chip when the host next renders the draft; one that no longer resolves is
shown as unresolved.

**Ownership.**

| Thing | Owner | Released |
|---|---|---|
| The editor, its listeners and timers | the `<dsh-composer>` element | `disconnectedCallback` |
| The stored draft | the session's pending-input state on the host | on a successful send, or when the session is deleted |
| A pending attachment | the session's pending-input state | on send, on removal, on session deletion, or by retention |
| The vendored files | the repository; embedded in the binary | replaced only by a reviewed manifest change |

**Failure.** A completion request that fails closes the menu; typing continues. A draft
save that fails is retried on the next change and the composer shows that the draft is
not saved. An upload that fails turns its chip into a failed chip with a retry control
inside it; the rest of the text is untouched. A submit that fails or is refused leaves the draft,
chips and attachments in place (`DH-ADR-REACT-3`). If the island or any vendored file
fails to load, the plain form stays visible and still posts text
(`DH-LIB-LEXICAL-R8`). Nothing is rolled back in the browser: the host's stored draft is
the state a reload returns to.

## 7. Risks and open questions

- **The import map and the page policy.** Settled as a design: one hash is added to
  `script-src` ([go-ui-stack.md](go-ui-stack.md) `DH-ADR-GOUISTACK-3`;
  [DH-ADR-UICHAT-3](../areas/ui-chat.md#dh-adr-uichat-3)), with a framed composer as
  the fallback. Still unverified, and only a browser can settle it: that every
  supported browser accepts a hash source for an import map, and that an import map
  placed in the page head serves modules imported later on demand.
- **Lexical's published files, unbundled.** Unverified: whether each package's
  production ES module imports its siblings only by package name, and whether it loads
  without a build-time setting. The decision note warns that one entry file "selects its
  flavor with a top-level await" (`...lexical-editor.md:60`); the island must import
  the production file directly, not that entry. Nothing was fetched, so file names,
  sizes and hashes are not recorded.
- **Which Lexical release.** 0.49.0 is specified because upstream verified it. It has
  the on-screen-keyboard defect that upstream patched. Whether a later release fixes it
  was not checked. If none does, the island needs its own scroll-into-view workaround,
  to be shown on real touch devices by `DH-E-LEXICAL-1`.
- **Does Lexical stay inside the policy at run time?** Unverified: whether the library
  writes `style` attributes or `<style>` elements, which `style-src 'self'` would block.
  `DH-E-LEXICAL-1` requires zero policy violations; a violation forces the frame.
- **Licence.** MIT is stated by the library's repository for Lexical; the licence of
  `@preact/signals-core` was not read. Both must be recorded when the files are fetched.
- **Attachments as chips: decided.** The owner confirmed that uploads are chips inside
  the text, a fourth chip kind beside file, folder and session references
  ([DH-ADR-21](../design.md#dh-adr-21)). Upstream shows uploads in a rail; this document
  first followed upstream and now follows the owner. The host side does not change: an
  upload is stored at once and its chip carries a canonical token like a mention. What
  only a browser can show is whether progress and retry stay usable inside a chip on a
  narrow screen; area C3's real-browser scenario covers it.
- **New harness code.** The island script, the completion-source interface, the draft
  record, the admission-time resolution and the import-map generator are delivered by
  area C3 ([../areas/ui-chat.md](../areas/ui-chat.md)), with the shell's import-map slot
  from area C1.
- **Who writes and reviews the island.** It is JavaScript in a Go repository. Area C3
  names the review and browser-test duty.

## 8. Verification

Local tests cover the host side. Everything that happens inside the editor
(composition, undo, chip gestures, the on-screen keyboard, paste, the page policy) can
only be shown in a real browser, by the scenario below. Local tests and the scenario are
different kinds of evidence, and a skipped scenario leaves its requirements open.
Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-LEXICAL-1` | local | `DH-LIB-LEXICAL-R2` | The completion handler returns candidates filtered by the principal's access, each with a kind, a label and a canonical token, and keeps no state about them; a failing source is omitted and logged; the fragment names the query it answers. The trigger-detection table ported from upstream's pure core (token boundaries, a trigger inside a word, a dismissed token, mixed scripts) runs against the island in `DH-E-LEXICAL-1`, since there is no JavaScript test runner |
| `DH-T-LEXICAL-2` | local | `DH-LIB-LEXICAL-R3` | For a corpus of submitted drafts, each canonical token resolves to the target its menu answer named; a token whose target was deleted, and a session token the caller's principal may not read, each refuse the submission and are listed as unresolved; the same draft typed by hand in the no-script form resolves to the same targets |
| `DH-T-LEXICAL-3` | local | `DH-LIB-LEXICAL-R4` | Upload stores the blob before the record; the answer carries the attachment's token; the rendered draft holds it as an attachment chip, and after a reload it still does; removal and session deletion release it; a file over the size limit is refused with a stable error and nothing is stored |
| `DH-T-LEXICAL-4` | local, golden | `DH-LIB-LEXICAL-R1`, `-R8` | The composer fragment contains a form whose text field and submit button work as plain HTML; a POST with only the text field creates a prompt; the fragment contains no inline script or style |
| `DH-T-LEXICAL-5` | local | `DH-LIB-LEXICAL-R6` | The host draft store: save and restore per session and user, text and chips; a second session's draft is untouched; a successful send clears it; a malformed stored draft is discarded with a diagnostic and the composer renders empty |
| `DH-T-LEXICAL-6` | local | `DH-LIB-LEXICAL-R9` | Every file in the vendor manifest exists, matches its SHA-256 and has a licence entry; no file under the vendor directory is absent from the manifest; the import map generated from the manifest names every vendored module once. The composer only registers the map's text; the `web` component of area A19 computes the hash and places it in the page policy, and its own test (`DH-T-API-25`) checks that the two agree |
| `DH-T-LEXICAL-7` | local | `DH-LIB-LEXICAL-R10` | Hostile submissions: text over the size limit, a malformed token, a token that names a session of another principal, a forged label beside a valid token, and a label or token holding markup. Each is refused or rendered as text; the menu and rail fragments, parsed, contain no element or attribute that came from the label |
| `DH-E-LEXICAL-1` | real browser | `DH-LIB-LEXICAL-R1`, `-R3`, `-R5`, `-R7`, `-R9` | See below |

<a id="dh-e-lexical-1"></a>
### DH-E-LEXICAL-1 The composer island in real browsers under the real page policy

- **Given.** The built binary with the replay model provider and a temporary harness
  home; a workspace with known files; real Chromium, Firefox and WebKit through the
  driver chosen in [playwright.md](playwright.md), each recording policy violations; a
  touch device or emulation with an on-screen keyboard for the last step; an input
  method that composes (for example Pinyin).
- **When.** In each browser: (1) the session page is opened and the composer is
  focused; (2) `@` and part of a file name are typed, a candidate is picked with the
  keyboard, and text is typed after it; (3) Backspace is pressed at the chip's edge, then
  undo, then redo; (4) text is pasted, then undone; (5) a word is composed with the input
  method and Enter confirms the candidate; (6) the page is reloaded; (7) Enter sends;
  (8) on the touch device, a draft long enough to scroll is edited at its end.
- **Then.** (1) every vendored module loads from the host and the violation count is
  zero; (2) the reference is one chip with its label, and the caret is after it;
  (3) the chip is removed whole, undo restores it and redo removes it; (4) the paste is
  one undo step; (5) the candidate is confirmed and no message is sent; (6) the draft
  returns with text and chip; (7) one prompt is accepted whose parts name the picked
  file, the draft clears, and the session log holds that file reference; (8) the caret
  stays visible above the keyboard and the page does not zoom. The trigger-detection
  table of `DH-T-LEXICAL-1` passes in the page.
- **Cleanup.** Close the session, stop the host, delete the home and the workspace. On
  failure keep the session export, the violation reports and a screenshot per browser.

## 9. Units dispositioned by this spec

- `UP-DEP-lexical`
- `UP-DEP-@lexical/plain-text`
- `UP-DEP-@lexical/history`
- `UP-DEP-@lexical/text`
- `UP-DEP-@lexical/utils`
- `UP-DEP-@lexical/headless`
- `UP-DEBT-patch.lexical@0.49.0`

Go items declared here: `DH-LIB-LEXICAL`, `DH-ADR-LEXICAL-1`, `DH-ADR-LEXICAL-2`,
`DH-T-LEXICAL-1` to `-7`, `DH-E-LEXICAL-1`.
