package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// Artifact is one binary of the release rehearsal and what it embeds.
type Artifact struct {
	GOOS, GOARCH string
	Path         string
	SHA256       string
	Version      string // the version dsh --version prints: the snapshot's main.version (snapshotVersion) after Rehearse, else the main module version
	Revision     string // vcs.revision, empty when the build had no VCS
	Modified     string // vcs.modified
	Settings     map[string]string
	Archive      string // the archive the binary was extracted from, verified against ChecksumFile
}

// Name is the binary's file name, e.g. dsh-linux-amd64 or dsh-windows-arm64.exe.
func (a Artifact) Name() string { return filepath.Base(a.Path) }

// GoReleaserConfig is the dsh release configuration, relative to the repository root
// (plan 19.1.1, M1-G-TOOL-8). It is separate from .goreleaser.yaml, the file
// release.yml runs, so a release tag publishes nothing of dsh before DH-S-46.
const GoReleaserConfig = ".goreleaser.dsh.yaml"

// ChecksumFile is the checksum file GoReleaserConfig names, in the dist directory.
const ChecksumFile = "checksums.txt"

// LicenseFile is the licence notice every archive must carry (DH-T-TOOLCHAIN-14).
const LicenseFile = "LICENSE"

// ReplayCheck is the seam of the replay half of the rehearsal (plan 19.1.2,
// M1-G-TOOL-9; the "host-matching binary passes one keyless replay" clause of the
// Stage 19.1 scenario and of DH-E-TOOLCHAIN-2). It runs one keyless replay against
// bin, the binary of the host's platform extracted from its verified archive. It is
// nil until impl.md step 16.11 builds the replay gate on the replay runner of step
// 16.7; while nil the rehearsal reports the clause as open and never as passed.
var ReplayCheck func(ctx context.Context, w io.Writer, bin string) error

// distKey matches a top-level "dist:" key, which GoReleaserConfig must not set: the
// rehearsal sets it to the --out directory, so goreleaser's --clean can never remove
// a directory of the repository.
var distKey = regexp.MustCompile(`(?m)^dist\s*:`)

// publishTokens are the variables goreleaser reads to publish. The rehearsal removes
// them from goreleaser's environment, so nothing can be published even if a
// configuration or flag asked for it.
var publishTokens = []string{"GITHUB_TOKEN", "GITLAB_TOKEN", "GITEA_TOKEN", "GORELEASER_KEY"}

// dshModule is the module that holds cmd/dsh.
func (c *Config) dshModule() (string, error) {
	for _, m := range c.Modules {
		if isDir(filepath.Join(m, "cmd", "dsh")) {
			return m, nil
		}
	}
	return "", fmt.Errorf("no module under %s holds cmd/dsh (QG-8)", c.Root)
}

