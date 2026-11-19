# rrule: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go modules named here were checked on 2026-11-18 with
> `go list -m -json <module>@latest` and `go mod download -json`, reading `LICENSE`,
> `go.mod` and source in the module cache.

| | |
|---|---|
| **Library ID** | `DH-LIB-RRULE` |
| **Upstream unit** | `UP-DEP-rrule` |
| **Upstream version** | 2.8.1, pinned exactly (`packages/util/time/package.json`, `pnpm-lock.yaml:27392`) |
| **Kind** | runtime |
| **Used by** | util (`packages/util/time`), one importing file |
| **Go choice** | own code in the harness on the standard library (`time`), built on the local-time resolver of [js-temporal-polyfill.md](js-temporal-polyfill.md) |
| **Related areas** | schedules (`areas/jobs-schedule-goal-plan.md`), shared utilities (`areas/util-test-support.md`) |

## 1. What it is and why upstream needs it

A **recurrence rule** (RRULE) is the text format that calendar files use to say "this
repeats": `FREQ=WEEKLY;BYDAY=MO,WE,FR` means every Monday, Wednesday and Friday. It is
defined by RFC 5545, the iCalendar standard. A rule is read together with a start line,
`DTSTART`, which gives the first occurrence and the time zone.

The harness needs it because RRULE is the language in which the model writes a schedule.
The model-facing tool `schedule_create` takes exactly one timing input, `rrule`
(`packages/schedule/tool-schedule/README.md:40`), and RRULE is one of seven timing kinds
the schedule store keeps (`docs/subsystems/schedule.md:5`). It expresses what a
five-field cron expression cannot: "every second week", "the last weekday of the month",
"ten times and then stop".

