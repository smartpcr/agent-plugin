// Package storetest is the store contract's conformance suite (DH-D-STORE-1,
// DH-T-STORE-1): every store.Store provider passes Run with its own factory.
//
//	func TestConformance(t *testing.T) {
//	    storetest.Run(t, func(t *testing.T) store.Store { return memstore.New() })
//	}
//
// The suite asserts ordering, read-your-writes, the flush barrier, the single
// writer and takeover, the refusal classes, paged reads, listing, checkpoints,
// fork, deletion and that a record is the same bytes after hand-over. It judges
// opens against event.Default(), the registry a provider uses unless told
// otherwise.
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// Factory returns a new, empty store for one case.
type Factory func(t *testing.T) store.Store

// Seeder is the fixture hook every provider's store implements for the suite: Seed
// stores a durable, unheld session holding recs byte for byte, without the
// admission rule Append applies, as rows another build wrote would be. The suite
// uses it only to prove that an open judges such rows.
type Seeder interface {
	Seed(ctx context.Context, h store.Header, recs []store.Record) error
}

// Run runs every case of the suite against stores from factory, each case on a
// fresh store.
func Run(t *testing.T, factory Factory) {
	cases := []struct {
		name string
		run  func(t *testing.T, s store.Store)
	}{
		{"CreateAndStat", testCreateAndStat},
		{"AppendOrderAndReadYourWrites", testAppendOrder},
		{"FlushBarrier", testFlushBarrier},
		{"SeqConflict", testSeqConflict},
		{"SingleWriter", testSingleWriter},
		{"ForceTakeover", testForceTakeover},
		{"PagedReads", testPagedReads},
		{"ReadSeqs", testReadSeqs},
		{"ColdPart", testColdPart},
		{"Checkpoints", testCheckpoints},
		{"List", testList},
		{"ListByPrompt", testListByPrompt},
		{"Delete", testDelete},
		{"OpenRefusals", testOpenRefusals},
		{"AppendAdmission", testAdmission},
		{"UnsignedBoundaries", testBoundaries},
		{"IgnorableKeptByteIdentical", testIgnorable},
		{"ClosedHandles", testClosed},
		{"Fork", testFork},
		{"Immutability", testImmutability},
		{"Concurrent", testConcurrent},
		{"CancelledContext", testCancelled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := factory(t)
			if s == nil {
				t.Fatal("the factory returned no store")
			}
			c.run(t, s)
		})
	}
}

var bg = context.Background()

// clock stamps records with strictly increasing millisecond times.
type clock struct {
	mu sync.Mutex
	ms int64
}

func (c *clock) next() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ms++
	return c.ms
}

var times = &clock{ms: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()}

func stamp(t *testing.T, d event.Draft, seq store.Seq) store.Record {
	t.Helper()
	r, err := d.Stamp(seq, times.next())
	if err != nil {
		t.Fatalf("Stamp(%d): %v", seq, err)
	}
	return r
}

// call is a tool/call record at seq.
func call(t *testing.T, seq store.Seq, sources ...store.Seq) store.Record {
	t.Helper()
	d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("c%d", seq), Name: "grep", Arguments: `{"q":"x"}`}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return stamp(t, d, seq)
}

// calls is n tool/call records from first.
func calls(t *testing.T, first store.Seq, n int) []store.Record {
	t.Helper()
	out := make([]store.Record, n)
	for i := range out {
		out[i] = call(t, first+store.Seq(i))
	}
	return out
}

// raw is a hand-built record, as another build might have written it.
func raw(seq store.Seq, typ string, version int, ignorable bool, payload string) store.Record {
	return store.Record{Seq: seq, Time: times.next(), Type: typ, Version: version, Ignorable: ignorable, Payload: payload}
}

func header(id string) store.Header {
	return store.Header{ID: id, CreatedAt: time.UnixMilli(times.next()).UTC(), Cwd: "/work", Preset: "default"}
}

func create(t *testing.T, s store.Store, id string, recs ...store.Record) store.Writer {
	t.Helper()
	w, err := s.Create(bg, header(id), store.CreateOptions{})
	if err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
	if len(recs) > 0 {
		appendOK(t, w, recs...)
	}
	return w
}

func appendOK(t *testing.T, w store.Writer, recs ...store.Record) {
	t.Helper()
	if err := w.Append(bg, store.Batch{First: recs[0].Seq, Records: recs}); err != nil {
		t.Fatalf("Append at %d: %v", recs[0].Seq, err)
	}
}

func closeOK(t *testing.T, c interface{ Close(context.Context) error }) {
	t.Helper()
	if err := c.Close(bg); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func readAll(t *testing.T, r store.Reader, o store.ReadOptions) []store.Record {
	t.Helper()
	recs, err := r.Read(bg, 0, 0, o)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return recs
}

func sameRecords(t *testing.T, what string, got, want []store.Record) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d records, want %d", what, len(got), len(want))
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Fatalf("%s: record %d is\n%+v\nwant\n%+v", what, i, got[i], want[i])
		}
	}
}

