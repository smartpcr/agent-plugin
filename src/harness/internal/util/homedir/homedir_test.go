package homedir

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

func fakeHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	userHome = func() (string, error) { return h, nil }
	t.Cleanup(func() { userHome = defaultUserHome })
	return h
}

var defaultUserHome = userHome

func env(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// TestResolveOrder is the homedir part of DH-T-UTIL-8.
func TestResolveOrder(t *testing.T) {
	h := fakeHome(t)
	explicit := filepath.Join(t.TempDir(), "x")
	fromEnv := filepath.Join(t.TempDir(), "y")
	for _, tc := range []struct {
		name     string
		explicit string
		env      map[string]string
		want     string
	}{
		{"explicit beats DSH_HOME", explicit, map[string]string{Env: fromEnv}, explicit},
		{"DSH_HOME beats default", "", map[string]string{Env: fromEnv}, fromEnv},
		{"default", "", nil, filepath.Join(h, ".dsh")},
		{"blank explicit is unset", "  ", map[string]string{Env: fromEnv}, fromEnv},
		{"blank DSH_HOME is unset", "", map[string]string{Env: " \t"}, filepath.Join(h, ".dsh")},
		{"tilde explicit", "~/w", nil, filepath.Join(h, "w")},
		{"tilde DSH_HOME", "", map[string]string{Env: "~"}, h},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.explicit, env(tc.env))
			if err != nil || got != tc.want {
				t.Fatalf("Resolve = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if got, _ := Resolve("", nil); got != filepath.Join(h, ".dsh") {
		t.Fatalf("nil lookup: %q", got)
	}
}

func TestExpandTilde(t *testing.T) {
	h := fakeHome(t)
	for in, want := range map[string]string{
		"~":        h,
		"~/a/b":    filepath.Join(h, "a", "b"),
		"~alice/x": "~alice/x",
		"a/~/b":    "a/~/b",
		"":         "",
	} {
		if got, err := ExpandTilde(in); err != nil || got != want {
			t.Errorf("ExpandTilde(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestNoUserHome(t *testing.T) {
	userHome = func() (string, error) { return "", errors.New("unset") }
	t.Cleanup(func() { userHome = defaultUserHome })
	if _, err := Default(); herr.Kind(err) != ReasonNoUserHome {
		t.Fatalf("Default err = %v", err)
	}
	if !ReasonNoUserHome.Valid() {
		t.Fatal("reason is not of the form <domain>/<reason>")
	}
	if got, err := Resolve("", env(map[string]string{Env: filepath.Join(t.TempDir(), "h")})); err != nil || got == "" {
		t.Fatalf("DSH_HOME should not need the user home: %q, %v", got, err)
	}
}

// TestDisplay: Display never prints an absolute machine path.
func TestDisplay(t *testing.T) {
	h := fakeHome(t)
	other := filepath.Join(t.TempDir(), "proj", "file.txt")
	for in, want := range map[string]string{
		h:                              "~",
		filepath.Join(h, ".dsh"):       "~/.dsh",
		filepath.Join(h, "a", "b.txt"): "~/a/b.txt",
		other:                          ".../file.txt",
		filepath.Join("rel", "x"):      "rel/x",
		"":                             "",
	} {
		got := Display(in)
		if got != want {
			t.Errorf("Display(%q) = %q, want %q", in, got, want)
		}
		if in != "" && filepath.IsAbs(in) && (strings.Contains(got, filepath.ToSlash(filepath.Dir(in))) || filepath.IsAbs(got)) {
			t.Errorf("Display(%q) = %q shows a machine path", in, got)
		}
	}
	if got := Display(h + "x"); got == "~" || strings.HasPrefix(got, "~/") {
		t.Errorf("a sibling with a common prefix is shown under ~: %q", got)
	}
}
