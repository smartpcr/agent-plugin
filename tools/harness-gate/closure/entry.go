package closure

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// Entry is the entry condition of a story whose first scenario run and first stage
// closure wait for an earlier story to close (M2-DEC-1 as amended by M1 gap-closure
// step 12.1.9; implementation-plan Stage 1.2): every stage record of the prior story
// reads closed, the prior story's closure record lists each of its deliverables as
// done or built with a reason, and built-through has reached the prior story's last
// stage.
type Entry struct {
	Story        string   // the story that is entered
	Prior        string   // the story that must be closed first
	PriorStages  []string // the prior story's stages, each with an evidence record
	BuiltThrough string   // the least built-through of the acceptance index
	Deliverables []string // every deliverable the prior closure record must list
}

// Entries are the entry conditions the gate knows, by the story entered.
var Entries = map[string]Entry{
	"harness:DSH-M2": {
		Story:        "harness:DSH-M2",
		Prior:        "harness:DSH-M1",
		PriorStages:  []string{"DH-S-1", "DH-S-2", "DH-S-3", "DH-S-4", "DH-S-5", "DH-S-6", "DH-S-7", "DH-S-8", "DH-S-9", "DH-S-10", "DH-S-11"},
		BuiltThrough: "DH-S-11",
		Deliverables: m1Deliverables,
	},
}

// m1Deliverables are the 99 deliverables of impl.md steps 1.1 to 11.7, in step order:
// each step's first DH-D link, whether it builds the deliverable or a part of it.
var m1Deliverables = []string{
	"DH-D-TOOLCHAIN-1", "DH-D-TOOLCHAIN-2", "DH-D-TOOLCHAIN-3", "DH-D-TOOLCHAIN-4", "DH-D-TOOLCHAIN-5",
	"DH-D-CORDIS-1", "DH-D-CORDIS-2", "DH-D-CORDIS-3", "DH-D-CORDIS-4", "DH-D-CORDIS-7", "DH-D-CORDIS-5",
	"DH-D-CORDIS-8", "DH-D-CORDIS-9",
	"DH-D-LOADER-1", "DH-D-LOADER-3", "DH-D-LOADER-4", "DH-D-LOADER-6", "DH-D-LOADER-7", "DH-D-LOADER-10",
	"DH-D-LOADER-8", "DH-D-LOADER-11", "DH-D-LOADER-13",
	"DH-D-SDK-1", "DH-D-UTIL-1", "DH-D-SDK-2", "DH-D-UTIL-4", "DH-D-LLM-11", "DH-D-FS-1", "DH-D-FS-2",
	"DH-D-FS-3", "DH-D-UTIL-3", "DH-D-LOADER-2", "DH-D-BOOT-4", "DH-D-LOADER-5", "DH-D-LOADER-12",
	"DH-D-BOOT-1", "DH-D-SESSION-1", "DH-D-SESSION-8", "DH-D-STORE-1", "DH-D-STORE-5", "DH-D-SESSION-2",
	"DH-D-SESSION-3", "DH-D-SESSION-4", "DH-D-SESSION-5", "DH-D-LLM-1", "DH-D-AGENT-1", "DH-D-AGENT-4",
	"DH-D-HUMAN-1", "DH-D-VIEW-1",
	"DH-D-UTIL-5", "DH-D-EXEC-1", "DH-D-EXEC-2", "DH-D-BOOT-2", "DH-D-STORAGE-2", "DH-D-HUMAN-7",
	"DH-D-BOOT-3", "DH-D-CLI-1", "DH-D-CLI-3", "DH-D-HOST-7", "DH-D-BOOT-5", "DH-D-BOOT-6", "DH-D-BOOT-7",
	"DH-D-STORAGE-8", "DH-D-HOST-4", "DH-D-UTIL-2", "DH-D-UTIL-6",
	"DH-D-STORE-2", "DH-D-STORE-3", "DH-D-STORE-4", "DH-D-STORE-7", "DH-D-STORE-8", "DH-D-STORE-6",
	"DH-D-STORAGE-1", "DH-D-VIEW-2", "DH-D-VIEW-3", "DH-D-STORAGE-7", "DH-D-VIEW-5", "DH-D-STORAGE-5",
	"DH-D-STORAGE-3",
	"DH-D-LLM-2", "DH-D-LLM-3", "DH-D-LLM-6",
	"DH-D-TOOLS-1", "DH-D-TOOLS-5", "DH-D-HUMAN-2", "DH-D-TOOLS-2", "DH-D-TOOLS-3", "DH-D-STORAGE-6",
	"DH-D-AGENT-2", "DH-D-HUMAN-5", "DH-D-HUMAN-6", "DH-D-HUMAN-3",
	"DH-D-AGENT-7", "DH-D-AGENT-10", "DH-D-AGENT-3", "DH-D-AGENT-5", "DH-D-AGENT-6", "DH-D-SUBAGENT-1",
	"DH-D-CONTEXT-1",
}

