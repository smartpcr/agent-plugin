//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/archtest"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

const (
	jcsSeedsRel  = "src/sdk/jsonrpc/seeds_test.go"
	jcsCorpusRel = "src/sdk/jsonrpc/testdata/fuzz/FuzzDecode"
	jcsSchemaRel = "src/sdk/protocol/schema.json"

	// jcsBound is the line bound of the oversized-line scenario.
	jcsBound = 256 << 10
	// jcsReadBuffer mirrors the codec's unexported read-buffer cap (64 KiB): the
	// reader may pull at most one buffer past the bound before it gives up.
	jcsReadBuffer = 64 << 10
)

// jcsCountingReader counts the bytes the codec pulls from the peer.
type jcsCountingReader struct {
	r io.Reader
	n int
}

func (c *jcsCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

type jcsInput struct {
	Name string
	Data []byte
}

type jcsGoRun struct {
	Label  string
	Output string
	Err    error
}

type jsonrpcCodecSchema struct {
	root string

	// oversized line
	control     []byte
	controlErr  error
	src         *jcsCountingReader
	reader      *jsonrpc.Reader
	line        []byte
	lineErr     error
	stickyErr   error
	allocated   uint64
	inputLength int

	// seeds and corpus
	inputs   []jcsInput
	failures []string
	goTest   jcsGoRun

	// schema
	derived []byte

	// archtest
	pkgs []archtest.Package
}

// jcsRepoRoot walks up from the working directory to the checkout holding go.work.
func jcsRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "src", "sdk", "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.work found above the test directory")
		}
		dir = parent
	}
}

func (w *jsonrpcCodecSchema) path(rel string) string {
	return filepath.Join(w.root, filepath.FromSlash(rel))
}

// goTool runs the pinned go toolchain in dir; inherited GOFLAGS are dropped so the
// scenario decides the flags (no -fuzz, no -update).
func (w *jsonrpcCodecSchema) goTool(dir string, args ...string) jcsGoRun {
	label := fmt.Sprintf("go %s (in %s)", strings.Join(args, " "), dir)
	gobin, err := exec.LookPath("go")
	if err != nil {
		return jcsGoRun{Label: label, Err: fmt.Errorf("the pinned go toolchain is required: %w", err)}
	}
	cmd := exec.Command(gobin, args...)
	cmd.Dir = dir
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		return strings.EqualFold(key, "GOFLAGS")
	})
	out, err := cmd.CombinedOutput()
	return jcsGoRun{Label: label, Output: string(out), Err: err}
}

// decodeInput holds the property every input must satisfy: Decode returns a frame
// that re-encodes stably, or a table error of parse-error / invalid-request; a line
// reader over the same bytes fails only with table errors or io.EOF. A panic is
// recovered and reported as a failure.
func decodeInput(b []byte) (failure string) {
	defer func() {
		if r := recover(); r != nil {
			failure = fmt.Sprintf("panicked: %v", r)
		}
	}()
	f, err := jsonrpc.Decode(b)
	if err != nil {
		if msg := tableError(err, jsonrpc.ReasonParseError, jsonrpc.ReasonInvalidRequest); msg != "" {
			return "Decode: " + msg
		}
	} else {
		enc, err := jsonrpc.Encode(f)
		if err != nil {
			return fmt.Sprintf("Encode(Decode) failed: %v", err)
		}
		f2, err := jsonrpc.Decode(enc)
		if err != nil {
			return fmt.Sprintf("Decode(Encode(Decode)) = %q failed: %v", enc, err)
		}
		if reflect.TypeOf(f) != reflect.TypeOf(f2) {
			return fmt.Sprintf("frame kind changed over a round trip: %T then %T", f, f2)
		}
		enc2, err := jsonrpc.Encode(f2)
		if err != nil || !bytes.Equal(enc, enc2) {
			return fmt.Sprintf("encoding is not stable: %q then %q (%v)", enc, enc2, err)
		}
	}
	r := jsonrpc.NewReader(bytes.NewReader(b), 64)
	for range 1 + bytes.Count(b, []byte{'\n'}) {
		_, err := r.Read()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if msg := tableError(err, jsonrpc.ReasonParseError, jsonrpc.ReasonInvalidRequest, jsonrpc.ReasonLineTooLong); msg != "" {
			return "Reader: " + msg
		}
		if jsonrpc.HasReason(err, jsonrpc.ReasonLineTooLong) {
			break
		}
	}
	return ""
}

