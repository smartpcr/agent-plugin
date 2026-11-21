//go:build e2e

package sqlite

import (
	"context"
	"database/sql"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// E2EStaleWriter is a writer of session id on s in the name of holder at epoch,
// as a displaced holder (dead or alive, in another process) would still carry
// it. It takes no lease: its appends and flushes meet the writer guard as the
// real holder's would. Only the e2e build (-tags e2e) has it; the package's own
// tests use StaleWriter (export_test.go).
func E2EStaleWriter(ctx context.Context, s *Store, id, holder string, epoch uint64) (store.Writer, error) {
	var r row
	if err := s.read(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = liveByID(ctx, tx, id)
		return err
	}); err != nil {
		return nil, err
	}
	return &writer{reader: reader{s: s, id: id, uid: r.uid, header: r.sum.Header}, epoch: epoch, holder: holder}, nil
}
