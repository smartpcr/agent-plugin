# @octokit/webhooks: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Short form: sections 1, 2, 3, 4, 5, 8 and 9.

| | |
|---|---|
| **Library ID** | `DH-LIB-OCTOKITWEBHOOKS` |
| **Upstream unit** | `UP-DEP-@octokit/webhooks` |
| **Upstream version** | 14.2.0 (`packages/experimental/webhook-github/package.json:53`) |
| **Kind** | Runtime, experimental package only |
| **Used by** | `packages/experimental/webhook-github` |
| **Go choice** | Standard library: `crypto/hmac`, `crypto/sha256`, `encoding/hex` |
| **Related areas** | [../upstream/wire-and-api.md](../upstream/wire-and-api.md#dh-adr-wire-12) (webhook ingress decision). Owning area: experimental hooks and webhooks (cluster X5) |

## 1. What it is and why upstream needs it

A **webhook** is an HTTP request that an outside service sends when something happens,
here GitHub reporting an event in a repository. Anyone on the internet can send a
request to the same address, so the receiver must prove the sender knows a shared
secret. GitHub does this by sending, in a header, an HMAC (a keyed hash) of the request
body computed with that secret.

`@octokit/webhooks` is GitHub's JavaScript toolkit for receiving webhooks: signature
checks, event routing and typed payloads. Upstream uses one function of it, the
signature check, and re-exports two type names.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/webhook-github/src/handler.ts:103` | Verifies the `x-hub-signature-256` header against the raw body with the configured secret; any error counts as "not verified" | `new Webhooks({ secret }).verify(body, signature)` |
| `packages/experimental/webhook-github/src/types.ts:22` | Re-exports two type names for rule authors | `EmitterWebhookEvent`, `EmitterWebhookEventName` (types only) |

The order around the call is upstream's own code and is the part worth keeping
(`packages/experimental/webhook-github/src/handler.ts:86` to `:109`): method must be
`POST`; content type must be JSON; the body is read under a size limit; the signature,
delivery and event headers must each appear exactly once; the secret is resolved from a
credential reference, and an unavailable secret answers 503; the signature is verified;
only then is the body parsed. Payload fields are not validated by the adapter
(`packages/experimental/webhook-github/README.md:75`).

## 3. Tech debt upstream carries

Search: `patches/`, `pnpm-workspace.yaml`, the package README's limitations and the two
import sites. None found for this dependency. No `UP-DEBT` unit is assigned.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-OCTOKITWEBHOOKS-R1` | A delivery is accepted only if the header is exactly `sha256=` followed by 64 hexadecimal digits and equals the HMAC-SHA-256 of the raw body bytes under the configured secret. The comparison takes the same time whatever the input | `handler.ts:103` |
| `DH-LIB-OCTOKITWEBHOOKS-R2` | Verification happens before the body is parsed, on the bytes as received | `handler.ts:93`, `:103`, `:108`; DH-ADR-WIRE-12 |
| `DH-LIB-OCTOKITWEBHOOKS-R3` | A missing or empty secret refuses every delivery with 503. A failed check answers 401 with a fixed message that does not say what was wrong | `handler.ts:97` to `:107` |

## 5. Go choice

**Decision.** About ten lines of own code on the standard library: compute
`hmac.New(sha256.New, secret)` over the body, decode the header with `encoding/hex`, and
compare with `hmac.Equal`.

**Why.** One function of the library is used, and the standard library has every part
of it, including the constant-time comparison.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Standard library** (chosen) | No dependency; the whole check is visible in one function | The header format is own parsing, so it gets a fuzz target | `crypto/hmac`, `crypto/sha256`, `encoding/hex` in Go 1.27.4 (installed toolchain) |
| `github.com/google/go-github` | Has `ValidateSignature` and `ValidatePayload`, and typed event structs | A complete GitHub API client linked for one function; upstream deliberately does no typed payload validation | v88.0.0, BSD-3-Clause, released 2026-05-21; `github/messages.go:272`, `:292` read in the module cache on 2026-11-18. A newer major version may exist |

**Tradeoff.** No typed GitHub event structs. Rules read the fields they need from the
JSON payload, as upstream's rules do. The event name is a string.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-OCTOKITWEBHOOKS-1` | local | `DH-LIB-OCTOKITWEBHOOKS-R1`, `DH-LIB-OCTOKITWEBHOOKS-R3` | Table test: the provider's published example (secret, body, expected header); a changed body byte; a changed secret; a `sha1=` header; wrong length; non-hexadecimal digits; duplicate header; empty secret. A fuzz target feeds arbitrary header strings and never panics |
| `DH-T-OCTOKITWEBHOOKS-2` | local | `DH-LIB-OCTOKITWEBHOOKS-R2` | With a body that is not valid JSON and a bad signature, the handler answers 401 and the JSON decoder is never called |

A real GitHub delivery to a deployed listener is the real-environment proof named by
DH-ADR-WIRE-12; it belongs to the webhook area. Nothing here has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-@octokit/webhooks`: replaced (`DH-LIB-OCTOKITWEBHOOKS`).
