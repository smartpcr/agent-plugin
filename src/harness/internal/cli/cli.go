// Package cli is the one command tree of dsh (DH-D-CLI-1, DH-D-CLI-3): the grammar,
// help and usage errors of the launcher and of every fixed command, and the mapping
// of each command's result to the result stream, the diagnostic stream and an exit
// status. cmd/dsh only calls Main.
//
// The grammar (DH-ADR-COMMANDER-2, app-cli.md §6.2.1):
//
//	dsh [launcher options] <profile> [app words...]
//	dsh [launcher options] --profile <profile> [app words...]
//	dsh [launcher options] <command> [command words...]
//
// The launcher reads its own options up to the first free word. A first word that is
// a fixed command runs it; any other word names a profile, and every later word
// belongs to the profile's app unchanged, --help included.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/cmdline"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Launch is a profile launch the launcher hands to boot.
type Launch struct {
	// Request carries the process facts and the launcher's options: Home, Profile,
	// Safe, Overlays and the app's Words.
	Request launch.Request
	// Profile is the resolved profile.
	Profile profile.Definition
	// Result is the launch's result writer; the app prints through it.
	Result *output.Result
	// Stderr is the diagnostic stream boot prints to.
	Stderr io.Writer
}

// LaunchFunc runs a profile launch to its end; its error is mapped to the exit
// status like any command's.
type LaunchFunc func(ctx context.Context, l Launch) error

// Env is everything Main reads from the process, so a test runs it in memory.
type Env struct {
	// Request holds the argument vector (program first), the build identity and the
	// other process facts.
	Request launch.Request
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	// Launch runs a profile; nil reports that launching is unavailable in this build.
	Launch LaunchFunc

	probeErr error
}

// ProcessEnv returns the Env of the running process. version, when not empty,
// replaces the module version (it is set at release time with -ldflags).
func ProcessEnv(version string) Env {
	req, err := launch.Probe()
	if err != nil {
		req = launch.Request{Args: slices.Clone(os.Args), OS: runtime.GOOS}
	}
	if version != "" {
		req.Build.Version = version
	}
	return Env{Request: req, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Launch: Boot, probeErr: err}
}

// Boot is the LaunchFunc of the process: boot.Process runs the launch with the
// process's stop signals, and its status passes through output.Exit, as boot
// already printed the launch's diagnostics.
func Boot(ctx context.Context, l Launch) error {
	return output.Exit(boot.Process(ctx, boot.Options{Request: l.Request, Profile: l.Profile, Stderr: l.Stderr}))
}

// launcherSpec is the launcher's own command line.
func launcherSpec() cmdline.Spec {
	return cmdline.Spec{
		Name:    "dsh",
		Summary: "The DeepSeek harness: launch a profile or run a fixed command.",
		Description: "Launcher options come before the profile or command name. Every word after a\n" +
			"profile name, or after --profile <name>, belongs to the profile's app, --help included.",
		Options: []cmdline.Option{
			{Name: "home", Kind: cmdline.String, Value: "dir", Help: "Harness home (default $DSH_HOME, then ~/.dsh)"},
			{Name: "profile", Kind: cmdline.String, Value: "name", Ends: true, Help: "Profile to launch; every later word goes to its app"},
			{Name: "layer", Kind: cmdline.Strings, Value: "file", Help: "Add a config layer above the user layer"},
			{Name: "safe", Kind: cmdline.Bool, Help: "Start with optional bundles and plugins off"},
			{Name: "version", Short: "V", Kind: cmdline.Bool, Help: "Print the version and exit"},
		},
		Rules:      []cmdline.Rule{{Kind: cmdline.OneOf, Names: []string{"version", "profile"}}},
		Args:       cmdline.Args{Name: "profile|command", Max: -1},
		StopAtArgs: true,
		Examples: []cmdline.Example{
			{Command: "dsh web", Help: "Start the host and open the web UI"},
			{Command: "dsh --home ./h --layer extra.yml headless \"run the tests\" --json"},
			{Command: "dsh --profile sdk", Help: "How programs start the SDK protocol"},
		},
	}
}

// state is one run of Main.
type state struct {
	env     Env
	ctx     context.Context
	res     *output.Result
	command string
	tree    *tree
	// home is the launcher's --home; access is the running command's class.
	home   string
	access access
	// helpErr is the failure of help printed by cobra's help func, which cannot
	// return it.
	helpErr error
}

