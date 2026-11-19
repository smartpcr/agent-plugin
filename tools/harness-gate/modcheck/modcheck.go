// Package modcheck holds the repository's module pin rules: exactly three product
// modules (src/go, src/sdk, src/harness) on one go floor, no replace directive, and an
// src/sdk that requires nothing (RM-1..RM-4; DH-T-TOOLCHAIN-11, DH-T-SDK-17).
//
// go.mod and go.work files are read with a small line parser of their own, so the
// gate stays standard-library only and checks the exact committed bytes.
package modcheck

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Floor is the single go line every product module and the workspace state (RM-2).
const Floor = "1.26.0"

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

// Repo is the module files of a checkout: the root go.work and every go.mod under
// src/, keyed by slash-separated module directory.
type Repo struct {
	Work []byte
	Mods map[string][]byte
}

// Load reads go.work and every go.mod below src/ of the checkout at root.
func Load(root string) (Repo, error) {
	repo := Repo{Mods: map[string][]byte{}}
	work, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return repo, err
	}
	repo.Work = work
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
		repo.Mods[filepath.ToSlash(rel)] = data
		return nil
	})
	return repo, err
}
