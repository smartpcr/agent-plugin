package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// lockDep is one dependency edge of a workspace package, as pnpm resolved it.
type lockDep struct{ Kind, Name, Specifier, Version string }

// importer is one workspace package in pnpm-lock.yaml.
type importer struct {
	Path string
	Deps []lockDep
}

type fileInfo struct {
	Rel  string
	Size int64
}

var (
	reImporterKey = regexp.MustCompile(`^  (\S.*):$`)
	reSectionKey  = regexp.MustCompile(`^    ([A-Za-z]+):$`)
	reDepKey      = regexp.MustCompile(`^      (\S.*):$`)
	reNodeBuiltin = regexp.MustCompile(`["']node:([a-z_]+)(?:/[a-z_/]+)?["']`)
	reMarker      = regexp.MustCompile(`(?://|/\*|\*|#|<!--)\s*(FIXME|TODO|XXX)\b`)
	reDeprecated  = regexp.MustCompile(`@deprecated\b`)
	rePkgMention  = regexp.MustCompile(`\b(?:packages/([a-z0-9-]+)|apps/([a-z0-9-]+)|(vendor|native|python))/`)
	reIDUnsafe    = regexp.MustCompile(`[^A-Za-z0-9._@/:+-]`)
)

// extraUnits are upstream directories that are not pnpm workspaces but still hold
// something the Go design must account for.
var extraUnits = map[string]string{
	"python/sdk":         "Python SDK (client library, examples, tests)",
	"scripts":            "Repository gates, generators, build and release scripts",
	"scripts/cua-helper": "Computer-use helper packaging scripts",
	"scripts/ssh-helper": "SSH helper executable build and probes",
	"vendor/cosmokit":    "Vendored cosmokit utility library (linked by override, not a workspace)",
	"snapshots":          "Recorded-session snapshot fixtures",
	".github":            "GitHub workflows and repository automation",
	".agents/skills":     "Agent skills used by upstream contributors",
	"docs":               "Upstream documentation tree",
}

func runInventory(c config) error {
	if _, err := os.Stat(filepath.Join(c.upstream, "pnpm-lock.yaml")); err != nil {
		return fmt.Errorf("upstream checkout not found at %s: %w", c.upstream, err)
	}
	files, err := walkUpstream(c.upstream)
	if err != nil {
		return err
	}
	imps, err := readImporters(filepath.Join(c.upstream, "pnpm-lock.yaml"))
	if err != nil {
		return err
	}

	// Every path that owns files: workspaces first, then the extra directories.
	ownerPaths := make([]string, 0, len(imps)+len(extraUnits))
	for _, im := range imps {
		ownerPaths = append(ownerPaths, im.Path)
	}
	for p := range extraUnits {
		ownerPaths = append(ownerPaths, p)
	}
	sort.Slice(ownerPaths, func(i, j int) bool { return len(ownerPaths[i]) > len(ownerPaths[j]) })
	ownerOf := func(rel string) string {
		for _, p := range ownerPaths {
			if p != "." && (rel == p || strings.HasPrefix(rel, p+"/")) {
				return p
			}
		}
		return "."
	}

	inv := &inventory{c: c, files: files, imps: imps, ownerOf: ownerOf, seen: map[string]bool{}}
	inv.nameToPath = map[string]string{}
	inv.pkgs()
	inv.deps()
	inv.decisions()
	inv.debt()
	inv.catalogs()

	byType := map[string][]Unit{}
	for _, u := range inv.units {
		byType[u.Type] = append(byType[u.Type], u)
	}
	fmt.Printf("upstream: %s\n", c.upstream)
	for _, t := range unitTypes {
		rows := byType[t]
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		if err := writeJSONL(filepath.Join(c.trace, "inventory", strings.ToLower(t)+".jsonl"), rows); err != nil {
			return err
		}
		fmt.Printf("%-8s %5d", t, len(rows))
		if t == "DEBT" || t == "DEP" || t == "DEC" {
			kinds := map[string]int{}
			for _, u := range rows {
				kinds[fmt.Sprint(u.Attrs["kind"])]++
			}
			for _, k := range sortedKeys(kinds) {
				fmt.Printf("  %s=%d", k, kinds[k])
			}
		}
		fmt.Println()
	}
	fmt.Printf("%-8s %5d\n", "TOTAL", len(inv.units))
	counts := map[string]int{}
	for t, rows := range byType {
		counts[t] = len(rows)
	}
	return writeMeta(c, counts)
}

