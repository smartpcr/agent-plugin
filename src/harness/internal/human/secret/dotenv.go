package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Origins of the .env sources: the working directory's file and the home's
// config/.env, the third and fourth layers of the resolution order
// (DH-ADR-HUMAN-9).
const (
	OriginProjectEnv = "project-env"
	OriginHomeEnv    = "home-env"
)

// maxDotEnvFile bounds a .env read.
const maxDotEnvFile = 1 << 20

// DotEnvSource reads Ref.EnvName from the .env file at path, named origin in
// descriptions. The file is read again on every lookup, so an edited file is seen
// by the next use, and nothing of it is ever written into the process
// environment. A missing file has no value. The parser is strict (ParseDotEnv): a
// malformed file fails every lookup with a DotEnvError, which names the file, the
// line and the variable, never the line's text.
func DotEnvSource(path, origin string) Source { return dotEnvSource{path: path, origin: origin} }

type dotEnvSource struct{ path, origin string }

func (s dotEnvSource) Origin() string { return s.origin }

func (s dotEnvSource) Lookup(_ context.Context, r Ref) (Value, bool, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Value{}, false, nil
	}
	if err != nil {
		return Value{}, false, &DotEnvError{File: filepath.Base(s.path), Msg: "cannot be read: " + pathless(err).Error()}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDotEnvFile+1))
	if err != nil {
		return Value{}, false, &DotEnvError{File: filepath.Base(s.path), Msg: "cannot be read: " + pathless(err).Error()}
	}
	if len(data) > maxDotEnvFile {
		return Value{}, false, &DotEnvError{File: filepath.Base(s.path), Msg: fmt.Sprintf("is larger than %d bytes", maxDotEnvFile)}
	}
	vars, err := ParseDotEnv(data)
	if err != nil {
		var pe *DotEnvError
		if errors.As(err, &pe) {
			pe.File = filepath.Base(s.path)
		}
		return Value{}, false, err
	}
	for _, v := range vars {
		if v.Name == r.EnvName() {
			return v.Value, true, nil
		}
	}
	return Value{}, false, nil
}

// DotEnvVar is one assignment of a .env file. Its value is a Value, so a parsed
// file prints no value either.
type DotEnvVar struct {
	Name  string
	Value Value
	Line  int
}

// DotEnvError is a .env file that does not parse or cannot be read. It names the
// file, the line and the variable, never the text of the line.
type DotEnvError struct {
	File string // the file's base name; empty for parsed bytes
	Line int    // 0 when the error is not about one line
	Name string // empty when the line names no variable
	Msg  string
}

func (e *DotEnvError) Error() string {
	where := e.File
	if where == "" {
		where = ".env"
	}
	if e.Line > 0 {
		where = fmt.Sprintf("%s:%d", where, e.Line)
	}
	if e.Name != "" {
		where += ": " + e.Name
	}
	return string(ReasonDotEnv) + ": " + where + ": " + e.Msg
}

// Reason classifies the error for herr.
func (e *DotEnvError) Reason() string { return string(ReasonDotEnv) }

// ParseDotEnv parses a .env file strictly: one NAME=value per line, an optional
// "export " prefix, '#' comment lines and " #" trailing comments on unquoted
// values, single quotes taken literally, double quotes with the escapes \n, \t,
// \r, \", \\ and \$. A name is letters, digits and underscores, not starting with
// a digit, and is assigned once. Anything else fails the whole file: a secret
// source never guesses.
func ParseDotEnv(data []byte) ([]DotEnvVar, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var vars []DotEnvVar
	seen := map[string]bool{}
	for i, raw := range strings.Split(string(data), "\n") {
		n := i + 1
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(rest)
		}
		name, rest, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !validEnvName(name) {
			return nil, &DotEnvError{Line: n, Msg: "expected NAME=value with a name of letters, digits and underscores"}
		}
		if !ok {
			return nil, &DotEnvError{Line: n, Name: name, Msg: "missing '='"}
		}
		value, problem := dotEnvValue(strings.TrimSpace(rest))
		if problem != "" {
			return nil, &DotEnvError{Line: n, Name: name, Msg: problem}
		}
		if seen[name] {
			return nil, &DotEnvError{Line: n, Name: name, Msg: "assigned twice"}
		}
		seen[name] = true
		vars = append(vars, DotEnvVar{Name: name, Value: NewValue(value), Line: n})
	}
	return vars, nil
}

func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		switch {
		case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// dotEnvValue returns the value and, on failure, a problem that does not quote it.
func dotEnvValue(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", "unterminated single quote"
		}
		if !commentOnly(s[end+2:]) {
			return "", "text after the closing quote"
		}
		return s[1 : end+1], ""
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			c := s[i]
			switch {
			case c == '"':
				if !commentOnly(s[i+1:]) {
					return "", "text after the closing quote"
				}
				return b.String(), ""
			case c == '\\' && i+1 < len(s):
				i++
				switch s[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				case '"', '\\', '$':
					b.WriteByte(s[i])
				default:
					return "", "unknown escape in double quotes"
				}
			default:
				b.WriteByte(c)
			}
		}
		return "", "unterminated double quote"
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, `"'`) {
		return "", "a quote inside an unquoted value"
	}
	return s, ""
}

func commentOnly(rest string) bool {
	rest = strings.TrimSpace(rest)
	return rest == "" || strings.HasPrefix(rest, "#")
}
