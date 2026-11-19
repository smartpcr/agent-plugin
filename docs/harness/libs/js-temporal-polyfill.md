# @js-temporal/polyfill: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go facts were checked on 2026-11-18 with `go doc` and the `api/`
> files of the installed Go 1.27.4 toolchain; modules were checked with
> `go list -m -json <module>@latest` and `go mod download -json`, reading `LICENSE` and
> source in the module cache.

| | |
|---|---|
| **Library ID** | `DH-LIB-TEMPORAL` |
| **Upstream unit** | `UP-DEP-@js-temporal/polyfill` |
| **Upstream version** | 0.5.1, pinned exactly (`packages/util/time/package.json`, `pnpm-lock.yaml:22272`); it brings `jsbi` 4.3.2 (`pnpm-lock.yaml:29979-29981`) |
| **Kind** | runtime |
| **Used by** | util (`packages/util/time`), schedule (`packages/schedule/schedule`) |
| **Go choice** | standard library (`time`, `time/tzdata`) plus a small local-time resolver written in the harness |
| **Related areas** | schedules (`areas/jobs-schedule-goal-plan.md`), shared utilities (`areas/util-test-support.md`), request context (`areas/context-compaction-skill.md`), the command-line build (`areas/app-cli.md`); recurrence rules in [rrule.md](rrule.md); timers and virtual time in [node-crypto-timers-perf-hooks.md](node-crypto-timers-perf-hooks.md) |

## 1. What it is and why upstream needs it

Three kinds of time appear in this document. An **instant** is a point on the global
timeline, written in UTC. A **wall time** (also called local time) is what a clock on a
wall shows: a date and a time of day with no zone, such as "8 March, 02:30". A **time
zone** in the IANA database sense (a name such as `America/New_York`) is the rule set
that turns one into the other, including daylight-saving transitions. At a spring
transition some wall times never happen (a **gap**); at an autumn transition some happen
twice (an **overlap**).

Temporal is the modern JavaScript date and time interface. JavaScript's built-in `Date`
cannot answer "which instant is 02:30 on this date in this zone, and does that wall time
exist at all", so upstream uses the Temporal polyfill for exactly that question and for
calendar-date arithmetic. It matters because the harness delivers scheduled instructions:
"every weekday at 09:00 in Asia/Shanghai" must fire at the right instant on every date,
including the two days a year when the clock changes.

