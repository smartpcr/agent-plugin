package file

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Format names the envelope's layout; another format is refused, not guessed.
const Format = "dsh-secret-store/1"

// envelope is the store file.
type envelope struct {
	Format     string `json:"format"`
	Protection string `json:"protection"`
	Generation uint64 `json:"generation"`
	Nonce      []byte `json:"nonce"`
	Sealed     []byte `json:"sealed"`
}

// contents is the sealed part.
type contents struct {
	Entries []entry `json:"entries"`
}

// entry is one stored reference.
type entry struct {
	Ref      string `json:"ref"`
	Value    string `json:"value"`
	Revision uint64 `json:"revision"`
}

// additional binds the header to the sealed part.
func additional(protection string, generation uint64) []byte {
	return []byte(Format + "\x00" + protection + "\x00" + strconv.FormatUint(generation, 10))
}

func aead(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, herr.Wrap(secret.ReasonKeyLost, err, "the secret store's key is not an AES-256 key")
	}
	return cipher.NewGCM(block)
}

// seal encrypts entries into the bytes of a store file.
func seal(key []byte, protection string, generation uint64, entries []entry) ([]byte, error) {
	if entries == nil {
		entries = []entry{}
	}
	plain, err := json.Marshal(contents{Entries: entries})
	if err != nil {
		return nil, herr.New(secret.ReasonSource, "the secret store's contents cannot be encoded")
	}
	g, err := aead(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, herr.Wrap(secret.ReasonSource, err, "no random nonce can be made")
	}
	env := envelope{Format: Format, Protection: protection, Generation: generation, Nonce: nonce,
		Sealed: g.Seal(nil, nonce, plain, additional(protection, generation))}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, herr.New(secret.ReasonSource, "the secret store's envelope cannot be encoded")
	}
	return append(out, '\n'), nil
}

// parseEnvelope reads the store file's header strictly. Its errors quote nothing
// of the file.
func parseEnvelope(data []byte) (envelope, error) {
	var env envelope
	if err := strictJSON(data, &env, StoreFileName); err != nil {
		return envelope{}, err
	}
	if env.Format != Format {
		return envelope{}, herr.Newf(secret.ReasonCorrupt, "%s is not a secret store of format %s", StoreFileName, Format)
	}
	if env.Protection == "" || env.Generation == 0 || len(env.Nonce) == 0 || len(env.Sealed) == 0 {
		return envelope{}, herr.Newf(secret.ReasonCorrupt, "%s lacks its protection, generation, nonce or sealed contents", StoreFileName)
	}
	return env, nil
}

// open decrypts and checks the sealed part. ok is false when the key does not
// open it: the key is lost or wrong, or the file was altered.
func open(key []byte, env envelope) (entries []entry, ok bool, err error) {
	g, err := aead(key)
	if err != nil {
		return nil, false, nil
	}
	if len(env.Nonce) != g.NonceSize() {
		return nil, false, herr.Newf(secret.ReasonCorrupt, "%s has a nonce of %d bytes, not %d", StoreFileName, len(env.Nonce), g.NonceSize())
	}
	plain, err := g.Open(nil, env.Nonce, env.Sealed, additional(env.Protection, env.Generation))
	if err != nil {
		return nil, false, nil
	}
	entries, err = parseContents(plain, env.Generation)
	if err != nil {
		return nil, false, err
	}
	return entries, true, nil
}

// parseContents reads and checks the sealed entries. Its errors name an entry by
// number, never its reference or value.
func parseContents(plain []byte, generation uint64) ([]entry, error) {
	var c contents
	if err := strictJSON(plain, &c, StoreFileName+"'s sealed contents"); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i, e := range c.Entries {
		n := i + 1
		switch _, err := secret.Parse(e.Ref); {
		case err != nil:
			return nil, herr.Newf(secret.ReasonCorrupt, "%s: entry %d names no valid secret reference", StoreFileName, n)
		case seen[e.Ref]:
			return nil, herr.Newf(secret.ReasonCorrupt, "%s: entry %d repeats the reference of an earlier entry", StoreFileName, n)
		case e.Value == "":
			return nil, herr.Newf(secret.ReasonCorrupt, "%s: entry %d holds an empty value", StoreFileName, n)
		case e.Revision == 0 || e.Revision > generation:
			return nil, herr.Newf(secret.ReasonCorrupt, "%s: entry %d has revision %d outside 1 to %d", StoreFileName, n, e.Revision, generation)
		}
		seen[e.Ref] = true
	}
	return c.Entries, nil
}

// strictJSON decodes one JSON object with no unknown field and nothing after it.
// The error gives a byte offset only: encoding/json's own messages may quote a
// character or a field name of the input.
func strictJSON(data []byte, v any, what string) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		if dec.More() {
			return herr.Newf(secret.ReasonCorrupt, "%s has data after its object at byte %d", what, dec.InputOffset())
		}
		return nil
	}
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return herr.Newf(secret.ReasonCorrupt, "%s ends early, at byte %d", what, len(data))
	case errors.As(err, &syn):
		return herr.Newf(secret.ReasonCorrupt, "%s is malformed at byte %d", what, syn.Offset)
	case errors.As(err, &typ):
		return herr.Newf(secret.ReasonCorrupt, "%s has a value of the wrong type at byte %d", what, typ.Offset)
	}
	return herr.Newf(secret.ReasonCorrupt, "%s is malformed near byte %d", what, dec.InputOffset())
}
