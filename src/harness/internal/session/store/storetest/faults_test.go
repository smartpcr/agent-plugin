package storetest_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/storetest"
)

// The conformance suite is only worth its pass if it fails a store that breaks
// the contract. Each fault below breaks one clause of a correct store (memstore),
// and the suite, run against it in a child process (a failing suite fails the
// test that runs it, so it cannot run in this one), must fail the case that
// checks that clause, saying what is wrong.

// faults are the contract breaks a faultyStore injects.
type faults struct {
	nilStore, noSeeder                                      bool
	readIgnoresLimit, negativeLimitOK, hotKeepsCold         bool
	shortRead, mangledRead, readFails, aliasRecords         bool
	checkpointsReversed                                     bool
	listIgnoresLimit, listOldestFirst, listIgnoresCwd       bool
	listIgnoresCreator, foreignCursorOK, listLoops          bool
	deleteWhileHeld, ownedUntyped, openReadUntyped          bool
	epochStuck, headerDropsCreator, holderStays             bool
	neverDurable, statFails                                 bool
	flushFails, appendFails, closeFails, createFails        bool
	appendIgnoresCtx, createIgnoresCtx, cancelledAppendHits bool
	emptyBatchRefused                                       bool
}

// variants maps a variant to its faults and to what the suite must report: each
// entry is a case name and a fragment of its failure message.
var variants = map[string]struct {
	f    faults
	want [][2]string
}{
	"nil store":           {faults{nilStore: true}, [][2]string{{"CreateAndStat", "the factory returned no store"}}},
	"no seeder":           {faults{noSeeder: true}, [][2]string{{"OpenRefusals", "does not implement storetest.Seeder"}}},
	"read ignores limit":  {faults{readIgnoresLimit: true}, [][2]string{{"PagedReads", "page of 10 records, limit 3"}}},
	"negative limit":      {faults{negativeLimitOK: true}, [][2]string{{"PagedReads", "a negative limit was accepted"}}},
	"hot read keeps cold": {faults{hotKeepsCold: true}, [][2]string{{"ColdPart", "hot read ="}}},
	"short read":          {faults{shortRead: true}, [][2]string{{"AppendOrderAndReadYourWrites", "writer read: 2 records, want 3"}}},
	"mangled read":        {faults{mangledRead: true}, [][2]string{{"AppendOrderAndReadYourWrites", "writer read: record 0 is"}}},
	"read fails":          {faults{readFails: true}, [][2]string{{"AppendOrderAndReadYourWrites", "Read: read failed"}}},
	"aliased records":     {faults{aliasRecords: true}, [][2]string{{"Immutability", "a stored record changed after hand-over"}}},
	"checkpoint order":    {faults{checkpointsReversed: true}, [][2]string{{"Checkpoints", "Checkpoints = "}}},
	"list ignores limit":  {faults{listIgnoresLimit: true}, [][2]string{{"List", "page of 5, limit 2"}}},
	"list oldest first": {faults{listOldestFirst: true}, [][2]string{
		{"List", "List is not most recent first"}, {"ListByPrompt", "listed as of the cut"}}},
	"list ignores cwd":     {faults{listIgnoresCwd: true}, [][2]string{{"List", "List(cwd) ="}}},
	"list ignores creator": {faults{listIgnoresCreator: true}, [][2]string{{"List", "List(creator) ="}}},
	"foreign cursor": {faults{foreignCursorOK: true}, [][2]string{
		{"List", "a foreign cursor was accepted"}, {"ListByPrompt", "a recent-order cursor in prompt order"}}},
	"list loops": {faults{listLoops: true}, [][2]string{
		{"List", "List does not terminate"}, {"ListByPrompt", "List does not terminate"}}},
	"delete while held": {faults{deleteWhileHeld: true}, [][2]string{{"SingleWriter", "Delete while held"}}},
	"owned untyped":     {faults{ownedUntyped: true}, [][2]string{{"SingleWriter", "want *OwnedError naming the holder"}}},
	"open untyped":      {faults{openReadUntyped: true}, [][2]string{{"OpenRefusals", "want *UnknownTypeError"}}},
	"epoch stuck": {faults{epochStuck: true}, [][2]string{
		{"CreateAndStat", "Stat = "}, {"SingleWriter", "did not rise"}, {"ForceTakeover", "did not rise"}}},
	"header drops creator": {faults{headerDropsCreator: true}, [][2]string{
		{"CreateAndStat", "Header = "}, {"Fork", "fork header ="}}},
	"holder stays": {faults{holderStays: true}, [][2]string{
		{"CreateAndStat", "Stat after Close"}, {"OpenRefusals", "a refused open took the lease"}}},
	"never durable": {faults{neverDurable: true}, [][2]string{
		{"FlushBarrier", "after Flush Durable = 0, want 4"}, {"Concurrent", "s0: "}}},
	"stat fails": {faults{statFails: true}, [][2]string{{"CreateAndStat", "Stat(s1): stat failed"}}},
	"flush fails": {faults{flushFails: true}, [][2]string{
		{"FlushBarrier", "Flush: flush failed"}, {"ForceTakeover", "flush failed"}, {"Concurrent", "flush failed"}}},
	"append fails": {faults{appendFails: true}, [][2]string{
		{"AppendOrderAndReadYourWrites", "Append at 0: append failed"}, {"Checkpoints", "append failed"}, {"Concurrent", "append failed"}}},
	"close fails":           {faults{closeFails: true}, [][2]string{{"CreateAndStat", "Close: close failed"}}},
	"create fails":          {faults{createFails: true}, [][2]string{{"CreateAndStat", "Create: create failed"}, {"AppendOrderAndReadYourWrites", "Create(s1): create failed"}}},
	"append ignores ctx":    {faults{appendIgnoresCtx: true}, [][2]string{{"CancelledContext", "Append(cancelled) = <nil>"}}},
	"create ignores ctx":    {faults{createIgnoresCtx: true}, [][2]string{{"CancelledContext", "Create(cancelled) = <nil>"}}},
	"cancelled append hits": {faults{cancelledAppendHits: true}, [][2]string{{"CancelledContext", "a cancelled append wrote: NextSeq 2"}}},
	"empty batch refused":   {faults{emptyBatchRefused: true}, [][2]string{{"AppendOrderAndReadYourWrites", "empty batch: empty batch refused"}}},
}