type inventory struct {
	c          config
	files      []fileInfo
	imps       []importer
	ownerOf    func(string) string
	nameToPath map[string]string // npm package name -> workspace path
	units      []Unit
	seen       map[string]bool
}

// add records a unit, making its ID unique if two upstream things share a name.
func (inv *inventory) add(u Unit) {
	u.ID = reIDUnsafe.ReplaceAllString(u.ID, "_")
	base := u.ID
	for n := 2; inv.seen[u.ID]; n++ {
		u.ID = fmt.Sprintf("%s~%d", base, n)
	}
	inv.seen[u.ID] = true
	inv.units = append(inv.units, u)
}

func (inv *inventory) read(rel string) string {
	b, err := os.ReadFile(filepath.Join(inv.c.upstream, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func walkUpstream(root string) ([]fileInfo, error) {
	var out []fileInfo
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, fileInfo{Rel: slash(rel), Size: info.Size()})
		return nil
	})
	return out, err
}

func unquote(s string) string { return strings.Trim(s, `'"`) }

// readImporters reads the importers section of pnpm-lock.yaml. The section has a fixed
// indentation, so a line scanner is enough and no YAML library is needed.
func readImporters(lock string) ([]importer, error) {
	b, err := os.ReadFile(lock)
	if err != nil {
		return nil, err
	}
	var out []importer
	var cur *importer
	var kind, name, spec string
	in := false
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if line == "importers:" {
			in = true
			continue
		}
		if !in {
			continue
		}
		if line != "" && line[0] != ' ' {
			break
		}
		switch {
		case reImporterKey.MatchString(line):
			out = append(out, importer{Path: unquote(reImporterKey.FindStringSubmatch(line)[1])})
			cur = &out[len(out)-1]
		case reSectionKey.MatchString(line):
			kind = reSectionKey.FindStringSubmatch(line)[1]
		case reDepKey.MatchString(line):
			name = unquote(reDepKey.FindStringSubmatch(line)[1])
		case strings.HasPrefix(line, "        specifier:"):
			spec = unquote(strings.TrimSpace(strings.TrimPrefix(line, "        specifier:")))
		case strings.HasPrefix(line, "        version:"):
			v := unquote(strings.TrimSpace(strings.TrimPrefix(line, "        version:")))
			if cur != nil {
				cur.Deps = append(cur.Deps, lockDep{Kind: kind, Name: name, Specifier: spec, Version: v})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no importers found in %s", lock)
	}
	return out, nil
}

func groupOf(p string) string {
	seg := strings.Split(p, "/")
	switch {
	case p == ".":
		return "root"
	case seg[0] == "packages" && len(seg) > 1:
		return seg[1]
	default:
		return seg[0]
	}
}

func pkgSlug(p string) string {
	if p == "." {
		return "root"
	}
	return strings.ReplaceAll(p, "/", ".")
}

var codeExt = map[string]bool{
	".ts": true, ".tsx": true, ".mts": true, ".cts": true, ".js": true, ".mjs": true, ".cjs": true,
	".py": true, ".swift": true, ".cs": true, ".c": true, ".cc": true, ".cpp": true, ".h": true,
	".hpp": true, ".m": true, ".mm": true, ".sh": true, ".ps1": true, ".nsi": true, ".nsh": true,
	".css": true,
}

var jsExt = map[string]bool{".ts": true, ".tsx": true, ".mts": true, ".cts": true, ".js": true, ".mjs": true, ".cjs": true}

func isTestPath(rel string) bool {
	for _, m := range []string{".spec.", ".test.", "/tests/", "/test/", "/__tests__/", "/fixtures/",
		"/__fixtures__/", ".expected.", "/snapshots/", ".bench.", "/test_", ".fixture."} {
		if strings.Contains("/"+rel, m) {
			return true
		}
	}
	return false
}

func inScanRoots(rel string) bool {
	for _, r := range []string{"packages/", "apps/", "vendor/", "native/", "python/", "scripts/", "benchmarks/", "website/"} {
		if strings.HasPrefix(rel, r) {
			return true
		}
	}
	return false
}

// pkgs writes one unit per workspace package and per extra directory.
func (inv *inventory) pkgs() {
	type size struct{ src, test, files int64 }
	sizes := map[string]*size{}
	for _, f := range inv.files {
		o := inv.ownerOf(f.Rel)
		s := sizes[o]
		if s == nil {
			s = &size{}
			sizes[o] = s
		}
		s.files++
		if !codeExt[path.Ext(f.Rel)] {
			continue
		}
		if isTestPath(f.Rel) {
			s.test += f.Size
		} else {
			s.src += f.Size
		}
	}
	attrsFor := func(p string) map[string]any {
		s := sizes[p]
		if s == nil {
			s = &size{}
		}
		return map[string]any{"group": groupOf(p), "srcBytes": s.src, "testBytes": s.test, "files": s.files}
	}
	for _, im := range inv.imps {
		var meta struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Private     bool            `json:"private"`
			Bin         json.RawMessage `json:"bin"`
			Dsh         json.RawMessage `json:"dsh"`
		}
		_ = json.Unmarshal([]byte(inv.read(path.Join(im.Path, "package.json"))), &meta)
		if meta.Name != "" {
			inv.nameToPath[meta.Name] = im.Path
		}
		a := attrsFor(im.Path)
		a["private"] = meta.Private
		a["hasBin"] = len(meta.Bin) > 0
		if len(meta.Dsh) > 0 {
			a["dshField"] = json.RawMessage(meta.Dsh)
		}
		var ws, third []string
		for _, d := range im.Deps {
			if strings.HasPrefix(d.Version, "link:") {
				if d.Kind != "devDependencies" {
					ws = append(ws, path.Join(im.Path, strings.TrimPrefix(d.Version, "link:")))
				}
			} else if d.Kind != "devDependencies" {
				third = append(third, d.Name)
			}
		}
		sort.Strings(ws)
		sort.Strings(third)
		a["workspaceDeps"] = ws
		a["thirdPartyDeps"] = third
		inv.add(Unit{ID: "UP-PKG-" + pkgSlug(im.Path), Type: "PKG", Name: meta.Name, Path: im.Path,
			Title: meta.Description, Attrs: a})
	}
	for _, p := range sortedKeys(extraUnits) {
		a := attrsFor(p)
		a["workspace"] = false
		inv.add(Unit{ID: "UP-PKG-" + pkgSlug(p), Type: "PKG", Name: p, Path: p, Title: extraUnits[p], Attrs: a})
	}
}

