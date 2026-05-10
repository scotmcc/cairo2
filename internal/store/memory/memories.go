package memory

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"regexp"
	"strings"
	"time"
)

var wsRe = regexp.MustCompile(`\s+`)

func normalizeContent(s string) string {
	return wsRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), " ")
}

func contentHash(s string) string {
	h := sha256.Sum256([]byte(normalizeContent(s)))
	return hex.EncodeToString(h[:])
}

type Memory struct {
	ID              int64
	Content         string
	Tags            string // JSON array
	Embedding       []float32
	EmbedModel      string
	Importance      float64
	Weight          float64
	LastRetrievedAt *int64 // nil = never retrieved; non-nil = unix timestamp
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *int64     // nil = not deleted; non-nil = unix timestamp of soft-delete
	PinnedAt        *time.Time // nil = not pinned; non-nil = when pinned
	ArchivedAt      *time.Time // nil = not archived; non-nil = when archived (curator lifecycle)
	ReviewedAt      *time.Time // nil = unreviewed; non-nil = when last reviewed by dream-pass curator
}

type MemoryQ struct{ db *sql.DB }

func NewMemoryQ(db *sql.DB) *MemoryQ { return &MemoryQ{db: db} }

// findByHash returns an existing memory with matching content_hash, embedding bytes,
// and embed_model within the recency window. All three must match: same normalized
// content + same model + same embedding bytes = genuine duplicate in production.
// Tests with synthetic embeddings (different bytes, same content) correctly bypass dedup.
func (q *MemoryQ) findByHash(hash string, encodedEmb []byte, embedModel string, window time.Duration) (*Memory, error) {
	cutoff := time.Now().Add(-window).Unix()
	var id int64
	var existingEmb []byte
	var existingModel string
	err := q.db.QueryRow(
		`SELECT id, embedding, embed_model FROM memories WHERE content_hash = ? AND deleted_at IS NULL AND created_at > ? ORDER BY created_at DESC LIMIT 1`,
		hash, cutoff,
	).Scan(&id, &existingEmb, &existingModel)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if existingModel != embedModel || !bytes.Equal(existingEmb, encodedEmb) {
		return nil, nil
	}
	return q.Get(id)
}

func (q *MemoryQ) Add(content, tags, embedModel string, embedding []float32) (*Memory, error) {
	hash := contentHash(content)
	encodedEmb := encodeEmbedding(embedding)
	existing, err := q.findByHash(hash, encodedEmb, embedModel, 7*24*time.Hour)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		log.Printf("memories.Add: deduped (hash=%s, id=%d)", hash[:8], existing.ID)
		return existing, nil
	}
	res, err := q.db.Exec(
		// importance=0.5 at insert (matches the schema default and acts as the
		// "unrated" sentinel — Unrated() picks these up on the dream pass for
		// LLM rating). 0.5 keeps the memory findable in retrieval until rated;
		// importance=0 would make decayImportance() return 0 (invisible).
		`INSERT INTO memories(content, tags, embed_model, embedding, content_hash, importance) VALUES(?, ?, ?, ?, ?, 0.5)`,
		content, tags, embedModel, encodedEmb, hash,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.Get(id)
}

// Get returns the memory by id, filtering out soft-deleted (deleted_at) and
// dream-pass-archived (archived_at) rows. Callers that need archived rows
// should use raw SQL — Get's contract is "active row by id."
func (q *MemoryQ) Get(id int64) (*Memory, error) {
	row := q.db.QueryRow(
		`SELECT id, content, tags, embed_model, embedding, importance, weight, last_retrieved_at, created_at, updated_at, deleted_at, pinned_at, archived_at, reviewed_at
		 FROM memories WHERE id = ? AND deleted_at IS NULL AND archived_at IS NULL`, id)
	return scanMemory(row)
}

