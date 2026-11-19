# @anthropic-ai/sdk: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The Go module named here was checked on 2026-11-18 with
> `go list -m -json` and `go mod download -json`, reading `LICENSE` and `go.mod` in the
> module cache.

| | |
|---|---|
| **Library ID** | `DH-LIB-ANTHROPICSDK` |
| **Upstream unit** | `UP-DEP-@anthropic-ai/sdk` |
| **Upstream version** | 0.93.0, pinned exactly (`packages/subagent/subagent-claude-code/package.json:52`) |
| **Kind** | runtime, declared but never imported |
| **Used by** | subagent (`packages/subagent/subagent-claude-code`) |
| **Go choice** | dropped: nothing replaces it |
| **Related areas** | subagent area (the Claude Code provider), model provider area. |

## 1. What it is and why upstream needs it

`@anthropic-ai/sdk` is Anthropic's JavaScript client for its Messages API: the library a
program uses to send a prompt to a Claude model and read the answer.

Upstream does not use it for that. The harness reaches Anthropic models through the
multi-provider library ([earendil-works-pi.md](earendil-works-pi.md)), which brings its own
copy at a different version (`pnpm-lock.yaml:29188`). The copy this unit describes is
declared by the Claude Code subagent provider, a package that delegates a task to the
Claude Code program through a second library, the Claude Agent SDK
(`packages/subagent/subagent-claude-code/package.json:3`). That second library names
`@anthropic-ai/sdk` as a **peer dependency**: an npm rule that says "whoever installs me
must also install this" (`pnpm-lock.yaml:20915-20916`). The provider therefore lists it,
at the lowest version the rule accepts, and never calls it.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/subagent/subagent-claude-code/package.json:52` | Satisfying the peer-dependency rule of `@anthropic-ai/claude-agent-sdk` 0.3.263 | none |
| `packages/subagent/subagent-claude-code/src/process.ts:12`, `src/run.ts:17` | These are the package's only Anthropic imports, and both import the Agent SDK, not this library | none |

Search performed: every `.ts`, `.tsx`, `.js`, `.mjs`, `.json`, `.yml` and `.py` file under
`packages/`, `apps/`, `python/`, `scripts/` and `vendor/` for the package name. The only
hits outside lockfiles are the manifest line above. There is no patch, pin comment or
install-script entry for it in `pnpm-workspace.yaml`. The declared-but-unused state is
recorded as debt `UP-DEBT-found.L2.2` in [earendil-works-pi.md](earendil-works-pi.md).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-ANTHROPICSDK-R1` | Delegating a task to Claude Code needs no Anthropic API client in the harness. The child program makes its own model requests with its own settings and credentials; the harness only starts it, passes the task and reads the result. | `packages/subagent/subagent-claude-code/README.md:56`, `:112`, `:138` |

## 5. Go choice

**Decision.** Dropped. The Go harness requires no Anthropic API client module.

**Why.** The dependency exists only because of an npm installation rule. Go has no peer
dependencies: a module's requirements are the packages its code imports. The two real
needs near this unit are met elsewhere. Calling Anthropic models is done by the harness's
own Anthropic Messages driver
([DH-ADR-PIAI-1](earendil-works-pi.md#dh-adr-piai-1)). Running Claude Code as a subagent
is a child process, which the subagent area designs with the Agent SDK unit.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** no module | Nothing to maintain | none | not applicable |
| `github.com/anthropics/anthropic-sdk-go` | The vendor's typed client | Not needed for delegation; rejected for model calls in [earendil-works-pi.md](earendil-works-pi.md), option B | v1.84.0 (2026-10-20), MIT, `go 1.24`; its `go.mod` requires AWS, Google API, OpenTelemetry and MCP modules |

**Tradeoff.** None for this unit. The cost of not using the vendor client for model calls
is recorded in [earendil-works-pi.md](earendil-works-pi.md).

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-ANTHROPICSDK-1` | local | `DH-LIB-ANTHROPICSDK-R1` | A check over the harness module's import graph fails if any package of an Anthropic API client module is linked into the binary. |

Local check only. That delegation to a real Claude Code program works is a
real-environment scenario owned by the subagent area. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@anthropic-ai/sdk`.