// deps writes one unit per third-party npm dependency, Node built-in module and
// Python dependency.
func (inv *inventory) deps() {
	type agg struct {
		versions, prodBy, devBy map[string]bool
	}
	m := map[string]*agg{}
	for _, im := range inv.imps {
		for _, d := range im.Deps {
			if strings.HasPrefix(d.Version, "link:") {
				continue
			}
			a := m[d.Name]
			if a == nil {
				a = &agg{map[string]bool{}, map[string]bool{}, map[string]bool{}}
				m[d.Name] = a
			}
			v := d.Version
			if i := strings.Index(v, "("); i > 0 {
				v = v[:i]
			}
			a.versions[v] = true
			if d.Kind == "devDependencies" {
				a.devBy[im.Path] = true
			} else {
				a.prodBy[im.Path] = true
			}
		}
	}
	patched := map[string]bool{}
	for _, f := range inv.files {
		if strings.HasPrefix(f.Rel, "patches/") {
			patched[patchDep(path.Base(f.Rel))] = true
		}
	}
	groupsOf := func(paths map[string]bool) []string {
		g := map[string]bool{}
		for p := range paths {
			g[groupOf(p)] = true
		}
		return sortedKeys(g)
	}
	for _, name := range sortedKeys(m) {
		a := m[name]
		kind := "npm-dev-only"
		if len(a.prodBy) > 0 {
			kind = "npm-runtime"
		}
		inv.add(Unit{ID: "UP-DEP-" + name, Type: "DEP", Name: name, Attrs: map[string]any{
			"kind": kind, "versions": sortedKeys(a.versions),
			"runtimeConsumers": sortedKeys(a.prodBy), "devConsumers": sortedKeys(a.devBy),
			"runtimeGroups": groupsOf(a.prodBy), "devGroups": groupsOf(a.devBy), "patched": patched[name],
		}})
	}

	// Node built-in modules are platform dependencies too: each needs a Go answer.
	type bagg struct {
		files  int
		groups map[string]bool
	}
	b := map[string]*bagg{}
	for _, f := range inv.files {
		if !jsExt[path.Ext(f.Rel)] || !inScanRoots(f.Rel) || isTestPath(f.Rel) || f.Size > 1<<20 {
			continue
		}
		found := map[string]bool{}
		for _, mm := range reNodeBuiltin.FindAllStringSubmatch(inv.read(f.Rel), -1) {
			found[mm[1]] = true
		}
		for mod := range found {
			x := b[mod]
			if x == nil {
				x = &bagg{groups: map[string]bool{}}
				b[mod] = x
			}
			x.files++
			x.groups[groupOf(inv.ownerOf(f.Rel))] = true
		}
	}
	for _, mod := range sortedKeys(b) {
		inv.add(Unit{ID: "UP-DEP-node:" + mod, Type: "DEP", Name: "node:" + mod, Attrs: map[string]any{
			"kind": "node-builtin", "importingFiles": b[mod].files, "groups": sortedKeys(b[mod].groups),
		}})
	}

	rePy := regexp.MustCompile(`"([A-Za-z0-9_.-]+)[^"]*"`)
	for _, proj := range []string{"python/sdk/pyproject.toml", "python/sdk-runtime/pyproject.toml"} {
		inList := false
		for _, line := range strings.Split(inv.read(proj), "\n") {
			t := strings.TrimSpace(line)
			if !inList && (strings.HasPrefix(t, "dependencies = [") || strings.HasPrefix(t, "requires = [") ||
				strings.HasPrefix(t, "dev = [") || strings.HasPrefix(t, "test = [")) {
				inList = true
			}
			if inList {
				for _, mm := range rePy.FindAllStringSubmatch(t, -1) {
					id := "UP-DEP-py:" + strings.ToLower(mm[1])
					if !inv.seen[id] {
						inv.add(Unit{ID: id, Type: "DEP", Name: "py:" + mm[1], Path: path.Dir(proj),
							Attrs: map[string]any{"kind": "python", "declaredIn": proj, "spec": strings.Trim(mm[0], `"`)}})
					}
				}
				if strings.Contains(t, "]") {
					inList = false
				}
			}
		}
	}
}

