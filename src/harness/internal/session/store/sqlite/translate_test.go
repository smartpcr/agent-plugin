package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// Every driver error crosses the store boundary as a harness code with the
// driver's message, never as the driver's type (DH-LIB-SQLITE-R7).
func TestTranslate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raw.db")
	rawOpen := func(q string) *sql.DB {
		db, err := sql.Open("sqlite", path+q)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { db.Close() })
		return db
	}
	a := rawOpen("?_pragma=busy_timeout(0)&_pragma=journal_mode(WAL)")
	if _, err := a.Exec(`CREATE TABLE t (x INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_, constraint := a.Exec(`INSERT INTO t VALUES (1), (1)`)
	_, readOnly := rawOpen("?_pragma=query_only(1)").Exec(`INSERT INTO t VALUES (2)`)
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO t VALUES (3)`); err != nil {
		t.Fatal(err)
	}
	_, busy := rawOpen("?_pragma=busy_timeout(0)").Exec(`INSERT INTO t VALUES (4)`)
	tx.Rollback() //nolint:errcheck // only held the lock

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	harnessErr := store.Errorf(store.CodeDeleted, "gone")
	unknown := &event.UnknownTypeError{Type: "x/y"}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want error
	}{
		{"constraint", ctx, constraint, &store.Error{Code: CodeConstraint}},
		{"read-only", ctx, readOnly, &store.Error{Code: CodeReadOnly}},
		{"busy", ctx, busy, &store.Error{Code: CodeBusy}},
		{"plain", ctx, errors.New("boom"), &store.Error{Code: CodeIO}},
		{"cancelled context", cancelled, constraint, context.Canceled},
		{"context error", ctx, context.DeadlineExceeded, context.DeadlineExceeded},
		{"harness error", ctx, harnessErr, harnessErr},
		{"registry refusal", ctx, unknown, unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("the driver returned no error to translate")
			}
			got := translate(tc.ctx, tc.err)
			if !errors.Is(got, tc.want) {
				t.Fatalf("translate(%v) = %v, want %v", tc.err, got, tc.want)
			}
			var se *store.Error
			if errors.As(got, &se) && se.Err != nil {
				t.Fatalf("translate wrapped %T", se.Err)
			}
		})
	}
	if translate(ctx, nil) != nil {
		t.Fatal("translate(nil) is not nil")
	}
}