// Rehearse is the release rehearsal (DH-D-TOOLCHAIN-5; DH-ADR-TOOLCHAIN-13,
// DH-ADR-15; plan 19.1.1): the real release pipeline with nothing published. It runs
// the goreleaser pinned in GoReleaserModule as "goreleaser release --snapshot --clean
// -f .goreleaser.dsh.yaml" with GOWORK=off and GOFLAGS cleared, its dist directory
// set to out/dist, and then verifies the snapshot (VerifySnapshot): one archive per
// target of Targets and ChecksumFile, each archive's SHA-256 equal to its line, each
// archive holding LicenseFile and a pure-Go, -trimpath dsh for its target. Each binary
// is extracted to out as dsh-<goos>-<goarch>[.exe] and printed as one line,
// "<sha256>  <name>  version=... vcs.revision=... vcs.modified=...", in the format of
// sha256sum; the version is the snapshot's (snapshotVersion), the one each binary
// prints with --version.
//
// It publishes nothing: the configuration disables the release, snapshot mode skips
// every publishing step, goreleaser gets no publishing token, and the repository's
// tags are compared before and after (a change fails the rehearsal). The replay of
// the host's binary is ReplayCheck; while that is nil it is reported open.
func (c *Config) Rehearse(ctx context.Context, w io.Writer, out string) ([]Artifact, error) {
	mod, err := c.dshModule()
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(c.Root, GoReleaserConfig)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("the dsh release configuration %s: %w (plan 19.1.1)", GoReleaserConfig, err)
	}
	if distKey.Match(data) {
		return nil, fmt.Errorf("%s sets dist:, which the rehearsal owns (it is set to the --out directory, so --clean never removes a directory of the repository)", GoReleaserConfig)
	}
	bin, err := c.goreleaserBinary(ctx)
	if err != nil {
		return nil, err
	}
	dist := filepath.Join(out, "dist")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	// goreleaser has no flag for the dist directory, so a temporary copy of the
	// configuration gains the one key; the copy lives outside the repository and out.
	tmp, err := os.MkdirTemp("", "harness-gate-goreleaser-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	runCfg := filepath.Join(tmp, GoReleaserConfig)
	quoted := "'" + strings.ReplaceAll(filepath.ToSlash(dist), "'", "''") + "'"
	if err := os.WriteFile(runCfg, append(bytes.TrimRight(data, "\n"), []byte("\n\n# Set by harness-gate rehearse.\ndist: "+quoted+"\n")...), 0o644); err != nil {
		return nil, err
	}

	env := c.env("CGO_ENABLED=0", "GOFLAGS=")
	env = slices.DeleteFunc(env, func(e string) bool {
		return slices.ContainsFunc(publishTokens, func(k string) bool { return envKey(e) == envKey(k+"=") })
	})
	tagsBefore, err := c.tags(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(w, "rehearse: goreleaser release --snapshot --clean -f %s (cmd/dsh of %s) with GOWORK=off CGO_ENABLED=0 -trimpath into %s; nothing is published\n", GoReleaserConfig, c.rel(mod), dist)
	runErr := c.step(ctx, w, c.Root, env, "goreleaser release --snapshot --clean -f "+GoReleaserConfig, bin, "release", "--snapshot", "--clean", "-f", runCfg)

	var arts []Artifact
	var errs []error
	if runErr != nil {
		errs = append(errs, runErr)
	} else {
		arts, err = VerifySnapshot(dist, out)
		if err != nil {
			errs = append(errs, err)
		}
		if v, err := snapshotVersion(dist); err != nil {
			errs = append(errs, err)
		} else {
			for i := range arts {
				arts[i].Version = v
			}
		}
	}
	if tagsAfter, err := c.tags(ctx); err != nil {
		errs = append(errs, err)
	} else if tagsAfter != tagsBefore {
		errs = append(errs, fmt.Errorf("the rehearsal changed the repository's tags:\nbefore:\n%s\nafter:\n%s", tagsBefore, tagsAfter))
	}
	for _, a := range arts {
		fmt.Fprintf(w, "%s  %s  version=%s vcs.revision=%s vcs.modified=%s\n", a.SHA256, a.Name(), a.Version, orNone(a.Revision), orNone(a.Modified))
	}
	if err := errors.Join(errs...); err != nil {
		return arts, err
	}
	fmt.Fprintf(w, "rehearse: %d archives and %s in %s; every archive verified against %s\n", len(arts), ChecksumFile, dist, ChecksumFile)
	if err := c.replay(ctx, w, arts); err != nil {
		return arts, err
	}
	fmt.Fprintf(w, "rehearse: %d binaries, nothing published\n", len(arts))
	return arts, nil
}

// replay runs ReplayCheck against the binary of the host's platform, or reports the
// clause open while ReplayCheck is nil.
func (c *Config) replay(ctx context.Context, w io.Writer, arts []Artifact) error {
	i := slices.IndexFunc(arts, func(a Artifact) bool { return a.GOOS == runtime.GOOS && a.GOARCH == runtime.GOARCH })
	if i < 0 {
		fmt.Fprintf(w, "rehearse: no archive matches this host (%s/%s); its replay is not run\n", runtime.GOOS, runtime.GOARCH)
		return nil
	}
	host := arts[i]
	if ReplayCheck == nil {
		fmt.Fprintf(w, "rehearse: replay of %s: OPEN, not built (plan 19.1.2 needs impl.md step 16.11 on the replay runner of step 16.7); not a pass\n", host.Name())
		return nil
	}
	if err := ReplayCheck(ctx, w, host.Path); err != nil {
		return fmt.Errorf("replay of %s: %w", host.Name(), err)
	}
	fmt.Fprintf(w, "rehearse: replay of %s passed\n", host.Name())
	return nil
}

// GoReleaserModule is the tools module that pins the rehearsal's goreleaser, relative
// to the repository root. It is its own module, not a tool line of SelfModule, so
// goreleaser's large module graph stays out of the gate's and the linters' build list.
const GoReleaserModule = "tools/goreleaser"

// goreleaserBinary builds the goreleaser that GoReleaserModule pins, on the floor
// toolchain with GOWORK=off, and returns its path ("go tool -n goreleaser"). There is
// no fallback to a goreleaser on PATH: an unpinned binary fails the rehearsal.
func (c *Config) goreleaserBinary(ctx context.Context) (string, error) {
	dir := filepath.Join(c.Root, filepath.FromSlash(GoReleaserModule))
	if !isFile(filepath.Join(dir, "go.mod")) {
		return "", fmt.Errorf("the tools module %s was not found under %s: the pinned goreleaser cannot run", GoReleaserModule, c.Root)
	}
	out, err := c.command(ctx, dir, c.env("GOFLAGS="), c.Go, "tool", "-n", "goreleaser")
	if err != nil {
		return "", fmt.Errorf("go tool -n goreleaser in %s: %w\n%s", GoReleaserModule, err, out)
	}
	bin := lastLine(out)
	if !isFile(bin) {
		return "", fmt.Errorf("go tool -n goreleaser in %s printed %q, which is not a file", GoReleaserModule, bin)
	}
	return bin, nil
}

// tags lists the repository's tags, to prove the rehearsal created none.
func (c *Config) tags(ctx context.Context) (string, error) {
	out, err := c.command(ctx, c.Root, c.env(), "git", "for-each-ref", "refs/tags")
	if err != nil {
		return "", fmt.Errorf("git for-each-ref refs/tags: %w\n%s", err, out)
	}
	return out, nil
}

// archiveSuffix ends the archive name of a target in GoReleaserConfig: zip on
// Windows, tar.gz elsewhere.
func archiveSuffix(goos, goarch string) string {
	if goos == "windows" {
		return "_" + goos + "_" + goarch + ".zip"
	}
	return "_" + goos + "_" + goarch + ".tar.gz"
}

// VerifySnapshot verifies the snapshot in dist: ChecksumFile lists exactly one archive
// per target of Targets and nothing else, each archive's SHA-256 equals its line, and
// each archive holds LicenseFile and the dsh binary. Each binary is extracted to out as
// dsh-<goos>-<goarch>[.exe], and its embedded build information must record
// CGO_ENABLED=0, -trimpath and its target. Every target is checked; each failure
// names its target or archive.
func VerifySnapshot(dist, out string) ([]Artifact, error) {
	sums, err := readChecksums(filepath.Join(dist, ChecksumFile))
	if err != nil {
		return nil, err
	}
	var arts []Artifact
	var errs []error
	used := map[string]bool{}
	for _, t := range Targets {
		var name string
		for n := range sums {
			if strings.HasPrefix(n, "dsh_") && strings.HasSuffix(n, archiveSuffix(t.GOOS, t.GOARCH)) {
				if name != "" {
					errs = append(errs, fmt.Errorf("%s/%s: two archives in %s: %s and %s", t.GOOS, t.GOARCH, ChecksumFile, name, n))
				}
				name = n
			}
		}
		if name == "" {
			errs = append(errs, fmt.Errorf("%s/%s: no dsh_*%s archive in %s", t.GOOS, t.GOARCH, archiveSuffix(t.GOOS, t.GOARCH), ChecksumFile))
			continue
		}
		used[name] = true
		a, err := verifyArchive(filepath.Join(dist, name), sums[name], t.GOOS, t.GOARCH, out)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %s: %w", t.GOOS, t.GOARCH, name, err))
			continue
		}
		arts = append(arts, a)
	}
	var extra []string
	for n := range sums {
		if !used[n] {
			extra = append(extra, n)
		}
	}
	if len(extra) > 0 {
		slices.Sort(extra)
		errs = append(errs, fmt.Errorf("%s lists files that are not one archive per target: %s", ChecksumFile, strings.Join(extra, ", ")))
	}
	return arts, errors.Join(errs...)
}

