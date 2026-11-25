package agentpreset

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/yamlx"
)

// shipped are the agent presets compiled into the binary (DH-D-BUNDLE-5,
// bundle-preset.md §6.2 "Shipped presets"): one declaration per file, named by its
// id.
//
//go:embed presets/*.yml
var shipped embed.FS

// Standard is the id of the default shipped preset.
const Standard = "standard"

// ShippedNames lists the ids of the shipped presets, sorted.
func ShippedNames() []string {
	files, _ := fs.Glob(shipped, "presets/*.yml")
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = strings.TrimSuffix(path.Base(f), ".yml")
	}
	return names
}

// Shipped is the shipped declaration id, read from presets/<id>.yml. The file is
// decoded strictly: an unknown key, a wrong type or an id that is not the file's name
// is an error, a build defect the tests keep out of a release.
func Shipped(id string) (Declaration, error) {
	file := "presets/" + id + ".yml"
	data, err := shipped.ReadFile(file)
	if err != nil {
		return Declaration{}, fmt.Errorf("agentpreset: no shipped preset %q (shipped: %s)", id, strings.Join(ShippedNames(), ", "))
	}
	return ParseDeclaration(file, data, id)
}

// ShippedAll is every shipped declaration in the order of ShippedNames.
func ShippedAll() ([]Declaration, error) {
	var out []Declaration
	for _, id := range ShippedNames() {
		d, err := Shipped(id)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// ParseDeclaration decodes one declaration file in the harness YAML dialect, refusing
// unknown keys. A non-empty id must be the declaration's id.
func ParseDeclaration(file string, data []byte, id string) (Declaration, error) {
	doc, err := yamlx.Parse(file, data)
	if err != nil {
		return Declaration{}, fmt.Errorf("agentpreset: %w", err)
	}
	raw, err := json.Marshal(doc.Value().Plain())
	if err != nil {
		return Declaration{}, fmt.Errorf("agentpreset: %s: %w", file, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Declaration
	if err := dec.Decode(&d); err != nil {
		return Declaration{}, fmt.Errorf("agentpreset: %s: %w", file, err)
	}
	if id != "" && d.ID != id {
		return Declaration{}, fmt.Errorf("agentpreset: %s declares id %q, want %q", file, d.ID, id)
	}
	return d, nil
}