const variantEnv = "DSH_STORETEST_FAULT"

// DH-T-STORE-1: the conformance suite fails each store that breaks one of its
// clauses, in the case that checks that clause.
//
//dsh:test DH-T-STORE-1
func TestSuiteFailsABrokenStore(t *testing.T) {
	names := make([]string, 0, len(variants))
	for name := range variants {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		v := variants[name]
		// One child at a time: children started together race to write the
		// coverage directory's shared meta file, and a child that loses writes
		// no counters.
		t.Run(name, func(t *testing.T) {
			out, err := runChild(t, name)
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("the suite over a store with %q passed or did not run (%v):\n%s", name, err, out)
			}
			for _, w := range v.want {
				if !failedWith(out, w[0], w[1]) {
					t.Errorf("the suite did not fail %s with %q:\n%s", w[0], w[1], out)
				}
			}
		})
	}
}

// runChild runs TestFaultyStoreChild for variant in a re-executed test binary,
// passing the coverage directory on so the child's counters are merged.
func runChild(t *testing.T, variant string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	args := []string{"-test.run=^TestFaultyStoreChild$", "-test.count=1", "-test.v"}
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		args = append(args, "-test.gocoverdir="+dir)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), variantEnv+"="+variant)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// failedWith reports whether the child's output fails case with a message
// holding fragment: the case's failure line follows its messages.
func failedWith(out, name, fragment string) bool {
	head := "--- FAIL: TestFaultyStoreChild/" + name + " "
	i := strings.Index(out, head)
	if i < 0 {
		return false
	}
	// The case's messages are printed between its "=== RUN" line and its
	// "--- FAIL" line.
	run := strings.LastIndex(out[:i], "=== RUN   TestFaultyStoreChild/"+name+"\n")
	if run < 0 {
		run = 0
	}
	return strings.Contains(out[run:i], fragment)
}

