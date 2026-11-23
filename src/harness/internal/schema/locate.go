package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

// Locate returns where raw, one JSON value, holds the value at path, a JSON
// Pointer as an Issue carries it ("" is the whole input): the 1-based line and
// column, counted in characters, of the value's first character. When a key
// repeats, the last one counts, as it does when the input is decoded. ok is false
// when raw is not one JSON value or nothing is at path (areas A21 and A22 point a
// reader at a configuration or manifest error with it).
func Locate(raw []byte, path string) (line, col int, ok bool) {
	steps, valid := pointerSteps(path)
	if !valid {
		return 0, 0, false
	}
	l := locator{raw: raw, dec: json.NewDecoder(bytes.NewReader(raw))}
	l.dec.UseNumber()
	at, found, err := l.value(steps)
	if err != nil || !found {
		return 0, 0, false
	}
	if _, err := l.dec.Token(); !errors.Is(err, io.EOF) {
		return 0, 0, false
	}
	line, col = 1, 1
	for _, r := range string(raw[:at]) {
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return line, col, true
}

// pointerSteps splits a JSON Pointer into its unescaped reference tokens.
func pointerSteps(path string) ([]string, bool) {
	if path == "" {
		return nil, true
	}
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	parts := strings.Split(path[1:], "/")
	unescape := strings.NewReplacer("~1", "/", "~0", "~")
	for i, p := range parts {
		parts[i] = unescape.Replace(p)
	}
	return parts, true
}

type locator struct {
	raw []byte
	dec *json.Decoder
}

// start is the offset of the next value: past white space and the separator
// the decoder has not consumed yet.
func (l *locator) start() int {
	at := int(l.dec.InputOffset())
	for at < len(l.raw) {
		switch l.raw[at] {
		case ' ', '\t', '\r', '\n', ':', ',':
			at++
			continue
		}
		break
	}
	return at
}

// value reads one value and returns the offset of the one at steps below it.
func (l *locator) value(steps []string) (int, bool, error) {
	begin := l.start()
	if len(steps) == 0 {
		var skip json.RawMessage
		return begin, true, l.dec.Decode(&skip)
	}
	tok, err := l.dec.Token()
	if err != nil {
		return 0, false, err
	}
	at, found := 0, false
	switch tok {
	case json.Delim('{'):
		for l.dec.More() {
			kt, err := l.dec.Token()
			if err != nil {
				return 0, false, err
			}
			key, _ := kt.(string)
			if key != steps[0] {
				if err := l.skip(); err != nil {
					return 0, false, err
				}
				continue
			}
			p, f, err := l.value(steps[1:])
			if err != nil {
				return 0, false, err
			}
			if f {
				at, found = p, true
			}
		}
	case json.Delim('['):
		want, err := strconv.Atoi(steps[0])
		if err != nil || strconv.Itoa(want) != steps[0] {
			want = -1 // not an index: nothing in an array is at it
		}
		for i := 0; l.dec.More(); i++ {
			if i != want {
				if err := l.skip(); err != nil {
					return 0, false, err
				}
				continue
			}
			if at, found, err = l.value(steps[1:]); err != nil {
				return 0, false, err
			}
		}
	default:
		return 0, false, nil // a scalar has nothing below it
	}
	if _, err := l.dec.Token(); err != nil { // the closing delimiter
		return 0, false, err
	}
	return at, found, nil
}

func (l *locator) skip() error {
	var skip json.RawMessage
	return l.dec.Decode(&skip)
}
