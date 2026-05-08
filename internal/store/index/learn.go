package index

// learn.go — schema-backed query layer for the learn-about feature:
// per-project namespaced indexed files with small-model summaries plus
// summary embeddings. Distinct from CodeIndex (one big embedding pool, no
// summaries); learn is intentional, project-named, summary-first retrieval.

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Project is one indexed codebase / docs root the user has asked cairo to
// "learn about". Projects own their indexed files via cascade delete.
type Project struct {
	Name        string
	RootPath    string
	Description string
	FileCount   int
	IndexedAt   time.Time
	LastUpdated time.Time
}

// IndexedFile is one file inside a project's index. Summary is a small-model
// distillation; embedding is over the augmented summary (filename + project
// description + summary) for richer retrieval than raw filename matching.
type IndexedFile struct {
	ID         int64
	Project    string
	RelPath    string
	FileType   string
	Bytes      int
	SHA256     string
	Summary    string
	Embedding  []float32
	EmbedModel string
	IndexedAt  time.Time
}

// --- ProjectQ ---

type ProjectQ struct{ db *sql.DB }

func NewProjectQ(db *sql.DB) *ProjectQ { return &ProjectQ{db: db} }

// Upsert inserts a project or updates root_path/description. Does not touch
// file_count — that's maintained by IndexedFileQ on insert/delete.
func (q *ProjectQ) Upsert(name, rootPath, description string) error {
	_, err := q.db.Exec(
		`INSERT INTO projects(name, root_path, description) VALUES(?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   root_path = excluded.root_path,
		   description = CASE WHEN excluded.description != '' THEN excluded.description ELSE projects.description END,
		   last_updated = unixepoch()`,
		name, rootPath, description)
	return err
}

func (q *ProjectQ) Get(name string) (*Project, error) {
	row := q.db.QueryRow(
		`SELECT name, root_path, description, file_count, indexed_at, last_updated
		 FROM projects WHERE name = ?`, name)
	return scanProject(row)
}

