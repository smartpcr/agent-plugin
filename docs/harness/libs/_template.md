# <library name>: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-<KEY>` |
| **Upstream unit** | `UP-DEP-<name>` (copy the ID from your assignment file) |
| **Upstream version** | as resolved in `pnpm-lock.yaml` |
| **Kind** | runtime / build-only / test-only / bundled into the client / vendored / native / Node built-in |
| **Used by** | upstream package groups that import it |
| **Go choice** | standard library / named Go module / own code / dropped |
| **Related areas** | links to `../areas/<area>.md` |

## 1. What it is and why upstream needs it

Two or three plain-English paragraphs. Say what problem the library solves for the
harness, not only what the library is. Define any term a reader new to the system
would not know.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|

List every importing package, and the part of the library's API that is used. A
library that is imported widely but only for one function is a small requirement.

## 3. Tech debt upstream carries

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|

Cover patches under `patches/`, version pins and overrides with their stated reasons,
install-script exemptions, workarounds in calling code, and limitations the package
READMEs admit. Every `UP-DEBT` unit assigned to you must appear here. "None found" is
a valid result only after the search is described.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-<KEY>-R1` | observable behaviour the Go side must provide | use site or upstream decision |

Requirements describe behaviour, not the upstream API shape.

## 5. Go choice

**Decision.** What is used in Go.

**Why.** The reason, tied to the requirements above.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| chosen option | | | module path, version, licence, last release date, how it was checked |
| alternative | | | |

A Go module is named only when its existence, licence and maintenance were checked
(pkg.go.dev or its repository). Record what was checked and when. If it could not be
checked, write **open** and say what is missing.

**Tradeoff.** What is given up by this choice.

## 6. Design notes

The Go API shape the harness needs (interfaces, key types), which harness package owns
it, and what happens on failure, retry and cleanup. Say which fiber owns each resource
the library creates (process, socket, file handle, watcher) so that it is released when
that fiber unloads.

## 7. Risks and open questions

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-<KEY>-1` | local | `DH-LIB-<KEY>-R1` | what is asserted |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

List the unit IDs for which this spec wrote a ledger row.
