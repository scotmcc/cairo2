package sqliteopen_test

import (
	"testing"

	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

func TestResolveModelWithExplicit_EmptyExplicitFallsThroughToRole(t *testing.T) {
	db := testdb.OpenTestDB(t)

	// Set a model on the "assistant" role.
	if err := db.Roles.Upsert("assistant", "assistant", "role-model", "", ""); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := sqliteopen.ResolveModelWithExplicit(db, "", "assistant", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "role-model"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveModelWithExplicit_NonEmptyExplicitWinsOverRole(t *testing.T) {
	db := testdb.OpenTestDB(t)

	if err := db.Roles.Upsert("assistant", "assistant", "role-model", "", ""); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := sqliteopen.ResolveModelWithExplicit(db, "explicit-model", "assistant", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "explicit-model"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveModelWithExplicit_EmptyExplicitPlusEmptyConfigFallsThroughToFallback(t *testing.T) {
	db := testdb.OpenTestDB(t)

	// Clear the seeded config.model so ResolveModel falls through to fallback.
	if _, err := db.SQL().Exec("DELETE FROM config WHERE key = ?", config.KeyModel); err != nil {
		t.Fatalf("clear config: %v", err)
	}

	got, err := sqliteopen.ResolveModelWithExplicit(db, "", "", "fallback-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "fallback-model"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveModelWithExplicit_NoModelAnywhereReturnsError(t *testing.T) {
	db := testdb.OpenTestDB(t)

	// Clear the seeded config.model so there's no model anywhere.
	if _, err := db.SQL().Exec("DELETE FROM config WHERE key = ?", config.KeyModel); err != nil {
		t.Fatalf("clear config: %v", err)
	}

	_, err := sqliteopen.ResolveModelWithExplicit(db, "", "", "")
	if err == nil {
		t.Fatal("expected error when no model is configured anywhere")
	}
}
