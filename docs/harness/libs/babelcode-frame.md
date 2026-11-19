# @babel/code-frame: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-CODEFRAME` |
| **Upstream unit** | `UP-DEP-@babel/code-frame` |
| **Upstream version** | 7.29.7 |
| **Kind** | runtime |
| **Used by** | `boot` (`hmr`), `vendor` (`vendor/hmr`, the same file) |
| **Go choice** | own code: one small function |
| **Related areas** | A21, whose rejected-layer diagnostic uses it ([../areas/boot.md](../areas/boot.md)); A24, whose `dsh tool call` command uses it for a malformed argument document ([../areas/app-cli.md](../areas/app-cli.md)); A23, whose leaf utility packages hold the function ([../areas/util-test-support.md](../areas/util-test-support.md)) |

## 1. What it is and why upstream needs it

A **code frame** is the short excerpt a compiler prints under an error: a few source
lines, line numbers, and a caret under the offending column. `@babel/code-frame` builds
that excerpt, with syntax colouring.

Upstream uses it in one place: hot module replacement (HMR), the development feature
that recompiles and reloads a plugin when its source file changes. When the recompile
fails, the error carries a file, a line and a column, and the handler logs a code frame
so the developer sees where. It is not used on model-written programs; those errors
are formatted by hand (`packages/ptc-runtime/ptc-runtime-node/src/evaluators/vm-script-await.ts:214-217`).

The Go harness has no embedded interpreter ([DH-ADR-8](../design.md#dh-adr-8)), so it
never parses a model-written script and reports no syntax error in one: the shell or
language that runs the script does. What the harness still parses, and can point into,
is configuration and the JSON argument document given to the `dsh tool call` command.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/boot/hmr/src/error.ts:3`, `:28-36` | Read the failing file and log an excerpt at the error's line and column, with the error text as the message | `codeFrameColumns(source, { start: { line, column } }, { highlightCode: true, message })` |
| `vendor/hmr/src/error.ts:3` | The vendored original of the same file | same |

No patch, pin or exemption names the package (`patches/` and `pnpm-workspace.yaml`
searched).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CODEFRAME-R1` | A diagnostic that carries a position (file or text, line, column) can be shown as the offending line, a caret under the column and the message. | `error.ts:28-36`; [DH-O-2](../design.md#dh-o-2) ("rejected with file, line and column") |
| `DH-LIB-CODEFRAME-R2` | The excerpt is plain text: no colour codes are written to a log, a file or a model-visible result. | Inference: the Go consumers are the log, the UI and the model, not a developer terminal |

## 5. Go choice

**Decision.** Own code: one function of a few dozen lines in `internal/util`, with no
syntax colouring. Verdict: **drop** the library and its only caller (hot reload of code
is dropped by [DH-ADR-LOADER-8](cordis-plugins.md#dh-adr-loader-8)); **keep** the idea
for the two places Go has positioned errors.

**Why.** The original caller disappears, but the need does not. A rejected config layer
must name file, line and column (DH-O-2), and an argument document that is not valid
JSON is refused by the `dsh tool call` command with line, column and the offending
line, before anything is sent or logged
(`DH-LIB-PTCGUEST-R12` in [ptc-guest-runtime.md](ptc-guest-runtime.md)). Splitting text
into lines and padding a caret needs no dependency. Syntax colouring, the only complex
part of the library, has no reader in the Go design.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Own function** (chosen) | No dependency; identical output for config errors and argument-document errors | Must handle tabs and wide characters itself | Standard library only (`strings`, `unicode/utf8`) |
| Rely on each producer's own message (the YAML decoder, `encoding/json`) | No code | Formats differ by producer; `encoding/json` gives a byte offset, not a line and column | Depends on modules chosen elsewhere |
| Nothing | Least work | A model or an operator gets "line 14" with no line to look at | |

**Tradeoff.** No colour. Columns for tabs and double-width characters are approximate
unless the function measures display width.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CODEFRAME-1` | local | `DH-LIB-CODEFRAME-R1`, `DH-LIB-CODEFRAME-R2` | Table test: first line, last line, a column past the end, a tab-indented line, an empty file; output contains the line, a caret at the column, the message, and no escape character. |

Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@babel/code-frame`.
