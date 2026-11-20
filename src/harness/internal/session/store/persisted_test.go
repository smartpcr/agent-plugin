package store_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func committed(t *testing.T) ([]store.InventoryEntry, []store.InventoryNote) {
	t.Helper()
	inv, err := store.ParseInventory(readTestdata(t, "persisted.golden"))
	if err != nil {
		t.Fatal(err)
	}
	notes, err := store.ParseNotes(readTestdata(t, "persisted.notes"))
	if err != nil {
		t.Fatal(err)
	}
	return inv, notes
}

// TestInventory is DH-T-STORE-10: the inventory of the program's registry matches
// the committed golden, and every type, version and fingerprint in it is
// accounted for in persisted.notes.
func TestInventory(t *testing.T) {
	cur, err := store.Inventory(event.Default())
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "persisted", store.RenderInventory(cur))

	was, notes := committed(t)
	if problems := store.CheckInventory(cur, was, notes); len(problems) > 0 {
		t.Fatalf("persisted types changed without a version bump and note:\n%s", strings.Join(problems, "\n"))
	}
	// Every committed line needs its own note, so a type cannot slip in by
	// re-recording the golden alone.
	if problems := store.CheckInventory(cur, nil, notes); len(problems) > 0 {
		t.Fatalf("persisted types without a note:\n%s", strings.Join(problems, "\n"))
	}
}

// changedToolCall is tool/call with one added field and no version bump.
type changedToolCall struct {
	event.ToolCallPayload
	Timeout int `json:"timeout,omitempty"`
}

// TestInventoryCatchesUnbumpedChange is the DH-T-STORE-10 scenario: a persisted
// type that gains a field without a version bump fails the check, naming the type.
func TestInventoryCatchesUnbumpedChange(t *testing.T) {
	was, notes := committed(t)
	cur, err := store.Inventory(event.Default())
	if err != nil {
		t.Fatal(err)
	}

	same, err := store.EntryOf[event.ToolCallPayload]("tool/call", 1)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := store.EntryOf[changedToolCall]("tool/call", 1)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Fingerprint == same.Fingerprint {
		t.Fatal("adding a field left the fingerprint unchanged")
	}
	for i, e := range cur {
		if e.Type == "tool/call" {
			if e != same {
				t.Fatalf("EntryOf does not reproduce the registry's entry: %v vs %v", same, e)
			}
			cur[i] = changed
		}
	}

	problems := store.CheckInventory(cur, was, notes)
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "tool/call: shape changed at version 1") {
		t.Fatalf("problems = %q, want one naming tool/call", problems)
	}

	// A bump without a note is still refused; the note clears it.
	bumped := changed
	bumped.Version = 2
	for i, e := range cur {
		if e.Type == "tool/call" {
			cur[i] = bumped
		}
	}
	if p := store.CheckInventory(cur, was, notes); len(p) != 1 || !strings.HasPrefix(p[0], "tool/call: version 1 -> 2") {
		t.Fatalf("bump without note: %q", p)
	}
	withNote := append(notes, store.InventoryNote{Type: "tool/call", From: 1, To: 2, Fingerprint: bumped.Fingerprint, Reason: "timeout added"})
	if p := store.CheckInventory(cur, was, withNote); len(p) != 0 {
		t.Fatalf("bump with note: %q", p)
	}

	// A same-version note clears it only when it says why it is compatible.
	cur2 := slicesReplace(cur, changed)
	vague := append(notes, store.InventoryNote{Type: "tool/call", From: 1, To: 1, Fingerprint: changed.Fingerprint, Reason: "timeout added"})
	if p := store.CheckInventory(cur2, was, vague); len(p) != 1 {
		t.Fatalf("vague same-version note accepted: %q", p)
	}
	ok := append(notes, store.InventoryNote{Type: "tool/call", From: 1, To: 1, Fingerprint: changed.Fingerprint, Reason: "compatible: optional field an older binary ignores"})
	if p := store.CheckInventory(cur2, was, ok); len(p) != 0 {
		t.Fatalf("compatible note refused: %q", p)
	}
}

func slicesReplace(cur []store.InventoryEntry, e store.InventoryEntry) []store.InventoryEntry {
	out := append([]store.InventoryEntry(nil), cur...)
	for i := range out {
		if out[i].Type == e.Type {
			out[i] = e
		}
	}
	return out
}

func TestInventoryRefusesRemovalAndDowngrade(t *testing.T) {
	was, notes := committed(t)
	cur, err := store.Inventory(event.Default())
	if err != nil {
		t.Fatal(err)
	}
	var without []store.InventoryEntry
	for _, e := range cur {
		switch e.Type {
		case "turn/start":
		case "tool/result":
			e.Version = 1
			without = append(without, e)
		default:
			without = append(without, e)
		}
	}
	p := store.CheckInventory(without, was, notes)
	if len(p) != 2 || !strings.HasPrefix(p[0], "tool/result: version went down") || !strings.HasPrefix(p[1], "turn/start: persisted type is no longer declared") {
		t.Fatalf("problems = %q", p)
	}
}

// TestPersistedRowsDecode keeps one stored row per inventoried type and version,
// including retired versions, and requires each to decode with the current
// registry: a version bump must ship its upgrade.
func TestPersistedRowsDecode(t *testing.T) {
	reg := event.Default()
	type tv struct {
		typ string
		v   int
	}
	have := map[tv]bool{}
	sc := bufio.NewScanner(strings.NewReader(readTestdata(t, "rows.jsonl")))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var rec event.Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("row: %v", err)
		}
		if _, err := reg.DecodeAny(rec); err != nil {
			t.Errorf("%s v%d at seq %d does not decode: %v", rec.Type, rec.Version, rec.Seq, err)
		}
		have[tv{rec.Type, rec.Version}] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	for _, info := range reg.Types() {
		for v := 1; v <= info.Version; v++ {
			if !have[tv{info.Name, v}] {
				t.Errorf("testdata/rows.jsonl has no %s v%d row", info.Name, v)
			}
		}
	}

	old := event.Record{}
	sc = bufio.NewScanner(strings.NewReader(readTestdata(t, "rows.jsonl")))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var rec event.Record
		_ = json.Unmarshal(sc.Bytes(), &rec)
		if rec.Type == "tool/result" && rec.Version == 1 {
			old = rec
		}
	}
	got, err := event.ToolResult.Decode(old)
	if err != nil || got.CallID != "c1" {
		t.Fatalf("tool/result v1 upgrade: %+v, %v", got, err)
	}
}
