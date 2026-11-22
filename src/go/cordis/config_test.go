package cordis_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
)

type sized struct {
	N int `json:"n"`
}

// sizedComponent loads sized config and counts its loads.
func sizedComponent(name string, loads *atomic.Int32, options ...cordis.Option) *cordis.Component {
	return cordis.Define(name, func(ctx *cordis.Context, config sized) error {
		loads.Add(1)
		return nil
	}, options...)
}

func effective(t *testing.T, fiber *cordis.Fiber) any {
	t.Helper()
	config, ok := fiber.EffectiveConfig()
	True(t, ok, fiber.Name()+" has no effective config")
	return config
}

func configIssue(t *testing.T, err error) cordis.ConfigIssue {
	t.Helper()
	var configError *cordis.ConfigError
	True(t, errors.As(err, &configError), fmt.Sprintf("want a ConfigError, got %v", err))
	True(t, len(configError.Issues) == 1, fmt.Sprintf("want one issue, got %v", configError.Issues))
	return configError.Issues[0]
}

// ---------------------------------------------------------- DH-T-CORDIS-8

//dsh:test DH-T-CORDIS-8
func TestRejectedCandidateDoesNotLeakIntoEffectiveConfig(t *testing.T) {
	root, _ := Quiet()
	var loads atomic.Int32
	onlyOne := cordis.WithSchema(cordis.SchemaFunc(func(config any) (any, error) {
		if config.(sized).N != 1 {
			return nil, &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Message: "must be 1", Path: "n"}}}
		}
		return config, nil
	}))
	var mu sync.Mutex
	var accepted []sized
	fiber := MustWait(t, Must(root.Use(cordis.Define("one", func(ctx *cordis.Context, config sized) error {
		loads.Add(1)
		_, err := cordis.AcceptUpdate(ctx, func(config sized) {
			mu.Lock()
			defer mu.Unlock()
			accepted = append(accepted, config)
		})
		return err
	}, onlyOne), sized{N: 1})))
	Equal(t, any(sized{N: 1}), effective(t, fiber), "effective after load")

	_, err := fiber.ResolveConfig(sized{N: 2})
	Equal(t, "n", configIssue(t, err).Path, "ResolveConfig rejection")
	err = fiber.Update(sized{N: 2}, false)
	Equal(t, "n", configIssue(t, err).Path, "Update rejection")

	Equal(t, any(sized{N: 1}), fiber.Config(), "raw after rejection")
	Equal(t, any(sized{N: 1}), effective(t, fiber), "effective after rejection")
	mu.Lock()
	Equal(t, 0, len(accepted), "the update hook saw a rejected candidate")
	mu.Unlock()
	Equal(t, int32(1), loads.Load(), "a rejected candidate restarted the fiber")
}

//dsh:test DH-T-CORDIS-8
func TestResolveConfigStoresNothing(t *testing.T) {
	root, _ := Quiet()
	var loads atomic.Int32
	fiber := MustWait(t, Must(root.Use(sizedComponent("sized", &loads), sized{N: 1})))

	resolved, err := fiber.ResolveConfig(json.RawMessage(`{"n": 5}`))
	NoError(t, err)
	Equal(t, any(sized{N: 5}), resolved, "resolved candidate")
	Equal(t, any(sized{N: 1}), fiber.Config(), "raw after ResolveConfig")
	Equal(t, any(sized{N: 1}), effective(t, fiber), "effective after ResolveConfig")
	Equal(t, int32(1), loads.Load(), "ResolveConfig reloaded the fiber")

	_, err = root.Fiber().ResolveConfig(sized{})
	True(t, errors.Is(err, cordis.ErrRootFiber), "the root fiber has no config to resolve")
}

