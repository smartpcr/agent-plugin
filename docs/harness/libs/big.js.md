# big.js: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-BIGJS` |
| **Upstream unit** | `UP-DEP-big.js` |
| **Upstream version** | 7.0.1 |
| **Kind** | bundled into the client |
| **Used by** | client (`packages/client/ui-settings-account`) |
| **Go choice** | standard library: `math/big` |
| **Related areas** | C7 (settings and account pages), A17 (account service) |

## 1. What it is and why upstream needs it

The account page shows the user's DeepSeek balance. The platform sends each wallet's
balance as a **decimal string** such as `"12.3456"`, and may use forms such as `0E-16`
or `1e+3` (`packages/account/deepseek-account/src/details.ts:11-13`). JavaScript
numbers are binary floating point, so comparing or rounding money with them can be off
by a cent. big.js is a small arbitrary-precision decimal library; upstream uses it in
the browser to compare and round these strings exactly.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-settings-account/src/client/formatBalance.ts:11-15` | Format an amount: zero is `0.00`; a positive amount below one cent is `<0.01`; otherwise round **down** to two places and add thousands separators; a negative amount shows its absolute value, or `0.01` when it is smaller than a cent | `new Big(amount)`, `.eq`, `.lt`, `.gt`, `.abs`, `.round(2, Big.roundDown)`, `.toFixed(2)` |
| `packages/client/ui-settings-account/src/client/AccountSection.tsx:96` | Show only bonus wallets whose balance is above zero | `new Big(balance).gt(0)` |
| `packages/client/ui-settings-account/src/client/onboarding-balance.ts:11` | Decide whether the account has any positive balance, for onboarding guidance | `new Big(balance).gt(0)` |
| `packages/account/deepseek-account/src/details.ts:11-13` | The host validates the string against the grammar big.js accepts, so the browser never throws on it | regular expression, not the library |

No patch, pin or exemption names it (`patches/` and `pnpm-workspace.yaml` searched).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-BIGJS-R1` | A balance string is parsed exactly as a decimal, including an omitted integer or fraction part and a decimal exponent. A string outside that grammar is rejected where the platform response is decoded, not where it is displayed. | `details.ts:11-13` |
| `DH-LIB-BIGJS-R2` | Display follows the five formatting rules of `formatBalance.ts:11-15`, with rounding toward zero and no binary floating-point step. | `formatBalance.ts:11-15` |
| `DH-LIB-BIGJS-R3` | "Has a positive balance" is an exact comparison with zero. | `AccountSection.tsx:96`, `onboarding-balance.ts:11` |

## 5. Go choice

**Decision.** `math/big.Rat` from the standard library, in the host. The account page
is rendered by the host ([DH-ADR-3](../design.md#dh-adr-3)), so the formatted text is
produced in Go and the browser needs no decimal library.

**Why.** `Rat.SetString` accepts decimal strings with an optional exponent and keeps
them as exact fractions. Comparison is `Rat.Sign` and `Rat.Cmp`. Rounding down to two
places is integer division of the numerator times 100 by the denominator
(`big.Int.Quo` truncates toward zero), which is exact.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `math/big` | Standard library; exact; no dependency for three small functions | `Rat.SetString` also accepts fractions such as `1/3` and other bases' prefixes, so the grammar check of `DH-LIB-BIGJS-R1` stays in front of it | Go 1.27.4 standard library |
| A decimal module | A richer decimal type | A dependency for comparison with zero and one rounding rule | not evaluated; none is named |
| `float64` | Shortest code | Wrong by construction for money | n/a |

**Tradeoff.** None of substance. The formatting moves from the browser to the host, so
a balance is formatted when the page fragment is rendered, not on every repaint.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-BIGJS-1` | local | `DH-LIB-BIGJS-R1` to `-R3` | Table test: `0`, `0E-16`, `.5`, `5.`, `1e+3`, `0.009`, `0.019`, `1234567.899`, `-0.001`, `-12.345`, and rejected inputs `1/3`, `0x10`, `abc`, an empty string; expected text and sign for each. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-big.js`.