func wantErr(t *testing.T, what string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s = %v, want %v", what, err, target)
	}
}

func stat(t *testing.T, s store.Store, id string) store.Summary {
	t.Helper()
	sum, err := s.Stat(bg, id)
	if err != nil {
		t.Fatalf("Stat(%s): %v", id, err)
	}
	return sum
}

func testCreateAndStat(t *testing.T, s store.Store) {
	h := header("s1")
	h.Origin, h.Creator, h.DelegationDepth = "subagent", "root", 1
	w, err := s.Create(bg, h, store.CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := w.Header(); got.ID != h.ID || got.Creator != "root" || !got.CreatedAt.Equal(h.CreatedAt) {
		t.Fatalf("Header = %+v, want %+v", got, h)
	}
	sum := stat(t, s, "s1")
	if sum.NextSeq != 0 || sum.Durable != 0 || sum.Holder == nil || sum.LeaseEpoch == 0 || sum.LeaseEpoch != w.Epoch() {
		t.Fatalf("Stat = %+v, epoch %d", sum, w.Epoch())
	}
	if sum.Header.Origin != "subagent" || sum.Header.DelegationDepth != 1 || sum.Header.Preset != "default" {
		t.Fatalf("Stat header = %+v", sum.Header)
	}
	_, err = s.Create(bg, header("s1"), store.CreateOptions{})
	wantErr(t, "Create twice", err, store.ErrExists)
	_, err = s.Stat(bg, "missing")
	wantErr(t, "Stat(missing)", err, store.ErrNotFound)
	_, err = s.OpenRead(bg, "missing")
	wantErr(t, "OpenRead(missing)", err, store.ErrNotFound)
	_, err = s.OpenWrite(bg, "missing", store.OpenOptions{})
	wantErr(t, "OpenWrite(missing)", err, store.ErrNotFound)
	closeOK(t, w)
	if sum := stat(t, s, "s1"); sum.Holder != nil || sum.LeaseEpoch != w.Epoch() {
		t.Fatalf("Stat after Close = %+v", sum)
	}
}

func testAppendOrder(t *testing.T, s store.Store) {
	w := create(t, s, "s1")
	var want []store.Record
	for first := store.Seq(0); first < 9; first += 3 {
		b := calls(t, first, 3)
		appendOK(t, w, b...)
		want = append(want, b...)
		// Read-your-writes: the writer and a reader in this process see every
		// accepted record, flushed or not.
		sameRecords(t, "writer read", readAll(t, w, store.ReadOptions{}), want)
	}
	r, err := s.OpenRead(bg, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer closeOK(t, r)
	sameRecords(t, "reader read", readAll(t, r, store.ReadOptions{}), want)
	if sum := stat(t, s, "s1"); sum.NextSeq != 9 {
		t.Fatalf("NextSeq = %d, want 9", sum.NextSeq)
	}
	recs, err := r.Read(bg, 9, 10, store.ReadOptions{})
	if err != nil || len(recs) != 0 {
		t.Fatalf("Read past the end = %v, %v", recs, err)
	}
	if err := w.Append(bg, store.Batch{First: 9}); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	closeOK(t, w)
}

func testFlushBarrier(t *testing.T, s store.Store) {
	w := create(t, s, "s1", calls(t, 0, 4)...)
	before := stat(t, s, "s1")
	if before.Durable > before.NextSeq || before.NextSeq != 4 {
		t.Fatalf("before Flush: %+v", before)
	}
	if err := w.Flush(bg); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if sum := stat(t, s, "s1"); sum.Durable != 4 {
		t.Fatalf("after Flush Durable = %d, want 4", sum.Durable)
	}
	appendOK(t, w, calls(t, 4, 2)...)
	w2 := create(t, s, "s2", calls(t, 0, 3)...)
	if err := s.Flush(bg); err != nil {
		t.Fatalf("Store.Flush: %v", err)
	}
	if a, b := stat(t, s, "s1"), stat(t, s, "s2"); a.Durable != 6 || b.Durable != 3 {
		t.Fatalf("after Store.Flush: %d, %d", a.Durable, b.Durable)
	}
	appendOK(t, w2, call(t, 3))
	closeOK(t, w2) // Close flushes
	if sum := stat(t, s, "s2"); sum.Durable != 4 {
		t.Fatalf("after Close Durable = %d, want 4", sum.Durable)
	}
	closeOK(t, w)
}

func testSeqConflict(t *testing.T, s store.Store) {
	w := create(t, s, "s1", calls(t, 0, 2)...)
	defer closeOK(t, w)
	err := w.Append(bg, store.Batch{First: 3, Records: []store.Record{call(t, 3)}})
	wantErr(t, "gap", err, store.ErrSeqConflict)
	err = w.Append(bg, store.Batch{First: 1, Records: []store.Record{call(t, 1)}})
	wantErr(t, "duplicate", err, store.ErrSeqConflict)
	err = w.Append(bg, store.Batch{First: 2, Records: []store.Record{call(t, 2), call(t, 4)}})
	wantErr(t, "hole inside a batch", err, store.ErrSeqConflict)
	if sum := stat(t, s, "s1"); sum.NextSeq != 2 {
		t.Fatalf("a refused batch wrote: NextSeq = %d", sum.NextSeq)
	}
	appendOK(t, w, call(t, 2))
}

func testSingleWriter(t *testing.T, s store.Store) {
	w := create(t, s, "s1", calls(t, 0, 1)...)
	_, err := s.OpenWrite(bg, "s1", store.OpenOptions{})
	wantErr(t, "second writer", err, store.ErrOwned)
	var owned *store.OwnedError
	if !errors.As(err, &owned) || owned.ID != "s1" || owned.Holder.Instance == "" {
		t.Fatalf("second writer error = %#v, want *OwnedError naming the holder", err)
	}
	if err := s.Delete(bg, "s1", store.DeleteOptions{}); !errors.Is(err, store.ErrOwned) {
		t.Fatalf("Delete while held = %v", err)
	}
	closeOK(t, w)
	w2, err := s.OpenWrite(bg, "s1", store.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenWrite after Close: %v", err)
	}
	if w2.Epoch() <= w.Epoch() {
		t.Fatalf("epoch %d after %d did not rise", w2.Epoch(), w.Epoch())
	}
	appendOK(t, w2, call(t, 1))
	closeOK(t, w2)
}

func testForceTakeover(t *testing.T, s store.Store) {
	w1 := create(t, s, "s1", calls(t, 0, 2)...)
	if err := w1.Flush(bg); err != nil {
		t.Fatal(err)
	}
	appendOK(t, w1, call(t, 2)) // accepted, maybe not durable
	w2, err := s.OpenWrite(bg, "s1", store.OpenOptions{Takeover: store.Force})
	if err != nil {
		t.Fatalf("forced takeover: %v", err)
	}
	if w2.Epoch() <= w1.Epoch() {
		t.Fatalf("epoch %d after %d did not rise", w2.Epoch(), w1.Epoch())
	}
	sum := stat(t, s, "s1")
	if sum.NextSeq < 2 || sum.NextSeq > 3 || sum.LeaseEpoch != w2.Epoch() {
		t.Fatalf("after takeover: %+v", sum)
	}
	// The displaced writer learns it at its next append or flush and writes
	// nothing.
	err = w1.Append(bg, store.Batch{First: sum.NextSeq, Records: []store.Record{call(t, sum.NextSeq)}})
	wantErr(t, "stale append", err, store.ErrLeaseLost)
	wantErr(t, "stale flush", w1.Flush(bg), store.ErrLeaseLost)
	if got := stat(t, s, "s1").NextSeq; got != sum.NextSeq {
		t.Fatalf("a stale writer wrote: NextSeq %d -> %d", sum.NextSeq, got)
	}
	appendOK(t, w2, call(t, sum.NextSeq))
	if err := w1.Close(bg); err != nil {
		t.Fatalf("closing a displaced writer: %v", err)
	}
	if h := stat(t, s, "s1").Holder; h == nil {
		t.Fatal("closing the displaced writer released the new holder's lease")
	}
	closeOK(t, w2)
}

func testPagedReads(t *testing.T, s store.Store) {
	all := calls(t, 0, 10)
	w := create(t, s, "s1", all...)
	defer closeOK(t, w)
	var got []store.Record
	for from := store.Seq(0); ; {
		page, err := w.Read(bg, from, 3, store.ReadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > 3 {
			t.Fatalf("page of %d records, limit 3", len(page))
		}
		got = append(got, page...)
		from = page[len(page)-1].Seq + 1
	}
	sameRecords(t, "paged", got, all)
	mid, err := w.Read(bg, 4, 2, store.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sameRecords(t, "window", mid, all[4:6])
	if _, err := w.Read(bg, 0, -1, store.ReadOptions{}); err == nil {
		t.Fatal("a negative limit was accepted")
	}
}

func testReadSeqs(t *testing.T, s store.Store) {
	all := calls(t, 0, 6)
	w := create(t, s, "s1", all...)
	defer closeOK(t, w)
	got, err := w.ReadSeqs(bg, []store.Seq{5, 1, 3}, store.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sameRecords(t, "ReadSeqs", got, []store.Record{all[5], all[1], all[3]})
	_, err = w.ReadSeqs(bg, []store.Seq{2, 6}, store.ReadOptions{})
	wantErr(t, "ReadSeqs past the end", err, store.ErrInvalidBoundary)
}

func assistant(t *testing.T, seq store.Seq) store.Record {
	t.Helper()
	m := mustMessage(t)
	d, err := event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
		Message: m, Stream: json.RawMessage(`[{"delta":"hello"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	return stamp(t, d, seq)
}

func mustMessage(t *testing.T) vocab.Message {
	t.Helper()
	m, err := vocab.NewMessage(vocab.RoleAssistant, vocab.TextBlock("hello"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func testColdPart(t *testing.T, s store.Store) {
	rec := assistant(t, 0)
	if rec.Cold == "" {
		t.Fatal("the assistant record has no cold part")
	}
	w := create(t, s, "s1", rec)
	defer closeOK(t, w)
	hot := readAll(t, w, store.ReadOptions{})
	if len(hot) != 1 || hot[0].Cold != "" || hot[0].Payload != rec.Payload {
		t.Fatalf("hot read = %+v", hot)
	}
	full := readAll(t, w, store.ReadOptions{WithAux: true})
	sameRecords(t, "WithAux", full, []store.Record{rec})
	seqs, err := w.ReadSeqs(bg, []store.Seq{0}, store.ReadOptions{WithAux: true})
	if err != nil {
		t.Fatal(err)
	}
	sameRecords(t, "ReadSeqs WithAux", seqs, []store.Record{rec})
}

func testCheckpoints(t *testing.T, s store.Store) {
	w := create(t, s, "s1")
	defer closeOK(t, w)
	b := store.Batch{First: 0, Records: calls(t, 0, 3), Checkpoints: []store.Checkpoint{
		{Key: "core/surface", StateVersion: 1, Seq: 2, State: `{"n":1}`},
		{Key: "core/tools", StateVersion: 1, Seq: 1, State: `{}`},
	}}
	if err := w.Append(bg, b); err != nil {
		t.Fatal(err)
	}
	b = store.Batch{First: 3, Records: calls(t, 3, 2), Checkpoints: []store.Checkpoint{
		{Key: "core/surface", StateVersion: 1, Seq: 4, State: `{"n":2}`},
		{Key: "core/surface", StateVersion: 2, Seq: 4, State: `{"v2":true}`},
	}}
	if err := w.Append(bg, b); err != nil {
		t.Fatal(err)
	}
	got, err := w.Checkpoints(bg)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.Checkpoint{
		{Key: "core/surface", StateVersion: 1, Seq: 4, State: `{"n":2}`},
		{Key: "core/surface", StateVersion: 2, Seq: 4, State: `{"v2":true}`},
		{Key: "core/tools", StateVersion: 1, Seq: 1, State: `{}`},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Checkpoints = %+v, want %+v", got, want)
	}
	err = w.Append(bg, store.Batch{First: 5, Records: calls(t, 5, 1), Checkpoints: []store.Checkpoint{{Key: "core/x", Seq: 9}}})
	wantErr(t, "checkpoint past the log", err, store.ErrInvalidBatch)
	if n := stat(t, s, "s1").NextSeq; n != 5 {
		t.Fatalf("a refused batch wrote its records: NextSeq %d", n)
	}
}

func testList(t *testing.T, s store.Store) {
	ids := []string{"a", "b", "c", "d", "e"}
	for i, id := range ids {
		h := header(id)
		if i%2 == 0 {
			h.Cwd = "/other"
		}
		if i == 1 {
			h.Creator = "boss"
		}
		w, err := s.Create(bg, h, store.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		appendOK(t, w, call(t, 0))
		closeOK(t, w)
	}
	var seen []string
	q := store.Query{Limit: 2}
	var prev *store.Summary
	for pages := 0; ; pages++ {
		if pages > len(ids) {
			t.Fatal("List does not terminate")
		}
		p, err := s.List(bg, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Summaries) > 2 {
			t.Fatalf("page of %d, limit 2", len(p.Summaries))
		}
		for i := range p.Summaries {
			sum := p.Summaries[i]
			if prev != nil && sum.LastEventAt.After(prev.LastEventAt) {
				t.Fatalf("List is not most recent first: %s after %s", sum.Header.ID, prev.Header.ID)
			}
			prev = &sum
			seen = append(seen, sum.Header.ID)
		}
		if p.Next == "" {
			break
		}
		q.Cursor = p.Next
	}
	if want := []string{"e", "d", "c", "b", "a"}; !slices.Equal(seen, want) {
		t.Fatalf("listed %v, want %v", seen, want)
	}
	p, err := s.List(bg, store.Query{Cwd: "/other"})
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryIDs(p); !slices.Equal(got, []string{"e", "c", "a"}) {
		t.Fatalf("List(cwd) = %v", got)
	}
	p, err = s.List(bg, store.Query{Creator: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryIDs(p); !slices.Equal(got, []string{"b"}) || p.Next != "" {
		t.Fatalf("List(creator) = %v next %q", got, p.Next)
	}
	if _, err := s.List(bg, store.Query{Cursor: "not a cursor"}); err == nil {
		t.Fatal("a foreign cursor was accepted")
	}
}

func summaryIDs(p store.Page) []string {
	var out []string
	for _, s := range p.Summaries {
		out = append(out, s.Header.ID)
	}
	return out
}

// prompt is a user/message record at seq.
func prompt(t *testing.T, seq store.Seq) store.Record {
	t.Helper()
	m, err := vocab.NewMessage(vocab.RoleUser, vocab.TextBlock("hi"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: m})
	if err != nil {
		t.Fatal(err)
	}
	return stamp(t, d, seq)
}

// testListByPrompt pages store.OrderPrompt while prompts are appended between
// pages: the order is by last prompt as of the first page, not by the last
// event, and no session is skipped or repeated.
func testListByPrompt(t *testing.T, s store.Store) {
	ws := map[string]store.Writer{}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		ws[id] = create(t, s, id, prompt(t, 0))
	}
	bPrompt := stat(t, s, "b").LastPromptAt
	appendOK(t, ws["b"], assistant(t, 1)) // b's last event is the newest; its prompt is not
	create(t, s, "f")                     // blank: keyed by its creation
	if sum := stat(t, s, "b"); !sum.LastPromptAt.Equal(bPrompt) || bPrompt.IsZero() || !sum.LastEventAt.After(bPrompt) {
		t.Fatalf("b: LastPromptAt %v (was %v), LastEventAt %v", sum.LastPromptAt, bPrompt, sum.LastEventAt)
	}
	if !stat(t, s, "f").LastPromptAt.IsZero() {
		t.Fatal("a blank session has a LastPromptAt")
	}
	cut := time.UnixMilli(times.next())
	q := store.Query{Order: store.OrderPrompt, AsOf: cut, Limit: 2}
	var seen []string
	for pages := 0; ; pages++ {
		if pages > 6 {
			t.Fatal("List does not terminate")
		}
		p, err := s.List(bg, q)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, summaryIDs(p)...)
		if pages == 0 {
			// After the first page: a (not yet listed) and e (listed) are
			// prompted again and g is created. Without the cut a would move
			// ahead of the cursor and be skipped.
			appendOK(t, ws["a"], prompt(t, 1))
			appendOK(t, ws["e"], prompt(t, 1))
			create(t, s, "g", prompt(t, 0))
		}
		if p.Next == "" {
			break
		}
		q.Cursor = p.Next
	}
	if want := []string{"f", "e", "d", "c", "b", "a"}; !slices.Equal(seen, want) {
		t.Fatalf("listed as of the cut %v, want %v", seen, want)
	}
	p, err := s.List(bg, store.Query{Order: store.OrderPrompt, AsOf: time.UnixMilli(times.next())})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := summaryIDs(p), []string{"g", "e", "a", "f", "d", "c", "b"}; !slices.Equal(got, want) {
		t.Fatalf("listed as of now %v, want %v", got, want)
	}
	recent, err := s.List(bg, store.Query{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(bg, store.Query{Order: store.OrderPrompt, Cursor: recent.Next}); !errors.Is(err, store.ErrInvalidBoundary) {
		t.Fatalf("a recent-order cursor in prompt order = %v", err)
	}
}

func testDelete(t *testing.T, s store.Store) {
	parent := create(t, s, "p", calls(t, 0, 4)...)
	child, err := s.Create(bg, header("c"), store.CreateOptions{ForkOf: &store.ForkOf{ID: "p", Through: 1}})
	if err != nil {
		t.Fatal(err)
	}
	closeOK(t, child)
	closeOK(t, parent)
	r, err := s.OpenRead(bg, "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(bg, "p", store.DeleteOptions{}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = s.Stat(bg, "p")
	wantErr(t, "Stat after Delete", err, store.ErrNotFound)
	_, err = s.OpenRead(bg, "p")
	wantErr(t, "OpenRead after Delete", err, store.ErrNotFound)
	_, err = r.Read(bg, 0, 0, store.ReadOptions{})
	wantErr(t, "Read through an old reader", err, store.ErrDeleted)
	wantErr(t, "Delete twice", s.Delete(bg, "p", store.DeleteOptions{}), store.ErrNotFound)
	if p, err := s.List(bg, store.Query{}); err != nil || slices.Contains(summaryIDs(p), "p") {
		t.Fatalf("List after Delete = %v, %v", summaryIDs(p), err)
	}
	// The fork survives its parent.
	cr, err := s.OpenRead(bg, "c")
	if err != nil {
		t.Fatalf("OpenRead(child): %v", err)
	}
	if n := len(readAll(t, cr, store.ReadOptions{})); n != 2 {
		t.Fatalf("child has %d records after the parent's deletion, want 2", n)
	}
	closeOK(t, cr)
	// The id can be used again, and the old reader still sees a deleted session.
	create(t, s, "p", calls(t, 0, 1)...)
	_, err = r.Read(bg, 0, 0, store.ReadOptions{})
	wantErr(t, "old reader after re-create", err, store.ErrDeleted)
}

// seed stores recs through the provider's Seeder, as another build wrote them.
func seed(t *testing.T, s store.Store, id string, recs ...store.Record) {
	t.Helper()
	sd, ok := s.(Seeder)
	if !ok {
		t.Fatalf("%T does not implement storetest.Seeder", s)
	}
	if err := sd.Seed(bg, header(id), recs); err != nil {
		t.Fatalf("Seed(%s): %v", id, err)
	}
}

func testOpenRefusals(t *testing.T, s store.Store) {
	// An undeclared required type: refused by name and first position.
	seed(t, s, "unknown", call(t, 0), raw(1, "future/thing", 1, false, `{}`), raw(2, "future/thing", 1, false, `{}`))
	_, err := s.OpenRead(bg, "unknown")
	var unknown *event.UnknownTypeError
	if !errors.As(err, &unknown) || unknown.Type != "future/thing" || unknown.Seq != 1 {
		t.Fatalf("OpenRead(unknown) = %v, want *UnknownTypeError for future/thing at 1", err)
	}
	_, err = s.OpenWrite(bg, "unknown", store.OpenOptions{})
	wantErr(t, "OpenWrite(unknown)", err, event.ErrUnknownType)

	// A newer payload version: refused with the direction "upgrade".
	newer := raw(0, "tool/result", event.ToolResult.Version()+1, false, `{"call_id":"c","message":{}}`)
	newer.Op = event.OpAppend
	seed(t, s, "newer", newer)
	_, err = s.OpenRead(bg, "newer")
	var np *event.NewerPayloadError
	if !errors.As(err, &np) || np.Type != "tool/result" || np.Version != event.ToolResult.Version()+1 || np.Known != event.ToolResult.Version() {
		t.Fatalf("OpenRead(newer) = %v, want *NewerPayloadError", err)
	}
	_, err = s.OpenWrite(bg, "newer", store.OpenOptions{Takeover: store.Force})
	wantErr(t, "OpenWrite(newer)", err, event.ErrNewerPayload)
	if sum := stat(t, s, "newer"); sum.Holder != nil {
		t.Fatal("a refused open took the lease")
	}

	// Bytes of a known type and version that do not decode.
	for name, rec := range map[string]store.Record{
		"not JSON":       raw(1, "tool/call", 1, false, `{"call_id":`),
		"not an object":  raw(1, "tool/call", 1, false, `null`),
		"wrong shape":    raw(1, "tool/call", 1, false, `{"call_id":5}`),
		"bad envelope":   func() store.Record { r := raw(1, "tool/call", 1, false, `{}`); r.Op = event.OpAppend; return r }(),
		"cold not JSON":  func() store.Record { r := assistant(t, 1); r.Cold = `[{`; return r }(),
		"null with cold": func() store.Record { r := assistant(t, 1); r.Payload = `null`; return r }(),
	} {
		id := "malformed " + name
		seed(t, s, id, call(t, 0), rec)
		_, err := s.OpenRead(bg, id)
		wantErr(t, "OpenRead("+id+")", err, store.ErrInvalidBatch)
		_, err = s.OpenWrite(bg, id, store.OpenOptions{})
		wantErr(t, "OpenWrite("+id+")", err, store.ErrInvalidBatch)
	}
}

// testAdmission: Append applies the structural admission rule to every record and
// refuses a batch atomically.
func testAdmission(t *testing.T, s store.Store) {
	w := create(t, s, "s1", call(t, 0))
	defer closeOK(t, w)
	logOp := func(r store.Record) store.Record { r.Op = event.OpAppend; return r }
	for _, tc := range []struct {
		name string
		rec  store.Record
		want error
	}{
		{"undeclared required type", raw(2, "future/thing", 1, false, `{}`), event.ErrUnknownType},
		{"newer required version", raw(2, "tool/call", 2, false, `{}`), event.ErrNewerPayload},
		{"payload not JSON", raw(2, "tool/call", 1, false, `{"call_id":`), store.ErrInvalidBatch},
		{"payload not an object", raw(2, "tool/call", 1, false, `[1]`), store.ErrInvalidBatch},
		{"payload of the wrong shape", raw(2, "tool/call", 1, false, `{"name":["x"]}`), store.ErrInvalidBatch},
		{"version 0 of a declared type", raw(2, "tool/call", 0, false, `{}`), store.ErrInvalidBatch},
		{"log-only type with an operation", logOp(raw(2, "tool/call", 1, false, `{}`)), store.ErrInvalidBatch},
		{"source not earlier", func() store.Record { r := call(t, 2); r.Sources = []store.Seq{2}; return r }(), store.ErrInvalidBatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			good := call(t, 1)
			bad := tc.rec
			bad.Time = good.Time
			err := w.Append(bg, store.Batch{First: 1, Records: []store.Record{good, bad}})
			wantErr(t, "Append", err, tc.want)
			if sum := stat(t, s, "s1"); sum.NextSeq != 1 {
				t.Fatalf("a refused batch left NextSeq %d; the batch must be all or nothing", sum.NextSeq)
			}
		})
	}
	appendOK(t, w, call(t, 1))
}

// testBoundaries: positions are unsigned; the largest values are refused or read
// as past the end, never wrapped.
func testBoundaries(t *testing.T, s store.Store) {
	const top = store.Seq(^uint64(0))
	all := calls(t, 0, 3)
	w := create(t, s, "s1", all...)
	defer closeOK(t, w)
	for _, from := range []store.Seq{3, top, top / 2, 1 << 63} {
		got, err := w.Read(bg, from, 0, store.ReadOptions{})
		if err != nil || len(got) != 0 {
			t.Fatalf("Read(%d) = %d records, %v; want none", from, len(got), err)
		}
	}
	got, err := w.Read(bg, 1, int(^uint(0)>>1), store.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sameRecords(t, "Read with the largest limit", got, all[1:])
	for _, q := range []store.Seq{top, 1 << 63} {
		_, err = w.ReadSeqs(bg, []store.Seq{0, q}, store.ReadOptions{})
		wantErr(t, fmt.Sprintf("ReadSeqs(%d)", q), err, store.ErrInvalidBoundary)
	}
	_, err = s.Create(bg, header("fork"), store.CreateOptions{ForkOf: &store.ForkOf{ID: "s1", Through: top}})
	wantErr(t, "fork through the largest seq", err, store.ErrInvalidBoundary)
	wantErr(t, "Append at the largest seq", w.Append(bg, store.Batch{First: top, Records: []store.Record{call(t, top)}}), store.ErrSeqConflict)
	wantErr(t, "checkpoint at the largest seq", w.Append(bg, store.Batch{First: 3, Records: calls(t, 3, 1),
		Checkpoints: []store.Checkpoint{{Key: "core/x", Seq: top, State: "{}"}}}), store.ErrInvalidBatch)
	if sum := stat(t, s, "s1"); sum.NextSeq != 3 {
		t.Fatalf("NextSeq = %d after refusals", sum.NextSeq)
	}
}

func testIgnorable(t *testing.T, s store.Store) {
	plugin, err := event.PluginDraft("acme", "cursor", json.RawMessage(`{"z":1,"a":[1,2]}`))
	if err != nil {
		t.Fatal(err)
	}
	recs := []store.Record{
		call(t, 0),
		raw(1, "future/note", 7, true, `{"b":2,  "a":"\u00e9"}`),
		stamp(t, plugin, 2),
		raw(3, "tool/call", 9, true, `{"from":"the future"}`),
	}
	w := create(t, s, "s1", recs...)
	closeOK(t, w)
	r, err := s.OpenRead(bg, "s1")
	if err != nil {
		t.Fatalf("OpenRead with ignorable records: %v", err)
	}
	sameRecords(t, "ignorable read", readAll(t, r, store.ReadOptions{WithAux: true}), recs)
	closeOK(t, r)
	c, err := s.Create(bg, header("fork"), store.CreateOptions{ForkOf: &store.ForkOf{ID: "s1", Through: 3}})
	if err != nil {
		t.Fatal(err)
	}
	sameRecords(t, "ignorable fork", readAll(t, c, store.ReadOptions{WithAux: true}), recs)
	closeOK(t, c)
	w, err = s.OpenWrite(bg, "s1", store.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenWrite with ignorable records: %v", err)
	}
	closeOK(t, w)
}

func testClosed(t *testing.T, s store.Store) {
	w := create(t, s, "s1", calls(t, 0, 1)...)
	closeOK(t, w)
	closeOK(t, w) // idempotent
	wantErr(t, "Append after Close", w.Append(bg, store.Batch{First: 1, Records: calls(t, 1, 1)}), store.ErrClosed)
	wantErr(t, "Flush after Close", w.Flush(bg), store.ErrClosed)
	r, err := s.OpenRead(bg, "s1")
	if err != nil {
		t.Fatal(err)
	}
	closeOK(t, r)
	closeOK(t, r)
	_, err = r.Read(bg, 0, 0, store.ReadOptions{})
	wantErr(t, "Read after Close", err, store.ErrClosed)
	_, err = r.Checkpoints(bg)
	wantErr(t, "Checkpoints after Close", err, store.ErrClosed)
}

func testFork(t *testing.T, s store.Store) {
	all := calls(t, 0, 5)
	p := create(t, s, "p", all...)
	defer closeOK(t, p)
	h := header("c")
	h.Origin, h.Creator = "subagent", "p"
	c, err := s.Create(bg, h, store.CreateOptions{ForkOf: &store.ForkOf{ID: "p", Through: 2}})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	ch := c.Header()
	if ch.ForkSource != "p" || ch.ForkThrough != 2 || ch.InheritedCount != 3 || ch.Creator != "p" {
		t.Fatalf("fork header = %+v", ch)
	}
	sameRecords(t, "fork prefix", readAll(t, c, store.ReadOptions{}), all[:3])
	appendOK(t, c, call(t, 3))
	sameRecords(t, "parent after child append", readAll(t, p, store.ReadOptions{}), all)
	if sum := stat(t, s, "c"); sum.NextSeq != 4 || sum.Header.ForkSource != "p" {
		t.Fatalf("Stat(child) = %+v", sum)
	}
	closeOK(t, c)
	_, err = s.Create(bg, header("c2"), store.CreateOptions{ForkOf: &store.ForkOf{ID: "p", Through: 5}})
	wantErr(t, "fork past the end", err, store.ErrInvalidBoundary)
	_, err = s.Create(bg, header("c3"), store.CreateOptions{ForkOf: &store.ForkOf{ID: "missing"}})
	wantErr(t, "fork of a missing parent", err, store.ErrNotFound)
	if _, err := s.Stat(bg, "c2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a refused fork created its session: %v", err)
	}
}

func testImmutability(t *testing.T, s store.Store) {
	first := call(t, 0)
	in := call(t, 1, 0)
	want := in.Clone()
	w := create(t, s, "s1", first, in)
	defer closeOK(t, w)
	in.Sources[0] = 99 // the caller's slice after hand-over
	got := readAll(t, w, store.ReadOptions{})
	got[1].Sources[0] = 42 // the reader's copy
	again := readAll(t, w, store.ReadOptions{})
	if !again[1].Equal(want) {
		t.Fatalf("a stored record changed after hand-over: %+v, want %+v", again[1], want)
	}
}

func testConcurrent(t *testing.T, s store.Store) {
	const sessions, batches, per = 4, 20, 3
	var wg sync.WaitGroup
	errs := make(chan error, sessions*2)
	for i := range sessions {
		id := fmt.Sprintf("s%d", i)
		w, err := s.Create(bg, header(id), store.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(2)
		go func() {
			defer wg.Done()
			defer w.Close(bg)
			for b := range batches {
				first := store.Seq(b * per)
				recs := make([]store.Record, per)
				for j := range recs {
					d, _ := event.ToolCall.Draft(event.ToolCallPayload{CallID: "c", Name: "n", Arguments: "{}"})
					recs[j], _ = d.Stamp(first+store.Seq(j), times.next())
				}
				if err := w.Append(bg, store.Batch{First: first, Records: recs}); err != nil {
					errs <- err
					return
				}
				if b%5 == 0 {
					if err := w.Flush(bg); err != nil {
						errs <- err
						return
					}
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range batches {
				recs, err := w.Read(bg, 0, 0, store.ReadOptions{})
				if err != nil {
					if errors.Is(err, store.ErrClosed) {
						return
					}
					errs <- err
					return
				}
				for k, r := range recs {
					if r.Seq != store.Seq(k) {
						errs <- fmt.Errorf("%s: record %d has seq %d", id, k, r.Seq)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for i := range sessions {
		if sum := stat(t, s, fmt.Sprintf("s%d", i)); sum.NextSeq != batches*per || sum.Durable != batches*per {
			t.Errorf("s%d: %+v", i, sum)
		}
	}
}

func testCancelled(t *testing.T, s store.Store) {
	w := create(t, s, "s1", calls(t, 0, 1)...)
	defer closeOK(t, w)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if err := w.Append(ctx, store.Batch{First: 1, Records: calls(t, 1, 1)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append(cancelled) = %v", err)
	}
	if _, err := s.Create(ctx, header("s2"), store.CreateOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create(cancelled) = %v", err)
	}
	if n := stat(t, s, "s1").NextSeq; n != 1 {
		t.Fatalf("a cancelled append wrote: NextSeq %d", n)
	}
}
