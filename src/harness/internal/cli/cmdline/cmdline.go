// Package cmdline is how a command line is declared in dsh (DH-ADR-COMMANDER-1,
// DH-LIB-COMMANDER-R3..R6): a Spec names the options, the rules between them, the
// free arguments and the examples; Parse reads a word list against it on a private
// pflag set and returns the values, a help request or a *UsageError; Help renders
// the Spec. Nothing is registered globally, and Parse never prints and never exits.
package cmdline

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// ReasonUsage is the code of every usage error (status 64).
const ReasonUsage herr.Reason = "cli/usage"

// Kind is an option's kind.
type Kind int

const (
	// Bool is an on/off switch; --name=false turns it off.
	Bool Kind = iota
	// String takes one value.
	String
	// Int takes one whole number.
	Int
	// Strings takes one value and may repeat; the values are kept in order.
	Strings
	// Choice takes one value from Choices.
	Choice
)

// Option is one option of a Spec.
type Option struct {
	// Name is the long form, without dashes.
	Name string
	// Short is the single-letter form, or "".
	Short string
	Kind  Kind
	// Default is the value when the option is not given, in text form.
	Default string
	// Choices are the values a Choice accepts.
	Choices []string
	// Value names the value in help ("dir", "file"); "" is "value".
	Value string
	// Help is the one-line description.
	Help string
	// Hidden keeps the option out of help.
	Hidden bool
	// Ends ends the options after this option's value: every later word is a free
	// word, passed on unchanged even when it looks like an option of the Spec.
	Ends bool
}

// RuleKind is the kind of a rule between options.
type RuleKind int

const (
	// OneOf allows at most one of the options.
	OneOf RuleKind = iota
	// Together requires all of the options or none.
	Together
)

// Rule is a declared rule between options.
type Rule struct {
	Kind  RuleKind
	Names []string
}

// Args is the free-argument rule: between Min and Max words (Max < 0 is unbounded).
// The zero value takes no free words.
type Args struct {
	// Name names the words in the usage line ("command", "shell", "word").
	Name string
	Help string
	Min  int
	Max  int
	// Extra, when set, is the usage error's message for words beyond Max, which
	// it then does not quote: they may be a secret typed in the wrong place.
	Extra string
}

// Example is one example of the help text.
type Example struct {
	Command string
	Help    string
}

// Spec declares one command line.
type Spec struct {
	// Name is the full invocation, such as "dsh catalog commands".
	Name string
	// Summary is the one-line description.
	Summary string
	// Description is the longer text of help; "" leaves it out.
	Description string
	Options     []Option
	Rules       []Rule
	Args        Args
	Examples    []Example
	// StopAtArgs ends the options at the first free word: every later word is a free
	// word, as the launcher stage needs (DH-ADR-COMMANDER-2).
	StopAtArgs bool
	// Sensitive marks a command whose words may hold a secret typed in the wrong
	// place (M2-C-11): its usage errors quote no word, neither an option's name nor
	// its value.
	Sensitive bool
}

// HelpOption is the -h/--help switch every Spec has.
var HelpOption = Option{Name: "help", Short: "h", Kind: Bool, Help: "Show this help"}

// Values are the result of a successful Parse.
type Values struct {
	set     map[string]bool
	values  map[string]string
	lists   map[string][]string
	args    []string
	help    bool
	options map[string]Option
}

// Help reports a help request: -h or --help was given. The other values are not
// checked then.
func (v *Values) Help() bool { return v.help }

// Args returns the free words.
func (v *Values) Args() []string { return slices.Clone(v.args) }

// Set reports whether the option was given.
func (v *Values) Set(name string) bool { return v.set[name] }

// String returns a String or Choice option's value.
func (v *Values) String(name string) string { return v.values[name] }

// Bool returns a Bool option's value.
func (v *Values) Bool(name string) bool { return v.values[name] == "true" }

// Int returns an Int option's value.
func (v *Values) Int(name string) int {
	n, _ := strconv.Atoi(v.values[name])
	return n
}

// Strings returns a Strings option's values in order.
func (v *Values) Strings(name string) []string { return slices.Clone(v.lists[name]) }

// UsageError is a word list that does not fit the Spec.
type UsageError struct {
	// Command is the Spec's Name.
	Command string
	// Option names the option at fault, without dashes; "" when none.
	Option  string
	Message string
}

func (e *UsageError) Error() string { return string(ReasonUsage) + ": " + e.Message }

