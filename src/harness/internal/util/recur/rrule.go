// Package recur is the harness's one meaning of a recurrence rule (DH-D-UTIL-4 /
// DH-D-UTIL-11, docs/harness/libs/rrule.md DH-ADR-RRULE-1). A rule is the two-line
// RFC 5545 text `DTSTART` + `RRULE`, restricted to a bounded subset, and is
// evaluated in the wall time of the start's zone on the resolver of package
// caltime: a wall time the zone skips is no occurrence, a wall time it repeats is
// the earlier instant, once. Minute and hour rules advance in elapsed time
// (DH-ADR-RRULE-2).
//
// The package reads no clock and starts no timer: Next and Resolve are pure
// functions of their arguments, and every search is bounded.
//
//dsh:untrusted-parser rules arrive from people, the model and stored schedules
package recur

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/caltime"
)

// Limits checked before any occurrence is computed (DH-LIB-RRULE-R3).
const (
	MaxCount      = 1000
	MaxCandidates = 10080 // every minute of a week, in one period
	MaxSetPos     = 31
	MaxInterval   = 10000
)

// Rule is a parsed recurrence rule.
type Rule interface {
	// Expression is the stored text, which stays authoritative.
	Expression() string
	// Zone is "UTC" or the IANA name the rule was written with.
	Zone() string
	// Next is the first occurrence strictly after after; false when the rule is
	// exhausted.
	Next(after time.Time) (time.Time, bool)
	// Resolve is the latest occurrence at or before decision, never earlier than
	// committed, and the first occurrence after decision.
	Resolve(committed, decision time.Time) (Occurrence, error)
}

// Occurrence answers Resolve.
type Occurrence struct {
	At   time.Time // latest due occurrence, never before the committed one
	Next time.Time // zero when the rule is exhausted
}

// Reason classifies a rejection.
type Reason string

// The rejection reasons.
const (
	Syntax   Reason = "syntax"
	TimeZone Reason = "time_zone"
	Density  Reason = "density"
)

// Rejection reports a rule that was refused.
type Rejection struct {
	Reason Reason
	Detail string
}

func (r *Rejection) Error() string { return fmt.Sprintf("recur: %s: %s", r.Reason, r.Detail) }

func reject(r Reason, format string, args ...any) *Rejection {
	return &Rejection{Reason: r, Detail: fmt.Sprintf(format, args...)}
}

// Freq is a rule's frequency.
type Freq int

// The accepted frequencies.
const (
	Minutely Freq = iota + 1
	Hourly
	Daily
	Weekly
	Monthly
	Yearly
)

var freqNames = map[string]Freq{"MINUTELY": Minutely, "HOURLY": Hourly, "DAILY": Daily, "WEEKLY": Weekly, "MONTHLY": Monthly, "YEARLY": Yearly}

// weekday is an ISO weekday: Monday 1 to Sunday 7.
var dayNames = map[string]int{"MO": 1, "TU": 2, "WE": 3, "TH": 4, "FR": 5, "SA": 6, "SU": 7}

type byDay struct {
	n   int // 0 for every such weekday of the period
	day int // ISO weekday
}

type rrule struct {
	text  string
	zone  string
	loc   *time.Location
	start time.Time
	wall  caltime.LocalTime // start in loc

	freq     Freq
	interval int
	count    int        // 0 when absent
	until    *time.Time // inclusive
	wkst     int

	seconds, minutes, hours []int
	days                    []byDay
	monthDays, yearDays     []int
	weekNos, months         []int
	setPos                  []int

	ops *int // when set, counts the periods listed (tests)
}

// Parse reads a rule. With authoring set, the stricter checks for a rule being
// written apply (DH-LIB-RRULE-R9); a stored rule is parsed with authoring unset.
func Parse(text string, authoring bool) (Rule, *Rejection) {
	r, rej := parse(text, authoring)
	if rej != nil {
		return nil, rej
	}
	return r, nil
}

