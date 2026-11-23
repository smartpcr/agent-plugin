//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

const cfgTimeout = 5 * time.Second

func cfgWithin(what string, fn func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-time.After(cfgTimeout):
		return fmt.Errorf("%s did not return within %s", what, cfgTimeout)
	}
}

func cfgWait(fiber *cordis.Fiber) error {
	var err error
	if timeout := cfgWithin("wait "+fiber.Name(), func() { err = fiber.Wait() }); timeout != nil {
		return timeout
	}
	return err
}

type cfgSized struct {
	N int `json:"n"`
}

type cfgRetry struct {
	Retries int `json:"retries"`
}

type configViewsWorld struct {
	roots []*cordis.Context

	// candidate leak
	fiber      *cordis.Fiber
	loads      atomic.Int32
	hookMu     sync.Mutex
	hookSeen   []cfgSized
	resolveErr error
	updateErr  error

	// strict decoding
	strict      *cordis.Context
	component   *cordis.Component
	constructed atomic.Int32
	decoded     *cordis.Fiber
	decodeErr   error

	// exporters
	mem            *cordis.MemoryExporter
	chosen, none   *cordis.Context
	stdout, stderr string
	globalLog      string
	globalSlog     string

	// golden
	moduleDir   string
	goldenRel   string
	goldenOut   string
	goldenErr   error
	goldenCount int
}

func (w *configViewsWorld) track(root *cordis.Context) *cordis.Context {
	w.roots = append(w.roots, root)
	return root
}

func cfgIssue(err error) (cordis.ConfigIssue, error) {
	var configError *cordis.ConfigError
	if !errors.As(err, &configError) {
		return cordis.ConfigIssue{}, fmt.Errorf("want a ConfigError, got %v", err)
	}
	if len(configError.Issues) != 1 {
		return cordis.ConfigIssue{}, fmt.Errorf("want one config issue, got %v", configError.Issues)
	}
	return configError.Issues[0], nil
}

// --- Scenario: Candidate config does not leak into effective config ---

// cfgAcceptedN is the only n the scenario's schema accepts ("whose schema accepts only n=1").
const cfgAcceptedN = 1

func (w *configViewsWorld) componentWithEffective(n int) error {
	if n != cfgAcceptedN {
		return fmt.Errorf("step asks for effective n=%d, but the schema accepts only n=%d", n, cfgAcceptedN)
	}
	root := w.track(cordis.New(cordis.WithExporters()))
	onlyOne := cordis.WithSchema(cordis.SchemaFunc(func(config any) (any, error) {
		if config.(cfgSized).N != cfgAcceptedN {
			return nil, &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Message: "must be 1", Path: "n"}}}
		}
		return config, nil
	}))
	component := cordis.Define("one", func(ctx *cordis.Context, config cfgSized) error {
		w.loads.Add(1)
		_, err := cordis.AcceptUpdate(ctx, func(config cfgSized) {
			w.hookMu.Lock()
			defer w.hookMu.Unlock()
			w.hookSeen = append(w.hookSeen, config)
		})
		return err
	}, onlyOne)
	fiber, err := root.Use(component, cfgSized{N: n})
	if err != nil {
		return fmt.Errorf("use: %w", err)
	}
	if err := cfgWait(fiber); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	w.fiber = fiber
	return w.effectiveIs(n)
}

func (w *configViewsWorld) candidateRejected() error {
	_, w.resolveErr = w.fiber.ResolveConfig(cfgSized{N: 2})
	if err := cfgWithin("update", func() { w.updateErr = w.fiber.Update(cfgSized{N: 2}, false) }); err != nil {
		return err
	}
	for name, err := range map[string]error{"ResolveConfig": w.resolveErr, "Update": w.updateErr} {
		issue, ierr := cfgIssue(err)
		if ierr != nil {
			return fmt.Errorf("%s was not rejected: %w", name, ierr)
		}
		if issue.Path != "n" {
			return fmt.Errorf("%s rejection path = %q, want n", name, issue.Path)
		}
	}
	return nil
}

func (w *configViewsWorld) rawIs(n int) error {
	if got := w.fiber.Config(); got != any(cfgSized{N: n}) {
		return fmt.Errorf("raw config = %#v, want n=%d", got, n)
	}
	return nil
}

func (w *configViewsWorld) effectiveIs(n int) error {
	got, ok := w.fiber.EffectiveConfig()
	if !ok {
		return errors.New("fiber has no effective config")
	}
	if got != any(cfgSized{N: n}) {
		return fmt.Errorf("effective config = %#v, want n=%d", got, n)
	}
	return nil
}

