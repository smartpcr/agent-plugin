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
// revisions recorded in its go.sum (CheckPins), no product module or go.work carries
// a replace directive (RM-4), and src/go requires nothing (CheckCordisStdlib).
//
// A test module (IsTestModule) is outside the RM-1 count and go.work. It must declare
// <product module path>/test/e2e on the go floor, and its only allowed replace points
// its own product module at a directory of the checkout. Every violation is reported,
// each naming its file and line. Reason comments are CheckReasons.
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
	tests := testFiles(repo, fail, &errs)
	named = append(named, tests...)
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
		if slices.Contains(tests, file) {
			continue
		}
		for _, d := range file.All("replace") {
			fail("%s: replace directive is not allowed (RM-4)", d)
		}
	}
	for _, file := range tests {
		product := strings.TrimSuffix(strings.TrimSuffix(file.Name, "/go.mod"), TestModuleSuffix)
		for _, d := range file.All("replace") {
			from, to, _ := strings.Cut(d.Args, "=>")
			if strings.TrimSpace(from) != repoModulePath(product) || !strings.HasPrefix(strings.TrimSpace(to), ".") {
				fail("%s: a test module may replace only its own product module %s with a directory of the checkout (RM-4)", d, repoModulePath(product))
			}
		}
	}
	if data, ok := repo.Mods["src/go"]; ok {
		if err := CheckCordisStdlib(data); err != nil {
			errs = append(errs, err)
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

// testFiles parses the test modules of repo, in directory order, and checks that each
// declares its expected module path.
func testFiles(repo Repo, fail func(string, ...any), errs *[]error) []*File {
	var dirs []string
	for dir := range repo.Tests {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	var files []*File
	for _, dir := range dirs {
		file, err := Parse(dir+"/go.mod", repo.Tests[dir])
		if err != nil {
			*errs = append(*errs, err)
			continue
		}
		want := repoModulePath(strings.TrimSuffix(dir, TestModuleSuffix)) + TestModuleSuffix
		if modules := file.All("module"); len(modules) != 1 || modules[0].Args != want {
			fail("%s/go.mod: module is %v, want %s (test module of M1-D-TOOL-4)", dir, modules, want)
		}
		files = append(files, file)
	}
	return files
}

// repoModulePath is the module path of the product module in dir.
func repoModulePath(dir string) string {
	for _, m := range Modules {
		if m.Dir == dir {
			return m.Path
		}
	}
	return dir
}

// CheckCordisStdlib is TestCordisModuleIsStandardLibraryOnly's rule: src/go/go.mod
// requires nothing, so the published cordis module depends on the standard library
// only (M1-D-TOOL-4 (A); its Gherkin suites are the test module src/go/test/e2e).
// Each require line is named.
func CheckCordisStdlib(data []byte) error {
	file, err := Parse("src/go/go.mod", data)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range file.All("require") {
		errs = append(errs, fmt.Errorf("%s: src/go must require nothing; test-only requirements belong in src/go/test/e2e/go.mod (M1-D-TOOL-4)", d))
	}
	return errors.Join(errs...)
}