// readChecksums parses a sha256sum-format file into name -> lowercase hex digest.
func readChecksums(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the checksum file: %w", err)
	}
	sums := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || len(f[0]) != 2*sha256.Size {
			return nil, fmt.Errorf("%s:%d: %q is not \"<sha256>  <file>\"", path, n, line)
		}
		if _, err := hex.DecodeString(f[0]); err != nil {
			return nil, fmt.Errorf("%s:%d: %q is not a SHA-256", path, n, f[0])
		}
		name := strings.TrimPrefix(f[1], "*")
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("%s:%d: %s is listed twice", path, n, name)
		}
		sums[name] = strings.ToLower(f[0])
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("%s lists no file", path)
	}
	return sums, sc.Err()
}

// MetadataFile is the file goreleaser writes into the dist directory naming the run's
// version, the {{ .Version }} GoReleaserConfig links into cmd/dsh as main.version.
const MetadataFile = "metadata.json"

// snapshotVersion reads the version of the snapshot in dist from MetadataFile: the
// version every rehearsed dsh prints with --version. -trimpath keeps -ldflags out of
// the binaries' build information, so the snapshot is the only place it is recorded.
func snapshotVersion(dist string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dist, MetadataFile))
	if err != nil {
		return "", fmt.Errorf("the snapshot's version: %w", err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("the snapshot's version: %s: %w", MetadataFile, err)
	}
	if m.Version == "" || strings.ContainsAny(m.Version, " \t\r\n") {
		return "", fmt.Errorf("the snapshot's version: %s names version %q", MetadataFile, m.Version)
	}
	return m.Version, nil
}

