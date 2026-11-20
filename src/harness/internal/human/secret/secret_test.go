package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
