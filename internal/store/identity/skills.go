package identity

import (
	"database/sql"
	"log"
	"time"
)

type Skill struct {
	ID          int64
	Name        string
	Description string
	Content     string
	Tags        string // JSON array
	Embedding   []float32
	EmbedModel  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type SkillQ struct{ db *sql.DB }

func NewSkillQ(db *sql.DB) *SkillQ { return &SkillQ{db: db} }

func (q *SkillQ) Get(name string) (*Skill, error) {
	row := q.db.QueryRow(
		`SELECT id, name, description, content, tags, embed_model, created_at, updated_at FROM skills WHERE name = ?`, name)
	return scanSkill(row)
}

func (q *SkillQ) List() ([]*Skill, error) {
	rows, err := q.db.Query(
		`SELECT id, name, description, content, tags, embed_model, created_at, updated_at FROM skills ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AllWithEmbedding returns all skills including their embedding BLOBs.
// Used for semantic search — skips this for normal List() to avoid decode cost.
func (q *SkillQ) AllWithEmbedding() ([]*Skill, error) {
	rows, err := q.db.Query(
		`SELECT id, name, description, content, tags, embed_model, embedding, created_at, updated_at FROM skills ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Skill
	for rows.Next() {
		s, err := scanSkillWithEmbedding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (q *SkillQ) Create(name, description, content, tags, embedModel string, embedding []float32) error {
	_, err := q.db.Exec(
		`INSERT INTO skills(name, description, content, tags, embed_model, embedding) VALUES(?, ?, ?, ?, ?, ?)`,
		name, description, content, tags, embedModel, encodeEmbedding(embedding))
	return err
}

func (q *SkillQ) Update(name, content, embedModel string, embedding []float32) error {
	_, err := q.db.Exec(
		`UPDATE skills SET content = ?, embed_model = ?, embedding = ?, updated_at = unixepoch() WHERE name = ?`,
		content, embedModel, encodeEmbedding(embedding), name)
	return err
}

func (q *SkillQ) Delete(name string) error {
	_, err := q.db.Exec(`DELETE FROM skills WHERE name = ?`, name)
	return err
}

// Count returns the total number of skills.
func (q *SkillQ) Count() (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM skills`).Scan(&n)
	return n, err
}

// Search returns the top-k skills by cosine similarity to the query embedding.
// Skips rows whose embed_model differs from queryModel.
func (q *SkillQ) Search(query []float32, queryModel string, k int) ([]*Skill, error) {
	all, err := q.AllWithEmbedding()
	if err != nil {
		return nil, err
	}

	type scored struct {
		s     *Skill
		score float32
	}
	var candidates []scored
	var skippedModel int
	for _, s := range all {
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
		log.Printf("skill_search: skipped %d rows with different embed_model (query model: %q)", skippedModel, queryModel)
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
	out := make([]*Skill, k)
	for i := range out {
		out[i] = candidates[i].s
	}
	return out, nil
}

// SearchFTS returns skills matching the FTS5 query (name + description + content), ordered by rank.
// query supports FTS5 syntax: phrases ("exact phrase"), AND/OR, prefix (word*), etc.
func (q *SkillQ) SearchFTS(query string, limit int) ([]*Skill, error) {
	rows, err := q.db.Query(
		`SELECT s.id, s.name, s.description, s.content, s.tags, s.embed_model, s.created_at, s.updated_at
		 FROM skills s
		 JOIN skills_fts fts ON s.id = fts.rowid
		 WHERE skills_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanSkill(row scanner) (*Skill, error) {
	var s Skill
	var createdAt, updatedAt int64
	err := row.Scan(&s.ID, &s.Name, &s.Description, &s.Content, &s.Tags, &s.EmbedModel, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	s.CreatedAt = time.Unix(createdAt, 0)
	s.UpdatedAt = time.Unix(updatedAt, 0)
	return &s, nil
}

func scanSkillWithEmbedding(row scanner) (*Skill, error) {
	var s Skill
	var createdAt, updatedAt int64
	var embBlob []byte
	err := row.Scan(&s.ID, &s.Name, &s.Description, &s.Content, &s.Tags, &s.EmbedModel, &embBlob, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	s.Embedding = decodeEmbedding(embBlob)
	s.CreatedAt = time.Unix(createdAt, 0)
	s.UpdatedAt = time.Unix(updatedAt, 0)
	return &s, nil
}

func (s *Skill) GetEmbedding() []float32 { return s.Embedding }
func (s *Skill) GetEmbedModel() string   { return s.EmbedModel }
func (s *Skill) GetImportance() float64  { return 0.5 }
func (s *Skill) GetUpdatedAt() time.Time { return s.UpdatedAt }

func (q *SkillQ) UpdateEmbeddingOnly(id int64, embedModel string, embedding []float32) error {
	_, err := q.db.Exec(
		`UPDATE skills SET embed_model=?, embedding=?, updated_at=unixepoch() WHERE id=?`,
		embedModel, encodeEmbedding(embedding), id)
	return err
}

// All returns all skills including their embedding BLOBs. Skills always require
// their embeddings for semantic search, so there is no separate listing variant.
func (q *SkillQ) All() ([]*Skill, error) {
	return q.AllWithEmbedding()
}