// Reason returns the code, so herr.Kind classifies the error.
func (e *UsageError) Reason() string { return string(ReasonUsage) }

// Usagef returns a *UsageError for the command.
func Usagef(command, format string, args ...any) *UsageError {
	return &UsageError{Command: command, Message: fmt.Sprintf(format, args...)}
}

// choiceValue is a pflag value limited to choices.
type choiceValue struct {
	value   string
	choices []string
}

func (c *choiceValue) String() string { return c.value }
func (c *choiceValue) Type() string   { return "string" }
func (c *choiceValue) Set(s string) error {
	if !slices.Contains(c.choices, s) {
		return fmt.Errorf("must be one of %s", strings.Join(c.choices, ", "))
	}
	c.value = s
	return nil
}

// Parse reads words against spec. It returns a *UsageError for an unknown option, a
// bad value, a broken rule or a wrong number of free words.
func Parse(spec Spec, words []string) (*Values, error) {
	fs := pflag.NewFlagSet(spec.Name, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	fs.SetInterspersed(!spec.StopAtArgs)
	fs.SortFlags = false

	options := map[string]Option{}
	var help bool
	strs := map[string]*string{}
	bools := map[string]*bool{}
	ints := map[string]*int{}
	lists := map[string]*[]string{}
	choices := map[string]*choiceValue{}
	for _, o := range append(slices.Clone(spec.Options), HelpOption) {
		options[o.Name] = o
		switch o.Kind {
		case Bool:
			if o.Name == HelpOption.Name {
				fs.BoolVarP(&help, o.Name, o.Short, false, o.Help)
				continue
			}
			bools[o.Name] = fs.BoolP(o.Name, o.Short, o.Default == "true", o.Help)
		case String:
			strs[o.Name] = fs.StringP(o.Name, o.Short, o.Default, o.Help)
		case Int:
			def, _ := strconv.Atoi(o.Default)
			ints[o.Name] = fs.IntP(o.Name, o.Short, def, o.Help)
		case Strings:
			lists[o.Name] = fs.StringArrayP(o.Name, o.Short, nil, o.Help)
		case Choice:
			c := &choiceValue{value: o.Default, choices: o.Choices}
			choices[o.Name] = c
			fs.VarP(c, o.Name, o.Short, o.Help)
		}
	}
	head, tail := words, []string(nil)
	if cut := endOfOptions(spec, words); cut < len(words) {
		head, tail = words[:cut], words[cut:]
	}
	if err := fs.Parse(head); err != nil {
		if spec.Sensitive {
			return nil, sensitiveUsage(spec.Name, err)
		}
		return nil, usageFromFlag(spec.Name, err)
	}
	v := &Values{set: map[string]bool{}, values: map[string]string{}, lists: map[string][]string{},
		args: append(fs.Args(), tail...), help: help, options: options}
	if help {
		return v, nil
	}
	fs.Visit(func(f *pflag.Flag) { v.set[f.Name] = true })
	for name, p := range strs {
		v.values[name] = *p
	}
	for name, p := range bools {
		v.values[name] = strconv.FormatBool(*p)
	}
	for name, p := range ints {
		v.values[name] = strconv.Itoa(*p)
	}
	for name, p := range choices {
		v.values[name] = p.value
	}
	for name, p := range lists {
		v.lists[name] = slices.Clone(*p)
	}
	for _, r := range spec.Rules {
		var given []string
		for _, n := range r.Names {
			if v.set[n] {
				given = append(given, "--"+n)
			}
		}
		switch {
		case r.Kind == OneOf && len(given) > 1:
			return nil, &UsageError{Command: spec.Name, Option: strings.TrimPrefix(given[1], "--"),
				Message: fmt.Sprintf("%s cannot be used together", strings.Join(given, " and "))}
		case r.Kind == Together && len(given) > 0 && len(given) < len(r.Names):
			return nil, &UsageError{Command: spec.Name, Message: fmt.Sprintf("%s must be given together", dashed(r.Names))}
		}
	}
	if n := len(v.args); n < spec.Args.Min || spec.Args.Max >= 0 && n > spec.Args.Max {
		return nil, argsError(spec, v.args)
	}
	return v, nil
}

func argsError(spec Spec, args []string) *UsageError {
	a := spec.Args
	switch {
	case len(args) < a.Min:
		return Usagef(spec.Name, "missing <%s>", argName(a))
	case a.Extra != "":
		return Usagef(spec.Name, "%s", a.Extra)
	case a.Max == 0:
		return Usagef(spec.Name, "unexpected argument %q", args[0])
	default:
		return Usagef(spec.Name, "unexpected argument %q (at most %d <%s>)", args[a.Max], a.Max, argName(a))
	}
}

func argName(a Args) string {
	if a.Name == "" {
		return "argument"
	}
	return a.Name
}

func dashed(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "--" + n
	}
	return strings.Join(out, ", ")
}

