package sqliteopen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

func TestRegistrationQ(t *testing.T) {
	dir := t.TempDir()
	database, err := sqliteopen.OpenAt(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	defer os.Remove(filepath.Join(dir, "test.db"))

	q := database.Registrations

	t.Run("get missing returns empty", func(t *testing.T) {
		got, err := q.Get("http://localhost:8080")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})

	t.Run("upsert then get", func(t *testing.T) {
		if err := q.Upsert("http://localhost:8080", "agent-abc"); err != nil {
			t.Fatal(err)
		}
		got, err := q.Get("http://localhost:8080")
		if err != nil {
			t.Fatal(err)
		}
		if got != "agent-abc" {
			t.Fatalf("expected agent-abc, got %q", got)
		}
	})

	t.Run("upsert overwrites", func(t *testing.T) {
		if err := q.Upsert("http://localhost:8080", "agent-xyz"); err != nil {
			t.Fatal(err)
		}
		got, err := q.Get("http://localhost:8080")
		if err != nil {
			t.Fatal(err)
		}
		if got != "agent-xyz" {
			t.Fatalf("expected agent-xyz, got %q", got)
		}
	})
}
