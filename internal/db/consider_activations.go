package db

import (
	"database/sql"
	"strings"
)

// ConsiderActivation is one row in the consider_activations table — a single
// aspect-fire from a consider step, including alignment=0 fires (staying quiet
// is signal too). message_id back-fills to the user message whose inner_voice
// summary this activation contributed to.
type ConsiderActivation struct {
	ID            int64
	SessionID     int64
	MessageID     sql.NullInt64
	AspectName    string
	Alignment     float64
	Thought       string
	Question      string
	LatencyMs     int64
	TriggerSource string
}

// ConsiderActivationQ provides query methods for the consider_activations table.
type ConsiderActivationQ struct{ db *sql.DB }

// Insert writes one aspect activation and returns its row id. Called once per
// aspect per consider fire — the caller collects ids and back-fills message_id
// after the user message is persisted.
func (q *ConsiderActivationQ) Insert(sessionID int64, aspect string, alignment float64,
	thought, question string, latencyMs int64, triggerSource string) (int64, error) {
	if triggerSource == "" {
		triggerSource = "tui"
	}
	res, err := q.db.Exec(
		`INSERT INTO consider_activations(session_id, aspect_name, alignment, thought, question, latency_ms, trigger_source)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		sessionID, aspect, alignment, thought, question, latencyMs, triggerSource)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LinkToMessage stamps message_id on the given activation rows. Used by the
// consider runner to attach a turn's activations to the user message whose
// inner_voice summary they produced. No-op when ids is empty.
func (q *ConsiderActivationQ) LinkToMessage(ids []int64, messageID int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, messageID)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	_, err := q.db.Exec(
		`UPDATE consider_activations SET message_id = ? WHERE id IN (`+
			strings.Join(placeholders, ",")+`)`, args...)
	return err
}
