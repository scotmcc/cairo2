package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps sql.DB and owns all query methods via embedded sub-types.
type DB struct {
	sql *sql.DB

	Config              *ConfigQ
	Sessions            *SessionQ
	Messages            *MessageQ
	Memories            *MemoryQ
	Roles               *RoleQ
	Prompts             *PromptQ
	Tools               *CustomToolQ
	Skills              *SkillQ
	Jobs                *JobQ
	Worktrees           *WorktreeQ
	Tasks               *TaskQ
	TaskArtifacts       *TaskArtifactQ
	Summaries           *SummaryQ
	Facts               *FactQ
	Hooks               *HookQ
	Dreams              *DreamQ
	DreamLog            *DreamLogQ
	Projects            *ProjectQ
	IndexedFiles        *IndexedFileQ
	Chunks              *ChunkQ
	ConsiderAspects     *ConsiderAspectQ
	ConsiderActivations *ConsiderActivationQ
	State               *StateQ
	Registrations       *RegistrationQ
}

// Open opens (or creates) the cairo database at ~/.cairo2/cairo.db.
// It's a thin wrapper over OpenAt that resolves the default production path;
// tests call OpenAt directly with a tempdir path to stay isolated.
func Open() (*DB, error) {
	dir := DefaultDataDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return OpenAt(filepath.Join(dir, "cairo.db"))
}

// OpenAt opens (or creates) a cairo DB at the given path.
func OpenAt(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	sqldb.SetMaxOpenConns(1)

	// Wait up to 15s for the write lock before giving up.
	// Prevents SQLITE_BUSY when multiple subprocesses open the DB at the same time.
	if _, err := sqldb.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMs)); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	// Belt-and-suspenders: the _foreign_keys=on DSN parameter has turned out
	// to be unreliable with modernc.org/sqlite in some code paths (discovered
	// while writing export strip-history). Set it explicitly on the pinned
	// single connection so cascade deletes fire.
	if _, err := sqldb.Exec("PRAGMA foreign_keys = ON"); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}

	if err := execSchema(sqldb, schema); err != nil {
		sqldb.Close()
		return nil, err
	}
	// Back up the DB before applying any pending migrations.
	if hasPending, err := hasPendingMigrations(sqldb); err == nil && hasPending {
		if err := backupBeforeMigrations(path); err != nil {
			// Non-fatal: log the failure but don't block the open.
			fmt.Fprintf(os.Stderr, "db: backup before migration failed: %v\n", err)
		}
	}
	if err := applyMigrations(sqldb); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	db := &DB{sql: sqldb}
	db.Config = &ConfigQ{db: sqldb}
	db.Sessions = &SessionQ{db: sqldb}
	db.Messages = &MessageQ{db: sqldb}
	db.Memories = &MemoryQ{db: sqldb}
	db.Roles = &RoleQ{db: sqldb}
	db.Prompts = &PromptQ{db: sqldb}
	db.Tools = &CustomToolQ{db: sqldb}
	db.Skills = &SkillQ{db: sqldb}
	db.Jobs = &JobQ{db: sqldb}
	db.Worktrees = &WorktreeQ{db: sqldb}
	db.Tasks = &TaskQ{db: sqldb}
	db.TaskArtifacts = &TaskArtifactQ{db: sqldb}
	db.Summaries = &SummaryQ{db: sqldb}
	db.Facts = &FactQ{db: sqldb}
	db.Hooks = &HookQ{db: sqldb}
	db.Dreams = &DreamQ{db: sqldb}
	db.DreamLog = &DreamLogQ{db: sqldb}
	db.Projects = &ProjectQ{db: sqldb}
	db.IndexedFiles = &IndexedFileQ{db: sqldb}
	db.Chunks = &ChunkQ{db: sqldb}
	db.ConsiderAspects = &ConsiderAspectQ{db: sqldb}
	db.ConsiderActivations = &ConsiderActivationQ{db: sqldb}
	db.State = &StateQ{db: sqldb}
	db.Registrations = &RegistrationQ{db: sqldb}

	if err := db.seed(); err != nil {
		sqldb.Close()
		return nil, err
	}

	// Sweep any tasks that were "running" when cairo last crashed —
	// their PID is no longer alive, so mark them failed now instead of
	// leaving job_list reporting them as in-flight forever.
	// Errors are logged-and-ignored: a reap failure shouldn't block startup.
	if _, err := db.ReapOrphanedTasks(); err != nil {
		fmt.Fprintf(os.Stderr, "reap: %v\n", err)
	}

	return db, nil
}

