# internal/testkit

Test support for `src/harness`, imported only by `_test.go` files. This stage
(impl.md step 1.6, DH-D-TOOLCHAIN-3) fixes the test **layers** and their
conventions; the replay and scenario helpers themselves arrive with DH-S-16.

## Test layers (DH-ADR-TOOLCHAIN-1)

| # | layer | where it lives | runs under |
|---|-------|----------------|------------|
| 1 | unit | `*_test.go`, fakes only at the model, network and clock boundaries | plain `go test ./...` and the gate |
| 2 | composition | `*_test.go`, a real configuration through the cordis loader, scripted model | plain `go test ./...` and the gate |
| 3 | golden | `*_test.go` + `testdata/*.golden`, through [`golden`](golden/golden.go) | plain `go test ./...` and the gate |
| 4 | replay | `*_replay_test.go` with `//go:build replay` | `go test -tags replay ./...` only |
| 5 | scenario | `*_scenario_test.go` with `//go:build scenario` | `go test -tags scenario ./...` only, from the credentialed scenario workflow |

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

// TestToolchainRehearsal is DH-E-TOOLCHAIN-2.
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
- It is authored, reviewed and indexed in the story that writes it, and executed
  only at the stage impl.md assigns (EV-4); a skipped scenario stays open.

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
