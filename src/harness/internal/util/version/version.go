// Package version is the harness's one meaning of a version and a version range
// (DH-D-UTIL-4, docs/harness/libs/semver.md DH-ADR-SEMVER-1). No other package
// imports the semantic-version module; versions travel as Version, not as strings.
//
// A version is accepted only in strict SemVer 2.0.0 spelling: `1.2.3`,
// `1.2.3-rc.1`, `1.2.3+build.5`; never `v1.2.3`, `1.2` or `01.2.3`. A range uses
// comparison operators, `^`, `~`, `x` wildcards, hyphen ranges, a space for "and"
// and `||` for "or"; prereleases always count, so `0.3.0-rc.1` satisfies
// `>=0.2.0`.
//
//dsh:untrusted-parser versions and ranges arrive from plugin manifests and vendor listings
package version

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Version is one strictly spelled version. The zero value is no version: it
// satisfies no range and compares below every version.
type Version struct {
	text string
	v    *semver.Version
}

// Error reports a version or range that was refused.
type Error struct {
	Text   string
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("version: %q %s", e.Text, e.Reason) }

// Parse reads a strictly spelled version; String returns s unchanged.
func Parse(s string) (Version, error) {
	if s == "" {
		return Version{}, &Error{Text: s, Reason: "is empty"}
	}
	if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
		return Version{}, &Error{Text: s, Reason: "has a 'v' prefix; write " + strings.TrimLeft(s, "vV")}
	}
	v, err := semver.StrictNewVersion(s)
	if err != nil {
		return Version{}, &Error{Text: s, Reason: "is not a strict semantic version (MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD])"}
	}
	return Version{text: s, v: v}, nil
}

// MustParse is Parse for versions written in Go source; it panics on an error.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// String returns the text the version was parsed from.
func (v Version) String() string { return v.text }

// IsZero reports whether v is the zero value.
func (v Version) IsZero() bool { return v.v == nil }

// Compare orders by SemVer precedence: -1, 0 or +1. Build metadata is ignored, and a
// prerelease is older than its release.
func (v Version) Compare(o Version) int {
	switch {
	case v.v == nil && o.v == nil:
		return 0
	case v.v == nil:
		return -1
	case o.v == nil:
		return 1
	}
	return v.v.Compare(o.v)
}

// Exact reports whether v and o are the same text, build metadata included.
func (v Version) Exact(o Version) bool { return v.v != nil && v.text == o.text }

// IsPrerelease reports whether v has a prerelease part.
func (v Version) IsPrerelease() bool { return v.v != nil && v.v.Prerelease() != "" }

// AtLeast reports whether current is required or newer. A zero version on either
// side is never enough.
func AtLeast(current, required Version) bool {
	return current.v != nil && required.v != nil && current.Compare(required) >= 0
}

// SortNewestFirst sorts vs by descending precedence; equal precedence keeps order.
func SortNewestFirst(vs []Version) {
	slices.SortStableFunc(vs, func(a, b Version) int { return b.Compare(a) })
}

// MarshalText writes the version's text.
func (v Version) MarshalText() ([]byte, error) {
	if v.v == nil {
		return nil, errors.New("version: the zero version has no text")
	}
	return []byte(v.text), nil
}

// UnmarshalText reads a version strictly.
func (v *Version) UnmarshalText(b []byte) error {
	p, err := Parse(string(b))
	if err != nil {
		return err
	}
	*v = p
	return nil
}

// Range is one parsed range. The zero value satisfies nothing.
type Range struct {
	text string
	c    *semver.Constraints
}

// ParseRange reads a range. The empty range is an error, as are the comma and `!=`
// forms the module accepts but npm ranges do not have.
func ParseRange(s string) (Range, error) {
	switch {
	case strings.TrimSpace(s) == "":
		return Range{}, &Error{Text: s, Reason: "is an empty range"}
	case strings.Contains(s, ","):
		return Range{}, &Error{Text: s, Reason: "uses ','; write a space for \"and\""}
	case strings.Contains(s, "!="):
		return Range{}, &Error{Text: s, Reason: "uses '!=', which is not in the range dialect"}
	}
	c, err := semver.NewConstraint(s)
	if err != nil {
		return Range{}, &Error{Text: s, Reason: "is not a version range: " + err.Error()}
	}
	c.IncludePrerelease = true
	return Range{text: s, c: c}, nil
}

// String returns the text the range was parsed from.
func (r Range) String() string { return r.text }

// Satisfies reports whether v is in r; prereleases count.
func (r Range) Satisfies(v Version) bool {
	return r.c != nil && v.v != nil && r.c.Check(v.v)
}

// MarshalText writes the range's text.
func (r Range) MarshalText() ([]byte, error) {
	if r.c == nil {
		return nil, errors.New("version: the zero range has no text")
	}
	return []byte(r.text), nil
}

// UnmarshalText reads a range.
func (r *Range) UnmarshalText(b []byte) error {
	p, err := ParseRange(string(b))
	if err != nil {
		return err
	}
	*r = p
	return nil
}
