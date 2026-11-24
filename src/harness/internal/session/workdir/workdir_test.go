package workdir_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir/workdirtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

func TestCodeIsDeclared(t *testing.T) {
	if !workdir.CodeUnavailable.Valid() || workdir.CodeUnavailable.Domain() != "workdir" {
		t.Fatalf("code %q is malformed", workdir.CodeUnavailable)
	}
}

func TestRecordShape(t *testing.T) {
	root := t.TempDir()
	w := workdir.WorkingDirectory{Path: filepath.Join(root, "sub"), Workspace: "ws1", Root: root, Revision: 3, State: workdir.Available}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(w)
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"path", "workspace", "root", "revision", "state"} {
		if _, ok := m[k]; !ok {
			t.Errorf("record has no %q field: %s", k, b)
		}
	}
	for _, bad := range []workdir.WorkingDirectory{
		{Path: "rel", Root: root, State: workdir.Available},
		{Path: filepath.Dir(root), Root: root, State: workdir.Available},
		{Path: root, Root: root, State: "gone"},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v validated", bad)
		}
	}
}

func TestFakeAccess(t *testing.T) {
	root := t.TempDir()
	l := workdirtest.New()
	if _, err := l.Access("nobody"); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("unknown session: %v", err)
	}
	a := l.Set("s", root, root)
	got, err := l.Access("s")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := got.Path(); err != nil || p != root {
		t.Fatalf("Path = %q, %v", p, err)
	}
	a.Gone()
	if _, err := got.Root(); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("Root of a gone directory: %v", err)
	}
	if w, _ := got.Current(); w.Revision != 2 || w.State != workdir.Unavailable {
		t.Fatalf("record after Gone = %+v", w)
	}
}
