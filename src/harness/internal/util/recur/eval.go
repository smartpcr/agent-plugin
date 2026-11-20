package recur

import (
	"errors"
	"slices"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/caltime"
)

// ErrNotDue is returned by Resolve when the decision instant is before the
// committed occurrence.
var ErrNotDue = errors.New("recur: the decision instant is before the committed occurrence")

// emptyLimit is how many consecutive periods without an occurrence end the
// search. Each covers one 400-year Gregorian cycle, after which the calendar
// repeats, so a rule with no occurrence by then has none at all (DH-LIB-RRULE-R8).
func (r *rrule) emptyLimit() int {
	switch r.freq {
	case Daily:
		return 146097 + 1
	case Weekly:
		return 20871 + 1
	case Monthly:
		return 4800 + 1
	default:
		return 400 + 1
	}
}

func civil(l caltime.LocalTime) time.Time {
	return time.Date(l.Year, time.Month(l.Month), l.Day, 0, 0, 0, 0, time.UTC)
}

func isoWeekday(d time.Time) int {
	if w := int(d.Weekday()); w != 0 {
		return w
	}
	return 7
}

func daysIn(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func daysBetween(a, b time.Time) int { return int((b.Unix() - a.Unix()) / 86400) }

// weekStart is the first day of the WKST-week holding d.
func (r *rrule) weekStart(d time.Time) time.Time {
	return d.AddDate(0, 0, -((isoWeekday(d) - r.wkst + 7) % 7))
}

// firstWeek is the first day of week 1 of year: the first WKST-week with at
// least four days in the year.
func (r *rrule) firstWeek(year int) time.Time {
	jan1 := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	off := (isoWeekday(jan1) - r.wkst + 7) % 7
	if off <= 3 {
		return jan1.AddDate(0, 0, -off)
	}
	return jan1.AddDate(0, 0, 7-off)
}

// weekNo is the week of d and the number of weeks in that week's year.
func (r *rrule) weekNo(d time.Time) (week, weeks int) {
	y := d.Year()
	switch {
	case !d.Before(r.firstWeek(y + 1)):
		y++
	case d.Before(r.firstWeek(y)):
		y--
	}
	first := r.firstWeek(y)
	return daysBetween(first, d)/7 + 1, daysBetween(first, r.firstWeek(y+1)) / 7
}

// periodOf is the index of the period holding t, never negative.
func (r *rrule) periodOf(t time.Time) int {
	if !t.After(r.start) {
		return 0
	}
	w := caltime.Local(t, r.loc)
	var n int
	switch r.freq {
	case Daily:
		n = daysBetween(civil(r.wall), civil(w))
	case Weekly:
		n = daysBetween(r.weekStart(civil(r.wall)), civil(w)) / 7
	case Monthly:
		n = (w.Year-r.wall.Year)*12 + w.Month - r.wall.Month
	default:
		n = w.Year - r.wall.Year
	}
	return max(n/r.interval, 0)
}

// period lists the wall-time candidates of period p in order, after BYSETPOS;
// false once the period starts after year 9999.
func (r *rrule) period(p int) ([]caltime.LocalTime, bool) {
	var from time.Time
	var n int
	start := civil(r.wall)
	switch r.freq {
	case Daily:
		from, n = start.AddDate(0, 0, p*r.interval), 1
	case Weekly:
		from, n = r.weekStart(start).AddDate(0, 0, 7*p*r.interval), 7
	case Monthly:
		months := r.wall.Month - 1 + p*r.interval
		y, m := r.wall.Year+months/12, months%12+1
		if y > 9999 {
			return nil, false
		}
		from, n = time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC), daysIn(y, m)
	default:
		y := r.wall.Year + p*r.interval
		if y > 9999 {
			return nil, false
		}
		from = time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		n = daysBetween(from, from.AddDate(1, 0, 0))
	}
	if from.Year() > 9999 {
		return nil, false
	}
	var out []caltime.LocalTime
	for i := range n {
		d := from.AddDate(0, 0, i)
		if !r.matches(d) {
			continue
		}
		for _, h := range r.hours {
			for _, m := range r.minutes {
				for _, s := range r.seconds {
					out = append(out, caltime.LocalTime{Year: d.Year(), Month: int(d.Month()), Day: d.Day(), Hour: h, Minute: m, Second: s})
				}
			}
		}
	}
	if r.setPos == nil || len(out) == 0 {
		return out, true
	}
	var picked []int
	for _, pos := range r.setPos {
		i := pos - 1
		if pos < 0 {
			i = len(out) + pos
		}
		if i >= 0 && i < len(out) && !slices.Contains(picked, i) {
			picked = append(picked, i)
		}
	}
	slices.Sort(picked)
	sel := make([]caltime.LocalTime, len(picked))
	for k, i := range picked {
		sel[k] = out[i]
	}
	return sel, true
}

func inSigned(list []int, v, size int) bool {
	for _, n := range list {
		if n == v || (n < 0 && size+1+n == v) {
			return true
		}
	}
	return false
}