The `rrule` npm library lists the occurrences of a rule. Upstream uses it for much less
than that. It wraps the library in 689 lines of its own code
(`packages/util/time/src/rrule.ts`) that validate the text, bound the cost, decide the
time-zone behaviour and apply the end conditions, and it asks the library only to list
candidate wall times inside one period. That narrow use is the main fact behind the Go
choice.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/util/time/src/rrule.ts:3`, `:15` | The only import | default export; `rrulestr`, `Weekday` |
| `packages/util/time/src/rrule.ts:535-537` | Build a rule from a start that upstream rewrites to UTC and a rule line upstream has already validated and normalised | `rrulestr("DTSTART:...Z\nRRULE:...")` |
| `packages/util/time/src/rrule.ts:568`, `:611` | List candidate wall times in order, stopping from a callback | `rule.all(iterator)` |
| `packages/util/time/src/rrule.ts:553` | Ask whether a rule can ever produce a wall time | `rule.after(date, true)` |
| `packages/util/time/src/rrule.ts:407` | Spell a weekday code for an implied `BYDAY` | `new Weekday(n).toString()` |

What upstream does itself, around the library:

| Concern | Upstream's own code | Evidence |
|---|---|---|
| Syntax and the accepted subset | Two lines only; 14 rule parts; value ranges; forbidden combinations | `rrule.ts:52-82`, `:151-322` |
| Cost limits before any listing | `COUNT` at most 1,000; at most 10,080 candidates in one period; at most 31 set positions | `rrule.ts:17-24`, `:366-395` |
| Time zones | The library is always given UTC. Upstream converts each candidate wall time to an instant itself | `rrule.ts:456-471` |
| End conditions | `COUNT` and `UNTIL` are removed before the library sees the rule and applied to real instants | `rrule.ts:119-120`, `:434-435`, `:607-626` |
| Defaults taken from the start | Made explicit so any period can be listed on its own | `rrule.ts:397-411` |
| A rule that never matches | Detected inside one 400-year calendar cycle | `rrule.ts:36-47`, `:539-554` |

## 3. Tech debt upstream carries

No `UP-DEBT` unit is assigned for this dependency. Search performed: `patches/` (none),
`pnpm-workspace.yaml` (no comment), the READMEs of the two consuming packages, and the
comments in `rrule.ts`. The workarounds found in calling code are listed because each one
disappears with the library.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| none assigned | The library reads years 0000 to 0099 as 1900 to 1999, so starts before year 100 are refused | `packages/util/time/src/rrule.ts:26-27` | Removed with the library: the Go evaluator uses the same year range as stored instants (0001 to 9999) |
| none assigned | The library's own time-zone support is not used; every rule is rewritten to UTC and mapped back by the caller | `packages/util/time/src/rrule.ts:103-111`, `:456-471` | Removed: the evaluator works in wall time by design ([DH-ADR-RRULE-1](#dh-adr-rrule-1)) |
| none assigned | The library combines `BYHOUR`, `BYMINUTE` and `BYSECOND` in the order written, so the caller sorts them first | `packages/util/time/src/rrule.ts:436-440` | Removed: candidate times are generated sorted |
| none assigned | The library cuts the first weekly period at the start date, so the caller keeps special anchoring for the first period | `packages/util/time/src/rrule.ts:525-532` | Removed: one rule for every period, "not before the start" |
| none assigned | The recurrence library is loaded by every importer of the time package, and into browser bundles | `packages/util/time/README.md:113` | Not applicable: Go links what is imported, and no schedule code runs in the browser ([DH-ADR-3](../design.md#dh-adr-3)) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-RRULE-R1` | **Input form.** A rule is exactly two non-empty lines: `DTSTART` then `RRULE`. The start is a real date and time written `YYYYMMDDTHHMMSS`, either UTC with `Z` or local with an explicit IANA `TZID`. A start with no zone, a numeric offset, a date without a time, and any further line (`EXDATE`, `RDATE`, a second `RRULE`) are rejected. The trimmed two-line text is what is stored, and it stays authoritative: it is never converted to another kind of rule. | `docs/subsystems/schedule.md:65`, `:10`; `packages/util/time/src/rrule.ts:77-78`, `:151-169` |
| `DH-LIB-RRULE-R2` | **Accepted subset of RFC 5545.** Frequencies `MINUTELY`, `HOURLY`, `DAILY`, `WEEKLY`, `MONTHLY`, `YEARLY`. Parts `FREQ`, `INTERVAL`, `COUNT`, `UNTIL`, `WKST`, `BYSECOND`, `BYMINUTE`, `BYHOUR`, `BYDAY`, `BYMONTHDAY`, `BYYEARDAY`, `BYWEEKNO`, `BYMONTH`, `BYSETPOS`, each at most once, list values unique and in range. Minute and hour rules take no `BY` part. `BYMONTHDAY` and numbered `BYDAY` (`2TU`) need a monthly or yearly rule; `BYYEARDAY` and `BYWEEKNO` need a yearly rule; `BYSETPOS` needs another `BY` part. `WKST` defaults to Monday. Everything else, including `SECONDLY`, is rejected with a message that names the rule broken. | `docs/subsystems/schedule.md:65-67`; `packages/util/time/src/rrule.ts:52-55`, `:194-207`, `:289-322` |
| `DH-LIB-RRULE-R3` | **Limits checked before any occurrence is computed.** `COUNT` is 1 to 1,000 and cannot appear with `UNTIL`. `UNTIL` is an absolute UTC time ending in `Z`, and is inclusive. One period of the rule may hold at most 10,080 candidates (every minute of a week). `BYSECOND` has one value, so occurrences are at least a minute apart. At most 31 set positions, each within the period's candidate count. A rejection says whether the cause is syntax, zone or density. | `docs/subsystems/schedule.md:69`; `packages/util/time/src/rrule.ts:17-24`, `:295-300`, `:366-395`; `packages/util/time/tests/rrule.spec.ts:293` |
| `DH-LIB-RRULE-R4` | **Daylight saving.** Daily and longer rules are evaluated in the wall time of the start's zone. A candidate wall time that does not exist is skipped and does not use up `COUNT`. A wall time that occurs twice gives the earlier instant, once. A start that falls in a gap is rejected. | `docs/subsystems/schedule.md:71`; `packages/util/time/src/rrule.ts:425-427`, `:607-626`; `packages/util/time/tests/rrule.spec.ts:314`, `:330` |
| `DH-LIB-RRULE-R5` | **Two questions, no clock.** `Next(after)` answers the first occurrence strictly after a given instant, or "exhausted". `Resolve(committed, decision)` answers the latest occurrence at or before `decision`, never earlier than `committed`, and the first occurrence after `decision` if one exists. Both are pure functions of their arguments: the package reads no clock and starts no timer. | `packages/util/time/README.md:109`; `packages/util/time/src/rrule.ts:667-688` |
| `DH-LIB-RRULE-R6` | **Missed runs: latest only.** The schedule store holds one **committed occurrence** per task: the earliest occurrence not yet delivered, as a UTC instant. When the host finds it due, however late, it delivers once, labelled with the latest occurrence that is due, and commits the first future occurrence. Occurrences in between are not delivered and not replayed. `COUNT` counts occurrences of the calendar, not deliveries, so occurrences missed while the host was down still use it up. When no future occurrence exists the task is marked ended after that delivery. | `docs/subsystems/schedule.md:24`, `:51`; `packages/schedule/schedule/README.md:12`; `packages/util/time/src/rrule.ts:607-626`, `:679-688` |
| `DH-LIB-RRULE-R7` | **Zone data may change; commitments do not.** A committed occurrence is honoured even if newer zone data would now place the rule's occurrence later: the answer is never earlier than the committed instant, and reading a task never recomputes it. | `packages/util/time/src/rrule.ts:685`; `packages/util/time/src/cron.ts:432-433`; `docs/subsystems/schedule.md:24` |
| `DH-LIB-RRULE-R8` | **Bounded work.** A rule that can never produce an occurrence (30 February) is answered "exhausted" after a bounded search, not an endless one. Resolving after years of downtime does not walk every missed occurrence. | `packages/util/time/src/rrule.ts:539-554`, `:596-605`; `packages/schedule/schedule/tests/daily.spec.ts:159` |
| `DH-LIB-RRULE-R9` | **Stricter checks for new rules only.** When a rule is being written, a `BYSETPOS` that can have no effect is rejected with an example of correct use. A stored rule with the same shape still loads and runs. | `docs/subsystems/schedule.md:69`; `packages/util/time/src/rrule.ts:376-384` |
| `DH-LIB-RRULE-R10` | **Minute and hour rules keep their real spacing.** Consecutive occurrences of a `MINUTELY` or `HOURLY` rule are exactly one interval apart in elapsed time, also across a daylight-saving transition. This changes upstream ([DH-ADR-RRULE-2](#dh-adr-rrule-2)). | Inference from `packages/util/time/src/rrule.ts:561-586`; `docs/subsystems/schedule.md:36` |

## 5. Go choice

**Decision.** Own code: a bounded RRULE evaluator in the harness, next to the cron
evaluator, on `time` and the resolver of
[js-temporal-polyfill.md](js-temporal-polyfill.md) §6. No module.

**Why.** Requirements R1, R2, R3, R4, R6, R7 and R9 are upstream's own code today; the
library contributes only "list the candidate wall times of one period". With the
defaults made explicit (as upstream already does), that step is a filter: a day of the
period is a candidate when it satisfies every `BY` day part that is present, and the
times of day are a small sorted product. A module would replace that filter and leave
everything else to be written anyway, plus the work of discovering the module's quirks,
which cost upstream four workarounds (§3).

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** own bounded evaluator | No dependency; wall-time evaluation and the gap and overlap rule are designed in, not worked around; limits are enforced by construction; the same shape as the cron evaluator | The harness must get the calendar corner cases of RFC 5545 right (numbered weekdays, week numbers, negative positions). Inference: 600 to 800 lines of Go plus tests | Standard library only |
| `github.com/teambition/rrule-go` used as upstream uses its library (UTC in, wall times out) | A port of the well-known Python implementation; lazy iterator; full RFC 5545 rule support | Last release nearly four years ago. Builds each occurrence with `time.Date` in the start's location (`rrule.go:637`, `:672`), so a skipped wall time is moved, not skipped: it could only be used in UTC with all of upstream's wrapper rewritten around it | v1.8.2 (2023-01-13), MIT, `go 1.16`, no requirements in `go.mod`, no `import "C"` |
| `github.com/stephens2424/rrule` | Exists | Last release 2019 | v1.2.0 (2019-06-11); existence and date checked only, licence not read |

**Tradeoff.** The calendar logic becomes the harness's to maintain. It is frozen logic
(the Gregorian calendar and a fixed subset), and `DH-T-RRULE-2` pins it with the RFC's
own examples. If the evaluator proves harder than estimated, `rrule-go` in UTC is the
checked fallback.

<a id="dh-adr-rrule-1"></a>
### DH-ADR-RRULE-1 A bounded evaluator in wall time, written in the harness

- **Decision.** As above. The evaluator lists candidates period by period in wall time,
  converts each to an instant with the shared resolver, and applies `COUNT` and `UNTIL`
  to real instants. The accepted subset and limits of R1 to R3 are kept from upstream
  unchanged, except the lower year bound (§3).
- **Why.** It is the design upstream arrived at around its library. Keeping the subset
  keeps the tool description the model was taught.
- **Tradeoff.** No support for the parts upstream refuses (exception dates, rule sets,
  `SECONDLY`). **Serves.** [DH-O-9](../design.md#dh-o-9).
  **Verified by.** `DH-T-RRULE-1`, `DH-T-RRULE-2`, `DH-T-RRULE-3`, `DH-T-RRULE-4`,
  `DH-T-RRULE-5`.

<a id="dh-adr-rrule-2"></a>
### DH-ADR-RRULE-2 Minute and hour rules advance in elapsed time

- **Decision.** Change upstream, as a recommendation to the schedule area. A
  `MINUTELY` or `HOURLY` rule produces the start instant plus whole multiples of its
  interval. Daily and longer rules stay in wall time (R4).
- **Why.** Inference from `packages/util/time/src/rrule.ts:561-586`: upstream lists
  minute and hour rules in wall time and maps each to the *earlier* instant, so in the
  repeated hour of an autumn transition the second pass gets no occurrence. A rule
  "every 5 minutes" in `America/New_York` is silent for one real hour each November.
  These rules accept no `BY` part (R2), so they are plain fixed intervals, which the
  design already treats as elapsed time ([DH-LIB-TEMPORAL-R7](js-temporal-polyfill.md)).
- **Tradeoff.** In a zone whose offset is not a whole number of hours after a
  transition, an hourly rule no longer lands on the same minute of the wall clock.
  Behaviour differs from upstream for one hour a year in zones with daylight saving;
  [DH-ADR-2](../design.md#dh-adr-2) allows that with this reason recorded.
- **Serves.** [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-RRULE-6`.

**Upstream verdicts.** *Keep*: the two-line form, the subset, the limits, wall-time
evaluation for daily and longer rules, skip in a gap, earlier in an overlap, latest-only
catch-up, the committed occurrence, `COUNT` counting calendar occurrences, pure
functions with explicit times. Note that skipping in a gap differs from RFC 5545, which
moves such a time forward; upstream chose skipping for every timing kind, and a zone can
skip a whole date (`packages/schedule/schedule/tests/daily.spec.ts:145`), where there is
nothing to move to. *Change*: library to own code; minute and hour rules
(DH-ADR-RRULE-2). *Drop*: the browser copy of the evaluator, used by upstream's editor
(`packages/client/ui-schedule/src/client/editor-timing.ts:3`); the server validates the
form ([DH-ADR-3](../design.md#dh-adr-3)).

## 6. Design notes

**Owner.** Inference: a pure package beside the time package, for example
`internal/util/recur`, holding both the cron and the RRULE evaluator behind one
interface; the utilities area owns the name. It owns no resource: no goroutine, timer,
file or socket, so nothing is released on unload. The schedule service (delegation
family) owns the store, the wake timer and delivery.

```go
type Rule interface {
    Expression() string                 // stored text, authoritative
    Zone() string                       // "UTC" or the IANA name given
    Next(after time.Time) (time.Time, bool)
    Resolve(committed, decision time.Time) (Occurrence, error)
}
type Occurrence struct {
    At   time.Time  // latest due occurrence, never before the committed one
    Next time.Time  // zero when the rule is exhausted
}
func ParseRRule(text string, authoring bool) (Rule, *Rejection) // Rejection{Reason: syntax|time_zone|density, Detail}
```

**How one period is listed.** A period is one interval-aligned minute, hour, day, week
(starting on `WKST`), month or year. For a daily or longer rule: walk the days of the
period; keep a day when it passes every present part among `BYMONTH`, `BYWEEKNO`,
`BYYEARDAY`, `BYMONTHDAY` and `BYDAY` (a numbered weekday counts within the month for a
monthly rule or a yearly rule with `BYMONTH`, otherwise within the year); combine with
the sorted times of day; apply `BYSETPOS` to the sorted result; drop anything before the
start. Parts the rule omits are filled from the start first (hour, minute, second;
weekday for weekly; day of month for monthly; month and day for yearly), as upstream
does (`packages/util/time/src/rrule.ts:397-411`).

**Failure and retry.** Parsing returns a rule or a rejection. `Resolve` returns an error
only when its arguments are impossible (a decision before the committed occurrence),
which is a programming error in the caller and is reported, not retried. The package
keeps no state, so there is nothing to roll back. Delivery failure, retry and the
at-least-once guarantee belong to the schedule service.

**What the schedule service must do with this** (stated here because it is where
schedulers go wrong; the schedule area owns the decision):

- Decide "is it due" by comparing the committed instant with the wall clock at the
  moment of waking, never by trusting that a timer fired on time.
- Keep the wake timer short enough that a suspended laptop or a stepped clock delays a
  delivery by a bounded amount: sleep for the smaller of "time to the next committed
  occurrence" and a configurable cap. Area A13 owns the value and sets it to 30 seconds
  (`schedule.Config.wakeCap`,
  [../areas/jobs-schedule-goal-plan.md](../areas/jobs-schedule-goal-plan.md)
  `DH-ADR-JOBS-6`), half the one-minute minimum spacing of R3. Upstream caps the sleep only because Node timers overflow at about 24.8
  days (`packages/schedule/schedule/src/runtime.ts:29`, `:222-223`).
- If the clock moves backwards and a task is no longer due, do nothing and keep waiting
  (`packages/schedule/schedule/src/runtime.ts:243-252`).
- Make the wake timer an effect of the schedule fiber
  ([node-crypto-timers-perf-hooks.md](node-crypto-timers-perf-hooks.md) §6), so tests
  drive it in virtual time and unload cancels it.

## 7. Risks and open questions

1. **For the schedule area: adopt or overrule DH-ADR-RRULE-2.** It changes behaviour for
   one hour a year. The alternative is upstream's behaviour, stated in the decision.
2. **For the schedule area: the wake-timer cap** in §6 is a recommendation, not a
   requirement of this library.
3. **Cron is not a dependency.** Upstream's five-field cron evaluator is its own code
   (`packages/util/time/src/cron.ts`, 492 lines) and is dispositioned by the utilities
   area. It should be ported on the same resolver. Two modules were looked at for it:
   `github.com/robfig/cron/v3` (v3.0.1, 2020-01-04, MIT) offers only "next", has no
   "latest at or before", and bundles a job runner the harness does not need;
   `github.com/adhocore/gronx` (v1.20.7, 2026-11-12, MIT) was checked for existence and
   licence only. Neither is chosen by this spec.
4. **Corner cases of week numbers and negative positions** are the likeliest source of
   defects. A differential test against `rrule-go` in UTC is possible as extra evidence
   but would add a test-only requirement to the module; left to the implementer.
5. **Rule descriptions for people** ("every weekday at 09:00") are produced by upstream's
   client code, not by this library. The UI area owns them.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-RRULE-1` | local | `DH-LIB-RRULE-R1`, `DH-LIB-RRULE-R2`, `DH-LIB-RRULE-R3`, `DH-LIB-RRULE-R9` | A table of texts: each accepted part and frequency; each rejection of R1 to R3 with its reason (`syntax`, `time_zone`, `density`); a one-candidate `BYSETPOS` rejected in authoring mode and accepted when loading. |
| `DH-T-RRULE-2` | local | `DH-LIB-RRULE-R2`, `DH-LIB-RRULE-R5` | Occurrence vectors: every example of RFC 5545 section 3.8.5.3 that falls inside the subset, plus last weekday of the month, every second week with a non-Monday `WKST`, week 53, day −1 of the year and 29 February. `Next` is strictly after its argument. |
| `DH-T-RRULE-3` | local | `DH-LIB-RRULE-R4` | In `America/New_York`: a daily 02:30 rule has no occurrence on 2021-03-14 and `COUNT=3` still yields three; a daily 01:30 rule fires once on 2021-11-07 at the earlier instant; a start in a gap is rejected. |
| `DH-T-RRULE-4` | local | `DH-LIB-RRULE-R6`, `DH-LIB-RRULE-R7` | `Resolve` after a long gap returns only the latest due occurrence and the next one; a `COUNT=5` rule resolved after all five have passed returns the fifth and no next; an `UNTIL` rule ends inclusively; a committed instant later than the recomputed occurrence is returned unchanged. |
| `DH-T-RRULE-5` | local | `DH-LIB-RRULE-R8` | A rule for 30 February returns "exhausted"; resolving a daily rule committed in 1800 at a decision in 2026 completes within a fixed operation budget (a counter in the evaluator, not wall time). |
| `DH-T-RRULE-6` | local | `DH-LIB-RRULE-R10` | A `MINUTELY;INTERVAL=5` rule in `America/New_York` yields an occurrence every 300 seconds through both 2021 transitions (DH-ADR-RRULE-2). |

Local tests only: they prove the evaluator against written inputs and zone data built
into the test binary. That a scheduled instruction is delivered by a running host at the
right moment is a real-environment scenario the schedule area owns. Nothing here has
been run.

## 9. Units dispositioned by this spec

`UP-DEP-rrule`.
