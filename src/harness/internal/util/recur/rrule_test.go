package recur

import (
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, text string) *rrule {
	t.Helper()
	r, rej := parse(text, false)
	if rej != nil {
		t.Fatalf("Parse(%q): %v", text, rej)
	}
	return r
}

func ny(t *testing.T, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	v, err := time.ParseInLocation("2006-01-02T15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// take lists up to n occurrences by repeated Next, checking each is strictly
// after its argument.
func take(t *testing.T, r Rule, n int) []time.Time {
	t.Helper()
	var out []time.Time
	cur := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	for range n {
		next, ok := r.Next(cur)
		if !ok {
			break
		}
		if !next.After(cur) {
			t.Fatalf("Next(%v) = %v is not strictly after", cur, next)
		}
		out = append(out, next)
		cur = next
	}
	return out
}

// TestParse is DH-T-RRULE-1.
func TestParse(t *testing.T) {
	accepted := []string{
		"DTSTART:20260101T090000Z\nRRULE:FREQ=MINUTELY;INTERVAL=15",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=HOURLY;COUNT=3",
		"DTSTART;TZID=Asia/Shanghai:20260101T090000\nRRULE:FREQ=DAILY;BYHOUR=9,17;BYMINUTE=0,30;BYSECOND=0",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=WEEKLY;WKST=SU;BYDAY=MO,FR;UNTIL=20270101T000000Z",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=MONTHLY;BYMONTHDAY=1,-1;BYMONTH=1,6",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=MONTHLY;BYDAY=2TU,-1FR",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=YEARLY;BYYEARDAY=1,-1",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=YEARLY;BYWEEKNO=1,53;BYDAY=MO",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=1,-1",
		"  DTSTART:20260101T090000Z\r\n  RRULE:FREQ=DAILY  ",
		// exactly the density limit: every minute of a week
		"DTSTART:20260101T090000Z\nRRULE:FREQ=WEEKLY;BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23;BYSECOND=0;" +
			"BYMINUTE=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32,33,34,35,36,37,38,39,40,41,42,43,44,45,46,47,48,49,50,51,52,53,54,55,56,57,58,59",
	}
	for _, text := range accepted {
		r, rej := Parse(text, true)
		if rej != nil {
			t.Errorf("Parse(%q) rejected: %v", text, rej)
			continue
		}
		if strings.ContainsAny(r.Expression(), "\r ") {
			t.Errorf("Expression %q is not the trimmed text", r.Expression())
		}
	}
	if r, _ := Parse("DTSTART;TZID=US/Eastern:20260101T090000\nRRULE:FREQ=DAILY", false); r == nil || r.Zone() != "US/Eastern" {
		t.Error("the zone spelling is not kept")
	}

	const s = "DTSTART:20260101T090000Z\nRRULE:"
	rejected := []struct {
		text string
		want Reason
	}{
		{"RRULE:FREQ=DAILY", Syntax},
		{s + "FREQ=DAILY\nEXDATE:20260102T090000Z", Syntax},
		{"DTSTART:20260101T090000Z\nRRULE:FREQ=DAILY\nRRULE:FREQ=WEEKLY", Syntax},
		{"DTSTART:20260101\nRRULE:FREQ=DAILY", Syntax},
		{"DTSTART:20260230T090000Z\nRRULE:FREQ=DAILY", Syntax},
		{"DTSTART:20260101T090000\nRRULE:FREQ=DAILY", TimeZone},
		{"DTSTART:20260101T090000+0800\nRRULE:FREQ=DAILY", TimeZone},
		{"DTSTART;TZID=Mars/Olympus:20260101T090000\nRRULE:FREQ=DAILY", TimeZone},
		{"DTSTART;TZID=EST:20260101T090000\nRRULE:FREQ=DAILY", TimeZone},
		{"DTSTART;TZID=UTC:20260101T090000Z\nRRULE:FREQ=DAILY", TimeZone},
		{"DTSTART;TZID=America/New_York:20210314T023000\nRRULE:FREQ=DAILY", TimeZone},
		{s + "FREQ=SECONDLY", Syntax},
		{s + "INTERVAL=2", Syntax},
		{s + "FREQ=DAILY;FREQ=DAILY", Syntax},
		{s + "FREQ=DAILY;BYEASTER=0", Syntax},
		{s + "FREQ=DAILY;BYHOUR=24", Syntax},
		{s + "FREQ=DAILY;BYHOUR=9,9", Syntax},
		{s + "FREQ=DAILY;INTERVAL=0", Syntax},
		{s + "FREQ=HOURLY;BYMINUTE=0", Syntax},
		{s + "FREQ=WEEKLY;BYMONTHDAY=1", Syntax},
		{s + "FREQ=WEEKLY;BYDAY=2TU", Syntax},
		{s + "FREQ=MONTHLY;BYWEEKNO=1", Syntax},
		{s + "FREQ=YEARLY;BYWEEKNO=1;BYDAY=1MO", Syntax},
		{s + "FREQ=DAILY;BYSETPOS=1", Syntax},
		{s + "FREQ=DAILY;COUNT=0", Syntax},
		{s + "FREQ=DAILY;COUNT=1001", Syntax},
		{s + "FREQ=DAILY;COUNT=2;UNTIL=20270101T000000Z", Syntax},
		{s + "FREQ=DAILY;UNTIL=20270101T000000", Syntax},
		{s + "FREQ=DAILY;BYSECOND=0,30", Density},
		{s + "FREQ=MONTHLY;BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23;" +
			"BYMINUTE=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32,33,34,35,36,37,38,39,40,41,42,43,44,45,46,47,48,49,50,51,52,53,54,55,56,57,58,59", Density},
		{s + "FREQ=MONTHLY;BYDAY=MO;BYSETPOS=" + strings.Repeat("1,", 31) + "1", Syntax},
		{s + "FREQ=MONTHLY;BYDAY=MO,TU;BYSETPOS=1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32", Density},
		{s + "FREQ=MONTHLY;BYDAY=MO;BYSETPOS=6", Density},
	}
	for _, tc := range rejected {
		_, rej := Parse(tc.text, false)
		if rej == nil || rej.Reason != tc.want {
			t.Errorf("Parse(%q) = %v, want %s", tc.text, rej, tc.want)
		} else if rej.Error() == "" || rej.Detail == "" {
			t.Errorf("Parse(%q) rejection names no rule", tc.text)
		}
	}

	// DH-LIB-RRULE-R9: a BYSETPOS that can have no effect is refused when
	// written and still loads when stored.
	oneCandidate := s + "FREQ=MONTHLY;BYMONTHDAY=15;BYSETPOS=1"
	if _, rej := Parse(oneCandidate, true); rej == nil || !strings.Contains(rej.Detail, "BYSETPOS=-1") {
		t.Errorf("authoring %q: %v, want a rejection with an example", oneCandidate, rej)
	}
	if _, rej := Parse(oneCandidate, false); rej != nil {
		t.Errorf("loading %q: %v", oneCandidate, rej)
	}
}

// TestOccurrences is DH-T-RRULE-2: RFC 5545 section 3.8.5.3 examples inside the
// subset, then the extra vectors.
func TestOccurrences(t *testing.T) {
	const nyStart = "DTSTART;TZID=America/New_York:"
	cases := []struct {
		name, text string
		n          int
		want       []string // New York wall time, or UTC when the rule is UTC
	}{
		{"daily count 10", nyStart + "19970902T090000\nRRULE:FREQ=DAILY;COUNT=10", 20,
			[]string{"1997-09-02T09:00", "1997-09-03T09:00", "1997-09-04T09:00", "1997-09-05T09:00", "1997-09-06T09:00", "1997-09-07T09:00", "1997-09-08T09:00", "1997-09-09T09:00", "1997-09-10T09:00", "1997-09-11T09:00"}},
		{"every 10 days", nyStart + "19970902T090000\nRRULE:FREQ=DAILY;INTERVAL=10;COUNT=5", 9,
			[]string{"1997-09-02T09:00", "1997-09-12T09:00", "1997-09-22T09:00", "1997-10-02T09:00", "1997-10-12T09:00"}},
		{"weekly count 10", nyStart + "19970902T090000\nRRULE:FREQ=WEEKLY;COUNT=10", 20,
			[]string{"1997-09-02T09:00", "1997-09-09T09:00", "1997-09-16T09:00", "1997-09-23T09:00", "1997-09-30T09:00", "1997-10-07T09:00", "1997-10-14T09:00", "1997-10-21T09:00", "1997-10-28T09:00", "1997-11-04T09:00"}},
		{"every other week tu th", nyStart + "19970902T090000\nRRULE:FREQ=WEEKLY;INTERVAL=2;COUNT=8;WKST=SU;BYDAY=TU,TH", 20,
			[]string{"1997-09-02T09:00", "1997-09-04T09:00", "1997-09-16T09:00", "1997-09-18T09:00", "1997-09-30T09:00", "1997-10-02T09:00", "1997-10-14T09:00", "1997-10-16T09:00"}},
		{"first friday", nyStart + "19970905T090000\nRRULE:FREQ=MONTHLY;COUNT=10;BYDAY=1FR", 20,
			[]string{"1997-09-05T09:00", "1997-10-03T09:00", "1997-11-07T09:00", "1997-12-05T09:00", "1998-01-02T09:00", "1998-02-06T09:00", "1998-03-06T09:00", "1998-04-03T09:00", "1998-05-01T09:00", "1998-06-05T09:00"}},
		{"second to last monday", nyStart + "19970922T090000\nRRULE:FREQ=MONTHLY;COUNT=6;BYDAY=-2MO", 20,
			[]string{"1997-09-22T09:00", "1997-10-20T09:00", "1997-11-17T09:00", "1997-12-22T09:00", "1998-01-19T09:00", "1998-02-16T09:00"}},
		{"third to last day", nyStart + "19970928T090000\nRRULE:FREQ=MONTHLY;BYMONTHDAY=-3", 6,
			[]string{"1997-09-28T09:00", "1997-10-29T09:00", "1997-11-28T09:00", "1997-12-29T09:00", "1998-01-29T09:00", "1998-02-26T09:00"}},
		{"first and last day", nyStart + "19970930T090000\nRRULE:FREQ=MONTHLY;COUNT=10;BYMONTHDAY=1,-1", 20,
			[]string{"1997-09-30T09:00", "1997-10-01T09:00", "1997-10-31T09:00", "1997-11-01T09:00", "1997-11-30T09:00", "1997-12-01T09:00", "1997-12-31T09:00", "1998-01-01T09:00", "1998-01-31T09:00", "1998-02-01T09:00"}},
		{"friday 13th", nyStart + "19970902T090000\nRRULE:FREQ=MONTHLY;BYDAY=FR;BYMONTHDAY=13", 5,
			[]string{"1998-02-13T09:00", "1998-03-13T09:00", "1998-11-13T09:00", "1999-08-13T09:00", "2000-10-13T09:00"}},
		{"june and july", nyStart + "19970610T090000\nRRULE:FREQ=YEARLY;COUNT=10;BYMONTH=6,7", 20,
			[]string{"1997-06-10T09:00", "1997-07-10T09:00", "1998-06-10T09:00", "1998-07-10T09:00", "1999-06-10T09:00", "1999-07-10T09:00", "2000-06-10T09:00", "2000-07-10T09:00", "2001-06-10T09:00", "2001-07-10T09:00"}},
		{"year days every third year", nyStart + "19970101T090000\nRRULE:FREQ=YEARLY;INTERVAL=3;COUNT=10;BYYEARDAY=1,100,200", 20,
			[]string{"1997-01-01T09:00", "1997-04-10T09:00", "1997-07-19T09:00", "2000-01-01T09:00", "2000-04-09T09:00", "2000-07-18T09:00", "2003-01-01T09:00", "2003-04-10T09:00", "2003-07-19T09:00", "2006-01-01T09:00"}},
		{"monday of week 20", nyStart + "19970512T090000\nRRULE:FREQ=YEARLY;BYWEEKNO=20;BYDAY=MO", 3,
			[]string{"1997-05-12T09:00", "1998-05-11T09:00", "1999-05-17T09:00"}},
		{"thursdays in march", nyStart + "19970313T090000\nRRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=TH", 7,
			[]string{"1997-03-13T09:00", "1997-03-20T09:00", "1997-03-27T09:00", "1998-03-05T09:00", "1998-03-12T09:00", "1998-03-19T09:00", "1998-03-26T09:00"}},
		{"third tu we th", nyStart + "19970904T090000\nRRULE:FREQ=MONTHLY;COUNT=3;BYDAY=TU,WE,TH;BYSETPOS=3", 9,
			[]string{"1997-09-04T09:00", "1997-10-07T09:00", "1997-11-06T09:00"}},
		{"last weekday of month", nyStart + "19970930T090000\nRRULE:FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", 5,
			[]string{"1997-09-30T09:00", "1997-10-31T09:00", "1997-11-28T09:00", "1997-12-31T09:00", "1998-01-30T09:00"}},
		{"wkst monday", nyStart + "19970805T090000\nRRULE:FREQ=WEEKLY;INTERVAL=2;COUNT=4;BYDAY=TU,SU;WKST=MO", 9,
			[]string{"1997-08-05T09:00", "1997-08-10T09:00", "1997-08-19T09:00", "1997-08-24T09:00"}},
		{"wkst sunday", nyStart + "19970805T090000\nRRULE:FREQ=WEEKLY;INTERVAL=2;COUNT=4;BYDAY=TU,SU;WKST=SU", 9,
			[]string{"1997-08-05T09:00", "1997-08-17T09:00", "1997-08-19T09:00", "1997-08-31T09:00"}},
		{"every three hours until", "DTSTART:19970902T090000Z\nRRULE:FREQ=HOURLY;INTERVAL=3;UNTIL=19970902T170000Z", 9,
			[]string{"1997-09-02T09:00", "1997-09-02T12:00", "1997-09-02T15:00"}},
		{"every 15 minutes", "DTSTART:19970902T090000Z\nRRULE:FREQ=MINUTELY;INTERVAL=15;COUNT=6", 9,
			[]string{"1997-09-02T09:00", "1997-09-02T09:15", "1997-09-02T09:30", "1997-09-02T09:45", "1997-09-02T10:00", "1997-09-02T10:15"}},
		{"week 53", "DTSTART:20200101T090000Z\nRRULE:FREQ=YEARLY;BYWEEKNO=53;BYDAY=MO", 3,
			[]string{"2020-12-28T09:00", "2026-12-28T09:00", "2032-12-27T09:00"}},
		{"day -1 of the year", "DTSTART:20230101T090000Z\nRRULE:FREQ=YEARLY;BYYEARDAY=-1", 2,
			[]string{"2023-12-31T09:00", "2024-12-31T09:00"}},
		{"29 february", "DTSTART:20240229T090000Z\nRRULE:FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", 3,
			[]string{"2024-02-29T09:00", "2028-02-29T09:00", "2032-02-29T09:00"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustParse(t, tc.text)
			var got []string
			for _, v := range take(t, r, tc.n) {
				got = append(got, v.In(r.loc).Format("2006-01-02T15:04"))
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestDaylightSaving is DH-T-RRULE-3.
func TestDaylightSaving(t *testing.T) {
	r := mustParse(t, "DTSTART;TZID=America/New_York:20210313T023000\nRRULE:FREQ=DAILY;COUNT=3")
	got := take(t, r, 10)
	want := []time.Time{ny(t, "2021-03-13T02:30"), ny(t, "2021-03-15T02:30"), ny(t, "2021-03-16T02:30")}
	if len(got) != 3 || !got[0].Equal(want[0]) || !got[1].Equal(want[1]) || !got[2].Equal(want[2]) {
		t.Fatalf("spring forward: %v", got)
	}

	r = mustParse(t, "DTSTART;TZID=America/New_York:20211106T013000\nRRULE:FREQ=DAILY;COUNT=3")
	got = take(t, r, 10)
	if len(got) != 3 || !got[1].Equal(time.Date(2021, 11, 7, 5, 30, 0, 0, time.UTC)) {
		t.Fatalf("fall back: %v", got)
	}
	if got[2].Sub(got[1]) != 25*time.Hour {
		t.Fatalf("01:30 on 7 Nov fired twice or late: %v", got)
	}
}

// TestResolve is DH-T-RRULE-4.
func TestResolve(t *testing.T) {
	daily := mustParse(t, "DTSTART:20260101T090000Z\nRRULE:FREQ=DAILY")
	committed := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	decision := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	occ, err := daily.Resolve(committed, decision)
	if err != nil || !occ.At.Equal(time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)) || !occ.Next.Equal(time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("long gap: %+v, %v", occ, err)
	}
	if _, err := daily.Resolve(decision, committed); err != ErrNotDue {
		t.Fatalf("decision before commitment: %v", err)
	}

	five := mustParse(t, "DTSTART:20260101T090000Z\nRRULE:FREQ=DAILY;COUNT=5")
	occ, _ = five.Resolve(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), decision)
	if !occ.At.Equal(time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)) || !occ.Next.IsZero() {
		t.Fatalf("COUNT=5: %+v", occ)
	}

	until := mustParse(t, "DTSTART:20260101T090000Z\nRRULE:FREQ=DAILY;UNTIL=20260103T090000Z")
	if got := take(t, until, 10); len(got) != 3 {
		t.Fatalf("UNTIL is not inclusive: %v", got)
	}
	occ, _ = until.Resolve(time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC), decision)
	if !occ.At.Equal(time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC)) || !occ.Next.IsZero() {
		t.Fatalf("UNTIL resolve: %+v", occ)
	}

	// A committed instant later than the recomputed occurrence is kept (R7).
	late := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	occ, _ = daily.Resolve(late, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC))
	if !occ.At.Equal(late) || !occ.Next.Equal(time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("commitment not honoured: %+v", occ)
	}

	minutely := mustParse(t, "DTSTART:20260101T090000Z\nRRULE:FREQ=MINUTELY;INTERVAL=10")
	occ, _ = minutely.Resolve(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 25, 0, 0, time.UTC))
	if occ.At.Minute() != 20 || occ.Next.Minute() != 30 {
		t.Fatalf("minutely resolve: %+v", occ)
	}
}