// RecentContent returns just the content strings for the N most recent memories.
// Skips embedding BLOB decoding entirely — the prompt builder only needs content,
// and decoding every embedding on every turn was a hot-path cost flagged in review.
func (q *MemoryQ) RecentContent(limit int) ([]string, error) {
	rows, err := q.db.Query(
		`SELECT content FROM memories WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Recent returns the N most recently updated memories without embedding BLOBs.
// Used for quick display (command palette initial load, status summaries) where
// cosine search is not needed and decoding embeddings would be wasteful.
func (q *MemoryQ) Recent(n int) ([]*Memory, error) {
	rows, err := q.db.Query(
		`SELECT id, content, tags, embed_model, NULL as embedding, importance, weight, last_retrieved_at, created_at, updated_at, deleted_at, pinned_at, archived_at, reviewed_at
		 FROM memories WHERE deleted_at IS NULL ORDER BY updated_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Count returns the total number of stored memories.
func (q *MemoryQ) Count() (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL`).Scan(&n)
	return n, err
}

// CountPinned returns the number of pinned, non-deleted memories.
func (q *MemoryQ) CountPinned() (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE pinned_at IS NOT NULL AND deleted_at IS NULL`).Scan(&n)
	return n, err
}

// DimBreakdown reports how many stored memories exist at each embedding
// dimension, derived cheaply from blob byte length (float32 => 4 bytes).
// Useful for diagnosing a silent embed_model swap — if the map has more
// than one non-zero key, cross-dim cosine won't work.
func (q *MemoryQ) DimBreakdown() (map[int]int, error) {
	rows, err := q.db.Query(
		`SELECT COALESCE(length(embedding),0)/4 AS dim, COUNT(*)
		 FROM memories WHERE deleted_at IS NULL GROUP BY dim`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]int)
	for rows.Next() {
		var dim, count int
		if err := rows.Scan(&dim, &count); err != nil {
			return nil, err
		}
		out[dim] = count
	}
	return out, rows.Err()
}

