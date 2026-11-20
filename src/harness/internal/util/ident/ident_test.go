package ident

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"regexp"
	"testing"
)

type counting struct{ n int }

func (c *counting) Read(p []byte) (int, error) {
	c.n += len(p)
	return rand.Read(p)
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestNew is DH-T-NODECRYPTO-3: UUID text form with the right version and variant
// bits, no repeat in a million values, and every byte from the swapped reader.
func TestNew(t *testing.T) {
	c := &counting{}
	Reader = c
	t.Cleanup(func() { Reader = rand.Reader })

	n := 1_000_000
	if testing.Short() {
		n = 10_000
	}
	seen := make(map[string]struct{}, n)
	for range n {
		id := New()
		if !uuidV4.MatchString(id) {
			t.Fatalf("New() = %q, not a version 4 UUID", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("repeat %q", id)
		}
		seen[id] = struct{}{}
	}
	if c.n != 16*n {
		t.Fatalf("read %d bytes from the reader, want %d", c.n, 16*n)
	}

	before := c.n
	tok := Token(8)
	if b, err := hex.DecodeString(tok); err != nil || len(b) < 32 {
		t.Fatalf("Token(8) = %q: %d bytes, %v", tok, len(b), err)
	}
	if c.n-before != 32 {
		t.Fatalf("Token read %d bytes from the reader", c.n-before)
	}
}

func TestNewPanicsOnFailedSource(t *testing.T) {
	Reader = failing{}
	t.Cleanup(func() { Reader = rand.Reader })
	defer func() {
		if recover() == nil {
			t.Fatal("New did not panic on a failed source")
		}
	}()
	New()
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
