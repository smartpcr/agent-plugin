// Package mediatype is the harness's one table of file extensions, media types and
// highlighting languages (DH-D-UTIL-5, DH-LIB-MIME-R1 to R4). The answers are
// compiled in: they are the same on every machine and never read the operating
// system's registry or mime.types file. The package never calls
// mime.TypeByExtension or mime.AddExtensionType, so nothing else in the process can
// change its answers.
package mediatype

import (
	"mime"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// Unknown is what a caller serves for a type the table lacks.
const Unknown = "application/octet-stream"

// entry is one media type and its extensions, the preferred one first.
type entry struct {
	typ  string
	exts []string
}

// table is the one table. Extensions are lower case with the dot.
var table = []entry{
	{"text/plain", []string{".txt", ".text", ".log", ".conf", ".ini", ".cfg"}},
	{"text/markdown", []string{".md", ".markdown"}},
	{"text/html", []string{".html", ".htm"}},
	{"text/css", []string{".css"}},
	{"text/csv", []string{".csv"}},
	{"text/tab-separated-values", []string{".tsv"}},
	{"text/javascript", []string{".js", ".mjs", ".cjs"}},
	{"text/x-typescript", []string{".ts", ".mts", ".cts"}},
	{"text/x-go", []string{".go"}},
	{"text/x-python", []string{".py"}},
	{"text/x-rust", []string{".rs"}},
	{"text/x-c", []string{".c", ".h"}},
	{"text/x-c++", []string{".cpp", ".cc", ".cxx", ".hpp", ".hh"}},
	{"text/x-java", []string{".java"}},
	{"text/x-csharp", []string{".cs"}},
	{"text/x-shellscript", []string{".sh", ".bash", ".zsh"}},
	{"text/x-powershell", []string{".ps1", ".psm1", ".psd1"}},
	{"text/x-sql", []string{".sql"}},
	{"text/xml", []string{".xml"}},
	{"text/yaml", []string{".yaml", ".yml"}},
	{"application/toml", []string{".toml"}},
	{"application/json", []string{".json", ".map"}},
	{"application/x-ndjson", []string{".jsonl", ".ndjson"}},
	{"application/pdf", []string{".pdf"}},
	{"application/zip", []string{".zip"}},
	{"application/gzip", []string{".gz", ".tgz"}},
	{"application/x-tar", []string{".tar"}},
	{"application/wasm", []string{".wasm"}},
	{"application/msword", []string{".doc"}},
	{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", []string{".docx"}},
	{"application/vnd.ms-excel", []string{".xls"}},
	{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", []string{".xlsx"}},
	{"application/vnd.ms-powerpoint", []string{".ppt"}},
	{"application/vnd.openxmlformats-officedocument.presentationml.presentation", []string{".pptx"}},
	{"application/vnd.oasis.opendocument.text", []string{".odt"}},
	{"application/vnd.oasis.opendocument.spreadsheet", []string{".ods"}},
	{"application/vnd.oasis.opendocument.presentation", []string{".odp"}},
	{"application/rtf", []string{".rtf"}},
	{"image/png", []string{".png"}},
	{"image/jpeg", []string{".jpg", ".jpeg", ".jpe", ".jfif"}},
	{"image/gif", []string{".gif"}},
	{"image/webp", []string{".webp"}},
	{"image/svg+xml", []string{".svg", ".svgz"}},
	{"image/bmp", []string{".bmp"}},
	{"image/x-icon", []string{".ico"}},
	{"image/avif", []string{".avif"}},
	{"image/tiff", []string{".tif", ".tiff"}},
	{"audio/mpeg", []string{".mp3"}},
	{"audio/wav", []string{".wav"}},
	{"audio/ogg", []string{".ogg", ".oga"}},
	{"video/mp4", []string{".mp4", ".m4v"}},
	{"video/webm", []string{".webm"}},
	{"font/woff", []string{".woff"}},
	{"font/woff2", []string{".woff2"}},
	{"font/ttf", []string{".ttf"}},
	{"font/otf", []string{".otf"}},
}

var (
	byExt  = map[string]string{}
	byType = map[string][]string{}
)

func init() {
	for _, e := range table {
		byType[e.typ] = e.exts
		for _, x := range e.exts {
			if _, dup := byExt[x]; dup {
				panic("mediatype: extension " + x + " listed twice")
			}
			byExt[x] = e.typ
		}
	}
}

// ByPath returns the media type of a file name or path from its extension,
// without reading the file and without regard to case. ok is false for an unknown
// extension, which a caller serves as Unknown.
func ByPath(name string) (typ string, ok bool) {
	ext := strings.ToLower(path.Ext(slash(name)))
	if ext == "" {
		return "", false
	}
	typ, ok = byExt[ext]
	return typ, ok
}

// PreferredExtension returns the extension a file of type typ gets; parameters
// such as "; charset=" are ignored.
func PreferredExtension(typ string) (string, bool) {
	exts := byType[base(typ)]
	if len(exts) == 0 {
		return "", false
	}
	return exts[0], true
}

// Extensions returns every extension type typ allows, the preferred one first.
func Extensions(typ string) []string {
	return append([]string(nil), byType[base(typ)]...)
}

// base drops parameters and case from a media type.
func base(typ string) string {
	if t, _, err := mime.ParseMediaType(typ); err == nil {
		return t
	}
	t, _, _ := strings.Cut(typ, ";")
	return strings.ToLower(strings.TrimSpace(t))
}

func slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// maxNameBytes is the longest file name the common file systems take.
const maxNameBytes = 255

// reserved are the device names Windows refuses as a file name, with or without an
// extension.
var reserved = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true}

func init() {
	for _, p := range []string{"com", "lpt"} {
		for i := '1'; i <= '9'; i++ {
			reserved[p+string(i)] = true
		}
	}
}

// SafeName returns a download name for name: characters no file system accepts
// are replaced with "_", a reserved Windows device name is prefixed with "_",
// trailing dots and spaces are dropped, the result fits in 255 bytes without
// splitting a character, and it ends in an extension typ allows (ext, or the
// type's preferred one, is appended when it does not). An empty result is
// "download".
func SafeName(name, typ, ext string) string {
	var b strings.Builder
	for _, r := range path.Base(slash(name)) {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	n := strings.TrimRight(b.String(), ". ")
	if n == "" || n == "." || n == ".." {
		n = "download"
	}
	allowed := Extensions(typ)
	cur := path.Ext(n)
	ok := cur != "" && (len(allowed) == 0 || slices.Contains(allowed, strings.ToLower(cur)))
	stem, suffix := strings.TrimSuffix(n, cur), cur
	if !ok {
		if ext == "" {
			ext, _ = PreferredExtension(typ)
		}
		if ext != "" && !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		stem, suffix = n, ext
	}
	if first, _, _ := strings.Cut(stem, "."); reserved[strings.ToLower(first)] {
		stem = "_" + stem
	}
	for len(stem)+len(suffix) > maxNameBytes && stem != "" {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	if stem == "" {
		stem = "download"
	}
	return stem + suffix
}