func parse(text string, authoring bool) (*rrule, *Rejection) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	lines := strings.Split(text, "\n")
	if len(lines) != 2 {
		return nil, reject(Syntax, "a rule is exactly two lines, DTSTART then RRULE; found %d", len(lines))
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
		if lines[i] == "" {
			return nil, reject(Syntax, "line %d is empty", i+1)
		}
	}
	r := &rrule{text: lines[0] + "\n" + lines[1], wkst: 1, interval: 1}
	if rej := r.parseStart(lines[0]); rej != nil {
		return nil, rej
	}
	body, ok := strings.CutPrefix(lines[1], "RRULE:")
	if !ok {
		return nil, reject(Syntax, "the second line must start with RRULE:")
	}
	if rej := r.parseParts(body); rej != nil {
		return nil, rej
	}
	if rej := r.check(authoring); rej != nil {
		return nil, rej
	}
	r.fillDefaults()
	return r, nil
}

func (r *rrule) parseStart(line string) *Rejection {
	head, value, ok := strings.Cut(line, ":")
	if !ok {
		return reject(Syntax, "DTSTART has no value")
	}
	zone := ""
	switch {
	case head == "DTSTART":
	case strings.HasPrefix(head, "DTSTART;"):
		param := strings.TrimPrefix(head, "DTSTART;")
		name, ok := strings.CutPrefix(param, "TZID=")
		if !ok || name == "" || strings.Contains(name, ";") {
			return reject(Syntax, "DTSTART takes only a TZID parameter")
		}
		zone = name
	default:
		return reject(Syntax, "the first line must be DTSTART")
	}
	utc := strings.HasSuffix(value, "Z")
	digits := strings.TrimSuffix(value, "Z")
	l, ok := basicDateTime(digits)
	if !ok {
		if len(value) == 8 {
			return reject(Syntax, "DTSTART %q is a date without a time", value)
		}
		if strings.ContainsAny(value, "+-") {
			return reject(TimeZone, "DTSTART %q has a numeric offset; use Z or TZID", value)
		}
		return reject(Syntax, "DTSTART %q is not a real date and time YYYYMMDDTHHMMSS", value)
	}
	switch {
	case utc && zone != "":
		return reject(TimeZone, "DTSTART has both Z and TZID")
	case utc:
		r.zone, r.loc = "UTC", time.UTC
	case zone == "":
		return reject(TimeZone, "DTSTART %q has no zone; end it with Z or give a TZID", value)
	default:
		loc, why := caltime.LoadZone(zone)
		if why != caltime.OK {
			return reject(TimeZone, "TZID %q: %s", zone, why)
		}
		r.zone, r.loc = zone, loc
	}
	t, exists := caltime.Resolve(l, r.loc)
	if !exists {
		return reject(TimeZone, "DTSTART %s does not exist in %s (a daylight-saving gap)", value, r.zone)
	}
	r.start, r.wall = t, l
	return nil
}

// basicDateTime reads YYYYMMDDTHHMMSS.
func basicDateTime(s string) (caltime.LocalTime, bool) {
	if len(s) != 15 || s[8] != 'T' {
		return caltime.LocalTime{}, false
	}
	for i, c := range []byte(s) {
		if i != 8 && (c < '0' || c > '9') {
			return caltime.LocalTime{}, false
		}
	}
	n := func(a, b int) int { v, _ := strconv.Atoi(s[a:b]); return v }
	l := caltime.LocalTime{Year: n(0, 4), Month: n(4, 6), Day: n(6, 8), Hour: n(9, 11), Minute: n(11, 13), Second: n(13, 15)}
	return l, l.Valid()
}

