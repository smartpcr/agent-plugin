package launch

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// files is an in-memory ReadFile.
func files(m map[string]string) ReadFile {
	return func(path string) ([]byte, error) {
		if s, ok := m[path]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	}
}

// TC-06a / DH-D-BOOT-2: in-memory launch facts with cwd /w and OS linux feed the
// expression scope; "${{ launch.os }}" yields linux.
//
//dsh:test DH-T-BOOT-6
func TestLaunchFactsFeedExpressionScope(t *testing.T) {
	d := Data{Profile: "web", Home: "/h", Cwd: "/w", Version: "1.0.0", OS: "linux", TTY: true}
	env := NewSnapshot("linux", []string{"EDITOR=vim", "DEEPSEEK_API_KEY=sk-x"})
	secret := func(name string) bool { return name == "DEEPSEEK_API_KEY" }
	e, err := expr.New(Scope(d, env, secret))
	if err != nil {
		t.Fatal(err)
	}
	node, err := loader.CodecFor("layer.json").Decode("layer.json", []byte(`"${{ launch.os }}"`))
	if err != nil {
		t.Fatal(err)
	}
	if node.Kind != loader.ExprNode {
		t.Fatalf("kind %v, want expression", node.Kind)
	}
	for source, want := range map[string]any{
		node.Text:        "linux",
		"os":             "linux",
		"cwd":            "/w",
		"launch.cwd":     "/w",
		"launch.profile": "web",
		"launch.version": "1.0.0",
		"launch.tty":     true,
		"env.EDITOR":     "vim",
	} {
		got, err := e.Eval(source, loader.Scope{Kind: loader.DisabledScope})
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", source, got, err, want)
		}
	}
	if err := e.Check("env.DEEPSEEK_API_KEY", loader.ConfigScope); err == nil {
		t.Error("a secret name was readable through env")
	}
	for _, hidden := range []string{"launch.words", "launch.args", "launch.user", "launch.overlays"} {
		if err := e.Check(hidden, loader.ConfigScope); err == nil {
			t.Errorf("%s is readable", hidden)
		}
	}
}

func TestBuildFacts(t *testing.T) {
	cwd, root := t.TempDir(), t.TempDir()
	req := Request{
		Args:     []string{"dsh", "web", "--safe"},
		Cwd:      cwd,
		OS:       "linux",
		User:     "alice",
		Build:    BuildIdentity{Version: "v1.2.3", Revision: "abc", GoVersion: "go1.26"},
		Home:     root,
		Profile:  "web",
		Safe:     true,
		Overlays: []string{"a.yml"},
		Words:    []string{"web"},
	}
	d, _, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	if d.Cwd != cwd || d.Home != root || d.OS != "linux" || d.User != "alice" || d.Version != "v1.2.3" ||
		d.Build.Revision != "abc" || !d.Safe || d.Profile != "web" || !slices.Equal(d.Args, req.Args) {
		t.Fatalf("facts %+v", d)
	}
	req.Args[0] = "changed"
	if d.Args[0] != "dsh" {
		t.Error("Data shares argv with the request")
	}
	if d, _, _ := Build(Request{Cwd: cwd, Home: root}); d.Version != DevelVersion || d.Build.Version != DevelVersion {
		t.Errorf("version %q, want %q", d.Version, DevelVersion)
	}
	if _, _, err := Build(Request{Home: root}); !herr.Has(err, ReasonFacts) {
		t.Errorf("no cwd: %v", err)
	}
}