//dsh:test DH-T-CORDIS-8
func TestInPlaceUpdatePublishesEffectiveConfig(t *testing.T) {
	root, _ := Quiet()
	var loads atomic.Int32
	fiber := MustWait(t, Must(root.Use(cordis.Define("live", func(ctx *cordis.Context, config sized) error {
		loads.Add(1)
		_, err := cordis.AcceptUpdate(ctx, func(sized) {})
		return err
	}), sized{N: 1})))

	NoError(t, fiber.Update(json.RawMessage(`{"n": 3}`), true))
	Equal(t, any(sized{N: 3}), effective(t, fiber), "effective after an in-place update")
	Equal(t, `{"n": 3}`, string(fiber.Config().(json.RawMessage)), "raw keeps the form handed in")
	Equal(t, int32(1), loads.Load(), "an in-place update restarted the fiber")

	// An update that falls through to a restart is published by the reload.
	restarting := MustWait(t, Must(root.Use(sizedComponent("restarting", &loads), sized{N: 1})))
	NoError(t, restarting.Update(sized{N: 6}, true))
	Equal(t, any(sized{N: 6}), effective(t, restarting), "effective after a restart")
}

//dsh:test DH-T-CORDIS-8
func TestSupersededInPlaceUpdateIsNotPublished(t *testing.T) {
	root, _ := Quiet()
	entered := make(chan struct{})
	release := make(chan struct{})
	live := cordis.Define("live", func(ctx *cordis.Context, config sized) error {
		_, err := ctx.OnUpdate(func(resolved any, next func() error) error {
			if resolved.(sized).N == 3 {
				close(entered)
				<-release
			}
			return nil
		}, false)
		return err
	})

	t.Run("by a later update", func(t *testing.T) {
		entered, release = make(chan struct{}), make(chan struct{})
		unblock := closeOnce(release)
		fiber := MustWait(t, Must(root.Use(live, sized{N: 1})))
		stale := make(chan error, 1)
		goJoined(t, unblock, func() { stale <- fiber.Update(sized{N: 3}, true) })
		<-entered
		NoError(t, fiber.Update(sized{N: 4}, true))
		Equal(t, any(sized{N: 4}), effective(t, fiber), "effective after the later update")
		unblock()
		NoError(t, <-stale)
		Equal(t, any(sized{N: 4}), effective(t, fiber), "a stale update was published")
		Equal(t, any(sized{N: 4}), fiber.Config(), "raw after the stale update")
	})

	t.Run("by a restart", func(t *testing.T) {
		entered, release = make(chan struct{}), make(chan struct{})
		unblock := closeOnce(release)
		fiber := MustWait(t, Must(root.Use(live, sized{N: 1})))
		stale := make(chan error, 1)
		goJoined(t, unblock, func() { stale <- fiber.Update(sized{N: 3}, true) })
		<-entered
		NoError(t, fiber.RetainConfig(sized{N: 9}))
		NoError(t, fiber.Restart())
		unblock()
		NoError(t, <-stale)
		Equal(t, any(sized{N: 9}), effective(t, fiber), "an update superseded by a restart was published")
	})

	t.Run("by disposal", func(t *testing.T) {
		entered, release = make(chan struct{}), make(chan struct{})
		unblock := closeOnce(release)
		fiber := MustWait(t, Must(root.Use(live, sized{N: 1})))
		stale := make(chan error, 1)
		goJoined(t, unblock, func() { stale <- fiber.Update(sized{N: 3}, true) })
		<-entered
		Within(t, "dispose", fiber.Dispose)
		unblock()
		NoError(t, <-stale)
		_, ok := fiber.EffectiveConfig()
		True(t, !ok, "a disposed fiber holds an effective config")
	})
}

//dsh:test DH-T-CORDIS-8
func TestPendingFiberHasNoEffectiveConfig(t *testing.T) {
	root, _ := Quiet()
	var loads atomic.Int32
	fiber := Must(root.Use(sizedComponent("waiting", &loads, cordis.WithInject("missing")), sized{N: 2}))
	NoError(t, Wait(t, fiber))
	Equal(t, cordis.Pending, fiber.State(), "state without the service")
	_, ok := fiber.EffectiveConfig()
	True(t, !ok, "a pending fiber has an effective config")
	Equal(t, any(sized{N: 2}), fiber.Config(), "raw of a pending fiber")

	Must(root.Provide("missing", 1))
	Eventually(t, func() bool { return fiber.State() == cordis.Active }, "the fiber did not load")
	Equal(t, any(sized{N: 2}), effective(t, fiber), "effective once loaded")
}

