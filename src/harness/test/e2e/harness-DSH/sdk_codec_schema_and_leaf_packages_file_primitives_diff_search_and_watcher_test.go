//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/search"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/textdiff/patch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

const (
	fpdswReplaceCount = 200
	fpdswSettle       = 500 * time.Millisecond
	fpdswDeadline     = 5 * time.Second
	fpdswQuiet        = 1200 * time.Millisecond
)

// fpdswSearchTree mirrors the fixture the committed list.golden was produced from.
var fpdswSearchTree = map[string]string{
	".gitignore":        "build/\n*.log\n!keep.log\n",
	".git/config":       "[core]\n",
	".git/info/exclude": "secret.txt\n",
	".hidden/h.txt":     "needle\n",
	"README.md":         "needle in the readme\n",
	"build/out.txt":     "needle\n",
	"build/sub/x.txt":   "needle\n",
	"docs/.gitignore":   "draft*\n",
	"docs/draft1.md":    "needle\n",
	"docs/guide.md":     "a\nneedle\nb\n",
	"secret.txt":        "needle\n",
	"src/app.log":       "needle\n",
	"src/build/gen.go":  "needle\n",
	"src/keep.log":      "needle\n",
	"src/main.go":       "package main\n\n// needle\nfunc main() {}\n",
	"src/zz.bin":        "needle\x00binary\n",
}

type filePrimitivesWorld struct {
	dirs []string

	// atomic replace
	path       string
	oldContent []byte
	newContent []byte
	stop       chan struct{}
	readerDone chan error
	reads      int
	replaceErr error

	// patch round-trip
	a, b   string
	result string

	// search
	srcDir  string
	listing []string
	summary search.Summary

	// watcher
	svc     *watch.Service
	w       watch.Watch
	notices chan error
	cancel  context.CancelFunc
	target  string
}

func (x *filePrimitivesWorld) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "fpdsw-*")
	if err != nil {
		return "", err
	}
	x.dirs = append(x.dirs, d)
	return d, nil
}

func (x *filePrimitivesWorld) cleanup() {
	if x.stop != nil {
		select {
		case <-x.stop:
		default:
			close(x.stop)
		}
	}
	if x.w != nil {
		_ = x.w.Close()
	}
	if x.cancel != nil {
		x.cancel()
	}
	if x.svc != nil {
		_ = x.svc.Close()
	}
	for _, d := range x.dirs {
		_ = os.RemoveAll(d)
	}
}

// --- Atomic replace ---

func (x *filePrimitivesWorld) aFileAndALoopingReader() error {
	dir, err := x.tempDir()
	if err != nil {
		return err
	}
	x.path = filepath.Join(dir, "state.json")
	x.oldContent = bytes.Repeat([]byte("old-content;"), 4096)
	x.newContent = bytes.Repeat([]byte("NEW-CONTENT-LONGER;"), 6000)
	if err := fsx.WriteFile(x.path, x.oldContent, fsx.Options{Perm: 0o644}); err != nil {
		return err
	}
	x.stop = make(chan struct{})
	x.readerDone = make(chan error, 1)
	go func() {
		n := 0
		for {
			select {
			case <-x.stop:
				x.reads = n
				x.readerDone <- nil
				return
			default:
			}
			got, err := fsx.ReadFile(x.path)
			if err != nil {
				x.reads = n
				x.readerDone <- fmt.Errorf("read %d failed: %w", n, err)
				return
			}
			n++
			if !bytes.Equal(got, x.oldContent) && !bytes.Equal(got, x.newContent) {
				x.reads = n
				x.readerDone <- errors.New("read a partial file of " + strconv.Itoa(len(got)) + " bytes")
				return
			}
		}
	}()
	return nil
}

