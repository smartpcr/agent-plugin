// Package ident makes every random identifier of the harness
// (DH-ADR-NODECRYPTO-2). Callers treat the result as an opaque string and never
// parse it; the format, today the text form of a version 4 UUID, is not a contract.
//
// ident imports the standard library only (RM-6).
package ident

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

// Reader is the randomness source. Only a test replaces it.
var Reader io.Reader = rand.Reader

// New returns a fresh random identifier. It panics only when the system's
// randomness source fails, which crypto/rand treats as unrecoverable.
func New() string {
	var b [16]byte
	if _, err := io.ReadFull(Reader, b[:]); err != nil {
		panic("ident: randomness source failed: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 9562 variant
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:], b[10:])
	return string(out[:])
}

// Token returns n random bytes (at least 32) as lower-case hex, for secrets such
// as a launch token.
func Token(n int) string {
	if n < 32 {
		n = 32
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(Reader, b); err != nil {
		panic("ident: randomness source failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