// patchDep turns a patch file name such as "@botiverse__oar@0.67.0.patch" into the
// dependency name "@botiverse/oar".
func patchDep(file string) string {
	n := strings.TrimSuffix(file, ".patch")
	n = strings.ReplaceAll(n, "__", "/")
	if i := strings.LastIndex(n, "@"); i > 0 {
		n = n[:i]
	}
	return n
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// section returns the body of the first heading that starts with the given text, up
// to the next heading of the same or a higher level.
func section(doc, heading string) string {
	lines := strings.Split(doc, "\n")
	level := strings.Count(strings.SplitN(heading, " ", 2)[0], "#")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, heading) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "#") {
			h := strings.SplitN(lines[i], " ", 2)[0]
			if strings.Trim(h, "#") == "" && len(h) <= level {
				end = i
				break
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func firstHeading(doc string) string {
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "# "))
		}
	}
	return ""
}

func isTranslation(rel string) bool {
	return strings.HasSuffix(rel, ".zh.md") || strings.HasSuffix(rel, ".i18n.yaml")
}

// decisions writes one unit per upstream decision note that is still current.
// Archived notes are frozen and are not current authority upstream, so they are not units.
func (inv *inventory) decisions() {
	for _, f := range inv.files {
		seg := strings.Split(f.Rel, "/")
		if len(seg) != 5 || seg[0] != ".agents" || seg[1] != "notes" || !strings.HasSuffix(f.Rel, ".md") || isTranslation(f.Rel) {
			continue
		}
		status, category := seg[2], seg[3]
		if status != "implemented" && status != "proposed" && status != "rejected" {
			continue
		}
		base := strings.TrimSuffix(seg[4], ".md")
		if base == "AGENTS" || base == "CLAUDE" || base == "README" {
			continue
		}
		doc := inv.read(f.Rel)
		excerpt := section(doc, "## Decision")
		if excerpt == "" {
			excerpt = strings.TrimPrefix(doc, "# "+firstHeading(doc))
		}
		counts := map[string]int{}
		for _, mm := range rePkgMention.FindAllStringSubmatch(doc, -1) {
			switch {
			case mm[1] != "":
				counts[mm[1]]++
			case mm[2] != "":
				counts["apps/"+mm[2]]++
			default:
				counts[mm[3]]++
			}
		}
		hints := sortedKeys(counts)
		sort.SliceStable(hints, func(i, j int) bool { return counts[hints[i]] > counts[hints[j]] })
		if len(hints) > 4 {
			hints = hints[:4]
		}
		inv.add(Unit{ID: "UP-DEC-" + status + "." + category + "." + base, Type: "DEC", Name: base, Path: f.Rel,
			Title: strings.TrimPrefix(firstHeading(doc), "Agent Note: "),
			Attrs: map[string]any{"kind": status, "category": category, "bytes": f.Size,
				"areaHints": hints, "problem": clip(section(doc, "## Problem"), 500), "decision": clip(excerpt, 1200)}})
	}
}

