//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/storetest"
)

// evscOuterT is the suite's *testing.T: storetest.Run needs a real *testing.T to
// run its cases as subtests, which godog's per-scenario TestingT does not expose.
var evscOuterT *testing.T

// evscStoreTestdata is the committed persisted-type golden directory, relative
// to this package.
var evscStoreTestdata = filepath.Join("..", "..", "..", "internal", "session", "store", "testdata")

// evscChangedToolCall is tool/call with one added field and no version bump.
type evscChangedToolCall struct {
	event.ToolCallPayload
	Timeout int `json:"timeout,omitempty"`
}

type evscState struct {
	// unknown version
	rec     event.Record
	decoded event.ToolResultPayload
	decErr  error

	// conformance
	factory   storetest.Factory
	mu        sync.Mutex
	fresh     map[string]int
	suiteOK   bool
	suiteRan  bool
	failedOut []string

	// inventory
	golden   []store.InventoryEntry
	notes    []store.InventoryNote
	current  []store.InventoryEntry
	problems []string
}

func (s *evscState) registryKnows(name string, version int) error {
	info, ok := event.Default().Lookup(name)
	if !ok {
		return fmt.Errorf("default registry does not declare %q", name)
	}
	if info.Version != version {
		return fmt.Errorf("default registry knows %q at version %d, want %d", name, info.Version, version)
	}
	return nil
}

func (s *evscState) encodedEvent(name string, version, seq int) error {
	s.rec = event.Record{Seq: event.Seq(seq), Type: name, Version: version, Op: event.OpAppend,
		Payload: `{"call_id":"c1","is_error":true,"message":{"role":"tool","blocks":[]},"new_field":1}`}
	return nil
}

func (s *evscState) decodeEvent() error {
	s.decoded, s.decErr = event.ToolResult.Decode(s.rec)
	return nil
}

func (s *evscState) typedUnknownVersion(name string, version, known, seq int) error {
	var newer *event.NewerPayloadError
	if !errors.As(s.decErr, &newer) || !errors.Is(s.decErr, event.ErrNewerPayload) {
		return fmt.Errorf("Decode error = %v, want *event.NewerPayloadError", s.decErr)
	}
	if newer.Type != name || newer.Version != version || newer.Known != known || newer.Seq != event.Seq(seq) {
		return fmt.Errorf("error fields = %+v", *newer)
	}
	return nil
}

func (s *evscState) noValueFabricated() error {
	d := s.decoded
	if !d.Message.IsZero() || d.CallID != "" || d.IsError || d.Spill != nil {
		return fmt.Errorf("a value was fabricated: %+v", d)
	}
	return nil
}

func (s *evscState) othersRefuse() error {
	if v, err := event.Default().DecodeAny(s.rec); v != nil || !errors.Is(err, event.ErrNewerPayload) {
		return fmt.Errorf("DecodeAny = %v, %v; want nil, ErrNewerPayload", v, err)
	}
	var newer *event.NewerPayloadError
	if err := event.Default().Classify(s.rec); !errors.As(err, &newer) {
		return fmt.Errorf("Classify = %v, want *event.NewerPayloadError", err)
	}
	return nil
}

func (s *evscState) conformanceSuite() error {
	if evscOuterT == nil {
		return errors.New("no outer *testing.T; run through TestE2E_session_core_contracts_event_vocabulary_and_store_contract")
	}
	s.fresh = map[string]int{}
	s.factory = func(t *testing.T) store.Store {
		s.mu.Lock()
		s.fresh[t.Name()]++
		s.mu.Unlock()
		return memstore.New()
	}
	return nil
}

func (s *evscState) runSuite() error {
	s.suiteRan = true
	s.suiteOK = evscOuterT.Run("storetest-memstore", func(t *testing.T) {
		storetest.Run(t, s.factory)
	})
	return nil
}

func (s *evscState) everyCasePasses() error {
	if !s.suiteRan {
		return errors.New("the suite did not run")
	}
	if !s.suiteOK {
		return errors.New("storetest.Run on memstore failed; see the storetest-memstore subtest output")
	}
	if len(s.fresh) == 0 {
		return errors.New("the suite ran no cases")
	}
	return nil
}