// Main runs one command line and returns its exit status. It never exits the
// process and prints only to env's writers.
func Main(ctx context.Context, env Env) int {
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Request.Build.Version == "" {
		env.Request.Build.Version = launch.DevelVersion
	}
	st := &state{env: env, ctx: ctx, res: output.New(env.Stdout, env.Stderr, output.Text)}
	st.tree = newTree()
	var args []string
	if len(env.Request.Args) > 1 {
		args = env.Request.Args[1:]
	}
	err := st.run(args)
	return st.res.Fail(st.command, err)
}

func (st *state) run(args []string) error {
	if len(args) > 0 && slices.Contains(hiddenModes, args[0]) {
		st.command = args[0]
		return herr.Newf(output.ReasonUnavailable, "%s is not available in this build", args[0])
	}
	spec := launcherSpec()
	v, err := cmdline.Parse(spec, args)
	if err != nil {
		return err
	}
	if v.Help() {
		return st.printHelp(st.launcherHelp)
	}
	if v.Bool("version") {
		if len(v.Args()) > 0 {
			return cmdline.Usagef(spec.Name, "--version takes no arguments")
		}
		return st.res.Line(versionLine(st.env.Request.Build))
	}
	words := v.Args()
	name := v.String("profile")
	if name == "" && len(words) > 0 && st.tree.isCommand(words[0]) {
		for _, launchOnly := range []string{"layer", "safe"} {
			if v.Set(launchOnly) {
				return cmdline.Usagef(spec.Name, "--%s applies to a profile launch, not to %q", launchOnly, words[0])
			}
		}
		st.home = v.String("home")
		return st.runCommand(words)
	}
	if name == "" {
		if len(words) == 0 {
			return cmdline.Usagef(spec.Name, "missing <profile> or <command>")
		}
		name, words = words[0], words[1:]
	}
	st.command = name
	def, err := profile.Lookup(name)
	if err != nil {
		return err
	}
	if st.env.probeErr != nil {
		return st.env.probeErr
	}
	if st.env.Launch == nil {
		return herr.Newf(output.ReasonUnavailable, "launching profile %s is not available in this build", name)
	}
	req := st.env.Request
	req.Args = slices.Clone(req.Args)
	req.Home, req.Profile, req.Safe = v.String("home"), name, v.Bool("safe")
	req.Overlays, req.Words = v.Strings("layer"), words
	return st.env.Launch(st.ctx, Launch{Request: req, Profile: def, Result: st.res, Stderr: st.env.Stderr})
}

// versionLine is "dsh <version> (<revision>; <go>; <os>/<arch>)".
func versionLine(b launch.BuildIdentity) string {
	rev := b.Revision
	if rev == "" {
		rev = "unknown"
	} else if b.Modified {
		rev += "-modified"
	}
	goVersion := b.GoVersion
	if goVersion == "" {
		goVersion = runtime.Version()
	}
	return fmt.Sprintf("dsh %s (%s; %s; %s/%s)", b.Version, rev, goVersion, runtime.GOOS, runtime.GOARCH)
}

// launcherHelp writes the launcher's help: its options, the fixed commands and the
// built-in profiles.
func (st *state) launcherHelp(w io.Writer) error {
	return cmdline.Help(w, launcherSpec(), st.tree.commandSection(), profileSection())
}

// printHelp renders help through the result writer, so a failed write to stdout is
// ReasonClosed like any other result.
func (st *state) printHelp(render func(io.Writer) error) error {
	var buf bytes.Buffer
	if err := render(&buf); err != nil {
		return err
	}
	return st.res.Text(buf.String())
}

func profileSection() cmdline.Section {
	s := cmdline.Section{Title: "Profiles"}
	for _, d := range profile.Builtins() {
		s.Rows = append(s.Rows, [2]string{d.Name, d.Summary})
	}
	return s
}

// profileHelp is `dsh help <profile>`: the launch line and the bundles. The app's
// own options are its component's, shown by `dsh <profile> --help`.
func profileHelp(w io.Writer, d profile.Definition) error {
	_, err := fmt.Fprintf(w, "Usage: dsh [launcher options] %s [app words...]\n\n%s\n\nBundles: %s\n\n"+
		"Run 'dsh %s --help' for the app's options.\n", d.Name, d.Summary, strings.Join(d.Bundles, ", "), d.Name)
	return err
}