// debt writes one unit per piece of tech debt that can be found mechanically.
func (inv *inventory) debt() {
	// Patched dependencies.
	for _, f := range inv.files {
		if strings.HasPrefix(f.Rel, "patches/") && strings.HasSuffix(f.Rel, ".patch") {
			base := path.Base(f.Rel)
			inv.add(Unit{ID: "UP-DEBT-patch." + strings.TrimSuffix(base, ".patch"), Type: "DEBT", Path: f.Rel,
				Title: "Local patch applied to " + patchDep(base),
				Attrs: map[string]any{"kind": "patch", "dep": patchDep(base), "bytes": f.Size}})
		}
	}

	// Pins, overrides and install-policy exemptions in pnpm-workspace.yaml.
	var sectionName string
	var comment []string
	for _, line := range strings.Split(inv.read("pnpm-workspace.yaml"), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			comment = nil
		case line[0] != ' ' && strings.HasSuffix(t, ":"):
			sectionName = strings.TrimSuffix(t, ":")
			comment = nil
		case strings.HasPrefix(t, "#"):
			comment = append(comment, strings.TrimSpace(strings.TrimPrefix(t, "#")))
		case sectionName == "overrides" || sectionName == "allowBuilds" || sectionName == "minimumReleaseAgeExclude":
			entry := strings.TrimPrefix(t, "- ")
			key := unquote(entry)
			if sectionName != "minimumReleaseAgeExclude" {
				if i := strings.LastIndex(entry, ": "); i > 0 {
					key = unquote(entry[:i])
				}
			}
			dep := key
			if i := strings.LastIndex(dep, ">"); i >= 0 {
				dep = dep[i+1:]
			}
			if i := strings.Index(dep[1:], "@"); i >= 0 {
				dep = dep[:i+1]
			}
			inv.add(Unit{ID: "UP-DEBT-pin." + sectionName + "." + clip(key, 70), Type: "DEBT", Path: "pnpm-workspace.yaml",
				Title: sectionName + ": " + entry,
				Attrs: map[string]any{"kind": "pin", "dep": dep, "section": sectionName, "statedReason": strings.Join(comment, " ")}})
		}
	}

	// Limitations each package README admits.
	for _, im := range inv.imps {
		doc := inv.read(path.Join(im.Path, "README.md"))
		body := section(doc, "## Known Limitations and Deferred Work")
		if i := strings.Index(body, `<a id="dev-note">`); i >= 0 {
			body = body[:i]
		}
		if i := strings.Index(body, "### Dev Note"); i >= 0 {
			body = body[:i]
		}
		var bullets []string
		for _, l := range strings.Split(body, "\n") {
			switch {
			case strings.HasPrefix(l, "- "):
				bullets = append(bullets, strings.TrimPrefix(l, "- "))
			case strings.HasPrefix(l, "  ") && len(bullets) > 0 && strings.TrimSpace(l) != "":
				bullets[len(bullets)-1] += " " + strings.TrimSpace(l)
			}
		}
		for i, b := range bullets {
			inv.add(Unit{ID: fmt.Sprintf("UP-DEBT-limit.%s.%d", pkgSlug(im.Path), i+1), Type: "DEBT", Path: im.Path,
				Title: clip(b, 240), Attrs: map[string]any{"kind": "limit", "text": clip(b, 1500), "readme": path.Join(im.Path, "README.md")}})
		}
	}

	// Markers and deprecations in product source.
	for _, f := range inv.files {
		if !codeExt[path.Ext(f.Rel)] || !inScanRoots(f.Rel) || isTestPath(f.Rel) || f.Size > 1<<20 {
			continue
		}
		for i, l := range strings.Split(inv.read(f.Rel), "\n") {
			if mm := reMarker.FindStringSubmatch(l); mm != nil {
				inv.add(Unit{ID: fmt.Sprintf("UP-DEBT-marker.%s:%d", f.Rel, i+1), Type: "DEBT", Path: f.Rel,
					Title: clip(l, 220), Attrs: map[string]any{"kind": "marker", "marker": mm[1], "line": i + 1}})
			}
			if reDeprecated.MatchString(l) {
				inv.add(Unit{ID: fmt.Sprintf("UP-DEBT-deprecated.%s:%d", f.Rel, i+1), Type: "DEBT", Path: f.Rel,
					Title: clip(l, 220), Attrs: map[string]any{"kind": "deprecated", "line": i + 1}})
			}
		}
	}

	// Baselines that record violations upstream has not yet removed, and postmortems.
	for _, f := range inv.files {
		switch {
		case strings.HasPrefix(f.Rel, "scripts/") && strings.HasSuffix(f.Rel, ".baseline.json"),
			f.Rel == "scripts/coverage-exempt.ts":
			inv.add(Unit{ID: "UP-DEBT-baseline." + path.Base(f.Rel), Type: "DEBT", Path: f.Rel,
				Title: "Recorded exceptions to a repository rule: " + path.Base(f.Rel),
				Attrs: map[string]any{"kind": "baseline", "bytes": f.Size}})
		case strings.HasPrefix(f.Rel, "docs/postmortem/") && strings.HasSuffix(f.Rel, ".md") &&
			!isTranslation(f.Rel) && path.Base(f.Rel) != "README.md":
			inv.add(Unit{ID: "UP-DEBT-postmortem." + strings.TrimSuffix(path.Base(f.Rel), ".md"), Type: "DEBT", Path: f.Rel,
				Title: firstHeading(inv.read(f.Rel)), Attrs: map[string]any{"kind": "postmortem"}})
		}
	}
}