// matches applies the BY day parts to day d of the current period: in this
// evaluator every BY part is the intersection of the period with its set,
// which is what RFC 5545's expand-then-limit table yields for the subset.
func (r *rrule) matches(d time.Time) bool {
	y, m, day := d.Year(), int(d.Month()), d.Day()
	if r.months != nil && !slices.Contains(r.months, m) {
		return false
	}
	yearLen := daysBetween(time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC))
	if r.yearDays != nil && !inSigned(r.yearDays, d.YearDay(), yearLen) {
		return false
	}
	if r.monthDays != nil && !inSigned(r.monthDays, day, daysIn(y, m)) {
		return false
	}
	if r.weekNos != nil {
		w, weeks := r.weekNo(d)
		if !inSigned(r.weekNos, w, weeks) {
			return false
		}
	}
	if r.days == nil {
		return true
	}
	wd := isoWeekday(d)
	inMonth := r.freq == Monthly || r.months != nil
	for _, b := range r.days {
		if b.day != wd {
			continue
		}
		if b.n == 0 {
			return true
		}
		pos, size := d.YearDay(), yearLen
		if inMonth {
			pos, size = day, daysIn(y, m)
		}
		if b.n > 0 && (pos-1)/7+1 == b.n || b.n < 0 && (size-pos)/7+1 == -b.n {
			return true
		}
	}
	return false
}

// scan yields the occurrences from period from on, in order, until yield
// returns false or the rule is exhausted. COUNT is honoured only from period 0.
func (r *rrule) scan(from int, yield func(time.Time) bool) {
	n, empty, limit := 0, 0, r.emptyLimit()
	for p := from; ; p++ {
		if r.ops != nil {
			*r.ops++
		}
		walls, ok := r.period(p)
		if !ok {
			return
		}
		got := false
		for _, w := range walls {
			t, exists := caltime.Resolve(w, r.loc)
			if !exists || t.Before(r.start) {
				continue // a skipped wall time uses up no COUNT
			}
			got = true
			n++
			if r.count > 0 && n > r.count || r.until != nil && t.After(*r.until) {
				return
			}
			if !yield(t) {
				return
			}
		}
		if got {
			empty = 0
		} else if empty++; empty > limit {
			return
		}
	}
}

// elapsed answers the minute and hour rules: occurrence k is start + k steps of
// elapsed time (DH-ADR-RRULE-2). last is the highest k allowed, or -1 when none.
func (r *rrule) elapsed() (step int64, last int64) {
	step = int64(r.interval) * 60
	if r.freq == Hourly {
		step *= 60
	}
	last = (time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC).Unix() - r.start.Unix()) / step
	if r.count > 0 {
		last = min(last, int64(r.count-1))
	}
	if r.until != nil {
		if r.until.Before(r.start) {
			return step, -1
		}
		last = min(last, (r.until.Unix()-r.start.Unix())/step)
	}
	return step, last
}

func (r *rrule) Next(after time.Time) (time.Time, bool) {
	if r.freq == Minutely || r.freq == Hourly {
		step, last := r.elapsed()
		k := int64(0)
		if !after.Before(r.start) {
			k = (after.Unix()-r.start.Unix())/step + 1
		}
		if k > last {
			return time.Time{}, false
		}
		return r.at(k * step), true
	}
	from := 0
	if r.count == 0 {
		from = max(r.periodOf(after)-1, 0)
	}
	var out time.Time
	found := false
	r.scan(from, func(t time.Time) bool {
		if t.After(after) {
			out, found = t, true
			return false
		}
		return true
	})
	return out, found
}

// atOrBefore is the latest occurrence at or before decision. Without COUNT it
// searches back from decision in widening windows, so years of downtime cost a
// few periods, not one step per missed occurrence; floor bounds the search
// because nothing before the committed occurrence is ever returned.
func (r *rrule) atOrBefore(decision, floor time.Time) (time.Time, bool) {
	if decision.Before(r.start) {
		return time.Time{}, false
	}
	if r.freq == Minutely || r.freq == Hourly {
		step, last := r.elapsed()
		k := min((decision.Unix()-r.start.Unix())/step, last)
		if k < 0 {
			return time.Time{}, false
		}
		return r.at(k * step), true
	}
	latest := func(from int) (time.Time, bool) {
		var out time.Time
		found := false
		r.scan(from, func(t time.Time) bool {
			if t.After(decision) {
				return false
			}
			out, found = t, true
			return true
		})
		return out, found
	}
	if r.count > 0 {
		return latest(0)
	}
	top, bottom := r.periodOf(decision), r.periodOf(floor)
	for w := 1; ; w *= 2 {
		from := max(top-w, 0)
		if t, ok := latest(from); ok {
			return t, true
		}
		if from == 0 || from <= bottom {
			return time.Time{}, false
		}
	}
}

func (r *rrule) Resolve(committed, decision time.Time) (Occurrence, error) {
	if decision.Before(committed) {
		return Occurrence{}, ErrNotDue
	}
	at := committed // a commitment is honoured even if zone data moved (R7)
	if t, ok := r.atOrBefore(decision, committed); ok && t.After(committed) {
		at = t
	}
	next, _ := r.Next(decision)
	return Occurrence{At: at, Next: next}, nil
}

// at is the start plus secs of elapsed time; time.Duration spans only 292 years.
func (r *rrule) at(secs int64) time.Time {
	return time.Unix(r.start.Unix()+secs, int64(r.start.Nanosecond())).In(r.loc)
}
