package db

import (
	"database/sql"
	"errors"
	"os"
	"time"
)

// Dream is one completed dream maintenance session.
type Dream struct {
	ID            int64
	CreatedAt     time.Time
	Date          string
	NarrativePath string
	Themes        string
	Mood          string
	StateDailyRef *string // date of the state_daily row this dream was processed against
	LastEditedAt  *time.Time
}

// DreamQ owns all queries against the dreams table.
type DreamQ struct{ db *sql.DB }

// Add inserts a new dream record for date and returns its ID.
func (q *DreamQ) Add(date, narrativePath, themes, mood string, stateDailyRef *string) (int64, error) {
	res, err := q.db.Exec(
		`INSERT INTO dreams(date, narrative_path, themes, mood, state_daily_ref, created_at)
		 VALUES(?, ?, ?, ?, ?, datetime('now'))`,
		date, narrativePath, themes, mood, stateDailyRef)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// List returns the most recent limit dream records, newest first.
func (q *DreamQ) List(limit int) ([]*Dream, error) {
	rows, err := q.db.Query(
		`SELECT id, created_at, date, narrative_path, themes, mood, state_daily_ref, last_edited_at
		 FROM dreams ORDER BY date DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Dream
	for rows.Next() {
		d, err := scanDream(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetByDate returns the dream record for the given date string (YYYY-MM-DD), or nil if none.
func (q *DreamQ) GetByDate(date string) (*Dream, error) {
	row := q.db.QueryRow(
		`SELECT id, created_at, date, narrative_path, themes, mood, state_daily_ref, last_edited_at
		 FROM dreams WHERE date = ?`, date)
	d, err := scanDream(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

// UpdateMetadata sets narrative_path, themes, and mood for a dream row in a
// single UPDATE. Called by RunDreamer after the narrative file is written.
func (q *DreamQ) UpdateMetadata(id int64, narrativePath, themes, mood string) error {
	_, err := q.db.Exec(
		`UPDATE dreams SET narrative_path = ?, themes = ?, mood = ?, last_edited_at = datetime('now')
		 WHERE id = ?`,
		narrativePath, themes, mood, id)
	return err
}

// SetLastEdited updates last_edited_at to now for the given dream ID.
func (q *DreamQ) SetLastEdited(id int64) error {
	_, err := q.db.Exec(
		`UPDATE dreams SET last_edited_at = datetime('now') WHERE id = ?`, id)
	return err
}

// Delete removes the dream record, its dream_log entries, and the narrative
// file on disk. A missing file is not treated as an error. Returns an error
// only if the DB deletes fail.
func (q *DreamQ) Delete(id int64) error {
	// Fetch narrative_path before deleting the row.
	var narrativePath string
	_ = q.db.QueryRow(`SELECT narrative_path FROM dreams WHERE id = ?`, id).Scan(&narrativePath)

	if err := removeNarrativeFile(narrativePath); err != nil {
		return err
	}

	if _, err := q.db.Exec(`DELETE FROM dream_log WHERE dream_id = ?`, id); err != nil {
		return err
	}
	_, err := q.db.Exec(`DELETE FROM dreams WHERE id = ?`, id)
	return err
}

// removeNarrativeFile deletes the narrative file at path, ignoring not-found errors.
// Returns an error only for unexpected failures (permissions, etc.).
func removeNarrativeFile(path string) error {
	if path == "" || path == "<pending>" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func scanDream(row scanner) (*Dream, error) {
	var d Dream
	var createdAt string
	var lastEditedAt sql.NullTime
	err := row.Scan(
		&d.ID, &createdAt, &d.Date, &d.NarrativePath,
		&d.Themes, &d.Mood, &d.StateDailyRef, &lastEditedAt,
	)
	if err != nil {
		return nil, err
	}
	if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
		d.CreatedAt = t
	}
	if lastEditedAt.Valid {
		t := lastEditedAt.Time
		d.LastEditedAt = &t
	}
	return &d, nil
}
