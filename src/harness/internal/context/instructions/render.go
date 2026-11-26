package instructions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The forms of the source record.
const (
	FormBaseline = "baseline"
	FormUpdate   = "update"
)

// Record is the source data of an instructions message and the state of the
// instructions.visible fold (architecture.md sec. 4.1).
type Record struct {
	Form    string    `json:"form,omitempty"`
	Root    string    `json:"root,omitempty"`
	Files   []File    `json:"files"`
	Refused []Refusal `json:"refused,omitempty"`
	// Unwatched are the candidates the watch could not cover; the chain is
	// read again at every step until it can.
	Unwatched []Unwatched `json:"unwatched,omitempty"`
	// Budget is the byte budget the section was rendered with.
	Budget int `json:"budget"`
	// Digest is the hex SHA-256 of the rendered section and of its files,
	// refusals and unwatched candidates: what the model saw and what was
	// diagnosed.
	Digest string `json:"digest,omitempty"`
}

// Unwatched is a candidate whose watch could not be registered.
type Unwatched struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// Section is a rendered section and its record.
type Section struct {
	Text   string
	Record Record
}

const (
	frameOpen  = "<instructions>\n"
	frameClose = "</instructions>\n"
	preamble   = "These are the project's instruction files, from the project root down to the working directory. " +
		"A later file is more specific and takes precedence over an earlier one. " +
		"No instruction file overrides system, developer or direct user messages.\n"
	userPreamble = "These are the user's own instruction files, then the project's, from the project root down to the working directory. " +
		"A later file is more specific and takes precedence over an earlier one. " +
		"No instruction file overrides system, developer or direct user messages.\n"
	noFiles       = "There are no instruction files.\n"
	budgetMarker  = "\n[truncated: the rest of this file did not fit the instruction budget]\n"
	sourceMarkerF = "\n[truncated: the file is longer than the %d bytes read]\n"
)

// frameTag matches the opening of a frame tag in any case, so a file cannot
// close the frame it is rendered in.
var frameTag = regexp.MustCompile(`(?i)<(/?)(instructions|file|omitted)`)

// Escape makes text safe inside the frame.
func Escape(text string) string { return frameTag.ReplaceAllString(text, "&lt;$1$2") }

