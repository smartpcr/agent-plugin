// Package caltime is the harness's one meaning of an instant and a wall time in a
// zone (DH-D-UTIL-4 / DH-D-UTIL-11, docs/harness/libs/js-temporal-polyfill.md). It
// adds to the standard library's time package the strict parsers every boundary
// shares and the one answer for a wall time that a zone skips or repeats.
//
// Parsers return a Reason instead of an error, so each caller states the failure in
// its own words. No function panics on input. The only state is a cache of loaded
// zones.
//
//dsh:untrusted-parser instants and zone names arrive from people, the model and stored rules
package caltime

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Reason says why a text was refused; the empty Reason means it was accepted.
type Reason string

// The closed set of reasons.
const (
	OK                   Reason = ""
	NotRFC3339           Reason = "not_rfc3339"
	OutOfRange           Reason = "out_of_range"
	NotACalendarDate     Reason = "not_a_calendar_date"
	InvalidNumericOffset Reason = "invalid_numeric_offset"
	Malformed            Reason = "malformed"
	Blank                Reason = "blank"
	Padded               Reason = "padded"
	NotAreaLocation      Reason = "not_area_location"
	Unsupported          Reason = "unsupported"
	UnknownZone          Reason = "unknown_zone"
)

// StoredLayout is the one text form of a stored instant: UTC, three fractional
// digits, a Z.
const StoredLayout = "2006-01-02T15:04:05.000Z"

// DisplayLayout shows an instant as local date and time with its numeric offset.
const DisplayLayout = "2006-01-02T15:04:05-07:00"

// LocalTime is a wall time with no zone: it is not an instant.
type LocalTime struct{ Year, Month, Day, Hour, Minute, Second, Millisecond int }

// Valid reports whether l names a real calendar date and a time of day.
func (l LocalTime) Valid() bool {
	if l.Year < 1 || l.Year > 9999 || l.Hour < 0 || l.Hour > 23 || l.Minute < 0 || l.Minute > 59 ||
		l.Second < 0 || l.Second > 59 || l.Millisecond < 0 || l.Millisecond > 999 {
		return false
	}
	t := l.utc()
	return t.Year() == l.Year && int(t.Month()) == l.Month && t.Day() == l.Day
}

func (l LocalTime) utc() time.Time {
	return time.Date(l.Year, time.Month(l.Month), l.Day, l.Hour, l.Minute, l.Second, l.Millisecond*int(time.Millisecond), time.UTC)
}