func shortPkg(name string) string { return strings.TrimPrefix(name, "@deepseek-ai/") }

// catalogs writes the contract units upstream already enumerates in generated documents.
func (inv *inventory) catalogs() {
	reH2Tick := regexp.MustCompile("^## `([^`]+)`")
	reH3Tick := regexp.MustCompile("^### `([^`]+)`")
	reH4Tick := regexp.MustCompile("^#### `([^`]+)`(?: — (.*))?")

	pkg := ""
	for _, l := range strings.Split(inv.read("docs/tool-catalog.md"), "\n") {
		if m := reH2Tick.FindStringSubmatch(l); m != nil {
			pkg = m[1]
		} else if m := reH3Tick.FindStringSubmatch(l); m != nil && pkg != "" {
			inv.add(Unit{ID: "UP-TOOL-" + shortPkg(pkg) + "." + m[1], Type: "TOOL", Name: m[1], Path: inv.nameToPath[pkg],
				Attrs: map[string]any{"package": pkg, "catalog": "docs/tool-catalog.md"}})
		}
	}

	for _, l := range strings.Split(inv.read("docs/config-catalog.md"), "\n") {
		if m := reH2Tick.FindStringSubmatch(l); m != nil {
			inv.add(Unit{ID: "UP-CFG-" + shortPkg(m[1]), Type: "CFG", Name: m[1], Path: inv.nameToPath[m[1]],
				Attrs: map[string]any{"package": m[1], "catalog": "docs/config-catalog.md"}})
		}
	}

	reDeclared := regexp.MustCompile("\\[`([^`:]+):(\\d+)`\\]")
	for _, l := range strings.Split(inv.read("docs/event-producer-consumer.md"), "\n") {
		if !strings.HasPrefix(l, "| `") {
			continue
		}
		cols := strings.Split(l, " | ")
		if len(cols) < 3 {
			continue
		}
		name := strings.Trim(strings.TrimPrefix(cols[0], "| "), "`")
		declared, owner := "", ""
		if m := reDeclared.FindStringSubmatch(cols[2]); m != nil {
			declared = m[1] + ":" + m[2]
			owner = inv.ownerOf(m[1])
		}
		inv.add(Unit{ID: "UP-EVT-" + name, Type: "EVT", Name: name, Path: owner,
			Attrs: map[string]any{"mode": strings.Trim(cols[1], "`"), "declaredIn": declared, "catalog": "docs/event-producer-consumer.md"}})
	}

	reSvc := regexp.MustCompile("^\\| `ctx\\.([A-Za-z0-9_$]+)` \\| `([a-z-]+)` \\| (.*)")
	reLink := regexp.MustCompile(`\]\(\.\./([^)]+)\)`)
	for _, l := range strings.Split(inv.read("docs/capability-seams.md"), "\n") {
		m := reSvc.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		owner := ""
		if lm := reLink.FindStringSubmatch(m[3]); lm != nil {
			owner = lm[1]
		}
		inv.add(Unit{ID: "UP-SVC-" + m[1], Type: "SVC", Name: "ctx." + m[1], Path: owner,
			Attrs: map[string]any{"role": m[2], "catalog": "docs/capability-seams.md"}})
	}

	for _, l := range strings.Split(inv.read("docs/cli-help.md"), "\n") {
		if m := reH2Tick.FindStringSubmatch(l); m != nil {
			inv.add(Unit{ID: "UP-CLI-" + strings.ReplaceAll(m[1], " ", "."), Type: "CLI", Name: m[1], Path: "apps/cli",
				Attrs: map[string]any{"catalog": "docs/cli-help.md"}})
		}
	}

	for _, cat := range []string{"docs/persistence-catalog.md", "docs/experimental-persistence-catalog.md"} {
		for _, l := range strings.Split(inv.read(cat), "\n") {
			if m := reH4Tick.FindStringSubmatch(l); m != nil {
				inv.add(Unit{ID: "UP-PERSIST-" + m[1], Type: "PERSIST", Name: m[1],
					Attrs: map[string]any{"visibility": m[2], "catalog": cat}})
			}
		}
	}
}
