// Command cordis-sample walks through the cordis model end to end: a service with an
// asynchronous Init, a component that waits for it, live reconfiguration, a provider
// going away and coming back, and the same component loaded from a watched JSON file.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/go/timer"
)

// ------------------------------------------------------------- database

// Database is the "database" service.
type Database struct {
	ctx  *cordis.Context
	mu   sync.Mutex
	rows map[string]string
}

// DatabaseKey is the typed key of the database service.
var DatabaseKey = cordis.NewServiceKey[*Database]("database")

// DatabaseComponent provides Database. Dependents wait for Init.
var DatabaseComponent = cordis.Construct("DatabaseService", func(ctx *cordis.Context, _ cordis.None) (*Database, error) {
	db := &Database{ctx: ctx, rows: map[string]string{}}
	if _, err := DatabaseKey.Provide(ctx, db); err != nil {
		return nil, err
	}
	return db, nil
})

// Init pretends to open a connection.
func (db *Database) Init() error {
	time.Sleep(50 * time.Millisecond)
	db.mu.Lock()
	db.rows["greeting"] = "hello from the database"
	db.mu.Unlock()
	db.ctx.Logger().Infof("connected")
	return nil
}

// Read returns a row.
func (db *Database) Read(key string) string {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.rows[key]
}

// Close runs when the provider unloads, after every dependent has.
func (db *Database) Close() error {
	db.ctx.Logger().Infof("disconnected")
	return nil
}

// ------------------------------------------------------------- heartbeat

// HeartbeatOptions is the heartbeat's config.
type HeartbeatOptions struct {
	IntervalMs int `json:"intervalMs"`
}

// Validate keeps the interval in [10, 60000].
func (o HeartbeatOptions) Validate() error {
	if o.IntervalMs < 10 || o.IntervalMs > 60_000 {
		return &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Message: "must be between 10 and 60000", Path: "intervalMs"}}}
	}
	return nil
}

// Heartbeat logs a database row on an interval owned by its own fiber.
var Heartbeat = cordis.Define("HeartbeatPlugin", func(ctx *cordis.Context, config HeartbeatOptions) error {
	db, err := DatabaseKey.Get(ctx)
	if err != nil {
		return err
	}
	service, err := timer.Key.Get(ctx)
	if err != nil {
		return err
	}
	// The interval is owned by *this* fiber: unloading the component stops it.
	if _, err := service.Interval(func() {
		ctx.Logger().Infof("beat: %s", db.Read("greeting"))
	}, time.Duration(config.IntervalMs)*time.Millisecond); err != nil {
		return err
	}
	_, err = ctx.Effect("heartbeat", func() (cordis.Disposer, error) {
		ctx.Logger().Infof("heartbeat every %dms", config.IntervalMs)
		return func() error {
			ctx.Logger().Infof("heartbeat stopped")
			return nil
		}, nil
	})
	return err
},
	cordis.WithInject(DatabaseKey.Name(), timer.Key.Name()),
	cordis.WithDefaults(func() HeartbeatOptions { return HeartbeatOptions{IntervalMs: 250} }))

func init() {
	// make the component resolvable by name in config files
	loader.Register("HeartbeatPlugin", Heartbeat)
}

// sampleEnv is everything run takes from its surroundings, so a test can run the
// walkthrough on a virtual clock with deterministic output.
type sampleEnv struct {
	Out      io.Writer           // where the walkthrough prints
	TempDir  string              // where the loader's JSON file is written
	Exporter cordis.Exporter     // replaces the root's log exporters
	Pause    func(time.Duration) // waits between the walkthrough's windows
	Settle   func()              // called after each lifecycle call and before each print
}

func main() {
	env := sampleEnv{
		Out:      os.Stdout,
		TempDir:  os.TempDir(),
		Exporter: cordis.NewConsoleExporter(os.Stdout, os.Stderr),
		Pause:    time.Sleep,
		Settle:   func() {},
	}
	if err := run(env); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(env sampleEnv) error {
	root := cordis.New()
	root.Loggers().SetLevel(cordis.LevelDebug)
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(env.Exporter)

	// 1. imperative composition
	if err := use(env, root, timer.Component, nil); err != nil {
		return err
	}
	database, err := root.Use(DatabaseComponent, nil)
	env.Settle()
	if err != nil {
		return err
	}

	// The heartbeat is created before the database is ready; it stays Pending and
	// starts by itself once the database finishes Init.
	heartbeat, err := root.Use(Heartbeat, HeartbeatOptions{IntervalMs: 200})
	env.Settle()
	if err != nil {
		return err
	}
	env.Settle()
	fmt.Fprintln(env.Out, "heartbeat before database:  ", heartbeat.State())
	err = database.Wait()
	env.Settle()
	if err != nil {
		return err
	}
	err = heartbeat.Wait()
	env.Settle()
	if err != nil {
		return err
	}
	env.Settle()
	fmt.Fprintln(env.Out, "heartbeat after database:   ", heartbeat.State())
	env.Pause(500 * time.Millisecond)

	// 2. live reconfiguration: the component unloads and reloads with the new config
	err = heartbeat.Update(HeartbeatOptions{IntervalMs: 100}, false)
	env.Settle()
	if err != nil {
		return err
	}
	env.Pause(300 * time.Millisecond)

	// 3. pull the dependency: every dependent unloads first, then comes back
	database.Dispose()
	env.Settle()
	env.Settle()
	fmt.Fprintln(env.Out, "heartbeat without database: ", heartbeat.State())
	if err := use(env, root, DatabaseComponent, nil); err != nil {
		return err
	}
	err = heartbeat.Wait()
	env.Settle()
	if err != nil {
		return err
	}
	env.Settle()
	fmt.Fprintln(env.Out, "heartbeat restored:         ", heartbeat.State())
	env.Pause(300 * time.Millisecond)

	// 4. declarative composition through the loader
	path := filepath.Join(env.TempDir, "cordis-sample.json")
	config, _ := json.Marshal(HeartbeatOptions{IntervalMs: 150})
	entries, _ := json.MarshalIndent([]loader.EntryOptions{{ID: "beat2", Name: "HeartbeatPlugin", Config: config}}, "", "  ")
	if err := os.WriteFile(path, entries, 0o644); err != nil {
		return err
	}
	defer os.Remove(path)
	if err := use(env, root, loader.Component, nil); err != nil {
		return err
	}
	if err := use(env, root, loader.ConfigFileComponent, loader.ConfigFileOptions{Path: path, Watch: true}); err != nil {
		return err
	}
	env.Settle()
	fmt.Fprintln(env.Out, "loaded from", path, "- edit it and watch it reload")
	env.Pause(500 * time.Millisecond)

	root.Dispose()
	env.Settle()
	return nil
}

func use(env sampleEnv, ctx *cordis.Context, component *cordis.Component, config any) error {
	fiber, err := ctx.Use(component, config)
	env.Settle()
	if err != nil {
		return err
	}
	err = fiber.Wait()
	env.Settle()
	return err
}
