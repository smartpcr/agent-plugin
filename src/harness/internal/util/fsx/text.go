package fsx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// SniffLen is how many leading bytes a binary sniff (search's skip of binary files)
// inspects; IsText itself judges the whole content.
const SniffLen = 8000

// IsText is the one verdict on whether content is text, for every reader and editor
// alike (DH-LIB-NODEFS-R8, DH-T-FS-2): content behind a UTF-16 byte-order mark is
// text when it decodes (an even length, no unpaired surrogate); other content is
// text when it has no NUL byte and is valid UTF-8, judged over all of it, so a NUL at
// byte 9,000 makes a file binary on every path.
func IsText(b []byte) bool {
	if enc, n := sniffBOM(b); enc == UTF16LE || enc == UTF16BE {
		return validUTF16(b[n:], enc)
	}
	return bytes.IndexByte(b, 0) < 0 && utf8.Valid(b)
}

// IsBinarySniff reports a NUL byte in the first SniffLen bytes, the cheap test a
// search uses to skip binary files without reading them whole.
func IsBinarySniff(b []byte) bool {
	if enc, _ := sniffBOM(b); enc == UTF16LE || enc == UTF16BE {
		return false
	}
	return bytes.IndexByte(b[:min(len(b), SniffLen)], 0) >= 0
}

func validUTF16(b []byte, enc Encoding) bool {
	if len(b)%2 != 0 {
		return false
	}
	order := byteOrder(enc)
	for i := 0; i < len(b); i += 2 {
		u := order.Uint16(b[i:])
		switch {
		case u >= 0xD800 && u < 0xDC00:
			if i+2 >= len(b) {
				return false
			}
			if v := order.Uint16(b[i+2:]); v < 0xDC00 || v >= 0xE000 {
				return false
			}
			i += 2
		case u >= 0xDC00 && u < 0xE000:
			return false
		}
	}
	return true
}

// byteOrderer reads and appends in one byte order.
type byteOrderer interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

// byteOrder is the byte order of a UTF-16 encoding.
func byteOrder(enc Encoding) byteOrderer {
	if enc == UTF16BE {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

// DominantEOL is "\r\n" when CRLF line ends outnumber lone LF ones in s, else "\n".
func DominantEOL(s string) string {
	crlf := strings.Count(s, "\r\n")
	if crlf > strings.Count(s, "\n")-crlf {
		return "\r\n"
	}
	return "\n"
}

// NormalizeEOL turns every CRLF in s into LF.
func NormalizeEOL(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// RestoreEOL writes s, whose lines end in LF, with line ends eol.
func RestoreEOL(s, eol string) string {
	if eol == "\n" {
		return s
	}
	return strings.ReplaceAll(NormalizeEOL(s), "\n", eol)
}

// Encoding is the byte form a text file was read in, so an edit writes it back the
// same way.
type Encoding int

const (
	UTF8 Encoding = iota
	UTF8BOM
	UTF16LE
	UTF16BE
)

// ErrBadUTF16 reports UTF-16 content with an odd length or an unpaired surrogate.
var ErrBadUTF16 = errors.New("fsx: invalid UTF-16 content")

func sniffBOM(b []byte) (Encoding, int) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return UTF8BOM, 3
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return UTF16LE, 2
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return UTF16BE, 2
	}
	return UTF8, 0
}

// Decode returns the text of b and the encoding its byte-order mark names.
func Decode(b []byte) (string, Encoding, error) {
	enc, n := sniffBOM(b)
	b = b[n:]
	if enc != UTF16LE && enc != UTF16BE {
		return string(b), enc, nil
	}
	if !validUTF16(b, enc) {
		return "", enc, ErrBadUTF16
	}
	order := byteOrder(enc)
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = order.Uint16(b[2*i:])
	}
	return string(utf16.Decode(units)), enc, nil
}

// Encode is the inverse of Decode: s in enc, byte-order mark included.
func Encode(s string, enc Encoding) []byte {
	switch enc {
	case UTF8BOM:
		return append([]byte{0xEF, 0xBB, 0xBF}, s...)
	case UTF16LE, UTF16BE:
		units := utf16.Encode([]rune(s))
		out := make([]byte, 2, 2+2*len(units))
		order := byteOrder(enc)
		order.PutUint16(out, 0xFEFF)
		for _, u := range units {
			out = order.AppendUint16(out, u)
		}
		return out
	}
	return []byte(s)
}