// EntryStates are the states of a prior deliverable in its closure record: done, or
// built and not done, which must give its reason (the scenario and stage that finish
// it, or the item that holds it open).
var EntryStates = []string{"done", "built"}

// EntryInputs is what Entry.Check reads: the prior story's stage records (a stage
// absent from Records has none), its closure record (nil when there is none) and the
// acceptance index's built-through.
type EntryInputs struct {
	Records      map[string]*Record
	Closure      *Record
	BuiltThrough string
}

// Check returns every unmet entry condition, in a fixed order: each stage record that
// does not read closed, then the closure record, then built-through. No condition
// means the story may be entered.
func (e Entry) Check(in EntryInputs) []string {
	var out []string
	for _, stage := range e.PriorStages {
		rel := RecordPath(e.Prior, stage)
		rec := in.Records[stage]
		switch {
		case rec == nil:
			out = append(out, fmt.Sprintf("%s: no evidence record %s", stage, rel))
		case rec.Status == StatusMissing:
			out = append(out, fmt.Sprintf("%s: %s has no %q line; want closed", stage, rel, statusPrefix))
		case rec.Status != StatusClosed:
			out = append(out, fmt.Sprintf("%s: %s reads %q; want closed", stage, rel, statusPrefix+" "+rec.StatusValue))
		}
	}
	out = append(out, e.closureProblems(in.Closure)...)
	want, _ := acceptance.StageNumber(e.BuiltThrough)
	if got, ok := acceptance.StageNumber(in.BuiltThrough); !ok || got < want {
		out = append(out, fmt.Sprintf("built-through in %s reads %s; want %s or later", acceptance.IndexPath, in.BuiltThrough, e.BuiltThrough))
	}
	return out
}

// closureProblems checks the prior closure record: a "## Deliverables" table with
// Deliverable, State and Reason, one row per prior deliverable, each done or built,
// and every built row with its reason.
func (e Entry) closureProblems(rec *Record) []string {
	rel := ClosureRecordPath(e.Prior)
	if rec == nil {
		return []string{fmt.Sprintf("no %s closure record %s: it must list every %s deliverable as done or built with a reason", e.Prior, rel, e.Prior)}
	}
	var out []string
	add := func(format string, args ...any) { out = append(out, rel+": "+fmt.Sprintf(format, args...)) }
	d := rec.Extra[DeliverablesHeading]
	if d == nil || !d.Present {
		add("no %q section", DeliverablesHeading)
		return out
	}
	for _, c := range []string{"deliverable", "state", "reason"} {
		if !d.Has(c) {
			add("%s has no %q column", DeliverablesHeading, c)
		}
	}
	seen := map[string]bool{}
	for _, r := range d.Rows {
		id := r.Get("deliverable")
		switch {
		case !deliverablePattern.MatchString(id):
			add("line %d: deliverable %q is not a deliverable ID (DH-D-<AREA>-<n>)", r.Line, id)
			continue
		case seen[id]:
			add("%s is listed twice", id)
		case !slices.Contains(e.Deliverables, id):
			add("%s is not a deliverable of %s", id, e.Prior)
		}
		seen[id] = true
		switch state := r.Get("state"); {
		case !slices.Contains(EntryStates, state):
			add("%s: state %q, want done or built (built, not done, with a reason)", id, state)
		case state == "built" && r.Get("reason") == "":
			add("%s reads built but gives no reason it is not done", id)
		}
	}
	for _, id := range e.Deliverables {
		if !seen[id] {
			add("deliverable %s has no row", id)
		}
	}
	return out
}

// ReadEntryInputs reads e's inputs under root. A missing record or closure record is
// left absent, for Check to name; any other read or parse error is returned.
func ReadEntryInputs(root string, e Entry) (EntryInputs, error) {
	in := EntryInputs{Records: map[string]*Record{}}
	for _, stage := range e.PriorStages {
		rec, err := readDocument(root, RecordPath(e.Prior, stage))
		if err != nil {
			return in, err
		}
		if rec != nil {
			in.Records[stage] = rec
		}
	}
	rec, err := readDocument(root, ClosureRecordPath(e.Prior), DeliverablesHeading)
	if err != nil {
		return in, err
	}
	in.Closure = rec
	x, err := acceptance.ReadIndex(root)
	if err != nil {
		return in, err
	}
	in.BuiltThrough = x.BuiltThrough
	return in, nil
}

// readDocument parses the markdown file rel under root, or returns nil, nil when it
// does not exist.
func readDocument(root, rel string, extra ...string) (*Record, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	defer f.Close()
	return ParseDocument(rel, f, extra...)
}

// Summary is the pass line's description of what held.
func (e Entry) Summary() string {
	return fmt.Sprintf("%s .. %s read closed, %s lists all %d deliverables, built-through is at least %s",
		e.PriorStages[0], e.PriorStages[len(e.PriorStages)-1], ClosureRecordPath(e.Prior), len(e.Deliverables), e.BuiltThrough)
}