func (w *configViewsWorld) hookNotCalledWith(n int) error {
	w.hookMu.Lock()
	defer w.hookMu.Unlock()
	for _, seen := range w.hookSeen {
		if seen.N == n {
			return fmt.Errorf("the update hook was called with n=%d", n)
		}
	}
	if got := w.loads.Load(); got != 1 {
		return fmt.Errorf("the fiber loaded %d times, want 1 (a rejected candidate restarted it)", got)
	}
	return nil
}

// --- Scenario: Unknown config field fails with its path ---

func (w *configViewsWorld) strictTreeWithField(field string) error {
	if field != "retries" {
		return fmt.Errorf("unsupported schema field %q", field)
	}
	w.strict = w.track(cordis.New(cordis.WithStrictConfig(), cordis.WithExporters()))
	w.component = cordis.Construct("retry", func(ctx *cordis.Context, config cfgRetry) (*cfgRetry, error) {
		w.constructed.Add(1)
		return &config, nil
	})
	return nil
}

func (w *configViewsWorld) decodeStrictly(input string) error {
	fiber, err := w.strict.Use(w.component, json.RawMessage(input))
	if err != nil {
		w.decodeErr = err
		return nil
	}
	w.decoded = fiber
	w.decodeErr = cfgWait(fiber)
	return nil
}

func (w *configViewsWorld) decodingFailsNaming(field string) error {
	if w.decodeErr == nil {
		return errors.New("strict decoding accepted the unknown field")
	}
	issue, err := cfgIssue(w.decodeErr)
	if err != nil {
		return err
	}
	if issue.Path != field {
		return fmt.Errorf("issue path = %q, want %q", issue.Path, field)
	}
	if !strings.Contains(w.decodeErr.Error(), field) {
		return fmt.Errorf("error %q does not name %q", w.decodeErr.Error(), field)
	}
	if w.decoded != nil && w.decoded.State() != cordis.Failed {
		return fmt.Errorf("fiber state = %v, want Failed", w.decoded.State())
	}
	return nil
}

func (w *configViewsWorld) notConstructed() error {
	if got := w.constructed.Load(); got != 0 {
		return fmt.Errorf("component was constructed %d times from a rejected config", got)
	}
	// Control: a well-formed config does construct, so the zero above is meaningful.
	ok, err := w.strict.Use(w.component, json.RawMessage(`{"retries": 3}`))
	if err != nil {
		return fmt.Errorf("control use: %w", err)
	}
	if err := cfgWait(ok); err != nil {
		return fmt.Errorf("control load: %w", err)
	}
	if got := w.constructed.Load(); got != 1 {
		return fmt.Errorf("well-formed config constructed %d times, want 1", got)
	}
	return nil
}

// --- Scenario: Tree logs only to the chosen exporter ---

func (w *configViewsWorld) twoTrees() error {
	w.mem = &cordis.MemoryExporter{}
	w.chosen = w.track(cordis.New(cordis.WithExporters(w.mem)))
	w.none = w.track(cordis.New(cordis.WithExporters()))
	return nil
}

// captureGlobalSinks swaps the process's standard streams and the log and log/slog
// package defaults for buffers while fn runs.
func (w *configViewsWorld) captureGlobalSinks(fn func()) error {
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	var outText, errText []byte
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); outText, _ = io.ReadAll(outRead) }()
	go func() { defer readers.Done(); errText, _ = io.ReadAll(errRead) }()

	var logBuf, slogBuf bytes.Buffer
	savedOut, savedErr := os.Stdout, os.Stderr
	savedLogWriter, savedSlog := log.Writer(), slog.Default()
	os.Stdout, os.Stderr = outWrite, errWrite
	log.SetOutput(&logBuf)
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	func() {
		defer func() {
			os.Stdout, os.Stderr = savedOut, savedErr
			log.SetOutput(savedLogWriter)
			slog.SetDefault(savedSlog)
		}()
		fn()
	}()
	_ = outWrite.Close()
	_ = errWrite.Close()
	readers.Wait()
	_ = outRead.Close()
	_ = errRead.Close()
	w.stdout, w.stderr = string(outText), string(errText)
	w.globalLog, w.globalSlog = logBuf.String(), slogBuf.String()
	return nil
}

func (w *configViewsWorld) eachTreeLogs() error {
	return w.captureGlobalSinks(func() {
		w.chosen.Logger().Infof("one line")
		w.none.Logger().Infof("one line")
	})
}

func (w *configViewsWorld) memoryHoldsOne() error {
	messages := w.mem.Messages()
	if len(messages) != 1 {
		return fmt.Errorf("in-memory exporter holds %d records, want 1: %v", len(messages), messages)
	}
	if messages[0].Text != "one line" {
		return fmt.Errorf("record text = %q, want %q", messages[0].Text, "one line")
	}
	return nil
}

func (w *configViewsWorld) noGlobalSink() error {
	for name, got := range map[string]string{
		"standard output": w.stdout, "standard error": w.stderr,
		"log default": w.globalLog, "slog default": w.globalSlog,
	} {
		if got != "" {
			return fmt.Errorf("%s received %q", name, got)
		}
	}
	return nil
}

