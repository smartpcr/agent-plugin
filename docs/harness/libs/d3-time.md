# d3-time: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-D3TIME` |
| **Upstream unit** | `UP-DEP-d3-time` |
| **Upstream version** | 3.1.0 (ISC, `THIRD_PARTY_NOTICES.md:94`) |
| **Kind** | bundled into the client |
| **Used by** | experimental (`packages/experimental/timeline`) |
| **Go choice** | standard library (`time`) plus a few lines of own code |
| **Related areas** | X3 (timeline) |

## 1. What it is and why upstream needs it

`d3-time` is the calendar arithmetic part of the D3 charting library. Given a start, an
end and a wanted number of marks, its `timeTicks` function returns "round" moments
between them: every five minutes, every hour, every day, whichever spacing comes closest
to the wanted count. A time axis labelled 14:00, 15:00, 16:00 reads better than one
labelled 14:07, 15:07, 16:07.

Upstream uses it in one place: the experimental Timeline's report view, a vertical
schedule of the last seven days (`packages/experimental/timeline/src/policy.ts:10`) that
needs grid lines and labels down its time axis.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/timeline/src/client/TimeScale.ts:1`, `:46-57` | Ticks for the part of the axis on screen. The result is then restricted to whole minutes, sorted newest first, and thinned so that two labels are never closer than 65% of the wanted spacing | `timeTicks(start, stop, count)` |
| `packages/experimental/timeline/src/client/Schedule.tsx:138-146` | Called twice per draw: once for major marks (spacing of eight text heights) and once for minor marks (three), dropping a minor mark that sits on or near a major one | `TimeScale.ticks` |

This is the only import of `d3-time` in the checkout (searched for `from 'd3-time'`).
No patch, pin or exemption exists for it, and the Timeline README lists no limitation
caused by it.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-D3TIME-R1` | For a time range and a wanted count, return marks on round calendar boundaries in **the viewer's time zone**, at a spacing chosen from a fixed ladder (minutes, hours, days), never finer than one minute, in time order | `TimeScale.ts:46-49` |
| `DH-LIB-D3TIME-R2` | Marks stay round across a daylight-saving change: an hourly mark is on the hour before and after it | Inference: `timeTicks` works in local calendar time; a fixed-duration step would drift by an hour |
| `DH-LIB-D3TIME-R3` | Major and minor marks never overlap, and two labels are never closer than the stated fraction of the wanted spacing | `TimeScale.ts:51-56`, `Schedule.tsx:140-146` |

## 5. Go choice

**Decision.** The standard library. Under [DH-ADR-3](../design.md#dh-adr-3) the host
renders the schedule, so the ticks are computed in Go: choose the step from a fixed
ladder (1, 5, 15 and 30 minutes; 1, 3, 6 and 12 hours; 1, 2 and 7 days) as the one
whose count is nearest the wanted count, then walk the range in the viewer's
`*time.Location`, building each mark with `time.Date` so that it lands on a calendar
boundary. Inference: this is a few dozen lines in the timeline package.

**Why.** One function of the library is used, at one site, for a fixed seven-day window.
A dependency is not justified, and Go's `time` package already does calendar arithmetic
in a named zone.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`time` + own ladder** (chosen) | No dependency; zone-aware by construction | The ladder is ours to keep sensible | Standard library, Go 1.27.4 installed |
| Keep `d3-time` in a browser island | Same marks as upstream | An island that only does arithmetic breaks the island rule of [DH-ADR-GOUISTACK-6](go-ui-stack.md#dh-adr-gouistack-6) | Not pursued |
| A Go charting module | Would bring axes too | Far more than one function; none was verified | Not pursued |

**Tradeoff.** The marks need not be identical to D3's for every range. The host must
know the viewer's time zone, which HTTP does not carry; it comes from the cookie
described in [DH-ADR-GOUISTACK-8](go-ui-stack.md#dh-adr-gouistack-8). Upstream computes
ticks for the scrolled part of the axis on every draw; a host-rendered page computes
them for the whole window once, and the timeline area decides whether seven days of
marks are sent at once or paged.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-D3TIME-1` | local | `DH-LIB-D3TIME-R1`, `R2`, `R3` | Table test in three zones (UTC, one with a daylight-saving change inside the range, one with a 30-minute offset): every mark is on a boundary of the chosen step in that zone, marks are ordered and at least one minute apart, the count is within a factor of two of the wanted count, and no minor mark coincides with a major one |

Nothing has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-d3-time`
