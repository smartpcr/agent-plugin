package recur

import "testing"

// parseSeeds are the compiled-in rule seeds of FuzzParse (QG-6): a plain daily
// rule, a zoned monthly rule with BYSETPOS, a yearly rule by negative week number
// and a minutely rule bounded by UNTIL.
var parseSeeds = []string{
	"DTSTART:20260101T090000Z\nRRULE:FREQ=DAILY",
	"DTSTART;TZID=America/New_York:19970930T090000\nRRULE:FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1",
	"DTSTART:20200101T090000Z\nRRULE:FREQ=YEARLY;BYWEEKNO=-1;BYDAY=MO;COUNT=2",
	"DTSTART:20260101T090000Z\nRRULE:FREQ=MINUTELY;INTERVAL=7;UNTIL=20260102T000000Z",
}

// addParseSeeds registers each of parseSeeds, parsed in authoring mode, with f.
func addParseSeeds(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add(s, true)
	}
}
