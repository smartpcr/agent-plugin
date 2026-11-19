# eventsource-parser: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go modules named here were checked on 2026-11-18 with
> `go list -m -json <module>@latest` and `go mod download -json`, reading `LICENSE` and
> source in the module cache.

| | |
|---|---|
| **Library ID** | `DH-LIB-EVENTSOURCEPARSER` |
| **Upstream unit** | `UP-DEP-eventsource-parser` |
| **Upstream version** | 3.1.0 (`pnpm-lock.yaml:25320`) |
| **Kind** | runtime |
| **Used by** | llm (`packages/llm/llm-deepseek`), one import |
| **Go choice** | own code in the harness on the standard library (`bufio`, `bytes`, `io`) |
| **Related areas** | model provider area; the provider drivers in [earendil-works-pi.md](earendil-works-pi.md) |

## 1. What it is and why upstream needs it

Server-sent events (SSE) is the text format model providers use to stream a response over
one HTTP connection. The body is a sequence of lines. `data:` lines carry the payload, an
optional `event:` line names the event, a line that starts with `:` is a comment (providers
send comments as keep-alive signals), and a blank line ends one event. Getting the framing
right has small traps: three kinds of line ending, payloads split over several `data:`
lines, an optional byte-order mark at the start, and a last event that was cut off.

`eventsource-parser` does that framing and nothing else. Upstream's first-party DeepSeek
adapter uses it to turn the response body into framed events, then parses each payload as
JSON itself (`packages/llm/llm-deepseek/src/sse.ts:13-27`). Upstream first wrote this
parser by hand (about 67 lines plus 108 lines of tests) and replaced it with the library
because the library was already installed through the MCP SDK and "a ~700-byte SSE
micro-parser is transport plumbing, not the design under verification"
(`.agents/notes/archived/simplification/2026-07-26-eventsource-parser-for-deepseek-sse.md:10-12`,
`:22`). Upstream also declined the same swap elsewhere because "only ~15 lines of framing
are deletable"
(`.agents/notes/rejected/simplification/2026-07-26-dependency-swaps-rejected-by-nih-audit.md:21`).
The requirement is small either way.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/llm/llm-deepseek/src/sse.ts:3`, `:14` | Frame the provider's response body into events | `EventSourceParserStream` from `eventsource-parser/stream`, after a `TextDecoderStream` |
| `packages/llm/llm-deepseek/src/sse.ts:14` | Treat a keep-alive comment as activity for the idle timer | the `onComment` option |
| `packages/llm/llm-deepseek/src/sse.ts:18`, `:22` | Read the payload and the event name of each frame; the JSON check and the name-versus-payload check are upstream's own code | `frame.data`, `frame.event` |

The library is also present indirectly through the MCP packages
(`pnpm-lock.yaml:30170`, `:30195`); that use belongs to the MCP library specification.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-EVENTSOURCEPARSER-R1` | A byte stream in SSE format is delivered as events with a name and a payload. Line endings LF, CRLF and CR are accepted; several `data:` lines join with a newline; one space after the colon is removed; a leading byte-order mark is ignored; comments and unknown fields produce no event and do not disturb the events around them. | `packages/llm/llm-deepseek/src/sse.ts:14-16`; archived note `:10`, `:18` |
| `DH-LIB-EVENTSOURCEPARSER-R2` | An event that was not closed by a blank line before the stream ended is not delivered. The reader reports the end of input; deciding that the response was cut short is the driver's job. | `packages/llm/llm-deepseek/src/sse.ts:8`; archived note `:28` |
| `DH-LIB-EVENTSOURCEPARSER-R3` | Memory is bounded: one event larger than a configured limit ends the stream with a distinct error instead of growing a buffer without limit. The default is large enough for a protocol that sends a whole response in one event. | archived note `:18` (`maxBufferSize`) |
| `DH-LIB-EVENTSOURCEPARSER-R4` | The reader knows nothing about any provider: no JSON parsing, no end-of-stream sentinel, no event-name rules. | `packages/llm/llm-deepseek/src/sse.ts:17-25` keeps these in adapter code |

## 5. Go choice

**Decision.** A small SSE reader written in the harness on `bufio`, in the transport
package shared by every provider driver. No module.

**Why.** The four requirements are a line scanner and a few field rules. Inference: about a
hundred lines of Go and a table-driven test. Upstream's reason for taking a library was
that it cost nothing extra because it was already installed; in Go nothing equivalent is
already in the build. The reader also sits on the path of every model response, so the
harness wants to set its limits and its end-of-input behaviour itself (R2, R3). Upstream's
`onComment` hook is not needed: the idle timer watches bytes arriving on the response
body (`DH-LIB-PIAI-R8` in [earendil-works-pi.md](earendil-works-pi.md)), which sees
comments without the reader reporting them.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** own reader on `bufio` | No dependency; limits and truncation behaviour are the harness's; one place to test | The harness owns the format rules | Standard library only |
| `github.com/tmaxmax/go-sse` | Maintained; no dependencies of its own; reads from any `io.Reader` | Version 0.x, so the API may change; the default limit for one event is 64 KB (`event.go:28`) and must be raised; the public reader does not report comments (`event.go:83`); most of the module is an SSE server and client the harness would not use | v0.12.0 (2026-11-06), MIT, `go 1.26`, empty requirement list in `go.mod`; no `import "C"` in its source |
| `github.com/r3labs/sse/v2` | Widely known | Last release nearly four years ago | v2.10.0 (2023-01-18); existence and date checked only, licence not read |

**Tradeoff.** The harness must get the format's corner cases right itself.
`DH-T-EVENTSOURCEPARSER-1` covers the ones upstream used to test by hand before it took the
library. If the reader ever needs more than framing, `go-sse` is the fallback and is
already checked.

**Upstream verdict.** *Keep* the split upstream ended with: framing is generic, protocol
meaning stays in the driver. *Change* library to own code, for the reason above.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-EVENTSOURCEPARSER-1` | local | `DH-LIB-EVENTSOURCEPARSER-R1`, `DH-LIB-EVENTSOURCEPARSER-R4` | A table of byte inputs and expected events: each line ending, multi-line payloads, a field with no space after the colon, a byte-order mark, comments between and inside events, unknown fields, and the same inputs delivered one byte at a time. A payload that is not JSON and a payload equal to a sentinel string are delivered unchanged. |
| `DH-T-EVENTSOURCEPARSER-2` | local | `DH-LIB-EVENTSOURCEPARSER-R2` | Input that ends in the middle of an event, and input whose last event lacks its blank line, deliver the earlier events and then end of input with no further event. A read error from the underlying reader is returned as that error. |
| `DH-T-EVENTSOURCEPARSER-3` | local | `DH-LIB-EVENTSOURCEPARSER-R3` | An event one byte over the configured limit returns the size error; an event at the limit is delivered; allocation stays bounded for an endless line. |

Local tests only: they prove the reader against written inputs. Behaviour against a real
provider's stream is proved by the drivers' real-environment scenarios, which the model
provider area owns. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-eventsource-parser`.
