package mediatype

import (
	"path"
	"strings"
)

// Languages are the highlighting-language ids Language returns. Each is an id the
// UI's highlighter accepts; the list is closed so a test can hold both sides to it.
var Languages = []string{
	"bash", "c", "cpp", "csharp", "css", "csv", "diff", "dockerfile", "go", "html",
	"ini", "java", "javascript", "json", "jsonl", "kotlin", "lua", "makefile",
	"markdown", "php", "plaintext", "powershell", "python", "ruby", "rust", "scss",
	"sql", "swift", "toml", "typescript", "tsx", "jsx", "xml", "yaml",
}

// langByName matches whole file names, without regard to case.
var langByName = map[string]string{
	"dockerfile":     "dockerfile",
	"containerfile":  "dockerfile",
	"makefile":       "makefile",
	"gnumakefile":    "makefile",
	"go.mod":         "go",
	"go.sum":         "plaintext",
	"gemfile":        "ruby",
	"rakefile":       "ruby",
	".bashrc":        "bash",
	".bash_profile":  "bash",
	".zshrc":         "bash",
	".profile":       "bash",
	".gitignore":     "plaintext",
	".gitattributes": "plaintext",
	".editorconfig":  "ini",
	"cmakelists.txt": "plaintext",
}

var langByExt = map[string]string{
	".sh": "bash", ".bash": "bash", ".zsh": "bash",
	".c": "c", ".h": "c",
	".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp",
	".cs": "csharp", ".css": "css", ".scss": "scss", ".csv": "csv",
	".diff": "diff", ".patch": "diff",
	".go": "go", ".html": "html", ".htm": "html",
	".ini": "ini", ".cfg": "ini", ".conf": "ini",
	".java": "java", ".kt": "kotlin", ".kts": "kotlin",
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "jsx",
	".ts": "typescript", ".mts": "typescript", ".cts": "typescript", ".tsx": "tsx",
	".json": "json", ".jsonl": "jsonl", ".ndjson": "jsonl",
	".lua": "lua", ".mk": "makefile",
	".md": "markdown", ".markdown": "markdown",
	".php": "php", ".txt": "plaintext", ".log": "plaintext",
	".ps1": "powershell", ".psm1": "powershell", ".psd1": "powershell",
	".py": "python", ".rb": "ruby", ".rs": "rust",
	".sql": "sql", ".swift": "swift", ".toml": "toml",
	".xml": "xml", ".svg": "xml", ".yaml": "yaml", ".yml": "yaml",
}

// Language returns the highlighting language of a file: whole names such as
// Dockerfile and Makefile first, then names like "Dockerfile.dev", then the
// extension. An unknown file is "plaintext".
func Language(p string) string {
	name := strings.ToLower(path.Base(slash(p)))
	if l, ok := langByName[name]; ok {
		return l
	}
	if stem, _, ok := strings.Cut(name, "."); ok && stem != "" {
		if l, ok := langByName[stem]; ok && (l == "dockerfile" || l == "makefile") {
			return l
		}
	}
	if l, ok := langByExt[path.Ext(name)]; ok {
		return l
	}
	return "plaintext"
}
