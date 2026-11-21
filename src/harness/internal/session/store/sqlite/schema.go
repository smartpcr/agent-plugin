package sqlite

// ApplicationID is the identity written into the header of every dsh.db
// (PRAGMA application_id): "DSH1" as a big-endian int32. A database carrying
// any other non-zero id is refused without being changed (DH-LIB-SQLITE-R12).
const ApplicationID = 0x44534831

// migrations is the numbered layout history of architecture.md §4.3: entry i
// takes a database from layout version i to i+1 (PRAGMA user_version). Entries
// are appended, never edited; a database whose version is past the end was
// written by a newer build and is refused.
var migrations = []string{
	// 1: the tables of architecture.md §4.3.
	`
CREATE TABLE sessions (
	-- uid is never reused (AUTOINCREMENT), so a handle opened on a deleted
	-- session never reads a later session that took the same id.
	uid              INTEGER PRIMARY KEY AUTOINCREMENT,
	id               TEXT    NOT NULL UNIQUE,
	created_at       INTEGER NOT NULL,
	cwd              TEXT    NOT NULL DEFAULT '',
	origin           TEXT    NOT NULL DEFAULT '',
	creator          TEXT    NOT NULL DEFAULT '',
	fork_source      TEXT    NOT NULL DEFAULT '',
	fork_through     INTEGER NOT NULL DEFAULT 0,
	inherited_count  INTEGER NOT NULL DEFAULT 0,
	delegation_depth INTEGER NOT NULL DEFAULT 0,
	preset           TEXT    NOT NULL DEFAULT '',
	next_seq         INTEGER NOT NULL DEFAULT 0 CHECK (next_seq >= 0),
	lease_epoch      INTEGER NOT NULL DEFAULT 0,
	lease_holder     TEXT,
	lease_pid        INTEGER NOT NULL DEFAULT 0,
	lease_kind       TEXT    NOT NULL DEFAULT '',
	lease_since      INTEGER NOT NULL DEFAULT 0,
	state            TEXT    NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'deleting')),
	last_event_at    INTEGER NOT NULL DEFAULT 0,
	bytes            INTEGER NOT NULL DEFAULT 0,
	imported_from    TEXT    NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX sessions_recent ON sessions (last_event_at DESC, created_at DESC, id);
CREATE INDEX sessions_cwd ON sessions (cwd);
CREATE INDEX sessions_creator ON sessions (creator);

CREATE TABLE events (
	session   INTEGER NOT NULL REFERENCES sessions (uid),
	seq       INTEGER NOT NULL CHECK (seq >= 0),
	time      INTEGER NOT NULL,
	type      TEXT    NOT NULL CHECK (type <> ''),
	pv        INTEGER NOT NULL,
	ignorable INTEGER NOT NULL CHECK (ignorable IN (0, 1)),
	surface   INTEGER NOT NULL CHECK (surface IN (0, 1, 2)),
	env       TEXT,
	data      TEXT    NOT NULL,
	cold      TEXT,
	PRIMARY KEY (session, seq)
) STRICT, WITHOUT ROWID;

-- The log is immutable: no UPDATE ever, and a DELETE only while its session is
-- being deleted.
CREATE TRIGGER events_no_update BEFORE UPDATE ON events
BEGIN
	SELECT RAISE(ABORT, 'session/immutable: an event is never updated');
END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events
WHEN (SELECT state FROM sessions WHERE uid = OLD.session) IS NOT 'deleting'
BEGIN
	SELECT RAISE(ABORT, 'session/immutable: an event is deleted only with its session');
END;

CREATE TABLE checkpoints (
	session       INTEGER NOT NULL REFERENCES sessions (uid),
	key           TEXT    NOT NULL CHECK (key <> ''),
	state_version INTEGER NOT NULL,
	seq           INTEGER NOT NULL CHECK (seq >= 0),
	state         TEXT    NOT NULL,
	PRIMARY KEY (session, key, state_version)
) STRICT, WITHOUT ROWID;

CREATE TABLE hosts (
	instance   TEXT    PRIMARY KEY,
	pid        INTEGER NOT NULL,
	kind       TEXT    NOT NULL,
	started_at INTEGER NOT NULL
) STRICT;

CREATE TABLE duties (
	name        TEXT    PRIMARY KEY,
	holder      TEXT,
	epoch       INTEGER NOT NULL DEFAULT 0,
	last_run_at INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE requests (
	id      TEXT PRIMARY KEY,
	session TEXT NOT NULL,
	kind    TEXT NOT NULL,
	drafts  TEXT NOT NULL,
	state   TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'applied', 'refused', 'expired'))
) STRICT;
`,
	// 2: the machine-state tables of docs/harness/areas/storage.md §6.2
	// (DH-D-STORAGE-1), written through internal/storage: typed records per
	// namespace, each namespace's revision and authority, and the workspace
	// registry.
	`
CREATE TABLE kv_ns (
	namespace TEXT    PRIMARY KEY CHECK (namespace <> ''),
	revision  INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
	authority TEXT    NOT NULL CHECK (authority IN ('authoritative', 'derived'))
) STRICT;

CREATE TABLE kv (
	namespace  TEXT    NOT NULL REFERENCES kv_ns (namespace),
	key        TEXT    NOT NULL CHECK (key <> ''),
	version    INTEGER NOT NULL CHECK (version >= 1),
	value      TEXT    NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (namespace, key)
) STRICT, WITHOUT ROWID;

CREATE TABLE workspaces (
	id             TEXT    PRIMARY KEY CHECK (id <> ''),
	path           TEXT    NOT NULL UNIQUE CHECK (path <> ''),
	requested_path TEXT    NOT NULL,
	title          TEXT    NOT NULL,
	position       INTEGER NOT NULL,
	created_at     INTEGER NOT NULL,
	updated_at     INTEGER NOT NULL
) STRICT;
CREATE INDEX workspaces_order ON workspaces (position, id);
`,
	// 3: workspace membership and session flags of docs/harness/areas/storage.md
	// DH-D-STORAGE-7 (DH-ADR-STORAGE-7): rows, not session events, so a process
	// that does not hold a session's lease can archive it. Both go with their
	// session's row, and membership with its workspace's row.
	`
CREATE TABLE workspace_sessions (
	session      INTEGER PRIMARY KEY REFERENCES sessions (uid) ON DELETE CASCADE,
	workspace    TEXT    NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
	attached_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX workspace_sessions_by_workspace ON workspace_sessions (workspace, session);

CREATE TABLE session_flags (
	session    INTEGER PRIMARY KEY REFERENCES sessions (uid) ON DELETE CASCADE,
	archive    TEXT    NOT NULL DEFAULT '' CHECK (archive IN ('', 'pending', 'archived')),
	archive_at INTEGER NOT NULL DEFAULT 0,
	archive_owner TEXT NOT NULL DEFAULT '',
	pinned     INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1)),
	updated_at INTEGER NOT NULL
) STRICT;
`,
}

// LayoutVersion is the layout this build writes: the number of migrations.
var LayoutVersion = len(migrations)
