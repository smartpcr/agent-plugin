# undici (and global fetch): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go standard-library facts were read on 2026-11-18 in the
> Go 1.27.4 source; no harness code exists.
>
> **Unverified research notes.** Where this document says "probe" (the behaviour table
> in section 5), it reports observations made during library research by running a
> throwaway program outside this repository. That was against the project's rule that
> nothing is built or run in the design task, and the program was not kept, so the
> observations cannot be repeated from this repository. Every such statement is
> therefore **unverified and to be measured** by `DH-T-UNDICI-2` to `-6` (section 8); if
> a test contradicts an observation, the affected row of the design is reopened.

| | |
|---|---|
| **Library ID** | `DH-LIB-UNDICI` (the outbound HTTP client), `DH-LIB-HTTPPROXY` (the proxy matcher) |
| **Upstream unit** | `UP-DEP-undici` |
| **Upstream version** | 8.11.2 and 8.10.2 in `pnpm-lock.yaml`; Node's built-in `fetch` is a second, bundled copy |
| **Kind** | runtime |
| **Used by** | util (`http-proxy`), web (`web-fetch-http`), experimental (`liveshare`; `subagent-oar` declares it and imports nothing) |
| **Go choice** | standard library `net/http` and `crypto/tls`; named Go module `golang.org/x/net/http/httpproxy` for bypass matching |
| **Related areas** | A23, which owns the `egress` package ([../areas/util-test-support.md](../areas/util-test-support.md), `DH-D-UTIL-2`); its users A04 (model providers), A15 (web tools, MCP over HTTP), A18 (telemetry export) and A24 (`dsh update`) |

## 1. What it is and why upstream needs it

undici is Node's HTTP client library. Node's global `fetch` function is a copy of undici
built into the runtime. A **dispatcher** is undici's name for the object that owns
connections: it decides, per request, which socket to use and whether to go through a
proxy. Node has one process-wide "global dispatcher" that every plain `fetch()` uses.

Upstream needs the npm copy of undici for one reason: to control that dispatcher. A user
behind a corporate proxy exports `HTTPS_PROXY`, and every outbound request (model calls,
web search, page fetches, MCP servers over HTTP) must then go through it. Node's `fetch`
does not read those variables in the way upstream wants, so the `http-proxy` package
resolves one **proxy policy** at launch and installs a dispatcher that applies it to
every request (`packages/util/http-proxy/README.md:28`).

Upstream in fact has three HTTP client stacks, plus a WebSocket client. They differ in
whether they see the proxy policy, which is the main lesson for the Go design:

