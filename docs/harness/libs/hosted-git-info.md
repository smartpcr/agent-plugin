# hosted-git-info: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-HOSTEDGITINFO` |
| **Upstream unit** | `UP-DEP-hosted-git-info` |
| **Upstream version** | 9.0.3 (`pnpm-lock.yaml`) |
| **Kind** | runtime |
| **Used by** | apps (`plugin-market-generator`), boot (`plugin-manager`, `plugin-market`) |
| **Go choice** | standard library (`net/url`) plus about 60 lines of own code |
| **Related areas** | A22 and A21 (the manifest of a local plugin directory, [../areas/extensions.md](../areas/extensions.md)); owner decision [DH-ADR-13](../design.md#dh-adr-13): plugins are installed from local directories, and nothing is fetched |

## 1. What it is and why upstream needs it

`hosted-git-info` is an npm library that knows the URL shapes of a few Git hosting
services, "such as GitHub and GitLab.com"
(`packages/boot/plugin-manager/src/git-transport.ts:19-21`). Given a repository address in
any spelling npm accepts, "a manifest `repository` URL or shorthand"
(`packages/boot/plugin-market/src/detail.ts:47`), it says which service it is, who the
user and project are, and what the web page and clone addresses are. Inference from the
library's documentation: the shorthands include `github:user/repo` and `user/repo`.

Upstream needs it because plugins are npm packages. An npm manifest's `repository` field
may use any of those spellings, and the package manager pnpm treats the services this
library knows differently from every other Git server. Both reasons come from npm.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/boot/plugin-manager/src/git-transport.ts:26-27` | Decide whether a Git URL is on a service the library knows. For those, pnpm itself tries HTTPS and then SSH. For any other server, the harness probes with `git ls-remote` and rewrites the address to SSH when only SSH can read it (`:44-73`). | `fromUrl(...) !== undefined` |
| `packages/boot/plugin-market/src/detail.ts:50-58` | Turn a manifest's `repository` value into host, `user/project` and an HTTPS clone address, to fetch a plugin's README for its detail page; a "gist" is refused | `fromUrl`, `type`, `user`, `project`, `domain` |
| `apps/plugin-market-generator/src/metadata.ts:138-141` | Turn the same value into the web page address listed in a market index | `fromUrl`, `browse()` |

At each site a hand-written fallback handles plain `https://` addresses on other servers
(`packages/boot/plugin-market/src/detail.ts:59-65`, `apps/plugin-market-generator/src/metadata.ts:142-151`).

## 4. Requirements for the Go implementation

What each upstream need becomes once plugins are processes, not npm packages
([DH-ADR-4](../design.md#dh-adr-4)):

| Upstream need | Under DH-ADR-4 |
|---|---|
| Accept npm's repository shorthands | **Disappears.** The plugin manifest is the harness's own format, so it can require one spelling. |
| Know which servers pnpm treats specially | **Disappears.** There is no pnpm. If the harness fetches from Git itself, it treats every server the same way. |
| Derive a web page and a clone address from a repository address | **Stays**, as R1, wherever a plugin or an index names its source repository. |
| Choose HTTPS or SSH by probing | **Not built.** The owner decided that plugins come from local directories and that the harness downloads none ([DH-ADR-13](../design.md#dh-adr-13)), so the harness never fetches from Git. R3 records the rule a later registry or Git source would have to meet. |

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-HOSTEDGITINFO-R1` | A repository address in a plugin manifest or index is one full address: `https://host/path` or the SSH form `git@host:path`. From it the harness derives the host (lower case), the project path without a trailing `.git`, the HTTPS clone address and the web page address. No table of known services is used. | `packages/boot/plugin-market/src/detail.ts:50-65` |
| `DH-LIB-HOSTEDGITINFO-R2` | An address that carries a user name and password, a query, an unsupported scheme, or fewer than two path segments is rejected with a reason. A revision suffix after `#` is separated and returned, not treated as part of the address. Plain `http`, which upstream accepts, is rejected (Inference: a plugin source should not be read without transport security). | `packages/boot/plugin-market/src/detail.ts:51-52`, `:63`; `apps/plugin-market-generator/src/metadata.ts:139-149` |
| `DH-LIB-HOSTEDGITINFO-R3` | **Deferred, not part of this design.** If a later registry fetches plugins from Git: the transport is chosen the same way for every host, by one read-only probe with prompts disabled, and a private source never falls back to a public one. Today the harness fetches no plugin ([DH-ADR-13](../design.md#dh-adr-13)), so nothing implements or tests this. | `packages/boot/plugin-manager/src/git-transport.ts:44-73` |

## 5. Go choice

**Decision.** The standard library. `net/url` parses the HTTPS form; a few lines split the
SSH form at the first `:`.

**Why.** With one accepted spelling there is nothing left for a host-knowledge library to
do. R1 and R2 are string rules on a parsed URL.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`net/url` and own code (chosen)** | No dependency; rules are explicit and testable | The SSH form is not a URL and needs its own small parser | Go 1.27.4 standard library |
| `github.com/whilp/git-urls` | Parses the SSH form and others | One release, 2020-08-27; adds a dependency for a dozen lines | v1.0.0; version and date checked 2026-11-18 with `go list -m -json`; licence not read, so it is not a candidate |
| Port the library's table of services | Shorthands such as `user/repo` keep working | Keeps an npm convention the manifest does not need | n/a |

**Tradeoff.** `github:user/repo` and `user/repo` are not accepted; the error asks for the
full address.

## 7. Risks and open questions

- The parser belongs with the plugin manifest code of area A22; this specification
  declares no deliverable. With local directories only, the one use left for R1 and R2
  is a manifest field that names a plugin's source repository for display.
- R3 does not apply: no Git-based source exists under [DH-ADR-13](../design.md#dh-adr-13).

## 8. Verification

Local Go test on strings. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-HOSTEDGITINFO-1` | local | `DH-LIB-HOSTEDGITINFO-R1`, `DH-LIB-HOSTEDGITINFO-R2` | A table of addresses: HTTPS with and without `.git`, a nested group path, the SSH form, mixed-case host, a `#` suffix, each giving the expected host, project, clone and page addresses; credentials, a query, plain `http`, a one-segment path and an npm shorthand are each rejected with a reason |

`DH-LIB-HOSTEDGITINFO-R3` is deferred with the registry
([DH-ADR-13](../design.md#dh-adr-13)); no scenario is declared for it.

## 9. Units dispositioned by this spec

`UP-DEP-hosted-git-info` (replaced).