//dsh:test DH-T-CORDIS-8
func TestRetainConfigStoresRawWithoutUpdate(t *testing.T) {
	root, _ := Quiet()
	var loads atomic.Int32
	var commits atomic.Int32
	root.Internal().OnCommit(func(*cordis.Fiber, any) { commits.Add(1) })
	fiber := MustWait(t, Must(root.Use(sizedComponent("retained", &loads), sized{N: 1})))

	NoError(t, fiber.RetainConfig(sized{N: 7}))
	Equal(t, any(sized{N: 7}), fiber.Config(), "raw after RetainConfig")
	Equal(t, any(sized{N: 1}), effective(t, fiber), "effective after RetainConfig")
	Equal(t, int32(1), loads.Load(), "RetainConfig reloaded the fiber")
	Equal(t, int32(0), commits.Load(), "RetainConfig was committed")

	NoError(t, fiber.Restart())
	Equal(t, any(sized{N: 7}), effective(t, fiber), "effective after the next load")
}

//dsh:test DH-T-CORDIS-8
func TestResolutionCannotMutateStoredConfigViews(t *testing.T) {
	root, _ := Quiet()
	// The hook edits a map or pointer candidate in place, then rejects n == 2.
	Must(root.OnConfig(func(_ *cordis.Fiber, candidate any) (any, error) {
		switch config := candidate.(type) {
		case map[string]any:
			config["n"] = config["n"].(int) * 10
			config["tags"].([]any)[0] = "edited"
			if config["n"] == 20 {
				return nil, errors.New("two is refused")
			}
		case *sized:
			config.N *= 10
			if config.N == 20 {
				return nil, errors.New("two is refused")
			}
		}
		return candidate, nil
	}))

	t.Run("map", func(t *testing.T) {
		raw := map[string]any{"n": 1, "tags": []any{"a"}}
		fiber := MustWait(t, Must(root.Use(cordis.Define("mapped", func(*cordis.Context, sized) error { return nil }), raw)))
		Equal(t, 1, raw["n"].(int), "the initial resolution changed the raw map")
		Equal(t, "a", raw["tags"].([]any)[0].(string), "the initial resolution changed a nested slice")
		Equal(t, any(sized{N: 10}), effective(t, fiber), "effective")

		candidate := map[string]any{"n": 2, "tags": []any{"b"}}
		_ = configIssue(t, fiber.Update(candidate, false))
		Equal(t, 2, candidate["n"].(int), "a rejected update changed the caller's candidate")
		_, err := fiber.ResolveConfig(fiber.Config())
		NoError(t, err)
		Equal(t, 1, fiber.Config().(map[string]any)["n"].(int), "ResolveConfig changed the raw config")
		Equal(t, any(sized{N: 10}), effective(t, fiber), "effective after resolution")
	})

	t.Run("pointer", func(t *testing.T) {
		raw := &sized{N: 1}
		var got *sized
		fiber := MustWait(t, Must(root.Use(cordis.Define("pointed", func(_ *cordis.Context, config *sized) error {
			got = config
			return nil
		}), raw)))
		Equal(t, 1, raw.N, "the initial resolution changed the raw pointer")
		True(t, got != raw, "the body received the raw pointer")
		Equal(t, 10, effective(t, fiber).(*sized).N, "effective")

		NoError(t, fiber.RetainConfig(&sized{N: 2}))
		_, err := fiber.ResolveConfig(fiber.Config())
		_ = configIssue(t, err)
		Equal(t, 2, fiber.Config().(*sized).N, "a rejected ResolveConfig changed the raw config")
		Equal(t, 10, effective(t, fiber).(*sized).N, "a rejected ResolveConfig changed the effective config")
		_, err = fiber.ResolveConfig(effective(t, fiber))
		NoError(t, err)
		Equal(t, 10, effective(t, fiber).(*sized).N, "resolving the effective config changed it")
	})

	t.Run("defined pointer", func(t *testing.T) {
		type sizedPtr *sized
		raw := sizedPtr(&sized{N: 3})
		var hooked any
		Must(root.OnConfig(func(fiber *cordis.Fiber, candidate any) (any, error) {
			if fiber.Name() == "defined" {
				hooked = candidate
			}
			return candidate, nil
		}))
		var got sizedPtr
		fiber := MustWait(t, Must(root.Use(cordis.Define("defined", func(_ *cordis.Context, config sizedPtr) error {
			got = config
			return nil
		}), raw)))
		_, isDefined := hooked.(sizedPtr)
		True(t, isDefined, fmt.Sprintf("the hook received %T, not sizedPtr", hooked))
		True(t, got != nil && got != raw, "the body did not receive a copy of the defined pointer")
		Equal(t, 3, (*got).N, "the body's config")
		Equal(t, 3, (*effective(t, fiber).(sizedPtr)).N, "effective")
	})
}

