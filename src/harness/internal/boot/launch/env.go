package launch

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/homedir"
)

// Source says where a launch environment value came from.
type Source string

// The sources, highest precedence first.
const (
	SourceProcess Source = "process"
	SourceProject Source = "project-file"
	SourceHome    Source = "home-file"
	// SourceSystem is the operating system's proxy setting: proxy names only,
	// below every other source.
	SourceSystem Source = "system"
)

// Env is the launch environment: the process environment, then the working
// directory's .env, then the home's .env, then (proxy names only) the system
// proxy setting.
type Env interface {
	Lookup(name string) (value string, from Source, ok bool)
}

type entry struct {
	name  string // as written
	value string
	from  Source
}

// Snapshot is the frozen launch environment. It is never written into the process
// environment.
type Snapshot struct {
	goos   string
	layers []map[string]entry // by precedence
}

var _ Env = (*Snapshot)(nil)

func newSnapshot(goos string, layers ...map[string]entry) *Snapshot {
	s := &Snapshot{goos: goos}
	for _, l := range layers {
		if len(l) > 0 {
			s.layers = append(s.layers, l)
		}
	}
	return s
}

// NewSnapshot returns a snapshot of the process environment environ only, for a
// caller that reads no file (a test, an offline command).
func NewSnapshot(goos string, environ []string) *Snapshot {
	return newSnapshot(goos, parseEnviron(environ, goos))
}

// Lookup returns the value of name and its source. Names are case-insensitive on
// Windows.
func (s *Snapshot) Lookup(name string) (string, Source, bool) {
	if s == nil {
		return "", "", false
	}
	k := key(name, s.goos)
	for _, l := range s.layers {
		if e, ok := l[k]; ok {
			return e.value, e.from, true
		}
	}
	return "", "", false
}

// Func adapts Lookup to the plain form expr and homedir take.
func (s *Snapshot) Func() func(name string) (string, bool) {
	return func(name string) (string, bool) {
		v, _, ok := s.Lookup(name)
		return v, ok
	}
}

// Trusted returns the view limited to the process, the home file and the system
// proxy setting: what A23's egress reads proxy settings from, so a project file can
// never supply one.
func (s *Snapshot) Trusted() *Snapshot {
	if s == nil {
		return nil
	}
	t := &Snapshot{goos: s.goos}
	for _, l := range s.layers {
		if sourceOf(l) != SourceProject {
			t.layers = append(t.layers, l)
		}
	}
	return t
}

