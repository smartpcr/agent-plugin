package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/cmdline"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
)

// hiddenModes are private modes of the binary: dispatched on the exact first word
// before any parsing, absent from help and completion, reserved as profile names.
var hiddenModes = []string{"sandbox-launch", "ssh-helper"}

// command is one fixed command of the tree. A command with children is a group:
// it only routes.
type command struct {
	spec     cmdline.Spec
	children []*command
	run      func(st *state, v *cmdline.Values) error
	// access is the command's class: an admin command runs in the admin tree
	// (DH-D-CLI-9), read-only or writing.
	access access
}

// word is the command's own word: the last word of its Spec name.
func (c *command) word() string {
	words := strings.Fields(c.spec.Name)
	return words[len(words)-1]
}

// tree is the fixed command tree.
type tree struct {
	commands []*command
}

func jsonOption() cmdline.Option {
	return cmdline.Option{Name: "json", Kind: cmdline.Bool, Help: "Print the result as one JSON value"}
}

func newTree() *tree {
	return &tree{commands: []*command{
		{
			spec: cmdline.Spec{Name: "dsh help", Summary: "Show the help of a command or a profile",
				Args:     cmdline.Args{Name: "command", Help: "A command path or a profile name", Max: -1},
				Examples: []cmdline.Example{{Command: "dsh help catalog commands"}}},
			run: runHelp,
		},
		{
			spec: cmdline.Spec{Name: "dsh version", Summary: "Print the version of this build",
				Options: []cmdline.Option{jsonOption()}},
			run: runVersion,
		},
		{
			spec: cmdline.Spec{Name: "dsh catalog", Summary: "Print the generated catalogs of this build"},
			children: []*command{{
				spec: cmdline.Spec{Name: "dsh catalog commands", Summary: "Print the command reference",
					Options:  []cmdline.Option{jsonOption()},
					Examples: []cmdline.Example{{Command: "dsh catalog commands > reference.md"}}},
				run: runReference,
			}},
		},
		{
			spec: cmdline.Spec{Name: "dsh completion", Summary: "Print a shell completion script",
				Args: cmdline.Args{Name: "shell", Help: strings.Join(shells, ", "), Min: 1, Max: 1},
				Examples: []cmdline.Example{
					{Command: "dsh completion bash > /etc/bash_completion.d/dsh"},
					{Command: "dsh completion powershell | Out-String | Invoke-Expression"},
				}},
			run: runCompletion,
		},
		{
			spec: cmdline.Spec{Name: "dsh secret", Summary: "Manage the stored secrets"},
			children: []*command{{
				spec: cmdline.Spec{Name: "dsh secret set", Summary: "Store a secret read from standard input",
					Description: "The value is read from standard input, without one trailing line end; it is\n" +
						"never an argument and never printed. <name> is OWNER_NAME or secret://owner/name.",
					Options: []cmdline.Option{jsonOption()},
					Args: cmdline.Args{Name: "name", Help: "The secret's name, such as DEEPSEEK_API_KEY", Min: 1, Max: 1,
						Extra: "a secret value is read from standard input, never from an argument"},
					Sensitive: true,
					Examples: []cmdline.Example{
						{Command: "printf %s \"$KEY\" | dsh secret set DEEPSEEK_API_KEY"},
						{Command: "Get-Content key.txt | dsh secret set secret://deepseek/api-key"},
					}},
				run:    runSecretSet,
				access: adminWrite,
			}},
		},
		{
			spec: cmdline.Spec{Name: "dsh sandbox", Summary: "Show how commands are confined on this machine"},
			children: []*command{{
				spec: cmdline.Spec{Name: "dsh sandbox status", Summary: "Print the confinement a command gets now",
					Description: "Probes the sandbox backends and prints Full, Partial or Unconfined, the\n" +
						"backend, the effects it does not govern and the exposures. Writes nothing to the home.",
					Options: []cmdline.Option{jsonOption()}},
				run:    runSandboxStatus,
				access: adminRead,
			}},
		},
		{
			spec: cmdline.Spec{Name: "dsh config", Summary: "Preview and check the composed configuration"},
			children: []*command{
				{
					spec: cmdline.Spec{Name: "dsh config show", Summary: "Print a profile's composed tree and the layer of each value",
						Description: "Composes the profile's layers as a launch would and prints every entry and\n" +
							"value with the layer that supplied it, then the issues. Starts no component\n" +
							"and writes nothing to the home.",
						Options: []cmdline.Option{profileOption(),
							{Name: "defaults", Kind: cmdline.Bool, Help: "Leave the profile layer out"}, jsonOption()},
						Args: cmdline.Args{Name: "profile", Help: "The profile, as an argument instead of --profile", Max: 1},
						Examples: []cmdline.Example{
							{Command: "dsh config show --profile headless"},
							{Command: "dsh --home ./h config show web --defaults --json"},
						}},
					run:    runConfigShow,
					access: adminRead,
				},
				{
					spec: cmdline.Spec{Name: "dsh config validate", Summary: "Check the configuration of one or every profile",
						Description: "Checks the composition of the profile, or of every built-in profile, with\n" +
							"each <file> as a layer above the user layer. An issue exits 78 and names its\n" +
							"file, line and field. Starts no component and writes nothing to the home.",
						Options: []cmdline.Option{profileOption(), jsonOption()},
						Args:    cmdline.Args{Name: "file", Help: "A layer file to check above the user layer", Max: -1},
						Examples: []cmdline.Example{
							{Command: "dsh config validate"},
							{Command: "dsh config validate --profile web extra.yml"},
						}},
					run:    runConfigValidate,
					access: adminRead,
				},
			},
		},
	}}
}

