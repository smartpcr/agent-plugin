package transport

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"iter"
	"strings"
	"unicode/utf8"
)

// Event is one dispatched server-sent event.
type Event struct {
	// Type is the event field, "message" when the event named none.
	Type string
	// Data is the data lines joined with "\n", valid UTF-8.
	Data string
	// ID is the last event id the stream set, "" when none.
	ID string
}

// Bounds of the reader: a longer line or event fails the stream.
const (
	MaxLine  = 1 << 20
	MaxEvent = 8 << 20
)

// ErrTooLarge is the error of a line or event over its bound.
var ErrTooLarge = errors.New("transport: an event-stream line or event exceeds its bound")

// Events reads r as text/event-stream (HTML Living Standard, "Server-sent
// events", interpreting an event stream): lines end in CR, LF or CRLF; a leading
// byte-order mark is dropped; a line starting with ':' is a comment; a blank line
// dispatches the pending event when it has data; the fields event, data and id are
// read and every other field is ignored. Bytes that are not UTF-8 become U+FFFD.
// The pending event at the end of r is discarded, as the standard requires, so a
// stream cut mid-event yields only the events completed before the cut. A read
// error other than io.EOF, or a line or event over its bound, ends the sequence
// with that error.
func Events(r io.Reader) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		br := bufio.NewReaderSize(r, 32<<10)
		var (
			typ, id string
			data    strings.Builder
			hasData bool
			first   = true
			line    []byte
			afterCR bool
		)
		for {
			var err error
			line, err = readLine(br, line[:0], &afterCR)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					yield(Event{}, err)
				}
				return
			}
			if first {
				first = false
				line = trimBOM(line)
			}
			if len(line) == 0 {
				if hasData {
					d := strings.TrimSuffix(data.String(), "\n")
					t := typ
					if t == "" {
						t = "message"
					}
					if !yield(Event{Type: t, Data: d, ID: id}, nil) {
						return
					}
				}
				typ, hasData = "", false
				data.Reset()
				continue
			}
			if line[0] == ':' {
				continue
			}
			s := toValid(line)
			field, value, found := strings.Cut(s, ":")
			if found {
				value = strings.TrimPrefix(value, " ")
			}
			switch field {
			case "event":
				typ = value
			case "data":
				if data.Len()+len(value)+1 > MaxEvent {
					yield(Event{}, ErrTooLarge)
					return
				}
				data.WriteString(value)
				data.WriteByte('\n')
				hasData = true
			case "id":
				if !strings.ContainsRune(value, 0) {
					id = value
				}
			}
		}
	}
}

// readLine appends the next line of br to buf, without its terminator. A final
// line with no terminator is reported as io.EOF: the standard discards it. A CR
// ends its line at once, without waiting for the next byte; *afterCR records it
// so the LF of a CRLF pair is dropped at the start of the next call.
func readLine(br *bufio.Reader, buf []byte, afterCR *bool) ([]byte, error) {
	for {
		b, err := br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf, io.EOF
			}
			return buf, err
		}
		if *afterCR {
			*afterCR = false
			if b == '\n' {
				continue
			}
		}
		switch b {
		case '\n':
			return buf, nil
		case '\r':
			*afterCR = true
			return buf, nil
		}
		if len(buf) >= MaxLine {
			return buf, ErrTooLarge
		}
		buf = append(buf, b)
	}
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

func toValid(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// Encode writes e in the form Events reads back as e: the event field, one data
// line per line of Data and the id field when set. A type or id holding a line
// break, or data holding CR, has no such form and is refused.
func Encode(w io.Writer, e Event) error {
	if strings.ContainsAny(e.Type, "\r\n") || strings.ContainsAny(e.ID, "\r\n\x00") || strings.ContainsRune(e.Data, '\r') {
		return fmt.Errorf("transport: event %q cannot be encoded", e.Type)
	}
	var b strings.Builder
	if e.Type != "" {
		b.WriteString("event: " + e.Type + "\n")
	}
	if e.ID != "" {
		b.WriteString("id: " + e.ID + "\n")
	}
	for _, l := range strings.Split(e.Data, "\n") {
		b.WriteString("data: " + l + "\n")
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}
