package modcheck

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// reasonedVerbs are the directives that always need a reason comment. A require needs
// one only when it is direct (no "// indirect" mark).
var reasonedVerbs = []string{"replace", "exclude", "tool", "toolchain"}

// CheckReasons is the reason rule of RM-10 and DH-ADR-TOOLCHAIN-11 for one go.mod (or
// go.work) file, reported under name: every direct require, and every replace,
// exclude, tool and toolchain directive, carries a comment that gives its reason,
// either on the comment lines directly above it (no blank line between) or at the end
// of its own line. A bare "// indirect" mark is not a reason, and a comment above a
// block's opening line does not count for the entries inside it. Every directive
// without a reason is named with its file and line.
func CheckReasons(name string, data []byte) error {
	var errs []error
	block := ""
	commentAbove := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for number := 1; scanner.Scan(); number++ {
		raw := strings.TrimSpace(scanner.Text())
		code, comment, hasComment := strings.Cut(raw, "//")
		code = strings.TrimSpace(code)
		if code == "" {
			commentAbove = hasComment
			continue
		}
		above := commentAbove
		commentAbove = false
		verb, args := block, code
		switch {
		case block != "" && code == ")":
			block = ""
			continue
		case block == "":
			v, rest, _ := strings.Cut(code, " ")
			verb, args = v, strings.TrimSpace(rest)
			if args == "(" {
				block = verb
				continue
			}
		}
		comment = strings.TrimSpace(comment)
		indirect := false
		if rest, ok := strings.CutPrefix(comment, "indirect"); ok && (rest == "" || strings.IndexAny(rest[:1], " ;,:") == 0) {
			indirect = true
			comment = strings.TrimSpace(strings.TrimLeft(rest, " ;,:"))
		}
		needs := slices.Contains(reasonedVerbs, verb) || (verb == "require" && !indirect)
		if !needs || above || comment != "" {
			continue
		}
		d := Directive{File: name, Line: number, Verb: verb, Args: args}
		errs = append(errs, fmt.Errorf("%s: no reason comment above it or at the end of its line (RM-10, DH-ADR-TOOLCHAIN-11)", d))
	}
	if err := scanner.Err(); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}
	return errors.Join(errs...)
}

// CheckRepoReasons runs CheckReasons over every module file of repo: the product
// modules, the test modules, the tools modules and go.work, in that order.
func CheckRepoReasons(repo Repo) error {
	var errs []error
	for _, group := range []map[string][]byte{repo.Mods, repo.Tests, repo.Tools} {
		var dirs []string
		for dir := range group {
			dirs = append(dirs, dir)
		}
		slices.Sort(dirs)
		for _, dir := range dirs {
			if err := CheckReasons(dir+"/go.mod", group[dir]); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if repo.Work != nil {
		if err := CheckReasons("go.work", repo.Work); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