func tableError(err error, allowed ...string) string {
	var e *jsonrpc.Error
	if !errors.As(err, &e) {
		return fmt.Sprintf("error %v (%T) is not a table error", err, err)
	}
	if _, ok := jsonrpc.Lookup(e.Reason); !ok {
		return fmt.Sprintf("reason %q is not in the table", e.Reason)
	}
	if !slices.Contains(allowed, e.Reason) {
		return fmt.Sprintf("reason %q, want one of %v", e.Reason, allowed)
	}
	return ""
}

func (w *jsonrpcCodecSchema) decodeAll() {
	w.failures = nil
	for _, in := range w.inputs {
		if msg := decodeInput(in.Data); msg != "" {
			w.failures = append(w.failures, fmt.Sprintf("%s (%q): %s", in.Name, in.Data, msg))
		}
	}
}

// byteSliceLiteral reads []byte("...") / []byte(`...`) or nil.
func byteSliceLiteral(e ast.Expr) ([]byte, error) {
	if id, ok := e.(*ast.Ident); ok && id.Name == "nil" {
		return nil, nil
	}
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, fmt.Errorf("unsupported element %T", e)
	}
	at, ok := call.Fun.(*ast.ArrayType)
	if !ok || at.Len != nil {
		return nil, errors.New("element is not a []byte conversion")
	}
	if elt, ok := at.Elt.(*ast.Ident); !ok || elt.Name != "byte" {
		return nil, errors.New("element is not a []byte conversion")
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil, errors.New("element is not a string literal")
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// ---------------------------------------------------------------- scenario 1

func (w *jsonrpcCodecSchema) anInMemoryReaderWithOneLineOneByteOverTheBound() error {
	w.control = []byte(strings.Repeat("b", jcsBound) + "\n")
	input := strings.Repeat("a", jcsBound+1) + "\n" + `{"jsonrpc":"2.0","method":"x"}` + "\n" + strings.Repeat("c", 4*jcsBound) + "\n"
	w.inputLength = len(input)
	w.src = &jcsCountingReader{r: strings.NewReader(input)}
	w.reader = jsonrpc.NewReader(w.src, jcsBound)
	if got := w.reader.MaxLineBytes(); got != jcsBound {
		return fmt.Errorf("MaxLineBytes = %d, want %d", got, jcsBound)
	}
	return nil
}

func (w *jsonrpcCodecSchema) theCodecReads() error {
	// A line of exactly the bound is accepted: the bound is inclusive.
	got, err := jsonrpc.NewReader(bytes.NewReader(w.control), jcsBound).ReadLine()
	if err != nil || len(got) != jcsBound {
		w.controlErr = fmt.Errorf("a line of exactly the bound: %d bytes, %v", len(got), err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	w.line, w.lineErr = w.reader.ReadLine()
	runtime.ReadMemStats(&after)
	w.allocated = after.TotalAlloc - before.TotalAlloc
	_, w.stickyErr = w.reader.Read()
	return nil
}

func (w *jsonrpcCodecSchema) itReturns(reason string) error {
	if w.controlErr != nil {
		return w.controlErr
	}
	if w.line != nil {
		return fmt.Errorf("ReadLine returned %d bytes, want none", len(w.line))
	}
	if !jsonrpc.HasReason(w.lineErr, reason) {
		return fmt.Errorf("ReadLine err = %v, want %s", w.lineErr, reason)
	}
	if msg := tableError(w.lineErr, reason); msg != "" {
		return errors.New(msg)
	}
	if !jsonrpc.HasReason(w.stickyErr, reason) {
		return fmt.Errorf("the next Read err = %v, want the final %s", w.stickyErr, reason)
	}
	return nil
}

func (w *jsonrpcCodecSchema) itDoesNotAllocatePastTheBound() error {
	// The line buffer grows by doubling and is capped at the bound: below 2x the
	// bound in total, plus slack for the read buffer, the error and the runtime.
	if limit := uint64(2*jcsBound + 2*jcsReadBuffer); w.allocated > limit {
		return fmt.Errorf("reading a line one byte over a %d-byte bound allocated %d bytes (limit %d)", jcsBound, w.allocated, limit)
	}
	if w.src.n > jcsBound+jcsReadBuffer+1 {
		return fmt.Errorf("the codec pulled %d of %d input bytes; it should stop just past the bound %d", w.src.n, w.inputLength, jcsBound)
	}
	return nil
}

// ---------------------------------------------------------------- scenario 2

func (w *jsonrpcCodecSchema) theSeedSliceCompiledInto(rel string) error {
	file := w.path(rel)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		return err
	}
	if f.Name.Name != "jsonrpc" {
		return fmt.Errorf("%s is package %s, want jsonrpc", rel, f.Name.Name)
	}
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "os" || p == "io/ioutil" {
			return fmt.Errorf("%s imports %s: the seeds must be compiled in, not read at run time", rel, p)
		}
	}
	var elts []ast.Expr
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "decodeSeeds" || len(vs.Values) != 1 {
			return true
		}
		if cl, ok := vs.Values[0].(*ast.CompositeLit); ok {
			elts, found = cl.Elts, true
		}
		return false
	})
	if !found {
		return fmt.Errorf("%s declares no decodeSeeds composite literal", rel)
	}
	w.inputs = nil
	for i, e := range elts {
		b, err := byteSliceLiteral(e)
		if err != nil {
			return fmt.Errorf("seed %d at %s: %w", i, fset.Position(e.Pos()), err)
		}
		w.inputs = append(w.inputs, jcsInput{Name: fmt.Sprintf("seed#%d", i), Data: b})
	}
	if len(w.inputs) == 0 {
		return errors.New("the seed slice is empty")
	}
	return nil
}

