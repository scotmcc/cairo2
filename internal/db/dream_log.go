package db

import (
	"database/sql"
	"time"
)

// DreamLogEntry is one audit record written by a dream-pass role.
type DreamLogEntry struct {
	ID          int64
	DreamID     int64
	CreatedAt   time.Time
	Action      string
	TargetTable string
	TargetIDs   string // JSON array of affected row IDs
	Note        string
}

// DreamLogQ owns all queries against the dream_log table.
type DreamLogQ struct{ db *sql.DB }

// Add inserts a dream_log entry and returns nil on success.
func (q *DreamLogQ) Add(dreamID int64, action, targetTable, targetIDs, note string) error {
	_, err := q.db.Exec(
		`INSERT INTO dream_log(dream_id, action, target_table, target_ids, note, created_at)
		 VALUES(?, ?, ?, ?, ?, datetime('now'))`,
		dreamID, action, targetTable, targetIDs, note)
	return err
}

// List returns all dream_log entries for the given dream ID, oldest first.
func (q *DreamLogQ) List(dreamID int64) ([]*DreamLogEntry, error) {
	rows, err := q.db.Query(
		`SELECT id, dream_id, created_at, action, target_table, target_ids, note
		 FROM dream_log WHERE dream_id = ? ORDER BY id ASC`, dreamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DreamLogEntry
	for rows.Next() {
		e, err := scanDreamLogEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanDreamLogEntry(row scanner) (*DreamLogEntry, error) {
	var e DreamLogEntry
	var createdAt string
	err := row.Scan(
		&e.ID, &e.DreamID, &createdAt,
		&e.Action, &e.TargetTable, &e.TargetIDs, &e.Note,
	)
	if err != nil {
		return nil, err
	}
	if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
		e.CreatedAt = t
	}
	return &e, nil
}
