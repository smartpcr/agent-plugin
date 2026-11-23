package catalog

// Family is one registered first segment of a catalogue name and the area that
// owns it (DH-ADR-BOOT-13): a component in the family is specified by that area. Area
// is an area key of docs/harness/design.md ("Area keys"); "X" is the experimental
// areas X1 to X6 together, which share the x. prefix.
type Family struct {
	Name string
	Area string
}

// Families are the registered families, in the order of docs/harness/areas/boot.md
// §6.2 "Names". A new area adds its family here, once, with its area; CheckName
// rejects any other. The prefix x. is reserved for experimental features and ui. for
// page components.
var Families = []Family{
	{"boot", "A21"}, {"host", "A18"}, {"wire", "A20"}, {"cli", "A24"}, {"update", "A24"},
	{"util", "A23"}, {"ui", "C1"}, {"x", "X"},
	{"agent", "A02"}, {"tools", "A03"}, {"tool", "A03"}, {"llm", "A04"}, {"session", "A01"},
	{"storage", "A07"}, {"subagent", "A12"}, {"team", "A12"}, {"jobs", "A13"},
	{"schedule", "A13"}, {"goal", "A13"}, {"plan", "A13"}, {"command", "A13"},
	{"context", "A14"}, {"compact", "A14"}, {"skill", "A14"},
	{"human", "A17"}, {"permission", "A17"}, {"plugin", "A21"}, {"mcp", "A15"}, {"lsp", "A15"},
	{"web", "A15"}, {"office", "A16"}, {"browser", "A16"}, {"computer", "A16"},
	{"program", "A11"}, {"workflow", "A11"}, {"subprocess", "A09"}, {"terminal", "A09"},
	{"shell", "A09"}, {"shellexec", "A09"}, {"sandbox", "A10"}, {"fs", "A08"},
	{"deliver", "A08"},
	{"translate", "X6"}, {"title", "X6"},
}

// family returns the registered family called name.
func family(name string) (Family, bool) {
	for _, f := range Families {
		if f.Name == name {
			return f, true
		}
	}
	return Family{}, false
}
