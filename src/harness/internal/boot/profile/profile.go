// Package profile is what a profile is in Go (DH-D-BOOT-3, DH-ADR-BOOT-1,
// DH-ADR-BOOT-2): a compiled-in definition (name, ordered bundle names, limits and
// the Required list), bundle files embedded in the binary with embed.FS and parsed
// by the YAML codec like any layer, and the layer stack of one launch built on the
// loader's layers:
//
//	bundle layers (in the profile's order) < profile layer (the one writable layer)
//	< user layer (the home layer) < overlays (--layer, in order) < launcher layer
//
// The bundles of a launchable profile begin with the kernel, admin.yml then core.yml
// (DH-D-BUNDLE-2). A layer is written with three operations, insert, merge and
// remove (Insert, Merge, Remove), plus the field override of the loader. Later
// layers win; the loader's ownership index names the layer that supplied each
// field, and Preview returns it with the composed tree for `dsh config show`.
package profile

import (
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Error codes of this package.
const (
	// ReasonUnknown: no profile has the name (status 5).
	ReasonUnknown herr.Reason = "profile/unknown"
	// ReasonBadLayer: a layer file is unreadable or does not parse (status 78).
	ReasonBadLayer herr.Reason = "profile/bad-layer"
	// ReasonBadBundle: an embedded bundle is missing or does not parse; a build
	// defect that the bundle tests keep out of a release.
	ReasonBadBundle herr.Reason = "profile/bad-bundle"
	// ReasonMissingRequired: a composed profile lacks an entry of its Required list,
	// or holds it disabled.
	ReasonMissingRequired herr.Reason = "profile/missing-required"
)

// Limits are a profile's time bounds (boot.md §6.2; tech-spec §5.5).
type Limits struct {
	// StartupTimeout bounds "apply and settle".
	StartupTimeout time.Duration
	// ShutdownTimeout is the one exit bound; past it the process exits 124.
	ShutdownTimeout time.Duration
	// FatalReleaseTimeout bounds the release hook of the fatal path.
	FatalReleaseTimeout time.Duration
	// CleanupWarnAfter is when the watchdog names a cleanup that has not returned.
	CleanupWarnAfter time.Duration
}

// DefaultLimits are the limits of every built-in profile.
func DefaultLimits() Limits {
	return Limits{
		StartupTimeout:      60 * time.Second,
		ShutdownTimeout:     20 * time.Second,
		FatalReleaseTimeout: 2 * time.Second,
		CleanupWarnAfter:    10 * time.Second,
	}
}

// Definition is one profile.
type Definition struct {
	// Name is how the profile is selected: `dsh <name>` or `dsh --profile <name>`.
	Name string
	// Summary is the one-line description of the command reference.
	Summary string
	// Bundles are the bundle names whose layers come first, in this order.
	Bundles []string
	// Limits are the profile's time bounds.
	Limits Limits
	// Required are the entry ids that must be present, enabled and active at the
	// boot audit.
	Required []string
	// Reload is how often the layer files are polled once the app is ready, a
	// changed and valid tree then applied while it runs (DH-D-BOOT-7); zero turns
	// reload off: a change applies at the next start.
	Reload time.Duration
}

// DefaultReload is the layer poll of a profile that reloads (web).
const DefaultReload = 500 * time.Millisecond

// Built-in profile names (DH-ADR-BOOT-1).
const (
	Web        = "web"
	Headless   = "headless"
	SDK        = "sdk"
	SDKMinimal = "sdk-minimal"
	ACP        = "acp"
)

// Names of the two compositions that are not launched by name (bundle-preset.md
// §6.2): Admin is what the offline admin commands run in (A24, DH-ADR-CLI-2) and has
// no required entry; Core is the kernel every launchable profile starts with.
const (
	Admin = "admin"
	Core  = "core"
)

// Spine are the entry ids every launchable profile requires (DH-D-BUNDLE-1): the
// store, the agent registry and driver, the tool registry and the preset registry.
// Each Required list is Spine followed by the profile's carrier set.
var Spine = []string{"session.store", "agent.registry", "agent.driver", "tools.registry", "agent.presets"}

// Carrier sets: the carrier entry of a profile and the entries it cannot serve
// without (bundle-preset.md §6.2).
var (
	webCarrier      = []string{"host.listen", "host.auth", "host.assets", "host.web", "host.access", "host.stream", "ui.shell"}
	headlessCarrier = []string{"cli.headless"}
	sdkCarrier      = []string{"wire.stdio", "wire.sdk"}
)

// actingPrefixes are the component-name prefixes of what acts: agents, model
// adapters, carriers and plugin processes.
var actingPrefixes = []string{"agent.", "llm.", "wire.", "cli.", "plugin.", "boot.plugins", "subagent."}

// Acting reports whether the entry of id and component name is a carrier, an
// agent, a model adapter or a plugin process: what the admin composition never
// mounts (INV-10, M2-C-14).
func Acting(id, name string) bool {
	for _, set := range [][]string{webCarrier, headlessCarrier, sdkCarrier, {"wire.acp"}} {
		if slices.Contains(set, id) || slices.Contains(set, name) {
			return true
		}
	}
	return slices.ContainsFunc(actingPrefixes, func(p string) bool { return strings.HasPrefix(name, p) })
}

// required is Spine followed by carrier.
func required(carrier []string) []string {
	return append(slices.Clone(Spine), carrier...)
}

// kernel are the bundle files of the core kernel (bundle-preset.md §6.2): the Go
// bundle core is admin.yml then core.yml, each its own layer so the ownership index
// names the file that supplied a value.
var kernel = []string{Admin, Core}

// onKernel is the kernel followed by the bundles above it.
func onKernel(above ...string) []string { return append(slices.Clone(kernel), above...) }

// builtins are the compiled-in profiles, stacked as bundle-preset.md §6.2 orders:
// the kernel, base (every P0 profile but sdk-minimal) and the mode layer; presets
// joins them with DH-D-BUNDLE-4. There is no bundle named sdk-minimal: that profile
// stacks the kernel, minimal and sdk (DH-ADR-BOOT-1). sdk-minimal and acp are not
// among the five definitions of DH-D-BUNDLE-1. sdk-minimal inherits the sdk bundle,
// carriers included, so it requires what sdk requires; acp keeps its M1 content.
var builtins = []Definition{
	{Name: Web, Summary: "Start the host and open the web UI", Bundles: onKernel("base", "web"), Reload: DefaultReload, Required: required(webCarrier)},
	{Name: Headless, Summary: "Run one task without a UI and print the answer", Bundles: onKernel("base", "headless"), Required: required(headlessCarrier)},
	{Name: SDK, Summary: "Serve the SDK protocol on standard input and output", Bundles: onKernel("base", "sdk"), Required: required(sdkCarrier)},
	{Name: SDKMinimal, Summary: "Serve the SDK protocol with the minimal agent preset", Bundles: onKernel("minimal", "sdk"), Required: required(sdkCarrier)},
	{Name: ACP, Summary: "Serve the Agent Client Protocol on standard input and output", Bundles: onKernel("base", "acp")},
}

// compositions are the definitions that are not launched by name.
var compositions = []Definition{
	{Name: Admin, Summary: "The composition the offline admin commands run in", Bundles: []string{"admin"}},
	{Name: Core, Summary: "The kernel every launchable profile starts with", Bundles: onKernel(), Required: slices.Clone(Spine)},
}

// Builtins returns copies of the launchable built-in profiles in a fixed order.
func Builtins() []Definition { return clone(builtins) }

// Definitions returns copies of the five profile definitions of DH-D-BUNDLE-1, in
// this order: admin, the core kernel, web, headless and sdk. Only the last three are
// launched by name (Lookup).
func Definitions() []Definition {
	five := slices.Clone(compositions)
	for _, name := range []string{Web, Headless, SDK} {
		five = append(five, builtinNamed(name))
	}
	return clone(five)
}

// builtinNamed returns the entry of builtins called name. The names are compiled-in
// constants, so a miss is a programming error.
func builtinNamed(name string) Definition {
	for _, d := range builtins {
		if d.Name == name {
			return d
		}
	}
	panic("profile: no built-in profile " + name)
}

func clone(defs []Definition) []Definition {
	out := make([]Definition, len(defs))
	for i, d := range defs {
		d.Bundles = slices.Clone(d.Bundles)
		d.Required = slices.Clone(d.Required)
		d.Limits = DefaultLimits()
		out[i] = d
	}
	return out
}

// Names returns the built-in profile names in the order of Builtins.
func Names() []string {
	names := make([]string, len(builtins))
	for i, d := range builtins {
		names[i] = d.Name
	}
	return names
}

// Lookup returns the built-in profile called name; an unknown name is
// ReasonUnknown, naming the known profiles.
func Lookup(name string) (Definition, error) {
	for _, d := range Builtins() {
		if d.Name == name {
			return d, nil
		}
	}
	return Definition{}, herr.Newf(ReasonUnknown, "no profile %q (profiles: %s)", name, strings.Join(Names(), ", "))
}
