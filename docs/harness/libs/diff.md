# diff: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-DIFF` (compute a line diff). The second item this document first declared, a parser for Git patch text, was withdrawn with its only consumer (section 7) |
| **Upstream unit** | `UP-DEP-diff` |
| **Upstream version** | 9.0.0 (the npm package `diff`, also called jsdiff) |
| **Kind** | runtime in four host packages; bundled into the client in two; test-only at the root |
| **Used by** | fs, deliverables, experimental; client |
| **Go choice** | named Go module `github.com/aymanbagabas/go-udiff`, behind one harness package |
| **Related areas** | A08, which owns the `textdiff` package ([../areas/filesystem.md](../areas/filesystem.md), `DH-D-FS-2`); the conversation UI (C4), which draws the stored hunks |

## 1. What it is and why upstream needs it

A **diff** is the list of differences between two versions of a text. A **line diff**
works in whole lines. A **hunk** is one changed region together with a few unchanged
**context** lines around it, and a **unified patch** is the familiar text form of a list
of hunks: lines that start with a space (unchanged), `-` (removed) or `+` (added), under
a header such as `@@ -10,7 +10,8 @@`.

Upstream uses the library to *show* changes: what a `write` or `edit` tool call did to a
file, what a whole turn did to the workspace, how the system prompt changed between two
requests, and what a merge request contains. It calls two functions only:
`structuredPatch`, which compares two texts and returns hunks, and `parsePatch`, which
reads a unified patch that something else produced.

