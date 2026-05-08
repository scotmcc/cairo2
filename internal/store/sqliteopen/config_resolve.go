package sqliteopen

import (
	"fmt"

	"github.com/scotmcc/cairo2/internal/store/config"
)

// ResolveCodeEmbedModel returns the effective embedding model for code indexing
// (indexed_files + indexed_chunks). It reads embed_model_code first; if that
// is unset it falls back to embed_model. Returns an error when neither key is
// configured — the caller should surface a clear message prescribing the fix.
func ResolveCodeEmbedModel(database *DB) (string, error) {
	code, _ := database.Config.Get(config.KeyEmbedModelCode)
	if code != "" {
		return code, nil
	}
	prose, _ := database.Config.Get(config.KeyEmbedModel)
	if prose != "" {
		return prose, nil
	}
	return "", fmt.Errorf("embed_model_code or embed_model not configured — run: cairo config set embed_model_code <model> (or embed_model)")
}
