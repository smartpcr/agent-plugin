# @xterm/headless and @xterm/addon-serialize: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18 with
> `go list -m -json <module>@latest`, `go mod download -json` and by reading the source in
> the module cache. No module code was compiled or run, so what a module *does* is stated
> from its source, and how well it emulates is **not** established here.

| | |
|---|---|
| **Library ID** | `DH-LIB-XTERM` (one family, two packages) |
| **Upstream units** | `UP-DEP-@xterm/headless`, `UP-DEP-@xterm/addon-serialize` |
| **Upstream version** | `@xterm/headless` 6.0.0; `@xterm/addon-serialize` 0.14.0 |
| **Kind** | runtime, pure JavaScript, loaded lazily |
| **Used by** | api (`packages/api/terminal-controller`), terminal (`packages/terminal/terminal-bash`); declared but not imported in `benchmarks` |
| **Go choice** | named Go module `github.com/danielgatis/go-headless-term` v1.0.9 behind a harness interface, **provisional**; the serializer and the marks are own code |
| **Related areas** | terminal and web terminal; [node-pty.md](node-pty.md) for where the bytes come from; the UI stack named in [DH-ADR-3](../design.md#dh-adr-3) for the browser terminal widget |

| Package | What it is | Go replacement |
|---|---|---|
| `@xterm/headless` | The xterm.js terminal emulator without a display: it consumes the bytes a program prints and keeps the resulting grid of characters, the cursor and the scrolled-off history in memory | The chosen emulator module, wrapped by a harness adapter |
| `@xterm/addon-serialize` | Turns that in-memory state back into a string of escape sequences which, written to another xterm.js, redraws the same screen | Own code in the adapter ([DH-ADR-XTERM-2](#dh-adr-xterm-2)) |

## 1. What it is and why upstream needs it

A program on a terminal does not print lines. It prints characters mixed with **escape
sequences**: short commands such as "move the cursor up two rows", "erase to the end of
the line", "switch to the alternate screen" (the separate full-screen page that editors
and pagers draw on). What a person sees is the result of applying all of them in order to
a grid of cells. Software that does this is a **terminal emulator**; in a browser it is
xterm.js.

The harness needs the result on the **host**, without a display, for three reasons.

1. **To answer the program.** Some sequences are questions: "where is the cursor?",
   "what kind of terminal are you?". The program waits for the reply on its input. If no
   browser is attached, somebody else must answer, or the program stalls or misdraws.
2. **To let the model read the screen.** The model cannot interpret escape sequences. It
   needs the text a person would see, including the current page of a full-screen
   program.
3. **To restore a browser tab.** A terminal keeps running while no browser shows it.
   When a tab opens or reloads, it needs the current screen at once, not a replay of
   everything since the terminal started.

Two terms used below. **Scrollback** is the bounded history of rows that have scrolled off
the top of the screen. A **wrapped row** is a row that exists only because the previous
row ran out of columns; one long line of output occupies several rows, and a reader that
wants lines back must know which rows to join.

## 2. How upstream uses it

Every import was read. There are two independent uses, and they read very different
amounts.

**Use A: `terminal-bash`, the shell the model drives. Replies only.**

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/terminal/terminal-bash/src/session.ts:28`, `:283-284` | Create an emulator of the terminal's size with **no** scrollback | `new Terminal({ cols, rows, scrollback: 0 })` |
| `session.ts:647-684` | Feed every chunk of output, one write at a time, and learn when it has been applied | `write(data, callback)` |
| `session.ts:285-295` | Take the emulator's replies to the program's questions and write them to the terminal's input, in order | `onData(listener)` |
| `session.ts:698-708` | Release | `dispose()` on the listener and the emulator |

Nothing is read from the grid here. The text the model receives comes from a separate
hand-written filter (`terminal-bash/src/sanitize.ts`, 188 lines) that strips escape
sequences from the byte stream; the README says so: "a headless xterm maintains
control-sequence state only for terminal-protocol replies"
(`packages/terminal/terminal-bash/README.md:169`).

**Use B: `terminal-controller`, the terminal tab in the web sidebar, also readable by an agent. The full state.**

| Use site (`path:line`) | What is read or done | API used |
|---|---|---|
| `packages/api/terminal-controller/src/terminal.ts:52-56` | Create an emulator with bounded scrollback (`scrollback`, default 1000 rows, README `:39`) and attach the serializer | `new Terminal({ cols, rows, scrollback, allowProposedApi: true })`, `loadAddon(new SerializeAddon())` |
| `terminal.ts:388-395` | Apply each output chunk, then give the same chunk a sequence number and send it to attached browsers; applying and numbering happen in one queue | `write(data, callback)` |
| `terminal.ts:96-103` | **Snapshot for a new browser attachment:** the whole state as one string, taken in the same queue so that it is exactly "everything before sequence N" | `SerializeAddon.serialize()` with default options |
| `terminal.ts:146-152` | Resize together with the real terminal | `resize(cols, rows)` |
| `terminal.ts:276-280`, `src/screen.ts:10-15` | **Last row with content:** the cursor row, or a later row that is not blank | `buffer.active`, `.length`, `.baseY`, `.cursorY`, `getLine(row).translateToString(true)` |
| `src/screen.ts:35-49` | **Text as lines:** every row from the first retained row to the last content row, joining each wrapped row to the one before it and trimming trailing blanks | `getLine(row).isWrapped`, `.translateToString(false)` |
| `terminal.ts:278`, `:290`, `:295-297` | **Which screen is showing** (normal or alternate); a full-screen program is read "as displayed" | `buffer.active.type` |
| `terminal.ts:287-303`, `src/screen.ts:22-26` | **A mark:** remember the first row of the line that holds the cursor, later read only the lines from there on, and learn if that row has been trimmed from scrollback | `registerMarker(offset)`, `marker.line`, `marker.isDisposed`, `marker.dispose()` |
| `terminal.ts:340-351` | Release when the terminal closes; later reads fail with a stable error | `dispose()` |

Use B never listens for replies (`onData` is not used in `terminal-controller`). The
browser's xterm.js answers the program while a tab is attached. Inference: an agent
terminal with no tab attached has nobody answering.

`src/screen.ts:57-77` (choose a page of lines counted from the newest, within a byte
limit) is ordinary harness code that happens to live beside the emulator; it carries over
unchanged and is not a requirement on the library.

## 3. Tech debt upstream carries

Search performed: every import of both packages; `patches/` and `pnpm-workspace.yaml`
(no patch, pin, override or exemption for either); the "Known Limitations" of
`terminal-bash` and `terminal-controller`. No `UP-DEBT` unit is assigned to this family.
The limitations below belong to the consuming packages and are dispositioned by their
area; they are listed because the Go choice changes them.

| Item | What it is | Evidence (`path:line`) | Effect of the Go design |
|---|---|---|---|
| Two answers to "what is on the screen" | The model's shell reads a byte filter; the web terminal reads an emulator. The same output can therefore read differently in the two places, and the filter cannot follow a full-screen program | `terminal-bash/README.md:169`; `terminal-controller/README.md:63` | One adapter can serve both (`DH-LIB-XTERM-R5`). Whether the model's shell switches to it is the terminal area's decision |
| Bookkeeping around asynchronous writes | xterm.js applies a write later and calls back. Upstream keeps promise chains to know when "applied" and "replied" are both true before it inspects the foreground process (`emulatorWrites`, `responseWrites`, `drainTerminalProtocol`) | `terminal-bash/src/session.ts:270-276`, `:616-645` | Removed: in Go a write returns when it has been applied, and replies are collected during that call (section 6) |
| Nobody answers an unattended agent terminal | See the inference above | `terminal-controller/src/terminal.ts` (no `onData`) | `DH-LIB-XTERM-R2` makes the host able to answer; who answers when is an open question (section 7) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-XTERM-R1` | Output bytes are applied in arrival order, and a caller can obtain a state that includes exactly the output up to a known sequence number | `terminal-controller/src/terminal.ts:96-103`, `:266-268`, `:388-395` |
| `DH-LIB-XTERM-R2` | Questions in the output (cursor position, device attributes, device status, mode reports) produce replies that reach the program's input in order. The owner can turn replies off, so that two emulators never answer the same question | `terminal-bash/src/session.ts:285-295` |
| `DH-LIB-XTERM-R3` | History is bounded by a configured number of rows, including zero, and memory is bounded by columns times (rows plus history) | `terminal.ts:54`; `session.ts:284`; `terminal-controller/README.md:39` |
| `DH-LIB-XTERM-R4` | **Snapshot.** One byte string that, written to a new xterm.js of the same size in the browser, reproduces the retained history, the visible screen with its colours and attributes, the cursor position, and which screen (normal or alternate) is showing | `terminal.ts:100`; `terminal-controller/README.md:61` |
| `DH-LIB-XTERM-R5` | **Text.** The showing screen can be read as plain lines from the first retained row to the last row with content, with wrapped rows joined and trailing blanks trimmed; the alternate screen is read as displayed | `src/screen.ts:10-49`; `terminal.ts:276-280` |
| `DH-LIB-XTERM-R6` | **Marks.** A caller can mark the start of the line that holds the cursor and later read only the lines from the mark on. If the marked row was trimmed from history the read says "truncated"; if a different screen is now showing, the whole showing screen is returned | `terminal.ts:287-303`; `src/screen.ts:22-26` |
| `DH-LIB-XTERM-R7` | The grid is resized together with the real terminal, within configured limits | `terminal.ts:146-152` |
| `DH-LIB-XTERM-R8` | The text between the start of the cursor's row and the cursor can be read after each applied chunk, with untouched cells read as spaces, so that a prompt is recognised by what is on the screen and not by which bytes drew it | Inference from the bundled-console-host note (`.agents/notes/implemented/bug-fix/2026-09-28-pty-bundled-console-host.md:13`) and [DH-ADR-NODEPTY-2](node-pty.md#dh-adr-nodepty-2) |
| `DH-LIB-XTERM-R9` | Closing releases the state; a read or mark after closing fails with a stable error | `terminal.ts:311-315`, `:340-351` |
| `DH-LIB-XTERM-R10` | Program output is untrusted input: any byte sequence is applied without a panic, an unbounded loop or unbounded memory. The emulator is pure Go and builds with `CGO_ENABLED=0` | Inference from the release assumption and from the fact that the bytes come from arbitrary programs |

## 5. Go choice

**Decision.** Recorded as [DH-ADR-XTERM-1](#dh-adr-xterm-1) and
[DH-ADR-XTERM-2](#dh-adr-xterm-2). The harness defines its own small screen interface.
Its first implementation wraps `github.com/danielgatis/go-headless-term` v1.0.9. The
snapshot serializer, the marks and the line joining are harness code on top of that
interface. The module choice is **provisional**: it becomes final only when the
conformance test `DH-T-XTERM-9` passes for it.

**Why.** No pure-Go module read for this specification provides everything xterm.js does.
The candidate chosen is the only one that records which rows are wrapped, lets the
harness supply the history store (which is what makes marks and "truncated" possible),
and has a small dependency tree. Putting it behind an interface keeps the three known
gaps, and the risk of a young single-maintainer module, in one place.

| Option | Replies (`R2`) | History (`R3`) | Wrapped rows (`R5`) | Alternate screen | State export (`R4`) | Verified facts |
|---|---|---|---|---|---|---|
| **`github.com/danielgatis/go-headless-term`** (chosen, provisional) | Yes: `WithPTYWriter` receives them; cursor position and device attributes handled in `handler.go:513-536`, `:632-644` | Pluggable: `ScrollbackProvider` interface (`providers.go:93`), every evicted row is handed to `Push` | Partly: `IsWrapped(row)` for rows on screen (`terminal.go:1114`); the flag is **not** passed to `Push` (`buffer.go:171-175`) | `IsAlternateScreen()` | `Snapshot(detail)` returns text, styled segments or cells per line, plus cursor; no escape-sequence output | v1.0.9, 2026-01-02, MIT, no cgo. Direct dependencies `github.com/danielgatis/go-ansicode` v1.0.14 and `github.com/unilibs/uniwidth` v0.1.0, three indirect; all MIT. About 12,300 lines including tests. Also reports shell prompt marks (`SemanticPromptHandler`). No reflow on resize (`terminal.go:465`) |
| `github.com/charmbracelet/x/vt` | Yes: replies are read from the emulator (`Read`); it is an unbuffered `io.Pipe` (`emulator.go:101`), so a reader must always be running or `Write` blocks | Built in and bounded (`SetScrollbackSize`, `Scrollback().Lines()`); no event when a row is evicted | **No**: only the pending-wrap state of the cursor is kept | `IsAltScreen()` and a callback | `Render()` returns the visible screen with colours as escape sequences; history rows are rendered one by one | v0.1.0, 2026-10-12 (first tagged release), MIT, no cgo. Requires `github.com/charmbracelet/ultraviolet` at an untagged pseudo-version and 14 indirect modules. Maintained by an organisation. No reflow on resize (`emulator.go:230`) |
| `github.com/vito/midterm` | Yes: `ForwardResponses` writer | None of its own: `OnScrollback` callback hands over each evicted row | **No** | `IsAlt` | `MarshalBinary()` writes both screens and the cursor as escape sequences, the closest thing to the serialize addon | v0.2.5, 2026-08-07, MIT, no cgo. `go.mod` lists `bubbletea`, `termenv` and test libraries as direct requirements |
| `github.com/hinshun/vt10x` | Device attributes and status only (`csi.go:80`, `:166`) | **None** (no scrollback in its source) | No | Yes | No | Last commit 2022-03-01, no tagged release, MIT, no cgo. Rejected: unmaintained and no history |
| `github.com/mitchellh/go-libghostty` | Yes | Yes | Yes | Yes | Yes | Untagged (pseudo-version of 2026-11-07), MIT. **Requires cgo** (`cgo_static.go`, `cgo_shared.go`) and a native library per platform. Rejected under the `CGO_ENABLED=0` release assumption; it is the option to revisit if the pure-Go emulators fail `DH-T-XTERM-9` |
| No host emulator: keep the raw bytes and replay them to the browser | n/a | A byte ring | n/a | n/a | The bytes themselves | No module. Rejected: cannot give the model a full-screen program's current page (`R5`), cannot answer questions when no browser is attached (`R2`), and a replay that starts mid-stream loses modes set earlier |
| Own emulator on a parser such as `github.com/charmbracelet/x/ansi` | Own | Own | Own | Own | Own | `x/ansi` v0.11.10, 2026-11-18, MIT. Rejected as a first step: terminal emulation is a long tail of rules and xterm.js took years; own code is justified only for the thin parts above an emulator |

**Tradeoff.** Three behaviours are weaker than xterm.js until closed, each stated so that
nobody discovers them later:

| Gap | Effect | Handling |
|---|---|---|
| A row's wrapped flag is lost when the row moves into history: `Push` receives only the cells, and it runs while the emulator holds its lock, so it cannot ask `IsWrapped` (which takes the same lock, `terminal.go:1114-1117`) | Joining rows into lines is exact on the screen and approximate in history | The adapter joins a history row to the next one when its last cell is not blank. This is wrong for a line that is exactly as wide as the terminal and ends with a real line break (it is joined to the next line). The exact fix is a one-field change to `ScrollbackProvider.Push`, to be offered to the module's maintainer; the approximation is removed when it lands |
| No reflow on resize | After the width changes, wrapped history keeps its old row breaks on the host, while the browser's xterm.js re-wraps its own copy | Accepted. Text reads join rows, so they are unaffected. A browser that re-attaches gets the host's layout |
| Young module, one maintainer, last release ten months before this check | Emulation defects may surface with programs not tried | The interface in section 6 confines a replacement to one adapter file; `x/vt` is the named fallback; the cgo option is the escalation and needs a release-plan decision |

<a id="dh-adr-xterm-1"></a>
### DH-ADR-XTERM-1 The host emulator sits behind one harness interface; `go-headless-term` is its first, provisional implementation

- **Decision.** The harness owns a `Screen` interface (section 6). Only one adapter file
  imports an emulator module. The first adapter wraps
  `github.com/danielgatis/go-headless-term` v1.0.9.
- **Why.** The harness needs replies, history it controls, wrapped-row flags and the
  alternate screen (`R2`, `R3`, `R5`, `R6`). That module has more of them than the
  alternatives, with the fewest dependencies. An interface is needed regardless, because
  the serializer and the marks are harness code that must not depend on one module's
  cell type.
- **Verdict on upstream.** *Keep* the idea of a host-side emulator for the web terminal.
  *Change* how it is used: one write path, synchronous, serving replies, text and
  snapshot from the same state.
- **Tradeoff or constraint.** `CGO_ENABLED=0` excludes the most complete emulator. The
  chosen one has the three gaps in the table above.
- **Serves.** `DH-LIB-XTERM-R1` to `R3`, `R5` to `R10`.
- **Verified by.** `DH-T-XTERM-1` to `DH-T-XTERM-9`. `DH-T-XTERM-9` is the gate that
  turns "provisional" into "chosen", or triggers the fallback.

<a id="dh-adr-xterm-2"></a>
### DH-ADR-XTERM-2 The snapshot serializer and the marks are harness code

- **Decision.** The harness writes the snapshot itself: for each retained history row and
  each screen row, the text with its colour and attribute changes; a real line break
  between lines and none between a row and the wrapped row that continues it; then
  "enter alternate screen" and that screen's rows if it is showing; then the cursor
  position and visibility. Marks are a counter: the adapter's history store counts every
  row it has ever received, a mark stores "rows ever evicted plus screen row", and
  "truncated" means the stored number is now below the oldest retained row.
- **Why.** No Go emulator read here exports its state in a form xterm.js can replay with
  history (`midterm` comes closest, without history), and none has markers. Both are
  small and depend only on reading cells, so they belong above the interface where a
  module swap cannot break them.
- **Verdict on upstream.** *Keep* the behaviour of `SerializeAddon.serialize()` and
  `registerMarker`; *replace* the mechanism.
- **Tradeoff or constraint.** The browser side stays xterm.js
  ([DH-ADR-3](../design.md#dh-adr-3) allows a JavaScript island for the terminal), so
  the snapshot's only consumer is known and the test can compare against it exactly.
  Modes other than the alternate screen (mouse reporting, bracketed paste, application
  cursor keys) must be included in the snapshot or a re-attached tab sends the wrong
  input; the serializer reads them from the emulator (`HasMode`).
- **Serves.** `DH-LIB-XTERM-R4`, `R6`.
- **Verified by.** `DH-T-XTERM-4`, `DH-T-XTERM-6`.

## 6. Design notes

**Shape.** The interface belongs to the terminal area's package; the adapter is the only
importer of the emulator module.

```go
type Screen interface {
    // Write applies output. It returns when the bytes have been applied and
    // hands back any replies the emulator produced while applying them.
    Write(p []byte) (replies []byte, err error)
    Resize(cols, rows int) error

    Alternate() bool                       // which screen is showing
    Lines(from Mark) (lines []string, truncated bool)   // R5, R6; zero Mark = all retained
    CursorPrefix() string                  // R8: cursor row, start to cursor
    Mark() Mark                            // start of the cursor's line
    Snapshot() []byte                      // R4

    Close()
}
```

**One owner, no callbacks.** Each terminal has one goroutine (its *pump*) that reads the
PTY, calls `Write`, sends the returned replies to the PTY's input if replies are enabled
for this terminal, numbers the chunk and passes it to attached browsers. Readers
(`Lines`, `Snapshot`, `CursorPrefix`) take the same mutex as `Write`. Because `Write` is
synchronous, "the snapshot includes exactly the chunks up to N" needs no queue and no
completion callback: it is whatever state the mutex protects when the reader takes it.

**Replies never block the emulator.** The module calls the reply writer from inside
`Write`. The adapter's writer only appends to a small bounded buffer; the pump writes
that buffer to the PTY after `Write` returns. A full PTY input buffer therefore cannot
block the emulator while it holds its lock, and replies keep their order relative to
later input. If the bound is exceeded (a program printing thousands of questions), the
excess replies are dropped and counted; this is safe because a reply is only ever useful
to the program that asked.

**Wrapped-row convention.** The module flags the row that overflowed
(`handler.go:687-688`); xterm.js flags the row that continues it. The adapter converts
once, so harness code has one meaning: `continues(row)`.

**Ownership.** The `Screen` is owned by its terminal session, which is owned by the
terminal service's fiber. `Close` drops the grid and the history store. Nothing else
(no process, descriptor or goroutine) belongs to the emulator, so closing cannot fail
and needs no retry.

**Failure.** `Write` returns an error only if the adapter recovers a panic from the
module. The session then marks its screen as unavailable, keeps the process and the raw
byte delivery to browsers running, and reports text reads as failed with a stable error.
A terminal emulator defect must not kill a running program or the host. The recovered
panic is logged with the offending chunk's sequence number, not its content.

## 7. Risks and open questions

- **The choice is provisional.** Emulation quality was not measured. `DH-T-XTERM-9`
  must be written and run early in the terminal stage, before other work depends on the
  adapter. If it fails for both pure-Go candidates, the decision goes back to the
  repository owner with the cgo option and its release cost.
- **Who answers the program?** With a browser attached, xterm.js answers; if the host
  answers too, the program receives two replies. Options: (a) the host answers only while
  no attachment holds input control, as upstream's split implies; (b) the host always
  answers and the browser widget's replies are filtered out before they are sent.
  Upstream already has trouble telling replies from typing on the browser side
  (`terminal-controller/README.md:63`), which argues for (b). For the web terminal area.
- **Does the model's shell read the emulator or keep the byte filter?** One projection
  is simpler and reads full-screen programs; the filter is cheaper and already matches
  upstream's recorded sessions. For the terminal area.
- **Upstream contribution.** The wrapped flag in `ScrollbackProvider.Push` should be
  proposed to the module. Until accepted, the approximation in section 5 stands; a fork
  is not planned, because a private fork is the kind of patch debt this design removes.
- **New harness package.** Adapter, serializer, marks and line joining are own code
  (Inference: a few hundred lines). The terminal area must schedule them.

## 8. Verification

All tests are local and need no terminal device: they feed recorded byte sequences to
the adapter. `DH-T-XTERM-4` and `DH-T-XTERM-9` additionally need xterm.js, run in the
browser test harness the UI specification chooses. Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-XTERM-1` | local | `R1` | Concurrent writers and readers: every state a reader observes equals the state after some whole prefix of the chunks, and the reported sequence number matches it |
| `DH-T-XTERM-2` | local | `R2` | Cursor-position, device-attribute and status queries produce the documented replies in order; with replies turned off none is produced; a flood of queries is bounded and counted |
| `DH-T-XTERM-3` | local | `R3`, `R10` | History never exceeds the configured rows, including zero; random and truncated escape sequences, invalid UTF-8 and very long sequences (fuzzing) cause no panic and no growth beyond the bound |
| `DH-T-XTERM-4` | local with xterm.js | `R4` | For each recorded session: write the adapter's snapshot into a fresh xterm.js and compare its rows, colours, cursor and showing screen with an xterm.js that received the original bytes |
| `DH-T-XTERM-5` | local | `R5` | A line longer than the width reads back as one line; trailing blanks are trimmed; a full-screen program's page is returned while the alternate screen shows, and the earlier lines after it leaves; the history-row approximation is asserted, including its known wrong case |
| `DH-T-XTERM-6` | local | `R6` | Lines since a mark; "truncated" once the marked row is evicted; the whole screen after a switch to the other screen |
| `DH-T-XTERM-7` | local | `R7`, `R9` | Resize larger and smaller keeps the cursor inside the grid; reads after `Close` return the stable error; an injected panic in the emulator leaves the session running with reads failing |
| `DH-T-XTERM-8` | local | `R8` | The prompt drawn as `dsh> ` and as newline, `dsh>`, cursor-right-one both give the cursor prefix `dsh> ` |
| `DH-T-XTERM-9` | local with xterm.js | [DH-ADR-XTERM-1](#dh-adr-xterm-1) | Conformance corpus: recorded output of `bash`, `zsh`, `pwsh` (on Windows through ConPTY), `vim`, `less`, `top`, `git log` with a pager, a progress bar and wide and combining characters. The adapter's grid must equal xterm.js's grid for each. The corpus and the pass threshold are fixed by the terminal area before the adapter is written |

## 9. Units dispositioned by this spec

- `UP-DEP-@xterm/headless`
- `UP-DEP-@xterm/addon-serialize`
