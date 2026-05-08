package db

import (
	"testing"
)

func TestResolveModelWithExplicit_EmptyExplicitFallsThroughToRole(t *testing.T) {
	db := openTest(t)

	// Set a model on the "assistant" role.
	if err := db.Roles.Upsert("assistant", "assistant", "role-model", "", ""); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := ResolveModelWithExplicit(db, "", "assistant", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "role-model"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveModelWithExplicit_NonEmptyExplicitWinsOverRole(t *testing.T) {
	db := openTest(t)

	if err := db.Roles.Upsert("assistant", "assistant", "role-model", "", ""); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := ResolveModelWithExplicit(db, "explicit-model", "assistant", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "explicit-model"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveModelWithExplicit_EmptyExplicitPlusEmptyConfigFallsThroughToFallback(t *testing.T) {
	db := openTest(t)

	// Clear the seeded config.model so ResolveModel falls through to fallback.
	if _, err := db.sql.Exec("DELETE FROM config WHERE key = ?", KeyModel); err != nil {
		t.Fatalf("clear config: %v", err)
	}

	got, err := ResolveModelWithExplicit(db, "", "", "fallback-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "fallback-model"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveModelWithExplicit_NoModelAnywhereReturnsError(t *testing.T) {
	db := openTest(t)

	// Clear the seeded config.model so there's no model anywhere.
	if _, err := db.sql.Exec("DELETE FROM config WHERE key = ?", KeyModel); err != nil {
		t.Fatalf("clear config: %v", err)
	}

	_, err := ResolveModelWithExplicit(db, "", "", "")
	if err == nil {
		t.Fatal("expected error when no model is configured anywhere")
	}
}