// walk visits every command depth first, in tree order.
func (t *tree) walk(visit func(c *command)) {
	var rec func([]*command)
	rec = func(cs []*command) {
		for _, c := range cs {
			visit(c)
			rec(c.children)
		}
	}
	rec(t.commands)
}

// find returns the command at the path of words, or nil.
func (t *tree) find(path []string) *command {
	cs := t.commands
	var found *command
	for _, w := range path {
		i := slices.IndexFunc(cs, func(c *command) bool { return c.word() == w })
		if i < 0 {
			return nil
		}
		found = cs[i]
		cs = found.children
	}
	return found
}

func (t *tree) isCommand(word string) bool {
	return t.find([]string{word}) != nil || word == cobra.ShellCompRequestCmd || word == cobra.ShellCompNoDescRequestCmd
}

// Reserved returns the words no profile may be called: every top-level command,
// the hidden modes and the completion helpers.
func Reserved() []string {
	words := slices.Clone(hiddenModes)
	for _, c := range newTree().commands {
		words = append(words, c.word())
	}
	words = append(words, cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd)
	slices.Sort(words)
	return words
}

func (t *tree) commandSection() cmdline.Section {
	return childSection("Commands", t.commands)
}

func childSection(title string, cs []*command) cmdline.Section {
	s := cmdline.Section{Title: title}
	for _, c := range cs {
		s.Rows = append(s.Rows, [2]string{c.word(), c.spec.Summary})
	}
	return s
}

func (c *command) help(w io.Writer) error {
	if len(c.children) > 0 {
		spec := c.spec
		spec.Args = cmdline.Args{Name: "command", Min: 1, Max: -1}
		return cmdline.Help(w, spec, childSection("Commands", c.children))
	}
	return cmdline.Help(w, c.spec)
}

// ranError marks an error returned by a command's own run, so runCommand can tell
// it from cobra's routing errors, which are usage errors.
type ranError struct{ err error }

func (e ranError) Error() string { return e.err.Error() }
func (e ranError) Unwrap() error { return e.err }

func ran(err error) error {
	if err == nil {
		return nil
	}
	return ranError{err}
}