// TestBoundedWork is DH-T-RRULE-5.
func TestBoundedWork(t *testing.T) {
	for _, text := range []string{
		"DTSTART:20260101T090000Z\nRRULE:FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=MONTHLY;BYMONTH=2;BYMONTHDAY=30",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=YEARLY;BYMONTH=4;BYMONTHDAY=31;COUNT=3",
	} {
		r := mustParse(t, text)
		if next, ok := r.Next(r.start); ok {
			t.Errorf("%q gave %v", text, next)
		}
	}

	r := mustParse(t, "DTSTART:18000101T090000Z\nRRULE:FREQ=DAILY")
	ops := 0
	r.ops = &ops
	occ, err := r.Resolve(time.Date(1800, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	if err != nil || !occ.At.Equal(time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)) || !occ.Next.Equal(time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("resolve from 1800: %+v, %v", occ, err)
	}
	if ops > 16 {
		t.Fatalf("resolving 226 years of downtime listed %d periods", ops)
	}

	end := mustParse(t, "DTSTART:99991230T090000Z\nRRULE:FREQ=DAILY")
	if got := take(t, end, 5); len(got) != 2 {
		t.Fatalf("a rule past year 9999 is not exhausted: %v", got)
	}
}

// TestElapsedSpacing is DH-T-RRULE-6 (DH-ADR-RRULE-2).
func TestElapsedSpacing(t *testing.T) {
	r := mustParse(t, "DTSTART;TZID=America/New_York:20210101T000000\nRRULE:FREQ=MINUTELY;INTERVAL=5")
	for _, around := range []time.Time{ny(t, "2021-03-14T01:00"), ny(t, "2021-11-07T00:30")} {
		prev, _ := r.Next(around)
		for range 36 { // three hours across the transition
			next, ok := r.Next(prev)
			if !ok || next.Sub(prev) != 300*time.Second {
				t.Fatalf("after %v came %v", prev, next)
			}
			prev = next
		}
	}
	hourly := mustParse(t, "DTSTART;TZID=America/New_York:20211107T000000\nRRULE:FREQ=HOURLY;COUNT=4")
	got := take(t, hourly, 10)
	if len(got) != 4 || got[3].Sub(got[0]) != 3*time.Hour {
		t.Fatalf("hourly across fall back: %v", got)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"DTSTART:20260101T090000Z\nRRULE:FREQ=DAILY",
		"DTSTART;TZID=America/New_York:19970930T090000\nRRULE:FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1",
		"DTSTART:20200101T090000Z\nRRULE:FREQ=YEARLY;BYWEEKNO=-1;BYDAY=MO;COUNT=2",
		"DTSTART:20260101T090000Z\nRRULE:FREQ=MINUTELY;INTERVAL=7;UNTIL=20260102T000000Z",
	} {
		f.Add(s, true)
	}
	f.Fuzz(func(t *testing.T, text string, authoring bool) {
		r, rej := parse(text, authoring)
		if rej != nil {
			if rej.Reason != Syntax && rej.Reason != TimeZone && rej.Reason != Density {
				t.Fatalf("unknown reason %q", rej.Reason)
			}
			return
		}
		if again, rej := parse(r.Expression(), false); rej != nil || again.Expression() != r.Expression() {
			t.Fatalf("Expression %q does not reload: %v", r.Expression(), rej)
		}
		ops := 0
		r.ops = &ops
		cur := r.start.Add(-time.Second)
		for range 3 {
			next, ok := r.Next(cur)
			if !ok {
				break
			}
			if !next.After(cur) {
				t.Fatalf("Next(%v) = %v", cur, next)
			}
			cur = next
		}
		if ops > 3*(r.emptyLimit()+2)+MaxCount {
			t.Fatalf("%d periods listed", ops)
		}
	})
}
