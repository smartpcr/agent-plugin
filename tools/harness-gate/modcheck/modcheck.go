// Package modcheck holds the repository's module pin rules: exactly three product
// modules (src/go, src/sdk, src/harness) on one go floor, no replace directive, an
// src/sdk and an src/go that require nothing, and a reason comment on every pin
// (RM-1..RM-4, RM-10; DH-T-TOOLCHAIN-11, DH-T-SDK-17).
//
// Three classes of module are read:
//
//   - the product modules of RM-1 (Modules), the only ones go.work uses;
//   - test modules: a nested test-only module at <product module>/test/e2e, such as
//     src/go/test/e2e (M1-D-TOOL-4 (A)). It is outside the RM-1 count and go.work, is
//     never released, may carry a replace onto its product module, and still needs a
//     reason on every pin;
//   - tools modules: tools/<name>/go.mod, such as tools/harness-gate, the tools module
//     of M1-D-TOOL-2 (a). They are outside go.work and need a reason on every pin.
//
// go.mod and go.work files are read with a small line parser of their own, so the
// package stays standard-library only and checks the exact committed bytes.
package modcheck

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Floor is the single go line every product module and the workspace state (RM-2).
const Floor = "1.27.4"

// Module is one product module of RM-1.
type Module struct {
	Dir  string // slash-separated, relative to the repository root
	Path string // module path
}

// Modules are the three product modules of RM-1, in dependency order.
var Modules = []Module{
	{Dir: "src/go", Path: "github.com/smartpcr/agent-plugin/src/go"},
	{Dir: "src/sdk", Path: "github.com/smartpcr/agent-plugin/src/sdk"},
	{Dir: "src/harness", Path: "github.com/smartpcr/agent-plugin/src/harness"},
}

// Directive is one directive of a go.mod or go.work file. A directive inside a block
// ("require ( ... )") carries the block's verb.
type Directive struct {
	File string // name the directive is reported under, e.g. "src/sdk/go.mod"
	Line int    // 1-based
	Verb string
	Args string
}

func (d Directive) String() string {
	return fmt.Sprintf("%s:%d: %s %s", d.File, d.Line, d.Verb, d.Args)
}

// File is a parsed go.mod or go.work.
type File struct {
	Name       string
	Directives []Directive
}

// Parse reads the directives of a go.mod or go.work file; name is used in messages.
func Parse(name string, data []byte) (*File, error) {
	file := &File{Name: name}
	block := ""
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for number := 1; scanner.Scan(); number++ {
		line := scanner.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case block != "":
			if line == ")" {
				block = ""
				continue
			}
			file.Directives = append(file.Directives, Directive{File: name, Line: number, Verb: block, Args: line})
		default:
			verb, args, _ := strings.Cut(line, " ")
			args = strings.TrimSpace(args)
			if args == "(" {
				block = verb
				continue
			}
			if args == "" {
				return nil, fmt.Errorf("%s:%d: directive %q has no arguments", name, number, verb)
			}
			file.Directives = append(file.Directives, Directive{File: name, Line: number, Verb: verb, Args: args})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if block != "" {
		return nil, fmt.Errorf("%s: unterminated %s block", name, block)
	}
	return file, nil
}

// All returns every directive with the given verb.
func (f *File) All(verb string) []Directive {
	var out []Directive
	for _, d := range f.Directives {
		if d.Verb == verb {
			out = append(out, d)
		}
	}
	return out
}

// Repo is the module files of a checkout: the root go.work, every go.mod under src/
// keyed by slash-separated module directory (test modules apart, in Tests), every
// tools/<name>/go.mod in Tools, and src/harness/go.sum.
type Repo struct {
	Work       []byte
	Mods       map[string][]byte
	Tests      map[string][]byte
	Tools      map[string][]byte
	HarnessSum []byte
}

// TestModuleSuffix is the directory, below a product module, of that module's nested
// test-only module.
const TestModuleSuffix = "/test/e2e"

// IsTestModule reports whether dir (slash-separated, relative to the root) is the
// nested test-only module of a product module: <product module>/test/e2e.
func IsTestModule(dir string) bool {
	parent, ok := strings.CutSuffix(dir, TestModuleSuffix)
	if !ok {
		return false
	}
	for _, m := range Modules {
		if m.Dir == parent {
			return true
		}
	}
	return false
}

// Load reads go.work, every go.mod below src/ and every tools/<name>/go.mod of the
// checkout at root.
func Load(root string) (Repo, error) {
	repo := Repo{Mods: map[string][]byte{}, Tests: map[string][]byte{}, Tools: map[string][]byte{}}
	work, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return repo, err
	}
	repo.Work = work
	sum, err := os.ReadFile(filepath.Join(root, "src", "harness", "go.sum"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return repo, err
	}
	repo.HarnessSum = sum
	err = filepath.WalkDir(filepath.Join(root, "src"), func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "go.mod" {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if dir := filepath.ToSlash(rel); IsTestModule(dir) {
			repo.Tests[dir] = data
		} else {
			repo.Mods[dir] = data
		}
		return nil
	})
	if err != nil {
		return repo, err
	}
	tools, err := filepath.Glob(filepath.Join(root, "tools", "*", "go.mod"))
	if err != nil {
		return repo, err
	}
	for _, p := range tools {
		data, err := os.ReadFile(p)
		if err != nil {
			return repo, err
		}
		repo.Tools["tools/"+filepath.Base(filepath.Dir(p))] = data
	}
	return repo, nil
}
