# @noble/hashes: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Short form: sections 1, 2, 4, 5, 8 and 9 of the template.

| | |
|---|---|
| **Library ID** | `DH-LIB-NOBLEHASHES` |
| **Upstream unit** | `UP-DEP-@noble/hashes` |
| **Upstream version** | 2.4.0, declared as `^2.4.0` (`packages/experimental/webworker-runtime/package.json`, `pnpm-lock.yaml:22596`) |
| **Kind** | runtime, bundled into a browser worker |
| **Used by** | experimental (`packages/experimental/webworker-runtime`), one importing file |
| **Go choice** | standard library (`crypto/sha1`, `crypto/sha256`, `crypto/sha512`); the reason the library exists does not arise in Go |
| **Related areas** | the experimental area that owns the browser-worker runtime; hashing in [node-crypto-timers-perf-hooks.md](node-crypto-timers-perf-hooks.md) |

## 1. What it is and why upstream needs it

`@noble/hashes` is a hash library written in plain JavaScript. A **hash** (or digest) is
a short fixed-size fingerprint of some bytes.

Upstream has an experimental package that runs the whole host inside a browser Web
Worker, as a preview with no server. Host code imports Node's `node:crypto`, which a
browser does not have, so the package supplies a stand-in module. Randomness comes from
the browser. Hashing cannot: the browser's own hashing interface (`SubtleCrypto`) only
answers asynchronously, "and every caller here hashes synchronously"
(`packages/experimental/webworker-runtime/src/node/builtin_modules/implemented/crypto.ts:2-5`).
The library is there to give the stand-in a synchronous SHA-1, SHA-256 and SHA-512. That
is the whole reason a library was needed although `node:crypto` exists: `node:crypto` is
not available where this code runs, and the platform's replacement has the wrong calling
style.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/webworker-runtime/src/node/builtin_modules/implemented/crypto.ts:6` | SHA-1 for the stand-in `createHash('sha1')` | `sha1` from `@noble/hashes/legacy.js` |
| `packages/experimental/webworker-runtime/src/node/builtin_modules/implemented/crypto.ts:7` | SHA-256 and SHA-512 for the stand-in `createHash` | `sha256`, `sha512` from `@noble/hashes/sha2.js` |
| `packages/experimental/webworker-runtime/src/node/builtin_modules/implemented/crypto.ts:13-17`, `:39-63` | A table of the three algorithms; any other name throws. Input chunks are collected and hashed in one call when the digest is asked for | one-shot call `hasher(bytes)` |

No patch, no pin comment and no README limitation mentions the library (searched
`patches/`, `pnpm-workspace.yaml` and the package README). `md5` is not in the table, so
host code that asks for it fails in the worker.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NOBLEHASHES-R1` | SHA-1, SHA-256 and SHA-512 digests are available to harness code as ordinary synchronous calls in every environment the harness is built for, with no dependency outside the standard library. | `packages/experimental/webworker-runtime/src/node/builtin_modules/implemented/crypto.ts:13-17` |

## 5. Go choice

**Decision.** No counterpart. Hashing in the harness uses the standard library
([DH-LIB-NODECRYPTO](node-crypto-timers-perf-hooks.md)).

**Why.** The library fills a gap between two JavaScript environments. Go has one
implementation of each hash, it is synchronous, and it is pure Go, so it compiles for
every target, including WebAssembly should the experimental area ever build the host for
a browser. There is no stand-in module to write because there is no `node:crypto` to
imitate. Whether the browser-worker preview itself exists in the Go design is the
experimental area's decision, not this specification's; either answer needs no hash
library.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** standard library | Nothing to add; same code on every target | none | `crypto/sha1`, `crypto/sha256`, `crypto/sha512` are in the installed Go 1.27.4 toolchain |
| A third-party hash module | none for these algorithms | A dependency with no benefit | not searched; none is named |

**Tradeoff.** None.

**Upstream verdict.** *Not applicable*: the need comes only from JavaScript's split
between Node and the browser.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-NOBLEHASHES-1` | local | `DH-LIB-NOBLEHASHES-R1` | A repository check that no hash implementation other than the standard library's is imported by the harness module (the import list of `go list -deps` contains no third-party SHA package). Known-answer vectors are the standard library's own tests and are not repeated. |

Local evidence only. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@noble/hashes`.