func (r *rrule) parseParts(body string) *Rejection {
	if body == "" {
		return reject(Syntax, "RRULE is empty")
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(body, ";") {
		key, val, ok := strings.Cut(part, "=")
		if !ok || key == "" || val == "" {
			return reject(Syntax, "RRULE part %q is not NAME=VALUE", part)
		}
		if seen[key] {
			return reject(Syntax, "RRULE part %s appears twice", key)
		}
		seen[key] = true
		var rej *Rejection
		switch key {
		case "FREQ":
			f, ok := freqNames[val]
			if !ok {
				return reject(Syntax, "FREQ=%s is not one of MINUTELY, HOURLY, DAILY, WEEKLY, MONTHLY, YEARLY", val)
			}
			r.freq = f
		case "INTERVAL":
			r.interval, rej = number(key, val, 1, MaxInterval)
		case "COUNT":
			r.count, rej = number(key, val, 1, MaxCount)
		case "UNTIL":
			l, ok := basicDateTime(strings.TrimSuffix(val, "Z"))
			if !ok || !strings.HasSuffix(val, "Z") {
				return reject(Syntax, "UNTIL=%s must be an absolute UTC time YYYYMMDDTHHMMSSZ", val)
			}
			u, _ := caltime.Resolve(l, time.UTC)
			r.until = &u
		case "WKST":
			d, ok := dayNames[val]
			if !ok {
				return reject(Syntax, "WKST=%s is not a weekday", val)
			}
			r.wkst = d
		case "BYSECOND":
			r.seconds, rej = list(key, val, 0, 59, false)
		case "BYMINUTE":
			r.minutes, rej = list(key, val, 0, 59, false)
		case "BYHOUR":
			r.hours, rej = list(key, val, 0, 23, false)
		case "BYMONTHDAY":
			r.monthDays, rej = list(key, val, 1, 31, true)
		case "BYYEARDAY":
			r.yearDays, rej = list(key, val, 1, 366, true)
		case "BYWEEKNO":
			r.weekNos, rej = list(key, val, 1, 53, true)
		case "BYMONTH":
			r.months, rej = list(key, val, 1, 12, false)
		case "BYSETPOS":
			r.setPos, rej = list(key, val, 1, 366, true)
		case "BYDAY":
			r.days, rej = days(val)
		default:
			return reject(Syntax, "RRULE part %s is not supported", key)
		}
		if rej != nil {
			return rej
		}
	}
	if r.freq == 0 {
		return reject(Syntax, "RRULE has no FREQ")
	}
	return nil
}

func number(key, val string, lo, hi int) (int, *Rejection) {
	n, err := strconv.Atoi(val)
	if err != nil || strings.HasPrefix(val, "+") || n < lo || n > hi {
		return 0, reject(Syntax, "%s=%s must be a whole number from %d to %d", key, val, lo, hi)
	}
	return n, nil
}

// list reads a comma list of unique values in lo..hi, or, when signed, in
// -hi..-lo and lo..hi.
func list(key, val string, lo, hi int, signed bool) ([]int, *Rejection) {
	var out []int
	for _, s := range strings.Split(val, ",") {
		n, err := strconv.Atoi(s)
		abs := n
		if signed && n < 0 {
			abs = -n
		}
		if err != nil || s == "" || (s[0] == '+' && !signed) || (n < 0 && !signed) || abs < lo || abs > hi {
			return nil, reject(Syntax, "%s value %q is out of range", key, s)
		}
		if slices.Contains(out, n) {
			return nil, reject(Syntax, "%s lists %d twice", key, n)
		}
		out = append(out, n)
	}
	return out, nil
}

func days(val string) ([]byDay, *Rejection) {
	var out []byDay
	for _, s := range strings.Split(val, ",") {
		if len(s) < 2 {
			return nil, reject(Syntax, "BYDAY value %q is not a weekday", s)
		}
		d, ok := dayNames[s[len(s)-2:]]
		if !ok {
			return nil, reject(Syntax, "BYDAY value %q is not a weekday", s)
		}
		n := 0
		if num := s[:len(s)-2]; num != "" {
			v, err := strconv.Atoi(num)
			abs := max(v, -v)
			if err != nil || abs < 1 || abs > 53 {
				return nil, reject(Syntax, "BYDAY value %q has an ordinal outside 1 to 53", s)
			}
			n = v
		}
		b := byDay{n, d}
		if slices.Contains(out, b) {
			return nil, reject(Syntax, "BYDAY lists %s twice", s)
		}
		out = append(out, b)
	}
	return out, nil
}

// check applies the cross-part rules and the density limits.
func (r *rrule) check(authoring bool) *Rejection {
	if r.count > 0 && r.until != nil {
		return reject(Syntax, "COUNT and UNTIL cannot both appear")
	}
	if len(r.seconds) > 1 {
		return reject(Density, "BYSECOND takes one value, so occurrences are at least a minute apart")
	}
	anyBy := r.seconds != nil || r.minutes != nil || r.hours != nil || r.days != nil || r.monthDays != nil ||
		r.yearDays != nil || r.weekNos != nil || r.months != nil || r.setPos != nil
	if (r.freq == Minutely || r.freq == Hourly) && anyBy {
		return reject(Syntax, "a MINUTELY or HOURLY rule takes no BY part")
	}
	numbered := slices.ContainsFunc(r.days, func(b byDay) bool { return b.n != 0 })
	if (r.monthDays != nil || numbered) && r.freq != Monthly && r.freq != Yearly {
		return reject(Syntax, "BYMONTHDAY and a numbered BYDAY need FREQ=MONTHLY or YEARLY")
	}
	if (r.yearDays != nil || r.weekNos != nil) && r.freq != Yearly {
		return reject(Syntax, "BYYEARDAY and BYWEEKNO need FREQ=YEARLY")
	}
	if numbered && r.weekNos != nil {
		return reject(Syntax, "a numbered BYDAY cannot be combined with BYWEEKNO")
	}
	if r.setPos != nil {
		if r.seconds == nil && r.minutes == nil && r.hours == nil && r.days == nil &&
			r.monthDays == nil && r.yearDays == nil && r.weekNos == nil && r.months == nil {
			return reject(Syntax, "BYSETPOS needs another BY part")
		}
		if len(r.setPos) > MaxSetPos {
			return reject(Density, "BYSETPOS lists more than %d positions", MaxSetPos)
		}
	}
	bound := r.candidateBound()
	if bound > MaxCandidates {
		return reject(Density, "one period may hold up to %d candidates; the limit is %d", bound, MaxCandidates)
	}
	for _, p := range r.setPos {
		if max(p, -p) > bound {
			return reject(Density, "BYSETPOS %d is beyond the at most %d candidates of one period", p, bound)
		}
	}
	if authoring && r.setPos != nil && bound <= 1 {
		return reject(Syntax, "BYSETPOS has no effect when a period has one candidate; "+
			"for example FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1 is the last weekday of the month")
	}
	return nil
}

// candidateBound is an upper bound on the candidates of one period.
func (r *rrule) candidateBound() int {
	times := max(len(r.hours), 1) * max(len(r.minutes), 1) * max(len(r.seconds), 1)
	var days int
	switch r.freq {
	case Minutely, Hourly:
		return 1
	case Daily:
		days = 1
	case Weekly:
		days = 7
		if r.days != nil {
			days = min(days, len(r.days))
		}
	case Monthly, Yearly:
		months := 12
		if r.freq == Monthly {
			months = 1
		}
		if r.months != nil {
			months = min(months, len(r.months))
		}
		days = 31 * months
		if r.freq == Yearly && r.months == nil {
			days = 366
		}
		if r.monthDays != nil {
			days = min(days, len(r.monthDays)*months)
		}
		if r.yearDays != nil {
			days = min(days, len(r.yearDays))
		}
		if r.weekNos != nil {
			days = min(days, 7*len(r.weekNos))
		}
		if r.days != nil {
			n := 0
			for _, b := range r.days {
				switch {
				case b.n != 0 && (r.freq == Monthly || r.months != nil):
					n += months
				case b.n != 0:
					n++
				case r.freq == Monthly || r.months != nil:
					n += 5 * months
				default:
					n += 53
				}
			}
			days = min(days, n)
		}
	}
	return days * times
}

// fillDefaults fills the parts a daily or longer rule omits from its start.
func (r *rrule) fillDefaults() {
	if r.freq == Minutely || r.freq == Hourly {
		return
	}
	if r.seconds == nil {
		r.seconds = []int{r.wall.Second}
	}
	if r.minutes == nil {
		r.minutes = []int{r.wall.Minute}
	}
	if r.hours == nil {
		r.hours = []int{r.wall.Hour}
	}
	dayParts := r.days != nil || r.monthDays != nil || r.yearDays != nil || r.weekNos != nil
	switch r.freq {
	case Weekly:
		if r.days == nil {
			r.days = []byDay{{0, r.wall.ISOWeekday()}}
		}
	case Monthly:
		if !dayParts {
			r.monthDays = []int{r.wall.Day}
		}
	case Yearly:
		switch {
		case r.weekNos != nil && r.days == nil && r.monthDays == nil && r.yearDays == nil:
			r.days = []byDay{{0, r.wall.ISOWeekday()}}
		case !dayParts:
			if r.months == nil {
				r.months = []int{r.wall.Month}
			}
			r.monthDays = []int{r.wall.Day}
		}
	}
	slices.Sort(r.seconds)
	slices.Sort(r.minutes)
	slices.Sort(r.hours)
}

func (r *rrule) Expression() string { return r.text }
func (r *rrule) Zone() string       { return r.zone }