//dsh:test DH-T-CORDIS-9
func TestConfigHookTransformsEffectiveViewOnly(t *testing.T) {
	root, _ := Quiet()
	var mu sync.Mutex
	var seen []*cordis.Fiber
	Must(root.OnConfig(func(fiber *cordis.Fiber, candidate any) (any, error) {
		config, ok := candidate.(sized)
		if !ok {
			return candidate, nil
		}
		mu.Lock()
		seen = append(seen, fiber)
		mu.Unlock()
		config.N *= 2
		return config, nil
	}))
	var got atomic.Int32
	fiber := MustWait(t, Must(root.Use(cordis.Define("doubled", func(ctx *cordis.Context, config sized) error {
		got.Store(int32(config.N))
		return nil
	}), sized{N: 2})))

	Equal(t, any(sized{N: 2}), fiber.Config(), "raw")
	Equal(t, any(sized{N: 4}), effective(t, fiber), "effective")
	Equal(t, int32(4), got.Load(), "the body's config")
	resolved, err := fiber.ResolveConfig(sized{N: 5})
	NoError(t, err)
	Equal(t, any(sized{N: 10}), resolved, "a candidate passes the hook")
	mu.Lock()
	defer mu.Unlock()
	True(t, len(seen) == 2 && seen[0] == fiber && seen[1] == fiber, "the hook did not receive the fiber being checked")
}

//dsh:test DH-T-CORDIS-9
func TestConfigHookCanRejectACandidate(t *testing.T) {
	root, _ := Quiet()
	var later atomic.Int32
	Must(root.OnConfig(func(_ *cordis.Fiber, candidate any) (any, error) {
		if config, ok := candidate.(sized); ok {
			switch config.N {
			case 7:
				return nil, errors.New("seven is refused")
			case 8:
				panic("eight")
			}
		}
		return candidate, nil
	}))
	Must(root.OnConfig(func(_ *cordis.Fiber, candidate any) (any, error) {
		if config, ok := candidate.(sized); ok && config.N >= 7 {
			later.Add(1)
		}
		return candidate, nil
	}))
	var loads atomic.Int32
	component := sizedComponent("checked", &loads)

	for _, n := range []int{7, 8} {
		failed := Must(root.Use(component, sized{N: n}))
		issue := configIssue(t, Wait(t, failed))
		True(t, strings.Contains(issue.Message, "config hook of root"), "the issue does not name the hook's owner: "+issue.Message)
		Equal(t, cordis.Failed, failed.State(), "state after a rejected load")
		Equal(t, int32(0), loads.Load(), "the body ran on a rejected candidate")
		failed.Dispose()
	}

	fiber := MustWait(t, Must(root.Use(component, sized{N: 1})))
	_ = configIssue(t, fiber.Update(sized{N: 7}, false))
	_ = configIssue(t, fiber.Update(sized{N: 8}, false))
	Equal(t, any(sized{N: 1}), fiber.Config(), "raw after rejected updates")
	Equal(t, any(sized{N: 1}), effective(t, fiber), "effective after rejected updates")
	Equal(t, int32(0), later.Load(), "a later hook saw a rejected candidate")
}