// usageFromFlag turns pflag's error into a UsageError naming the option.
func usageFromFlag(command string, err error) *UsageError {
	msg := err.Error()
	var notExist *pflag.NotExistError
	var valueErr *pflag.InvalidValueError
	var missing *pflag.ValueRequiredError
	ue := &UsageError{Command: command, Message: msg}
	switch {
	case errors.As(err, &notExist):
		ue.Option = notExist.GetSpecifiedName()
	case errors.As(err, &valueErr):
		ue.Option = valueErr.GetFlag().Name
	case errors.As(err, &missing):
		ue.Option = missing.GetFlag().Name
	}
	return ue
}

// sensitiveUsage turns pflag's error into a UsageError that quotes no word: an
// unknown option is not named (its name may be the secret) and an option's bad
// value is not shown; a declared option is named by its declaration.
func sensitiveUsage(command string, err error) *UsageError {
	var notExist *pflag.NotExistError
	var valueErr *pflag.InvalidValueError
	var missing *pflag.ValueRequiredError
	switch {
	case errors.As(err, &notExist):
		return &UsageError{Command: command, Message: "unknown option (not repeated: it may be a secret)"}
	case errors.As(err, &valueErr):
		name := valueErr.GetFlag().Name
		return &UsageError{Command: command, Option: name, Message: "invalid value for --" + name + " (not repeated: it may be a secret)"}
	case errors.As(err, &missing):
		name := missing.GetFlag().Name
		return &UsageError{Command: command, Option: name, Message: "--" + name + " needs a value"}
	default:
		return &UsageError{Command: command, Message: "malformed option (not repeated: it may be a secret)"}
	}
}

// scan walks the options of words as Parse reads them, calling visit with the index
// past each option (and its value) and the option's inline value, if any. It stops
// at "--", at the first free word under StopAtArgs, or when visit returns false.
// Unknown options are passed over: Parse reports them.
func scan(spec Spec, words []string, visit func(next int, o Option, value string, inline bool) bool) {
	long := map[string]Option{}
	short := map[string]Option{}
	for _, o := range append(slices.Clone(spec.Options), HelpOption) {
		long[o.Name] = o
		if o.Short != "" {
			short[o.Short] = o
		}
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w == "--" {
			return
		}
		if len(w) < 2 || w[0] != '-' {
			if spec.StopAtArgs {
				return
			}
			continue
		}
		if strings.HasPrefix(w, "--") {
			name, value, inline := strings.Cut(w[2:], "=")
			o, ok := long[name]
			if !ok {
				continue
			}
			if o.Kind != Bool && !inline {
				i++
			}
			if !visit(i+1, o, value, inline) {
				return
			}
			continue
		}
		for j, r := range w[1:] {
			o, ok := short[string(r)]
			if !ok {
				break
			}
			if o.Kind != Bool {
				rest := w[2+j:]
				if rest == "" {
					i++
				}
				if !visit(i+1, o, rest, rest != "") {
					return
				}
				break
			}
			if !visit(i+1, o, "", false) {
				return
			}
		}
	}
}

// endOfOptions returns the index of the first word after an Ends option's value, or
// len(words) when no Ends option is given.
func endOfOptions(spec Spec, words []string) int {
	end := len(words)
	scan(spec, words, func(next int, o Option, _ string, _ bool) bool {
		if o.Ends {
			end = min(next, len(words))
			return false
		}
		return true
	})
	return end
}

// Given reports whether the Bool option name is switched on in words, read as Parse
// reads them. It answers even when Parse fails, so a caller can honour a switch such
// as --json while it reports the usage error.
func Given(spec Spec, words []string, name string) bool {
	on := false
	words = words[:endOfOptions(spec, words)]
	scan(spec, words, func(_ int, o Option, value string, inline bool) bool {
		if o.Name == name && o.Kind == Bool {
			b, err := strconv.ParseBool(value)
			on = !inline || err == nil && b
		}
		return true
	})
	return on
}