// ISOWeekday is Monday 1 to Sunday 7.
func (l LocalTime) ISOWeekday() int {
	wd := int(l.utc().Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

// AddDays steps the date by n days and keeps the time of day. It walks the
// calendar, not a zone, so a date a zone skipped is stepped over like any other.
func (l LocalTime) AddDays(n int) LocalTime { return localOf(l.utc().AddDate(0, 0, n)) }

// String is the ISO form without an offset.
func (l LocalTime) String() string { return l.utc().Format("2006-01-02T15:04:05.000") }

func localOf(t time.Time) LocalTime {
	return LocalTime{t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond() / int(time.Millisecond)}
}

// Local returns the wall time of t in loc.
func Local(t time.Time, loc *time.Location) LocalTime { return localOf(t.In(loc)) }

// ParseStored reads a stored instant. Only StoredLayout with a real date in years
// 0001 to 9999 is accepted.
func ParseStored(s string) (time.Time, Reason) {
	if r := blankOrPadded(s); r != OK {
		return time.Time{}, r
	}
	if !shape(s, "dddd-dd-ddTdd:dd:dd.dddZ") {
		return time.Time{}, NotRFC3339
	}
	l, r := fields(s[:10], s[11:23])
	if r != OK {
		return time.Time{}, r
	}
	return l.utc(), OK
}

// FormatStored writes t in the stored form, truncated to milliseconds; false when t
// is outside years 0001 to 9999.
func FormatStored(t time.Time) (string, bool) {
	u := t.UTC().Truncate(time.Millisecond)
	if u.Year() < 1 || u.Year() > 9999 {
		return "", false
	}
	return u.Format(StoredLayout), true
}

// Stored truncates t to the precision of the stored form and strips its monotonic
// reading, so that equality means the same instant as a value read back.
func Stored(t time.Time) time.Time { return t.Round(0).UTC().Truncate(time.Millisecond) }

// ParseOffsetInstant reads an RFC 3339 instant with an explicit Z or numeric
// offset, as people and the model write it. A zoneless time, the offset -00:00 and
// a leap second are refused.
func ParseOffsetInstant(s string) (time.Time, Reason) {
	if r := blankOrPadded(s); r != OK {
		return time.Time{}, r
	}
	if len(s) < 20 || !shape(s[:19], "dddd-dd-ddTdd:dd:dd") {
		return time.Time{}, NotRFC3339
	}
	rest := s[19:]
	frac := 0
	if rest != "" && rest[0] == '.' {
		n := 1
		for n < len(rest) && isDigit(rest[n]) {
			n++
		}
		if n == 1 || n > 10 {
			return time.Time{}, NotRFC3339
		}
		digits := rest[1:n] + strings.Repeat("0", 10-n)
		frac, _ = strconv.Atoi(digits)
		rest = rest[n:]
	}
	var off int
	switch {
	case rest == "Z":
	case len(rest) == 6 && (rest[0] == '+' || rest[0] == '-') && shape(rest[1:], "dd:dd"):
		h, _ := strconv.Atoi(rest[1:3])
		m, _ := strconv.Atoi(rest[4:6])
		if h > 23 || m > 59 || (rest == "-00:00") {
			return time.Time{}, InvalidNumericOffset
		}
		off = h*3600 + m*60
		if rest[0] == '-' {
			off = -off
		}
	case rest == "":
		return time.Time{}, NotRFC3339
	default:
		if strings.ContainsAny(rest[:1], "+-") {
			return time.Time{}, InvalidNumericOffset
		}
		return time.Time{}, NotRFC3339
	}
	l, r := fields(s[:10], s[11:19]+".000")
	if r != OK {
		return time.Time{}, r
	}
	t := l.utc().Add(time.Duration(frac)).Add(-time.Duration(off) * time.Second)
	if t.Year() < 1 || t.Year() > 9999 {
		return time.Time{}, OutOfRange
	}
	return t, OK
}

// ParseLocal reads a local date `YYYY-MM-DD` and a clock `HH:MM`, `HH:MM:SS` or
// `HH:MM:SS.mmm`.
func ParseLocal(date, clock string) (LocalTime, Reason) {
	for _, s := range []string{date, clock} {
		if r := blankOrPadded(s); r != OK {
			return LocalTime{}, r
		}
	}
	if !shape(date, "dddd-dd-dd") {
		return LocalTime{}, Malformed
	}
	switch {
	case shape(clock, "dd:dd"):
		clock += ":00.000"
	case shape(clock, "dd:dd:dd"):
		clock += ".000"
	case shape(clock, "dd:dd:dd.ddd"):
	default:
		return LocalTime{}, Malformed
	}
	return fields(date, clock)
}

var zones sync.Map // name -> *time.Location

// LoadZone loads `UTC` or an IANA `Area/Location` zone and caches it. The name is
// kept as given: aliases are not rewritten (DH-ADR-TEMPORAL-2).
func LoadZone(name string) (*time.Location, Reason) {
	if r := blankOrPadded(name); r != OK {
		return nil, r
	}
	if name == "UTC" {
		return time.UTC, OK
	}
	if name == "Local" {
		return nil, Unsupported
	}
	if name[0] == '+' || name[0] == '-' {
		return nil, InvalidNumericOffset
	}
	if !areaLocation(name) {
		return nil, NotAreaLocation
	}
	if v, ok := zones.Load(name); ok {
		return v.(*time.Location), OK
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, UnknownZone
	}
	v, _ := zones.LoadOrStore(name, loc)
	return v.(*time.Location), OK
}

// areaLocation reports whether name is two or more segments of letters, digits,
// `_`, `+` and `-`, the first starting with a letter, and no segment `.` or `..`.
func areaLocation(name string) bool {
	segs := strings.Split(name, "/")
	if len(segs) < 2 {
		return false
	}
	for i, seg := range segs {
		if seg == "" {
			return false
		}
		for j := 0; j < len(seg); j++ {
			c := seg[j]
			letter := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
			if i == 0 && j == 0 && !letter {
				return false
			}
			if !letter && !isDigit(c) && c != '_' && c != '+' && c != '-' {
				return false
			}
		}
	}
	return true
}

// Resolve converts a wall time in loc to an instant. A wall time the zone skips has
// none (exists is false); a wall time the zone repeats resolves to the earlier
// instant. time.Date leaves both undefined.
func Resolve(l LocalTime, loc *time.Location) (t time.Time, exists bool) {
	if loc == nil || !l.Valid() {
		return time.Time{}, false
	}
	guess := time.Date(l.Year, time.Month(l.Month), l.Day, l.Hour, l.Minute, l.Second, l.Millisecond*int(time.Millisecond), loc)
	_, off := guess.Zone()
	offsets := []int{off}
	start, end := guess.ZoneBounds()
	if !start.IsZero() {
		_, o := start.Add(-time.Nanosecond).Zone()
		offsets = append(offsets, o)
	}
	if !end.IsZero() {
		_, o := end.Zone()
		offsets = append(offsets, o)
	}
	wall := l.utc()
	found := false
	for _, o := range offsets {
		c := wall.Add(-time.Duration(o) * time.Second)
		if Local(c, loc) != l {
			continue
		}
		if !found || c.Before(t) {
			t, found = c, true
		}
	}
	if !found {
		return time.Time{}, false
	}
	return t.In(loc), true
}

// FormatLocal shows t in loc as local date and time with the numeric offset; a nil
// loc shows UTC.
func FormatLocal(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format(DisplayLayout)
}

// AddMillis adds a fixed interval in elapsed time, which no zone change lengthens
// or shortens. Arithmetic is on Unix milliseconds, so it is not bounded by
// time.Duration's range; false when the result passes year 9999 (no further
// occurrence).
func AddMillis(t time.Time, ms int64) (time.Time, bool) {
	const maxMs = 253402300799999 // 9999-12-31T23:59:59.999Z
	const minMs = -62135596800000 // 0001-01-01T00:00:00.000Z
	base := t.UnixMilli()
	if (ms > 0 && base > maxMs-ms) || (ms < 0 && base < minMs-ms) {
		return time.Time{}, false
	}
	return time.UnixMilli(base + ms).UTC(), true
}

// ZoneDataSource names where time.LoadLocation finds zone data, following its
// search order: ZONEINFO, the system database, the Go installation's copy, then the
// copy embedded in the binary. The standard library does not report which answered.
func ZoneDataSource() string {
	if z := os.Getenv("ZONEINFO"); z != "" {
		return "ZONEINFO=" + z
	}
	if runtime.GOOS != "windows" {
		for _, dir := range []string{"/usr/share/zoneinfo/", "/usr/share/lib/zoneinfo/", "/usr/lib/locale/TZ/", "/etc/zoneinfo/"} {
			if _, err := os.Stat(filepath.Join(dir, "UTC")); err == nil {
				return "system:" + dir
			}
		}
	}
	if root := runtime.GOROOT(); root != "" {
		zip := filepath.Join(root, "lib", "time", "zoneinfo.zip")
		if _, err := os.Stat(zip); err == nil {
			return "goroot:" + zip
		}
	}
	return "embedded"
}

func blankOrPadded(s string) Reason {
	switch t := strings.TrimSpace(s); {
	case t == "":
		return Blank
	case t != s:
		return Padded
	}
	return OK
}

// shape reports whether s has the layout of tmpl, where 'd' is an ASCII digit and
// every other byte is itself.
func shape(s, tmpl string) bool {
	if len(s) != len(tmpl) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if tmpl[i] == 'd' {
			if !isDigit(s[i]) {
				return false
			}
		} else if s[i] != tmpl[i] {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// fields reads a shape-checked `YYYY-MM-DD` and `HH:MM:SS.mmm`.
func fields(date, clock string) (LocalTime, Reason) {
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	l := LocalTime{n(date[0:4]), n(date[5:7]), n(date[8:10]), n(clock[0:2]), n(clock[3:5]), n(clock[6:8]), n(clock[9:12])}
	if l.Year < 1 {
		return LocalTime{}, OutOfRange
	}
	if !l.Valid() {
		return LocalTime{}, NotACalendarDate
	}
	return l, OK
}
