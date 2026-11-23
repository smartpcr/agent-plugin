package sqlite

import (
	"context"
	"database/sql"
)

// The seams of the layout upgrade tests (migrate_test.go). Each returns its
// restore.

// SetLayoutCap makes Open upgrade to layout n at most (and refuse a database
// past n as newer), as a build of that layout would.
func SetLayoutCap(n int) func() {
	old := layoutTarget
	layoutTarget = func() int { return n }
	return func() { layoutTarget = old }
}

// SetMigrationFault makes the upgrade fail with err right before migration v
// (layout v to v+1), inside its transaction.
func SetMigrationFault(v int, err error) func() {
	old := beforeMigration
	beforeMigration = func(at int) error {
		if at == v {
			return err
		}
		return nil
	}
	return func() { beforeMigration = old }
}

// SetVacuumInto replaces the backup copy: fn writes (or fails to write) path.
func SetVacuumInto(fn func(ctx context.Context, c *sql.Conn, path string) error) func() {
	old := vacuumInto
	vacuumInto = fn
	return func() { vacuumInto = old }
}

// SetJournalPragma replaces the statement that enters the write-ahead log.
func SetJournalPragma(q string) func() {
	old := journalPragma
	journalPragma = q
	return func() { journalPragma = old }
}

// BuildVersion is buildVersion, for the version-string test.
var BuildVersion = buildVersion
