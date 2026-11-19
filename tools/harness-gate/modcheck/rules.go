package modcheck

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// CheckToolchain11 is DH-T-TOOLCHAIN-11: the checkout has exactly the three product
// modules of RM-1, go.work uses exactly those three, every one of them (and go.work)
// states the go floor (RM-2), src/harness requires src/go and src/sdk at real
// revisions recorded in its go.sum (CheckPins), and no file carries a replace
// directive (RM-4). Every violation is reported, each naming its
// file and line.
func CheckToolchain11(repo Repo) error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	var dirs []string
	for dir := range repo.Mods {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	var want []string
	for _, m := range Modules {
		want = append(want, m.Dir)
	}
	slices.Sort(want)
	if !slices.Equal(dirs, want) {
		fail("modules under src/ are %v, want exactly %v (RM-1)", dirs, want)
	}

	files := map[string]*File{}
	for _, m := range Modules {
		data, ok := repo.Mods[m.Dir]
		if !ok {
			continue
		}
		file, err := Parse(m.Dir+"/go.mod", data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		files[m.Dir] = file
		modules := file.All("module")
		if len(modules) != 1 || modules[0].Args != m.Path {
			fail("%s/go.mod: module is %v, want %s (RM-1)", m.Dir, modules, m.Path)
		}
	}

	work, err := Parse("go.work", repo.Work)
	if err != nil {
		errs = append(errs, err)
	} else {
		var uses []string
		for _, d := range work.All("use") {
			uses = append(uses, path.Clean(strings.TrimPrefix(d.Args, "./")))
		}
		slices.Sort(uses)
		if !slices.Equal(uses, want) {
			fail("go.work uses %v, want exactly %v (RM-1)", uses, want)
		}
	}

	// One go floor. A file off the floor is named with its own go line and the
	// floor's go line from the first file that has it.
	goLines := map[string]Directive{}
	var floorLine *Directive
	named := []*File{}
	for _, m := range Modules {
		if file := files[m.Dir]; file != nil {
			named = append(named, file)
		}
	}
	if work != nil {
		named = append(named, work)
	}
	for _, file := range named {
		lines := file.All("go")
		if len(lines) != 1 {
			fail("%s: has %d go lines, want exactly one (RM-2)", file.Name, len(lines))
			continue
		}
		goLines[file.Name] = lines[0]
		if lines[0].Args == Floor && floorLine == nil {
			floorLine = &lines[0]
		}
	}
	for _, file := range named {
		line, ok := goLines[file.Name]
		if !ok || line.Args == Floor {
			continue
		}
		reference := "go " + Floor
		if floorLine != nil {
			reference = floorLine.String()
		}
		fail("%s differs from the go floor %s (RM-2)", line, reference)
	}

	for _, file := range named {
		for _, d := range file.All("replace") {
			fail("%s: replace directive is not allowed (RM-4)", d)
		}
	}

	if harness := files["src/harness"]; harness != nil {
		required := map[string]bool{}
		for _, d := range harness.All("require") {
			required[strings.Fields(d.Args)[0]] = true
		}
		for _, m := range Modules[:2] {
			if !required[m.Path] {
				fail("src/harness/go.mod does not require %s (RM-4)", m.Path)
			}
		}
		if err := CheckPins(repo.Mods["src/harness"], repo.HarnessSum); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// CheckSDK17 is DH-T-SDK-17: src/sdk/go.mod requires nothing, so the SDK depends on
// the standard library only (RM-3). Each require line is named.
func CheckSDK17(data []byte) error {
	file, err := Parse("src/sdk/go.mod", data)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range file.All("require") {
		errs = append(errs, fmt.Errorf("%s: src/sdk must require nothing (RM-3)", d))
	}
	return errors.Join(errs...)
}