func (w *jsonrpcCodecSchema) testDecodeSeedsFeedsEachSeed() error {
	w.decodeAll()
	w.goTest = w.goTool(w.path("src/sdk"), "test", "-count=1", "-v", "-run", "^TestDecodeSeeds$", "./jsonrpc")
	return nil
}

func (w *jsonrpcCodecSchema) everySeedDecodesOrFailsWithATableError() error {
	if len(w.failures) > 0 {
		return fmt.Errorf("%d of %d seeds broke the decode property:\n%s", len(w.failures), len(w.inputs), strings.Join(w.failures, "\n"))
	}
	if w.goTest.Err != nil {
		return fmt.Errorf("%s: %v\n%s", w.goTest.Label, w.goTest.Err, w.goTest.Output)
	}
	if !strings.Contains(w.goTest.Output, "--- PASS: TestDecodeSeeds") {
		return fmt.Errorf("%s did not run TestDecodeSeeds:\n%s", w.goTest.Label, w.goTest.Output)
	}
	return nil
}

func (w *jsonrpcCodecSchema) noSeedPanics() error {
	if strings.Contains(w.goTest.Output, "panic:") {
		return fmt.Errorf("%s panicked:\n%s", w.goTest.Label, w.goTest.Output)
	}
	for _, f := range w.failures {
		if strings.Contains(f, "panicked") {
			return errors.New(f)
		}
	}
	return nil
}

// ---------------------------------------------------------------- scenario 3

func (w *jsonrpcCodecSchema) theCommittedFilesIn(rel string) error {
	dir := w.path(rel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	w.inputs = nil
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n"), "\n")
		if len(lines) != 2 || lines[0] != "go test fuzz v1" {
			return fmt.Errorf("corpus file %s is not a one-argument go fuzz v1 file", e.Name())
		}
		expr, err := parser.ParseExpr(lines[1])
		if err != nil {
			return fmt.Errorf("corpus file %s: %w", e.Name(), err)
		}
		b, err := byteSliceLiteral(expr)
		if err != nil {
			return fmt.Errorf("corpus file %s: %w", e.Name(), err)
		}
		w.inputs = append(w.inputs, jcsInput{Name: e.Name(), Data: b})
	}
	if len(w.inputs) == 0 {
		return fmt.Errorf("no corpus files in %s", rel)
	}
	return nil
}

