package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

const testValue = "sk-test-123"

// fakeSource returns one value and counts lookups.
type fakeSource struct {
	value   string
	lookups int
}

func (*fakeSource) Origin() string { return "fake" }

func (f *fakeSource) Lookup(_ context.Context, _ Ref) (Value, bool, error) {
	f.lookups++
	return NewValue(f.value), true, nil
}

var errUnauthorized = errors.New("unauthorized")

// TC-06a / DH-D-HUMAN-7: config naming secret://deepseek/api-key and a fake source
// returning the test value; the reference is resolved for one failing use; the
// value is used once and appears in no captured config dump, log line or error.
//
//dsh:test DH-T-HUMAN-10
func TestSecretResolvedPerUseAndRedacted(t *testing.T) {
	type config struct {
		APIKey Ref    `json:"apiKey"`
		Model  string `json:"model"`
	}
	var cfg config
	if err := json.Unmarshal([]byte(`{"apiKey":"secret://deepseek/api-key","model":"chat"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{value: testValue}
	store := NewStore(src)
	store.Name(cfg.APIKey)

	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	text := slog.New(slog.NewTextHandler(&logs, nil))

	uses := 0
	err := store.Use(context.Background(), cfg.APIKey, func(v Value) error {
		uses++
		if v.Reveal() != testValue {
			t.Fatalf("revealed %q", v.Reveal())
		}
		log.Info("calling provider", "key", v, "ref", cfg.APIKey)
		// A provider error that echoes the credential it was given.
		return fmt.Errorf("deepseek: %w: bad key %q (Bearer %s)", errUnauthorized, v.Reveal(), v.Reveal())
	})
	if uses != 1 || src.lookups != 1 {
		t.Fatalf("used %d times, looked up %d times; want 1, 1", uses, src.lookups)
	}
	if err == nil || !errors.Is(err, errUnauthorized) {
		t.Fatalf("error %v does not match the provider's", err)
	}
	if errors.Unwrap(err) != nil {
		t.Fatal("the masked error unwraps to the unmasked one")
	}
	log.Error("use failed", "err", err)
	text.Error("use failed", "err", err, "cfg", cfg)
	dump, _ := json.Marshal(cfg)

	captured := []string{
		err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), fmt.Sprintf("%q", err),
		logs.String(), string(dump), fmt.Sprintf("%+v", cfg),
	}
	for i, s := range captured {
		if strings.Contains(s, testValue) {
			t.Errorf("capture %d holds the value: %s", i, s)
		}
	}
	if !strings.Contains(string(dump), "secret://deepseek/api-key") || !strings.Contains(err.Error(), "secret://deepseek/api-key") {
		t.Errorf("the reference is not named: %s / %v", dump, err)
	}
	if !strings.Contains(logs.String(), Redacted) {
		t.Errorf("the log does not show the redaction marker: %s", logs.String())
	}

	// Per use: a second use reads the source again.
	if err := store.Use(context.Background(), cfg.APIKey, func(Value) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if src.lookups != 2 {
		t.Fatalf("lookups %d after the second use; the store cached the value", src.lookups)
	}
}

//dsh:test DH-T-HUMAN-10
func TestValueNeverPrints(t *testing.T) {
	v := NewValue(testValue)
	var b bytes.Buffer
	slog.New(slog.NewTextHandler(&b, nil)).Info("x", "v", v)
	j, _ := json.Marshal(struct{ V Value }{v})
	txt, _ := v.MarshalText()
	for _, s := range []string{
		fmt.Sprint(v), fmt.Sprintf("%v %+v %#v %s %q %x %d", v, v, v, v, v, v, v), b.String(), string(j), string(txt),
	} {
		if strings.Contains(s, testValue) || !strings.Contains(s, Redacted) {
			t.Errorf("printed %q", s)
		}
	}
}

// Configuration takes a reference only; a value in its place is refused without
// being echoed.
//
//dsh:test DH-T-HUMAN-10
func TestConfigRefusesValue(t *testing.T) {
	var cfg struct{ APIKey Ref }
	err := json.Unmarshal([]byte(`{"APIKey":"`+testValue+`"}`), &cfg)
	if err == nil || strings.Contains(err.Error(), testValue) {
		t.Fatalf("raw value accepted or echoed: %v", err)
	}
	for _, bad := range []string{"secret://", "secret://deepseek", "secret://DeepSeek/key", "secret://a/-b", "secret://a//b", "secret://a/b--c"} {
		if _, err := Parse(bad); !herr.Has(err, ReasonBadRef) {
			t.Errorf("Parse(%q) = %v", bad, err)
		}
	}
	r := MustParse("secret://deepseek/api-key")
	if r.EnvName() != "DEEPSEEK_API_KEY" || r.String() != "secret://deepseek/api-key" {
		t.Errorf("ref %s, env %s", r, r.EnvName())
	}
}

//dsh:test DH-T-HUMAN-11
func TestResolveOrderAndSources(t *testing.T) {
	root := t.TempDir()
	l := home.New(root)
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	ref := MustParse("secret://deepseek/api-key")
	env := map[string]string{}
	store := NewStore(
		EnvSource(func(n string) (string, bool) { v, ok := env[n]; return v, ok }),
		FileSource(l.Secrets()),
	)
	_, _, err := store.Resolve(context.Background(), ref)
	if !herr.Has(err, ReasonNotConfigured) || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
		t.Fatalf("unconfigured: %v", err)
	}

	dir := filepath.Join(l.Secrets(), "deepseek")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api-key"), []byte("sk-file-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, from, err := store.Resolve(context.Background(), ref)
	if err != nil || v.Reveal() != "sk-file-1" || from != "file" {
		t.Fatalf("file: %v from %s, %v", v.Reveal(), from, err)
	}
	env["DEEPSEEK_API_KEY"] = "sk-env-1"
	if v, from, _ := store.Resolve(context.Background(), ref); v.Reveal() != "sk-env-1" || from != "env" {
		t.Fatalf("env wins: %v from %s", v.Reveal(), from)
	}
	env["DEEPSEEK_API_KEY"] = ""
	if _, _, err := store.Resolve(context.Background(), ref); !herr.Has(err, ReasonEmpty) {
		t.Fatalf("empty: %v", err)
	}
	if got := store.MaskString("a sk-file-1 b sk-env-1"); got != "a "+Redacted+" b "+Redacted {
		t.Errorf("mask %q", got)
	}
}

//dsh:test DH-T-HUMAN-12
func TestIsSecretName(t *testing.T) {
	store := NewStore()
	store.Name(MustParse("secret://acme/login"))
	for name, want := range map[string]bool{
		"ACME_LOGIN": true, "DEEPSEEK_API_KEY": true, "GITHUB_TOKEN": true, "db_password": true,
		"EDITOR": false, "MONKEY": false,
	} {
		if got := store.IsSecretName(name); got != want {
			t.Errorf("IsSecretName(%s) = %v", name, got)
		}
	}
}

// stringErr and keyErr are value-typed errors that carry the credential in their
// fields, which %#v would print if the masked error kept them.
type stringErr string

func (e stringErr) Error() string { return string(e) }

type keyErr struct{ Key string }

func (e keyErr) Error() string { return "rejected key " + e.Key }

//dsh:test DH-T-HUMAN-10
func TestUseErrorFormatsMaskedForValueErrors(t *testing.T) {
	ref := MustParse("secret://deepseek/api-key")
	for _, cause := range []error{stringErr("bad key " + testValue), keyErr{Key: testValue}} {
		store := NewStore(&fakeSource{value: testValue})
		err := store.Use(context.Background(), ref, func(Value) error { return cause })
		if err == nil || !errors.Is(err, cause) {
			t.Fatalf("error %v does not match its cause", err)
		}
		var logs bytes.Buffer
		slog.New(slog.NewTextHandler(&logs, nil)).Error("use failed", "err", err)
		slog.New(slog.NewJSONHandler(&logs, nil)).Error("use failed", "err", err)
		for _, s := range []string{
			fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), fmt.Sprintf("%s", err),
			fmt.Sprintf("%q", err), fmt.Sprintf("%x", err), fmt.Sprintf("%#v", []error{err}),
			fmt.Sprintf("%+v", struct{ E error }{err}), logs.String(),
		} {
			if strings.Contains(s, testValue) || strings.Contains(s, fmt.Sprintf("%x", testValue)) {
				t.Errorf("%T: captured %s", cause, s)
			}
		}
	}
}

// link makes newname point at oldname: a symbolic link, or on Windows without the
// symlink privilege a directory junction.
func link(t *testing.T, oldname, newname string) {
	t.Helper()
	err := os.Symlink(oldname, newname)
	if err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		if out, jerr := exec.Command("cmd", "/c", "mklink", "/J", newname, oldname).CombinedOutput(); jerr == nil {
			return
		} else {
			t.Skipf("cannot link: %v; junction: %v %s", err, jerr, out)
		}
	}
	t.Skipf("cannot link: %v", err)
}

// A link inside secrets/ that leads outside it is refused, even when the file it
// reaches is private: the value must come from under the home.
func TestFileSourceRefusesLinkEscapingSecrets(t *testing.T) {
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	outside := home.New(t.TempDir())
	if err := home.Ensure(outside); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside.Secrets(), "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "api-key"), []byte(testValue), 0o600); err != nil {
		t.Fatal(err)
	}
	// Directly, the outside file passes the privacy check, so only containment
	// refuses it below.
	if _, _, err := NewStore(FileSource(outside.Secrets())).Resolve(context.Background(), MustParse("secret://elsewhere/api-key")); err != nil {
		t.Fatalf("outside file read directly: %v", err)
	}
	link(t, target, filepath.Join(l.Secrets(), "deepseek"))

	v, _, err := NewStore(FileSource(l.Secrets())).Resolve(context.Background(), MustParse("secret://deepseek/api-key"))
	if !herr.Has(err, ReasonSource) || v.Reveal() != "" {
		t.Fatalf("escaping link resolved: %q, %v", v.Reveal(), err)
	}
	if strings.Contains(err.Error(), testValue) {
		t.Fatalf("error holds the value: %v", err)
	}
}