//dsh:test DH-T-CORDIS-9
func TestConfigHookEndsWithItsOwner(t *testing.T) {
	root, _ := Quiet()
	owner := MustWait(t, Must(root.Apply("owner", func(ctx *cordis.Context) error {
		_, err := ctx.OnConfig(func(_ *cordis.Fiber, candidate any) (any, error) {
			if config, ok := candidate.(sized); ok {
				config.N++
				return config, nil
			}
			return candidate, nil
		})
		return err
	})))
	var loads atomic.Int32
	fiber := MustWait(t, Must(root.Use(sizedComponent("hooked", &loads), sized{N: 1})))
	Equal(t, any(sized{N: 2}), effective(t, fiber), "effective while the owner is loaded")

	Within(t, "dispose owner", owner.Dispose)
	resolved, err := fiber.ResolveConfig(sized{N: 1})
	NoError(t, err)
	Equal(t, any(sized{N: 1}), resolved, "the hook outlived its owner")
	NoError(t, fiber.Restart())
	Equal(t, any(sized{N: 1}), effective(t, fiber), "effective after the owner unloaded")
}

// --------------------------------------------------------- DH-T-CORDIS-13

type retryConfig struct {
	Retries int `json:"retries"`
}

//dsh:test DH-T-CORDIS-13
func TestStrictConfigRejectsUnknownKeysAndTrailingData(t *testing.T) {
	var constructed atomic.Int32
	construct := func(ctx *cordis.Context, config retryConfig) (*retryConfig, error) {
		constructed.Add(1)
		return &config, nil
	}
	retry := cordis.Construct("retry", construct)
	lenient := cordis.Construct("lenient", construct, cordis.LenientConfig())
	var schemaCalls atomic.Int32
	schemed := cordis.Construct("schemed", construct, cordis.WithSchema(cordis.SchemaFunc(func(config any) (any, error) {
		schemaCalls.Add(1)
		return config, nil
	})))

	root := cordis.New(cordis.WithStrictConfig(), cordis.WithExporters())
	defer root.Dispose()
	for _, tc := range []struct {
		name      string
		component *cordis.Component
		input     any
		path      string
		message   string
	}{
		{"unknown key", retry, json.RawMessage(`{"retrys": 3}`), "retrys", "unknown key"},
		{"unknown key in a map", retry, map[string]any{"retrys": 3}, "retrys", "unknown key"},
		{"unknown key as bytes", retry, []byte(`{"retries": 1, "retrys": 3}`), "retrys", "unknown key"},
		{"type mismatch", retry, json.RawMessage(`{"retries": "three"}`), "retries", "cannot unmarshal"},
		{"trailing value", retry, json.RawMessage(`{"retries": 3} {"retries": 4}`), "", `trailing data after the config at offset 14: "{\"retries\": 4}"`},
		{"trailing garbage", retry, json.RawMessage(`{"retries": 3}]`), "", "trailing data"},
		{"unknown key before a schema", schemed, json.RawMessage(`{"retrys": 3}`), "retrys", "unknown key"},
	} {
		fiber := Must(root.Use(tc.component, tc.input))
		issue := configIssue(t, Wait(t, fiber))
		Equal(t, tc.path, issue.Path, tc.name, "path")
		True(t, strings.Contains(issue.Message, tc.message), tc.name+": message "+issue.Message)
		Equal(t, cordis.Failed, fiber.State(), tc.name, "state")
		fiber.Dispose()
	}
	Equal(t, int32(0), constructed.Load(), "a component was constructed from a rejected config")
	Equal(t, int32(0), schemaCalls.Load(), "the schema saw a config with an unknown key")

	MustWait(t, Must(root.Use(lenient, json.RawMessage(`{"retrys": 3}`))))
	MustWait(t, Must(root.Use(retry, retryConfig{Retries: 3})))
	MustWait(t, Must(root.Use(retry, json.RawMessage(`{"retries": 3}`))))
	Equal(t, int32(3), constructed.Load(), "lenient, typed and well-formed configs load in a strict tree")

	loose, _ := Quiet()
	defer loose.Dispose()
	MustWait(t, Must(loose.Use(retry, json.RawMessage(`{"retrys": 3}`))))
	Equal(t, int32(4), constructed.Load(), "a tree without WithStrictConfig ignores the unknown key")
	mismatch := Must(loose.Use(retry, json.RawMessage(`{"retries": "three"}`)))
	Equal(t, "retries", configIssue(t, Wait(t, mismatch)).Path, "a type mismatch names its field in any tree")
}