func (x *filePrimitivesWorld) replaceManyTimes() error {
	for i := range fpdswReplaceCount {
		content := x.newContent
		if i%2 == 1 {
			content = x.oldContent
		}
		if err := fsx.Replace(x.path, fsx.Bytes(content), fsx.Options{Perm: 0o644}); err != nil {
			x.replaceErr = fmt.Errorf("replace %d: %w", i, err)
			break
		}
	}
	close(x.stop)
	return nil
}

func (x *filePrimitivesWorld) everyReadIsWhole() error {
	if x.replaceErr != nil {
		return x.replaceErr
	}
	select {
	case err := <-x.readerDone:
		if err != nil {
			return err
		}
	case <-time.After(10 * time.Second):
		return errors.New("reader goroutine did not stop")
	}
	if x.reads == 0 {
		return errors.New("reader observed no reads")
	}
	got, err := os.ReadFile(x.path)
	if err != nil {
		return err
	}
	// 200 replaces alternating new/old: the last one (index 199) wrote old.
	if !bytes.Equal(got, x.oldContent) {
		return fmt.Errorf("final content has %d bytes, want the old content", len(got))
	}
	entries, err := os.ReadDir(filepath.Dir(x.path))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), fsx.StagePrefix) {
			return fmt.Errorf("staged file left behind: %s", e.Name())
		}
	}
	return nil
}

// --- Patch round-trip ---

func (x *filePrimitivesWorld) inMemoryTexts() error {
	x.a = "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n\nfunc helper() int {\n\treturn 1\n}\n"
	x.b = "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tfmt.Println(\"hello, world\")\n\tos.Exit(0)\n}\n\nfunc helper() int {\n\treturn 2\n}\n"
	return nil
}

func (x *filePrimitivesWorld) makeAndApplyPatch() error {
	text, err := patch.Make("main.go", x.a, x.b)
	if err != nil {
		return fmt.Errorf("make: %w", err)
	}
	if !strings.HasPrefix(text, "*** Begin Patch") {
		return fmt.Errorf("not a model-format patch:\n%s", text)
	}
	p, err := patch.Parse(text)
	if err != nil {
		return fmt.Errorf("parse: %w\n%s", err, text)
	}
	if len(p.Hunks) != 1 {
		return fmt.Errorf("got %d hunks, want 1", len(p.Hunks))
	}
	got := x.a
	for _, h := range p.Hunks {
		if got, err = h.Apply(got); err != nil {
			return fmt.Errorf("apply: %w", err)
		}
	}
	x.result = got
	return nil
}

func (x *filePrimitivesWorld) resultEqualsB() error {
	if x.result != x.b {
		return fmt.Errorf("result differs from b:\n got %q\nwant %q", x.result, x.b)
	}
	return nil
}

// --- Search ---

func (x *filePrimitivesWorld) tempTreeWithGitignore() error {
	dir, err := x.tempDir()
	if err != nil {
		return err
	}
	for name, body := range fpdswSearchTree {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	x.srcDir = dir
	return nil
}

func (x *filePrimitivesWorld) searchListRuns() error {
	src, err := search.OpenDir(x.srcDir)
	if err != nil {
		return err
	}
	defer src.Close()
	x.listing, x.summary, err = search.List(context.Background(), search.FilesQuery{From: src})
	return err
}

func (x *filePrimitivesWorld) noBuildPaths() error {
	if len(x.listing) == 0 {
		return errors.New("empty listing")
	}
	for _, p := range x.listing {
		if strings.HasPrefix(p, "build/") || strings.Contains(p, "/build/") {
			return fmt.Errorf("ignored path listed: %s", p)
		}
	}
	if x.summary.Incomplete || x.summary.Matches != len(x.listing) {
		return fmt.Errorf("summary %+v for %d paths", x.summary, len(x.listing))
	}
	return nil
}

func (x *filePrimitivesWorld) listingEqualsGolden(rel string) error {
	// The package directory is src/harness/test/e2e/harness-DSH; the module root is three up.
	golden, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read committed golden: %w", err)
	}
	want := strings.ReplaceAll(string(golden), "\r\n", "\n")
	got := strings.Join(x.listing, "\n") + "\n"
	if got != want {
		return fmt.Errorf("listing differs from golden:\n got %q\nwant %q", got, want)
	}
	return nil
}

