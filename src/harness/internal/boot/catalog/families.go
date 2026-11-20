package catalog

// Families are the registered first segments of a catalogue name (DH-ADR-BOOT-13),
// in the order of docs/harness/areas/boot.md §6.2 "Names", which records each
// family's owning area. A new area adds its family here, once; CheckName rejects any
// other. The prefix x. is reserved for experimental features and ui. for page
// components.
var Families = []string{
	"boot", "host", "wire", "cli", "update", "util", "ui", "x",
	"agent", "tools", "tool", "llm", "session", "storage", "subagent", "team", "jobs",
	"schedule", "goal", "plan", "command", "context", "compact", "skill",
	"human", "permission", "plugin", "mcp", "lsp", "web", "office", "browser", "computer",
	"program", "workflow", "subprocess", "terminal", "shell", "shellexec", "sandbox", "fs",
	"deliver",
	"translate", "title",
}