// AllContent returns all memories with content and metadata but without
// embedding BLOBs — lighter than All() for listing and display purposes.
func (q *MemoryQ) AllContent() ([]*Memory, error) {
	rows, err := q.db.Query(
		`SELECT id, content, tags, embed_model, NULL as embedding, importance, weight, last_retrieved_at, created_at, updated_at, deleted_at, pinned_at, archived_at, reviewed_at
		 FROM memories WHERE deleted_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (q *MemoryQ) All() ([]*Memory, error) {
	rows, err := q.db.Query(
		`SELECT id, content, tags, embed_model, embedding, importance, weight, last_retrieved_at, created_at, updated_at, deleted_at, pinned_at, archived_at, reviewed_at
		 FROM memories WHERE deleted_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Delete soft-deletes a memory by setting deleted_at to the current unix
// timestamp. The row is retained for audit and multi-machine sync; all
// read/list/search methods filter it out via WHERE deleted_at IS NULL.
func (q *MemoryQ) Delete(id int64) error {
	_, err := q.db.Exec(`UPDATE memories SET deleted_at = unixepoch() WHERE id = ?`, id)
	return err
}

// Undelete reverses a soft-delete by clearing deleted_at. Useful for "undo
// memory deletion" tooling; safe to call on a row that was never deleted.
func (q *MemoryQ) Undelete(id int64) error {
	_, err := q.db.Exec(`UPDATE memories SET deleted_at = NULL WHERE id = ?`, id)
	return err
}

// UpdateContentOnly updates a memory's content without refreshing the embedding.
// The stale embedding will cause cosine search to diverge from the new content —
// use UpdateWithEmbedding unless you have a deliberate reason to skip re-embedding.
func (q *MemoryQ) UpdateContentOnly(id int64, content string) error {
	_, err := q.db.Exec(`UPDATE memories SET content = ?, updated_at = unixepoch() WHERE id = ?`, content, id)
	return err
}

func (q *MemoryQ) UpdateWithEmbedding(id int64, content, embedModel string, embedding []float32) error {
	_, err := q.db.Exec(
		`UPDATE memories SET content = ?, embed_model = ?, embedding = ?, updated_at = unixepoch() WHERE id = ?`,
		content, embedModel, encodeEmbedding(embedding), id)
	return err
}

// Search returns the top-k memories by cosine similarity to the query embedding.
// Skips rows whose embed_model differs from queryModel — cross-model cosine
// distances are meaningless and silently wrong.
// Pure Go — no CGO required. Fine for hundreds to low-thousands of memories.
func (q *MemoryQ) Search(query []float32, queryModel string, k int) ([]*Memory, error) {
	all, err := q.All()
	if err != nil {
		return nil, err
	}

	type scored struct {
		m     *Memory
		score float32
	}
	var candidates []scored
	var skippedModel int
	for _, m := range all {
		if len(m.Embedding) == 0 {
			continue
		}
		if m.EmbedModel != queryModel {
			skippedModel++
			continue
		}
		if len(m.Embedding) != len(query) {
			skippedModel++
			continue
		}
		candidates = append(candidates, scored{m, cosine(query, m.Embedding) * float32(decayImportance(m.Importance, m.UpdatedAt))})
	}
	if skippedModel > 0 {
		log.Printf("memory_search: skipped %d rows with different embed_model (query model: %q)", skippedModel, queryModel)
	}

	scoredEmbeddings := make([]ScoredEmbedding, len(candidates))
	for i, c := range candidates {
		scoredEmbeddings[i] = ScoredEmbedding{Score: c.score, Embedding: c.m.Embedding, Index: i}
	}
	selected := MMR(scoredEmbeddings, k, 0.7, 0.92)
	out := make([]*Memory, len(selected))
	for i, idx := range selected {
		out[i] = candidates[idx].m
	}
	return out, nil
}

// SearchFTS returns memories matching the FTS5 query, ordered by rank.
// query supports FTS5 syntax: phrases ("exact phrase"), AND/OR, prefix (word*), etc.
func (q *MemoryQ) SearchFTS(query string, limit int) ([]*Memory, error) {
	rows, err := q.db.Query(
		`SELECT m.id, m.content, m.tags, m.embed_model, m.embedding, m.importance, m.weight, m.last_retrieved_at, m.created_at, m.updated_at, m.deleted_at, m.pinned_at, m.archived_at, m.reviewed_at
		 FROM memories m
		 JOIN memories_fts fts ON m.id = fts.rowid
		 WHERE memories_fts MATCH ? AND m.deleted_at IS NULL
		 ORDER BY rank
		 LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanMemory(row scanner) (*Memory, error) {
	var m Memory
	var createdAt, updatedAt int64
	var embBlob []byte
	var pinnedAt, archivedAt, reviewedAt sql.NullString
	err := row.Scan(&m.ID, &m.Content, &m.Tags, &m.EmbedModel, &embBlob, &m.Importance, &m.Weight, &m.LastRetrievedAt, &createdAt, &updatedAt, &m.DeletedAt, &pinnedAt, &archivedAt, &reviewedAt)
	if err != nil {
		return nil, err
	}
	m.Embedding = decodeEmbedding(embBlob)
	m.CreatedAt = time.Unix(createdAt, 0)
	m.UpdatedAt = time.Unix(updatedAt, 0)
	if pinnedAt.Valid {
		m.PinnedAt = parseTimestamp(pinnedAt.String)
	}
	if archivedAt.Valid {
		m.ArchivedAt = parseTimestamp(archivedAt.String)
	}
	if reviewedAt.Valid {
		m.ReviewedAt = parseTimestamp(reviewedAt.String)
	}
	return &m, nil
}

// BumpRetrieval increments weight by 0.001 and stamps last_retrieved_at for
// the given memory IDs. Called after every explicit memory_tool search hit.
// NOT called from BuildSystemPrompt's top-N injection — that path runs every
// turn and would cause runaway weight inflation.
func (q *MemoryQ) BumpRetrieval(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		`UPDATE memories SET weight = weight + 0.001, last_retrieved_at = unixepoch() WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)
	_, err := q.db.Exec(query, args...)
	return err
}

// RunNightlyDecay decays weight by 0.001 for memories not retrieved in the
// past 24h, soft-deletes any whose weight reaches 0, and auto-promotes any
// whose weight >= 1.0 to importance=1.0. Returns (decayedCount, dumpedCount,
// promotedCount, error).
// Weight is a lifecycle signal only — it is NOT used in retrieval scoring.
// Auto-promote is the one-way bridge from weight to importance.
func (q *MemoryQ) RunNightlyDecay() (int, int, int, error) {
	decayRes, err := q.db.Exec(
		`UPDATE memories
		 SET weight = MAX(0.0, weight - 0.001)
		 WHERE (last_retrieved_at IS NULL OR last_retrieved_at < unixepoch() - 86400)
		   AND deleted_at IS NULL`,
	)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("decay update: %w", err)
	}
	decayed, err := decayRes.RowsAffected()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("decay rows affected: %w", err)
	}

	dumpRes, err := q.db.Exec(
		`UPDATE memories SET deleted_at = unixepoch()
		 WHERE weight <= 0.0 AND deleted_at IS NULL AND pinned_at IS NULL`,
	)
	if err != nil {
		return int(decayed), 0, 0, fmt.Errorf("dump update: %w", err)
	}
	dumped, err := dumpRes.RowsAffected()
	if err != nil {
		return int(decayed), 0, 0, fmt.Errorf("dump rows affected: %w", err)
	}

	promoteRes, err := q.db.Exec(
		`UPDATE memories SET importance = 1.0
		 WHERE weight >= 1.0 AND importance < 1.0 AND deleted_at IS NULL`,
	)
	if err != nil {
		return int(decayed), int(dumped), 0, fmt.Errorf("promote update: %w", err)
	}
	promoted, err := promoteRes.RowsAffected()
	if err != nil {
		return int(decayed), int(dumped), 0, fmt.Errorf("promote rows affected: %w", err)
	}

	return int(decayed), int(dumped), int(promoted), nil
}

func (q *MemoryQ) UpdateEmbeddingOnly(id int64, embedModel string, embedding []float32) error {
	_, err := q.db.Exec(
		`UPDATE memories SET embed_model=?, embedding=?, updated_at=unixepoch() WHERE id=?`,
		embedModel, encodeEmbedding(embedding), id)
	return err
}