// Names returns every name the snapshot holds, sorted, each once.
func (s *Snapshot) Names() []string {
	if s == nil {
		return nil
	}
	seen := map[string]string{}
	for _, l := range s.layers {
		for k, e := range l {
			if _, ok := seen[k]; !ok {
				seen[k] = e.name
			}
		}
	}
	names := make([]string, 0, len(seen))
	for _, n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func sourceOf(l map[string]entry) Source {
	for _, e := range l {
		return e.from
	}
	return ""
}

func key(name, goos string) string {
	if goos == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

// parseEnviron reads "NAME=value" entries; the first of two entries for one name
// wins, as the C library's getenv does. Windows' "=C:=C:\" entries are skipped.
func parseEnviron(environ []string, goos string) map[string]entry {
	m := make(map[string]entry, len(environ))
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		k := key(name, goos)
		if _, dup := m[k]; !dup {
			m[k] = entry{name: name, value: value, from: SourceProcess}
		}
	}
	return m
}

// proxyNames are the proxy names: accepted from the home file and the system
// setting, refused from a project file.
var proxyNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FTP_PROXY"}

// endpointSuffixes mark endpoint-routing names (DEEPSEEK_BASE_URL): accepted from
// the home file only.
var endpointSuffixes = []string{"_BASE_URL", "_API_BASE", "_ENDPOINT", "_API_URL"}

// trustNames route TLS trust; accepted from the home file only.
var trustNames = []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE"}

// launchOnly are the names no discovered file may set, grouped by function as
// upstream lists them: process launch, ambient programs, pre-run code, and home
// locations. The DSH_ namespace is launch-only as a whole.
var launchOnly = []string{
	// process launch
	"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT", "GODEBUG", "GOTRACEBACK", "GOMAXPROCS", "GOGC", "GOMEMLIMIT",
	"NODE_OPTIONS", "NODE_PATH", "PYTHONPATH", "PYTHONHOME", "PERL5OPT", "PERL5LIB", "RUBYOPT", "RUBYLIB",
	"JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_EXEC_PATH", "GIT_CONFIG_GLOBAL",
	// ambient programs
	"PATH", "PATHEXT", "SHELL", "COMSPEC", "EDITOR", "VISUAL", "PAGER", "BROWSER", "TERM",
	// pre-run code
	"BASH_ENV", "ENV", "ZDOTDIR", "PROMPT_COMMAND", "PYTHONSTARTUP",
	// home and user locations
	"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "TMPDIR", "TEMP", "TMP",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
}

// Class says which discovered files may set a name.
type Class int

// The classes.
const (
	// Ordinary: any source may set it.
	Ordinary Class = iota
	// HomeOnly: proxy, endpoint and trust routing; the home file may set it, a
	// project file may not.
	HomeOnly
	// LaunchOnly: only the process environment may set it.
	LaunchOnly
)

// Classify returns the class of name. Names compare case-insensitively.
func Classify(name string) Class {
	u := strings.ToUpper(name)
	switch {
	case strings.HasPrefix(u, "DSH_") || u == homedir.Env || strings.HasPrefix(u, "DYLD_"):
		return LaunchOnly
	case slices.Contains(launchOnly, u):
		return LaunchOnly
	case slices.Contains(proxyNames, u) || slices.Contains(trustNames, u):
		return HomeOnly
	}
	for _, s := range endpointSuffixes {
		if strings.HasSuffix(u, s) {
			return HomeOnly
		}
	}
	return Ordinary
}

// IsProxyName reports a proxy name (either case).
func IsProxyName(name string) bool { return slices.Contains(proxyNames, strings.ToUpper(name)) }

func systemProxy(m map[string]string, goos string) map[string]entry {
	out := map[string]entry{}
	for name, value := range m {
		if IsProxyName(name) && value != "" {
			out[key(name, goos)] = entry{name: name, value: value, from: SourceSystem}
		}
	}
	return out
}

// readEnvFile reads and checks one discovered file. A missing file is no file.
func readEnvFile(read ReadFile, path string, from Source, goos string) (map[string]entry, error) {
	data, err := read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, herr.Wrap(ReasonRead, err, "launch: "+homedir.Display(path))
	}
	vars, err := ParseDotenv(data)
	if err != nil {
		var pe *ParseError
		if errors.As(err, &pe) {
			pe.File = homedir.Display(path)
		}
		return nil, err
	}
	m := make(map[string]entry, len(vars))
	for _, v := range vars {
		switch c := Classify(v.Name); {
		case c == LaunchOnly:
			return nil, herr.Newf(ReasonForbiddenName,
				"%s:%d sets %s, a launch-only name; remove it from the file and set it in the process environment",
				homedir.Display(path), v.Line, v.Name)
		case c == HomeOnly && from == SourceProject:
			return nil, herr.Newf(ReasonForbiddenName,
				"%s:%d sets %s, a proxy or endpoint name; set it in the home's config/.env or the process environment",
				homedir.Display(path), v.Line, v.Name)
		}
		// ParseDotenv's check is exact; on Windows Foo and FOO are one variable.
		k := key(v.Name, goos)
		if _, dup := m[k]; dup {
			return nil, &ParseError{File: homedir.Display(path), Line: v.Line, Name: v.Name, Msg: "assigned twice"}
		}
		m[k] = entry{name: v.Name, value: v.Value, from: from}
	}
	return m, nil
}

// Var is one assignment of a .env file.
type Var struct {
	Name  string
	Value string
	Line  int
}

// ParseError is a .env syntax error. It names the file, line and variable, never
// the value.
type ParseError struct {
	File string
	Line int
	Name string // empty when the line names no variable
	Msg  string
}

func (e *ParseError) Error() string {
	where := e.File
	if where == "" {
		where = ".env"
	}
	if e.Name != "" {
		return fmt.Sprintf("%s:%d: %s: %s", where, e.Line, e.Name, e.Msg)
	}
	return fmt.Sprintf("%s:%d: %s", where, e.Line, e.Msg)
}

// Reason classifies a parse error for herr.
func (e *ParseError) Reason() string { return string(ReasonBadFile) }

// ParseDotenv parses a .env file strictly: one NAME=value per line, an optional
// "export " prefix, '#' comments, single quotes taken literally, double quotes with
// \n, \t, \", \\ escapes. A name may be assigned once.
func ParseDotenv(data []byte) ([]Var, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var vars []Var
	seen := map[string]bool{}
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		n := i + 1
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, rest, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !validName(name) {
			return nil, &ParseError{Line: n, Msg: "expected NAME=value with a name of letters, digits and underscores"}
		}
		if !ok {
			return nil, &ParseError{Line: n, Name: name, Msg: "missing '='"}
		}
		value, err := parseValue(strings.TrimSpace(rest))
		if err != "" {
			return nil, &ParseError{Line: n, Name: name, Msg: err}
		}
		if seen[name] {
			return nil, &ParseError{Line: n, Name: name, Msg: "assigned twice"}
		}
		seen[name] = true
		vars = append(vars, Var{Name: name, Value: value, Line: n})
	}
	return vars, nil
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// parseValue returns the value and, on failure, a message that does not quote it.
func parseValue(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", "unterminated single quote"
		}
		if !trailingOK(s[end+2:]) {
			return "", "text after the closing quote"
		}
		return s[1 : end+1], ""
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			c := s[i]
			switch {
			case c == '"':
				if !trailingOK(s[i+1:]) {
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

func trailingOK(rest string) bool {
	rest = strings.TrimSpace(rest)
	return rest == "" || strings.HasPrefix(rest, "#")
}
