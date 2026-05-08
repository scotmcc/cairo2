package testdb

import (
	"path/filepath"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

func OpenTestDB(t *testing.T) *sqliteopen.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := sqliteopen.OpenAt(path)
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}
