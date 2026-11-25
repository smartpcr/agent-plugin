package config_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// With reload disabled no Watch runs over the service: a layer file saved under the
// running tree changes nothing (no fiber restarts, the accepted revision stays) until
// a restart composes it, while an Apply still re-reads every layer under the lock,
// reconciles, and so also applies the other file's current content.
//
//dsh:test DH-T-BOOT-18
func TestReloadDisabledFileEditWaitsWhileApplyReconciles(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	ldr, cs := boot(t, h, p) // no Watch: the profile does not reload
	starts, revision := p.starts.Load(), cs.Revision()

	temphome.Write(t, profile.UserLayerPath(h.root), "- id: probe\n  merge:\n    level: debug\n")
	if got := effective(t, ldr, "probe"); got.Level != "info" {
		t.Errorf("probe after the file edit = %+v, want level info until restart", got)
	}
	if n := p.starts.Load(); n != starts || cs.Revision() != revision || cs.Pending() {
		t.Errorf("the file edit changed the running tree: starts %d -> %d, revision changed %v, pending %v", starts, n, cs.Revision() != revision, cs.Pending())
	}

	// A restart composes the saved file.
	restarted, _ := boot(t, h, newProbes(t))
	if got := effective(t, restarted, "probe"); got.Level != "debug" {
		t.Errorf("probe after a restart = %+v, want level debug", got)
	}

	// Apply edits another entry against the layers on disk now and reconciles the
	// whole tree, the user file's edit included.
	draft, err := cs.Draft()
	if err != nil {
		t.Fatal(err)
	}
	if draft.Revision == revision {
		t.Fatal("the draft did not read the user file's edit")
	}
	rev, err := cs.Apply(context.Background(), config.Edit{Revision: draft.Revision, Ops: []loader.Op{{Kind: loader.OpMerge, ID: "other", Patch: json.RawMessage(`{"port":9}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := effective(t, ldr, "other"); got.Port != 9 {
		t.Errorf("other after Apply = %+v, want port 9", got)
	}
	if got := effective(t, ldr, "probe"); got.Level != "debug" {
		t.Errorf("probe after Apply = %+v, want the user file's level debug", got)
	}
	if cs.Pending() || cs.Current().Revision != rev {
		t.Errorf("after Apply pending %v, current revision %s, want %s", cs.Pending(), short(cs.Current().Revision), short(rev))
	}
}

// A change saved between boot's read of the layers and readiness is applied after
// it: the service is built from the bytes boot read (Baseline), so the first Watch
// poll sees the file differ and reloads it.
//
//dsh:test DH-T-BOOT-18
func TestChangeSavedBeforeReadinessAppliesAtFirstPoll(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	baseline := map[string][]byte{}
	record := func(path string) ([]byte, error) {
		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			baseline[path] = nil
		case err == nil:
			baseline[path] = bytes.Clone(data)
		}
		return data, err
	}
	if _, _, err := h.stack(record); err != nil {
		t.Fatal(err)
	}
	user := profile.UserLayerPath(h.root)
	if _, read := baseline[user]; !read {
		t.Fatalf("boot's read did not consult the user layer %s: %v", user, baseline)
	}

	// Saved after boot's read, before the tree is ready.
	temphome.Write(t, user, "- id: probe\n  merge:\n    level: debug\n")
	root, ldr, cs := bootRoot(t, h, p, config.ServiceOptions{Baseline: baseline})
	if got := effective(t, ldr, "probe"); got.Level != "info" {
		t.Fatalf("probe at boot = %+v, want the level boot read (info)", got)
	}

	reloaded := make(chan struct{}, 1)
	if _, err := loader.Reloaded.Prepend(root, func(_ *cordis.Context, n loader.ReloadNotice) error {
		if n.Loader == ldr {
			select {
			case reloaded <- struct{}{}:
			default:
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		cs.Watch(ctx, time.Millisecond)
	}()
	t.Cleanup(func() {
		cancel()
		<-watched
	})
	select {
	case <-reloaded:
	case <-time.After(20 * time.Second):
		t.Fatal("the first polls never applied the change saved before readiness")
	}
	if got := effective(t, ldr, "probe"); got.Level != "debug" {
		t.Errorf("probe after the first poll = %+v, want level debug", got)
	}
}