// cobraTree builds the cobra commands of the tree. cobra routes the words to a
// command, nests groups and generates completion; options are parsed by cmdline
// from the command's Spec, so help and usage errors have one form.
func (st *state) cobraTree() *cobra.Command {
	root := &cobra.Command{
		Use:                "dsh",
		Short:              launcherSpec().Summary,
		SilenceErrors:      true,
		SilenceUsage:       true,
		DisableSuggestions: true,
		CompletionOptions:  cobra.CompletionOptions{DisableDefaultCmd: true},
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			return profile.Names(), cobra.ShellCompDirectiveNoFileComp
		},
	}
	root.SetIn(st.env.Stdin)
	root.SetOut(st.env.Stdout)
	root.SetErr(st.env.Stderr)
	byPath := map[string]*command{}
	var add func(parent *cobra.Command, c *command)
	add = func(parent *cobra.Command, c *command) {
		byPath[c.spec.Name] = c
		cc := &cobra.Command{Use: c.word(), Short: c.spec.Summary}
		if len(c.children) > 0 {
			cc.Args = cobra.NoArgs
			// A bare group lacks its required <command>: the help goes to the
			// diagnostic stream and the run is a usage error. --help is handled by
			// the help func and stays status 0 on the result stream.
			cc.RunE = func(*cobra.Command, []string) error {
				st.command = strings.TrimPrefix(c.spec.Name, "dsh ")
				var buf bytes.Buffer
				if err := c.help(&buf); err != nil {
					return ran(err)
				}
				_, _ = io.Copy(st.env.Stderr, &buf)
				return ran(cmdline.Usagef(c.spec.Name, "missing <command>"))
			}
		} else {
			cc.DisableFlagParsing = true
			cc.RunE = func(cc *cobra.Command, args []string) error {
				st.command = strings.TrimPrefix(cc.CommandPath(), "dsh ")
				useJSON := func() { st.res = output.New(st.env.Stdout, st.env.Stderr, output.JSON) }
				v, err := cmdline.Parse(c.spec, args)
				if err != nil {
					if cmdline.Given(c.spec, args, "json") {
						useJSON()
					}
					return ran(err)
				}
				if v.Help() {
					return ran(st.printHelp(c.help))
				}
				if v.Bool("json") {
					useJSON()
				}
				st.access = c.access
				return ran(c.run(st, v))
			}
		}
		for _, child := range c.children {
			add(cc, child)
		}
		if c.word() == "help" {
			parent.SetHelpCommand(cc)
			return
		}
		parent.AddCommand(cc)
	}
	for _, c := range st.tree.commands {
		add(root, c)
	}
	root.SetHelpFunc(func(cc *cobra.Command, _ []string) {
		if c := byPath[cc.CommandPath()]; c != nil {
			st.helpErr = st.printHelp(c.help)
			return
		}
		st.helpErr = st.printHelp(st.launcherHelp)
	})
	return root
}

// runCommand runs the fixed command named by the first of words.
func (st *state) runCommand(words []string) error {
	st.command = words[0]
	root := st.cobraTree()
	root.SetArgs(words)
	cc, err := root.ExecuteContextC(st.ctx)
	var r ranError
	if errors.As(err, &r) {
		return r.err
	}
	if err != nil {
		if cc != nil {
			st.command = strings.TrimPrefix(cc.CommandPath(), "dsh")
		}
		return cmdline.Usagef(st.command, "%s", err.Error())
	}
	return st.helpErr
}

func runHelp(st *state, v *cmdline.Values) error {
	path := v.Args()
	if len(path) == 0 {
		return st.printHelp(st.launcherHelp)
	}
	if c := st.tree.find(path); c != nil {
		return st.printHelp(c.help)
	}
	if len(path) == 1 {
		if d, err := profile.Lookup(path[0]); err == nil {
			return st.printHelp(func(w io.Writer) error { return profileHelp(w, d) })
		}
	}
	return cmdline.Usagef("dsh help", "no command or profile %q", strings.Join(path, " "))
}

// versionDoc is the JSON form of `dsh version --json`; its fields are part of the
// contract.
type versionDoc struct {
	Version   string `json:"version"`
	Revision  string `json:"revision,omitempty"`
	Time      string `json:"time,omitempty"`
	Modified  bool   `json:"modified"`
	GoVersion string `json:"go"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

func runVersion(st *state, _ *cmdline.Values) error {
	b := st.env.Request.Build
	if st.res.JSON() {
		doc := versionDoc{Version: b.Version, Revision: b.Revision, Time: b.Time, Modified: b.Modified, GoVersion: b.GoVersion}
		doc.OS, doc.Arch = runtime.GOOS, runtime.GOARCH
		return st.res.Value(doc)
	}
	return st.res.Line(versionLine(b))
}

func runReference(st *state, _ *cmdline.Values) error {
	if st.res.JSON() {
		return st.res.Value(ReferenceDoc())
	}
	return st.res.Text(Reference())
}

// shells are the shells `dsh completion` writes a script for.
var shells = []string{"bash", "zsh", "fish", "powershell"}

func runCompletion(st *state, v *cmdline.Values) error {
	shell := v.Args()[0]
	root := st.cobraTree()
	var b bytes.Buffer
	var err error
	switch shell {
	case "bash":
		err = root.GenBashCompletionV2(&b, true)
	case "zsh":
		err = root.GenZshCompletion(&b)
	case "fish":
		err = root.GenFishCompletion(&b, true)
	case "powershell":
		err = root.GenPowerShellCompletionWithDesc(&b)
	default:
		return cmdline.Usagef("dsh completion", "unknown shell %q (one of %s)", shell, strings.Join(shells, ", "))
	}
	if err != nil {
		return fmt.Errorf("generate %s completion: %w", shell, err)
	}
	return st.res.Text(b.String())
}
