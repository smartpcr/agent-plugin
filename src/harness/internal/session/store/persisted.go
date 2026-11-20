package store

import (
	"bufio"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// The persisted-type inventory (DH-D-STORE-5, DH-T-STORE-10): one line per
// persisted type at its current version, with the fingerprint the schema package
// derives from its shape. The committed file is testdata/persisted.golden; a test
// recomputes it and fails on any difference until the file is re-recorded with the
// golden switch and a line is added to testdata/persisted.notes.
//
// Each event type evolves by itself: a shape change that an older binary would
// misread needs a version bump and an upgrade; one it reads correctly keeps the
// version and its note says "compatible" and why.

// The pseudo-types for the shapes stored outside a payload.
const (
	InventoryEnvelope = "@envelope" // event.Record
	InventoryHeader   = "@header"   // Header
)

// InventoryEntry is one line of the inventory.
type InventoryEntry struct {
	Type        string
	Version     int
	Fingerprint string
}

func (e InventoryEntry) String() string {
	return fmt.Sprintf("%s %d %s", e.Type, e.Version, e.Fingerprint)
}

// EntryOf is the inventory entry of shape T persisted as name at version; a test
// uses it to stand a changed struct in for a declared one.
func EntryOf[T any](name string, version int) (InventoryEntry, error) {
	t, err := schema.ForType(reflect.TypeFor[T]())
	if err != nil {
		return InventoryEntry{}, err
	}
	return InventoryEntry{Type: name, Version: version, Fingerprint: t.Fingerprint()}, nil
}

// Inventory is the inventory of reg's declarations plus the envelope and header,
// sorted by type.
func Inventory(reg *event.Registry) ([]InventoryEntry, error) {
	var out []InventoryEntry
	for _, info := range reg.Types() {
		out = append(out, InventoryEntry{Type: info.Name, Version: info.Version, Fingerprint: info.Fingerprint()})
	}
	env, err := EntryOf[event.Record](InventoryEnvelope, 1)
	if err != nil {
		return nil, err
	}
	hdr, err := EntryOf[Header](InventoryHeader, 1)
	if err != nil {
		return nil, err
	}
	out = append(out, env, hdr)
	slices.SortFunc(out, func(a, b InventoryEntry) int { return strings.Compare(a.Type, b.Type) })
	return out, nil
}

const inventoryPreamble = "# Persisted-type inventory (DH-D-STORE-5): <type> <version> <fingerprint>.\n" +
	"# Re-record with -update only together with a line in persisted.notes.\n"

// RenderInventory is the committed form of entries.
func RenderInventory(entries []InventoryEntry) string {
	var b strings.Builder
	b.WriteString(inventoryPreamble)
	for _, e := range entries {
		b.WriteString(e.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// ParseInventory reads the committed form.
func ParseInventory(text string) ([]InventoryEntry, error) {
	var out []InventoryEntry
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("inventory line %d: want <type> <version> <fingerprint>", n)
		}
		v, err := strconv.Atoi(f[1])
		if err != nil {
			return nil, fmt.Errorf("inventory line %d: version: %v", n, err)
		}
		out = append(out, InventoryEntry{Type: f[0], Version: v, Fingerprint: f[2]})
	}
	return out, sc.Err()
}

// InventoryNote is one line of the notes file: a type moved from one version to
// another (equal for a compatible change), to the shape with this fingerprint,
// for this reason. Fingerprint "-" marks a historical version whose shape is no
// longer declared.
type InventoryNote struct {
	Type        string
	From, To    int
	Fingerprint string
	Reason      string
}

// ParseNotes reads the notes file: "<type> <from>-><to> <fingerprint> <reason>".
func ParseNotes(text string) ([]InventoryNote, error) {
	var out []InventoryNote
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.SplitN(line, " ", 4)
		if len(f) != 4 || strings.TrimSpace(f[3]) == "" {
			return nil, fmt.Errorf("notes line %d: want <type> <from>-><to> <fingerprint> <reason>", n)
		}
		from, to, ok := strings.Cut(f[1], "->")
		a, err1 := strconv.Atoi(from)
		b, err2 := strconv.Atoi(to)
		if !ok || err1 != nil || err2 != nil || b < a {
			return nil, fmt.Errorf("notes line %d: %q is not <from>-><to> with to >= from", n, f[1])
		}
		out = append(out, InventoryNote{Type: f[0], From: a, To: b, Fingerprint: f[2], Reason: strings.TrimSpace(f[3])})
	}
	return out, sc.Err()
}

// CheckInventory compares the current inventory with the committed one and the
// notes, and returns one problem per type that changed without its paperwork:
//   - a fingerprint differs from the committed one at the same version, and no
//     note for that version and fingerprint says "compatible";
//   - a new type or version has no note for that version and fingerprint;
//   - a version went down;
//   - a committed type is no longer declared (a persisted type cannot vanish).
//
// The notes are checked by CheckNotes too. A problem names the type first.
func CheckInventory(current, committed []InventoryEntry, notes []InventoryNote) []string {
	var problems []string
	was := map[string]InventoryEntry{}
	for _, e := range committed {
		was[e.Type] = e
	}
	problems = append(problems, CheckNotes(notes)...)
	// A same-version note counts only when it says why the change is compatible,
	// whether or not the committed inventory still shows the old fingerprint: the
	// check holds against the pre-update golden and after it was re-recorded.
	noted := func(e InventoryEntry, sameVersion bool) bool {
		for _, n := range notes {
			if n.Type != e.Type || n.To != e.Version || n.Fingerprint != e.Fingerprint {
				continue
			}
			compatible := n.From == n.To && isCompatible(n)
			if compatible || (!sameVersion && n.From < n.To) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	for _, e := range current {
		seen[e.Type] = true
		old, ok := was[e.Type]
		switch {
		case !ok:
			if !noted(e, false) {
				problems = append(problems, fmt.Sprintf("%s: new persisted type at version %d (%s) has no line in persisted.notes", e.Type, e.Version, e.Fingerprint))
			}
		case e.Version < old.Version:
			problems = append(problems, fmt.Sprintf("%s: version went down from %d to %d", e.Type, old.Version, e.Version))
		case e.Version == old.Version && e.Fingerprint != old.Fingerprint:
			if !noted(e, true) {
				problems = append(problems, fmt.Sprintf("%s: shape changed at version %d (%s -> %s) without a version bump; bump the version with an upgrade, or add a %q note saying why an older binary still reads it",
					e.Type, e.Version, old.Fingerprint, e.Fingerprint, "compatible"))
			}
		case e.Version > old.Version:
			if !noted(e, false) {
				problems = append(problems, fmt.Sprintf("%s: version %d -> %d (%s) has no line in persisted.notes", e.Type, old.Version, e.Version, e.Fingerprint))
			}
		}
	}
	for _, e := range committed {
		if !seen[e.Type] {
			problems = append(problems, fmt.Sprintf("%s: persisted type is no longer declared; stored rows of it would refuse to open", e.Type))
		}
	}
	return problems
}

func isCompatible(n InventoryNote) bool { return strings.Contains(n.Reason, "compatible") }

// CheckNotes judges the notes by themselves, so a re-recorded golden cannot hide
// an unexplained change: a same-version note must say "compatible" and why; a
// version is reached by at most one bump, so a second "<from>-><to>" line for it
// cannot launder a new shape; versions start at 1.
func CheckNotes(notes []InventoryNote) []string {
	var problems []string
	type tv struct {
		typ string
		to  int
	}
	bumps := map[tv]int{}
	for _, n := range notes {
		switch {
		case n.To < 1:
			problems = append(problems, fmt.Sprintf("%s: note %d->%d: versions start at 1", n.Type, n.From, n.To))
		case n.From == n.To && !isCompatible(n):
			problems = append(problems, fmt.Sprintf("%s: note %d->%d (%s) keeps the version but does not say %q and why", n.Type, n.From, n.To, n.Fingerprint, "compatible"))
		case n.From < n.To:
			bumps[tv{n.Type, n.To}]++
			if bumps[tv{n.Type, n.To}] == 2 {
				problems = append(problems, fmt.Sprintf("%s: version %d is reached by more than one bump note; a new shape at the same version needs a %q note", n.Type, n.To, "compatible"))
			}
		}
	}
	return problems
}
