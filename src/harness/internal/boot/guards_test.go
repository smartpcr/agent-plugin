package boot

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// guardedReport runs the real Guards step over a launch environment and secrets,
// reports one fatal error carrying text, and returns the report.
func guardedReport(t *testing.T, environ []string, store *secret.Store, text string) string {
	t.Helper()
	s := &State{
		Options: Options{Secrets: store},
		App:     launch.NewApp(),
		Clock:   wallClock{},
		Env:     launch.NewSnapshot(runtime.GOOS, environ),
		Home:    home.New(t.TempDir()),
	}
	if err := home.Ensure(s.Home); err != nil {
		t.Fatal(err)
	}
	if err := (Real{}).Guards(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := len(s.release) - 1; i >= 0; i-- {
			s.release[i]()
		}
	})
	path, first := s.Diag.Fatal("probe", errors.New(text))
	if !first || path == "" {
		t.Fatalf("Fatal = %q, %v; want a report", path, first)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// SP-11: with the process's secret store, the crash report masks the launch value
// of a named reference that no credential shape catches (GITHUB_PAT) and every
// value the store released; without a store the credential shapes still hold.
//
//dsh:test DH-T-HUMAN-12
func TestGuardsMaskTheStoresSecrets(t *testing.T) {
	const pat, released, shaped = "ghp-guard-dummy-pat", "sk-guard-dummy-released", "tok-guard-dummy-shaped"
	environ := []string{"GITHUB_PAT=" + pat, "ACME_TOKEN=" + shaped}
	store := secret.NewStore(secret.EnvSource(func(string) (string, bool) { return released, true }))
	store.Name(secret.MustParse("secret://github/pat"))
	if _, _, err := store.Resolve(context.Background(), secret.MustParse("secret://acme/key")); err != nil {
		t.Fatal(err)
	}
	text := "failed with " + pat + " and " + released + " and " + shaped

	report := guardedReport(t, environ, store, text)
	for _, v := range []string{pat, released, shaped} {
		if strings.Contains(report, v) {
			t.Errorf("the report with a store holds %q", v)
		}
	}
	if !strings.Contains(report, herr.Redacted) {
		t.Error("the report masks nothing")
	}

	report = guardedReport(t, environ, nil, text)
	if strings.Contains(report, shaped) {
		t.Error("without a store the credential-shaped value leaked")
	}
	if !strings.Contains(report, pat) {
		t.Error("without a store GITHUB_PAT is not known to be secret; the fallback changed")
	}
}
