package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// The owner record (DH-D-EXEC-2, M1-G-BOOT-3): run/owners.json lists every tree
// a running host owns, so the next host on the same home can report what a
// crashed one left behind. Every change takes run/owners.lock, reads the file,
// replaces this host's entries and publishes the result durably (fsx), so hosts
// that share a home keep each other's entries.
const (
	OwnersFile = "owners.json"
	ownersLock = "owners.lock"
)

// recordTimeout bounds each locked change of the record.
const recordTimeout = 5 * time.Second

// ownerEntry is one live tree in the record. Host and HostStart name the host
// that owns it; Pid and PidStart its direct child. A start identity is the
// system's start time of the process (Linux: the start tick in /proc/<pid>/stat;
// Windows: the creation time), so a reused pid is not taken for the same
// process; "" where the system has none.
type ownerEntry struct {
	Host      int       `json:"host"`
	HostStart string    `json:"host_start,omitempty"`
	Pid       int       `json:"pid"`
	PidStart  string    `json:"pid_start,omitempty"`
	Kind      OwnerKind `json:"kind"`
	Cgroup    string    `json:"cgroup,omitempty"`
	Started   time.Time `json:"started"`
}

type ownersDoc struct {
	Version int          `json:"version"`
	Owners  []ownerEntry `json:"owners"`
}

// Leftover is an owner a host that is no longer running left in the record,
// with the members still alive. It is reported, never stopped: an operator ends
// it.
type Leftover struct {
	// Host is the pid of the host that owned the tree.
	Host int
	// Pid is the tree's direct child, Kind its owner and Cgroup its control
	// group directory ("" off the control-group rung).
	Pid    int
	Kind   OwnerKind
	Cgroup string
	// Started is when the host recorded the tree.
	Started time.Time
	// Survivors are the pids of the tree still running: the direct child when it
	// is the same process, the members of its control group, or on Linux's
	// process-group rung the members of its process group.
	Survivors []int
}

func (l Leftover) String() string {
	where := string(l.Kind)
	if l.Cgroup != "" {
		where += " " + l.Cgroup
	}
	return fmt.Sprintf("tree %d of host %d (%s, recorded %s) left running: %v", l.Pid, l.Host, where, l.Started.Format(time.RFC3339), l.Survivors)
}

// ownerRecord is this host's part of the record.
type ownerRecord struct {
	path, lock string
	host       int
	hostStart  string

	mu   sync.Mutex
	live map[*Process]ownerEntry
	err  error
}

// openRecord reads the record in runDir, keeps the entries of running hosts,
// reports the leftovers of the others (dropping those with no survivor) and
// returns this host's record.
func openRecord(ctx context.Context, runDir string) (*ownerRecord, []Leftover, error) {
	if st, err := os.Stat(runDir); err != nil {
		return nil, nil, err
	} else if !st.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", runDir)
	}
	_, start := procIdentity(os.Getpid())
	r := &ownerRecord{
		path: filepath.Join(runDir, OwnersFile), lock: filepath.Join(runDir, ownersLock),
		host: os.Getpid(), hostStart: start, live: map[*Process]ownerEntry{},
	}
	var left []Leftover
	err := r.change(ctx, func(doc *ownersDoc) {
		kept := doc.Owners[:0]
		for _, e := range doc.Owners {
			if r.mine(e) || hostRunning(e) {
				kept = append(kept, e)
				continue
			}
			alive := survivors(e)
			if len(alive) == 0 {
				if e.Kind == ControlGroup && e.Cgroup != "" {
					_ = os.Remove(e.Cgroup) // an empty group only; a populated one refuses
				}
				continue
			}
			kept = append(kept, e) // reported again until an operator ends it
			left = append(left, Leftover{Host: e.Host, Pid: e.Pid, Kind: e.Kind, Cgroup: e.Cgroup, Started: e.Started, Survivors: alive})
		}
		doc.Owners = kept
	})
	if err != nil {
		return nil, nil, err
	}
	return r, left, nil
}

func (r *ownerRecord) mine(e ownerEntry) bool {
	return e.Host == r.host && e.HostStart == r.hostStart
}

// hostRunning reports whether the entry's host is still the running process it
// names; an unknown identity counts as running, so a live host's trees are
// never reported as left behind.
func hostRunning(e ownerEntry) bool {
	alive, start := procIdentity(e.Host)
	return alive && sameStart(e.HostStart, start)
}

func sameStart(recorded, now string) bool { return recorded == "" || now == "" || recorded == now }

// track records p, and removes it again once its tree is gone and its owner
// released. A nil record keeps nothing.
func (r *ownerRecord) track(p *Process) {
	if r == nil {
		return
	}
	pid := p.Pid()
	_, start := procIdentity(pid)
	e := ownerEntry{
		Host: r.host, HostStart: r.hostStart, Pid: pid, PidStart: start,
		Kind: p.report.Kind, Cgroup: ownerCgroup(p.owner), Started: time.Now().UTC().Truncate(time.Second),
	}
	r.mu.Lock()
	r.live[p] = e
	r.publish()
	r.mu.Unlock()
	go func() {
		<-p.releasedC
		r.mu.Lock()
		delete(r.live, p)
		r.publish()
		r.mu.Unlock()
	}()
}

// publish writes this host's live entries in place of those the file holds for
// it; r.mu is held.
func (r *ownerRecord) publish() {
	ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()
	mine := make([]ownerEntry, 0, len(r.live))
	for _, e := range r.live {
		mine = append(mine, e)
	}
	slices.SortFunc(mine, func(a, b ownerEntry) int { return a.Pid - b.Pid })
	edit := func(doc *ownersDoc) {
		doc.Owners = slices.DeleteFunc(doc.Owners, r.mine)
		doc.Owners = append(doc.Owners, mine...)
	}
	// A reader holding the file open on Windows can outlast fsx's own retries of
	// the rename; a stale record would stay until the next change, so try again.
	for attempt := 1; ; attempt++ {
		r.err = r.change(ctx, edit)
		if r.err == nil || attempt == 3 || ctx.Err() != nil {
			return
		}
		time.Sleep(time.Duration(attempt) * 50 * time.Millisecond)
	}
}

// change applies edit to the record under its lock and publishes the result.
// An unreadable record is an error and is left as it is.
func (r *ownerRecord) change(ctx context.Context, edit func(*ownersDoc)) error {
	l, err := fsx.Lock(ctx, r.lock)
	if err != nil {
		return fmt.Errorf("lock %s: %w", ownersLock, err)
	}
	defer l.Unlock()
	var doc ownersDoc
	b, err := os.ReadFile(r.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case len(strings.TrimSpace(string(b))) > 0:
		if err := json.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s is not an owner record: %w", OwnersFile, err)
		}
	}
	edit(&doc)
	doc.Version = 1
	if doc.Owners == nil {
		doc.Owners = []ownerEntry{}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return fsx.Replace(r.path, func(w io.Writer) error {
		_, err := w.Write(append(out, '\n'))
		return err
	}, fsx.Options{Perm: 0o600, Durable: true})
}

func (r *ownerRecord) lastErr() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}
