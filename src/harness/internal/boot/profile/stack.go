package profile

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// bundles are the bundle layers compiled into the binary (DH-ADR-BOOT-2). Their
// content is DH-D-BUNDLE-1's; each is a layer file in the harness YAML dialect.
//
//go:embed bundles/*.yml
var bundles embed.FS

// Layer labels of the stack, lowest first. A bundle layer is BundleLabel+name and an
// overlay OverlayLabel+index.
const (
	BundleLabel   = "bundle:"
	ProfileLabel  = "profile"
	UserLabel     = "user"
	OverlayLabel  = "overlay:"
	LauncherLabel = "launcher"
)

// PluginsGroup is the id of the group an installed plugin's entry is inserted
// into (boot.md §6.2, "How a plugin is represented").
const PluginsGroup = "plugins"

// OptionalPrefix starts the id of an optional bundle's group: an optional bundle
// is a bundle layer whose one operation inserts one group, disabled by default,
// with an id "x.<feature>" (DH-ADR-BUNDLE-1, DH-T-BOOT-2).
const OptionalPrefix = "x."

// Files this package owns under <home>/config (boot.md §6.1).
const (
	// ProfilesDir holds one directory per profile with a layer.
	ProfilesDir = "profiles"
	// LayerFile is the name of the profile layer and of the user (home) layer.
	LayerFile = "layer.yml"
)

// LayerPath returns <home>/config/profiles/<name>/layer.yml: the profile layer and
// the one writable layer of a launch.
func LayerPath(homeRoot, name string) string {
	return filepath.Join(home.New(homeRoot).Config(), ProfilesDir, name, LayerFile)
}

// UserLayerPath returns <home>/config/layer.yml: written by a person, never by dsh.
func UserLayerPath(homeRoot string) string {
	return filepath.Join(home.New(homeRoot).Config(), LayerFile)
}

// BundleNames lists the embedded bundles, sorted.
func BundleNames() []string {
	files, _ := fs.Glob(bundles, "bundles/*.yml")
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = path.Base(f[:len(f)-len(".yml")])
	}
	return names
}

// Bundle returns the embedded bundle called name as a read-only layer labelled
// BundleLabel+name. A missing or unparsable bundle is ReasonBadBundle.
func Bundle(name string) (loader.Layer, error) {
	file := "bundles/" + name + ".yml"
	data, err := bundles.ReadFile(file)
	if err != nil {
		return loader.Layer{}, herr.Wrap(ReasonBadBundle, err, "bundle "+name)
	}
	layer, issues, err := ParseLayer(BundleLabel+name, file, data)
	if err == nil && len(issues) > 0 {
		err = &loader.ParseError{Issues: issues}
	}
	if err != nil {
		return loader.Layer{}, herr.Wrap(ReasonBadBundle, err, "bundle "+name)
	}
	return layer, nil
}

// ParseLayer decodes one layer file with the codec of its extension (YAML for .yml
// and .yaml). Empty or blank data is an empty layer. Issues are the operations that
// could not be decoded; Compose skips them.
func ParseLayer(label, file string, data []byte) (loader.Layer, []loader.Issue, error) {
	config.Register()
	layer := loader.Layer{Label: label, Ops: []loader.Op{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return layer, nil, nil
	}
	doc, err := loader.ParseLayer(nil, file, data)
	if err != nil {
		return loader.Layer{}, nil, err
	}
	layer.Ops = doc.Ops
	return layer, doc.Issues, nil
}

// ReadFile reads one file; a missing file satisfies errors.Is(err, fs.ErrNotExist).
type ReadFile func(path string) ([]byte, error)

// Sources are the inputs of a launch's stack beyond the profile.
type Sources struct {
	// Home is the resolved home root.
	Home string
	// Overlays are the --layer files, lowest first.
	Overlays []string
	// Launcher are the in-memory operations of launcher flags; the top layer.
	Launcher []loader.Op
	// Safe is launch.Data.Safe (--safe): the launcher layer then also holds SafeOps
	// of the layers below it (DH-ADR-BOOT-12).
	Safe bool
	// Read reads a layer file; nil is os.ReadFile.
	Read ReadFile
}

// Stack builds the ordered layers of one launch of def: its bundles, the profile
// layer (the one writable layer), the user layer, the overlays and the launcher
// layer. A missing profile or user layer file is an empty layer; a missing overlay
// or a file that does not parse is ReasonBadLayer. The issues are the skipped
// operations of the files read.
func Stack(def Definition, src Sources) ([]loader.Layer, []loader.Issue, error) {
	read := src.Read
	if read == nil {
		read = os.ReadFile
	}
	var layers []loader.Layer
	for _, name := range def.Bundles {
		layer, err := Bundle(name)
		if err != nil {
			return nil, nil, err
		}
		layers = append(layers, layer)
	}
	var issues []loader.Issue
	file := func(label, p string, optional, writable bool) error {
		data, err := read(p)
		if err != nil && !(optional && errors.Is(err, fs.ErrNotExist)) {
			return herr.Wrap(ReasonBadLayer, err, "read layer "+p)
		}
		layer, found, err := ParseLayer(label, p, data)
		if err != nil {
			return herr.Wrap(ReasonBadLayer, err, "layer "+p)
		}
		layer.Path, layer.Writable = p, writable
		layers = append(layers, layer)
		issues = append(issues, found...)
		return nil
	}
	if err := file(ProfileLabel, LayerPath(src.Home, def.Name), true, true); err != nil {
		return nil, nil, err
	}
	if err := file(UserLabel, UserLayerPath(src.Home), true, false); err != nil {
		return nil, nil, err
	}
	for i, p := range src.Overlays {
		if err := file(fmt.Sprintf("%s%d", OverlayLabel, i+1), p, false, false); err != nil {
			return nil, nil, err
		}
	}
	ops := []loader.Op{}
	if src.Safe {
		ops = append(ops, SafeOps(layers)...)
	}
	launcher := loader.Layer{Label: LauncherLabel, Ops: append(ops, src.Launcher...)}
	return append(layers, launcher), issues, nil
}