func (w *jsonrpcCodecSchema) goTestRunsWithoutFuzz(command string) error {
	args := strings.Fields(command)
	if len(args) < 4 || args[0] != "go" || args[1] != "-C" || slices.Contains(args, "-fuzz") {
		return fmt.Errorf("unexpected command %q", command)
	}
	dir := w.path(args[2])
	rest := args[3:]
	// -v names every corpus entry; -count=1 defeats the test cache; ^...$ keeps the
	// run to the fuzz target itself.
	run := []string{rest[0], "-count=1", "-v"}
	for i := 1; i < len(rest); i++ {
		if rest[i] == "-run" && i+1 < len(rest) {
			run = append(run, "-run", "^"+rest[i+1]+"$")
			i++
			continue
		}
		run = append(run, rest[i])
	}
	w.goTest = w.goTool(dir, run...)
	w.decodeAll()
	return nil
}

func (w *jsonrpcCodecSchema) theGoTestRunnerReadsEveryCorpusFile() error {
	if w.goTest.Err != nil {
		return fmt.Errorf("%s: %v\n%s", w.goTest.Label, w.goTest.Err, w.goTest.Output)
	}
	if !strings.Contains(w.goTest.Output, "--- PASS: FuzzDecode") {
		return fmt.Errorf("%s did not run FuzzDecode:\n%s", w.goTest.Label, w.goTest.Output)
	}
	var missing []string
	for _, in := range w.inputs {
		if !strings.Contains(w.goTest.Output, "--- PASS: FuzzDecode/"+in.Name) {
			missing = append(missing, in.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the runner did not replay corpus files %v:\n%s", missing, w.goTest.Output)
	}
	return nil
}

func (w *jsonrpcCodecSchema) eachCorpusInputDecodesOrFails() error {
	if strings.Contains(w.goTest.Output, "panic:") || strings.Contains(w.goTest.Output, "--- FAIL") {
		return fmt.Errorf("%s failed:\n%s", w.goTest.Label, w.goTest.Output)
	}
	if len(w.failures) > 0 {
		return fmt.Errorf("%d of %d corpus inputs broke the decode property:\n%s", len(w.failures), len(w.inputs), strings.Join(w.failures, "\n"))
	}
	return nil
}

// ---------------------------------------------------------------- scenario 4

func (w *jsonrpcCodecSchema) theProtocolGoTypes() error {
	if len(protocol.Methods) == 0 || len(protocol.Errors) == 0 {
		return fmt.Errorf("protocol declares %d methods and %d errors", len(protocol.Methods), len(protocol.Errors))
	}
	return nil
}

func (w *jsonrpcCodecSchema) theSchemaIsDerived() error {
	var err error
	w.derived, err = protocol.Document()
	if err != nil {
		return err
	}
	again, err := protocol.Document()
	if err != nil {
		return err
	}
	if !bytes.Equal(w.derived, again) {
		return errors.New("two derivations of the schema differ")
	}
	return nil
}

func (w *jsonrpcCodecSchema) itIsByteIdenticalToCommitted(rel string) error {
	golden, err := os.ReadFile(w.path(rel))
	if err != nil {
		return err
	}
	if !bytes.Equal(w.derived, golden) {
		i := 0
		for i < len(w.derived) && i < len(golden) && w.derived[i] == golden[i] {
			i++
		}
		return fmt.Errorf("derived schema (%d bytes) differs from %s (%d bytes) at byte %d: run `go -C src/sdk generate ./protocol` and commit it", len(w.derived), rel, len(golden), i)
	}
	if !bytes.Equal(protocol.Schema(), golden) {
		return fmt.Errorf("the schema embedded in package protocol differs from %s on disk", rel)
	}
	return nil
}

// ---------------------------------------------------------------- scenario 5

func (w *jsonrpcCodecSchema) theThreeModules() error {
	for dir, mod := range map[string]string{"src/go": archtest.GoModule, "src/sdk": archtest.SDKModule, "src/harness": archtest.HarnessModule} {
		b, err := os.ReadFile(filepath.Join(w.path(dir), "go.mod"))
		if err != nil {
			return err
		}
		first, _, _ := strings.Cut(strings.ReplaceAll(string(b), "\r", ""), "\n")
		if strings.TrimSpace(first) != "module "+mod {
			return fmt.Errorf("%s/go.mod declares %q, want module %s", dir, first, mod)
		}
	}
	return nil
}

func (w *jsonrpcCodecSchema) theArchtestListsDependencies(rel string) error {
	res := w.goTool(w.path(rel), "list", "-deps", "-json", "./...")
	if res.Err != nil {
		return fmt.Errorf("%s: %v\n%s", res.Label, res.Err, res.Output)
	}
	pkgs, err := archtest.Parse(strings.NewReader(res.Output))
	if err != nil {
		return err
	}
	w.pkgs = pkgs
	for _, want := range []string{archtest.SDKModule + "/jsonrpc", archtest.SDKModule + "/protocol"} {
		if !slices.ContainsFunc(pkgs, func(p archtest.Package) bool { return p.ImportPath == want }) {
			return fmt.Errorf("go list -deps did not list %s", want)
		}
	}
	return nil
}

func (w *jsonrpcCodecSchema) noneHasPrefix(a, b string) error {
	var bad []string
	for _, p := range w.pkgs {
		for _, prefix := range []string{a, b} {
			if p.ImportPath == prefix || strings.HasPrefix(p.ImportPath, prefix+"/") {
				bad = append(bad, p.ImportPath)
			}
		}
	}
	for _, r := range archtest.Rules {
		if r.Module == archtest.SDKModule {
			bad = append(bad, r.Check(w.pkgs)...)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("src/sdk depends on the product: %v", bad)
	}
	return nil
}

// ---------------------------------------------------------------- wiring

func InitializeScenario_sdk_codec_schema_and_leaf_packages_jsonrpc_codec_and_shape_schema(ctx *godog.ScenarioContext) {
	w := &jsonrpcCodecSchema{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		root, err := jcsRepoRoot()
		if err != nil {
			return c, err
		}
		*w = jsonrpcCodecSchema{root: root}
		return c, nil
	})

	ctx.Step(`^an in-memory reader with one line one byte over the configured bound$`, w.anInMemoryReaderWithOneLineOneByteOverTheBound)
	ctx.Step(`^the codec reads$`, w.theCodecReads)
	ctx.Step(`^it returns "([^"]+)"$`, w.itReturns)
	ctx.Step(`^it does not allocate past the bound$`, w.itDoesNotAllocatePastTheBound)

	ctx.Step(`^the seed slice compiled into "([^"]+)"$`, w.theSeedSliceCompiledInto)
	ctx.Step(`^TestDecodeSeeds feeds each seed to the decoder in memory$`, w.testDecodeSeedsFeedsEachSeed)
	ctx.Step(`^every seed decodes or fails with a table error$`, w.everySeedDecodesOrFailsWithATableError)
	ctx.Step(`^no seed panics$`, w.noSeedPanics)

	ctx.Step(`^the committed files in "([^"]+)"$`, w.theCommittedFilesIn)
	ctx.Step(`^"([^"]+)" runs without -fuzz$`, w.goTestRunsWithoutFuzz)
	ctx.Step(`^the Go test runner reads every corpus file$`, w.theGoTestRunnerReadsEveryCorpusFile)
	ctx.Step(`^each corpus input decodes or fails with a table error, none panicking$`, w.eachCorpusInputDecodesOrFails)

	ctx.Step(`^the protocol Go types$`, w.theProtocolGoTypes)
	ctx.Step(`^the schema is derived$`, w.theSchemaIsDerived)
	ctx.Step(`^it is byte-identical to committed "([^"]+)"$`, w.itIsByteIdenticalToCommitted)

	ctx.Step(`^the three modules$`, w.theThreeModules)
	ctx.Step(`^the archtest lists "([^"]+)" dependencies$`, w.theArchtestListsDependencies)
	ctx.Step(`^none has prefix "([^"]+)" or "([^"]+)"$`, w.noneHasPrefix)
}

func TestE2E_sdk_codec_schema_and_leaf_packages_jsonrpc_codec_and_shape_schema(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_sdk_codec_schema_and_leaf_packages_jsonrpc_codec_and_shape_schema,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"sdk_codec_schema_and_leaf_packages_jsonrpc_codec_and_shape_schema.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