// --- Scenario: Sample output after all eight changes ---

func cfgModuleDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil &&
			strings.Contains(string(data), "module github.com/smartpcr/agent-plugin/src/go\n") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("module github.com/smartpcr/agent-plugin/src/go not found above the working directory")
		}
		dir = parent
	}
}

func (w *configViewsWorld) committedGolden(rel string) error {
	dir, err := cfgModuleDir()
	if err != nil {
		return err
	}
	w.moduleDir, w.goldenRel = dir, rel
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("committed golden missing: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return fmt.Errorf("committed golden %s is empty", rel)
	}
	return nil
}

func cfgRaceSupported() bool {
	out, err := exec.Command("go", "env", "CGO_ENABLED", "CC").Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != "1" {
		return false
	}
	_, err = exec.LookPath(fields[1])
	return err == nil
}

func (w *configViewsWorld) goldenRuns(count int) error {
	w.goldenCount = count
	args := []string{"test", "-v"}
	if cfgRaceSupported() {
		args = append(args, "-race")
	}
	args = append(args, fmt.Sprintf("-count=%d", count), "-run", "^TestOutputMatchesGolden$", "./cmd/cordis-sample")
	cmd := exec.Command("go", args...)
	cmd.Dir = w.moduleDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	w.goldenOut, w.goldenErr = string(out), err
	return nil
}

func (w *configViewsWorld) sampleMatchesGolden() error {
	if w.goldenErr != nil {
		return fmt.Errorf("cordis-sample output differs from %s: %v\n%s", w.goldenRel, w.goldenErr, w.goldenOut)
	}
	if strings.Contains(w.goldenOut, "no tests to run") {
		return fmt.Errorf("golden comparison ran no tests (TestOutputMatchesGolden missing?):\n%s", w.goldenOut)
	}
	// Count only top-level (unindented) PASS lines so subtests are not counted.
	passes := 0
	for _, line := range strings.Split(w.goldenOut, "\n") {
		if strings.HasPrefix(strings.TrimRight(line, "\r"), "--- PASS: TestOutputMatchesGolden ") {
			passes++
		}
	}
	if passes != w.goldenCount {
		return fmt.Errorf("golden comparison reported %d passes of TestOutputMatchesGolden, want %d:\n%s", passes, w.goldenCount, w.goldenOut)
	}
	if !strings.Contains(w.goldenOut, "ok") || !strings.Contains(w.goldenOut, "cmd/cordis-sample") {
		return fmt.Errorf("golden comparison did not report ok:\n%s", w.goldenOut)
	}
	return nil
}

func (w *configViewsWorld) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = w.moduleDir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (w *configViewsWorld) onlyListedReRecordings() error {
	repo := filepath.Dir(filepath.Dir(w.moduleDir))
	evidence, err := os.ReadFile(filepath.Join(repo, "docs", "stories", "harness-DSH-M1", "evidence", "DH-S-2.md"))
	if err != nil {
		return fmt.Errorf("DH-S-2 evidence record missing: %w", err)
	}
	text := string(evidence)
	start := strings.Index(text, "## Stage 2.3")
	if start < 0 {
		return errors.New("DH-S-2 evidence has no Stage 2.3 section")
	}
	section := text[start:]
	if next := strings.Index(section[1:], "\n## "); next >= 0 {
		section = section[:next+1]
	}
	if !strings.Contains(section, "TC-02b") || !strings.Contains(section, "output.golden") {
		return errors.New("Stage 2.3 evidence has no TC-02b golden record")
	}
	notReRecorded := strings.Contains(section, "was not re-recorded")

	// Every change to the golden since the Stage 2.3 base must be accounted for
	// by the evidence. Missing history is a failure, never a silent pass.
	base, err := w.stageBase()
	if err != nil {
		return err
	}
	dirty, err := w.git("diff", "--name-only", "HEAD", "--", w.goldenRel)
	if err != nil {
		return fmt.Errorf("git diff: %v: %s", err, dirty)
	}
	if dirty != "" {
		return fmt.Errorf("golden has uncommitted changes the evidence cannot list: %s", dirty)
	}
	changed, err := w.git("diff", "--name-only", base, "HEAD", "--", w.goldenRel)
	if err != nil {
		return fmt.Errorf("git diff %s HEAD: %v: %s", base, err, changed)
	}
	if notReRecorded {
		if changed != "" {
			return fmt.Errorf("golden differs from the Stage 2.3 base %s, but the evidence says it was not re-recorded", base)
		}
		return nil
	}

	// Re-recordings are listed: each commit that touched the golden since the
	// base must be named (by short or full hash) in the Stage 2.3 evidence.
	commits, err := w.git("log", "--format=%H", base+"..HEAD", "--", w.goldenRel)
	if err != nil {
		return fmt.Errorf("git log: %v: %s", err, commits)
	}
	if changed != "" && strings.TrimSpace(commits) == "" {
		return fmt.Errorf("golden differs from the Stage 2.3 base %s, but no commit since the base touched it", base)
	}
	var unlisted []string
	for _, full := range strings.Fields(commits) {
		if !strings.Contains(section, full[:7]) {
			unlisted = append(unlisted, full[:12])
		}
	}
	if len(unlisted) > 0 {
		return fmt.Errorf("golden re-recordings not listed in the Stage 2.3 evidence: %s", strings.Join(unlisted, ", "))
	}
	return nil
}

