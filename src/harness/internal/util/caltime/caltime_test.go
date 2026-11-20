package caltime

import (
	"testing"
	"time"
)

// TestStoredAndInput is DH-T-TEMPORAL-1.
func TestStoredAndInput(t *testing.T) {
	for _, s := range []string{"0001-01-01T00:00:00.000Z", "9999-12-31T23:59:59.999Z", "2099-01-01T01:00:00.000Z", "2024-02-29T12:00:00.500Z"} {
		v, r := ParseStored(s)
		if r != OK {
			t.Fatalf("ParseStored(%q) = %q", s, r)
		}
		back, ok := FormatStored(v)
		if !ok || back != s {
			t.Fatalf("FormatStored(ParseStored(%q)) = %q, %v", s, back, ok)
		}
	}
	for _, tc := range []struct {
		in   string
		want Reason
	}{
		{"", Blank},
		{" 2099-01-01T01:00:00.000Z", Padded},
		{"2099-01-01T01:00:00.00Z", NotRFC3339},
		{"2099-01-01T01:00:00.0000Z", NotRFC3339},
		{"2099-01-01T01:00:00.000", NotRFC3339},
		{"2099-01-01T01:00:00.000+00:00", NotRFC3339},
		{"2099-01-01t01:00:00.000z", NotRFC3339},
		{"0000-01-01T00:00:00.000Z", OutOfRange},
		{"2021-02-30T00:00:00.000Z", NotACalendarDate},
		{"2021-01-01T24:00:00.000Z", NotACalendarDate},
		{"2021-01-01T23:59:60.000Z", NotACalendarDate},
	} {
		if _, r := ParseStored(tc.in); r != tc.want {
			t.Errorf("ParseStored(%q) = %q, want %q", tc.in, r, tc.want)
		}
	}
	if _, ok := FormatStored(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatal("year 10000 was formatted")
	}

	want := time.Date(2099, 1, 1, 1, 0, 0, 0, time.UTC)
	for _, s := range []string{"2099-01-01T01:00:00Z", "2099-01-01T09:00:00+08:00", "2098-12-31T20:00:00-05:00", "2099-01-01T01:00:00.000000000Z"} {
		got, r := ParseOffsetInstant(s)
		if r != OK || !got.Equal(want) {
			t.Errorf("ParseOffsetInstant(%q) = %v, %q", s, got, r)
		}
	}
	if got, r := ParseOffsetInstant("2099-01-01T01:00:00.25Z"); r != OK || got.Nanosecond() != 250_000_000 {
		t.Errorf("fraction: %v, %q", got, r)
	}
	for _, tc := range []struct {
		in   string
		want Reason
	}{
		{"2099-01-01T01:00:00", NotRFC3339},
		{"2099-01-01 01:00:00Z", NotRFC3339},
		{"2099-01-01T01:00:00-00:00", InvalidNumericOffset},
		{"2099-01-01T01:00:00+24:00", InvalidNumericOffset},
		{"2099-01-01T01:00:00+0800", InvalidNumericOffset},
		{"2016-12-31T23:59:60Z", NotACalendarDate},
		{"2021-02-30T00:00:00Z", NotACalendarDate},
		{"2099-01-01T01:00:00.Z", NotRFC3339},
		{"2099-01-01T01:00:00.1234567890Z", NotRFC3339},
		{"0001-01-01T00:00:00+01:00", OutOfRange},
		{"\t2099-01-01T01:00:00Z", Padded},
	} {
		if _, r := ParseOffsetInstant(tc.in); r != tc.want {
			t.Errorf("ParseOffsetInstant(%q) = %q, want %q", tc.in, r, tc.want)
		}
	}
	if l, r := ParseLocal("2021-03-14", "02:30"); r != OK || l != (LocalTime{2021, 3, 14, 2, 30, 0, 0}) {
		t.Fatalf("ParseLocal = %v, %q", l, r)
	}
	for _, c := range [][2]string{{"2021-3-14", "02:30"}, {"2021-03-14", "2:30"}, {"2021-02-29", "00:00"}, {"2021-03-14", ""}} {
		if _, r := ParseLocal(c[0], c[1]); r == OK {
			t.Errorf("ParseLocal(%q, %q) accepted", c[0], c[1])
		}
	}
}

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, r := LoadZone(name)
	if r != OK {
		t.Fatalf("LoadZone(%q) = %q", name, r)
	}
	return loc
}