// --- Watcher ---

func (x *filePrimitivesWorld) watchOnTempDir() error {
	dir, err := x.tempDir()
	if err != nil {
		return err
	}
	x.target = filepath.Join(dir, "settings.json")
	if err := fsx.WriteFile(x.target, []byte("old"), fsx.Options{Perm: 0o644}); err != nil {
		return err
	}
	x.svc = watch.New(watch.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	x.cancel = cancel
	x.notices = make(chan error, 64)
	x.w, err = x.svc.Watch(ctx, watch.Target{Path: dir, Settle: fpdswSettle, Mode: watch.Auto}, func(err error) { x.notices <- err })
	if err != nil {
		return err
	}
	if m := x.w.Mode(); m != watch.Notify {
		return fmt.Errorf("watch on a local temp dir runs in mode %v, want Notify (OS notifications)", m)
	}
	select {
	case err := <-x.notices:
		return fmt.Errorf("unexpected notice before any change (err %v)", err)
	case <-time.After(fpdswQuiet):
	}
	return nil
}

func (x *filePrimitivesWorld) replaceAtomically() error {
	return fsx.WriteFile(x.target, []byte("new content"), fsx.Options{})
}

func (x *filePrimitivesWorld) exactlyOneNotice() error {
	select {
	case err := <-x.notices:
		if err != nil {
			return fmt.Errorf("notice carried %v", err)
		}
	case <-time.After(fpdswDeadline):
		return fmt.Errorf("no notice within %v", fpdswDeadline)
	}
	select {
	case err := <-x.notices:
		return fmt.Errorf("second notice for one save (err %v)", err)
	case <-time.After(fpdswQuiet):
	}
	got, err := os.ReadFile(x.target)
	if err != nil {
		return err
	}
	if string(got) != "new content" {
		return fmt.Errorf("target content %q", got)
	}
	return nil
}

func InitializeScenario_sdk_codec_schema_and_leaf_packages_file_primitives_diff_search_and_watcher(ctx *godog.ScenarioContext) {
	x := &filePrimitivesWorld{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		x.cleanup()
		return c, err
	})

	ctx.Step(`^a file in a temp directory and a reader goroutine reading it in a loop$`, x.aFileAndALoopingReader)
	ctx.Step(`^fsx\.Replace writes new content 200 times$`, x.replaceManyTimes)
	ctx.Step(`^every read returns either the old or the new full content$`, x.everyReadIsWhole)

	ctx.Step(`^in-memory texts a and b$`, x.inMemoryTexts)
	ctx.Step(`^a model-format patch from a to b is produced and applied to a$`, x.makeAndApplyPatch)
	ctx.Step(`^the result equals b byte-for-byte$`, x.resultEqualsB)

	ctx.Step(`^a temp tree with a \.gitignore excluding build/$`, x.tempTreeWithGitignore)
	ctx.Step(`^search\.List runs$`, x.searchListRuns)
	ctx.Step(`^no path under build/ is returned$`, x.noBuildPaths)
	ctx.Step(`^the listing equals the committed golden "([^"]+)"$`, x.listingEqualsGolden)

	ctx.Step(`^a watch on a temp directory$`, x.watchOnTempDir)
	ctx.Step(`^a file is replaced atomically$`, x.replaceAtomically)
	ctx.Step(`^exactly one change notice for that path arrives within a bounded deadline$`, x.exactlyOneNotice)
}

func TestE2E_sdk_codec_schema_and_leaf_packages_file_primitives_diff_search_and_watcher(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_sdk_codec_schema_and_leaf_packages_file_primitives_diff_search_and_watcher,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"sdk_codec_schema_and_leaf_packages_file_primitives_diff_search_and_watcher.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