// stageBase returns the commit the Stage 2.3 work started from: CORDIS_STAGE_BASE
// when set, otherwise the parent of the oldest "Config Views and Strict Decoding"
// commit. It fails when that history is not available (for example a shallow clone).
func (w *configViewsWorld) stageBase() (string, error) {
	if base := strings.TrimSpace(os.Getenv("CORDIS_STAGE_BASE")); base != "" {
		if out, err := w.git("rev-parse", "--verify", base+"^{commit}"); err != nil {
			return "", fmt.Errorf("CORDIS_STAGE_BASE %q is not a commit: %s", base, out)
		}
		return base, nil
	}
	out, err := w.git("log", "--format=%H", "--reverse", "--fixed-strings", "--grep", "Config Views and Strict Decoding", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git log: %v: %s", err, out)
	}
	first := strings.Fields(out)
	if len(first) == 0 {
		return "", errors.New("cannot locate the Stage 2.3 base: no \"Config Views and Strict Decoding\" commit in history; set CORDIS_STAGE_BASE")
	}
	base := first[0] + "^"
	if out, err := w.git("rev-parse", "--verify", base+"{commit}"); err != nil {
		return "", fmt.Errorf("Stage 2.3 base %s is not in local history (shallow clone?); fetch full history or set CORDIS_STAGE_BASE: %s", base, out)
	}
	return base, nil
}

func InitializeScenario_cordis_port_guarantees_config_views_and_strict_decoding(ctx *godog.ScenarioContext) {
	var w *configViewsWorld
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		w = &configViewsWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w != nil {
			for _, root := range w.roots {
				_ = cfgWithin("dispose root", root.Dispose)
			}
		}
		return c, nil
	})

	ctx.Step(`^a component with effective config n=(\d+) whose schema accepts only n=1$`, func(n int) error { return w.componentWithEffective(n) })
	ctx.Step(`^a candidate with n=2 is validated and rejected$`, func() error { return w.candidateRejected() })
	ctx.Step(`^the raw config still reads n=(\d+)$`, func(n int) error { return w.rawIs(n) })
	ctx.Step(`^the effective config still reads n=(\d+)$`, func(n int) error { return w.effectiveIs(n) })
	ctx.Step(`^the update hook was not called with n=(\d+)$`, func(n int) error { return w.hookNotCalledWith(n) })

	ctx.Step(`^a strict tree and a component whose schema has the field "([^"]+)"$`, func(f string) error { return w.strictTreeWithField(f) })
	ctx.Step(`^the config (\{.*\}) is decoded strictly$`, func(input string) error { return w.decodeStrictly(input) })
	ctx.Step(`^decoding fails with an error naming "([^"]+)"$`, func(f string) error { return w.decodingFailsNaming(f) })
	ctx.Step(`^the component is not constructed$`, func() error { return w.notConstructed() })

	ctx.Step(`^two trees, one created with an in-memory exporter and one with none$`, func() error { return w.twoTrees() })
	ctx.Step(`^each tree logs a line$`, func() error { return w.eachTreeLogs() })
	ctx.Step(`^the in-memory exporter holds exactly one record$`, func() error { return w.memoryHoldsOne() })
	ctx.Step(`^no package-global sink received anything$`, func() error { return w.noGlobalSink() })

	ctx.Step(`^the committed cordis-sample golden "([^"]+)"$`, func(rel string) error { return w.committedGolden(rel) })
	ctx.Step(`^the cordis-sample golden comparison runs (\d+) times$`, func(n int) error { return w.goldenRuns(n) })
	ctx.Step(`^the cordis-sample output matches the committed golden$`, func() error { return w.sampleMatchesGolden() })
	ctx.Step(`^the golden carries only the re-recordings listed in the DH-S-2 evidence record$`, func() error { return w.onlyListedReRecordings() })
}

func TestE2E_cordis_port_guarantees_config_views_and_strict_decoding(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cordis_port_guarantees_config_views_and_strict_decoding",
		ScenarioInitializer: InitializeScenario_cordis_port_guarantees_config_views_and_strict_decoding,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"cordis_port_guarantees_config_views_and_strict_decoding.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