func (s *evscState) casesRan(list string) error {
	var missing []string
	for _, name := range strings.Split(list, ",") {
		name = strings.Trim(strings.TrimSpace(name), `"`)
		found := false
		for full, n := range s.fresh {
			if strings.HasSuffix(full, "/"+name) {
				if n != 1 {
					return fmt.Errorf("case %s built %d stores, want one fresh store", name, n)
				}
				found = true
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(s.fresh))
		for k := range s.fresh {
			names = append(names, k)
		}
		slices.Sort(names)
		return fmt.Errorf("cases did not run: %v (ran %v)", missing, names)
	}
	return nil
}

func (s *evscState) committedGolden() error {
	g, err := os.ReadFile(filepath.Join(evscStoreTestdata, "persisted.golden"))
	if err != nil {
		return err
	}
	n, err := os.ReadFile(filepath.Join(evscStoreTestdata, "persisted.notes"))
	if err != nil {
		return err
	}
	if s.golden, err = store.ParseInventory(string(g)); err != nil {
		return err
	}
	if s.notes, err = store.ParseNotes(string(n)); err != nil {
		return err
	}
	if len(s.golden) == 0 {
		return errors.New("committed golden is empty")
	}
	return nil
}

// e2ePluginTypePrefix names the plugin event types sibling e2e suites define in
// event.Default() for their own scenarios; they share this test binary but are
// not part of the program's persisted vocabulary.
const e2ePluginTypePrefix = "ext/e2e-"

func (s *evscState) inventoryMatchesGolden() error {
	cur, err := store.Inventory(event.Default())
	if err != nil {
		return err
	}
	cur = slices.DeleteFunc(cur, func(e store.InventoryEntry) bool { return strings.HasPrefix(e.Type, e2ePluginTypePrefix) })
	if p := store.CheckInventory(cur, s.golden, s.notes); len(p) > 0 {
		return fmt.Errorf("unchanged registry already fails the check:\n%s", strings.Join(p, "\n"))
	}
	g, _ := os.ReadFile(filepath.Join(evscStoreTestdata, "persisted.golden"))
	if got, want := store.RenderInventory(cur), strings.ReplaceAll(string(g), "\r\n", "\n"); got != want {
		return fmt.Errorf("rendered inventory differs from committed golden:\n--- got\n%s\n--- want\n%s", got, want)
	}
	s.current = cur
	return nil
}

func (s *evscState) addFieldWithoutBump(name string) error {
	if name != "tool/call" {
		return fmt.Errorf("the test-only change is defined for tool/call, not %q", name)
	}
	same, err := store.EntryOf[event.ToolCallPayload](name, 1)
	if err != nil {
		return err
	}
	changed, err := store.EntryOf[evscChangedToolCall](name, 1)
	if err != nil {
		return err
	}
	if changed.Fingerprint == same.Fingerprint {
		return errors.New("adding a field left the fingerprint unchanged")
	}
	replaced := false
	out := slices.Clone(s.current)
	for i, e := range out {
		if e.Type == name {
			if e != same {
				return fmt.Errorf("EntryOf does not reproduce the registry's entry: %v vs %v", same, e)
			}
			out[i] = changed
			replaced = true
		}
	}
	if !replaced {
		return fmt.Errorf("%q is not in the inventory", name)
	}
	s.current = out
	return nil
}

func (s *evscState) runInventoryCheck() error {
	s.problems = store.CheckInventory(s.current, s.golden, s.notes)
	return nil
}

func (s *evscState) checkFailsNaming(name string) error {
	if len(s.problems) == 0 {
		return errors.New("the inventory check passed; the unbumped change went unnoticed")
	}
	for _, p := range s.problems {
		if !strings.HasPrefix(p, name+": ") {
			return fmt.Errorf("unexpected problem %q (want only ones naming %s)", p, name)
		}
	}
	if !strings.Contains(s.problems[0], "shape changed at version 1") {
		return fmt.Errorf("problem %q does not report the shape change", s.problems[0])
	}
	return nil
}

func InitializeScenario_session_core_contracts_event_vocabulary_and_store_contract(ctx *godog.ScenarioContext) {
	s := &evscState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = evscState{}
		return c, nil
	})

	ctx.Step(`^the default event registry knows "([^"]*)" at version (\d+)$`, s.registryKnows)
	ctx.Step(`^an in-memory encoded event "([^"]*)" at version (\d+) with seq (\d+)$`, s.encodedEvent)
	ctx.Step(`^the event is decoded$`, s.decodeEvent)
	ctx.Step(`^the decoder returns a typed unknown-version error naming "([^"]*)" version (\d+) known (\d+) at seq (\d+)$`, s.typedUnknownVersion)
	ctx.Step(`^no tool result value is fabricated$`, s.noValueFabricated)
	ctx.Step(`^the generic decoder and the classifier also refuse the event$`, s.othersRefuse)

	ctx.Step(`^the storetest conformance suite with the memstore factory$`, s.conformanceSuite)
	ctx.Step(`^the suite runs$`, s.runSuite)
	ctx.Step(`^every case passes$`, s.everyCasePasses)
	ctx.Step(`^the cases (.+) ran on a fresh store$`, s.casesRan)

	ctx.Step(`^the committed persisted-type golden and notes$`, s.committedGolden)
	ctx.Step(`^the current registry inventory matches the committed golden$`, s.inventoryMatchesGolden)
	ctx.Step(`^a test-only change adds a field to the persisted type "([^"]*)" without a version bump$`, s.addFieldWithoutBump)
	ctx.Step(`^the inventory check runs against the committed golden$`, s.runInventoryCheck)
	ctx.Step(`^the check fails naming "([^"]*)"$`, s.checkFailsNaming)
}

func TestE2E_session_core_contracts_event_vocabulary_and_store_contract(t *testing.T) {
	evscOuterT = t
	defer func() { evscOuterT = nil }()
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_session_core_contracts_event_vocabulary_and_store_contract,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"session_core_contracts_event_vocabulary_and_store_contract.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
