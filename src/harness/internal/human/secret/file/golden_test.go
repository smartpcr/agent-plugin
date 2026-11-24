package file

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// leak is the fake value each malformed store carries where a parser could quote it.
const leak = "sk-golden-FAKE-LEAK"

// sealRaw seals plain with key as a store file of generation 1.
func sealRaw(t *testing.T, key []byte, plain string) []byte {
	t.Helper()
	g, err := aead(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(envelope{Format: Format, Protection: ProtectionFile, Generation: 1, Nonce: nonce,
		Sealed: g.Seal(nil, nonce, []byte(plain), additional(ProtectionFile, 1))})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// DH-T-HUMAN-10 (golden): every parse error of the store file and of its sealed
// contents names the file and a byte offset or entry number, and quotes nothing:
// each malformed file below carries a fake value where a parser could echo it.
//
//dsh:test DH-T-HUMAN-10
func TestStoreParseErrorsGolden(t *testing.T) {
	dir := tempSecrets(t)
	key, err := KeyFile(dir).Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		data []byte
	}{
		{"not json", []byte(leak)},
		{"truncated", []byte(`{"format":"` + Format + `","sealed":"` + leak)},
		{"unknown field", []byte(`{"` + leak + `":1}`)},
		{"wrong type", []byte(`{"format":"` + Format + `","generation":"` + leak + `"}`)},
		{"bad base64", []byte(`{"format":"` + Format + `","protection":"file","generation":1,"nonce":"` + leak + `!","sealed":"AAAA"}`)},
		{"other format", []byte(`{"format":"` + leak + `","protection":"file","generation":1,"nonce":"AAAA","sealed":"AAAA"}`)},
		{"missing parts", []byte(`{"format":"` + Format + `","protection":"file","generation":1}`)},
		{"data after the object", []byte(`{"format":"` + Format + `"} "` + leak + `"`)},
		{"short nonce", []byte(`{"format":"` + Format + `","protection":"file","generation":1,"nonce":"AAAA","sealed":"AAAA"}`)},
		{"sealed truncated", sealRaw(t, key, `{"entries":[{"ref":"secret://a/b","value":"`+leak)},
		{"sealed unknown field", sealRaw(t, key, `{"entries":[],"`+leak+`":true}`)},
		{"sealed bad reference", sealRaw(t, key, `{"entries":[{"ref":"`+leak+`","value":"`+leak+`","revision":1}]}`)},
		{"sealed repeated reference", sealRaw(t, key, `{"entries":[{"ref":"secret://a/b","value":"`+leak+`","revision":1},{"ref":"secret://a/b","value":"`+leak+`","revision":1}]}`)},
		{"sealed empty value", sealRaw(t, key, `{"entries":[{"ref":"secret://a/b","value":"","revision":1}]}`)},
		{"sealed revision beyond the store", sealRaw(t, key, `{"entries":[{"ref":"secret://a/b","value":"`+leak+`","revision":9}]}`)},
	}
	var lines []string
	s := New(dir, Options{})
	r := secret.MustParse("secret://a/b")
	for _, c := range cases {
		if err := os.WriteFile(filepath.Join(dir, StoreFileName), c.data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := s.Lookup(context.Background(), r)
		if err == nil {
			t.Fatalf("%s: no error", c.name)
		}
		_, werr := s.Set(context.Background(), r, secret.NewValue(leak), "")
		if werr == nil || werr.Error() != err.Error() {
			t.Fatalf("%s: Set reports %v; Lookup %v", c.name, werr, err)
		}
		lines = append(lines, c.name+": "+err.Error())
	}
	got := strings.Join(lines, "\n") + "\n"
	if strings.Contains(got, "FAKE") {
		t.Fatalf("a parse error quotes the file:\n%s", got)
	}
	golden.Assert(t, "store-parse-errors", got)
}