// TestFaultyStoreChild is the child of TestSuiteFailsABrokenStore: it runs the
// suite over a faulty store and is expected to fail.
func TestFaultyStoreChild(t *testing.T) {
	name := os.Getenv(variantEnv)
	if name == "" {
		t.Skip("the child of TestSuiteFailsABrokenStore")
	}
	v, ok := variants[name]
	if !ok {
		t.Fatalf("unknown variant %q", name)
	}
	storetest.Run(t, func(*testing.T) store.Store {
		if v.f.nilStore {
			return nil
		}
		m := memstore.New()
		fs := &faultyStore{inner: m, f: v.f}
		if v.f.noSeeder {
			return fs
		}
		return seedingStore{fs, m}
	})
}

// ---------------------------------------------------------------- the store

type faultyStore struct {
	inner store.Store
	f     faults
}

type seedingStore struct {
	*faultyStore
	seeder storetest.Seeder
}

func (s seedingStore) Seed(ctx context.Context, h store.Header, recs []store.Record) error {
	return s.seeder.Seed(ctx, h, recs)
}

func (s *faultyStore) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	if s.f.createFails {
		return nil, errors.New("create failed")
	}
	if s.f.createIgnoresCtx {
		ctx = context.WithoutCancel(ctx)
	}
	w, err := s.inner.Create(ctx, h, o)
	if err != nil {
		return nil, err
	}
	return s.writer(w), nil
}

func (s *faultyStore) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	w, err := s.inner.OpenWrite(ctx, id, o)
	if err != nil {
		if s.f.ownedUntyped && errors.Is(err, store.ErrOwned) {
			return nil, fmt.Errorf("held elsewhere: %w", store.ErrOwned)
		}
		return nil, err
	}
	return s.writer(w), nil
}

func (s *faultyStore) OpenRead(ctx context.Context, id string) (store.Reader, error) {
	r, err := s.inner.OpenRead(ctx, id)
	if err != nil {
		if s.f.openReadUntyped {
			return nil, errors.New(err.Error())
		}
		return nil, err
	}
	return &faultyReader{Reader: r, f: s.f}, nil
}

func (s *faultyStore) writer(w store.Writer) store.Writer {
	return &faultyWriter{Writer: w, r: faultyReader{Reader: w, f: s.f}, f: s.f, aliased: map[store.Seq]store.Record{}}
}

func (s *faultyStore) Stat(ctx context.Context, id string) (store.Summary, error) {
	if s.f.statFails {
		return store.Summary{}, errors.New("stat failed")
	}
	sum, err := s.inner.Stat(ctx, id)
	if err != nil {
		return sum, err
	}
	if s.f.holderStays && sum.Holder == nil {
		sum.Holder = &store.Holder{Instance: "ghost"}
	}
	if s.f.neverDurable {
		sum.Durable = 0
	}
	return sum, nil
}

func (s *faultyStore) List(ctx context.Context, q store.Query) (store.Page, error) {
	if s.f.listIgnoresLimit {
		q.Limit = 0
	}
	if s.f.listIgnoresCwd {
		q.Cwd = ""
	}
	if s.f.listIgnoresCreator {
		q.Creator = ""
	}
	if s.f.listLoops && q.Cursor == "loop" {
		q.Cursor = ""
	}
	p, err := s.inner.List(ctx, q)
	if err != nil {
		if s.f.foreignCursorOK && q.Cursor != "" {
			return store.Page{}, nil
		}
		return p, err
	}
	if s.f.listOldestFirst {
		slices.Reverse(p.Summaries)
	}
	if s.f.listLoops {
		p.Next = "loop"
	}
	return p, nil
}

func (s *faultyStore) Delete(ctx context.Context, id string, o store.DeleteOptions) error {
	err := s.inner.Delete(ctx, id, o)
	if s.f.deleteWhileHeld && errors.Is(err, store.ErrOwned) {
		return nil
	}
	return err
}

