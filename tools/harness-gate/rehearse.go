package main

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Artifact is one binary of the release rehearsal and what it embeds.
type Artifact struct {
	GOOS, GOARCH string
	Path         string
	SHA256       string
	Version      string // the main module version the go command stamped
	Revision     string // vcs.revision, empty when the build had no VCS
	Modified     string // vcs.modified
	Settings     map[string]string
}

// Name is the binary's file name, e.g. dsh-linux-amd64 or dsh-windows-arm64.exe.
func (a Artifact) Name() string { return filepath.Base(a.Path) }

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
// DH-ADR-15): it builds cmd/dsh for every target of Targets with GOWORK=off,
// CGO_ENABLED=0 and -trimpath into out and prints one line per binary,
// "<sha256>  <name>  version=... vcs.revision=... vcs.modified=...", in the format
// of sha256sum. It publishes nothing: no tag, no release, no upload; it does not
// run git and starts no command but go build. GOFLAGS is cleared so no user flag
// changes the binaries. Every target is attempted; each failure names its target.
func (c *Config) Rehearse(ctx context.Context, w io.Writer, out string) ([]Artifact, error) {
	mod, err := c.dshModule()
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(w, "rehearse: cmd/dsh of %s with GOWORK=off CGO_ENABLED=0 -trimpath into %s; nothing is published\n", c.rel(mod), out)
	var arts []Artifact
	var errs []error
	for _, t := range Targets {
		bin := filepath.Join(out, "dsh-"+t.GOOS+"-"+t.GOARCH)
		if t.GOOS == "windows" {
			bin += ".exe"
		}
		env := c.env("CGO_ENABLED=0", "GOFLAGS=", "GOOS="+t.GOOS, "GOARCH="+t.GOARCH)
		if err := c.goStep(ctx, w, mod, env, "build", "-trimpath", "-o", bin, "./cmd/dsh"); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", t.GOOS, t.GOARCH, err))
			continue
		}
		a, err := inspect(bin)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", t.GOOS, t.GOARCH, err))
			continue
		}
		a.GOOS, a.GOARCH = t.GOOS, t.GOARCH
		arts = append(arts, a)
	}
	for _, a := range arts {
		fmt.Fprintf(w, "%s  %s  version=%s vcs.revision=%s vcs.modified=%s\n", a.SHA256, a.Name(), a.Version, orNone(a.Revision), orNone(a.Modified))
	}
	if err := errors.Join(errs...); err != nil {
		return arts, err
	}
	fmt.Fprintf(w, "rehearse: %d binaries, nothing published\n", len(arts))
	return arts, nil
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
