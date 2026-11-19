# acorn and acorn-walk: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". The decision this depends on is in
> [ptc-guest-runtime.md](ptc-guest-runtime.md).
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner decided
> that the harness has no embedded interpreter, so the reasons below no longer refer to
> a JavaScript engine.

| | |
|---|---|
| **Library ID** | `DH-LIB-ACORN` |
| **Upstream units** | `UP-DEP-acorn`, `UP-DEP-acorn-walk` |
| **Upstream version** | `acorn` 8.18.0, `acorn-walk` 8.3.5 |
| **Kind** | runtime, one family (parser and its tree walker) |
| **Used by** | `ptc-runtime` (`ptc-runtime-node`), `experimental` (`webworker-runtime`, `acorn` only) |
| **Go choice** | dropped: the Go harness neither parses, rewrites nor evaluates JavaScript |
| **Related areas** | programmatic tool calling (`areas/ptc-workflow.md`) |

## 1. What it is and why upstream needs it

`acorn` turns JavaScript text into a syntax tree. `acorn-walk` visits the nodes of that
tree. Upstream uses them to **rewrite** a model-written program before running it. They
do not check a program for safety.

The rewriting exists because of two limits of Node's `vm` module, which evaluates the
program. A `vm` script cannot contain `await` at the top level, and a retained
JavaScript scope refuses a second `const x` after an earlier one. Upstream works around
the first with a port of Node's own REPL transform, and around the second with a
"scoped REPL" that copies top-level names from one call into the next.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/ptc-runtime/ptc-runtime-node/src/evaluators/vm-script-await.ts:193-199` | Parse the program wrapped in an async arrow function | `Parser.parse(src, { ecmaVersion: 'latest' })` |
| `vm-script-await.ts:49-164`, `:250` | Walk the tree: hoist top-level declarations, turn them into assignments, box the last expression so its value is returned | `walk.recursive`, `walk.base` |
| `vm-script-await.ts:200-224` | Turn a parse failure into a message with the source line and a caret | `SyntaxError.pos`, `.loc` |
| `packages/ptc-runtime/ptc-runtime-node/src/evaluators/scoped-repl.ts:55-120` | Parse once and list top-level names, a leading `"use strict"` and the final expression | `parse(code, { sourceType: 'script', allowAwaitOutsideFunction: true })`, `getLineInfo` |
| `packages/experimental/webworker-runtime/src/compile/transform.ts:658`, `:704` | Parse ES modules that the image packer rewrites to CommonJS for the browser Web Worker runtime | `parse` |

Both private process bundles ship the two libraries inside the child
(`packages/ptc-runtime/ptc-runtime-node/README.md:169`).

## 3. Tech debt upstream carries

No patch, pin or exemption names either package (`patches/` and `pnpm-workspace.yaml`
searched). The debt is in what the rewriting does to the language, admitted in
`packages/ptc-runtime/ptc-runtime-node/README.md:251-253`: rewritten programs lose the
normal rules for `let`, `const` and `class`, error locations refer to rewritten text,
and a program containing the text `__$binding_` is refused. These bullets are units of
the programmatic-tool-calling area; their cause is removed by
[DH-ADR-PTCGUEST-4](ptc-guest-runtime.md#dh-adr-ptcguest-4).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-ACORN-R1` | The text a script's interpreter receives is the text the model submitted, byte for byte. The harness does not parse it, strip types from it or transform it, so declarations, scoping and error positions are the interpreter's own. | README `:251-253` |
| `DH-LIB-ACORN-R2` | No JavaScript parser and no JavaScript engine is linked into the `dsh` binary. | [DH-ADR-PTCGUEST-1](ptc-guest-runtime.md#dh-adr-ptcguest-1) |

Upstream also turns a parse failure into a positioned message
(`vm-script-await.ts:214-217`). In the Go design a script's syntax errors come from the
interpreter that runs it; the one document the harness parses for a script is the
argument object given to the tool command, and that error is positioned
(`DH-LIB-PTCGUEST-R12` in [ptc-guest-runtime.md](ptc-guest-runtime.md)).

## 5. Go choice

**Decision.** Dropped. No JavaScript parser is a dependency of the Go harness. Verdict on
upstream's source rewriting: **drop**.

**Why.** Both transforms compensate for `node:vm`. The owner decided that the Go harness
has no embedded interpreter
([DH-ADR-PTCGUEST-1](ptc-guest-runtime.md#dh-adr-ptcguest-1)): a model-written program
is an ordinary script, run by whatever interpreter it names as a sandboxed command, and
it calls tools through the `dsh tool` command. Nothing in the harness evaluates
JavaScript, so there is nothing to lower, nothing to copy between calls and nothing to
parse.

| Option | Why it was or was not taken | Verified facts |
|---|---|---|
| **No parser in the harness** (chosen) | No dependency; a script's language rules are its interpreter's | Nothing to verify |
| A Go JavaScript parser to port both transforms (`github.com/dop251/goja/parser`, or esbuild's parser) | Not taken: the transforms serve an embedded JavaScript engine, which the owner ruled out | `github.com/dop251/goja` checked 2026-11-18 (MIT, no cgo) |
| Keep `acorn` inside a JavaScript bootstrap for an external Node runtime | Not taken: no Node runtime is used for model-written programs | none |

**Tradeoff.** The harness cannot improve a script's error messages or keep names alive
between two scripts; a script that wants to keep a value writes it to a file.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-ACORN-1` | local | `DH-LIB-ACORN-R1`, `DH-LIB-ACORN-R2` | A script with unusual text (a shebang line, the string `__$binding_`, a top-level `await`, non-ASCII characters) is run through the shell tool by a stand-in interpreter that records what it received: the bytes equal the submitted text, and the logged outer call holds the same bytes. The import graph of the `dsh` binary contains no JavaScript parser or engine package. |

Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-acorn`, `UP-DEP-acorn-walk`.
