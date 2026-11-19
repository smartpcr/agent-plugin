# simple-icons: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Short form: sections 1, 2, 4, 5 and 8 of the template.

| | |
|---|---|
| **Library ID** | `DH-LIB-SIMPLEICONS` |
| **Upstream unit** | `UP-DEP-simple-icons` |
| **Upstream version** | 16.31.0 |
| **Kind** | bundled into the client (data only: SVG path strings) |
| **Used by** | `packages/client/ui-primitives`, one file |
| **Go choice** | **own code and data**: a fixed table from host name to icon, with the icon shapes copied once into the harness's embedded assets. No module |
| **Related areas** | Transcript UI area; [micromark.md](micromark.md), whose link renderer calls this |

## 1. What it is and why upstream needs it

`simple-icons` is a collection of several thousand brand logos, each as one SVG path,
published as a JavaScript package. Upstream uses 34 of them for one small affordance: a
link in the transcript to a well-known site leads with that site's own mark and not with
the generic globe, so a reader recognises a GitHub or Wikipedia link at a glance.

The library is data, not behaviour. The behaviour (which host gets which mark) is 60
lines of upstream code.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-primitives/src/SiteGlyph.tsx:9-45` | Import 34 named icons | `siGithub`, `siWikipedia`, ... (the `path` of each) |
| `SiteGlyph.tsx:55-96` | A table from host suffix to icon. A host matches a suffix when it equals it or is a subdomain of it, and the longest suffix wins, so `weixin.qq.com` is WeChat while `qq.com` is QQ | own code |
| `SiteGlyph.tsx:103-110` | Only an absolute `http` or `https` address can name a host | `URL` |
| `SiteGlyph.tsx:1-7` | The mark is drawn in the link's own colour, not the brand colour; the artwork set is stated to be CC0-1.0 | `currentColor` |
| `docs/web-styling.md:33` | Text links lead with a category glyph, "which for a well-known external host is that site's own mark instead of the globe"; image-only links and workflow member links carry none | style rule |

**Limitation upstream states.** "Known-site marks are a fixed list": recognising any
other site "would require fetching its icon over the network"
(`packages/client/ui-primitives/README.md:243`). That is a deliberate limit, and a good
one: fetching a site's icon would tell that site the user is looking at a link to it.

No patch, pin or `UP-DEBT` unit exists for this package (`patches/` and
`pnpm-workspace.yaml` were searched).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SIMPLEICONS-R1` | A link whose host is on the fixed list, or is a subdomain of an entry, leads with that entry's mark; the longest matching suffix wins; any other link leads with the generic glyph | `SiteGlyph.tsx:47-96` |
| `DH-LIB-SIMPLEICONS-R2` | Only an absolute `http` or `https` address is matched. Host comparison ignores letter case | `SiteGlyph.tsx:103-110` |
| `DH-LIB-SIMPLEICONS-R3` | The mark takes the surrounding text colour and is the same in light and dark themes | `SiteGlyph.tsx:1-7` |
| `DH-LIB-SIMPLEICONS-R4` | No request leaves the host or the browser to obtain an icon | `ui-primitives/README.md:243` |
| `DH-LIB-SIMPLEICONS-R5` | Every embedded mark is recorded with its source package, version and licence, and the table and the embedded set cannot drift apart | Inference: the marks are third-party artwork and trademarks |

## 5. Go choice

**Decision.** The host-to-mark table is a Go value in the UI package. The marks are
copied once, at a recorded `simple-icons` version, into one SVG sprite file (one
`<symbol>` per mark) that is embedded in the binary and served as a static asset. The
link renderer writes a `<use>` reference to the matching symbol. No Go module and no
JavaScript.

**Why.** The need is 34 small path strings and a suffix lookup. Upstream already serves
its own product icons as a sprite of symbols (`ui-primitives/README.md:254`), so the
marks join an existing mechanism.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Copied marks in an embedded sprite, own lookup table** (chosen) | No dependency; no request; the lookup runs where the link is rendered, on the host | Updating a mark is a manual copy; trademarks change and marks are occasionally withdrawn from the collection | No module. The CC0-1.0 statement is upstream's (`SiteGlyph.tsx:4-5`); the package's licence file was **not** read in this task because the package is not installed in the checkout. It must be read when the marks are copied |
| A Go module that packages the collection | Updates by version bump | Thousands of unused icons in the binary for 34 used | None was searched for or verified; not needed |
| Fetch each site's own icon | Covers every site | Leaks the reader's activity to the linked site; needs network; upstream rejects it | n/a |
| Drop the marks; always show the globe | Nothing to maintain | Loses a small, cheap aid to reading | n/a |

**Tradeoff.** The set is frozen until someone updates it by hand. A mark that its owner
objects to is removed by deleting one symbol and one table row.

**Failure.** A host that matches nothing, or an address that does not parse, gets the
generic glyph. A table row whose symbol is missing from the sprite is a build-time test
failure, not a runtime condition.

## 8. Verification

Local tests only. Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-SIMPLEICONS-1` | local | `DH-LIB-SIMPLEICONS-R1`, `-R2` | Table test of the lookup: exact host, subdomain, longest-suffix (`weixin.qq.com` against `qq.com`), mixed case, a look-alike host that only ends with the same letters (`notgithub.com`), a non-HTTP scheme, a relative address |
| `DH-T-SIMPLEICONS-2` | local, golden | `DH-LIB-SIMPLEICONS-R3`, `-R4` | A rendered link to a listed host references a sprite symbol, carries no colour of its own and contains no address other than the link's destination |
| `DH-T-SIMPLEICONS-3` | local | `DH-LIB-SIMPLEICONS-R5` | Every table row names a symbol that exists in the embedded sprite, every symbol is used by a row, and the sprite's manifest entry records package, version and licence |

## 9. Units dispositioned by this spec

- `UP-DEP-simple-icons`
