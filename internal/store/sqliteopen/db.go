package sqliteopen

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/index"
	"github.com/scotmcc/cairo2/internal/store/jobs"
	"github.com/scotmcc/cairo2/internal/store/memory"
	"github.com/scotmcc/cairo2/internal/store/schema"
	"github.com/scotmcc/cairo2/internal/store/sessions"
)

// DB wraps sql.DB and owns all query methods via embedded sub-types.
type DB struct {
	sql *sql.DB

	Config              *config.ConfigQ
	Sessions            *sessions.SessionQ
	Messages            *sessions.MessageQ
	Memories            *memory.MemoryQ
	Roles               *identity.RoleQ
	Prompts             *identity.PromptQ
	Tools               *identity.CustomToolQ
	Skills              *identity.SkillQ
	Jobs                *jobs.JobQ
	Worktrees           *jobs.WorktreeQ
	Tasks               *jobs.TaskQ
	TaskArtifacts       *jobs.TaskArtifactQ
	Summaries           *memory.SummaryQ
	Facts               *memory.FactQ
	Hooks               *identity.HookQ
	Dreams              *memory.DreamQ
	DreamLog            *memory.DreamLogQ
	Projects            *index.ProjectQ
	IndexedFiles        *index.IndexedFileQ
	Chunks              *index.ChunkQ
	ConsiderAspects     *identity.ConsiderAspectQ
	ConsiderActivations *identity.ConsiderActivationQ
	State               *identity.StateQ
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

	if err := schema.ExecSchema(sqldb); err != nil {
		sqldb.Close()
		return nil, err
	}
	// Back up the DB before applying any pending migrations.
	if hasPending, err := schema.HasPendingMigrations(sqldb); err == nil && hasPending {
		if err := backupBeforeMigrations(path); err != nil {
			// Non-fatal: log the failure but don't block the open.
			fmt.Fprintf(os.Stderr, "db: backup before migration failed: %v\n", err)
		}
	}
	if err := schema.ApplyMigrations(sqldb); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	db := &DB{sql: sqldb}
	db.Config = config.NewConfigQ(sqldb)
	db.Sessions = sessions.NewSessionQ(sqldb)
	db.Messages = sessions.NewMessageQ(sqldb)
	db.Memories = memory.NewMemoryQ(sqldb)
	db.Roles = identity.NewRoleQ(sqldb)
	db.Prompts = identity.NewPromptQ(sqldb)
	db.Tools = identity.NewCustomToolQ(sqldb)
	db.Skills = identity.NewSkillQ(sqldb)
	db.Jobs = jobs.NewJobQ(sqldb)
	db.Worktrees = jobs.NewWorktreeQ(sqldb)
	db.Tasks = jobs.NewTaskQ(sqldb)
	db.TaskArtifacts = jobs.NewTaskArtifactQ(sqldb)
	db.Summaries = memory.NewSummaryQ(sqldb)
	db.Facts = memory.NewFactQ(sqldb)
	db.Hooks = identity.NewHookQ(sqldb)
	db.Dreams = memory.NewDreamQ(sqldb)
	db.DreamLog = memory.NewDreamLogQ(sqldb)
	db.Projects = index.NewProjectQ(sqldb)
	db.IndexedFiles = index.NewIndexedFileQ(sqldb)
	db.Chunks = index.NewChunkQ(sqldb)
	db.ConsiderAspects = identity.NewConsiderAspectQ(sqldb)
	db.ConsiderActivations = identity.NewConsiderActivationQ(sqldb)
	db.State = identity.NewStateQ(sqldb)
	db.Registrations = &RegistrationQ{db: sqldb}

	if err := db.seed(); err != nil {
		sqldb.Close()
		return nil, err
	}

	// Sweep any tasks that were "running" when cairo last crashed —
	// their PID is no longer alive, so mark them failed now instead of
	// leaving job_list reporting them as in-flight forever.
	// Errors are logged-and-ignored: a reap failure shouldn't block startup.
	if _, err := jobs.ReapOrphanedTasks(db.SQL(), db.Tasks, db.Jobs); err != nil {
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
