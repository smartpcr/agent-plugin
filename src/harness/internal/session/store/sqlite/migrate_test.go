package sqlite_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// The layout upgrade (DH-T-STORE-2; M1-G-STORE-1, -2, -3). A home "at layout L"
// is built by this build with its layout capped at L (sqlite.SetLayoutCap), so
// the step from L to L+1 is a real one of this build.

const (
	migrateChildEnv = "DSH_SQLITE_MIGRATE_CHILD"
	migrateCapEnv   = "DSH_SQLITE_MIGRATE_CAP"
)

// TestMigrateChild is the re-exec'd older instance: it opens the database named
// by DSH_SQLITE_MIGRATE_CHILD as a build of layout DSH_SQLITE_MIGRATE_CAP would,
// reports "ready pid=<pid>" and holds the home open until the parent kills it.
func TestMigrateChild(t *testing.T) {
	path := os.Getenv(migrateChildEnv)
	if path == "" {
		t.Skip("the child of the layout upgrade tests")
	}
	n, err := strconv.Atoi(os.Getenv(migrateCapEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.SetLayoutCap(n)()
	if _, err := sqlite.Open(bg, path, sqlite.WithKind("older")); err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("ready pid=" + strconv.Itoa(os.Getpid()) + "\n")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
	}
	t.Fatal("the parent did not kill the child")
}

// atLayout builds the database at path at layout n with session "s": three
// events and a core checkpoint, closed cleanly.
func atLayout(t *testing.T, path string, n int) {
	t.Helper()
	defer sqlite.SetLayoutCap(n)()
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0), call(t, 1), call(t, 2)},
		Checkpoints: []store.Checkpoint{{Key: "core/x", StateVersion: 1, Seq: 2, State: `{"n":3}`}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if v := userVersion(t, path); v != int64(n) {
		t.Fatalf("fixture layout %d, want %d", v, n)
	}
}