| Client | Who uses it | Why this one | Proxy | Redirects | Timeout | Connections |
|---|---|---|---|---|---|---|
| Global `fetch` (built-in undici) | Model adapters (`packages/llm/llm-deepseek/src/adapter.ts:151`), web search (`packages/web/web-search-deepseek/src/provider.ts:270`), MCP OAuth (`packages/mcp/mcp-client/src/oauth-credentials.ts:80`), account (`packages/account/deepseek-account/src/protocol.ts:197`), plugin market (`packages/boot/plugin-market/src/index.ts:467`) | The default; third-party SDKs reach it without knowing about the harness | Yes, through the replaced global dispatcher | `redirect: 'error'` wherever the request carries a credential; `'manual'` for market and detail fetches | An abort signal per call | One process-wide pool |
| npm `undici` (`fetch`, `Agent`, `ProxyAgent`, `Pool`) | `http-proxy` (`packages/util/http-proxy/src/install.ts:163`), `web-fetch-http` (`packages/web/web-fetch-http/src/provider.ts:120`), LiveShare relay (`packages/experimental/liveshare/src/host/relay-fetch.ts:62`) | The dispatcher classes are not exported by the built-in copy; LiveShare wants its own pool with HTTP/2 on and immunity from a replaced global `fetch` | Yes, by the same policy | Manual, same-origin only, at most 5 hops (`web-fetch-http`) | 30 s default (`packages/web/web-fetch-http/src/index.ts:48`) | Per-origin pools; a caller-owned dispatcher is closed by its owner |
| `got` over `node:http` | Telemetry export only ([got.md](got.md)) | The OpenTelemetry exporter is built on `node:http` agents; `got` adds cancellation and retry | **No**, "direct by design" (`packages/util/http-proxy/README.md:113`) | Off | Per attempt | One keep-alive agent per channel |
| `ws` client over `node:http` | LiveShare owner gateway, desktop account watcher ([ws.md](ws.md)) | WebSocket | Inference: no. The options passed carry only headers (`packages/experimental/liveshare/src/owner-gateway/worker.ts:160`) and nothing routes `node:http` through the dispatcher | n/a | Heartbeat | One socket |

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/util/http-proxy/src/install.ts:162` | Build a dispatcher that picks a proxy or a direct pool per origin from the policy | `Agent` with a `factory`, `ProxyAgent`, `Pool` |
| `packages/util/http-proxy/src/install.ts:199` | Install it process-wide and restore the previous one on disposal | `getGlobalDispatcher`, `setGlobalDispatcher`, `close()` |
| `packages/util/http-proxy/src/install.ts:344` | A dispatcher a caller owns, with HTTP/2 allowed or forbidden | `Agent({ allowH2 })` |
| `packages/util/http-proxy/src/install.ts:287`, `:366` | Environment overlays for child processes: keep the user's values, or clear every proxy name | none (plain data) |
| `packages/util/http-proxy/src/policy.ts:306` | Resolve the policy from the launch environment and report unusable values | none (the half that imports no transport) |
| `packages/web/web-fetch-http/src/provider.ts:121` | One anonymous `GET` with redirects handled by the caller, a byte cap and a deadline | `fetch(url, { redirect: 'manual', signal })`, streamed body reader |
| `packages/experimental/liveshare/src/host/relay-fetch.ts:51`, `:64` | Relay calls on a private pool | `fetch(input, { dispatcher })` |
| `packages/experimental/liveshare/src/host/relay-h2-idle.ts:11` | Swallow an HTTP/2 idle-close error that would otherwise end the process | diagnostics channel `undici:client:connected` |
| `packages/experimental/subagent-oar/package.json:54` | Declared, never imported in `src` | none |

The proxy policy upstream implements (`packages/util/http-proxy/src/policy.ts`):

- Reads `http_proxy`, `https_proxy`, `no_proxy`, `all_proxy`, lower case first, blank
  treated as unset (`:109`). Values come from the launcher's snapshot: an exported
  variable, then `$DSH_HOME/.env`; a project `.env` may not set them, because that file
  arrives with a clone (`packages/util/http-proxy/README.md:51`).
- `ALL_PROXY` backs both schemes; HTTPS falls back to the HTTP proxy last (`:312`).
- A value that is not an `http:` or `https:` URL (SOCKS, PAC, garbage) is **reported and
  skipped**, and that scheme connects directly; it never borrows another scheme's proxy
  (`:193`). The message names the variable, never its value (`:150`).
- Loopback is always direct: `localhost`, `*.localhost`, `127.0.0.0/8`, `::1`, the
  unspecified addresses and IPv4-mapped spellings (`:257`).
- A bypass entry matches a host and its subdomains, may carry a port, `*` matches all;
  CIDR ranges are not matched (`:279`).

## 3. Tech debt upstream carries

Search performed: every import of `undici` (static and dynamic) in `apps`, `packages`
and `scripts`; `patches/` and `pnpm-workspace.yaml` (no patch, pin or exemption);
the `http-proxy`, `web-fetch-http` and `liveshare` sources and READMEs; the network
proxy user guide.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L7.1` | WebSocket clients do not go through the proxy policy, and no limitation says so (Inference from the call options) | `packages/experimental/liveshare/src/owner-gateway/worker.ts:160`; `apps/desktop/src/account-backend.ts:103` | Resolved by [DH-ADR-UNDICI-1](#dh-adr-undici-1): a WebSocket dial takes its `http.Client` from the same factory |
| `UP-DEBT-found.L7.2` | Reaching Node's built-in `fetch` from the npm copy depends on both writing one undocumented global symbol; a version bump can silently break proxying | `packages/util/http-proxy/README.md:123` | Resolved by [DH-ADR-UNDICI-1](#dh-adr-undici-1): there is no global to share; each client is built from the policy |
| `UP-DEBT-found.L7.3` | An idle HTTP/2 session close is raised as an error on a socket no request owns, which upstream's fail-loud process handler turns into an exit; callers either turn HTTP/2 off or install a socket guard | `packages/experimental/liveshare/src/host/relay-h2-idle.ts:4`; `packages/util/http-proxy/README.md:68` | Not applicable: it is a property of undici and Node's uncaught-error model. Go's HTTP/2 client closes idle connections without surfacing an error to the program |
| `UP-DEBT-found.L7.6` | The user guide says `web_fetch` refuses a literal private address; the code and the package README say every address is reachable; comments still describe "connection pinning" that no longer exists | `docs/user/guide/network-proxy.md:71`; `packages/web/web-fetch-http/README.md:136`; `packages/web/web-fetch-http/src/policy.ts:45` | Resolved by `DH-LIB-UNDICI-R9`: the client factory offers a connect-time destination guard, which [DH-ADR-SECURITY-10](../upstream/security.md#dh-adr-security-10) requires the fetch tool to use |

Limitations already inventoried from the `http-proxy` README belong to the area agent
that owns that package. This spec supplies the mechanism for each:

| Inventoried unit | Go situation |
|---|---|
| `UP-DEBT-limit.packages.util.http-proxy.1` (no SOCKS, PAC or OS proxy detection) | SOCKS5 works (`net/http` supports `socks5` and `socks5h` proxy URLs). PAC files and the operating system's proxy settings are still not read (section 7) |
| `UP-DEBT-limit.packages.util.http-proxy.2` (no custom certificate authority) | Removed by [DH-ADR-UNDICI-3](#dh-adr-undici-3) |
| `UP-DEBT-limit.packages.util.http-proxy.3` (a child honours the policy only on a new Node) | The `NODE_USE_ENV_PROXY` half stays true for Node children, which the Go harness still starts (MCP servers); `DH-LIB-UNDICI-R7` |
| `UP-DEBT-limit.packages.util.http-proxy.4` (telemetry is direct) | Removed: [got.md](got.md) |
| `UP-DEBT-limit.packages.util.http-proxy.6` (the gate sees source, not dependencies) | Still true of any lint; see `DH-T-UNDICI-8` and the egress tests |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-UNDICI-R1` | Every outbound connection the host makes (HTTP, WebSocket dial, telemetry export, MCP over HTTP) is routed by one proxy policy, resolved once at launch. | `packages/util/http-proxy/README.md:12`; debts L7.1 and http-proxy.4 |
| `DH-LIB-UNDICI-R2` | The policy reads `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` and `ALL_PROXY` in either case from the launch snapshot (exported variable, then the user's home `.env`). A project-level `.env` that sets any of them stops the launch. | `packages/util/http-proxy/README.md:51` |
| `DH-LIB-UNDICI-R3` | A proxy value the harness cannot use is reported by variable name, never by value, and that scheme connects directly without borrowing another scheme's proxy. | `packages/util/http-proxy/src/policy.ts:140`, `:193` |
| `DH-LIB-UNDICI-R4` | Loopback and the unspecified address are always direct, whatever `NO_PROXY` says. | `packages/util/http-proxy/src/policy.ts:257` |
| `DH-LIB-UNDICI-R5` | A caller can ask which proxy, if any, a URL would use, and gets the same answer the transport acts on. | `packages/util/http-proxy/src/install.ts:80` |
| `DH-LIB-UNDICI-R6` | TLS verification uses the operating system's trust store plus any extra certificate authorities the operator configures. Verification is never switched off. | `packages/util/http-proxy/README.md:111` |
| `DH-LIB-UNDICI-R7` | A child process receives the user's own proxy values where they set them and the resolved values where they did not, with the loopback bypass added. Test fixtures can get an overlay that clears every proxy name. A command the model runs is an ordinary sandboxed command ([DH-ADR-8](../design.md#dh-adr-8)): it gets the same proxy values except any value that carries a password, which is omitted and reported once. Area A23 names the two cases `Trusted` and `ModelRun` (`ChildEnv`). | `packages/util/http-proxy/src/install.ts:287`, `:366`; `packages/util/http-proxy/README.md:114` |
| `DH-LIB-UNDICI-R8` | A client does not follow redirects unless its owner asks. When it does, the owner sees each hop and may refuse it (same-origin only, hop limit). | `packages/llm/llm-deepseek/src/adapter.ts:151`; `packages/web/web-fetch-http/src/provider.ts:66` |
| `DH-LIB-UNDICI-R9` | A client can be given a destination guard that is checked against the address actually being connected to, after name resolution, and before a URL is handed to a proxy. | [DH-ADR-SECURITY-10](../upstream/security.md#dh-adr-security-10); debt L7.6 |
| `DH-LIB-UNDICI-R10` | Every request has a deadline or cancellation, and a response body is read under a byte limit; a declared length over the limit is refused before reading. | `packages/web/web-fetch-http/src/provider.ts:61`, `:174` |
| `DH-LIB-UNDICI-R11` | A transport failure is classified (name resolution, refused, timed out, certificate, interrupted, unreachable) so that callers and the model get a stable code. | `packages/web/web-fetch-http/README.md:70` |
| `DH-LIB-UNDICI-R12` | Connections are owned: the component that asked for a client releases its idle connections when it unloads, and unloading the policy lets requests already in flight finish. | `packages/util/http-proxy/src/install.ts:41`, `:337` |

## 5. Go choice

**Decision.** `net/http` (`http.Client`, `http.Transport`) and `crypto/tls` from the
standard library are the HTTP client. The bypass matcher is
`golang.org/x/net/http/httpproxy`, the same code the standard library vendors, called
with values the harness resolved itself. A small harness package, called `egress` here,
wraps both and is the only place that constructs a transport.

**Why.** The standard library already does what upstream built by hand: per-request
proxy selection (`Transport.Proxy`), `CONNECT` tunnelling, HTTP/2, pooling, context
cancellation and a dial hook. It does not do three things upstream requires, which is
why `egress` exists and why `http.ProxyFromEnvironment` is not used directly.

| Behaviour (probe on Go 1.27.4 and `x/net` v0.63.0; unverified, to be measured by `DH-T-UNDICI-2` to `-6`) | Standard library | Upstream policy | Go design |
|---|---|---|---|
| `ALL_PROXY` | Not read | Fallback for both schemes | `egress` resolves it (R2) |
| HTTPS with only `HTTP_PROXY` set | Direct | Uses the HTTP proxy | Keep upstream's fallback |
| Unusable value | An unknown scheme (`pac+http://...`) is returned as the proxy with no error (Inference: the request then fails when it connects); an unparseable value is dropped with no message | Reported, scheme stays direct | `egress` validates and reports (R3) |
| `socks5://` | Supported | Refused | Change: accept `socks5` and `socks5h` |
| CIDR in `NO_PROXY` (`10.0.0.0/8`) | Matched | Not matched | Change: matched |
| `127.0.0.2`, `localhost`, `::1`, `::ffff:127.0.0.1` | Always direct | Always direct | Same |
| `0.0.0.0`, `app.localhost` | Proxied | Direct | `egress` checks these first (R4) |
| `.example.com` entry and the bare `example.com` | Subdomains only | Both | Keep upstream: strip the leading dot before matching |
| Separators in `NO_PROXY` | Comma | Comma or white space | `egress` normalises to commas |
| Environment read | Once per process, from the real environment, cached | From the launcher snapshot | `egress` passes resolved values to `httpproxy.Config` |
| Redirects | Followed, up to 10 | Refused unless asked | `egress` default refuses (R8) |
| Server and client timeouts | None by default | Per call | `egress` requires a context; helpers add header and idle timeouts (R10) |

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `net/http` + `golang.org/x/net/http/httpproxy` | Matcher identical to every other Go tool; CIDR and SOCKS5 for free; the harness feeds it its own snapshot | One module import for about 300 lines of matching | `golang.org/x/net` v0.63.0, 2026-11-17, BSD-3-Clause; `go list -m -json`, `go mod download -json` and `LICENSE` in the module cache, 2026-11-18; no cgo |
| `http.ProxyFromEnvironment` alone | No module | Reads the process environment once and caches it; no `ALL_PROXY`; no diagnostics; cannot honour the launcher snapshot or the project-`.env` refusal | Go 1.27.4 `net/http/transport.go:1038` |
| Own matcher, as upstream wrote | No module; exact upstream semantics | Re-creates upstream's debt (no CIDR), and a matcher that differs from `curl` and from Go tools surprises operators | `packages/util/http-proxy/src/policy.ts:279` |

**Tradeoff.** Bypass matching follows Go's rules where they differ from upstream's in
ways an operator would welcome (CIDR, SOCKS5). A profile that relied on upstream refusing
SOCKS now proxies through it. The static proxy setting of the operating system is read
at launch on Windows and macOS, as the source of lowest precedence
([DH-ADR-UTIL-10](../areas/util-test-support.md#dh-adr-util-10)). A proxy
auto-configuration file is detected and reported, and never evaluated.

<a id="dh-adr-undici-1"></a>
### DH-ADR-UNDICI-1 One outbound client factory; nothing uses a default or global transport

- **Decision.** The `egress` service builds every `http.Client` the host uses. Model
  adapters, web tools, the MCP HTTP transport, telemetry export, the update check and
  the download of `dsh update`, and any WebSocket dial all receive a client from it.
  The harness downloads no plugin ([DH-ADR-13](../design.md#dh-adr-13)), so there is no
  plugin download to route. `http.DefaultClient`,
  `http.DefaultTransport`, `http.Get` and a bare `&http.Client{}` are forbidden outside
  the package.
- **Why.** Upstream has four stacks and two of them miss the proxy (section 1). Its fix,
  replacing a process global, works only for code that reaches that global and depends on
  an undocumented symbol (debt L7.2). Go has no such global to patch, so the rule has to
  be "ask for a client", which is also easier to test.
- **Tradeoff.** A third-party Go SDK must accept an `http.Client` or a
  `http.RoundTripper`. One that does not cannot be used for outbound traffic. Upstream
  has the same limit in another form: it must audit each SDK's transport
  (`packages/util/http-proxy/README.md:32`).
- **Serves.** [DH-O-14](../design.md#dh-o-14) (one outbound policy an operator can reason about), [DH-O-7](../design.md#dh-o-7) (a place to attach the destination guard); DH-ADR-2.
- **Verified by.** `DH-T-UNDICI-1`, `DH-T-UNDICI-8`.

<a id="dh-adr-undici-2"></a>
### DH-ADR-UNDICI-2 Proxy policy: upstream's resolution rules, Go's matcher

- **Decision.** Keep: resolve once at launch from the snapshot; `ALL_PROXY` fallback;
  HTTPS falls back to the HTTP proxy; a refused value keeps its scheme direct and is
  reported by variable name; loopback always direct; a project `.env` may not choose the
  proxy. Change: accept `socks5` and `socks5h`; match CIDR bypass entries. Drop: the
  HTTP/2 on/off switch (debt L7.3 does not exist in Go).
- **Why.** The kept rules are security and predictability rules that do not depend on
  the runtime. The changes remove two documented limitations at no cost.
- **Tradeoff.** No reload: changing the proxy needs a restart, as upstream.
- **Serves.** [DH-O-14](../design.md#dh-o-14); DH-ADR-2.
- **Verified by.** `DH-T-UNDICI-2` to `DH-T-UNDICI-4`.

<a id="dh-adr-undici-3"></a>
### DH-ADR-UNDICI-3 Trust: the operating system's store plus configured extra authorities

- **Decision.** Every client verifies servers against `x509.SystemCertPool()`. An
  operator setting (illustrative name `network.extraCACerts`, a list of PEM files, also
  settable from the environment) appends certificates to a copy of that pool. A file
  that cannot be read or holds no certificate stops the launch with the path in the
  message. No setting disables verification.
- **Why.** Node ships its own list of authorities and ignores the operating system, so a
  company whose proxy re-signs TLS must export `NODE_EXTRA_CA_CERTS` before launch
  (`docs/user/guide/network-proxy.md:52`). Go reads the Windows and macOS stores and the
  Linux bundle, so an authority the administrator installed already works. The setting
  covers containers and machines where it is not installed.
- **Constraint.** Go's own `SSL_CERT_FILE` and `SSL_CERT_DIR` variables **replace** the
  system roots, on every platform since Go 1.27 (`crypto/x509/cert_pool.go:108`). They
  keep working but are not the "extra" mechanism, which must add.
- **Tradeoff.** One more setting to document. Client certificates (mutual TLS to a proxy
  or provider) are not covered.
- **Serves.** [DH-O-14](../design.md#dh-o-14); DH-ADR-2.
- **Verified by.** `DH-T-UNDICI-5`.

## 6. Design notes

**Shape.** One service, constructed by the launcher before any component loads, as
upstream does (`apps/cli/src/profile-boot.ts:286`).

```go
package egress

type Policy struct {            // plain data, safe to log: holds no credentials
    HTTP, HTTPS string          // variable name that supplied each proxy, or ""
    Bypass      []string
}
type Diagnostic struct{ Variable, Message string }

func Resolve(env func(name string) (string, bool)) (*Service, []Diagnostic, error)

type Options struct {
    Redirect   func(req *http.Request, via []*http.Request) error // nil: refuse all
    Guard      func(addr netip.AddrPort) error                    // nil: allow all
    HeaderWait time.Duration                                      // response-header timeout
}

func (s *Service) Client(o Options) (c *http.Client, release func())
func (s *Service) ProxyFor(u *url.URL) (proxy *url.URL)  // same function the transport calls
func (s *Service) ChildEnv() map[string]*string          // nil value: remove the name
func ClearedEnv() map[string]*string
func ReadCapped(resp *http.Response, max int64) (body []byte, truncated bool, err error)
func Classify(err error) FailureKind
```

**Mechanism.**

- `Transport.Proxy` calls one function: loopback and unspecified hosts return direct;
  otherwise `httpproxy.Config{...}.ProxyFunc()` built from the resolved values. `ProxyFor`
  is that same function, which gives R5 without a second parser.
- `Transport.TLSClientConfig.RootCAs` is the pool from DH-ADR-UNDICI-3. Because a custom
  TLS configuration and dialer are set, HTTP/2 must be requested explicitly
  (`ForceAttemptHTTP2`, `net/http/transport.go:301`); otherwise it is silently lost.
- The guard (R9) runs in `net.Dialer.Control`, which sees the resolved address of every
  connection attempt, including each address tried after a DNS answer changes. When the
  URL would go through a proxy the host cannot see the final address, so `egress` also
  applies the guard to a literal IP in the URL before the request is sent, and the
  owning tool decides whether a guarded client may use a proxy at all.
- `Client.CheckRedirect` is `Options.Redirect`, or a function that returns
  `http.ErrUseLastResponse` so the caller sees the 3xx. Inference: this matters more in
  Go than in Node, because Go strips `Authorization` and `Cookie` on a cross-host
  redirect but not a custom API-key header.

**Ownership, failure and cleanup.** The service is owned by the root fiber. `Client`
returns a release function; the component registers it as an effect on its own fiber, so
unloading the component closes that client's idle connections
(`Transport.CloseIdleConnections`) and lets requests in flight finish, which is upstream's
"close, not destroy" rule (`packages/util/http-proxy/src/install.ts:46`). Clients with
the same options share one transport, so pools are not multiplied. A request cancelled by
its context returns promptly; whether the remote side acted on it is unknown to the
caller, as with any HTTP client, and tools that mutate remote state must say so.

**Verdict on upstream.** Keep the policy and its diagnostics. Drop the global-dispatcher
installation, the nested install and restore bookkeeping (`install.ts:199` to `:248`), the
HTTP/2 guard, and the `verify-no-bare-dispatcher` script in its current form.

## 7. Risks and open questions

- **Operating-system proxy settings and PAC.** Settled by area A23 as a recommended
  default (register entry U-K2-2;
  [DH-ADR-UTIL-10](../areas/util-test-support.md#dh-adr-util-10)): a best-effort
  `sysproxy` reads the static system setting at launch on Windows and macOS, below
  every environment variable in precedence, frozen with the other launch facts and
  shown in the launch diagnostics with its origin. A PAC address is reported as "not
  supported; export the variables" and never evaluated, because evaluating one needs a
  JavaScript engine. The first text of this document said that reading the setting
  needs a cgo framework; that was not established. Whether the read builds with
  `CGO_ENABLED=0` is an open fact in that specification, and the design holds either
  way: where the setting cannot be read, the environment variables are the only source,
  as before. The import-graph gate that keeps a dependency from opening its own
  connections is `DH-T-UTIL-22` (U-K2-3).
- **Guard behind a proxy.** Whether a guarded client (the fetch tool) may use a proxy for
  host names it cannot resolve itself is a product decision for the web-tools area.
- **Third-party SDKs.** The MCP and ACP Go SDKs, and any provider SDK, must be given the
  `egress` client. The areas that adopt them must confirm the hook exists.
- **Owner of `egress`.** This spec defines no deliverable. The area that owns outbound
  network policy must create it; the name and the setting names are illustrative.
- **`subagent-oar`** declares undici without using it; nothing follows for Go.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-UNDICI-1` | local | `DH-LIB-UNDICI-R1`, `DH-LIB-UNDICI-R5` | With a fake proxy (`net/http/httptest`) in the policy, a plain request, a WebSocket dial and a telemetry export all arrive at the proxy, and `ProxyFor` returns the same proxy for each URL. |
| `DH-T-UNDICI-2` | local | `DH-LIB-UNDICI-R2`, `DH-LIB-UNDICI-R3` | Table test of resolution: case precedence, blank values, `ALL_PROXY` fallback, HTTPS fallback, a refused HTTPS value keeps HTTPS direct, diagnostics contain the variable name and never the URL or its password. |
| `DH-T-UNDICI-3` | local | `DH-LIB-UNDICI-R4` | Every loopback spelling, `0.0.0.0`, `::` and `*.localhost` is direct even with `NO_PROXY` empty. |
| `DH-T-UNDICI-4` | local | `DH-LIB-UNDICI-R2` | Bypass table: host and subdomain, leading dot, port, `*`, bare and bracketed IPv6, CIDR, white-space separators. |
| `DH-T-UNDICI-5` | local | `DH-LIB-UNDICI-R6` | A TLS test server signed by a private authority is refused by default and accepted once the authority file is configured; an unreadable or empty file fails resolution. |
| `DH-T-UNDICI-6` | local | `DH-LIB-UNDICI-R8`, `DH-LIB-UNDICI-R10`, `DH-LIB-UNDICI-R11` | A redirect is returned, not followed, by default; a same-origin policy follows within its hop limit and refuses a cross-origin hop; a body over the cap is truncated or refused; each failure class maps to its code. |
| `DH-T-UNDICI-7` | local | `DH-LIB-UNDICI-R9` | A guard that denies private ranges refuses a literal address, a name that resolves to one, and a redirect to one, before any byte is sent. |
| `DH-T-UNDICI-8` | local | `DH-LIB-UNDICI-R1` | A `go/analysis` check fails on any use of the default client or transport, or a client literal, outside `egress`. |
| `DH-T-UNDICI-9` | local | `DH-LIB-UNDICI-R7`, `DH-LIB-UNDICI-R12` | Child overlays keep a user-set SOCKS value, add resolved values for unset names and clear every name on request; releasing a client closes idle connections while an in-flight request completes. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.
A real-environment scenario is recommended to the owning area: **Given** a host behind a
real TLS-intercepting HTTP proxy with its authority configured and a real model API key;
**When** the operator runs one prompt that calls the model, searches and fetches a page;
**Then** the proxy log shows all three and the host made no direct connection;
**Cleanup:** unset the variables and remove the temporary home.

## 9. Units dispositioned by this spec

`UP-DEP-undici`, `UP-DEBT-found.L7.1`, `UP-DEBT-found.L7.2`, `UP-DEBT-found.L7.3`,
`UP-DEBT-found.L7.6`.
