package settings_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/settings"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

type probeConfig struct {
	Level string `json:"level,omitempty" dsh:"live"`
	Token string `json:"token,omitempty" dsh:"live,secret"`
	Port  int    `json:"port,omitempty"`
}

const profileText = `# written by dsh
- insert:
    - id: probe
      name: x.probe
      config:
        level: info
        token: hunter2
        port: 1
`

type fixture struct {
	home, profile, user string
	form                *settings.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	component := cordis.Define("x.probe", func(*cordis.Context, probeConfig) error { return nil })
	cat, err := catalog.Build([]catalog.Item{{Name: "x.probe", Component: component, Config: reflect.TypeFor[probeConfig]()}})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{home: filepath.Join(t.TempDir(), "home")}
	def := profile.Definition{Name: "probe"}
	f.profile, f.user = profile.LayerPath(f.home, def.Name), profile.UserLayerPath(f.home)
	write(t, f.profile, profileText)
	cs, err := config.NewService(config.ServiceOptions{Catalog: cat, Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
		return profile.Stack(def, profile.Sources{Home: f.home, Read: profile.ReadFile(read)})
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.form = settings.New(cs, cat)
	return f
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestDescribeListsLiveFieldsWithTheirLayer(t *testing.T) {
	f := newFixture(t)
	write(t, f.user, "- id: probe\n  merge:\n    level: warn\n")
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Entries) != 1 || form.Revision == "" {
		t.Fatalf("form = %+v", form)
	}
	got := map[string]settings.Field{}
	for _, field := range form.Entries[0].Fields {
		got[field.Path] = field
	}
	if len(got) != 2 {
		t.Errorf("live fields = %+v, want level and token only", form.Entries[0].Fields)
	}
	if l := got["config.level"]; string(l.Value) != `"warn"` || l.Layer != "user" {
		t.Errorf("level = %+v", l)
	}
	if tok := got["config.token"]; !tok.Secret || string(tok.Value) != `"<set>"` || tok.Layer != "profile" {
		t.Errorf("token = %+v", tok)
	}
}

// A live edit lands as one merge in the profile layer; an edit from the same old
// read is then stale and changes nothing.
func TestApplyThenStaleEdit(t *testing.T) {
	f := newFixture(t)
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	res := f.form.Apply(context.Background(), form.Revision, "probe", "config.level", json.RawMessage(`"debug"`))
	if res.Status != settings.Applied || res.Revision == form.Revision {
		t.Fatalf("result = %+v", res)
	}
	after := read(t, f.profile)
	if !strings.Contains(after, "# written by dsh") || !strings.Contains(after, "merge") || !strings.Contains(after, "debug") {
		t.Errorf("profile layer:\n%s", after)
	}
	if _, err := os.Stat(f.user); !os.IsNotExist(err) {
		t.Errorf("the user layer was written: %v", err)
	}
	res = f.form.Apply(context.Background(), form.Revision, "probe", "config.level", json.RawMessage(`"warn"`))
	if res.Status != settings.Stale || !herr.Has(res.Err, config.ReasonStale) {
		t.Fatalf("stale edit = %+v", res)
	}
	if read(t, f.profile) != after {
		t.Error("a stale edit changed the file")
	}
}

// A bad, a not-live and a shadowed edit are refused with the field path and no
// file change.
func TestRefusedEditsChangeNothing(t *testing.T) {
	cases := []struct {
		name, path, value string
		status            settings.Status
		reason            herr.Reason
	}{
		{"invalid", "config.token", `7`, settings.Failed, config.ReasonRejected},
		{"not live", "config.port", `2`, settings.Failed, settings.ReasonNotLive},
		{"shadowed", "config.level", `"debug"`, settings.Overridden, settings.ReasonOverridden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			write(t, f.user, "- id: probe\n  merge:\n    level: warn\n")
			before, user := read(t, f.profile), read(t, f.user)
			form, err := f.form.Describe()
			if err != nil {
				t.Fatal(err)
			}
			res := f.form.Apply(context.Background(), form.Revision, "probe", tc.path, json.RawMessage(tc.value))
			if res.Status != tc.status || !herr.Has(res.Err, tc.reason) {
				t.Fatalf("result = %+v", res)
			}
			if tc.status == settings.Failed && (len(res.Issues) == 0 || !strings.Contains(res.Issues[0].Path, "probe."+tc.path)) {
				t.Errorf("issues do not name the field: %+v", res.Issues)
			}
			if tc.status == settings.Overridden && res.Layer != "user" {
				t.Errorf("shadowing layer = %q", res.Layer)
			}
			if read(t, f.profile) != before || read(t, f.user) != user {
				t.Error("a refused edit changed a layer file")
			}
		})
	}
}
