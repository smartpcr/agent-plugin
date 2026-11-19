# shell-quote: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18 with
> `go list -m -json <module>@latest`, `go mod download -json` and by reading the files in
> the module cache; nothing was compiled or run.

| | |
|---|---|
| **Library ID** | `DH-LIB-SHELLQUOTE` |
| **Upstream unit** | `UP-DEP-shell-quote` |
| **Upstream version** | 1.12.0 |
| **Kind** | runtime |
| **Used by** | experimental (`packages/experimental/forge-session`) |
| **Go choice** | named Go module `mvdan.cc/sh/v3` v3.14.1 (its `syntax` package) for reading a command line; own code for quoting |
| **Related areas** | forge integrations |

## 1. What it is and why upstream needs it

shell-quote does two opposite things with POSIX shell text. `parse` splits a command
line into words and operators the way a shell would. `quote` turns a list of words into
one string that a shell will read back as the same list.

`forge-session` connects a session to the pull request or merge request ("change
request") the agent created. It learns about one by **reading the shell commands the
agent already ran**, for example `gh pr create` or `GH_REPO=a/b gh pr view 12`. It
never runs the parsed text. From what it read it sometimes builds a new command, a
*lookup*, that is run later in the same shell environment to find the change request's
address. Reading uses `parse`; building the lookup uses `quote`.

The second of these is a safety boundary: the words come from a command the model wrote,
and the result is executed by a shell.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/forge-session/src/references.ts:31-39` | Split a logged command into tokens. Before parsing, a line with a newline, a backquote, `$(` or unbalanced quotes is discarded by regular expressions. Any variable reference makes the parse fail on purpose | `parse(command, env)` from `shell-quote/parse.js`, with an `env` callback that throws |
| `references.ts:20-21`, `:41-70` | Walk the tokens: words are collected; `&&`, `\|\|`, `;`, `\|`, `\|&` and `&` end a simple command; `2>&1` is tolerated; any other operator marks the command as unsupported; a pipe into `head`, `tail` or `cat` still counts as running alone | token shapes: string, `{ op }` |
| `packages/experimental/forge-session/src/commands.ts:155` | Build the lookup: `env`, the command's own variable assignments, then the lookup words, as one shell string | `quote(words)` from `shell-quote/quote.js` |

## 3. Tech debt upstream carries

Search performed: every import of `shell-quote`; `patches/` and `pnpm-workspace.yaml` (no
patch, pin or exemption). No `UP-DEBT` unit is assigned. One inconsistency is worth
recording because it decides the Go choice: upstream's dependency audit **rejected**
shell-quote for quoting in its shell tools, as "a maintenance-mode package with a CVE
history" at "a safety boundary", and kept one-line single-quoting helpers
(`.agents/notes/rejected/simplification/2026-07-26-dependency-swaps-rejected-by-nih-audit.md:46`;
one such helper is `packages/subprocess/subprocess-local/src/shell-activity.ts:7`). Then
`forge-session` adopted the same package for the same job. The Go design follows the
audit, not the exception.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SHELLQUOTE-R1` | A logged command line is split into its simple commands, each with its leading variable assignments and its words. Only literal words count: a command containing a variable, a command substitution, arithmetic, a glob, a here-document or a redirection other than `2>&1` is reported as not analysable. The reader never guesses and never executes anything | `references.ts:23-39`, `:47-70` |
| `DH-LIB-SHELLQUOTE-R2` | The separators `&&`, `\|\|`, `;`, `\|`, `\|&` and `&` are recognised, and the reader reports whether a command runs alone on its line (a following pipe into `head`, `tail` or `cat` is allowed) | `references.ts:20-21`, `:41-46` |
| `DH-LIB-SHELLQUOTE-R3` | A list of strings is turned into one POSIX shell string that a shell reads back as exactly that list, for every string without a NUL byte | `commands.ts:155` |

## 5. Go choice

**Decision.** Reading: `mvdan.cc/sh/v3/syntax`, a full shell parser that returns a syntax
tree. Quoting: an own function that wraps each word in single quotes and writes an
embedded single quote as `'\''`, the same rule as upstream's in-house helper.

**Why.** Upstream's reader is a tokenizer guarded by two regular expressions that try to
exclude everything the tokenizer would get wrong. A parser removes the guessing: the
tree says directly whether a word is literal (`Lit`, `SglQuoted`, or `DblQuoted` holding
only literals) or dynamic (`ParamExp`, `CmdSubst` and the rest), where each simple
command begins and ends (`CallExpr` with `Assigns` and `Args`), and which operator joins
them (`BinaryCmd`, `Stmt.Background`, `Stmt.Redirs`). Quoting needs no library: single
quoting is total and three lines long, and a safety boundary should be code the harness
can read in full.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`mvdan.cc/sh/v3/syntax` for reading, own single-quote function for writing** (chosen) | Exact classification of words and operators; actively maintained | A large module for one small feature; only the `syntax` package is imported | v3.14.1, 2026-09-06, BSD-3-Clause, no cgo. `syntax.NewParser` (`syntax/parser.go:227`), `Parser.Parse` (`:243`), and also `syntax.Quote` (`syntax/quote.go:48`). `Quote` is not used: for a string with a non-printable character, a newline for example, it returns an error in POSIX mode and a `$'...'` form in Bash mode (`quote.go:79-84`), while plain single quoting handles every string without a NUL byte in every POSIX shell |
| `github.com/google/shlex` | Tiny | Splits words only and discards operators, so `a && b` cannot be told from one command (`R2` fails); last commit 2019-12-02 | pseudo-version of 2019-12-02, Apache-2.0 |
| `github.com/kballard/go-shellquote` | Tiny; has both `Split` and `Join` | No operators (`R2` fails); last commit 2018-04-28 | pseudo-version of 2018-04-28, MIT |
| Own tokenizer | No module | Repeats upstream's fragile part | n/a |

**Tradeoff.** The module's `go.mod` lists test-only requirements (`creack/pty`,
`go-quicktest/qt` and others). Go builds only imported packages, so they do not enter the
binary, but they do appear in `go.sum`.

**Verdict on upstream.** *Keep* the behaviour ("recognise only what is certainly
literal"). *Change* the mechanism for reading. *Drop* the library for quoting, as
upstream's own audit recommends.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-SHELLQUOTE-1` | local | `DH-LIB-SHELLQUOTE-R1`, `R2` | A table of command lines: plain commands, leading assignments, each separator, `2>&1`, a pipe into `head`, and one line for every excluded form (variable, `$(...)`, backquotes, glob, here-document, other redirections, unbalanced quotes, a newline). Each yields the expected simple commands or "not analysable" |
| `DH-T-SHELLQUOTE-2` | local, property test | `DH-LIB-SHELLQUOTE-R3` | For generated lists of strings containing quotes, spaces, `$`, backquotes, newlines and non-ASCII text: quote, then parse with the same parser, and require the same list back. Run additionally against a real `sh` where one is available |

Nothing has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-shell-quote`
