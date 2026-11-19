# @eslint-community/regexpp: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". The schema design this belongs to is in
> [zod.md](zod.md) and [ajv.md](ajv.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-REGEXPP` |
| **Upstream unit** | `UP-DEP-@eslint-community/regexpp` |
| **Upstream version** | 4.12.2 |
| **Kind** | runtime, loaded on demand |
| **Used by** | `boot` (`app-boot`) |
| **Go choice** | standard library: `regexp` and `regexp/syntax` |
| **Related areas** | application boot (`areas/boot.md`); schema foundation ([zod.md](zod.md)) |

## 1. What it is and why upstream needs it

`regexpp` parses a regular expression into a syntax tree without running it. It is not
used on model-written programs.

Upstream exports a JSON Schema document that describes a composed profile, so an editor
can check a config file. Config rules are written as JavaScript regular expressions;
JSON Schema's `pattern` keyword is read with Unicode semantics. The same pattern text
can accept different strings under the two readings (for example `.` against a
character outside the Basic Multilingual Plane). Upstream parses each pattern both ways
and emits it only when the two trees are equal and every part is in a known-portable
subset; otherwise the exported schema marks the rule as only partly described.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/boot/app-boot/src/config-schema/pattern.ts:46-62` | Parse a pattern in Unicode and non-Unicode mode and compare the trees | `new RegExpParser().parsePattern(source, 0, len, { unicode })` |
| `pattern.ts:5-34` | Classify tree nodes as portable or not | `AST` node types |
| `packages/boot/app-boot/src/config-schema/projector.ts:152` | The one caller: the config-schema exporter | `createPatternCheck()` |

No patch, pin or exemption names the package (`patches/` and `pnpm-workspace.yaml`
searched). The schema foundation already records the surrounding debt and drops the
exporter and its pattern checker (`UP-DEBT-found.F3.7` in [ajv.md](ajv.md), resolved by
[DH-ADR-SCHEMA-2](zod.md#dh-adr-schema-2)).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-REGEXPP-R1` | A pattern declared on a harness config type is compiled when the schema is derived; a pattern Go cannot compile is a start-up error that names the type and field. | `pattern.ts:56-59`; [zod.md](zod.md) |
| `DH-LIB-REGEXPP-R2` | A pattern that the harness exports in a JSON Schema document accepts the same strings when a JavaScript-based reader (an editor, a browser form) evaluates it. Patterns outside a portable subset are refused at derivation, not exported with a different meaning. | `pattern.ts:41-45` |

## 5. Go choice

**Decision.** The standard library. `regexp.Compile` gives `DH-LIB-REGEXPP-R1`.
`regexp/syntax.Parse` returns a syntax tree; a small own check walks it and refuses
constructs that Go and JavaScript read differently. Verdict on upstream's two-parse
comparison: **drop** the mechanism, **keep** the guarantee in Go form.

**Why.** In Go the mismatch moves: the harness writes patterns for Go's engine (RE2
syntax), and the exported document is read by tools that use JavaScript's engine.
Inference: the constructs to refuse are the ones only RE2 has or reads differently:
inline flags such as `(?i)`, `(?P<name>...)` groups, `\z`, `\Q...\E`, POSIX classes such
as `[[:alpha:]]`, and `\pN` one-letter Unicode classes. The check is a tree walk of a
few dozen lines over a standard-library type.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`regexp` and `regexp/syntax`** (chosen) | No dependency; the parser is the one that will run the pattern | The portable subset is our own list and must be reviewed | Standard library; nothing to verify as a module |
| A Go ECMAScript regular-expression engine | Could accept JavaScript-only patterns such as look-ahead | A second regex dialect in the harness; [zod.md](zod.md) already decides harness patterns are written for Go | Not examined |
| No check | Least work | An editor could accept a value the host then rejects, or the reverse | |

**Tradeoff.** Harness authors cannot use RE2-only conveniences in patterns that are
exported. Whether `DH-LIB-REGEXPP-R2` is wanted at all is for the schema owner: it adds
a rule that [zod.md](zod.md) does not state today.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-REGEXPP-1` | local | `DH-LIB-REGEXPP-R1`, `DH-LIB-REGEXPP-R2` | Deriving a schema for a type with an invalid pattern fails naming type and field; a table of patterns (anchors, classes, quantifiers accepted; inline flags, named groups in RE2 form, POSIX classes refused) gives the expected verdicts. |

Nothing here has been run. A real-environment check (a browser form evaluating an
exported pattern) belongs to the UI area if `DH-LIB-REGEXPP-R2` is adopted.

## 9. Units dispositioned by this spec

`UP-DEP-@eslint-community/regexpp`.