// --------------------------------------------------------- DH-T-CORDIS-14

// captureStdStreams replaces the process's standard output and error with pipes while
// fn runs, and returns what was written to them. The readers end at EOF, so closing
// the write ends releases them; the cleanup does that, then joins them, even when fn
// fails the test half-way (P-C).
func captureStdStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outRead, outWrite, err := os.Pipe()
	NoError(t, err)
	errRead, errWrite, err := os.Pipe()
	NoError(t, err)
	var outText, errText []byte
	var readers sync.WaitGroup
	closeWriters := sync.OnceFunc(func() {
		_ = outWrite.Close()
		_ = errWrite.Close()
	})
	t.Cleanup(func() {
		closeWriters()
		Within(t, "joining the stream readers", readers.Wait)
		_ = outRead.Close()
		_ = errRead.Close()
	})
	readers.Add(2)
	go func() { defer readers.Done(); outText, _ = io.ReadAll(outRead) }()
	go func() { defer readers.Done(); errText, _ = io.ReadAll(errRead) }()

	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite
	func() {
		defer func() { os.Stdout, os.Stderr = savedOut, savedErr }()
		fn()
	}()
	closeWriters()
	readers.Wait()
	return string(outText), string(errText)
}

func logEveryLevel(ctx *cordis.Context, tag string) {
	logger := ctx.Logger()
	logger.Error(errors.New(tag + " error"))
	logger.Errorf("%s errorf", tag)
	logger.Warnf("%s warn", tag)
	logger.Infof("%s info", tag)
	logger.Debugf("%s debug", tag)
}

//dsh:test DH-T-CORDIS-14
func TestTreeLogsOnlyToTheExportersItsCreatorChose(t *testing.T) {
	mem := &cordis.MemoryExporter{}
	var stdout, stderr string
	stdout, stderr = captureStdStreams(t, func() {
		chosen := cordis.New(cordis.WithExporters(mem))
		none := cordis.New(cordis.WithExporters())
		chosen.Logger().Infof("one line")
		none.Logger().Infof("one line")
		Equal(t, 1, len(mem.Messages()), "records after one line each")

		chosen.Loggers().SetLevel(cordis.LevelDebug)
		none.Loggers().SetLevel(cordis.LevelDebug)
		for _, root := range []*cordis.Context{chosen, none} {
			MustWait(t, Must(root.Apply("talker", func(ctx *cordis.Context) error {
				logEveryLevel(ctx, "body")
				_, err := ctx.Effect("unload", func() (cordis.Disposer, error) {
					return func() error {
						logEveryLevel(ctx, "unload")
						return nil
					}, nil
				})
				return err
			})))
			Within(t, "root dispose", root.Dispose)
		}
	})
	Equal(t, "", stdout, "standard output")
	Equal(t, "", stderr, "standard error")

	messages := mem.Messages()
	Equal(t, 11, len(messages), "records of the chosen exporter")
	for _, tag := range []string{"body", "unload"} {
		for _, suffix := range []string{"error", "errorf", "warn", "info", "debug"} {
			text := tag + " " + suffix
			count := 0
			for _, message := range messages {
				if message.Text == text {
					count++
				}
			}
			Equal(t, 1, count, "records of", text)
		}
	}

	stdout, stderr = captureStdStreams(t, func() {
		root := cordis.New()
		root.Logger().Infof("to the console")
		root.Logger().Warnf("to the console")
	})
	True(t, strings.Contains(stdout, "[I] root to the console"), "New() without options lost the console exporter: "+stdout)
	True(t, strings.Contains(stderr, "[W] root to the console"), "New() without options lost the console exporter: "+stderr)
}
