package modcheck

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Pseudo is a parsed pseudo-version: the revision a requirement is pinned to.
type Pseudo struct {
	Time time.Time // committer time of the revision, UTC
	Rev  string    // 12-hex-digit revision prefix
}

// pseudoPattern matches the three pseudo-version forms of the go command:
// vX.0.0-yyyymmddhhmmss-rev, vX.Y.Z-pre.0.yyyymmddhhmmss-rev and
// vX.Y.(Z+1)-0.yyyymmddhhmmss-rev.
var pseudoPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+-(?:[0-9A-Za-z.-]*\.)?(?:0\.)?(\d{14})-([0-9a-f]{12})(?:\+incompatible)?$`)

var releasePattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// ParsePseudo parses a pseudo-version. The zero placeholder
// (v0.0.0-00010101000000-000000000000), which names no revision, is rejected.
func ParsePseudo(version string) (Pseudo, error) {
	m := pseudoPattern.FindStringSubmatch(version)
	if m == nil {
		return Pseudo{}, fmt.Errorf("%s is not a pseudo-version", version)
	}
	at, err := time.Parse("20060102150405", m[1])
	if err != nil {
		return Pseudo{}, fmt.Errorf("%s: bad timestamp: %w", version, err)
	}
	if at.Year() < 2000 || m[2] == strings.Repeat("0", 12) {
		return Pseudo{}, fmt.Errorf("%s is a placeholder that names no revision", version)
	}
	return Pseudo{Time: at.UTC(), Rev: m[2]}, nil
}

// GoModHash is the go.sum "h1:" hash of a go.mod file (dirhash Hash1 over the single
// file "go.mod"), so a pinned revision's go.mod can be checked against go.sum offline.
func GoModHash(data []byte) string {
	fileSum := sha256.Sum256(data)
	summary := sha256.Sum256([]byte(fmt.Sprintf("%x  %s\n", fileSum, "go.mod")))
	return "h1:" + base64.StdEncoding.EncodeToString(summary[:])
}

// Sums maps "path version" (and "path version/go.mod") to its go.sum hash.
type Sums map[string]string

// ParseSums reads a go.sum file.
func ParseSums(data []byte) Sums {
	sums := Sums{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		if f := strings.Fields(scanner.Text()); len(f) == 3 {
			sums[f[0]+" "+f[1]] = f[2]
		}
	}
	return sums
}

// Pin is one product-module requirement of src/harness.
type Pin struct {
	Module    Module
	Version   string
	Directive Directive
}

// Pins returns src/harness's requirements on the other product modules.
func Pins(harnessMod []byte) ([]Pin, error) {
	file, err := Parse("src/harness/go.mod", harnessMod)
	if err != nil {
		return nil, err
	}
	var pins []Pin
	for _, d := range file.All("require") {
		fields := strings.Fields(d.Args)
		for _, m := range Modules {
			if len(fields) >= 2 && fields[0] == m.Path {
				pins = append(pins, Pin{Module: m, Version: fields[1], Directive: d})
			}
		}
	}
	return pins, nil
}

// CheckPins requires every product-module requirement of src/harness to name a real
// revision -- a release tag or a pseudo-version, never the zero placeholder -- and to
// have both of its go.sum lines, so a GOWORK=off build can resolve it (RM-4).
func CheckPins(harnessMod, harnessSum []byte) error {
	pins, err := Pins(harnessMod)
	if err != nil {
		return err
	}
	sums := ParseSums(harnessSum)
	var errs []error
	for _, pin := range pins {
		if !releasePattern.MatchString(pin.Version) {
			if _, err := ParsePseudo(pin.Version); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w (RM-4)", pin.Directive, err))
				continue
			}
		}
		for _, key := range []string{pin.Module.Path + " " + pin.Version, pin.Module.Path + " " + pin.Version + "/go.mod"} {
			if sums[key] == "" {
				errs = append(errs, fmt.Errorf("%s: src/harness/go.sum has no line for %s (RM-4)", pin.Directive, key))
			}
		}
	}
	return errors.Join(errs...)
}