// TestResolve is DH-T-TEMPORAL-2.
func TestResolve(t *testing.T) {
	ny := zone(t, "America/New_York")
	if _, ok := Resolve(LocalTime{2021, 3, 14, 2, 30, 0, 0}, ny); ok {
		t.Fatal("2021-03-14 02:30 exists in New York")
	}
	got, ok := Resolve(LocalTime{2021, 11, 7, 1, 30, 0, 0}, ny)
	if _, off := got.Zone(); !ok || off != -4*3600 || got.UTC() != time.Date(2021, 11, 7, 5, 30, 0, 0, time.UTC) {
		t.Fatalf("overlap resolved to %v (%v)", got, ok)
	}
	if got, ok := Resolve(LocalTime{2021, 7, 1, 12, 0, 0, 0}, ny); !ok || got.UTC().Hour() != 16 {
		t.Fatalf("ordinary time resolved to %v", got)
	}

	lh := zone(t, "Australia/Lord_Howe") // +10:30 standard, +11:00 summer
	if _, ok := Resolve(LocalTime{2021, 10, 3, 2, 15, 0, 0}, lh); ok {
		t.Fatal("Lord Howe 02:15 on the spring-forward date exists")
	}
	got, ok = Resolve(LocalTime{2021, 4, 4, 1, 45, 0, 0}, lh)
	if _, off := got.Zone(); !ok || off != 11*3600 {
		t.Fatalf("Lord Howe overlap resolved to %v (%v)", got, ok)
	}

	apia := zone(t, "Pacific/Apia")
	for h := 0; h < 24; h++ {
		if _, ok := Resolve(LocalTime{2011, 12, 30, h, 0, 0, 0}, apia); ok {
			t.Fatalf("Apia 2011-12-30 %02d:00 exists", h)
		}
	}
	day := LocalTime{2011, 12, 29, 12, 0, 0, 0}
	if next := day.AddDays(1); next != (LocalTime{2011, 12, 30, 12, 0, 0, 0}) {
		t.Fatalf("AddDays = %v", next)
	}
	a, _ := Resolve(day, apia)
	b, ok := Resolve(day.AddDays(2), apia)
	if !ok || b.Sub(a) != 24*time.Hour {
		t.Fatalf("29 Dec 12:00 to 31 Dec 12:00 in Apia is %v (%v), want 24h", b.Sub(a), ok)
	}
	if w := (LocalTime{2024, 1, 1, 0, 0, 0, 0}).ISOWeekday(); w != 1 {
		t.Fatalf("2024-01-01 ISO weekday = %d", w)
	}
	if w := (LocalTime{2024, 1, 7, 0, 0, 0, 0}).ISOWeekday(); w != 7 {
		t.Fatalf("2024-01-07 ISO weekday = %d", w)
	}
	if _, ok := Resolve(LocalTime{2021, 2, 29, 0, 0, 0, 0}, time.UTC); ok {
		t.Fatal("an invalid local time resolved")
	}
}

