package blob_test

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/blob"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// The publish-then-cite crash test (DH-T-STORAGE-7) lives here, in the external
// test package, so it can drive the SQLite session store's blob_refs path: the
// store does not import this package, so there is no cycle.
//
// Its child is TestPublishCrashChild: it opens the home at DSH_BLOB_CRASH_CHILD,
// holds session "s" (creating it, or taking the dead previous child's lease),
// reports "ready next=<seq>" and then, for each "go" line on stdin, does the
// next half of one iteration: Put object n (its bytes are content(n)) and
// report "put n=<n>"; then append the event citing it at seq n and report
// "appended n=<n>". It never closes anything: the parent kills it.
const (
	publishChildEnv = "DSH_BLOB_CRASH_CHILD"
	// publishSeedEnv replays a logged seed.
	publishSeedEnv = "DSH_BLOB_CRASH_SEED"
	publishSession = "s"
)

// cited is the test event that cites one stored object; its BlobRefs puts the
// object in the event's blob_refs row, in the append's transaction.
type cited struct {
	N      int64    `json:"n"`
	Object blob.Ref `json:"object"`
}

func (c cited) BlobRefs() []string { return []string{c.Object.String()} }

// publishTypes is the registry the child and the parent open the store with.
func publishTypes(t testing.TB) (*event.Registry, *event.Type[cited]) {
	t.Helper()
	reg := event.NewRegistry()
	typ, err := event.Define(reg, event.Spec[cited]{Name: "test/cited", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	return reg, typ
}

// content is object n's bytes: distinct per n, 64 KiB to 448 KiB, so a kill
// sent while a Put is in flight can land inside its stream.
func content(n int64) []byte {
	line := fmt.Appendf(nil, "object %d\n", n)
	return bytes.Repeat(line, int((n%7+1)<<16)/len(line))
}

func TestPublishCrashChild(t *testing.T) {
	root := os.Getenv(publishChildEnv)
	if root == "" {
		t.Skip("the child of TestKilledPublisherNeverLeavesADanglingCitation")
	}
	l := home.New(root)
	reg, typ := publishTypes(t)
	s, err := sqlite.OpenHome(bg, l, sqlite.WithRegistry(reg), sqlite.WithKind("child"))
	if err != nil {
		t.Fatal(err)
	}
	bs, err := blob.New(l)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.OpenWrite(bg, publishSession, store.OpenOptions{})
	if err != nil {
		if w, err = s.Create(bg, store.Header{ID: publishSession}, store.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := s.Stat(bg, publishSession)
	if err != nil {
		t.Fatal(err)
	}
	in := bufio.NewScanner(os.Stdin)
	next := func() {
		if !in.Scan() {
			t.Fatal("the parent closed stdin without killing the child")
		}
	}
	fmt.Printf("ready next=%d\n", sum.NextSeq)
	for n := int64(sum.NextSeq); ; n++ {
		next()
		ref, err := bs.Put(bg, blob.KindData, bytes.NewReader(content(n)), 0)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("put n=%d\n", n)
		next()
		d, err := typ.Draft(cited{N: n, Object: ref})
		if err != nil {
			t.Fatal(err)
		}
		rec, err := d.Stamp(store.Seq(n), 1_700_000_000_000+n)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Append(bg, store.Batch{First: store.Seq(n), Records: []store.Record{rec}}); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("appended n=%d\n", n)
	}
}

// publisher is one running child.
type publisher struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Scanner
	log   []string
}

func startPublisher(t *testing.T, l home.Layout) *publisher {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPublishCrashChild$", "-test.count=1", "-test.v")
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "DSH_HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, publishChildEnv+"="+l.Root())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &publisher{t: t, cmd: cmd, stdin: stdin, out: bufio.NewScanner(out)}
	// The cleanup kills the child (unblocking it) and then waits for it; after
	// the test's own kill it finds no process and does nothing.
	t.Cleanup(func() { stopPublisher(t, cmd) })
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *publisher) expect(prefix string) string {
	p.t.Helper()
	for p.out.Scan() {
		p.log = append(p.log, p.out.Text())
		if strings.HasPrefix(p.out.Text(), prefix) {
			return p.out.Text()
		}
	}
	p.t.Fatalf("the child never reported %q:\n%s", prefix, strings.Join(p.log, "\n"))
	return ""
}

func (p *publisher) step() {
	p.t.Helper()
	if _, err := fmt.Fprintln(p.stdin, "go"); err != nil {
		p.t.Fatal(err)
	}
}

// kill kills the child, if it still runs, waits for it and checks it is gone.
func (p *publisher) kill() { stopPublisher(p.t, p.cmd) }

// stopPublisher kills cmd's process, if it has one, then waits for it and checks
// it is gone; a second call (the cleanup after the test's own kill) is a no-op.
func stopPublisher(t *testing.T, cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	cmd.Process.Kill() //nolint:errcheck // may be dead already
	cmd.Wait()         //nolint:errcheck // killed
	cmd.Process = nil
	if !subprocesstest.Exited(pid) {
		t.Errorf("child %d still runs after its kill", pid)
	}
}

func reportedN(t *testing.T, line string) int64 {
	t.Helper()
	_, v, ok := strings.Cut(line, "n=")
	if !ok {
		t.Fatalf("%q has no n", line)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// DH-D-STORAGE-3 / DH-ADR-STORAGE-3: a child publishes an object and then
// appends the event citing it, and is killed, 20 times over on one home, at a
// point drawn from a logged seed: paused between the publish and the append,
// paused after the append, or with its next Put or append in flight. After
// each kill the home is reopened: every blob_refs row of the session names an
// object that exists and verifies (length and digest) with the bytes its event
// published, every event cites its row, and every append the child reported
// is there. The draft clause of DH-T-STORAGE-7 waits for the draft record
// (M1-D-STORE-D, held open to DH-S-21).
//
//dsh:test DH-T-STORAGE-7
func TestKilledPublisherNeverLeavesADanglingCitation(t *testing.T) {
	seed := rand.Uint64()
	if v := os.Getenv(publishSeedEnv); v != "" {
		var err error
		if seed, err = strconv.ParseUint(v, 10, 64); err != nil {
			t.Fatalf("%s=%q: %v", publishSeedEnv, v, err)
		}
	}
	t.Logf("seed %d (replay with %s=%d)", seed, publishSeedEnv, seed)
	rnd := rand.New(rand.NewPCG(seed, 27))
	l := temphome.New(t)
	reg, _ := publishTypes(t)
	appended := int64(0) // the records the children reported appended
	for round := 0; round < 20; round++ {
		p := startPublisher(t, l)
		if got := reportedN(t, strings.Replace(p.expect("ready"), "next=", "n=", 1)); got < appended {
			t.Fatalf("round %d: the child starts at seq %d; %d were reported appended", round, got, appended)
		}
		for k := rnd.IntN(4); k > 0; k-- {
			p.step()
			p.expect("put")
			p.step()
			appended = reportedN(t, p.expect("appended")) + 1
		}
		var at string
		switch round % 3 {
		case 0:
			p.step()
			p.expect("put")
			at = "between publish and append"
		case 1:
			at = "after the append"
		default:
			if rnd.IntN(2) == 1 {
				p.step()
				p.expect("put")
				at = "with the append in flight"
			} else {
				at = "with the Put in flight"
			}
			p.step()
		}
		p.kill()
		n := verifyCitations(t, l, reg)
		if n < appended {
			t.Fatalf("round %d (killed %s): %d citing events; the children reported %d appended", round, at, n, appended)
		}
		t.Logf("round %d: killed %s; %d citations verify", round, at, n)
	}
}

// verifyCitations reopens the home and checks every blob_refs row of the
// session against the store's objects; it returns the number of events.
func verifyCitations(t *testing.T, l home.Layout, reg *event.Registry) int64 {
	t.Helper()
	s, err := sqlite.OpenHome(bg, l, sqlite.WithRegistry(reg))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close(bg) }()
	bs, err := blob.New(l, blob.WithIndex(s))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.OpenRead(bg, publishSession)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(bg) }()
	recs, err := r.Read(bg, 0, 0, store.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	events := map[int64]cited{}
	for i, rec := range recs {
		var c cited
		if err := json.Unmarshal([]byte(rec.Payload), &c); err != nil {
			t.Fatalf("seq %d: %v", rec.Seq, err)
		}
		if int64(rec.Seq) != int64(i) || c.N != int64(i) {
			t.Fatalf("record %d has seq %d and n %d", i, rec.Seq, c.N)
		}
		if ok, err := bs.ReferencedBy(bg, publishSession, c.Object); err != nil || !ok {
			t.Fatalf("seq %d cites %s with no blob_refs row (%v)", rec.Seq, c.Object, err)
		}
		events[c.N] = c
	}

	db, err := sql.Open("sqlite", l.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT b.seq, b.ref FROM blob_refs b JOIN sessions s ON s.uid = b.session WHERE s.id = ?`, publishSession)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var (
			seq int64
			ref string
		)
		if err := rows.Scan(&seq, &ref); err != nil {
			t.Fatal(err)
		}
		n++
		c, ok := events[seq]
		if !ok || c.Object.String() != ref {
			t.Fatalf("blob_refs row (%d, %s) has no event citing it", seq, ref)
		}
		rc, err := bs.Open(bg, c.Object)
		if err != nil {
			t.Fatalf("blob_refs row (%d, %s): %v", seq, ref, err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("blob_refs row (%d, %s) does not verify: %v", seq, ref, err)
		}
		if !bytes.Equal(got, content(seq)) {
			t.Fatalf("blob_refs row (%d, %s) reads %d other bytes", seq, ref, len(got))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != len(recs) {
		t.Fatalf("%d blob_refs rows for %d citing events", n, len(recs))
	}
	return int64(len(recs))
}
