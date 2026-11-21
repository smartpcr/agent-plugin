package prompt

import (
	"fmt"
	"regexp"
	"strings"
)

var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// interpolate replaces every {{name}} in text with lookup(name), strictly: an
// unknown or valueless reference is an error. "{{{{" is a literal "{{", and a
// substituted value is written as it is, never scanned again.
func interpolate(text string, lookup func(name string) (string, error)) (string, error) {
	var b strings.Builder
	for {
		i := strings.Index(text, "{{")
		if i < 0 {
			b.WriteString(text)
			return b.String(), nil
		}
		b.WriteString(text[:i])
		text = text[i:]
		if strings.HasPrefix(text, "{{{{") {
			b.WriteString("{{")
			text = text[4:]
			continue
		}
		end := strings.Index(text, "}}")
		if end < 0 {
			return "", &Error{Code: CodeMalformed, Msg: fmt.Sprintf("an unclosed reference %q", clip(text))}
		}
		name := strings.TrimSpace(text[2:end])
		if !varName.MatchString(name) {
			return "", &Error{Code: CodeMalformed, Msg: fmt.Sprintf("a malformed reference %q", text[:end+2])}
		}
		v, err := lookup(name)
		if err != nil {
			return "", err
		}
		b.WriteString(v)
		text = text[end+2:]
	}
}

func clip(s string) string {
	if len(s) > 24 {
		return s[:24] + "..."
	}
	return s
}