// TestZones is DH-T-TEMPORAL-3.
func TestZones(t *testing.T) {
	for _, name := range []string{"UTC", "Asia/Shanghai", "US/Eastern", "America/Argentina/Buenos_Aires", "Etc/GMT+5"} {
		loc := zone(t, name)
		if loc.String() != name {
			t.Errorf("LoadZone(%q).String() = %q; the spelling must be kept", name, loc.String())
		}
	}
	for _, tc := range []struct {
		in   string
		want Reason
	}{
		{"", Blank},
		{" Asia/Tokyo", Padded},
		{"Local", Unsupported},
		{"EST", NotAreaLocation},
		{"+08:00", InvalidNumericOffset},
		{"Mars/Olympus", UnknownZone},
		{"Asia//Tokyo", NotAreaLocation},
		{"../etc/passwd", NotAreaLocation},
		{"Asia/../Tokyo", NotAreaLocation},
	} {
		if _, r := LoadZone(tc.in); r != tc.want {
			t.Errorf("LoadZone(%q) = %q, want %q", tc.in, r, tc.want)
		}
	}
}

// TestElapsedAndDisplay is DH-T-TEMPORAL-4.
func TestElapsedAndDisplay(t *testing.T) {
	ny := zone(t, "America/New_York")
	before := time.Date(2021, 3, 14, 6, 0, 0, 0, time.UTC) // 01:00 EST
	after, ok := AddMillis(before, 2*3600*1000)
	if !ok || after.Sub(before) != 2*time.Hour {
		t.Fatalf("AddMillis across the transition = %v", after.Sub(before))
	}
	if got := FormatLocal(after, ny); got != "2021-03-14T04:00:00-04:00" {
		t.Fatalf("FormatLocal = %q", got)
	}
	inst := time.Date(2099, 1, 1, 1, 0, 0, 0, time.UTC)
	if got := FormatLocal(inst, zone(t, "Asia/Shanghai")); got != "2099-01-01T09:00:00+08:00" {
		t.Fatalf("FormatLocal = %q", got)
	}
	if got := FormatLocal(inst, nil); got != "2099-01-01T01:00:00+00:00" {
		t.Fatalf("FormatLocal(nil) = %q", got)
	}
	late, _ := ParseStored("9999-12-31T23:00:00.000Z")
	if _, ok := AddMillis(late, 2*3600*1000); ok {
		t.Fatal("an interval past year 9999 did not report no further occurrence")
	}
	far := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got, ok := AddMillis(far, 500*365*24*3600*1000); !ok || got.Year() != 2525 {
		t.Fatalf("a 500-year interval = %v, %v", got, ok)
	}
	now := time.Now()
	if s := Stored(now); s.Nanosecond()%int(time.Millisecond) != 0 || s != Stored(s) {
		t.Fatal("Stored is not idempotent at millisecond precision")
	}
}

// TestZoneDataSource covers the diagnostic half of DH-T-TEMPORAL-5; the build half
// (cmd/dsh imports time/tzdata) is a rule in internal/archtest.
func TestZoneDataSource(t *testing.T) {
	t.Setenv("ZONEINFO", "")
	if src := ZoneDataSource(); src == "" {
		t.Fatal("no zone-data source named")
	}
	t.Setenv("ZONEINFO", "custom.zip")
	if src := ZoneDataSource(); src != "ZONEINFO=custom.zip" {
		t.Fatalf("ZoneDataSource = %q", src)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"2099-01-01T01:00:00.000Z", "2099-01-01T09:00:00.5+08:00", "2016-12-31T23:59:60Z", "Asia/Tokyo", "-00:00", "2021-03-14"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if v, r := ParseStored(s); r == OK {
			if back, ok := FormatStored(v); !ok || back != s {
				t.Fatalf("stored %q does not round-trip: %q", s, back)
			}
		}
		if v, r := ParseOffsetInstant(s); r == OK && (v.Year() < 1 || v.Year() > 9999) {
			t.Fatalf("ParseOffsetInstant(%q) = %v outside the year range", s, v)
		}
		if len(s) > 10 {
			if l, r := ParseLocal(s[:10], s[10:]); r == OK && !l.Valid() {
				t.Fatalf("ParseLocal accepted an invalid %v", l)
			}
		}
		if loc, r := LoadZone(s); r == OK && loc.String() != s {
			t.Fatalf("LoadZone(%q) renamed the zone to %q", s, loc.String())
		}
	})
}