Upstream does not use Temporal as a general time library. It stores every time as a UTC
instant string, does elapsed-time arithmetic on plain millisecond numbers, and calls
Temporal only where a zone or a calendar is involved
(`.agents/notes/implemented/architecture/2026-09-24-shared-time-vocabulary.md:25`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/util/time/src/instant.ts:139`, `:310`; `packages/util/time/src/rrule.ts:144` | Refuse a date the calendar does not have (30 February), instead of rolling it forward | `Temporal.PlainDateTime.from(fields, { overflow: 'reject' })` |
| `packages/util/time/src/instant.ts:311-312` | Turn a wall time in a named zone into an instant: the earlier instant in an overlap, "no such time" in a gap (detected by converting back and comparing) | `toZonedDateTime(zone, { disambiguation: 'earlier' })`, `toPlainDateTime().equals()` |
| `packages/util/time/src/instant.ts:206` | Epoch value of a wall time read as UTC, for explicit-offset input | `toZonedDateTime('UTC').epochMilliseconds` |
| `packages/util/time/src/cron.ts:12`, `:351-356`; `packages/util/time/src/rrule.ts:458`; `packages/schedule/schedule/src/domain.ts:660` | Turn an instant into the local date, hour, minute and UTC offset of a zone | `Temporal.Instant.fromEpochMilliseconds().toZonedDateTimeISO(zone)`, `.toPlainDate()`, `.offsetNanoseconds` |
| `packages/util/time/src/cron.ts:357-374`, `:402-436`; `packages/schedule/schedule/src/domain.ts:683-714` | Walk calendar dates forward and backward, one day or one month at a time; read month, day and ISO weekday | `Temporal.PlainDate` `add`, `subtract`, `with`, `compare`, `equals`, `dayOfWeek`, `toPlainDateTime(time)` |
| `packages/schedule/schedule/src/domain.ts:427-447` | Hold a validated time of day for daily and weekly rules | `Temporal.PlainTime.from` |
| `packages/schedule/schedule/src/domain.ts:1315-1320` | Show an instant as local date and time with its numeric offset, to the second | `Temporal.Instant.from().toZonedDateTimeISO(zone).toString({ timeZoneName: 'never', smallestUnit: 'second' })` |

Two things beside Temporal complete the picture and are needed to state the requirement.
Zone names are validated and rewritten to a "canonical" spelling by the JavaScript `Intl`
interface, not by Temporal (`packages/util/time/src/instant.ts:275-290`). Strict text
parsing is upstream's own regular expressions, run before any date is constructed
(`packages/util/time/src/instant.ts:25-43`). The consumers of the shared time package are
the session controller and subagents (admit the zone a browser reports), the time-context
component (refuses a zone alias), the schedule service and the schedule editor in the web
client (`packages/util/time/README.md:32`).

## 3. Tech debt upstream carries

No `UP-DEBT` unit is assigned for this dependency. Search performed: `patches/` (no
patch), `pnpm-workspace.yaml` (no pin or override comment), the "Known Limitations"
sections of `packages/util/time/README.md` and `packages/schedule/schedule/README.md`.
The limits those READMEs admit are listed so that their Go resolution is on record.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| none assigned | Which name a zone alias is rewritten to depends on the data built into the Node binary, "so two processes on different Node builds can disagree" | `packages/util/time/README.md:108` | Removed: zone names are validated and stored as given, never rewritten ([DH-ADR-TEMPORAL-2](#dh-adr-temporal-2)) |
| none assigned | Importing the time package loads Temporal and the recurrence library even for a caller that needs only a zone check; browser bundles pay for both | `packages/util/time/README.md:113` | Not applicable: Go links only the packages a program imports, and no time code runs in the browser ([DH-ADR-3](../design.md#dh-adr-3)) |
| none assigned | A polyfill for a language feature, with a big-integer library behind it, pinned to an exact version | `packages/util/time/package.json`; `pnpm-lock.yaml:29979-29981` | Not applicable: the Go standard library has zone-aware time built in |
| none assigned | Rules use "current IANA data": a rule's future occurrences can move when the zone data changes | `packages/schedule/schedule/README.md:283` | Kept as a stated limit and made visible: [DH-ADR-TEMPORAL-1](#dh-adr-temporal-1) says where zone data comes from on each platform |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-TEMPORAL-R1` | **Stored instants.** Every stored time is one text form: UTC, exactly three fractional digits, a `Z`, a year from 0001 to 9999 (for example `2099-01-01T01:00:00.000Z`). Reading accepts only that form and a real calendar date; anything else is a rejection with a stable reason, never a silent correction. A stored instant is never recomputed from zone data on read. | `docs/subsystems/schedule.md:24`; `packages/util/time/src/instant.ts:229-236` |
| `DH-LIB-TEMPORAL-R2` | **Input instants.** An absolute time from a person or the model is either an RFC 3339 instant with an explicit `Z` or numeric offset, or a local date, a local time and a zone name. A time without a zone or offset is rejected; the offset `-00:00`, a leap second and a date that does not exist are rejected. The rejection carries a reason the caller turns into its own error. | `docs/subsystems/schedule.md:41`; `packages/util/time/src/instant.ts:173`, `:189-219` |
| `DH-LIB-TEMPORAL-R3` | **Zone names.** A zone is `UTC` or an IANA `Area/Location` name the host's zone data knows. Abbreviations (`EST`), numeric offsets, single-segment names, the empty string, padded text and the Go-specific name `Local` are rejected. | `packages/util/time/src/instant.ts:43`, `:275-290` |
| `DH-LIB-TEMPORAL-R4` | **Gap.** A wall time that does not exist in its zone has no instant. A recurring rule skips that date's occurrence (it is not moved to another time). A one-shot `at` input and a recurrence start in a gap are rejected as invalid input. | `docs/subsystems/schedule.md:36`, `:71`; `packages/util/time/src/instant.ts:312`; `packages/schedule/schedule/src/domain.ts:297-302`, `:691` |
| `DH-LIB-TEMPORAL-R5` | **Overlap.** A wall time that occurs twice resolves to the earlier instant, and a recurring rule fires once for that date. | `packages/util/time/src/instant.ts:311`; `packages/util/time/tests/cron.spec.ts:225` |
| `DH-LIB-TEMPORAL-R6` | **Calendar walking.** Given an instant and a zone, the local date, time of day, ISO weekday (Monday 1 to Sunday 7) and offset are available, and dates can be stepped by days and months, including across a date a zone skipped entirely. | `packages/schedule/schedule/src/domain.ts:683-714`; `packages/schedule/schedule/tests/daily.spec.ts:145` |
| `DH-LIB-TEMPORAL-R7` | **Elapsed time ignores zones.** Fixed delays and fixed intervals are arithmetic on instants. A daylight-saving change never lengthens or shortens them. | `docs/subsystems/schedule.md:36`; `packages/schedule/schedule/src/domain.ts:641-648` |
| `DH-LIB-TEMPORAL-R8` | **Display.** An instant is shown in the rule's own zone as local date and time with the numeric offset (for example `2099-01-01T09:00:00+08:00`); rules without a zone show UTC. Where the host must name a zone of its own and no client or configuration supplied one, it shows the numeric offset and does not invent a zone name. | `packages/schedule/schedule/src/domain.ts:1315-1320`; `packages/context/time-context/src/index.ts:134-146` |
| `DH-LIB-TEMPORAL-R9` | **Zone data is always present.** A release binary resolves every IANA zone on a machine with no zone database and no Go installation, in particular on Windows. Which data source is in use is reported by a diagnostic. | Inference from the single-binary release ([DH-O-14](../design.md#dh-o-14)); `packages/schedule/schedule/README.md:283` |

## 5. Go choice

**Decision.** The standard library: `time` for instants, zones and calendar fields,
`time/tzdata` for an embedded zone database, and about one hundred lines of harness code
for the one thing `time` leaves undefined, which is what to do in a gap or an overlap.
No module.

**Why.** Every row of §2 has a direct counterpart: `time.Time` is an instant,
`time.LoadLocation` loads a zone, `t.In(loc)` gives local fields and the offset,
`AddDate` steps dates. The exception is local-to-instant conversion. `time.Date`
documents that for a skipped or repeated wall time "the choice of time zone, and
therefore the time, is not well-defined" and that it returns a time correct in one of the
two zones "but it does not guarantee which" (`go doc time.Date`, Go 1.27.4). R4 and R5
need a defined answer, so the harness owns that function.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `time` + `time/tzdata` + own resolver | No dependency; the gap and overlap rule is written once and tested; zone data ships in the binary | The harness owns the resolver and the strict parsers | Standard library. `Time.ZoneBounds` exists since Go 1.19 (`api/go1.19.txt:292`); `time/tzdata` adds "about 450 KB" (`go doc time/tzdata`) |
| `time.Date` used directly | Nothing to write | Gap times are silently moved and overlap choice is unspecified, which breaks R4 and R5 | `go doc time.Date` |
| A third-party date library | Might offer disambiguation options | None was found that is needed: the missing piece is one function. Not searched further, so none is named | n/a |

**Tradeoff.** Go has no separate types for "date without zone" or "time of day"; the
harness uses small structs of its own for those (§6) so that a wall time cannot be
mistaken for an instant.

<a id="dh-adr-temporal-1"></a>
### DH-ADR-TEMPORAL-1 Zone data: embedded in the binary, system data first where it exists

- **Decision.** The `dsh` main package imports `time/tzdata` (equivalently, the release
  build sets the `timetzdata` build tag). Zones are loaded with `time.LoadLocation`,
  which looks, in order, at the `ZONEINFO` environment variable, the system database on
  Unix, `$GOROOT/lib/time/zoneinfo.zip`, and then the embedded copy
  (`go doc time.LoadLocation`). A loaded zone is cached for the life of the process.
- **Why.** Go finds no IANA database on Windows, and a release machine has no Go
  installation, so without the embedded copy `LoadLocation("America/New_York")` fails there and no
  zoned rule can be created (R9). On Linux and macOS the system copy is used first
  because it is updated when governments change their rules, without a harness release.
- **Tradeoff.** The embedded data is as old as the Go toolchain that built the release.
  On Windows a rule change therefore needs a new release, or the operator points
  `ZONEINFO` at a newer `zoneinfo.zip`. Two machines can hold different data, so the
  same rule can compute a different *future* occurrence on each; committed occurrences
  are stored as UTC and do not move (R1). About 450 KB is added to the binary.
- **Alternative considered.** Always read only the embedded copy, for identical results
  on every machine. Rejected: the standard library does not expose the embedded data
  through a public function, so this means shipping and refreshing a second copy of the
  database by hand, and it makes Linux installations staler than their own system.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-14](../design.md#dh-o-14).
  **Verified by.** `DH-T-TEMPORAL-5`, and the real-environment scenario requested in §7.

<a id="dh-adr-temporal-2"></a>
### DH-ADR-TEMPORAL-2 Zone names are validated and kept as given, not rewritten

- **Decision.** Change upstream. A zone name is accepted when it has the
  `Area/Location` shape (or is `UTC`) and `time.LoadLocation` loads it. It is stored
  and shown exactly as supplied. The harness has no "canonical name" step and never
  compares two zone names to decide whether two zones are the same.
- **Why.** Upstream rewrites an alias (`US/Eastern`) to the name its runtime prefers and
  admits that two Node builds can disagree (`packages/util/time/README.md:108`). The
  schedule store already keeps the alias a rule was written with
  (`packages/schedule/schedule/tests/domain-restart.spec.ts:126`). Go has no alias table:
  a compiled zone file does not say that it is a link to another name. Dropping the step
  removes a platform-dependent answer from durable data. Occurrence arithmetic is
  unaffected, because an alias and its target load the same rules.
- **Tradeoff.** Two spellings of one zone are two different strings. A browser reports a
  canonical name already (`packages/api/session-controller/src/client/time-zone.ts:9`),
  so upstream's refusal of a non-canonical browser zone
  (`packages/context/time-context/src/request-zone.ts:30-36`) has nothing to enforce and
  is dropped with it. On a file system that ignores letter case, a wrongly cased name
  may load from the system database; the shape check does not catch that (§7).
- **Serves.** [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-TEMPORAL-3`.

**Upstream verdicts.** *Keep*: UTC instants in storage with one text form; explicit zone
or offset on every input; reasons instead of exceptions from parsers; gap is skipped and
overlap takes the earlier instant; elapsed time ignores zones. *Change*: the library
(standard library instead of a polyfill) and zone canonicalisation
(DH-ADR-TEMPORAL-2). *Drop*: millisecond epoch numbers as the in-memory form (Go uses
`time.Time`), and the browser copy of this code.

## 6. Design notes

**Owner.** Inference: a pure package in the shared-utilities family, for example
`internal/util/caltime`; the utilities area owns the final name. It holds no state except
the zone cache, starts nothing and owns no resource, so there is nothing to release when
a fiber unloads. Recurrence ([rrule.md](rrule.md)) and the schedule service build on it.

**Shape.**

```go
type LocalTime struct{ Year, Month, Day, Hour, Minute, Second, Millisecond int } // a wall time; not an instant

func ParseStored(s string) (time.Time, Reason)            // R1: canonical UTC profile only
func FormatStored(t time.Time) (string, bool)             // false outside years 0001-9999
func ParseOffsetInstant(s string) (time.Time, Reason)     // R2
func ParseLocal(date, clock string) (LocalTime, Reason)   // R2
func LoadZone(name string) (*time.Location, Reason)       // R3; cached
func Resolve(l LocalTime, loc *time.Location) (t time.Time, exists bool) // R4, R5
func FormatLocal(t time.Time, loc *time.Location) string  // R8
```

`Reason` is a small closed set (`not_rfc3339`, `out_of_range`, `not_a_calendar_date`,
`invalid_numeric_offset`, `malformed`, `blank`, `padded`, `not_area_location`,
`unsupported`), kept from upstream so that each caller maps it to its own error.

**How `Resolve` decides.** Compute `t := time.Date(fields..., loc)`. Collect up to three
candidate offsets: the offset at `t`, the offset just before the start of `t`'s zone
period and the offset at its end (`t.ZoneBounds()`). For each offset, the candidate
instant is the wall time read as UTC minus that offset. Keep the candidates whose local
fields in `loc` equal the requested fields. None left means a gap: return
`exists == false`. Otherwise return the earliest. Inference: looking only at the
neighbouring zone periods is exact unless two transitions fall within hours of each
other; `DH-T-TEMPORAL-2` includes the largest jump in the database, a whole skipped day.

**Strict parsing.** `time.Parse` is more lenient than R1 and R2 (it accepts fractional
seconds of any length). The harness checks the shape first, parses, and for the stored
form formats the result back and requires the same text. All stored instants are
truncated to milliseconds so that a value read back equals the value written.

**Arithmetic.** Fixed intervals are computed on Unix milliseconds (`int64`), not with
`time.Duration`, whose range is about 292 years; a stored rule may sit further in the
future than that. `time.Time` values that carry a monotonic reading (those from
`time.Now`) are stripped with `Round(0)` before they are stored or compared with a
stored instant, so that equality means the same instant.

**Failure.** Every function returns a value or a reason; none panics on input. A zone
that loaded once stays cached; new zone data is picked up at the next start. The
diagnostic of R9 infers the data source by following the order `LoadLocation` uses (is
`ZONEINFO` set, does a system database exist, otherwise embedded), because the standard
library does not report which one answered.

## 7. Risks and open questions

1. **For the schedule area: the daylight-saving and missed-run contract.** R4, R5 and R7
   are the time half. The delivery half is in [rrule.md](rrule.md) §4 (latest-only
   catch-up, committed occurrence, counted rules). The area should restate both as its
   own acceptance criteria.
2. **For the release owner: a real-environment scenario for R9.** Given a Windows
   machine with no Go installation and `ZONEINFO` unset; when the released `dsh` creates
   a daily task in `Australia/Lord_Howe`; then the task is accepted and its next
   occurrence matches the published rule; cleanup deletes the task. A library spec
   declares no scenario, so the owning area must.
3. **For the context and command-line areas: the host's own zone has no name in Go.**
   `time.Local` is named `Local` on Windows and on Unix unless `TZ` is set
   (`src/time/zoneinfo_unix.go:42-55` in the toolchain). Upstream's time-context falls
   back to the process zone name. R8 says to show the offset instead. If a name is
   wanted, `github.com/thlib/go-timezone-local` exists (v0.1.0, 2026-11-17, Unlicense
   with an Apache-2.0 file for its data, requires `golang.org/x/sys`); it was checked for
   existence and licence only and is not chosen here.
4. **For the UI areas: localised dates.** The standard library formats month and weekday
   names in English only. Upstream's client uses the browser's `Intl.DateTimeFormat`
   (`packages/client/ui-schedule/src/client/schedule-format.ts:362`). Inference: the
   server should emit `<time datetime="...">` with an ISO text fallback and let a
   few lines of script localise it in the browser, which already knows the locale.
5. **Letter case of zone names** on case-insensitive file systems (DH-ADR-TEMPORAL-2).
   If this matters, validate against the embedded name list at build time.
6. **Zone data age on Windows** is a standing operational limit (DH-ADR-TEMPORAL-1). The
   diagnostic of R9 should print the source so a support request can tell.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-TEMPORAL-1` | local | `DH-LIB-TEMPORAL-R1`, `DH-LIB-TEMPORAL-R2` | Table of texts: the canonical form at both year bounds round-trips; two or four fractional digits, a missing `Z`, year 0000, 30 February, hour 24, second 60, offset `-00:00` and a zoneless time are each rejected with the expected reason. |
| `DH-T-TEMPORAL-2` | local | `DH-LIB-TEMPORAL-R4`, `DH-LIB-TEMPORAL-R5`, `DH-LIB-TEMPORAL-R6` | Historical transitions with embedded zone data: `America/New_York` 2021-03-14 02:30 does not exist; 2021-11-07 01:30 resolves to the earlier instant (offset −04:00); `Australia/Lord_Howe` half-hour gap and overlap; `Pacific/Apia` has no 2011-12-30; a date walk steps over it. |
| `DH-T-TEMPORAL-3` | local | `DH-LIB-TEMPORAL-R3` | `UTC`, `Asia/Shanghai` and the alias `US/Eastern` load and keep their spelling; `""`, `Local`, `EST`, `+08:00`, ` Asia/Tokyo` and `Mars/Olympus` are rejected with distinct reasons. |
| `DH-T-TEMPORAL-4` | local | `DH-LIB-TEMPORAL-R7`, `DH-LIB-TEMPORAL-R8` | An interval added across a transition advances by exactly its length; the display form of an instant in a zone and in UTC matches fixed strings; an interval that would pass year 9999 reports "no further occurrence" instead of overflowing. |
| `DH-T-TEMPORAL-5` | local | `DH-LIB-TEMPORAL-R9` | A build check that `time/tzdata` is among the dependencies of the `dsh` main package (`go list -deps`), and a test that the diagnostic names the zone-data source. A development machine has Go's own zone file, so a local test cannot show that the embedded copy is the one in use; §7 item 2 asks for that proof on a real machine. |

Local tests only. They prove the resolver against zone data built into the test binary;
they do not prove a released binary on a bare Windows machine (§7 item 2). Nothing here
has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@js-temporal/polyfill`.
