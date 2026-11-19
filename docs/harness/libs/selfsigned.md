# selfsigned: Go specification

> **Status: design, not built.** Short form with the debt table. Upstream facts were read
> at the pinned commit recorded in [../README.md](../README.md). Statements that are
> reasoning and not read from source open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-SELFSIGNED` |
| **Upstream unit** | `UP-DEP-selfsigned` |
| **Upstream version** | 4.0.1 |
| **Kind** | test-only (a development dependency of one package) |
| **Used by** | host (`packages/host/webserver`, tests) |
| **Go choice** | standard library: `crypto/x509`, `crypto/ecdsa`, `crypto/rand`, `encoding/pem`, in a test helper |
| **Related areas** | web host |

## 1. What it is and why upstream needs it

A **self-signed certificate** is a TLS certificate signed by its own key instead of by a
certificate authority. Nothing trusts it by default, which makes it right for tests: a
test server presents it, and the test client is told to trust exactly that one
certificate.

`selfsigned` generates such a certificate in JavaScript. Upstream uses it only in tests
of the web server's optional HTTPS listener. The product never generates a certificate:
an operator who wants HTTPS supplies a certificate file and a key file, and the server
"neither provisions nor renews certificates" (`packages/host/webserver/README.md:45`).

The material is generated when the tests run and never committed, because the code host
rejects pushes that contain private keys, even test ones
(`packages/host/webserver/README.md:151`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/host/webserver/tests/tls-fixture.ts:26` | One certificate per test process: RSA 2048, SHA-256, valid 30 days, not a certificate authority, server authentication only, names `localhost`, `127.0.0.1` and `::1` | `generate(attributes, { keySize, days, algorithm, extensions })` |
| `packages/host/webserver/tests/tls-fixture.ts:54` | Write certificate, key (owner-only file mode) and a copy of the certificate for the client to trust | none |

## 3. Tech debt upstream carries

Search performed: imports of `selfsigned`; `patches/` and `pnpm-workspace.yaml` (no
patch or workspace pin); the web server README's maintainer note.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L7.5` | The library is held on its 4.x line because 5.x, installed by pnpm, gets two copies of one ASN.1 package and then fails on every call. | `packages/host/webserver/README.md:152` | Not applicable: the cause is npm packaging. Go's certificate code is in the standard library and has no dependency to duplicate. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SELFSIGNED-R1` | Tests of the HTTPS listener can create a short-lived server certificate for `localhost`, `127.0.0.1` and `::1`, with its private key, as PEM files in a temporary directory, the key readable by the owner only. | `packages/host/webserver/tests/tls-fixture.ts:54` |
| `DH-LIB-SELFSIGNED-R2` | No private key, real or test, is committed to the repository. | `packages/host/webserver/README.md:151` |
| `DH-LIB-SELFSIGNED-R3` | The same helper can produce the negative cases the listener must refuse at start-up: an empty file, text that is not PEM, and a key that does not match the certificate. | `packages/host/webserver/README.md:45`; `packages/host/webserver/src/index.ts:352` |

## 5. Go choice

**Decision.** A test helper of about 40 lines in the web host's test support:
generate an ECDSA P-256 key, build an `x509.Certificate` template (30 days, server
authentication, the three names), self-sign it with `x509.CreateCertificate`, and write
both with `encoding/pem`.

**Why.** Everything needed is in the standard library, and the listener under test loads
the files with `tls.LoadX509KeyPair`, which already fails for an empty file ("failed to
find any PEM data") and for a mismatched key ("private key does not match public key",
Go 1.27.4 `crypto/tls/tls.go:278`, `:325`). ECDSA replaces upstream's RSA because key
generation is the slow part of upstream's fixture
(`packages/host/webserver/tests/tls-fixture.ts:23`). Inference: generating an elliptic
curve key is much cheaper than a 2048-bit RSA key, so each test can have its own.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `crypto/x509` test helper | No dependency; exact control of names and lifetime; negative cases from the same code | A few lines to maintain | Go 1.27.4 standard library |
| `httptest.NewTLSServer` | Zero code | It serves its own built-in certificate from its own listener, so it cannot test the harness's listener or its file loading | Go 1.27.4 `net/http/httptest` |

**Tradeoff.** None of substance. The helper is test code and is never linked into the
product binary.

Whether the product should *offer* to generate a local certificate (so that a remote
bind is not plain HTTP by default) is a product question upstream answers "no"; it is
left to the web-host area and would reuse this code if the answer changes.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-SELFSIGNED-1` | local | `DH-LIB-SELFSIGNED-R1`, `DH-LIB-SELFSIGNED-R3` | A listener started from the generated files completes a handshake with a client that trusts only that certificate, for each of the three names; a listener given the empty, non-PEM and mismatched variants fails to start with the path in the error. |
| `DH-T-SELFSIGNED-2` | local | `DH-LIB-SELFSIGNED-R2` | A repository check finds no file containing a PEM private-key header outside generated temporary directories. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-selfsigned`, `UP-DEBT-found.L7.5`.
