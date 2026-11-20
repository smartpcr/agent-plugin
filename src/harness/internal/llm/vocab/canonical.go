package vocab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// Encode is the canonical JSON encoding of v, the bytes a request digest is
// computed over. Object keys, including those inside raw JSON fields such as
// tool arguments and source data, are in byte order; there is no insignificant
// white space; numbers keep their written form; '<', '>' and '&' are not
// escaped. Equal values therefore encode to equal bytes however their maps were
// built.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return Canonical(buf.Bytes())
}

// Canonical rewrites one JSON document in the canonical form of Encode.
func Canonical(doc []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("vocab: canonical: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("vocab: canonical: trailing data after the document")
	}
	var out bytes.Buffer
	if err := write(&out, tree); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func write(out *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		out.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeString(out, k); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := write(out, t[k]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := write(out, e); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case string:
		return writeString(out, t)
	case json.Number:
		out.WriteString(t.String())
	case bool:
		if t {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case nil:
		out.WriteString("null")
	default:
		return fmt.Errorf("vocab: canonical: unexpected %T", v)
	}
	return nil
}

func writeString(out *bytes.Buffer, s string) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	out.Truncate(out.Len() - 1) // the encoder's newline
	return nil
}

// Digest is "sha256:" and the hex SHA-256 of the canonical encoding of v.
func Digest(v any) (string, error) {
	b, err := Encode(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Decode reads data into dst strictly, refusing unknown fields and trailing
// data, then validates dst when it has a Validate method.
func Decode(data []byte, dst any) error {
	if err := strictUnmarshal(data, dst); err != nil {
		return err
	}
	if v, ok := dst.(interface{ Validate() error }); ok {
		return v.Validate()
	}
	return nil
}

func strictUnmarshal(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("vocab: decode: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("vocab: decode: trailing data after the document")
	}
	return nil
}
