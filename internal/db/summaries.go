package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

type Summary struct {
	ID            int64
	SessionID     int64
	Content       string
	Embedding     []float32
	EmbedModel    string
	CoversFrom    int64 // first message ID covered
	CoversThrough int64 // last message ID covered
	CreatedAt     time.Time
	ReviewedAt    *time.Time // nil = unreviewed; non-nil = when last reviewed by dream-pass curator
}

type Fact struct {
	ID         int64
	SessionID  int64
	SummaryID  *int64
	Content    string
	Embedding  []float32
	EmbedModel string
	Importance float64
	CreatedAt  time.Time
	ArchivedAt *time.Time // nil = not archived; non-nil = when archived (curator lifecycle)
	ReviewedAt *time.Time // nil = unreviewed; non-nil = when last reviewed by dream-pass curator
}

type SummaryQ struct{ db *sql.DB }
type FactQ struct{ db *sql.DB }

// --- Summaries ---

func (q *SummaryQ) Add(sessionID, coversFrom, coversThrough int64, content, embedModel string, embedding []float32) (*Summary, error) {
	res, err := q.db.Exec(
		`INSERT INTO summaries(session_id, content, embed_model, embedding, covers_from, covers_through)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		sessionID, content, embedModel, encodeEmbedding(embedding), coversFrom, coversThrough)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.Get(id)
}

func (q *SummaryQ) Get(id int64) (*Summary, error) {
	row := q.db.QueryRow(
		`SELECT id, session_id, content, embed_model, embedding, covers_from, covers_through, created_at, reviewed_at
		 FROM summaries WHERE id = ?`, id)
	return scanSummary(row)
}

// LatestForSession returns the most recent n summaries for a session.
// Use these for the context window — most recent = most relevant.
func (q *SummaryQ) LatestForSession(sessionID int64, n int) ([]*Summary, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, content, embed_model, embedding, covers_from, covers_through, created_at, reviewed_at
		 FROM summaries WHERE session_id = ?
		 ORDER BY created_at DESC LIMIT ?`, sessionID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Summary
	for rows.Next() {
		s, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	// Reverse so chronological order is preserved for the LLM
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// Search finds the top-k summaries by semantic similarity across ALL sessions.
// Skips rows whose embed_model differs from queryModel.
func (q *SummaryQ) Search(query []float32, queryModel string, k int) ([]*Summary, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, content, embed_model, embedding, covers_from, covers_through, created_at, reviewed_at
		 FROM summaries WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		s     *Summary
		score float32
	}
	var candidates []scored
	var skippedModel int
	for rows.Next() {
		s, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		if len(s.Embedding) == 0 {
			continue
		}
		if s.EmbedModel != queryModel {
			skippedModel++
			continue
		}
		if len(s.Embedding) != len(query) {
			skippedModel++
			continue
		}
		candidates = append(candidates, scored{s, cosine(query, s.Embedding)})
	}
	if skippedModel > 0 {
		log.Printf("summary_search: skipped %d rows with different embed_model (query model: %q)", skippedModel, queryModel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := 0; i < k && i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	if k > len(candidates) {
		k = len(candidates)
	}
	out := make([]*Summary, k)
	for i := range out {
		out[i] = candidates[i].s
	}
	return out, nil
}

// CountBySession returns the number of summaries recorded for a session.
// Useful for programmatic verification that the summarizer is running.
func (q *SummaryQ) CountBySession(sessionID int64) (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM summaries WHERE session_id = ?`, sessionID).Scan(&n)
	return n, err
}

func (q *SummaryQ) All() ([]*Summary, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, content, embed_model, embedding, covers_from, covers_through, created_at, reviewed_at
		 FROM summaries ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Summary
	for rows.Next() {
		s, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (q *SummaryQ) Update(id int64, content, embedModel string, embedding []float32) error {
	_, err := q.db.Exec(
		`UPDATE summaries SET content = ?, embed_model = ?, embedding = ? WHERE id = ?`,
		content, embedModel, encodeEmbedding(embedding), id)
	return err
}

// UnreviewedIDsForSessions returns IDs of summaries that are unreviewed and
// belong to any of the given sessions. Used by the dream-pass to stamp
// reviewed_at on the summaries scanned this cycle.
// Returns an empty slice (not an error) when sessionIDs is empty.
func (q *SummaryQ) UnreviewedIDsForSessions(sessionIDs []int64) ([]int64, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	rows, err := q.db.Query(
		`SELECT id FROM summaries WHERE reviewed_at IS NULL AND session_id IN (`+buildPlaceholders(len(sessionIDs))+`) ORDER BY id ASC`,
		int64SliceToAny(sessionIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (q *SummaryQ) MarkReviewed(ids []int64) error {
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
		`UPDATE summaries SET reviewed_at = datetime('now') WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)
	_, err := q.db.Exec(query, args...)
	return err
}

func scanSummary(row scanner) (*Summary, error) {
	var s Summary
	var createdAt int64
	var embBlob []byte
	var reviewedAt sql.NullString
	err := row.Scan(&s.ID, &s.SessionID, &s.Content, &s.EmbedModel, &embBlob, &s.CoversFrom, &s.CoversThrough, &createdAt, &reviewedAt)
	if err != nil {
		return nil, err
	}
	s.Embedding = decodeEmbedding(embBlob)
	s.CreatedAt = time.Unix(createdAt, 0)
	if reviewedAt.Valid {
		s.ReviewedAt = parseTimestamp(reviewedAt.String)
	}
	return &s, nil
}

// --- Facts ---

func (q *FactQ) Add(sessionID, summaryID int64, content, embedModel string, embedding []float32) (*Fact, error) {
	res, err := q.db.Exec(
		`INSERT INTO facts(session_id, summary_id, content, embed_model, embedding) VALUES(?, ?, ?, ?, ?)`,
		sessionID, summaryID, content, embedModel, encodeEmbedding(embedding))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.GetFact(id)
}

// GetFact returns the fact by id, filtering out dream-pass-archived rows.
// Callers that need archived facts should use raw SQL — GetFact's contract
// is "active row by id."
func (q *FactQ) GetFact(id int64) (*Fact, error) {
	row := q.db.QueryRow(
		`SELECT id, session_id, summary_id, content, embed_model, embedding, importance, created_at, archived_at, reviewed_at FROM facts WHERE id = ? AND archived_at IS NULL`, id)
	return scanFact(row)
}

func (q *FactQ) ForSession(sessionID int64) ([]*Fact, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, summary_id, content, embed_model, embedding, importance, created_at, archived_at, reviewed_at
		 FROM facts WHERE session_id = ? ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Fact
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (q *FactQ) All() ([]*Fact, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, summary_id, content, embed_model, embedding, importance, created_at, archived_at, reviewed_at FROM facts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Fact
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (q *FactQ) Delete(id int64) error {
	_, err := q.db.Exec(`DELETE FROM facts WHERE id = ?`, id)
	return err
}

// Search returns the top-k facts by cosine similarity to the query embedding.
// Skips rows whose embed_model differs from queryModel — cross-model cosine
// distances are meaningless and silently wrong. Patterned after MemoryQ.Search.
func (q *FactQ) Search(query []float32, queryModel string, k int) ([]*Fact, error) {
	all, err := q.All()
	if err != nil {
		return nil, err
	}

	type scored struct {
		f     *Fact
		score float32
	}
	var candidates []scored
	var skippedModel int
	for _, f := range all {
		if len(f.Embedding) == 0 {
			continue
		}
		if f.EmbedModel != queryModel {
			skippedModel++
			continue
		}
		if len(f.Embedding) != len(query) {
			skippedModel++
			continue
		}
		candidates = append(candidates, scored{f, cosine(query, f.Embedding) * float32(decayImportance(f.Importance, f.CreatedAt))})
	}
	if skippedModel > 0 {
		log.Printf("fact_search: skipped %d rows with different embed_model (query model: %q)", skippedModel, queryModel)
	}

	// partial sort: bubble top-k to front
	for i := 0; i < k && i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	if k > len(candidates) {
		k = len(candidates)
	}
	out := make([]*Fact, k)
	for i := range out {
		out[i] = candidates[i].f
	}
	return out, nil
}

func (f *Fact) GetEmbedding() []float32 { return f.Embedding }
func (f *Fact) GetEmbedModel() string   { return f.EmbedModel }
func (f *Fact) GetImportance() float64  { return f.Importance }
func (f *Fact) GetUpdatedAt() time.Time { return f.CreatedAt }

func (q *FactQ) UpdateEmbeddingOnly(id int64, embedModel string, embedding []float32) error {
	_, err := q.db.Exec(
		`UPDATE facts SET embed_model=?, embedding=? WHERE id=?`,
		embedModel, encodeEmbedding(embedding), id)
	return err
}

func (q *FactQ) Unreviewed() ([]*Fact, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, summary_id, content, embed_model, NULL as embedding, importance, created_at, archived_at, reviewed_at
		 FROM facts WHERE reviewed_at IS NULL AND archived_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Fact
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UnreviewedWithEmbeddings is like Unreviewed, but loads the embedding BLOB.
// Used by the dream-pass curator for pairwise cosine.
func (q *FactQ) UnreviewedWithEmbeddings() ([]*Fact, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, summary_id, content, embed_model, embedding, importance, created_at, archived_at, reviewed_at
		 FROM facts WHERE reviewed_at IS NULL AND archived_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Fact
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (q *FactQ) MarkReviewed(ids []int64) error {
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
		`UPDATE facts SET reviewed_at = datetime('now') WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)
	_, err := q.db.Exec(query, args...)
	return err
}

func (q *FactQ) SetArchivedAt(id int64, t *time.Time) error {
	if t == nil {
		_, err := q.db.Exec(`UPDATE facts SET archived_at = NULL WHERE id = ?`, id)
		return err
	}
	_, err := q.db.Exec(`UPDATE facts SET archived_at = ? WHERE id = ?`, t.Format("2006-01-02 15:04:05"), id)
	return err
}

func (q *FactQ) DeleteArchived() (int, error) {
	res, err := q.db.Exec(
		`DELETE FROM facts WHERE archived_at IS NOT NULL AND archived_at < datetime('now', '-1 day')`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func scanFact(row scanner) (*Fact, error) {
	var f Fact
	var createdAt int64
	var embBlob []byte
	var sid sql.NullInt64
	var archivedAt, reviewedAt sql.NullString
	err := row.Scan(&f.ID, &f.SessionID, &sid, &f.Content, &f.EmbedModel, &embBlob, &f.Importance, &createdAt, &archivedAt, &reviewedAt)
	if err != nil {
		return nil, err
	}
	if sid.Valid {
		f.SummaryID = &sid.Int64
	}
	f.Embedding = decodeEmbedding(embBlob)
	f.CreatedAt = time.Unix(createdAt, 0)
	if archivedAt.Valid {
		f.ArchivedAt = parseTimestamp(archivedAt.String)
	}
	if reviewedAt.Valid {
		f.ReviewedAt = parseTimestamp(reviewedAt.String)
	}
	return &f, nil
}