// verifyArchive checks archive against want, its SHA-256 from the checksum file, and
// extracts its dsh binary into out.
func verifyArchive(archive, want, goos, goarch, out string) (Artifact, error) {
	data, err := os.ReadFile(archive)
	if err != nil {
		return Artifact{}, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return Artifact{}, fmt.Errorf("SHA-256 %s, %s records %s", got, ChecksumFile, want)
	}
	exe := "dsh"
	if goos == "windows" {
		exe += ".exe"
	}
	files, err := archiveFiles(archive, data)
	if err != nil {
		return Artifact{}, err
	}
	if _, ok := files[LicenseFile]; !ok {
		return Artifact{}, fmt.Errorf("holds no %s (the licence notice)", LicenseFile)
	}
	binData, ok := files[exe]
	if !ok {
		return Artifact{}, fmt.Errorf("holds no %s", exe)
	}
	bin := filepath.Join(out, "dsh-"+goos+"-"+goarch)
	if goos == "windows" {
		bin += ".exe"
	}
	if err := os.WriteFile(bin, binData, 0o755); err != nil {
		return Artifact{}, err
	}
	a, err := inspect(bin)
	if err != nil {
		return Artifact{}, err
	}
	a.GOOS, a.GOARCH, a.Archive = goos, goarch, filepath.Base(archive)
	for k, v := range map[string]string{"CGO_ENABLED": "0", "-trimpath": "true", "GOOS": goos, "GOARCH": goarch} {
		if a.Settings[k] != v {
			return a, fmt.Errorf("%s: build setting %s = %q, want %q", exe, k, a.Settings[k], v)
		}
	}
	return a, nil
}

// archiveFiles returns the regular files at the top of a .zip or .tar.gz archive,
// by name.
func archiveFiles(name string, data []byte) (map[string][]byte, error) {
	files := map[string][]byte{}
	switch {
	case strings.HasSuffix(name, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.FileInfo().Mode().IsRegular() && !strings.Contains(f.Name, "/") {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				b, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return nil, err
				}
				files[f.Name] = b
			}
		}
	case strings.HasSuffix(name, ".tar.gz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && !strings.Contains(h.Name, "/") {
				b, err := io.ReadAll(tr)
				if err != nil {
					return nil, err
				}
				files[h.Name] = b
			}
		}
	default:
		return nil, fmt.Errorf("not a .zip or .tar.gz archive")
	}
	return files, nil
}

// inspect hashes bin and reads the build information the linker embedded in it.
func inspect(bin string) (Artifact, error) {
	data, err := os.ReadFile(bin)
	if err != nil {
		return Artifact{}, err
	}
	sum := sha256.Sum256(data)
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		return Artifact{}, fmt.Errorf("read build info of %s: %w", bin, err)
	}
	a := Artifact{Path: bin, SHA256: hex.EncodeToString(sum[:]), Version: info.Main.Version, Settings: map[string]string{}}
	for _, s := range info.Settings {
		a.Settings[s.Key] = s.Value
	}
	a.Revision, a.Modified = a.Settings["vcs.revision"], a.Settings["vcs.modified"]
	return a, nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
