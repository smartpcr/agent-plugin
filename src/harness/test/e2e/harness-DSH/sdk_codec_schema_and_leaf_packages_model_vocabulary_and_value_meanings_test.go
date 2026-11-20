//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/archtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/glob"
)

const (
	mvvVocabDirRel = "src/harness/internal/llm/vocab"
	mvvGoldenRel   = "src/harness/internal/llm/vocab/testdata/fingerprints.golden"
)

// mvvPersisted mirrors the persisted vocabulary types the golden pins.
var mvvPersisted = []struct {
	name string
	typ  reflect.Type
}{
	{"vocab.Message", reflect.TypeFor[vocab.Message]()},
	{"vocab.Block", reflect.TypeFor[vocab.Block]()},
	{"vocab.Source", reflect.TypeFor[vocab.Source]()},
	{"vocab.ToolSchema", reflect.TypeFor[vocab.ToolSchema]()},
	{"vocab.Controls", reflect.TypeFor[vocab.Controls]()},
	{"vocab.CallConfig", reflect.TypeFor[vocab.CallConfig]()},
	{"vocab.Meta", reflect.TypeFor[vocab.Meta]()},
	{"vocab.Usage", reflect.TypeFor[vocab.Usage]()},
	{"vocab.Failure", reflect.TypeFor[vocab.Failure]()},
}

type mvvGlobRow struct {
	pattern string
	name    string
	want    bool
	got     bool
}

type modelVocabValues struct {
	root string

	// canonical encoding
	msgs    [2]vocab.Message
	rawArgs [2]string
	encoded [2][]byte
	digests [2]string

	// fingerprints
	golden map[string]string
	order  []string
	actual map[string]string

	// no request type
	sources fstest.MapFS
	found   []string
	control []string

	// glob
	rows []mvvGlobRow
}

func mvvRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "src", "harness", "go.mod")); err == nil {
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

func (w *modelVocabValues) path(rel string) string {
	return filepath.Join(w.root, filepath.FromSlash(rel))
}

// mvvObject writes a JSON object with keys in the given order; each value is a
// nested object whose keys are also order-dependent.
func mvvObject(order []string) string {
	var b strings.Builder
	b.WriteString("{")
	for i, k := range order {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q: {%q: 1, \"z\": [true, null, 1.50], \"a\": \"<&>\"}", k, k+"2")
	}
	b.WriteString("}")
	return b.String()
}

func mvvMessage(order []string) (vocab.Message, string, error) {
	raw := mvvObject(order)
	src := &vocab.Source{Kind: "compaction", Producers: []string{"a14"}, Data: json.RawMessage(raw)}
	m, err := vocab.RestoreMessage("msg_1", vocab.RoleAssistant, src,
		vocab.TextBlock("<b>& é"),
		vocab.ReasoningBlock("thinking", "sig", false),
		vocab.ToolUseBlock("call_1", "read", json.RawMessage(raw)),
	)
	return m, raw, err
}

// ---- Scenario: Canonical encoding is stable ----

func (w *modelVocabValues) twoMessagesDifferentOrders() error {
	orders := [2][]string{{"alpha", "beta", "gamma", "delta"}, {"delta", "gamma", "alpha", "beta"}}
	for i, o := range orders {
		m, raw, err := mvvMessage(o)
		if err != nil {
			return fmt.Errorf("build message %d: %w", i, err)
		}
		w.msgs[i], w.rawArgs[i] = m, raw
	}
	if w.rawArgs[0] == w.rawArgs[1] {
		return errors.New("precondition: the two inputs must differ in key insertion order")
	}
	// A plain json.Marshal keeps raw fields as written, so the inputs differ before canonicalization.
	p0, _ := json.Marshal(w.msgs[0])
	p1, _ := json.Marshal(w.msgs[1])
	if bytes.Equal(p0, p1) {
		return errors.New("precondition: non-canonical encodings unexpectedly identical")
	}
	return nil
}

func (w *modelVocabValues) eachCanonicallyEncoded() error {
	for i := range w.msgs {
		b, err := vocab.Encode(w.msgs[i])
		if err != nil {
			return fmt.Errorf("encode %d: %w", i, err)
		}
		d, err := vocab.Digest(w.msgs[i])
		if err != nil {
			return fmt.Errorf("digest %d: %w", i, err)
		}
		w.encoded[i], w.digests[i] = b, d
	}
	return nil
}

func (w *modelVocabValues) bytesAndDigestsIdentical() error {
	if !bytes.Equal(w.encoded[0], w.encoded[1]) {
		return fmt.Errorf("canonical bytes differ:\n%s\n%s", w.encoded[0], w.encoded[1])
	}
	if w.digests[0] != w.digests[1] {
		return fmt.Errorf("digests differ: %s vs %s", w.digests[0], w.digests[1])
	}
	sum := sha256.Sum256(w.encoded[0])
	if want := "sha256:" + hex.EncodeToString(sum[:]); w.digests[0] != want {
		return fmt.Errorf("digest %s is not the SHA-256 of the canonical bytes (%s)", w.digests[0], want)
	}
	b := w.encoded[0]
	if bytes.ContainsAny(b, "\n\t") || bytes.Contains(b, []byte(`": `)) || bytes.Contains(b, []byte(`, "`)) {
		return fmt.Errorf("canonical bytes hold insignificant white space:\n%s", b)
	}
	for _, want := range []string{`{"alpha":{"a":"<&>","alpha2":1,"z":[true,null,1.50]},"beta":`, `"<b>& é"`} {
		if !bytes.Contains(b, []byte(want)) {
			return fmt.Errorf("canonical bytes lack %s:\n%s", want, b)
		}
	}
	again, err := vocab.Canonical(b)
	if err != nil || !bytes.Equal(again, b) {
		return fmt.Errorf("canonical form is not a fixed point (err=%v)", err)
	}
	return nil
}

// ---- Scenario: Vocabulary fingerprints unchanged ----

func (w *modelVocabValues) committedFingerprintGolden() error {
	f, err := os.Open(w.path(mvvGoldenRel))
	if err != nil {
		return fmt.Errorf("committed golden: %w", err)
	}
	defer f.Close()
	w.golden = map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		name, fp, ok := strings.Cut(line, " ")
		if !ok || !strings.HasPrefix(fp, "sha256:") {
			return fmt.Errorf("malformed golden line %q", line)
		}
		if _, dup := w.golden[name]; dup {
			return fmt.Errorf("golden names %s twice", name)
		}
		w.golden[name] = fp
		w.order = append(w.order, name)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(w.golden) == 0 {
		return errors.New("golden is empty")
	}
	return nil
}

func (w *modelVocabValues) fingerprintEveryVocabType() error {
	w.actual = map[string]string{}
	for _, p := range mvvPersisted {
		ty, err := schema.ForType(p.typ)
		if err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		w.actual[p.name] = ty.Fingerprint()
	}
	return nil
}

func (w *modelVocabValues) eachEqualsGoldenLine() error {
	var problems []string
	for _, p := range mvvPersisted {
		want, ok := w.golden[p.name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s has no golden line", p.name))
			continue
		}
		if got := w.actual[p.name]; got != want {
			problems = append(problems, fmt.Sprintf("%s: fingerprint %s, golden %s", p.name, got, want))
		}
	}
	for _, name := range w.order {
		if _, ok := w.actual[name]; !ok {
			problems = append(problems, fmt.Sprintf("golden line %s names no persisted vocab type", name))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// ---- Scenario: vocab holds no request type ----

func (w *modelVocabValues) parsedVocabSources() error {
	dir := w.path(mvvVocabDirRel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	w.sources = fstest.MapFS{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		w.sources[e.Name()] = &fstest.MapFile{Data: b}
	}
	if len(w.sources) == 0 {
		return fmt.Errorf("no Go sources in %s", dir)
	}
	return nil
}

func (w *modelVocabValues) archtestLooksForRequest() error {
	found, err := archtest.RequestTypes(w.sources)
	if err != nil {
		return fmt.Errorf("archtest.RequestTypes: %w", err)
	}
	w.found = found

	// Control: the same predicate must catch a planted request type, so "none
	// found" is not a vacuous pass.
	planted := fstest.MapFS{}
	for k, v := range w.sources {
		planted[k] = v
	}
	planted["zz_planted.go"] = &fstest.MapFile{Data: []byte(
		"package vocab\n\ntype Request struct{}\n\ntype Call struct {\n\tMessages []Message\n\tControls Controls\n}\n")}
	control, err := archtest.RequestTypes(planted)
	if err != nil {
		return fmt.Errorf("archtest.RequestTypes (control): %w", err)
	}
	w.control = control
	return nil
}

func (w *modelVocabValues) noneFound() error {
	if len(w.found) > 0 {
		return fmt.Errorf("vocab holds request types:\n%s", strings.Join(w.found, "\n"))
	}
	if len(w.control) != 2 {
		return fmt.Errorf("predicate control expected 2 findings, got %d: %v", len(w.control), w.control)
	}
	return nil
}

// ---- Scenario: Glob meaning is single ----

func (w *modelVocabValues) globTable(table *godog.Table) error {
	if len(table.Rows) < 2 {
		return errors.New("glob table has no rows")
	}
	for _, r := range table.Rows[1:] {
		if len(r.Cells) != 3 {
			return fmt.Errorf("glob row needs 3 cells, got %d", len(r.Cells))
		}
		want, err := strconv.ParseBool(r.Cells[2].Value)
		if err != nil {
			return err
		}
		w.rows = append(w.rows, mvvGlobRow{pattern: r.Cells[0].Value, name: r.Cells[1].Value, want: want})
	}
	return nil
}

func (w *modelVocabValues) matchedWithGlob() error {
	for i := range w.rows {
		m, err := glob.Compile(w.rows[i].pattern)
		if err != nil {
			return fmt.Errorf("compile %q: %w", w.rows[i].pattern, err)
		}
		// The name goes through FromOS as any file-system path would.
		w.rows[i].got = m.Match(glob.FromOS(filepath.FromSlash(w.rows[i].name)))
	}
	return nil
}

func (w *modelVocabValues) resultsEqualTable() error {
	var bad []string
	for _, r := range w.rows {
		if r.got != r.want {
			bad = append(bad, fmt.Sprintf("%q vs %q: got %v, want %v", r.pattern, r.name, r.got, r.want))
		}
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "\n"))
	}
	return nil
}

func InitializeScenario_sdk_codec_schema_and_leaf_packages_model_vocabulary_and_value_meanings(ctx *godog.ScenarioContext) {
	w := &modelVocabValues{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = modelVocabValues{}
		root, err := mvvRepoRoot()
		if err != nil {
			return c, err
		}
		w.root = root
		return c, nil
	})

	ctx.Step(`^two Message values built with map fields inserted in different orders$`, w.twoMessagesDifferentOrders)
	ctx.Step(`^each is canonically encoded$`, w.eachCanonicallyEncoded)
	ctx.Step(`^the bytes and SHA-256 digests are identical$`, w.bytesAndDigestsIdentical)

	ctx.Step(`^the committed fingerprint golden$`, w.committedFingerprintGolden)
	ctx.Step(`^the schema package fingerprints every vocab type$`, w.fingerprintEveryVocabType)
	ctx.Step(`^each equals its golden line$`, w.eachEqualsGoldenLine)

	ctx.Step(`^the parsed AST of every file in internal/llm/vocab$`, w.parsedVocabSources)
	ctx.Step(`^the archtest predicate looks for a type named Request or a struct holding \[\]Message plus Controls$`, w.archtestLooksForRequest)
	ctx.Step(`^none is found$`, w.noneFound)

	ctx.Step(`^the glob table:$`, w.globTable)
	ctx.Step(`^matched with the util glob package$`, w.matchedWithGlob)
	ctx.Step(`^results equal the table$`, w.resultsEqualTable)
}

func TestE2E_sdk_codec_schema_and_leaf_packages_model_vocabulary_and_value_meanings(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "sdk_codec_schema_and_leaf_packages_model_vocabulary_and_value_meanings",
		ScenarioInitializer: InitializeScenario_sdk_codec_schema_and_leaf_packages_model_vocabulary_and_value_meanings,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"sdk_codec_schema_and_leaf_packages_model_vocabulary_and_value_meanings.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
