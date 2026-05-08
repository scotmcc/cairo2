package db

import (
	"database/sql"
	"fmt"
)

// ResolveCodeEmbedModel returns the effective embedding model for code indexing
// (indexed_files + indexed_chunks). It reads embed_model_code first; if that
// is unset it falls back to embed_model. Returns an error when neither key is
// configured — the caller should surface a clear message prescribing the fix.
func ResolveCodeEmbedModel(database *DB) (string, error) {
	code, _ := database.Config.Get(KeyEmbedModelCode)
	if code != "" {
		return code, nil
	}
	prose, _ := database.Config.Get(KeyEmbedModel)
	if prose != "" {
		return prose, nil
	}
	return "", fmt.Errorf("embed_model_code or embed_model not configured — run: cairo config set embed_model_code <model> (or embed_model)")
}

type ConfigQ struct{ db *sql.DB }

func (q *ConfigQ) Get(key string) (string, error) {
	var val string
	err := q.db.QueryRow(`SELECT value FROM config WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func (q *ConfigQ) GetWithDefault(key, defaultValue string) string {
	val, _ := q.Get(key)
	if val == "" {
		return defaultValue
	}
	return val
}

func (q *ConfigQ) GetRequired(key string) (string, error) {
	val, err := q.Get(key)
	if err != nil {
		return "", err
	}
	if val == "" {
		return "", fmt.Errorf("required config key %q is not set", key)
	}
	return val, nil
}

func (q *ConfigQ) Set(key, value string) error {
	_, err := q.db.Exec(`
		INSERT INTO config(key, value, updated_at) VALUES(?, ?, unixepoch())
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = unixepoch()`,
		key, value)
	return err
}

func (q *ConfigQ) All() (map[string]string, error) {
	rows, err := q.db.Query(`SELECT key, value FROM config ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}
