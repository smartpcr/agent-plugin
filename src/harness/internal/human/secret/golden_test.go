package secret_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	texttemplate "text/template"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// goldenFake is the obvious fake every printed form below holds.
const goldenFake = "sk-golden-FAKE-0001"

// noTime drops slog's time attribute, so the golden is stable.
func noTime(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

// DH-T-HUMAN-10 (golden): a secret.Value through every fmt verb, encoding/json,
// log/slog text and JSON, text/template and html/template (text, attribute and
// script contexts), a lease, and the parse errors of the .env source print
// "[redacted]" or name a position, never the value. The store file's parse errors
// are file's golden (secret/file TestStoreParseErrorsGolden).
//
//dsh:test DH-T-HUMAN-10
func TestPrintedFormsGolden(t *testing.T) {
	v := secret.NewValue(goldenFake)
	var lines []string
	add := func(name, out string) { lines = append(lines, name+": "+strings.TrimRight(out, "\n")) }

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%10s", "%-12v", "%T"} {
		add("fmt "+verb, fmt.Sprintf(verb, v))
	}
	add("fmt in a struct %+v", fmt.Sprintf("%+v", struct{ Key secret.Value }{v}))
	add("fmt in a slice %#v", fmt.Sprintf("%#v", []secret.Value{v}))
	add("fmt Sprint", fmt.Sprint(v))

	j, err := json.Marshal(struct {
		Key  secret.Value
		Keys map[string]secret.Value
	}{v, map[string]secret.Value{"a": v}})
	if err != nil {
		t.Fatal(err)
	}
	add("encoding/json", string(j))
	txt, _ := v.MarshalText()
	add("MarshalText", string(txt))

	var b bytes.Buffer
	slog.New(slog.NewTextHandler(&b, &slog.HandlerOptions{ReplaceAttr: noTime})).Info("call", "key", v)
	add("slog text", b.String())
	b.Reset()
	slog.New(slog.NewJSONHandler(&b, &slog.HandlerOptions{ReplaceAttr: noTime})).Info("call", "key", v, "group", slog.GroupValue(slog.Any("inner", v)))
	add("slog json", b.String())

	b.Reset()
	tt := texttemplate.Must(texttemplate.New("t").Parse(`{{.}} {{printf "%q" .}} {{printf "%#v" .}}`))
	if err := tt.Execute(&b, v); err != nil {
		t.Fatal(err)
	}
	add("text/template", b.String())
	b.Reset()
	ht := htmltemplate.Must(htmltemplate.New("h").Parse(`<p title="{{.}}">{{.}}</p><script>var k = {{.}};</script>`))
	if err := ht.Execute(&b, v); err != nil {
		t.Fatal(err)
	}
	add("html/template", b.String())

	store := secret.NewStore(secret.EnvSource(func(string) (string, bool) { return goldenFake, true }))
	lease, err := store.Acquire(context.Background(), secret.MustParse("secret://golden/api-key"))
	if err != nil {
		t.Fatal(err)
	}
	add("lease %v", fmt.Sprintf("%v", lease))
	add("lease %+v of its contents", fmt.Sprintf("%+v", lease.Value()))
	b.Reset()
	slog.New(slog.NewTextHandler(&b, &slog.HandlerOptions{ReplaceAttr: noTime})).Info("held", "lease", lease)
	add("lease slog", b.String())
	lease.Release()

	dir := t.TempDir()
	for _, c := range []struct{ name, text string }{
		{"no name", "=" + goldenFake},
		{"bad name", goldenFake + "=1"},
		{"no equals", "GOLDEN_API_KEY"},
		{"unterminated single", "GOLDEN_API_KEY='" + goldenFake},
		{"unterminated double", `GOLDEN_API_KEY="` + goldenFake},
		{"after the quote", `GOLDEN_API_KEY="x" ` + goldenFake},
		{"unknown escape", `GOLDEN_API_KEY="` + goldenFake + `\q"`},
		{"quote inside", `GOLDEN_API_KEY=` + goldenFake + `"`},
		{"assigned twice", "GOLDEN_API_KEY=" + goldenFake + "\nGOLDEN_API_KEY=" + goldenFake},
	} {
		path := filepath.Join(dir, "case.env")
		if err := os.WriteFile(path, []byte("# a comment\n"+c.text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := secret.DotEnvSource(path, secret.OriginProjectEnv).Lookup(context.Background(), secret.MustParse("secret://golden/api-key"))
		if err == nil {
			t.Fatalf(".env %s: no error", c.name)
		}
		add(".env "+c.name, err.Error())
	}

	got := strings.Join(lines, "\n") + "\n"
	if strings.Contains(got, "FAKE") || strings.Contains(got, fmt.Sprintf("%x", goldenFake)) {
		t.Fatalf("a printed form holds the value:\n%s", got)
	}
	golden.Assert(t, "printed-forms", got)
}
