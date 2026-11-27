package sqlite

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// FuzzParseExport feeds untrusted session-log bytes, an export file read by
// import, to parseExport (DH-ADR-TOOLCHAIN-3 "session log lines"): it never
// panics, and every input it refuses is refused with a coded store error.
func FuzzParseExport(f *testing.F) {
	addExportSeeds(f)
	s, err := Open(obg, filepath.Join(f.TempDir(), "dsh.db"))
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() {
		if err := s.Close(obg); err != nil {
			f.Error(err)
		}
	})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, err := s.parseExport(bytes.NewReader(data), 1<<16)
		var coded *store.Error
		if err != nil && (!errors.As(err, &coded) || coded.Code == "") {
			t.Fatalf("parseExport refused %q with an uncoded error: %v", data, err)
		}
	})
}