// userVersion is PRAGMA user_version read by a fresh connection.
func userVersion(t *testing.T, path string) int64 {
	t.Helper()
	var v int64
	if err := fresh(t, path).QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// hasTable reports whether the database at path has table name.
func hasTable(t *testing.T, path, name string) bool {
	t.Helper()
	return countWhere(t, fresh(t, path), `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, name) == 1
}

// backups lists the files under the backup directory beside path.
func backups(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(path), sqlite.BackupDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(filepath.Dir(path), sqlite.BackupDir, e.Name()))
	}
	return out
}

// checkBackup requires the backup at path to pass integrity_check, to be at
// layout from and to hold session "s" whole, and to be owner-only.
func checkBackup(t *testing.T, path string, from int) {
	t.Helper()
	if want := fmt.Sprintf("dsh-L%d-", from); !strings.HasPrefix(filepath.Base(path), want) || filepath.Ext(path) != ".db" {
		t.Fatalf("backup name %s, want %s<unix ns>.db", filepath.Base(path), want)
	}
	db := fresh(t, path)
	var ok string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		t.Fatalf("integrity_check of the backup = %q, %v", ok, err)
	}
	if v := userVersion(t, path); v != int64(from) {
		t.Fatalf("backup layout %d, want %d", v, from)
	}
	if events, next, cks := countRows(t, db, "s"); events != 3 || next != 3 || cks != 1 {
		t.Fatalf("the backup holds %d events, next seq %d, %d checkpoints; want 3, 3, 1", events, next, cks)
	}
	for _, p := range []string{path, filepath.Dir(path)} {
		if err := home.CheckPrivate(p); err != nil {
			t.Fatalf("backup: %v", err)
		}
	}
}

// DH-T-STORE-2, M1-G-STORE-1: a home at layout N-1 held open by a re-exec'd
// older instance is not upgraded: Open fails with store/migration-blocked naming
// the child's pid, kind and start time, user_version stays N-1 and no backup is
// taken. Once the child is gone, the next Open upgrades.
//
//dsh:test DH-T-STORE-2
func TestUpgradeRefusedWhileOlderInstanceLives(t *testing.T) {
	path := dbPath(t)
	from := sqlite.LayoutVersion - 1
	atLayout(t, path, from)

	cmd := exec.Command(os.Args[0], "-test.run=^TestMigrateChild$", "-test.count=1", "-test.v")
	cmd.Env = append(childEnviron(), migrateChildEnv+"="+path, migrateCapEnv+"="+strconv.Itoa(from))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &child{t: t, cmd: cmd, stdin: stdin, out: bufio.NewScanner(out)}
	t.Cleanup(func() { stop(cmd) })
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := c.expect("ready pid=")
	pid := strings.TrimPrefix(ready, "ready pid=")
	if pid != strconv.Itoa(cmd.Process.Pid) {
		t.Fatalf("the child reports pid %s, started as %d", pid, cmd.Process.Pid)
	}

	s, err := sqlite.Open(bg, path)
	if err == nil {
		s.Close(bg)
		t.Fatal("Open upgraded the layout under a live older instance")
	}
	if !errors.Is(err, &store.Error{Code: sqlite.CodeMigrationBlocked}) {
		t.Fatalf("Open = %v, want %s", err, sqlite.CodeMigrationBlocked)
	}
	for _, want := range []string{"pid " + pid + " ", "kind older", "started 20"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not name %q", err, want)
		}
	}
	if v := userVersion(t, path); v != int64(from) {
		t.Fatalf("user_version %d after the refusal, want %d", v, from)
	}
	if b := backups(t, path); len(b) != 0 {
		t.Fatalf("the refused upgrade took backups %v", b)
	}

	c.kill()
	s = open(t, path)
	if v := userVersion(t, path); v != int64(sqlite.LayoutVersion) {
		t.Fatalf("user_version %d once the child is gone, want %d", v, sqlite.LayoutVersion)
	}
	if n := countWhere(t, fresh(t, path), `SELECT count(*) FROM hosts WHERE instance <> ?`, s.Instance()); n != 0 {
		t.Fatalf("%d hosts rows of the dead child survived the upgrade", n)
	}
	b := backups(t, path)
	if len(b) != 1 {
		t.Fatalf("backups %v, want one", b)
	}
	checkBackup(t, b[0], from)
}

// DH-T-STORE-2, M1-G-STORE-1: the hosts are probed again inside the upgrade
// transaction, so an instance that registered after the first probe (here
// during the backup) still blocks it, and nothing changes but the kept backup.
//
//dsh:test DH-T-STORE-2
func TestUpgradeProbesAgainInsideItsTransaction(t *testing.T) {
	path := dbPath(t)
	from := sqlite.LayoutVersion - 1
	atLayout(t, path, from)
	const late = "4242-0123456789ab"
	lock, err := fsx.TryLockBeyondEnd(filepath.Join(filepath.Dir(path), "run", late+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Unlock() })
	defer sqlite.SetVacuumInto(func(ctx context.Context, c *sql.Conn, dst string) error {
		if _, err := fresh(t, path).Exec(`INSERT INTO hosts (instance, pid, kind, started_at) VALUES (?, 4242, 'late', 1)`, late); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, `VACUUM INTO ?`, dst)
		return err
	})()
	_, err = sqlite.Open(bg, path)
	if !errors.Is(err, &store.Error{Code: sqlite.CodeMigrationBlocked}) || !strings.Contains(err.Error(), "pid 4242 (kind late") {
		t.Fatalf("Open = %v, want %s naming the late instance", err, sqlite.CodeMigrationBlocked)
	}
	if v := userVersion(t, path); v != int64(from) {
		t.Fatalf("user_version %d, want %d", v, from)
	}
	b := backups(t, path)
	if len(b) != 1 {
		t.Fatalf("backups %v, want the one taken before the second probe", b)
	}
	checkBackup(t, b[0], from)
}

// DH-T-STORE-2, M1-G-STORE-2: every pending layout step runs in one
// transaction. A step that fails rolls back all of them: Open fails with
// store/migration-failed naming the kept backup, the database reads at its
// older layout with every row, and the backup passes integrity_check. Without
// the fault the next Open upgrades and keeps both backups, which Check lists.
//
//dsh:test DH-T-STORE-2
func TestFailedUpgradeRollsBackAndKeepsTheBackup(t *testing.T) {
	n := sqlite.LayoutVersion
	for _, from := range []int{n - 1, n - 2} {
		t.Run(fmt.Sprintf("from layout %d", from), func(t *testing.T) {
			path := dbPath(t)
			atLayout(t, path, from)
			had := hasTable(t, path, "lineage")
			restore := sqlite.SetMigrationFault(n-1, errors.New("injected failure of the last layout step"))
			_, err := sqlite.Open(bg, path)
			restore()
			if !errors.Is(err, &store.Error{Code: sqlite.CodeMigrationFailed}) {
				t.Fatalf("Open = %v, want %s", err, sqlite.CodeMigrationFailed)
			}
			b := backups(t, path)
			if len(b) != 1 {
				t.Fatalf("backups %v, want one", b)
			}
			if !strings.Contains(err.Error(), b[0]) || !strings.Contains(err.Error(), fmt.Sprintf("layout step %d", n)) {
				t.Fatalf("the refusal %q does not name the backup %s and step %d", err, b[0], n)
			}
			if v := userVersion(t, path); v != int64(from) {
				t.Fatalf("user_version %d after the rollback, want %d", v, from)
			}
			// Step n-1 ran before the failing step n and was rolled back with it.
			if hasTable(t, path, "lineage") != had {
				t.Fatal("an earlier step of the failed upgrade was committed")
			}
			if events, next, cks := countRows(t, fresh(t, path), "s"); events != 3 || next != 3 || cks != 1 {
				t.Fatalf("after the rollback: %d events, next seq %d, %d checkpoints; want 3, 3, 1", events, next, cks)
			}
			checkBackup(t, b[0], from)

			s := open(t, path)
			if v := userVersion(t, path); v != int64(n) {
				t.Fatalf("user_version %d after the retry, want %d", v, n)
			}
			if events, next, cks := countRows(t, fresh(t, path), "s"); events != 3 || next != 3 || cks != 1 {
				t.Fatalf("after the upgrade: %d events, next seq %d, %d checkpoints; want 3, 3, 1", events, next, cks)
			}
			var version string
			if err := fresh(t, path).QueryRow(`SELECT binary_version FROM hosts WHERE instance = ?`, s.Instance()).Scan(&version); err != nil || version != s.BinaryVersion() || version == "" {
				t.Fatalf("hosts.binary_version = %q, %v; want %q", version, err, s.BinaryVersion())
			}
			rep, err := s.Check(bg)
			if err != nil || !rep.Clean() {
				t.Fatalf("Check = %+v, %v", rep, err)
			}
			if len(rep.Backups) != 2 || !slicesContain(rep.Backups, b[0]) {
				t.Fatalf("Check lists backups %v; want the failed attempt's %s and the upgrade's", rep.Backups, b[0])
			}
			for _, p := range rep.Backups {
				checkBackup(t, p, from)
			}
		})
	}
}

func slicesContain(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// DH-T-STORE-2, M1-G-STORE-2: a backup that cannot be written (a full disk) or
// that fails its check refuses the open with nothing changed: the database is
// byte-identical at its older layout and no partial copy is left.
//
//dsh:test DH-T-STORE-2
func TestFailedBackupChangesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		want store.Code
		copy func(ctx context.Context, c *sql.Conn, dst string) error
	}{
		{"disk full", sqlite.CodeFull, func(_ context.Context, _ *sql.Conn, dst string) error {
			if err := os.WriteFile(dst, bytes.Repeat([]byte{0xa5}, 4096), 0o600); err != nil {
				return err
			}
			return &store.Error{Code: sqlite.CodeFull, Msg: "injected: database or disk is full"}
		}},
		{"copy fails its check", "", func(_ context.Context, _ *sql.Conn, dst string) error {
			return os.WriteFile(dst, bytes.Repeat([]byte("not a database\n"), 400), 0o600)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := dbPath(t)
			from := sqlite.LayoutVersion - 1
			atLayout(t, path, from)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			restore := sqlite.SetVacuumInto(tc.copy)
			_, err = sqlite.Open(bg, path)
			restore()
			if err == nil || (tc.want != "" && !errors.Is(err, &store.Error{Code: tc.want})) || !strings.Contains(err.Error(), "nothing changed") {
				t.Fatalf("Open = %v, want a refusal (%s) that changed nothing", err, tc.want)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("dsh.db changed although its backup failed")
			}
			if v := userVersion(t, path); v != int64(from) {
				t.Fatalf("user_version %d, want %d", v, from)
			}
			if b := backups(t, path); len(b) != 0 {
				t.Fatalf("the failed backup left %v", b)
			}
		})
	}
}

// DH-T-STORE-2, M1-G-STORE-3: a home where the write-ahead log cannot be
// entered (the journal mode forced to DELETE, as a filesystem without shared
// memory would leave it) is refused with store/no-wal.
//
//dsh:test DH-T-STORE-2
func TestOpenRefusesWithoutWAL(t *testing.T) {
	defer sqlite.SetJournalPragma(`PRAGMA journal_mode = DELETE`)()
	s, err := sqlite.Open(bg, dbPath(t))
	if err == nil {
		s.Close(bg)
		t.Fatal("Open succeeded outside WAL")
	}
	if !errors.Is(err, &store.Error{Code: sqlite.CodeNoWAL}) || !strings.Contains(err.Error(), `"delete"`) {
		t.Fatalf("Open = %v, want %s naming the journal mode", err, sqlite.CodeNoWAL)
	}
}

// DH-T-STORE-2, M1-G-STORE-3 (DH-ADR-STORE-8): dsh.db, its WAL files and run/
// are owner-only. On POSIX the store itself creates them (0600, 0700) in a plain
// directory; on Windows, where privacy rests on the home's access list, the home
// is made by home.Ensure and the files the store creates there pass the same
// DACL check boot applies (home.CheckPrivate).
//
//dsh:test DH-T-STORE-2
func TestStoreFilesAreOwnerOnly(t *testing.T) {
	var path string
	if runtime.GOOS == "windows" {
		path = dbPath(t)
	} else {
		dir := filepath.Join(t.TempDir(), "h")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(dir, "dsh.db")
	}
	s := open(t, path)
	run := filepath.Join(filepath.Dir(path), "run")
	files := []string{path, path + "-wal", path + "-shm", sqlite.LockPath(s, s.Instance())}
	for _, p := range append(files, run) {
		if err := home.CheckPrivate(p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, p := range files {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: mode %v, %v; want 0600", p, info.Mode().Perm(), err)
		}
	}
	if info, err := os.Stat(run); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("run/: mode %v, %v; want 0700", info.Mode().Perm(), err)
	}
}

// The build this binary records in its hosts row: the module version, the VCS
// revision when recorded, ".dirty" for a modified tree.
func TestBuildVersion(t *testing.T) {
	info := func(v string, settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}, Settings: settings}
	}
	for _, tc := range []struct {
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{nil, false, "(unknown)"},
		{info(""), true, "(devel)"},
		{info("v1.2.3"), true, "v1.2.3"},
		{info("(devel)", debug.BuildSetting{Key: "vcs.revision", Value: "abc"}), true, "(devel)+abc"},
		{info("(devel)", debug.BuildSetting{Key: "vcs.revision", Value: "abc"}, debug.BuildSetting{Key: "vcs.modified", Value: "true"}), true, "(devel)+abc.dirty"},
	} {
		if got := sqlite.BuildVersion(tc.info, tc.ok); got != tc.want {
			t.Errorf("buildVersion(%+v, %v) = %q, want %q", tc.info, tc.ok, got, tc.want)
		}
	}
	if sqlite.BinaryVersion() == "" {
		t.Fatal("no binary version")
	}
}