func attr(s string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Render frames obs within budget bytes per chain (Config.Validate holds the
// floor of the budget, Render does not). Each chain is guaranteed budget bytes,
// the frame charged to the project chain, and the bytes one chain does not use
// are lent to the other. Within a chain the broadest visible files are omitted
// first; when only the most specific is left and it still does not fit, it is
// cut at a character boundary with a marker. When the omitted files' tags alone
// would overrun a chain's share, they are listed as one summary tag.
func Render(obs Observation, budget int) Section {
	head := frameOpen + preamble
	if obs.user {
		head = frameOpen + userPreamble
	}
	var user, proj []Entry
	for _, e := range obs.Entries {
		if e.File.Scope == ScopeUser {
			user = append(user, e)
		} else {
			proj = append(proj, e)
		}
	}
	shareU, shareP := 0, budget-len(head)-len(frameClose)
	if obs.user {
		shareU = budget
		_, needU := chain(user, math.MaxInt)
		_, needP := chain(proj, math.MaxInt)
		switch {
		case len(needU) < shareU:
			shareP += shareU - len(needU)
		case len(needP) < shareP:
			shareU += shareP - len(needP)
		}
	}
	filesU, bodyU := chain(user, shareU)
	filesP, bodyP := chain(proj, shareP)
	body := bodyU + bodyP
	if body == "" {
		body = noFiles
	}
	text := head + body + frameClose
	rec := Record{
		Root: obs.Root, Files: append(filesU, filesP...), Refused: append([]Refusal(nil), obs.Refused...),
		Unwatched: append([]Unwatched(nil), obs.Unwatched...), Budget: budget,
	}
	rec.Digest = digest(text, rec)
	return Section{Text: text, Record: rec}
}

// chain is the files and the tags of one chain within share bytes.
func chain(entries []Entry, share int) ([]File, string) {
	files, body := fit(entries, share, false)
	if len(body) > share {
		files, body = fit(entries, share, true)
	}
	return files, body
}

// fit is the files and the tags of entries within budget; compact lists the
// omitted files in one tag.
func fit(entries []Entry, budget int, compact bool) ([]File, string) {
	files := make([]File, len(entries))
	bodies := make([]string, len(entries))
	var keep []int
	for i, e := range entries {
		files[i] = e.File
		if e.File.State != Visible {
			continue
		}
		b := Escape(e.Text)
		if e.limited {
			b += strings.Replace(sourceMarkerF, "%d", strconv.Itoa(e.File.Bytes), 1)
		}
		bodies[i] = b
		keep = append(keep, i)
	}
	text := build(files, bodies, compact)
	if len(text) > budget && len(keep) > 1 {
		// Drop the broadest files by the size each one adds, then build once.
		size, omitted, omittedBytes := len(text), 0, 0
		for _, f := range files {
			if f.State == Omitted {
				omitted, omittedBytes = omitted+1, omittedBytes+f.Bytes
			}
		}
		for size > budget && len(keep) > 1 {
			i := keep[0]
			size -= len(fileTag(files[i].Path, bodies[i]))
			if compact {
				size -= summaryLen(omitted, omittedBytes)
				omitted, omittedBytes = omitted+1, omittedBytes+files[i].Bytes
				size += summaryLen(omitted, omittedBytes)
			} else {
				size += len(omittedTag(files[i].Path, files[i].Bytes))
			}
			files[i].State = Omitted
			keep = keep[1:]
		}
		text = build(files, bodies, compact)
	}
	for len(text) > budget && len(keep) > 0 {
		if len(keep) > 1 {
			files[keep[0]].State = Omitted
			keep = keep[1:]
			text = build(files, bodies, compact)
			continue
		}
		last := keep[0]
		over := len(text) - budget + len(budgetMarker)
		n := len(bodies[last]) - over
		for n > 0 && !utf8.RuneStart(bodies[last][n]) {
			n--
		}
		if n <= 0 {
			files[last].State = Omitted
			keep = nil
		} else {
			bodies[last] = bodies[last][:n] + budgetMarker
			files[last].Truncated = true
		}
		text = build(files, bodies, compact)
		break
	}
	return files, text
}

// digest is the hex SHA-256 of the text and of the record's diagnostics, so a
// refusal, an unwatched candidate or a file state that leaves the text as it
// was is still a change.
func digest(text string, r Record) string {
	h := sha256.New()
	h.Write([]byte(text))
	h.Write([]byte{0})
	diag, _ := json.Marshal(struct {
		Files     []File      `json:"files"`
		Refused   []Refusal   `json:"refused"`
		Unwatched []Unwatched `json:"unwatched"`
	}{r.Files, r.Refused, r.Unwatched})
	h.Write(diag)
	return hex.EncodeToString(h.Sum(nil))
}

func fileTag(path, body string) string {
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return "\n<file path=\"" + attr(path) + "\">\n" + body + "</file>\n"
}

func omittedTag(path string, bytes int) string {
	return "\n<file path=\"" + attr(path) + "\" omitted=\"budget\" bytes=\"" + strconv.Itoa(bytes) + "\"/>\n"
}

func summaryTag(files, bytes int) string {
	return "\n<omitted reason=\"budget\" files=\"" + strconv.Itoa(files) + "\" bytes=\"" + strconv.Itoa(bytes) + "\"/>\n"
}

// summaryLen is the length of the summary tag; 0 for no file.
func summaryLen(files, bytes int) int {
	if files == 0 {
		return 0
	}
	return len(summaryTag(files, bytes))
}

// build is the tags of files: the visible bodies and the omitted files.
func build(files []File, bodies []string, compact bool) string {
	var b strings.Builder
	omitted, omittedBytes := 0, 0
	for i, f := range files {
		switch f.State {
		case Visible:
			b.WriteString(fileTag(f.Path, bodies[i]))
		case Omitted:
			if compact {
				omitted, omittedBytes = omitted+1, omittedBytes+f.Bytes
				continue
			}
			b.WriteString(omittedTag(f.Path, f.Bytes))
		}
	}
	if omitted > 0 {
		b.WriteString(summaryTag(omitted, omittedBytes))
	}
	return b.String()
}