func (s *faultyStore) Flush(ctx context.Context) error {
	if s.f.flushFails {
		return errors.New("flush failed")
	}
	return s.inner.Flush(ctx)
}

// ---------------------------------------------------------------- handles

type faultyReader struct {
	store.Reader
	f faults
}

func (r *faultyReader) Read(ctx context.Context, from store.Seq, limit int, o store.ReadOptions) ([]store.Record, error) {
	if r.f.readFails {
		return nil, errors.New("read failed")
	}
	if r.f.negativeLimitOK && limit < 0 {
		return nil, nil
	}
	if r.f.readIgnoresLimit && limit > 0 {
		limit = 0
	}
	if r.f.hotKeepsCold {
		o.WithAux = true
	}
	recs, err := r.Reader.Read(ctx, from, limit, o)
	if err != nil {
		return nil, err
	}
	if r.f.shortRead && len(recs) > 0 {
		recs = recs[:len(recs)-1]
	}
	if r.f.mangledRead {
		for i := range recs {
			recs[i].Payload += " "
		}
	}
	return recs, nil
}

func (r *faultyReader) ReadSeqs(ctx context.Context, seqs []store.Seq, o store.ReadOptions) ([]store.Record, error) {
	if r.f.hotKeepsCold {
		o.WithAux = true
	}
	return r.Reader.ReadSeqs(ctx, seqs, o)
}

func (r *faultyReader) Checkpoints(ctx context.Context) ([]store.Checkpoint, error) {
	cps, err := r.Reader.Checkpoints(ctx)
	if r.f.checkpointsReversed {
		slices.Reverse(cps)
	}
	return cps, err
}

type faultyWriter struct {
	store.Writer
	r faultyReader
	f faults

	mu      sync.Mutex
	aliased map[store.Seq]store.Record // the caller's records, kept without a copy
}

func (w *faultyWriter) Header() store.Header {
	h := w.Writer.Header()
	if w.f.headerDropsCreator {
		h.Creator = ""
	}
	return h
}

func (w *faultyWriter) Epoch() uint64 {
	if w.f.epochStuck {
		return 0
	}
	return w.Writer.Epoch()
}

func (w *faultyWriter) Append(ctx context.Context, b store.Batch) error {
	switch {
	case w.f.appendFails:
		return errors.New("append failed")
	case w.f.emptyBatchRefused && len(b.Records) == 0 && len(b.Checkpoints) == 0:
		return errors.New("empty batch refused")
	case w.f.appendIgnoresCtx:
		ctx = context.WithoutCancel(ctx)
	case w.f.cancelledAppendHits && ctx.Err() != nil:
		_ = w.Writer.Append(context.WithoutCancel(ctx), b)
		return ctx.Err()
	}
	if err := w.Writer.Append(ctx, b); err != nil {
		return err
	}
	if w.f.aliasRecords {
		w.mu.Lock()
		for _, r := range b.Records {
			w.aliased[r.Seq] = r
		}
		w.mu.Unlock()
	}
	return nil
}

func (w *faultyWriter) Flush(ctx context.Context) error {
	if w.f.flushFails {
		return errors.New("flush failed")
	}
	return w.Writer.Flush(ctx)
}

func (w *faultyWriter) Close(ctx context.Context) error {
	err := w.Writer.Close(ctx)
	if w.f.closeFails {
		return errors.New("close failed")
	}
	return err
}

func (w *faultyWriter) Read(ctx context.Context, from store.Seq, limit int, o store.ReadOptions) ([]store.Record, error) {
	recs, err := w.r.Read(ctx, from, limit, o)
	if err != nil || !w.f.aliasRecords {
		return recs, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, r := range recs {
		if a, ok := w.aliased[r.Seq]; ok {
			recs[i] = a
		}
	}
	return recs, nil
}

func (w *faultyWriter) ReadSeqs(ctx context.Context, seqs []store.Seq, o store.ReadOptions) ([]store.Record, error) {
	return w.r.ReadSeqs(ctx, seqs, o)
}

func (w *faultyWriter) Checkpoints(ctx context.Context) ([]store.Checkpoint, error) {
	return w.r.Checkpoints(ctx)
}