The library is **not** how edits are applied. The model's `apply_patch` tool has its own
patch format (`*** Begin Patch`) with upstream's own parser and applier
(`packages/fs/fs-patch/src/parse.ts:9`, `packages/fs/fs-patch/src/apply.ts:65`). That
code is ported by the filesystem area and needs nothing from this library.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/fs/tool-fs/src/diff.ts:36` | After `write` or `edit`: one card per changed hunk with three context lines, stored as before-text and after-text in the tool result's metadata and persisted with the session (`:14-22`). The "no newline at end of file" marker is dropped (`:42-44`). | `structuredPatch(…, { context: 3 })` |
| `packages/deliverables/workspace-changes/src/compare.ts:41` | End-of-turn summary of every file the turn changed: hunks plus added and deleted line counts. A comparison that runs longer than 100 ms (`packages/deliverables/workspace-changes/README.md:42`) is replaced by one hunk that deletes every old line and adds every new one, flagged `coarse` (`:44-52`). | `structuredPatch(…, { context: 3, timeout })` |
| `packages/experimental/forge-gitlab/src/diff.ts:211` | When GitLab leaves a file's diff out, compute it from the two texts and print it as patch text, numbering an empty side from 0 as Git does (`:212-213`). | `structuredPatch` |
| `packages/experimental/client-ui-forge/src/diff.ts:27` | Read the patch text from `git diff` or a code host into files with status (added, deleted, renamed, modified), binary flag, counts and hunks. The blob identifiers and the file modes are recovered by upstream's own regular expressions over the raw header (`:17`, `:29`, `:51-54`). | `parsePatch` |
| `packages/client/ui-primitives/src/DiffBlock.tsx:77-79` | In the browser: turn a stored before/after pair back into coloured lines. Gives up above an edit distance of 256 and shows a whole replacement (`:70`). | `structuredPatch(…, { context: 3, maxEditLength: 256 })` |
| `packages/client/ui-trajectory/src/client/TrajectoryTable.tsx:1422` | In the browser: show how the system prompt changed between two requests. | `structuredPatch(…, { context: 3 })` |

jsdiff's algorithm is the Myers shortest-edit-script search. Its cost grows with the
number of differences, which is why upstream bounds it in two different ways.

## 3. Tech debt upstream carries

Search performed: every import of `diff` in source and tests; `patches/` (no patch);
`pnpm-workspace.yaml` (no pin); the "Known Limitations" sections of the consuming
READMEs (none names this library).

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L9.3` | The search is bounded inconsistently. The host uses wall-clock time, so the same edit is recorded as fine hunks on an idle machine and as one whole-file hunk on a busy one. The browser uses an edit-distance cap for the same data. The tool-result diff has no bound at all below the 10 MiB input cap. | `packages/deliverables/workspace-changes/src/compare.ts:41-52`; `packages/client/ui-primitives/src/DiffBlock.tsx:70`; `packages/fs/tool-fs/src/diff.ts:36`; `packages/fs/fs-local/src/index.ts:63` | Resolved by [DH-ADR-DIFF-1](#dh-adr-diff-1): one package, one bound that depends only on the inputs. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-DIFF-R1` | Given two texts and a number of context lines, return hunks: start line and line count on each side, and the lines marked unchanged, removed or added. Identical texts give no hunks. | all `structuredPatch` sites |
| `DH-LIB-DIFF-R2` | The work is bounded by a rule that depends only on the two texts. Above the bound the result is one whole-replacement hunk flagged as coarse. The same inputs always give the same result. | `compare.ts:41-52`, `DiffBlock.tsx:70`, `UP-DEBT-found.L9.3` |
| `DH-LIB-DIFF-R3` | A missing final newline never appears as content in a hunk, and two texts that differ only in the final newline are handled the same way everywhere. | `tool-fs/src/diff.ts:42-44`, `compare.ts:16-22` |
| `DH-LIB-DIFF-R4` | Each hunk can be given as before-text and after-text (before is absent for a pure insertion), the form persisted with a tool result. | `tool-fs/src/diff.ts:56` |

Two requirements this document first carried, printing hunks as unified patch text (R5)
and reading Git patch text into files (R6), were withdrawn. Their only consumer was the
code-host diff view, which is not built: the code-host UI shows status only
([DH-ADR-38](../design.md#dh-adr-38);
[../areas/x-forge-worktree.md](../areas/x-forge-worktree.md)), and the filesystem area
parses no Git patch text.

## 5. Go choice

**Decision.** `github.com/aymanbagabas/go-udiff` computes line diffs, behind one harness
package so that no other code imports it.

**Why.** go-udiff is a copy of the diff package the Go team uses inside its own tools,
published so that others can import it; it has no dependencies and produces hunks with a
chosen number of context lines.

<a id="dh-adr-diff-1"></a>
### DH-ADR-DIFF-1 One diff package on the host, bounded by input size

- **Decision.** All diffs are computed on the host by one package, including those the
  browser computes upstream. The package refuses to run the line comparison when either
  text exceeds a byte limit or a line-count limit and returns a coarse whole-replacement
  result instead. There is no time limit.
- **Why.** The UI is rendered by the host ([DH-ADR-3](../design.md#dh-adr-3)), so there
  is no second place to compute a diff. A bound on input size is deterministic: a diff
  stored in the session log is the same on every machine and in every replay. The chosen
  module also bounds its own search depth (see the table), so a size limit is enough to
  bound the time.
- **Tradeoff.** A large file with a one-line change is shown coarsely even though a fine
  diff would have been quick. Limits are configuration; the proposed defaults are
  upstream's 10 MiB per side and 50,000 lines.
- **Serves.** `DH-LIB-DIFF-R2`; removes `UP-DEBT-found.L9.3`.
- **Verified by.** `DH-T-DIFF-2`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen (compute):** `github.com/aymanbagabas/go-udiff` | Line-based; hunks with context; no dependencies; search depth is capped inside the module, after which it returns a valid but not minimal diff (`lcs/old.go:29-33`) | Version 0.x; its hunk type carries start lines only, so counts are derived by the wrapper; `unified.go` carries a comment that one step is quadratic in the number of edits | v0.4.2, released 2026-11-15; two licence files, BSD 3-Clause (Go Authors) and MIT (Ayman Bagabas); no requirements; no cgo. Checked with `go list -m -json`, `go mod download -json` and by reading `LICENSE-BSD`, `LICENSE-MIT`, `README.md`, `ndiff.go`, `unified.go` and `lcs/old.go` in the module cache, 2026-11-18. |
| `github.com/sergi/go-diff` | Widely used; has a timeout option | Works in characters; line mode is a workaround that maps lines to characters; does not build hunks | v1.4.5, 2026-11-17, MIT (an Apache 2.0 file is also present); same method |
| `github.com/pmezard/go-difflib` | Prints unified diffs | Last release 2016; a different, non-minimal algorithm | v1.0.0, 2016-01-10, BSD 3-Clause; same method |
| Own Myers implementation | Full control of the bound and the hunk shape | About 200 lines of well-known but easy-to-get-wrong code to own | not applicable |
| Not used (a parser, for the withdrawn R6): `github.com/bluekeyes/go-gitdiff` | Parses Git headers into fields (`IsNew`, `IsDelete`, `IsRename`, `IsBinary`, `OldMode`, `NewMode`, `OldOIDPrefix`, `NewOIDPrefix`); can also apply | Version 0.x | v0.9.0, 2026-07-18, MIT, no requirements, no cgo; same method, fields read in `gitdiff/gitdiff.go` |
| `github.com/sourcegraph/go-diff` | Mature parser and printer of unified diffs | Git's extended header lines are left as raw strings, which would keep upstream's regular expressions | v0.10.0, 2026-11-12, MIT; same method |

**Tradeoff.** One small 0.x module whose output must be pinned by golden tests, because
a change in how it breaks ties would change what is stored and shown.

## 6. Design notes

**Owning package.** `textdiff`, a harness package with no cordis dependency.

```go
type Hunk struct {
    OldStart, OldLines, NewStart, NewLines int
    Lines []Line // Kind: Equal, Delete, Insert; Text without the line ending
}

type Result struct {
    Hunks          []Hunk
    Coarse         bool // the bound was exceeded; one whole-replacement hunk
    Added, Deleted int
}

func Lines(before, after string, context int, limits Limits) Result
func (h Hunk) BeforeAfter() (before *string, after string) // R4
```

- **Line endings.** Both inputs are normalised the way upstream's callers do it: every
  line is treated as terminated, and the final-newline fact is carried as a flag on the
  result, not as a line (`DH-LIB-DIFF-R3`).
- **Hunks from edits.** The wrapper builds hunks from line-index edits and counts lines
  per side itself. If a benchmark shows the module's quadratic step matters at the size
  limit, the wrapper can take line edits from the module's `lcs` package directly;
  callers are unaffected.
- **Persisted form.** What is stored with a tool result is the before/after pair per
  hunk, as upstream stores it. The card is rendered from the stored pair, so the diff
  module is not needed to replay a session, and a later module change cannot alter
  history.

**Failure, retry and cleanup.** `Lines` cannot fail: it returns hunks, a coarse result,
or nothing. The
package holds no resources, so there is nothing to release and no fiber owns anything.

**Verdict on upstream's design.** Keep three context lines, the coarse fallback and the
before/after persisted form. Change the bound from time to size. Drop the browser-side
diff.

## 7. Risks and open questions

- **Non-minimal diffs.** Above the module's search depth a diff is valid but can show
  more changed lines than strictly necessary. Acceptable for display; the wrapper's
  golden tests record the behaviour so that a change is noticed.
- **The Git patch parser was withdrawn.** The code-host UI shows status only
  ([DH-ADR-38](../design.md#dh-adr-38)), so nothing reads patch text. The library item
  for `github.com/bluekeyes/go-gitdiff`, the requirements R5 and R6 and their two tests
  were removed from this document and from the ledger. If a diff of a branch against
  its fork point returns later, the module facts in the options table are the starting
  point.
- **Owning area.** The `textdiff` package is delivered by area A08
  ([../areas/filesystem.md](../areas/filesystem.md), `DH-D-FS-2`).

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-DIFF-1` | local | `DH-LIB-DIFF-R1`, `R4` | Golden tests: insertion, deletion, replacement, two separate changes, change at the first and last line, identical texts. Hunk ranges and before/after pairs match the committed files. |
| `DH-T-DIFF-2` | local | `DH-LIB-DIFF-R2` | Inputs just under and just over each limit give a fine and a coarse result. The same inputs run 100 times in parallel give identical results. A fuzz test asserts that applying the hunks to `before` always yields `after`. |
| `DH-T-DIFF-3` | local | `DH-LIB-DIFF-R3` | Pairs that differ only in the final newline, and an empty file on either side. |

Local tests only. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-diff`, `UP-DEBT-found.L9.3`.