func (db *DB) Close() error { return db.sql.Close() }

// SQL exposes the underlying *sql.DB for callers (e.g. tests, audits) that
// need to issue ad-hoc queries the typed wrappers don't surface.
func (db *DB) SQL() *sql.DB { return db.sql }

// WithTx runs fn inside a transaction. Rolls back on error or panic.
func (db *DB) WithTx(fn func(*sql.Tx) error) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// DistinctEmbedModels returns the distinct non-empty embed_model values
// present in the named table. Used at startup to detect cross-model mismatch.
// Only call with trusted table names (constants).
func (db *DB) DistinctEmbedModels(table string) ([]string, error) {
	//nolint:gosec // table is a trusted constant, not user input
	rows, err := db.sql.Query(
		`SELECT DISTINCT embed_model FROM ` + table + ` WHERE embed_model != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func execSchema(sqldb *sql.DB, s string) error {
	for _, stmt := range strings.Split(s, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := sqldb.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func applyMigrations(sqldb *sql.DB) error {
	// Read current schema version. user_version starts at 0 on a new DB,
	// meaning no migrations have been applied yet.
	var version int
	if err := sqldb.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	for i, m := range migrations {
		if i < version {
			continue // already applied
		}
		if _, err := sqldb.Exec(m); err != nil {
			return fmt.Errorf("migration %d: %w", i, err)
		}
		// Bump user_version immediately after each successful migration so a
		// crash mid-run leaves the DB in a consistent, resumable state.
		// PRAGMA user_version does not support query parameters.
		if _, err := sqldb.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return fmt.Errorf("bump user_version to %d: %w", i+1, err)
		}
	}
	return nil
}

// hasPendingMigrations returns true when there are migrations not yet applied
// to sqldb. Used to decide whether a pre-migration backup is worthwhile.
func hasPendingMigrations(sqldb *sql.DB) (bool, error) {
	var version int
	if err := sqldb.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return false, err
	}
	return version < len(migrations), nil
}

// backupBeforeMigrations takes a VACUUM INTO snapshot of the database at
// dbPath into ~/.cairo/backups/, naming it cairo_premigration_<timestamp>.db.
// Keeps only the 5 most recent backups. Returns nil without doing anything
// when dbPath is outside the cairo data directory (e.g. test temp dirs).
func backupBeforeMigrations(dbPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	dataDir := DefaultDataDir()
	absDB, err := filepath.Abs(dbPath)
	if err == nil {
		absData, err2 := filepath.Abs(dataDir)
		if err2 == nil && !strings.HasPrefix(absDB, absData+string(filepath.Separator)) {
			return nil
		}
	}
	backupDir := filepath.Join(home, DataDirName, "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	ts := time.Now().UTC().Format("20060102T150405Z")
	dest := filepath.Join(backupDir, fmt.Sprintf("cairo_premigration_%s.db", ts))

	// Open a throwaway connection to run VACUUM INTO — we can't reuse the
	// caller's connection because VACUUM INTO requires no other transaction.
	tmp, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return fmt.Errorf("open for backup: %w", err)
	}
	defer tmp.Close()
	if _, err := tmp.Exec(fmt.Sprintf("VACUUM INTO '%s'", dest)); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}

	// Trim to 5 most-recent backups.
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil // non-fatal; backup already succeeded
	}
	var backups []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "cairo_premigration_") && strings.HasSuffix(e.Name(), ".db") {
			backups = append(backups, filepath.Join(backupDir, e.Name()))
		}
	}
	sort.Strings(backups) // lexicographic == chronological for the timestamp format
	for len(backups) > 5 {
		_ = os.Remove(backups[0])
		backups = backups[1:]
	}
	return nil
}

// seed inserts default data on first run (idempotent).
func (db *DB) seed() error {
	return db.seedDefaults()
}