// DH-T-BOOT-5: home precedence is option, then DSH_HOME; a blank variable is unset.
func TestBuildHomePrecedence(t *testing.T) {
	cwd, opt, viaEnv := t.TempDir(), t.TempDir(), t.TempDir()
	d, _, err := Build(Request{Cwd: cwd, Home: opt, Environ: []string{"DSH_HOME=" + viaEnv}})
	if err != nil || d.Home != opt {
		t.Fatalf("option: %q, %v", d.Home, err)
	}
	d, _, err = Build(Request{Cwd: cwd, Environ: []string{"DSH_HOME=" + viaEnv}})
	if err != nil || d.Home != viaEnv {
		t.Fatalf("DSH_HOME: %q, %v", d.Home, err)
	}
	d, _, err = Build(Request{Cwd: cwd, Environ: []string{"DSH_HOME=  "}})
	if err != nil || d.Home == "" || d.Home == cwd {
		t.Fatalf("blank DSH_HOME: %q, %v", d.Home, err)
	}
}

// DH-T-BOOT-4: precedence process, project file, home file, system (proxy only),
// each value reported with its source; the trusted view drops the project file.
func TestEnvPrecedence(t *testing.T) {
	cwd, root := t.TempDir(), t.TempDir()
	read := files(map[string]string{
		filepath.Join(cwd, ".env"):            "A=project\nB=project\n",
		filepath.Join(root, "config", ".env"): "B=home\nC=home\nHTTPS_PROXY=http://home:8080\n",
	})
	_, env, err := Build(Request{
		Cwd: cwd, Home: root, OS: "linux", ReadFile: read,
		Environ:     []string{"A=process"},
		SystemProxy: map[string]string{"HTTPS_PROXY": "http://sys", "NO_PROXY": "localhost", "EDITOR": "ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]struct {
		value string
		from  Source
	}{
		"A":           {"process", SourceProcess},
		"B":           {"project", SourceProject},
		"C":           {"home", SourceHome},
		"HTTPS_PROXY": {"http://home:8080", SourceHome},
		"NO_PROXY":    {"localhost", SourceSystem},
	} {
		v, from, ok := env.Lookup(name)
		if !ok || v != want.value || from != want.from {
			t.Errorf("%s = %q from %s (%v); want %q from %s", name, v, from, ok, want.value, want.from)
		}
	}
	if _, _, ok := env.Lookup("EDITOR"); ok {
		t.Error("a non-proxy name came from the system proxy setting")
	}
	if v, from, _ := env.Trusted().Lookup("B"); v != "home" || from != SourceHome {
		t.Errorf("trusted B = %q from %s; want the home file's", v, from)
	}
	if !slices.Equal(env.Names(), []string{"A", "B", "C", "HTTPS_PROXY", "NO_PROXY"}) {
		t.Errorf("names %v", env.Names())
	}
}

func TestEnvCaseOnWindows(t *testing.T) {
	env := NewSnapshot("windows", []string{"Path=C:\\bin", "PATH=second"})
	if v, _, ok := env.Lookup("PATH"); !ok || v != `C:\bin` {
		t.Errorf("PATH = %q", v)
	}
	if _, _, ok := NewSnapshot("linux", []string{"Path=x"}).Lookup("PATH"); ok {
		t.Error("linux names are case-sensitive")
	}
}

// DH-T-BOOT-4: a launch-only name in either file aborts naming the file and the
// name, never the value; proxy and endpoint names come from the home file only.
func TestDiscoveredFileCannotSetLaunchOnlyName(t *testing.T) {
	cwd, root := t.TempDir(), t.TempDir()
	project, homeEnv := filepath.Join(cwd, ".env"), filepath.Join(root, "config", ".env")
	cases := []struct {
		file, text, name string
	}{
		{project, "OK=1\nPATH=/evil-value\n", "PATH"},
		{project, "DSH_PROFILE=evil-value\n", "DSH_PROFILE"},
		{project, "DSH_HOME=evil-value\n", "DSH_HOME"},
		{project, "LD_PRELOAD=evil-value\n", "LD_PRELOAD"},
		{project, "https_proxy=evil-value\n", "https_proxy"},
		{project, "DEEPSEEK_BASE_URL=evil-value\n", "DEEPSEEK_BASE_URL"},
		{homeEnv, "BASH_ENV=evil-value\n", "BASH_ENV"},
		{homeEnv, "HOME=evil-value\n", "HOME"},
	}
	for _, c := range cases {
		_, env, err := Build(Request{Cwd: cwd, Home: root, ReadFile: files(map[string]string{c.file: c.text})})
		if !herr.Has(err, ReasonForbiddenName) || env != nil {
			t.Errorf("%s in %s: %v", c.name, c.file, err)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, c.name) || !strings.Contains(msg, ".env:") || strings.Contains(msg, "evil-value") {
			t.Errorf("message %q", msg)
		}
	}
	_, env, err := Build(Request{Cwd: cwd, Home: root, ReadFile: files(map[string]string{
		homeEnv: "HTTPS_PROXY=http://p\nDEEPSEEK_BASE_URL=http://e\n",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _ := env.Lookup("DEEPSEEK_BASE_URL"); v != "http://e" {
		t.Errorf("endpoint from the home file: %q", v)
	}
}

func TestParseDotenv(t *testing.T) {
	vars, err := ParseDotenv([]byte("\xef\xbb\xbf# comment\n\nexport A=1\nB = two words # note\nC='lit $x # no'\nD=\"q\\n\\\"x\\\"\"\r\nE=\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Var{{"A", "1", 3}, {"B", "two words", 4}, {"C", "lit $x # no", 5}, {"D", "q\n\"x\"", 6}, {"E", "", 7}}
	if !slices.Equal(vars, want) {
		t.Fatalf("got %+v", vars)
	}
	for _, bad := range []string{
		"KEY=\"sk-unterminated",
		"KEY='sk-unterminated",
		"KEY=\"sk-v\" trailing",
		"KEY=sk-v\"x",
		"1KEY=sk-v",
		"KEY sk-v",
		"KEY=a\nKEY=sk-v",
	} {
		_, err := ParseDotenv([]byte(bad))
		var pe *ParseError
		if err == nil || !strings.Contains(err.Error(), ":") {
			t.Errorf("%q parsed: %v", bad, err)
			continue
		}
		if strings.Contains(err.Error(), "sk-") {
			t.Errorf("%q: error shows the value: %v", bad, err)
		}
		if !herr.Has(err, ReasonBadFile) || !asParseError(err, &pe) {
			t.Errorf("%q: reason %v", bad, err)
		}
	}
}

func asParseError(err error, pe **ParseError) bool {
	p, ok := err.(*ParseError)
	*pe = p
	return ok
}

func TestBadFileNamesItsPath(t *testing.T) {
	cwd, root := t.TempDir(), t.TempDir()
	_, _, err := Build(Request{Cwd: cwd, Home: root, ReadFile: files(map[string]string{
		filepath.Join(cwd, ".env"): "OK=1\nKEY=\"sk-secret\n",
	})})
	if !herr.Has(err, ReasonBadFile) || !strings.Contains(err.Error(), ".env:2") || strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("%v", err)
	}
}

func TestClassify(t *testing.T) {
	for name, want := range map[string]Class{
		"DSH_ANYTHING": LaunchOnly, "dsh_x": LaunchOnly, "DYLD_INSERT_LIBRARIES": LaunchOnly, "Path": LaunchOnly,
		"HTTP_PROXY": HomeOnly, "no_proxy": HomeOnly, "OPENAI_BASE_URL": HomeOnly, "SSL_CERT_FILE": HomeOnly,
		"DEEPSEEK_API_KEY": Ordinary, "MY_FLAG": Ordinary,
	} {
		if got := Classify(name); got != want {
			t.Errorf("Classify(%s) = %d, want %d", name, got, want)
		}
	}
}

func TestProbe(t *testing.T) {
	req, err := Probe()
	if err != nil {
		t.Fatal(err)
	}
	if req.Cwd == "" || req.OS == "" || len(req.Args) == 0 || req.Build.Version == "" || req.ReadFile == nil {
		t.Fatalf("probe %+v", req)
	}
}
