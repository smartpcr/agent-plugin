# internal/testkit

Test support for `src/harness`, imported only by `_test.go` files and the kit's own
packages (archtest rule DH-T-UTIL-11). Step 1.6 (DH-D-TOOLCHAIN-3) fixed the test
**layers** and their conventions; step 6.17 (DH-D-UTIL-6) adds the kit that composes
real components against scripted boundaries.

## The kit (DH-D-UTIL-6, DH-ADR-UTIL-6)

Every helper takes the `*testing.T` and registers its release with `t.Cleanup`
(QG-5), so a test never closes, removes or stops anything itself.

| package | gives | released at cleanup |
|---------|-------|---------------------|
| `testkit` | `Tree(t)`: a cordis root with no exporters; `Buffer`: a goroutine-safe writer | the root is disposed |
| `clock` | virtual time: `Run(t, fn)` runs `fn` in a `testing/synctest` bubble (a goroutine left blocked fails the test), `Advance(d)`, `Until(date)`, `Settle()` inside it; the older explicit clock `NewManual(t0)`, `Now`, `After`, `(*Manual).Advance(d)`, `Waiters`, `BlockUntil` | the bubble's cleanups run inside it |
| `temphome` | `Dir(t)` (a home path below `t.TempDir()`), `New(t)` (a laid-out `home.Layout`), `Write`, `Layer`, `UserLayer` | the home is removed, and a home that survives fails the test |
| `model` | the fake model: `Transport` (an `http.RoundTripper` serving scripted SSE turns: `Reply`, `Status`, `Text`, `Usage`, `Finish`, `Error`, `Hang`), `Adapter` (an `llm.Adapter` over it), `Register(t, rt, id, tr)`; the chunk-level `Scripted` adapter (`NewScripted(t, scripts...)` with `Chunks`, `Answer`, `Fails`, `Blocks`; `Attempts`, `WaitAttempts`), `RegisterScripted(t, rt, id, s)`, `RegisterRoute(t, rt, route)` | the route is unregistered; unused turns or scripts, and an unscripted call, fail the test |
| `egresstest` | the recording egress: `New(t, allow...)`, `Allow`, `Host(name, ips...)`, `Fail(dest, attempt, Refused\|Reset\|Timeout)`; `Service()`, `Config(base)`, `Provide(ctx)`, `Component()` (stands in for `util.egress` under `egress.Key`), `Resolver()`; `Requests`, `Lookups`. An unlisted request or name lookup fails the test, naming it | a scripted failure that never happened fails the test |
| `compose` | `Start(t, Options{Bundle, Layers, Home, Components, Scripts, Allow, Env, Safe})` inside `clock.Run`: boot's real steps (facts, home, profile stack, `loader.Compose`, tree, settle, audit) on a fresh home, with `util.egress` replaced by the `egresstest` recorder and an `llm.scripted` entry providing `RuntimeKey` (a runtime whose `Route` is `model.Scripted`); returns `Tree{Model, Runtime, Egress, Loader, Sharing, Data, Layers, Issues, Audit, App, Stderr}`; `Layer(t, label, yaml)` | the tree is stopped and unloaded; a forced shutdown, a fiber left with its effects, or a bubble goroutine still alive fails the test |
| `faultserver` | a loopback HTTP/SSE fault server: `New(t, Options{Key})`, `Script(key, ...)` with `Stream`, `Reply`, `Status(code, retryAfter)`, `Reset(prefix...)`, `Stall(prefix...)`, `Malformed(prefix...)`; `Mix(t, seed, n, ...)`, `HeaderKey`, `Served(key)`, `Requests` | stalls released, connections closed; an unscripted request or a leftover behaviour fails the test |
| `answerer` | the fake answerer: `Register(t, owner, reg, channel, replies...)` answers interactions with `AllowOnce`, `AllowSession`, `Deny`, `Text` or `Never` and records what it was asked | the answerer is unloaded; unused replies fail the test |
| `permit` | `New(t, kind, dest)`: an egress permit, `Withdraw()` | its context is cancelled |
| `launcher` | `Build(t, dir, pkg, tags...)` builds a program; `Start(t, Program)` starts it through `world/subprocess` on a fresh temp home (`DSH_HOME` is managed: a `Program.Env` entry cannot override it) and returns a `src/sdk/jsonrpc` client on its stdio | the client and stdin close, the tree is stopped and awaited (survivors fail the test), then the home is removed |
| `launcher` (hosts) | `Spawn(t, Program)` starts a program in its own process tree with captured output and no home or client; `Run(t, within, Program)` waits up to `within` and on timeout force-stops the whole tree (descendants holding the output included) and returns `ErrTimeout` | a tree still running is force-stopped; the stop and the wait are bounded by `3*Grace`, survivors fail the test |
| `redact` | the identity filter of the manual checks on the user's signed-in clients (plan 16.5.6, 17.4.2): `New(identity...)`, `Apply(text)` masks token shapes (`TokenShape`), credential headers, emails, token file paths and the identity values, and names any pattern that survives; `Detect(text)` | -- |
| `procwait` | the one poll helper (M1-D-GATE-24 A), for facts with no in-process signal (another process's pid file, log line or port): `Poll(ctx, fact)` checks every `Interval` and returns an error naming the cause; `Until(ctx, t, fact)` fails the test instead. Every wait is bounded by the context's deadline or `DefaultTimeout`; in-process work is awaited through a signal or `testing/synctest`, never polled | -- |
| `dshbin` | `Path(t, tags...)` builds `./cmd/dsh` once per test binary and tag set through `launcher.Build` (DH-D-UTIL-7, C54); the package's `TestMain` calls `Main(m)` (or `Run(m)`, which returns the code) | `Main` removes the binary when the tests end; a failed build fails every later caller without a second build |
| `fixture` | the replay oracle kit (DH-D-UTIL-7, C54): `ReadManifest(dir)` (`scenario.json`), `Normalizer` (the fixed table of typed tokens: `<session:n>`, `<msg:n>`, `<in:n>`, `<call:n>`, `<digest:n>`, `<sha256:n>` numbered by first appearance; `<time>`, `<ms>`; named literal paths such as `<home>`, `<workspace>`), `Guard` (a committed fixture is a fixed point), `Diff`, `Tree`, `CopyTree`, `CompareTrees` | nothing |
| `scenario` | the scenario helper (DH-D-UTIL-8, C55): `Run(t, id, Needs, body)` checks the needs (`DSH_BIN`, variables, programs, OS), then runs the body with `S.Bin`, a scratch `S.Home` and `S.Workspace` of its own and a deadline (`S.Context`); every outcome is one JSON row appended to `DSH_SCENARIO_REPORT`. An unmet need skips with its reason, or fails with `required scenario skipped` when the ID is on `DSH_REQUIRE_SCENARIOS` (M2-DEC-3); `Check(rows, required)` fails a required ID without a passed row. `Bin(t)`, `Need(t, name, what)`, `Start(t, Program)` and `Main(m)` serve the scenarios written before it: the first of them in a test writes that test's one row when it ends (failed with the first failure, skipped or, when required, failed, failed past `DefaultDeadline`, else passed), under its `//dsh:scenario` ID; `Main` then checks the package's required IDs. A test file importing it imports no other harness-internal package but `launcher` (the `scenarioimport` analyzer) | the home and workspace are removed; a body past its deadline, a panic or an unwritable report fails |

```go
func TestOneTurn(t *testing.T) {
	tr := model.NewTransport(t, model.Reply("hello"))
	model.Register(t, runtime, "scripted", tr)
	// ... run one attempt through the real llm.Runtime; one assistant/message "hello"
}
```

Seams that belong to one package stay beside its contract (DH-ADR-UTIL-6): boot's
step fakes, the secret store's source fake, and `httptest` servers that stand in for
a remote peer.

## Test layers (DH-ADR-TOOLCHAIN-1)

| # | layer | where it lives | runs under |
|---|-------|----------------|------------|
| 1 | unit | `*_test.go`, fakes only at the model, network and clock boundaries | plain `go test ./...` and the gate |
| 2 | composition | `*_test.go`, a real configuration through the cordis loader, scripted model | plain `go test ./...` and the gate |
| 3 | golden | `*_test.go` + `testdata/*.golden`, through [`golden`](golden/golden.go) | plain `go test ./...` and the gate |
| 4 | replay | `*_replay_test.go` with `//go:build replay`; the keyless runner and its committed recordings are `src/harness/test/replay` (`llm/replay`, `fixture`, `dshbin`) | `go test -tags replay ./...` only, and the `harness-replay` gate |
| 5 | scenario | `*_scenario_test.go` with `//go:build scenario` | `go test -tags scenario ./...` only, from the credentialed scenario workflow |

### Naming what a test proves (`//dsh:test`; M1-G-CLOSE-9)

| directive | where | means |
|-----------|-------|-------|
| `//dsh:test DH-T-<AREA>-<n>[, ...]` | the line directly above a `func Test*`, `Fuzz*` or `Benchmark*`, in any layer; a deferral `//dsh:test DH-T-X-n later DH-S-m` only in the package's `doc.go` | this test proves those IDs; a deferral counts only when impl.md's DH-S-m section names the ID (not an exemption list, QG-2). `harness-gate trace-tests` reports every required ID no test names; existing `// DH-T-...` comments migrate to the directive. Rules: [`tools/harness-gate/README.md`](../../../../tools/harness-gate/README.md#test-traceability-dshtest-trace-tests-m1-g-close-9) |

### Build tags `replay` and `scenario` (DH-ADR-TOOLCHAIN-4; `DH-T-TOOLCHAIN-4`)

The two layer tags are the only build tags a test file of this module selects a
layer with (`LayerTags` in `tools/harness-gate/gates.go`). The keyless gate
(`tools/harness-gate`) **never** runs them: its `test` and `coverage` gates pass an
explicit empty tag list (`go test -tags= ...`), which overrides any `-tags` a
`GOFLAGS` setting would add. A failing scenario or replay file therefore cannot
fail, and cannot pass, the default gate (`TestLayerTaggedTestsAreExcludedFromDefaultGate`
over `tools/harness-gate/testdata/layer-tagged`).

**Authoring a scenario.** A `DH-E-*` scenario is a file named `*_scenario_test.go`
whose first line is the build constraint:

```go
//go:build scenario

package scenarios

import "testing"

// TestToolchainRehearsal: a release rehearsal yields a runnable binary on every platform.
//
//dsh:scenario DH-E-TOOLCHAIN-2
func TestToolchainRehearsal(t *testing.T) {
	// starts the built dsh binary, never "go run"
}
```

- The file name ends in `_scenario_test.go` and the constraint is exactly
  `//go:build scenario`, so it is invisible to `go test ./...`, `go vet ./...` and
  the gate.
- A scenario starts the **built** `dsh` binary, never `go run` (DH-ADR-TOOLCHAIN-1).
- It is vetted with the tag set: `go -C src/harness vet -tags scenario ./...`
  (build tags hide files from a plain `go vet`; DH-ADR-TOOLCHAIN-1 tradeoff).
- The `DH-E-*` ID is bound to the test by a `//dsh:scenario <ID>` line in the
  doc comment of a top-level `TestXxx` (one ID per test, one test per ID).
- It is authored, reviewed and indexed in the story that writes it, and executed
  only at the stage impl.md assigns (EV-4); a skipped scenario stays open.
- Indexing: the ID is listed, with its authoring stage, under the stage that
  executes it in `test/harness/acceptance-index.yaml`. The gate's
  `scenario-index` check fails when a scenario file's ID is not indexed (or is
  indexed twice), and when an indexed ID authored at or before the index's
  `built-through` stage has no file. The credentialed workflow
  (`.github/workflows/harness-scenarios.yml`) derives `DSH_REQUIRE_SCENARIOS`
  from the entry of the stage being accepted, and its preflight fails on a missing
  `DSH_DEEPSEEK_API_KEY` or a required ID without a scenario test.
- Scenarios of this module live in `internal/testkit/scenarios`.

Replay tests follow the same shape with `*_replay_test.go` and `//go:build replay`.

## Golden files (`golden`, QG-6, DH-ADR-TOOLCHAIN-4)

```go
func TestRender(t *testing.T) {
	golden.Assert(t, "render/plain", render(input)) // testdata/render/plain.golden
}
```

- Expected text lives beside its test under `testdata/` with a `.golden` suffix;
  `name` is slash-separated and must stay below `testdata/`.
- A difference fails the test with a line diff (`--- want` / `+++ got`; a carriage
  return is quoted and a missing final newline is marked).
- The **one** update switch is `-update`, on the test binary:
  `go -C src/harness test ./internal/... -run TestRender -update`. The flag is
  registered by `golden` itself, so a second `-update` in the same test binary is a
  duplicate-flag panic. No environment variable rewrites a golden.
- With `CI` set, `-update` **fails** the test and leaves the file alone: goldens are
  re-recorded locally, and every updated file is reviewed.
- A missing golden fails (record it with `-update`); nothing is created without the
  switch.

## Fuzz targets for untrusted bytes (QG-6, DH-ADR-TOOLCHAIN-3)

A package that parses untrusted bytes (session log lines, configuration files,
tool-call JSON, streamed model responses, JSON-RPC and MCP frames, patch text,
shell-command splitting) declares it with the marker line in one of its non-test
files:

```go
//dsh:untrusted-parser JSON-RPC frames read from a peer
package jsonrpc
```

The `fuzzcheck` analyzer of `tools/harness-gate/analyzers` (run by the `vet` gate)
then requires, in the package's own (not `_test`-suffixed) test files:

1. a fuzz target `func FuzzXxx(f *testing.F)`;
2. a `seeds_test.go` that registers the seeds **in code** with `f.Add`, from a Go
   slice or from `//go:embed` bytes, so a plain `go test` runs every seed as an
   ordinary case with no file read at run time (`seeds_test.go` may not import `os`
   or `io/ioutil`);
3. every fuzz target registers its seeds from `seeds_test.go`: it is declared there
   and calls `f.Add`, or it passes `f` to a `seeds_test.go` function that calls
   `Add` on the parameter `f` is passed as (a helper `add(unused, seeded *testing.F)`
   seeds only through `seeded`; helpers may chain). A function that reassigns its
   `*testing.F` seeds nothing, a call through a name the function declares itself
   (`addSeeds := func(*testing.F) {}`) is not a call of the helper, and `f.Add`
   outside `seeds_test.go` is reported;
4. every `f.Add` argument is **compiled in**: built from literals, composite
   literals of unnamed types (`[][]byte{...}`, never `seedText{}`), conversions to
   predeclared or unnamed slice types (`[]byte("...")`), the builtins
   `append`/`len`/`make`/..., the pure stdlib packages `strings`, `bytes`, `strconv`,
   `math`, `encoding/hex`, `encoding/base64` and `unicode/utf8`/`utf16` (not `fmt` or
   `encoding/json`, which dispatch to user `String`/`MarshalJSON` methods),
   constants and variables declared in `seeds_test.go` (initialised the same way, or
   `//go:embed`), and locals derived from them. `f.Add(readSeed())`,
   `f.Add(fmt.Sprint(v))`, a value of a named type, a local filled by a helper, a
   variable of another file, a predeclared name another file redeclares, or a
   `seeds_test.go` variable that another test file assigns, copies into or takes the
   address of is reported at the argument (`len`/`cap`/conversions elsewhere only read).
   Writes follow storage: `alias := b`, `alias := b[1:]`, `[][]byte{b}`,
   `append(b[:0], ...)` or `for _, s := range seeds` join one alias class, so
   `copy(alias, readSeed())` taints `b` too; package-level `var alias = seed` and
   writes to it in any test file count the same way. Fresh allocations, scalars,
   strings and detached copies (`[]byte("y")`, `bytes.Clone(b)`, `copy(scratch, b)`
   of a flat `[]byte`, `len(b)`, `string(b)`) stay independent, and so do copies of
   numbers and strings (`copied := seed` of an `int` or `string`). Another test file
   may alias a seed through any chain (`var a = seed; var b = a`) as long as no
   member of the chain is written; such a variable is still never a seed source.
   Names follow Go's block scopes: a variable of the same name declared in another
   scope (the `b` parameter of the `f.Fuzz` callback next to a seed loop's `b`) is a
   different variable, while a seed the callback captures and writes is still caught. `nil`, `true`,
   `false` and `iota` are trusted only when no declaration in the package or function
   renames them.

```go
// seeds_test.go
package jsonrpc

import "testing"

var frameSeeds = [][]byte{[]byte(`{"jsonrpc":"2.0","id":1}`), []byte(`{`), nil}

func FuzzDecodeFrame(f *testing.F) {
	for _, s := range frameSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = DecodeFrame(b) })
}
```

An on-disk corpus under `testdata/fuzz/<FuzzName>/` is optional. `go test` loads it
from the package directory by itself (that is where `go test -fuzz` writes every
crasher it finds, and a committed crasher then runs under plain `go test`); the
analyzer neither requires nor reads it.
