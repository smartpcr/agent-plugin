package transport

import (
	"bytes"
	"strings"
	"testing"
)

var sseSeeds = []string{
	"",
	"data: hello\n\n",
	"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
	"\xEF\xBB\xBFdata: bom\r\n\r\n",
	"data: a\rdata: b\r\r",
	": comment\nid: 7\nretry: 100\ndata:x\ndata\n\n",
	"event: e\ndata: cut mid-ev",
	"data: \xff\xfe\n\n",
	"field-without-colon\n\nevent: empty\n\n",
}

// FuzzSSE: the reader never panics; every dispatched event is valid UTF-8 and
// within bounds; and an event the reader yields encodes to a stream that reads
// back as the same event.
func FuzzSSE(f *testing.F) {
	for _, s := range sseSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, stream string) {
		for ev, err := range Events(strings.NewReader(stream)) {
			if err != nil {
				return
			}
			if len(ev.Data) > MaxEvent || ev.Type == "" {
				t.Fatalf("event out of bounds: %q", ev.Type)
			}
			var buf bytes.Buffer
			if Encode(&buf, ev) != nil {
				continue
			}
			var back []Event
			for e, err := range Events(&buf) {
				if err != nil {
					t.Fatalf("re-read: %v", err)
				}
				back = append(back, e)
			}
			if len(back) != 1 || back[0] != ev {
				t.Fatalf("round trip of %+v gave %+v", ev, back)
			}
		}
	})
}