func (q *MemoryQ) Pin(id int64) error {
	_, err := q.db.Exec(`UPDATE memories SET pinned_at = datetime('now') WHERE id = ?`, id)
	return err
}

func (q *MemoryQ) Unpin(id int64) error {
	_, err := q.db.Exec(`UPDATE memories SET pinned_at = NULL WHERE id = ?`, id)
	return err
}

func (q *MemoryQ) ListPinned() ([]*Memory, error) {
	rows, err := q.db.Query(
		`SELECT id, content, tags, embed_model, NULL as embedding, importance, weight, last_retrieved_at, created_at, updated_at, deleted_at, pinned_at, archived_at, reviewed_at
		 FROM memories WHERE pinned_at IS NOT NULL AND deleted_at IS NULL ORDER BY pinned_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (q *MemoryQ) Unreviewed() ([]*Memory, error) {
	rows, err := q.db.Query(
		`SELECT id, content, tags, embed_model, NULL as embedding, importance, weight, last_retrieved_at, created_at, updated_at, deleted_at, pinned_at, archived_at, reviewed_at
		 FROM memories WHERE reviewed_at IS NULL AND deleted_at IS NULL AND archived_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UnreviewedWithEmbeddings is like Unreviewed, but loads the embedding BLOB.
// The dream-pass curator needs the embedding to do pairwise cosine; everyone
// else should use Unreviewed for the lighter shape.
func (q *MemoryQ) UnreviewedWithEmbeddings() ([]*Memory, error) {
	rows, err := q.db.Query(
		`SELECT id, content, tags, embed_model, embedding, importance, weight, last_retrieved_at, created_at, updated_at, deleted_at, pinned_at, archived_at, reviewed_at
		 FROM memories WHERE reviewed_at IS NULL AND deleted_at IS NULL AND archived_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Unrated returns memories with importance=0.5 (the unrated sentinel — both
// archived, ordered oldest-first (rate in creation order).
func (q *MemoryQ) Unrated(limit int) ([]*Memory, error) {
	rows, err := q.db.Query(
		`SELECT id, content, tags, embed_model, NULL as embedding, importance, weight, last_retrieved_at, created_at, updated_at, deleted_at, pinned_at, archived_at, reviewed_at
		 FROM memories WHERE importance = 0.5 AND deleted_at IS NULL AND archived_at IS NULL
		 ORDER BY created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (q *MemoryQ) MarkReviewed(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		`UPDATE memories SET reviewed_at = datetime('now') WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)
	_, err := q.db.Exec(query, args...)
	return err
}

func (q *MemoryQ) SetArchivedAt(id int64, t *time.Time) error {
	if t == nil {
		_, err := q.db.Exec(`UPDATE memories SET archived_at = NULL WHERE id = ?`, id)
		return err
	}
	_, err := q.db.Exec(`UPDATE memories SET archived_at = ? WHERE id = ?`, t.Format("2006-01-02 15:04:05"), id)
	return err
}

func (q *MemoryQ) DeleteArchived() (int, error) {
	res, err := q.db.Exec(
		`DELETE FROM memories WHERE archived_at IS NOT NULL AND archived_at < datetime('now', '-1 day')`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (m *Memory) GetEmbedding() []float32 { return m.Embedding }
func (m *Memory) GetEmbedModel() string   { return m.EmbedModel }
func (m *Memory) GetImportance() float64  { return m.Importance }
func (m *Memory) GetUpdatedAt() time.Time { return m.UpdatedAt }

func (q *MemoryQ) SetImportance(id int64, importance float64) error {
	_, err := q.db.Exec(`UPDATE memories SET importance=?, updated_at=unixepoch() WHERE id=?`, importance, id)
	return err
}

// decayImportance returns effective importance after linear time decay.
// Decays from base to base*0.6 over 180 days, minimum base*0.6.
func decayImportance(base float64, updatedAt time.Time) float64 {
	days := time.Since(updatedAt).Hours() / 24
	decay := 1.0 - (days/180.0)*0.4
	if decay < 0.6 {
		decay = 0.6
	}
	return base * decay
}

// EncodeEmbedding is the exported version for cross-package tests.
func EncodeEmbedding(v []float32) []byte { return encodeEmbedding(v) }

// encodeEmbedding serializes float32 slice to little-endian bytes.
func encodeEmbedding(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func decodeEmbedding(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// Cosine returns the cosine similarity between two float32 vectors.
// Returns 0 if the vectors have different lengths or either has zero norm.
func Cosine(a, b []float32) float32 { return cosine(a, b) }

func cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}
