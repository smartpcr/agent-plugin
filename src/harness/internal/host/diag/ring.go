package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Ring keeps the last bytes written to it: the log tail of a report. It is a
// cordis.Exporter, so the boot tree exports its log lines into it.
type Ring struct {
	mu   sync.Mutex
	size int
	buf  []byte
}

var _ cordis.Exporter = (*Ring)(nil)

// NewRing returns a ring of size bytes; size <= 0 is RingSize.
func NewRing(size int) *Ring {
	if size <= 0 {
		size = RingSize
	}
	return &Ring{size: size}
}

// Write appends b, dropping the oldest bytes beyond the size.
func (r *Ring) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, b...)
	if over := len(r.buf) - r.size; over > 0 {
		r.buf = append(r.buf[:0], r.buf[over:]...)
	}
	return len(b), nil
}

// Bytes returns a copy of what the ring holds.
func (r *Ring) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf...)
}

// Export writes one log line.
func (r *Ring) Export(m cordis.LogMessage) {
	line := fmt.Sprintf("%s %s %s: %s", m.Time.UTC().Format(time.RFC3339Nano), m.Level, m.Name, m.Text)
	if m.Err != nil {
		line += ": " + m.Err.Error()
	}
	_, _ = r.Write([]byte(line + "\n"))
}

// CrashFile is the name, under <home>/run, of the file the Go runtime writes an
// unrecovered panic or a fatal runtime error to. It is kept out of reports/, which
// holds exactly the reports Fatal writes.
const CrashFile = "crash.txt"

// Install routes the runtime's crash output (a panic nobody recovered, a fatal
// runtime error) to <runDir>/crash.txt as well as standard error, and returns the
// function that undoes it and removes the file when nothing was written.
func Install(runDir string) (uninstall func(), err error) {
	path := filepath.Join(runDir, CrashFile)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return func() {}, err
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		return func() {}, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
			info, err := f.Stat()
			_ = f.Close()
			if err == nil && info.Size() == 0 {
				_ = os.Remove(path)
			}
		})
	}, nil
}
