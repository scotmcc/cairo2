package db

import (
	"database/sql"
	"fmt"
	"sort"
)

// IndexedChunk is one semantic unit within an indexed file.
type IndexedChunk struct {
	ID         int64
	FileID     int64
	StartLine  int
	Length     int
	Content    string
	Label      string // "method", "type", "paragraph", "section-h2", etc.
	Name       string // method/class name or heading (nullable)
	Embedding  []float32
	EmbedModel string
}

// ChunkSearchResult groups chunks by file, with the best chunk score for ranking.
type ChunkSearchResult struct {
	RelPath     string
	FileType    string
	FileSummary string
	Chunks      []*IndexedChunk
	Score       float32 // best chunk score for this file
}

// ChunkQ holds query methods for the indexed_chunks table.
type ChunkQ struct{ db *sql.DB }

// Upsert inserts a chunk for a given file. No conflict handling needed — we
// delete old chunks before inserting new ones.
func (q *ChunkQ) Upsert(fileID int64, label, name, content string, startLine, length int, embedding []float32, embedModel string) (int64, error) {
	res, err := q.db.Exec(
		`INSERT INTO indexed_chunks(
		    file_id, start_line, length, content, label, name, embedding, embed_model
		 ) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		fileID, startLine, length, content, label, name,
		encodeEmbedding(embedding), embedModel,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// DeleteForFile removes all chunks belonging to a file.
func (q *ChunkQ) DeleteForFile(fileID int64) error {
	_, err := q.db.Exec(`DELETE FROM indexed_chunks WHERE file_id = ?`, fileID)
	return err
}

// Search scores every chunk in a project against queryVec using cosine similarity,
// groups by file, keeps the best score per file, sorts by score, and returns top-k.
// queryModel must match the chunk's embed_model — mismatches are skipped silently.
func (q *ChunkQ) Search(project string, queryVec []float32, queryModel string, k int) ([]*ChunkSearchResult, []float32, error) {
	if len(queryVec) == 0 || queryModel == "" {
		return nil, nil, fmt.Errorf("query vector and model required")
	}

	// Load all chunks for the project, joining indexed_files for metadata.
	rows, err := q.db.Query(`
		SELECT c.id, c.file_id, c.start_line, c.length, c.content, c.label, c.name,
		       c.embedding, c.embed_model,
		       f.rel_path, f.file_type, f.summary
		  FROM indexed_chunks c
		  JOIN indexed_files f ON c.file_id = f.id
		 WHERE f.project = ?
		 ORDER BY c.file_id, c.start_line`, project)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	// Collect all valid chunks (matching model and dimension).
	type chunkFileMeta struct {
		relPath     string
		fileType    string
		fileSummary string
	}
	// All chunks indexed by fileID.
	fileChunks := make(map[int64][]*IndexedChunk)
	fileMeta := make(map[int64]chunkFileMeta)
	// Best chunk per file (for scoring).
	bestChunk := make(map[int64]*IndexedChunk)

	for rows.Next() {
		var c IndexedChunk
		var embBlob []byte
		var relPath, fileType, summary string
		if err := rows.Scan(
			&c.ID, &c.FileID, &c.StartLine, &c.Length, &c.Content,
			&c.Label, &c.Name, &embBlob, &c.EmbedModel,
			&relPath, &fileType, &summary,
		); err != nil {
			return nil, nil, err
		}
		c.Embedding = decodeEmbedding(embBlob)

		// Skip chunks from a different model or different dimension.
		if c.EmbedModel != queryModel || len(c.Embedding) != len(queryVec) {
			continue
		}
		score := cosine(queryVec, c.Embedding)

		meta := chunkFileMeta{relPath: relPath, fileType: fileType, fileSummary: summary}
		fileMeta[c.FileID] = meta

		fileChunks[c.FileID] = append(fileChunks[c.FileID], &c)

		// Track the best-scoring chunk per file.
		existing, ok := bestChunk[c.FileID]
		if !ok || score > cosine(queryVec, existing.Embedding) {
			bestChunk[c.FileID] = &c
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// Build scored file results.
	type scoredFile struct {
		result *ChunkSearchResult
		score  float32
	}
	var files []scoredFile
	for fileID, c := range bestChunk {
		score := cosine(queryVec, c.Embedding)
		meta := fileMeta[fileID]
		result := &ChunkSearchResult{
			RelPath:     meta.relPath,
			FileType:    meta.fileType,
			FileSummary: meta.fileSummary,
			Chunks:      fileChunks[fileID],
			Score:       score,
		}
		files = append(files, scoredFile{result, score})
	}

	// Sort by score descending.
	sort.Slice(files, func(i, j int) bool { return files[i].score > files[j].score })

	if k > len(files) {
		k = len(files)
	}

	out := make([]*ChunkSearchResult, k)
	scores := make([]float32, k)
	for i := range out {
		out[i] = files[i].result
		scores[i] = files[i].score
	}
	return out, scores, nil
}
