// Package title is the session titles component (C56, DH-D-VIEW-4; impl.md
// 16.9): every session with a text prompt has a title at once (the fallback, the
// leading words of its first prompt), usually a generated one seconds later, and
// a title the user gave (Rename) is pinned until Unpin. The model call that
// writes a title is in the log: session/title-request is appended and flushed
// before the request, which is rebuilt from that event, is sent; exactly one
// session/title-attempt settles it; an accepted reply becomes session/title in
// the same append group (DH-ADR-VIEW-6). The folds are title (the summary a
// list shows) and titleInput (the first and latest own prompt and their count).
package title

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// The reasons a title operation fails with.
const (
	CodePinned          herr.Reason = "title/pinned"
	CodeInvalid         herr.Reason = "title/invalid"
	CodeNoInput         herr.Reason = "title/no-input"
	CodeNoRoute         herr.Reason = "title/no-route"
	CodeUnknownProvider herr.Reason = "title/unknown-provider"
	CodeRejected        herr.Reason = "title/rejected"
	CodeSuperseded      herr.Reason = "title/superseded"
)

// Source is where a title came from.
type Source string

// The sources (architecture.md sec. 4.1, Title).
const (
	SourceFallback  Source = "fallback"
	SourceGenerated Source = "generated"
	SourceUser      Source = "user"
)

// Purpose is the auxiliary purpose a title request is attributed to.
const Purpose = "session-title"

// Title is the title fold of one session. Text, Source, Pinned and Revision are
// what readers show; the other fields are the fold's bookkeeping, exported so
// a stored row round-trips.
type Title struct {
	Text   string `json:"text"`
	Source Source `json:"source,omitempty"`
	Pinned bool   `json:"pinned"`
	// Revision increments on every session/title and session/title-request: a
	// generation that finds it moved since its request is superseded.
	Revision    int64       `json:"revision"`
	MessageSeqs []event.Seq `json:"messageSeqs,omitempty"`
	RequestSeq  *event.Seq  `json:"requestSeq,omitempty"`
	// Pending is every request no attempt has settled yet, in log order: a
	// Refresh may overlap an automatic generation, and each needs its attempt.
	Pending []event.Seq `json:"pending,omitempty"`
	// Requests counts the session's own requests (after a fork's cut); Covered
	// is the newest prompt seq a request covered.
	Requests int       `json:"requests"`
	Covered  event.Seq `json:"covered"`
	// Own is the first seq of the session's own events: past a fork's cut.
	Own event.Seq `json:"own"`
}

// Config is title.Config (session-views.md sec. 6.2).
type Config struct {
	FallbackMaxWords int    `json:"fallbackMaxWords,omitempty"`
	FallbackMaxBytes int    `json:"fallbackMaxBytes,omitempty"`
	MaxTitleBytes    int    `json:"maxTitleBytes,omitempty"`
	Provider         string `json:"provider,omitempty"`
}

// GeneratorConfig is title.GeneratorConfig (session-views.md sec. 6.2).
type GeneratorConfig struct {
	TargetWords         int           `json:"targetWords,omitempty"`
	TargetCJKCharacters int           `json:"targetCJKCharacters,omitempty"`
	MaxInputBytes       int           `json:"maxInputBytes,omitempty"`
	MaxOutputTokens     int           `json:"maxOutputTokens,omitempty"`
	Timeout             time.Duration `json:"timeout,omitempty"`
	// Route is the title route; empty is the session's logged main route.
	Route string `json:"route,omitempty"`
	// ReasoningEffort is the effort sent; "lowest" leaves the control unset, so
	// the route's default applies, since the runtime publishes no catalogue.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// EffortLowest is the default ReasoningEffort.
const EffortLowest = "lowest"

func (c Config) withDefaults() Config {
	if c.FallbackMaxWords <= 0 {
		c.FallbackMaxWords = 5
	}
	if c.FallbackMaxBytes <= 0 {
		c.FallbackMaxBytes = 40
	}
	if c.MaxTitleBytes <= 0 {
		c.MaxTitleBytes = 80
	}
	if c.Provider == "" {
		c.Provider = FirstPromptID
	}
	return c
}

func (g GeneratorConfig) withDefaults() GeneratorConfig {
	if g.TargetWords <= 0 {
		g.TargetWords = 5
	}
	if g.TargetCJKCharacters <= 0 {
		g.TargetCJKCharacters = 10
	}
	if g.MaxInputBytes <= 0 {
		g.MaxInputBytes = 4096
	}
	if g.MaxOutputTokens <= 0 {
		g.MaxOutputTokens = 4096
	}
	if g.Timeout <= 0 {
		g.Timeout = 60 * time.Second
	}
	if g.ReasoningEffort == "" {
		g.ReasoningEffort = EffortLowest
	}
	return g
}

// effort is the Controls.Effort a request sends.
func (g GeneratorConfig) effort() string {
	if g.ReasoningEffort == EffortLowest {
		return ""
	}
	return g.ReasoningEffort
}

// directional reports the bidirectional formatting characters and the byte
// order mark, which unicode.IsControl does not cover.
func directional(r rune) bool {
	switch {
	case r == 0x061C, r == 0x200E, r == 0x200F, r == 0xFEFF:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// Normalize is the one cleaning of a title: invalid UTF-8, control and
// direction characters removed (a line break or tab counts as a space), runs of
// space collapsed to one, trimmed, and cut to at most maxBytes (0: no limit)
// without splitting a character.
func Normalize(s string, maxBytes int) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToValidUTF8(s, "") {
		switch {
		case unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		case unicode.IsControl(r), directional(r):
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return truncate(b.String(), maxBytes)
}

// truncate cuts s to at most n bytes (0: no limit) at a character boundary and
// trims the space the cut leaves.
func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRightFunc(s[:cut], unicode.IsSpace)
}

// Fallback is the deterministic title of a prompt: its first maxWords words,
// normalized, at most maxBytes: whole words while one fits, else the first word
// cut at a character boundary.
func Fallback(prompt string, maxWords, maxBytes int) string {
	words := strings.Fields(Normalize(prompt, 0))
	if len(words) > maxWords {
		words = words[:maxWords]
	}
	for len(words) > 1 && len(strings.Join(words, " ")) > maxBytes {
		words = words[:len(words)-1]
	}
	return truncate(strings.Join(words, " "), maxBytes)
}

// accept is the one acceptance rule of a reply: its first non-empty line, one
// pair of enclosing quotes removed, normalized, non-empty and within maxBytes.
func accept(reply string, maxBytes int) (string, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(reply), "\n")
	line = strings.TrimSpace(line)
	for _, q := range [][2]string{{`"`, `"`}, {"'", "'"}, {"“", "”"}, {"`", "`"}} {
		if len(line) >= len(q[0])+len(q[1]) && strings.HasPrefix(line, q[0]) && strings.HasSuffix(line, q[1]) {
			line = line[len(q[0]) : len(line)-len(q[1])]
			break
		}
	}
	t := Normalize(line, 0)
	return t, t != "" && len(t) <= maxBytes
}

// Key is the service the component provides: the *Service of
// session-views.md sec. 6.1's Titles (Get, Rename, Unpin, Refresh).
var Key = cordis.NewServiceKey[*Service]("session.title")