func (q *ProjectQ) List() ([]*Project, error) {
	rows, err := q.db.Query(
		`SELECT name, root_path, description, file_count, indexed_at, last_updated
		 FROM projects ORDER BY last_updated DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetDescription updates only the description. Used after the indexer
// generates one from accumulated file summaries.
func (q *ProjectQ) SetDescription(name, description string) error {
	_, err := q.db.Exec(
		`UPDATE projects SET description = ?, last_updated = unixepoch() WHERE name = ?`,
		description, name)
	return err
}

// Delete removes a project and (via CASCADE) all its indexed files.
func (q *ProjectQ) Delete(name string) error {
	_, err := q.db.Exec(`DELETE FROM projects WHERE name = ?`, name)
	return err
}

// RecountFiles rewrites file_count from the indexed_files table. Called by
// the indexer after batch operations rather than on every single upsert.
func (q *ProjectQ) RecountFiles(name string) error {
	_, err := q.db.Exec(
		`UPDATE projects
		 SET file_count = (SELECT COUNT(*) FROM indexed_files WHERE project = ?),
		     last_updated = unixepoch()
		 WHERE name = ?`, name, name)
	return err
}

func scanProject(row scanner) (*Project, error) {
	var p Project
	var indexedAt, lastUpdated int64
	if err := row.Scan(&p.Name, &p.RootPath, &p.Description, &p.FileCount, &indexedAt, &lastUpdated); err != nil {
		return nil, err
	}
	p.IndexedAt = time.Unix(indexedAt, 0)
	p.LastUpdated = time.Unix(lastUpdated, 0)
	return &p, nil
}

// --- IndexedFileQ ---

type IndexedFileQ struct{ db *sql.DB }

func NewIndexedFileQ(db *sql.DB) *IndexedFileQ { return &IndexedFileQ{db: db} }

func (q *IndexedFileQ) Upsert(f *IndexedFile) (int64, error) {
	_, err := q.db.Exec(
		`INSERT INTO indexed_files(
		    project, rel_path, file_type, bytes, sha256, summary, embedding, embed_model
		 ) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project, rel_path) DO UPDATE SET
		    file_type   = excluded.file_type,
		    bytes       = excluded.bytes,
		    sha256      = excluded.sha256,
		    summary     = excluded.summary,
		    embedding   = excluded.embedding,
		    embed_model = excluded.embed_model,
		    indexed_at  = unixepoch()`,
		f.Project, f.RelPath, f.FileType, f.Bytes, f.SHA256, f.Summary,
		encodeEmbedding(f.Embedding), f.EmbedModel)
	if err != nil {
		return 0, err
	}

	// ON CONFLICT DO UPDATE does not create a new row, so LastInsertId()
	// is unreliable. SELECT the id to cover both paths.
	var id int64
	if err := q.db.QueryRow(
		`SELECT id FROM indexed_files WHERE project = ? AND rel_path = ?`,
		f.Project, f.RelPath,
	).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// GetSHA returns the stored SHA for (project, rel_path), or "" if absent.
// Used by the indexer to skip unchanged files on refresh.
func (q *IndexedFileQ) GetSHA(project, relPath string) (string, error) {
	var sha string
	err := q.db.QueryRow(
		`SELECT sha256 FROM indexed_files WHERE project = ? AND rel_path = ?`,
		project, relPath).Scan(&sha)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return sha, err
}

// ForProject lists every indexed file in a project, oldest first.
func (q *IndexedFileQ) ForProject(project string) ([]*IndexedFile, error) {
	rows, err := q.db.Query(
		`SELECT id, project, rel_path, file_type, bytes, sha256, summary,
		        embedding, embed_model, indexed_at
		 FROM indexed_files WHERE project = ? ORDER BY rel_path ASC`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIndexedFiles(rows)
}

// SearchSummaries scores every file in project by cosine similarity of the
// query vector against the stored summary embedding, returns top-k.
// queryModel must match the file's embed_model — mismatches are skipped
// silently so a model swap doesn't poison results.
func (q *IndexedFileQ) SearchSummaries(project string, queryVec []float32, queryModel string, k int) ([]*IndexedFile, []float32, error) {
	if len(queryVec) == 0 || queryModel == "" {
		return nil, nil, fmt.Errorf("query vector and model required")
	}
	files, err := q.ForProject(project)
	if err != nil {
		return nil, nil, err
	}

	type scored struct {
		f     *IndexedFile
		score float32
	}
	cand := make([]scored, 0, len(files))
	for _, f := range files {
		if f.EmbedModel != queryModel || len(f.Embedding) != len(queryVec) {
			continue
		}
		cand = append(cand, scored{f, cosine(queryVec, f.Embedding)})
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].score > cand[j].score })
	if k > len(cand) {
		k = len(cand)
	}
	out := make([]*IndexedFile, k)
	scores := make([]float32, k)
	for i := range out {
		out[i] = cand[i].f
		scores[i] = cand[i].score
	}
	return out, scores, nil
}

// Delete removes one file row from a project's index.
func (q *IndexedFileQ) Delete(project, relPath string) error {
	_, err := q.db.Exec(
		`DELETE FROM indexed_files WHERE project = ? AND rel_path = ?`,
		project, relPath)
	return err
}

// DeleteMissing removes indexed_files rows for the given project whose
// rel_path is NOT in the present slice. Chunks are removed automatically
// via ON DELETE CASCADE on indexed_chunks.file_id.
//
// present is the set of rel-paths discovered by the current walk. A file
// excluded by a new .gitignore entry is treated as absent and removed —
// excluded files should not appear in retrieval results.
//
// Returns the number of rows deleted.
func (q *IndexedFileQ) DeleteMissing(project string, present []string) (int, error) {
	if len(present) == 0 {
		// No files walked — delete everything for the project.
		res, err := q.db.Exec(
			`DELETE FROM indexed_files WHERE project = ?`, project)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		return int(n), nil
	}

	// Build a parameterised NOT IN list.
	placeholders := make([]string, len(present))
	args := make([]any, 0, len(present)+1)
	args = append(args, project)
	for i, p := range present {
		placeholders[i] = "?"
		args = append(args, p)
	}
	query := "DELETE FROM indexed_files WHERE project = ? AND rel_path NOT IN (" +
		strings.Join(placeholders, ",") + ")"
	res, err := q.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanIndexedFiles(rows *sql.Rows) ([]*IndexedFile, error) {
	var out []*IndexedFile
	for rows.Next() {
		var f IndexedFile
		var indexedAt int64
		var emb []byte
		if err := rows.Scan(
			&f.ID, &f.Project, &f.RelPath, &f.FileType, &f.Bytes, &f.SHA256,
			&f.Summary, &emb, &f.EmbedModel, &indexedAt,
		); err != nil {
			return nil, err
		}
		f.Embedding = decodeEmbedding(emb)
		f.IndexedAt = time.Unix(indexedAt, 0)
		out = append(out, &f)
	}
	return out, rows.Err()
}
