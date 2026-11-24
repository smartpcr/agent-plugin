//go:build windows

package local_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// TestCancellationDuringPublishRetryPublishesNothing (filesystem.md 6.3): another
// program holds the target open without delete sharing, so the first rename fails
// and fsx waits to retry. In that wait the handle is released and the call
// cancelled (or its deadline passes). The retry would now succeed, but the
// re-check before it observes the context: fs/aborted or fs/timeout, the old
// bytes in place and no staged file left.
func TestCancellationDuringPublishRetryPublishesNothing(t *testing.T) {
	for _, end := range []struct {
		name string
		code herr.Reason
		ctx  func() (context.Context, func(), func())
	}{
		{name: "cancelled", code: fs.CodeAborted, ctx: func() (context.Context, func(), func()) {
			ctx, cancel := context.WithCancel(context.Background())
			return ctx, cancel, cancel
		}},
		{name: "deadline", code: fs.CodeTimeout, ctx: func() (context.Context, func(), func()) {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			return ctx, func() { <-ctx.Done() }, cancel
		}},
	} {
		for _, versioned := range []bool{false, true} {
			name := end.name + "/no-guard"
			if versioned {
				name = end.name + "/replace-if-version"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				p := sandboxtest.Policy(sandbox.WorkspaceWrite, dir)
				f := filepath.Join(dir, "f")
				l := local.New()
				writeFile(t, f, "old")
				var guard fs.Guard = fs.NoGuard
				if versioned {
					v, _ := readVersion(t, l, f)
					guard = fs.ReplaceIfVersion{Version: v}
				}
				h := holdOpen(t, f)
				released := false
				t.Cleanup(func() {
					if !released {
						_ = windows.CloseHandle(h)
					}
				})
				ctx, stop, cancel := end.ctx()
				defer cancel()
				checks := 0
				t.Cleanup(local.AroundRecheck(func(n int) {
					checks = n + 1
					if n == 1 {
						// The first rename was refused while the handle was held:
						// release it and end the call before the retry.
						released = true
						_ = windows.CloseHandle(h)
						stop()
					}
				}))

				_, err := l.Write(ctx, fs.Ref{Path: f}, []byte("new"), guard, p)
				wantCode(t, "write ended during the publication retry", err, end.code)
				if checks < 2 {
					t.Fatalf("BeforePublish ran %d times; the first rename was not blocked and retried", checks)
				}
				if b, rerr := os.ReadFile(f); string(b) != "old" {
					t.Fatalf("file holds %q, %v; want the old bytes", b, rerr)
				}
				for _, e := range entries(t, dir) {
					if strings.HasPrefix(filepath.Base(e), fsx.StagePrefix) {
						t.Fatalf("staged file left behind: %s", e)
					}
				}
			})
		}
	}
}

// holdOpen opens path for reading without delete sharing, as an editor or a virus
// scanner does, so a rename over it fails until the handle closes.
func holdOpen(t *testing.T, path string) windows.Handle {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
