package sqliteopen_test

import (
	"testing"

	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

// TestResolveCodeEmbedModel_FallsBackToEmbedModel verifies that when
// embed_model_code is unset, the fallback returns the embed_model value.
func TestResolveCodeEmbedModel_FallsBackToEmbedModel(t *testing.T) {
	db := testdb.OpenTestDB(t)

	if _, err := db.SQL().Exec("DELETE FROM config WHERE key = ?", config.KeyEmbedModelCode); err != nil {
		t.Fatalf("delete embed_model_code: %v", err)
	}
	if err := db.Config.Set(config.KeyEmbedModel, "nomic-embed-text"); err != nil {
		t.Fatalf("set embed_model: %v", err)
	}

	got, err := sqliteopen.ResolveCodeEmbedModel(db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "nomic-embed-text" {
		t.Errorf("got %q, want %q", got, "nomic-embed-text")
	}
}

// TestResolveCodeEmbedModel_PrefersCodeKey verifies that embed_model_code
// wins over embed_model when set.
func TestResolveCodeEmbedModel_PrefersCodeKey(t *testing.T) {
	db := testdb.OpenTestDB(t)

	if err := db.Config.Set(config.KeyEmbedModel, "nomic-embed-text"); err != nil {
		t.Fatalf("set embed_model: %v", err)
	}
	if err := db.Config.Set(config.KeyEmbedModelCode, "nomic-embed-code:7b-q8_0"); err != nil {
		t.Fatalf("set embed_model_code: %v", err)
	}

	got, err := sqliteopen.ResolveCodeEmbedModel(db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "nomic-embed-code:7b-q8_0" {
		t.Errorf("got %q, want %q", got, "nomic-embed-code:7b-q8_0")
	}
}

// TestResolveCodeEmbedModel_ErrorWhenBothUnset verifies that an error is
// returned when neither embed_model_code nor embed_model is configured.
func TestResolveCodeEmbedModel_ErrorWhenBothUnset(t *testing.T) {
	db := testdb.OpenTestDB(t)

	if _, err := db.SQL().Exec("DELETE FROM config WHERE key IN (?, ?)", config.KeyEmbedModel, config.KeyEmbedModelCode); err != nil {
		t.Fatalf("delete embed keys: %v", err)
	}

	_, err := sqliteopen.ResolveCodeEmbedModel(db)
	if err == nil {
		t.Fatal("expected error when neither key is set")
	}
}
