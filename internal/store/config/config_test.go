package config

import (
	"testing"
)

// TestKeyEmbedModelCode asserts that the constant has the expected string value.
func TestKeyEmbedModelCode(t *testing.T) {
	if KeyEmbedModelCode != "embed_model_code" {
		t.Errorf("KeyEmbedModelCode = %q, want %q", KeyEmbedModelCode, "embed_model_code")
	}
}
